---
name: monolog
description: "Personal backlog capture and lookup via the monolog CLI. Use when the user asks to file or check something in their backlog (\"add this to my backlog\", \"put that in mlog\", \"what's on my plate\", \"anything in mlog about X\"). ALSO use proactively, without being asked, whenever work is identified that will NOT be done in the current session: tech debt noticed while doing something else, a bug found but not fixed, something the user deferred (\"not now\", \"later\", \"leave it\"), or unfinished deferred items when a plan or session wraps up. Filing is cheap and quarantined — losing the thought is not."
allowed-tools: Bash
---

# monolog

`monolog` is the user's personal backlog — one JSON file per task in a git repo, driven entirely from the CLI. Use it to file work that will outlive the current session, and to look up what is already filed.

Every write auto-commits locally. Nothing you capture reaches the user's other devices until they sync themselves.

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
monolog search "<keywords>"
```

Search is fuzzy across titles and bodies, prints untruncated titles, and returns the top 10 open tasks.

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

The `claude` tag is pure provenance and goes on **every** write, prompted or not, so the user can always tell what you filed. `-s someday` alone carries the triage state; leave it off and the task lands on today, where it interrupts.

`--tags` takes a **single comma-separated string** and is not repeatable — passing it twice keeps only the last value. Merge the tags yourself: `--tags claude,infra`.

Schedules are `today` (the default), `tomorrow`, `week`, `month`, `someday`, or a date in the user's configured format.

### Title and body conventions

Title:

- imperative — "Handle nil store in openStore", not "openStore nil bug"
- self-contained — it has to read cold in three weeks, with no memory of this session
- no "TODO:" prefix, no ticket-speak, no leading emoji

Body, one to three lines:

- where it lives, as a path with a line number: `internal/store/store.go:142`
- one sentence on why it was deferred, so future-them can judge whether it still matters

Put the repo name **in the body, not in a tag**. The TUI's tag view turns every tag into a tab, so a tag per repo makes that view unusable.

### Gotcha: titles that start with `word:`

If a title begins with `"<existing-tag>: "` and that tag is already on some other task, monolog silently adds it — on top of whatever `--tags` you passed. So `monolog add "monolog: add a search command" --tags claude` ends up tagged `claude` **and** `monolog`. Harmless, but surprising; prefer titles that do not lead with `word:`.

## Reading

```sh
monolog ls                               # today's open tasks
monolog ls -a                            # all open tasks, every schedule
monolog ls --active                      # the current working set
monolog ls -a --tag claude -s someday    # the quarantine queue you file into
monolog search "<keywords>"              # fuzzy, untruncated titles, top 10
monolog search "<keywords>" -n 25        # more hits
monolog search "<keywords>" -d           # include completed tasks
monolog show <id>                        # full detail, body and notes
monolog log                              # completed in the last 7 days
```

`ls -a` and `search -d` are **different axes** and easy to conflate: `-a` on `ls` means "all schedules, still open", `-d` on `search` means "include done", and `ls -d` means "only completed".

Identifiers resolve two ways. Anywhere a command takes `<id>` you can pass a ULID prefix (`01J5K`) or the initials of the title's words — `monolog show flb` resolves "Fix login bug". Two characters minimum; an ambiguous match lists the candidates instead of guessing.

## Never do these

- **Only `add` and `note` are ever unprompted.** `monolog done`, `monolog edit`, `monolog rm` and `monolog mv` need an explicit instruction in the current conversation. Fixing something does **not** license marking the matching task done — the user decides what is finished.
- **Never run `monolog sync`.** Pushing and rebasing against the user's remote is their call. The consequence is worth stating out loud if it matters: what you capture stays on this machine until they sync, so it will not show up on their phone right away.
- **Never launch the TUI.** Bare `monolog` opens an interactive terminal UI — useless here, and it fails outright without a TTY. Same for `monolog init`, `monolog email` and `monolog telegram`: repo setup and background integrations the user owns.
- **If `monolog` is not on PATH, stop and say so.** Do not install it, do not guess at a binary path, do not create a backlog directory.
