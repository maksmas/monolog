# monolog as a Claude Code skill

`SKILL.md` in this directory teaches [Claude Code](https://claude.com/claude-code) to use the monolog CLI: to file work into your backlog and to look up what is already there.

Claude can already shell out to `monolog` without any of this. What the skill adds is **discoverability** — Claude working in some unrelated repo has no idea monolog exists — and **trigger discipline**: when filing is appropriate, what the title and body should look like, and which commands are off limits.

The main payoff is proactive capture. Claude notices follow-up work it is not going to do in the current session — tech debt spotted in passing, a bug found but not fixed, something you waved off with "later" — and files it, instead of mentioning it in chat where it scrolls away.

This is deliberately **not** plan tracking. Claude manages plan files perfectly well on its own, and the skill says so explicitly.

One thing that looks like a typo but is not: the frontmatter trigger phrases match both "backlog" and "mlog" ("put that in mlog", "anything in mlog about X"). `mlog` is a common shell alias for the `monolog` binary, and people who alias it say it out loud too. Every command in the skill body is spelled `monolog` — only the trigger phrases match the short form.

## The quarantine convention

Spam is the obvious risk of letting an agent write to your backlog, so unprompted writes are fenced off:

```sh
monolog add "<title>" --tags claude -s someday --body "<where + why>"
```

- `--tags claude` is provenance. It goes on **every** task the skill adds, prompted or not, so you can always tell what came from Claude. (`monolog note` takes no tags — a note inherits whatever the task it lands on already carries.)
- `-s someday` is the quarantine. Someday is out of the today and week views, so nothing Claude files can interrupt your actual day.

Drain the queue when it suits you:

```sh
monolog ls --tag claude -s someday
```

Reschedule what is worth doing, `monolog rm` the rest. When you ask Claude to file something explicitly, it keeps the `claude` tag but uses whatever schedule you named — no quarantine.

There is no write cap. Deduplication (Claude runs `monolog search` before every unprompted write, and appends a `monolog note` rather than filing a near-duplicate) and a stated bar for what qualifies are the only guards.

## Install

Skills live in `~/.claude/skills/<name>/SKILL.md`. Symlink so your installed copy tracks this repo:

```sh
mkdir -p ~/.claude/skills/monolog
ln -s "$PWD/docs/claude-skill/SKILL.md" ~/.claude/skills/monolog/SKILL.md
```

Run that from the repo root — `ln -s` stores the path verbatim, so a relative source would dangle.

Prefer a pinned copy that will not change under you when you `git pull`? Copy instead:

```sh
mkdir -p ~/.claude/skills/monolog
cp docs/claude-skill/SKILL.md ~/.claude/skills/monolog/SKILL.md
```

Either way, start a **fresh** Claude Code session afterwards — skills are read at startup — and confirm `monolog` shows up in the available-skills list. If it does not, the frontmatter failed to parse.

The skill assumes `monolog` is on Claude's `PATH` and nothing else. No config, no environment variables; it uses whatever `MONOLOG_DIR` your shell already sets.

## Verifying it works

The explicit path is easy to check — ask Claude to "add X to my backlog" and watch for the `monolog add` call.

The proactive path is the interesting one and can only be judged over time: there is no user utterance to match against mid-task, so the frontmatter `description` is doing all the work and a miss is silent. Give it a few sessions, then look at what landed:

```sh
monolog ls --tag claude -s someday
```

If it never fires unprompted, the fix is a three-line primer in `~/.claude/CLAUDE.md` naming monolog, the filing rule and the one command — a deterministic trigger, at the cost of some permanent context.

If it fires too eagerly and the queue fills with things you delete on sight, raise the bar in `SKILL.md` rather than reinstating a cap.

## Keeping it honest

`cmd/skill_test.go` parses `SKILL.md` and cross-checks it against the live cobra command tree: every `monolog <subcommand>` it documents must resolve, and every `--flag` / `-x` must exist on the command it is shown with. Rename a flag and the test fails instead of the skill quietly rotting.

That test only covers the copy in this repo. A `cp`-based install drifts from it silently — one more reason to prefer the symlink.
