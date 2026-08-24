---
name: monolog
description: "Personal backlog capture and lookup via the monolog CLI. Use when the user asks to file or check something in their backlog (\"add this to my backlog\", \"put that in mlog\", \"what's on my plate\", \"anything in mlog about X\"). ALSO use proactively, without being asked, whenever work is identified that will NOT be done in the current session: tech debt noticed while doing something else, a bug found but not fixed, something the user deferred (\"not now\", \"later\", \"leave it\"), or unfinished deferred items when a plan or session wraps up. Filing is cheap and quarantined — losing the thought is not."
allowed-tools: Bash(monolog add *) Bash(monolog note *) Bash(monolog search *) Bash(monolog ls *) Bash(monolog show *) Bash(monolog log *)
---

# monolog

`monolog` is the user's personal backlog — one JSON file per task in a git repo, driven entirely from the CLI. Use it to file work that will outlive the current session, and to look up what is already filed.

Every write auto-commits and then pushes that commit to the user's remote in the background, so what you file reaches their other devices — including their phone — without them doing anything. The push is silent on success and non-fatal on failure: if it cannot reach the remote the commit simply stays local until the next one gets through. Nothing about that needs handling from you; it happens inside `add` and `note`.

## The bar for filing

File it when all three hold:

- it **outlives this session** — you are not going to do it now
- it is **concretely actionable** — a future reader knows what to change
- **losing it would be annoying** — it is real work, not a passing thought

Do not file:

- anything you already fixed in this session
- observations with no action attached ("this package is getting large")
- vague intentions — "maybe refactor X someday"
- anything already captured in a plan file — plans are tracked on their own, and copying them into the backlog creates two places to keep in step

There is **no cap** on writes per session. Dedupe and the bar above are the only guards: if three separate real follow-ups turn up, file three.

## Dedupe first — every time

Before every unprompted write, search:

```sh
monolog search "<distinctive words>"
```

Search reads titles and bodies, prints untruncated titles, and returns the top 10 open tasks.

**Two or three distinctive words is the best query.** A multi-word query is ranked by how many of its words the task actually contains: a task containing none of them is not returned at all, and the ones containing the most sort first. So extra words narrow the result set instead of widening it, and word order is irrelevant — `monolog search telegram week` and `monolog search week telegram` return exactly the same rows in the same order.

Pick the distinctive words in the thing you are about to file — a symbol, a filename, a package, a product name — and search for them together:

```sh
monolog search "telegram week reschedule"
```

Two things to keep in mind:

- A **single word** is matched fuzzily rather than by term count, so it also catches near-spellings — but a short or common one matches almost anything. One rare word is a fine query; one common word is not.
- Words are matched as **substrings**, so `week` finds "weekly" but `scheduling` does not find "schedule". When in doubt use the shorter stem.
- Search **splits on whitespace only**, so punctuation stuck to a word travels with it and stops it matching: `telegram,` and `openStore()` find nothing. Pass bare words — `telegram`, `openStore`.

Not every word has to hit. A three-word query still surfaces a task matching two of them, ranked below anything matching all three — so read past the first row before concluding there is no duplicate.

If a near-duplicate comes back, **do not file a second task**. Append what is new instead:

```sh
monolog note <id> "<the new detail>"
```

Same issue plus new information is a note. A second task is only right when the work itself is different.

## Writing

**Unprompted — you noticed it, the user did not ask.** Quarantined so it never lands in today or week:

```sh
monolog add "<title>" --tags claude -s someday --body "<where + why>"
```

**Explicit ask — the user told you to file it.** Use whatever schedule and tags they named:

```sh
monolog add "<title>" --tags claude
monolog add "<title>" --tags claude,infra -s week
```

The `claude` tag is pure provenance and goes on **every** `add`, prompted or not, so the user can always tell what you filed. (`monolog note` takes no tags — it inherits whatever the task already carries.) `-s someday` alone carries the triage state; leave it off and the task lands on today, where it interrupts.

`--tags` takes a **single comma-separated string** and is not repeatable — passing it twice keeps only the last value. Merge the tags yourself: `--tags claude,infra`.

Schedules are `today` (the default), `tomorrow`, `week`, `month`, `someday`, or a date in the user's configured format.

### Title and body conventions

Title:

- imperative — "Handle nil store in openStore", not "openStore nil bug"
- self-contained — it has to read cold in three weeks, with no memory of this session
- no "TODO:" prefix, no ticket-speak, no leading emoji

Body, one to three lines:

- where it lives, as a repo-qualified path with a line number: `monolog/internal/store/store.go:142`
- one sentence on why it was deferred, so future-them can judge whether it still matters

The repo name goes **in the body, not in a tag** — that is why the path above leads with it. The TUI's tag view turns every tag into a tab, so a tag per repo makes that view unusable.

### Gotcha: titles that start with `word:`

If a title begins with `"<existing-tag>: "` and that tag is already on some other task, monolog silently adds it — on top of whatever `--tags` you passed. So `monolog add "monolog: add a search command" --tags claude` ends up tagged `claude` **and** `monolog`. Harmless, but surprising; prefer titles that do not lead with `word:`.

## Reading

```sh
monolog ls                               # today's open tasks
monolog ls -a                            # all open tasks, every schedule
monolog ls --active                      # the current working set
monolog ls --tag claude -s someday       # the quarantine queue you file into
monolog search "<words>"                 # untruncated titles, top 10
monolog search "<words>" -n 25           # more hits
monolog search "<words>" -d              # include completed tasks
monolog show <id>                        # full detail, body and notes
monolog log                              # completed in the last 7 days
```

`ls -a` and `search -d` are **different axes** and easy to conflate: `-a` on `ls` means "all schedules, still open", `-d` on `search` means "include done", and `ls -d` means "only completed". Passing `--schedule` to `ls` already picks a schedule, so `-a` alongside it does nothing.

Identifiers resolve two ways. Anywhere a command takes `<id>` you can pass a ULID prefix (`01J5K`) or the initials of the title's words — `monolog show flb` resolves "Fix login bug". Two characters minimum; an ambiguous match lists the candidates instead of guessing. **Initials only match open tasks**, so a completed task pulled out of `search -d` has to be addressed by ULID prefix — copy it from the search output.

## Never do these

- **Only `add` and `note` are ever unprompted.** `monolog done`, `monolog edit`, `monolog rm` and `monolog mv` need an explicit instruction in the current conversation. Fixing something does **not** license marking the matching task done — the user decides what is finished.
- **Never run `monolog sync`.** Note the distinction: the push that `add` and `note` do for you is expected and already covers getting your capture off this machine. `monolog sync` is a different operation — a pull-and-rebase against the user's remote, which rewrites their local history and drags down whatever else is waiting there. That is theirs to run, so there is never a reason for you to reach for it.
- **Never launch the TUI.** Bare `monolog` opens an interactive terminal UI — useless here, and it fails outright without a TTY. Same for `monolog init`, `monolog email` and `monolog telegram`: repo setup and background integrations the user owns.
- **If `monolog` is not on PATH, stop and say so.** Do not install it, do not guess at a binary path.
- **If a write fails with `not a git repository`, the backlog is not set up. Stop.** Tell the user to run `monolog init` and do not run it yourself, do not retry, do not try another directory. Reads will not warn you about this: against an uninitialized directory `monolog search` and `monolog ls` just report nothing found — and the read itself creates an empty `.monolog/tasks/` as a side effect. So an empty backlog and a missing backlog look identical until the first write fails. Mention that the failed `add` still left an uncommitted task file behind — `monolog init` picks it up.
