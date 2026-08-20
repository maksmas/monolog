# Expose monolog as a Claude Code Skill (+ `monolog search`)

## Overview

Give Claude Code a first-class way to capture into and read from the monolog backlog. Claude can already shell out to the CLI, so the skill adds two things it lacks: **discoverability** (Claude in an unrelated repo doesn't know monolog exists) and **trigger discipline** (when filing is appropriate and what must never be touched).

The primary value is proactive capture — Claude noticing follow-up work it will not do in the current session and filing it, instead of mentioning it in chat where it scrolls away. Explicit asks ("add this to my backlog") and on-demand reads are secondary. Claude already manages plans well without monolog; this is deliberately **not** a plan-tracking integration.

Spam is the central risk, controlled by a quarantine convention: unprompted writes land as `--tags claude -s someday`, out of the today/week views, drained later via `monolog ls -a --tag claude -s someday`.

The skill's dedupe step needs a cheap, precise, untruncated lookup that does not exist today — hence deliverable 1.

> Note on naming: the binary, all repo documentation, and the shipped skill use `monolog`. `mlog` is a machine-local shell alias (`~/.zshrc:8`) and appears nowhere in the deliverables. Both `mlog` and `monolog` are aliased on the author's machine, so the generic shipped skill installs and runs unmodified there — there is deliberately **one** skill file, not a personal variant plus a public one.

## Context (from discovery)

- **Files/components involved:**
  - `internal/tui/search_match.go` — the fuzzy ranker (`rankSearch`, `searchDoc`, `searchResult`, plus `rankAgg`, `titleWeight`, `truncate`); pure, TUI-private
  - `internal/tui/search.go` — 7 `haystack`/rank references: `:51` (open), `:104` (keystroke rank), `:156-160` (commit + bounds check), `:258` (count), `:433`, `:530`, `:551`
  - `internal/tui/search.go:466` — `highlightMatches`, which **stays in `tui`** and consumes ranker offsets
  - `internal/tui/model.go:106-113` — `searchState` struct holding `haystack`/`titles`/`bodies` as 3 parallel fields
  - `internal/tui/model_test.go` — **10 occurrences across 7 sites** of `m.search.haystack` (`:8188`, `:8189`, `:8214`, `:8215`, `:8275`, `:8491`, `:8492`, `:8540`, `:9074`, `:9101`)
  - `internal/display/table.go` — `FormatTasks` (truncates at `titleColWidth = 40`), unexported `padRight` / `truncatePad`
  - `cmd/root.go` — `NewRootCmd` command registration
  - `cmd/ls.go:101-105`, `cmd/ls_test.go` — existing flag semantics and the test pattern to follow
- **Measured motivation:** 219 tasks on disk, 30 open. `monolog ls -a -f` = 298 lines / 14.2 KB (~3.5k tokens) — far too expensive per write. `monolog ls -a` = 31 lines / 2.9 KB but truncates titles at 40 runes (observed: `"jean: implement Date Impact Assesment m…"`).
- **Key patterns:** every mutation auto-commits (`git.AutoCommit`); `withPager` is TTY-guarded so `-f` never spawns `less` in a tool shell; identifiers resolve by ULID prefix *or* title initials via `store.Resolve`; doc comments in this codebase are dense and treated as part of the code.
- **Verified:** bare `monolog` without a TTY fails fast (`could not open a new TTY`) — it does not hang.

## Development Approach

- **Testing approach**: Regular (code first, tests after)
- Complete each task fully before moving to the next
- All tests must pass before starting the next task
- Each task gates on its own package for speed; **Task 6 runs the full `go build ./... && go test ./... && go vet ./...`**

## Testing Strategy

- **Unit tests**: `internal/search/rank_test.go` (migrated ranker tests, rewritten for the `Index` API), `internal/display/table_test.go` (new formatter), `cmd/search_test.go` (command behavior, following `cmd/ls_test.go`)
- **Cross-package coupling**: one TUI-side test must survive the migration asserting that `search.Index` offsets still feed `highlightMatches` correctly (see Task 2). Without it, `internal/search` and `internal/tui` could each pass in isolation while disagreeing at the boundary.
- **Regression**: `internal/tui/model_test.go` must keep passing after the `searchState` migration — overlay behavior is unchanged, only its internal plumbing moves
- **No e2e suite** in this project
- **Shipped-artifact test**: because the skill is now checked in, it gets a real test following `cmd/telegram_deploy_test.go`'s `TestDeployArtifactsExist` pattern (repo root resolved via `runtime.Caller(0)`). Beyond existence, it cross-checks every `monolog <subcommand>` and `--flag` the skill documents against the live cobra tree — so renaming a flag breaks the build instead of silently rotting the skill.

## Solution Overview

**Deliverable 1 — `monolog search`.** Promote the TUI-private ranker into `internal/search` behind an `Index` type, then expose it as a CLI command. `Index` is not ceremony: the current `rankSearch(query, docs, titles, bodies, limit)` takes three parallel slices *purely* so the TUI can cache them across keystrokes, and the TUI mirrors that with three parallel struct fields. Folding them into one object preserves the caching benefit and simplifies the TUI at the same time. Carrying `Task` on the result (instead of a `docIdx` back-pointer) also removes a bounds check at `search.go:157`.

Duplicating the ranker into `cmd/` was rejected: two rankers drift, and "the TUI found it but the CLI didn't" is exactly the failure that destroys trust in dedupe.

**Deliverable 2 — the skill, bundled and installed.** The skill ships in the repo at `docs/claude-skill/SKILL.md` with an install README, following the shape already used by `docs/raycast/` (artifact + README) and `docs/deploy/` (artifacts + README). It is then installed to `~/.claude/skills/monolog/SKILL.md` by symlink, so the author's copy tracks repo edits and cannot drift from the shipped one.

Being shipped means the file must be **generic**: `monolog` throughout, no dev-binary paths, no assumption of a `mlog` alias. Skill-only, with no `CLAUDE.md` primer — which makes the frontmatter `description` the entire trigger mechanism, so it is specified verbatim below rather than left to the implementer.

## Technical Details

### `internal/search` API

```go
type Result struct {
    Task     model.Task
    Score    int
    TitleHit []int // byte offsets for TUI highlighting; CLI ignores
}

type Index struct { /* tasks, titles, bodies */ }

func NewIndex(tasks []model.Task) *Index
func (ix *Index) Rank(query string, limit int) []Result
func (ix *Index) Len() int
```

`Len()` is required by `search.go:258` (`total := len(m.search.haystack)`).

**Nil contract (must be pinned, not left implicit):** `closeSearch` sets `index = nil`, and two existing tests assert cleared state by length (`model_test.go:8214`, `:8491`). Therefore **`Len()` and `Rank()` must be nil-receiver safe** — `Len()` returns `0`, `Rank()` returns an empty non-nil slice. Without this, Task 2's own test migration panics.

**Semantics to preserve exactly** (regressions here are silent):
- `titleWeight = 2`; score = `max(titleScore*2, bodyScore)`
- `CreatedAt`-descending tie-break, `sort.SliceStable`
- empty query → all docs by `CreatedAt` desc, no highlights
- `limit <= 0` → no truncation (note: the CLI clamps before calling — see below)
- **defensive copy of `m.MatchedIndexes`** — `sahilm/fuzzy` reuses that buffer across matches within one `Find` call; dropping the copy makes every earlier hit show the last match's offsets
- **no pre-lowercased title/body copies** — `sahilm/fuzzy` case-folds natively, and a lowercased copy misaligns `MatchedIndexes` byte offsets for runes whose lowercase form differs in byte length (Turkish `İ`→`i`, `ẞ`→`ß`). This is the highest-risk invariant in the migration: `NewIndex` is exactly the place an implementer would "helpfully" add normalization. Carry `search_match.go:10-14`'s comment across verbatim.
- `Rank` returns a **non-nil empty slice, never nil** (asserted at `search_match_test.go:112`, `:271`)
- body-only hits carry **nil `TitleHit`** (`search_match_test.go:210`)
- the ranker is **status-agnostic** — open/done filtering is the caller's job (`TestRankSearch_DoneTasksIncluded`, `:162`)

**Identifiers that move with the ranker:** `titleWeight` (`:30`), `rankAgg` (`:35`), `truncate` (`:118`). `truncate` has no other caller in `internal/tui` (verified), so deleting it there is safe — but rename it to `truncateResults` in the new package, since bare `truncate` is too generic for a package-level name in a fresh package that also neighbors `display.truncatePad`.

### `display.FormatSearchResults`

Lives in `internal/display` (not `cmd`) so it can use the existing unexported `padRight`, and takes `[]model.Task` rather than `[]search.Result` so `display` gains no dependency on `internal/search`.

```go
func FormatSearchResults(w io.Writer, tasks []model.Task, now time.Time, layout string)
```

- Title is **untruncated** — this is the entire point, since 40-rune truncation is what makes `ls` unfit for dedupe.
- Padded to the widest title in the result set, **capped at 60 columns**. Uncapped padding lets a single 150-rune title widen every row; the cap means over-long titles simply push their own trailing columns right instead of taxing the whole table. Never truncates either way.
- Drops the position column (meaningless across schedules) and the dates column (dedupe doesn't need them).
- 2-char status cell: `"x "` done, `"* "` active, `"  "` otherwise — **done takes precedence** if both somehow hold (`done` auto-deactivates, so it should not occur; pin it in a test regardless).
- Layout: `<status><8-char ID>  <padded title> <schedule> <tags>`
- Empty input → `No matches.` (mirrors `FormatTasks`' `No tasks.`)

### `monolog search`

```
monolog search <query>...        # open tasks, top 10
monolog search <query> -n 25     # --limit
monolog search <query> -d        # --done, include completed
```

- `cobra.MinimumNArgs(1)` with `strings.Join(args, " ")`, so `monolog search fix login` works without quoting — friendlier for an agent caller than `ExactArgs(1)`.
- **`-d/--done`, not `-a/--all`.** `ls -a` already means "all *open* tasks across schedules" and `ls -d` already means "completed" — reusing `-a` for "include done" would give the same letter opposite meanings across two commands the skill documents side by side.
- `--limit` clamps: any value `< 1` falls back to the default 10, so `-n 0` cannot dump all 219 tasks through the ranker's `limit <= 0` = no-truncation path.
- Default open-only matches `ls` semantics and is correct for dedupe: a completed task whose issue recurs *should* be re-filed, not suppressed.

### Skill frontmatter description (verbatim — do not paraphrase)

`name: monolog`, `allowed-tools: Bash`, and:

> Personal backlog capture and lookup via the monolog CLI. Use when the user asks to file or check something in their backlog ("add this to my backlog", "put that in mlog", "what's on my plate", "anything in mlog about X"). ALSO use proactively, without being asked, whenever work is identified that will NOT be done in the current session: tech debt noticed while doing something else, a bug found but not fixed, something the user deferred ("not now", "later", "leave it"), or unfinished deferred items when a plan or session wraps up. Filing is cheap and quarantined — losing the thought is not.

The explicit proactive permission, the quoted trigger phrases, and the closing cost-asymmetry line are all load-bearing. Without a `CLAUDE.md` primer this paragraph is the only thing that fires the skill. Keep the colloquial "mlog" spellings in the trigger phrases even though the commands say `monolog` — users say the short form.

## What Goes Where

**Implementation Steps** — all tasks are in this repo. Task 5 additionally symlinks the shipped skill into `~/.claude/skills/` (a local install step, not a repo change).

**Post-Completion** — manual verification of skill triggering, which can only be observed across future sessions.

## Implementation Steps

### Task 1: Create `internal/search` package with `Index`

**Files:**
- Create: `internal/search/rank.go`
- Create: `internal/search/rank_test.go`

- [x] create `internal/search/rank.go` with `Result`, `Index`, `NewIndex`, `Rank`, `Len`; `NewIndex` builds the titles/bodies slices once
- [x] move `titleWeight`, `rankAgg`, and `truncate` (renamed `truncateResults`) into the new package
- [x] port the ranking body preserving every invariant in the "Semantics to preserve exactly" list above — **especially the no-pre-lowercasing rule**; carry `search_match.go:10-14`'s case-folding comment across verbatim
- [x] make `Len()` and `Rank()` nil-receiver safe (`0` / empty non-nil slice) per the nil contract
- [x] leave `internal/tui/search_match.go` in place for now (Task 2 removes it) so the tree keeps compiling
- [x] port `internal/tui/search_match_test.go` to `internal/search/rank_test.go`, rewriting `docs[r.docIdx].task.ID` assertions as `r.Task.ID`; **skip the two `highlightMatches` cases (`:238`, `:263`)** — they stay in `tui` and are handled in Task 2
- [x] write tests for the multibyte/case-folding cases at the offset level (assert `TitleHit` byte offsets directly, since `highlightMatches` is unavailable here)
- [x] write a test asserting `TitleHit` offsets are correct for the *first* of several title matches (guards the defensive copy)
- [x] write tests for `Len()`, `NewIndex(nil)`, a nil `*Index` receiver, `Rank` returning non-nil on no match, and nil `TitleHit` on a body-only hit
- [x] run `go test ./internal/search/` — must pass before Task 2

### Task 2: Migrate the TUI to `internal/search`

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/search.go`
- Modify: `internal/tui/model_test.go`
- Delete: `internal/tui/search_match.go`
- Delete: `internal/tui/search_match_test.go`

- [x] in `model.go:106-113`, replace `haystack`/`titles`/`bodies` with `index *search.Index` and retype `results` to `[]search.Result`
- [x] `openSearch` (`search.go:36-51`): replace the three-slice build with `m.search.index = search.NewIndex(m.allTasks)` and seed `results` via `m.search.index.Rank("", searchResultLimit)`
- [x] `closeSearch` (`search.go:62-64`): nil out `index` instead of the three slices
- [x] `updateSearch` (`search.go:104`): call `m.search.index.Rank(query, searchResultLimit)`
- [x] `commitSearch` (`search.go:156-160`): read `res.Task` directly and delete the now-unreachable `docIdx` bounds check
- [x] update remaining reads at `search.go:258` (→ `m.search.index.Len()`), `:433`, `:530`, `:551` (→ `res.Task`)
- [x] rewrite the doc comments the migration invalidates: `model.go:101-105` (`searchState`), `search.go:28-33` (`openSearch`), `search.go:55-57` (`closeSearch`), and the "haystack" references in `model_test.go:9053`, `:9078`, `:9110`
- [x] **port the two `highlightMatches` coupling tests into `model_test.go`** (from deleted `search_match_test.go:222` / `:250`): rank a multibyte title through `search.Index`, feed `res.TitleHit` into `highlightMatches`, assert the `ansi.Strip` round-trip. This is the only test that catches `internal/search` and `internal/tui` disagreeing about byte offsets
- [x] delete `internal/tui/search_match.go` and `internal/tui/search_match_test.go`
- [x] update all **10 occurrences across 7 sites** of `m.search.haystack` in `model_test.go` (`:8188`, `:8189`, `:8214`, `:8215`, `:8275`, `:8491`, `:8492`, `:8540`, `:9074`, `:9101`) to use `index.Len()` / `r.Task`; keep every assertion's *intent* unchanged — these are the regression net for the overlay
- [x] confirm `internal/tui` no longer imports `sahilm/fuzzy` (it becomes an `internal/search`-only dependency; Task 7 updates the docs)
- [x] run `go build ./... && go test ./internal/tui/ && go vet ./...` — must pass before Task 3

### Task 3: Add `display.FormatSearchResults`

**Files:**
- Modify: `internal/display/table.go`
- Modify: `internal/display/table_test.go`

- [x] add `FormatSearchResults(w io.Writer, tasks []model.Task, now time.Time, layout string)` computing the max title rune-width, capping the pad at 60 columns, and padding with the existing `padRight`
- [x] render `<status><8-char ID>  <padded title> <schedule> <tags>`; status cell `"x "` done / `"* "` active / `"  "` otherwise; schedule via `schedule.FormatDisplay`; tags via `VisibleTags`
- [x] print `No matches.` for an empty slice
- [x] write tests: title never truncated (include a title longer than 40 runes and assert it appears in full), a >60-rune title does not widen other rows, columns align across mixed title lengths, done-beats-active precedence in the status cell, empty slice message, reserved `active` tag filtered from the tag cell
- [x] write a layout-sensitivity test mirroring `table_test.go:455` (`TestFormatTasks_ISOScheduleRendersInConfiguredLayout`) — stored ISO schedules must render in the configured layout
- [x] run `go test ./internal/display/` — must pass before Task 4

### Task 4: Add the `monolog search` command

**Files:**
- Create: `cmd/search.go`
- Create: `cmd/search_test.go`
- Modify: `cmd/root.go`

- [x] create `newSearchCmd()` with `Use: "search <query>"`, `Args: cobra.MinimumNArgs(1)` joining args with a space, flags `-n/--limit` (default 10, clamp `< 1` to default) and `-d/--done` (include completed)
- [x] load tasks via `store.List` — `ListOptions{Status: "open"}` by default, no status filter when `--done`
- [x] build `search.NewIndex(tasks)`, call `Rank(query, limit)`, map results to `[]model.Task`, print via `display.FormatSearchResults` with `config.DateFormat()`
- [x] register with `rootCmd.AddCommand(newSearchCmd())` in `cmd/root.go`
- [x] write tests following `cmd/ls_test.go`: query matches title, query matches body, title outranks body-only, multi-word unquoted query, `--limit` truncates, `-n 0` falls back to 10 rather than dumping everything, `--done` includes completed while default excludes them, no-match prints `No matches.`, long title survives untruncated
- [x] run `go test ./cmd/` — must pass
- [x] run `go build -o monolog` so the `mlog` alias target is current — Tasks 5 and 6 both exercise the binary, and the checked-in artifact is months stale

### Task 5: Ship the Claude skill and install it

**Files:**
- Create: `docs/claude-skill/SKILL.md`
- Create: `docs/claude-skill/README.md`
- Create: `cmd/skill_test.go`
- Symlink (local, not a repo change): `~/.claude/skills/monolog/SKILL.md` → `docs/claude-skill/SKILL.md`

- [x] create `docs/claude-skill/SKILL.md` with frontmatter `name: monolog`, `allowed-tools: Bash`, and the `description` **verbatim** from Technical Details
- [x] write it generically — `monolog` throughout, no `mlog` alias, no absolute dev-binary path, nothing specific to one machine
- [x] document the two write modes — unprompted `monolog add "<title>" --tags claude -s someday --body "<where + why>"`; explicit ask `monolog add "<title>" --tags claude` plus whatever schedule/tags the user named. Note `--tags` is a single comma-separated string, not repeatable, so tags must be merged (`--tags claude,domains`). The `claude` tag is pure provenance and goes on **every** write; `-s someday` alone carries triage state
- [x] document dedupe: run `monolog search "<keywords>"` before every unprompted write; on a near-duplicate do **not** file — use `monolog note <id> "<new detail>"` instead. Same issue plus new information is a note, not a second task
- [x] document the bar — files: outlives the session, concretely actionable, annoying to lose. Does not file: anything fixed in-session, observations with no action, vague "maybe refactor X someday", and explicitly anything already captured in a plan file. State that there is **no write cap** — dedupe and the bar are the only guards
- [x] document conventions — title imperative, self-contained, readable cold in three weeks; body 1-3 lines as `repo/path.go:142` plus one sentence on why it was deferred; repo name goes in the **body, not a tag**, because the TUI's tag view turns every tag into a tab
- [x] warn about the auto-tag rule: a title starting `"<existing-tag>: "` silently gains that tag via `model.ParseTitleTag` (`cmd/add.go:83`), so a title like `"monolog: add search command"` can pick up a second tag despite an explicit `--tags`. Prefer titles that don't lead with `word:`
- [x] document read commands: `monolog search`, `monolog ls`, `monolog ls --active`, `monolog ls -a --tag claude -s someday`, `monolog show <id-or-initials>`, `monolog log`; call out that `ls -a` means "all schedules, still open" while `search -d` means "include done" — different axes, easy to conflate
- [x] note that identifiers resolve by title initials as well as ULID prefix (`monolog show flb` → "Fix login bug")
- [x] document prohibitions: only `add` and `note` are ever unprompted — `done`/`edit`/`rm`/`mv` need an explicit instruction in the current conversation, and fixing something does **not** license marking the matching task done; never `monolog sync` (state the consequence — captures stay local until the user syncs, so they don't reach other devices immediately); never bare `monolog` (TUI), `init`, `email`, or `telegram`; if `monolog` is missing from PATH, stop and say so rather than installing it
- [x] create `docs/claude-skill/README.md` explaining what the skill does, the quarantine convention, and install: symlink (`ln -s "$PWD/docs/claude-skill/SKILL.md" ~/.claude/skills/monolog/SKILL.md`) to track repo updates, or copy for a pinned install. Mirror the tone of `docs/raycast/README.md`
- [x] create `cmd/skill_test.go` following `cmd/telegram_deploy_test.go`: resolve repo root via `runtime.Caller(0)`, assert both `docs/claude-skill/` files exist, assert `SKILL.md` frontmatter parses as YAML with non-empty `name` and `description`
- [x] extend that test to cross-check documentation against the live CLI: extract every `monolog <subcommand>` occurrence from `SKILL.md`, assert each resolves against `NewRootCmd().Commands()`, and assert every documented `--flag`/`-x` exists on the command it is shown with (`cmd.Flags().Lookup` / `ShorthandLookup`). This is what stops the shipped skill from rotting when a flag is renamed
- [x] run `go test ./cmd/` — must pass
- [x] install locally (deferred - worktree run; symlink after merge, see Post-Completion)
- [x] verify the CLI paths by hand: `monolog search "test"`, then one `monolog add ... --tags claude -s someday`, confirm it lands as documented, then `monolog rm` the test task
- [x] verify skill loads (skipped - not automatable; requires fresh session after install)

### Task 6: Verify acceptance criteria

All criteria were verified against a **throwaway `MONOLOG_DIR`** seeded to the
same scale as the measured motivation (222 tasks: 32 open, 190 done, titles up
to 69 runes). The real backlog at `~/.monolog` was never read or written.

- [x] `monolog search <query>` returns ranked matches with untruncated titles, in materially less output than `monolog ls -a -f`
  - `ls -a -f` = **304 lines / 14954 chars**; `search login` = **10 lines / 890 chars** (**16.8× fewer chars**), `search search ranker` = 4 lines / 219 chars. Reproduces the plan's measured 298 lines / 14.2 KB almost exactly.
  - The 69-rune fixture title prints in full under `search`; the same task under `ls -a` renders as `Fix the login bug that drops the OAuth …`. Confirmed the 60-column pad cap works: the over-long title pushes only its own trailing columns right.
- [x] TUI fuzzy search (`/`) behaves identically to before the refactor — ranking order, highlighting (including multibyte titles), Enter-commits-to-task, Esc-is-a-no-op
  - **Verified by the test suite, not by hand** — the interactive TUI cannot be driven from this environment. `go test -run TestSearch ./internal/tui/` = **32 pass / 0 fail**, covering ranking order (`TypingRerunsQueryAndChangesResults`), Enter-commits (`CommitScheduleViewFocusesTargetTab`, `CommitDoneTaskSwitchesToDoneTab`, `CommitTagView*`), Esc-is-a-no-op (`EscKeepsActiveTabAndListCursor`, `EscClosesSearchMode`), and the two ported multibyte coupling tests (`HighlightMultibyteTitleRoundTrips`, `HighlightCaseInsensitiveMultibyteRoundTrips`). `./internal/search/` adds 14 more covering the ranker invariants (defensive copy, no pre-lowercasing, nil receiver).
- [x] ranking is identical between TUI and CLI **for the same task set** — compare against `search --done`, since the TUI haystack is open+done (`model.go:831`) while the CLI defaults to open-only
  - Pinned by two new mirrored tests rather than left to reasoning: `cmd.TestSearchCommand_DoneRankingMatchesSharedIndex` (CLI `--done` output order == `search.NewIndex(store.List(ListOptions{})).Rank(...)`) and `tui.TestSearch_RankingMatchesSharedIndexOverStoreList` (overlay results == same shared index, asserting IDs *and* scores). Both sides reconstruct the haystack from `store.List(ListOptions{})`, so they meet in the middle.
  - Both tests were mutation-checked: reversing the CLI result order fails the first; filtering done tasks out of `openSearch` fails the second.
- [x] `--done` includes completed tasks; default excludes them
  - Against the temp store: `search "login redirect loop"` → `No matches.` (the matching task is done); `-d` and `--done` both print it with the `x ` status cell. Also covered by `TestSearchCommand_DefaultExcludesDone`.
  - Spot-checked alongside it: `-n 0` clamps to 10, `-n 3` → 3 rows, `-n 25` → 25 rows, no-match → `No matches.`
- [x] the shipped skill documents no command or flag that doesn't exist (`cmd/skill_test.go` passes)
  - 4 tests pass. Mutation-checked for teeth: appending `` `monolog search "x" --nonexistent-flag` `` and `` `monolog frobnicate` `` to `SKILL.md` fails `TestSkillDocumentsOnlyRealCommands` with both errors named individually. `SKILL.md` restored, tree clean.
- [x] `~/.claude/skills/monolog/SKILL.md` resolves through the symlink to the repo copy
  - **NOT verified — deliberately deferred, same reason as Task 5's install step.** `~/.claude/skills/` does not exist yet; creating the symlink from this worktree would dangle the moment the worktree is removed, and a dangling skill file can break session startup. Install after merge per Post-Completion, then re-check.
- [x] run full suite: `go build ./... && go test ./... && go vet ./...`
  - All three green with the two new tests included; every package `ok`, no cached results (`-count=1`).
  - [decision] `gofmt -l` flags 9 files (`cmd/email.go`, `cmd/pager.go`, `internal/telegram/*`, …) under Go 1.26.2. All predate this plan — none is in `git diff 38b2fa1..HEAD` — and the project's stated lint gate is `go vet ./...`, which passes. Left alone rather than mixing an unrelated repo-wide reformat into this change. The two files this task touched are gofmt-clean.

### Task 7: [Final] Update documentation

- [x] **edit** `CLAUDE.md:67` — "Pure ranker lives in `internal/tui/search_match.go`" is false after Task 2; point it at `internal/search/rank.go`
- [x] **edit** `CLAUDE.md:81` — Dependencies lists sahilm/fuzzy as "TUI fuzzy search ranker"; `internal/tui` no longer imports it, `internal/search` does
- [x] add `internal/search/` to the Architecture tree in `CLAUDE.md`
- [x] add a Key Design Decisions entry for the shared ranker: one `Index` used by both TUI and CLI, so ranking cannot drift between them; plus the nil-receiver contract and the no-pre-lowercasing invariant
- [x] update `README.md` command list with `monolog search` (repo docs use `monolog`, never the `mlog` alias)
- [x] add a `docs/claude-skill/` entry to `CLAUDE.md` describing the shipped skill, the `claude` + `someday` quarantine convention, and the fact that `cmd/skill_test.go` pins its documented commands to the live cobra tree
- [x] add a README section pointing at `docs/claude-skill/README.md`, alongside the existing Raycast quick-capture docs
  - [decision] `README.md` had **no** Raycast section to sit alongside — `docs/raycast/` was only ever linked from `CLAUDE.md`. Added a short `## Quick capture (Raycast)` section pointing at `docs/raycast/README.md` immediately before the new `## Claude Code skill` section, so the intended pairing now actually exists. Both sit after `## Telegram integration`, matching how the other integration docs are laid out.
  - [decision] Both new Key Design Decisions entries were **appended** at the end of the list rather than filed next to the "Fuzzy search" bullet — the list is append-ordered by feature. The fuzzy-search bullet gained an explicit "see **Shared search ranker** below" cross-reference so discoverability doesn't depend on reading order.
  - [decision] `grep -rn 'search_match' --include='*.md' .` still hits `docs/plans/*` (this plan's own Context section and the 2026-04-16 fuzzy-search plan, in both `docs/plans/` and `docs/plans/completed/`). Plans are point-in-time records, not live documentation — rewriting them would falsify the history the `completed/` convention exists to keep. All **live** docs (`CLAUDE.md`, `README.md`, `docs/**/README.md`, `SKILL.md`) are clean.
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*No checkboxes — requires observation across future sessions.*

**Deferred from Task 5 (run these after merging):**

Task 5 ran in a temporary git worktree, so the local install step was skipped —
a symlink into the worktree would dangle the moment it is removed, and a
dangling skill file can break session startup. After merging to `main`, install
from the real checkout:

```sh
mkdir -p ~/.claude/skills/monolog
ln -s /Users/mmaksmas/IdeaProjects/monolog/docs/claude-skill/SKILL.md ~/.claude/skills/monolog/SKILL.md
```

Then start a **fresh** Claude Code session and confirm `monolog` appears in the
available-skills list — that is the frontmatter-parse check, which cannot be
automated from inside a running session. (`cmd/skill_test.go` already asserts
the frontmatter parses as YAML with a non-empty `name`/`description`, so a hard
parse failure would have shown up in CI; the fresh-session check confirms
Claude Code itself picks the file up.)

The CLI half of that verification *was* done in Task 5, against a throwaway
`MONOLOG_DIR` rather than the real backlog: `init`, `add --tags claude -s
someday --body`, `add --tags claude,infra -s week`, `ls`, `ls -a`,
`ls --active`, `ls -a --tag claude -s someday`, `search`, `search -n 25 -d`,
`note` (resolved by title initials), `show`, `log`, `rm`. Behaviour matched the
skill, including the auto-tag gotcha — `add "infra: …" --tags claude` landed
tagged `claude, infra`.

**Manual verification of the skill:**
- Confirm the skill loads on an explicit ask ("add this to my backlog") — this path should be reliable.
- Watch whether it fires **proactively** over the next several sessions. This is the known weak point of the skill-only design: mid-task discovery has no user utterance to match against, so the description is doing all the work and failure is silent.
- **Escape hatch if it never fires unprompted:** add a 3-line primer to `~/.claude/CLAUDE.md` (currently empty) naming monolog, the filing rule, and the one command. That gives a deterministic trigger at a small permanent context cost. Deliberately deferred rather than rejected.

**Queue health:**
- After a few weeks, run `monolog ls -a --tag claude -s someday` and judge signal-to-noise. If the queue is full of items you delete on sight, the bar in the skill is too low — tighten it there rather than reinstating a write cap.
