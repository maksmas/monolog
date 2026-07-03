# Prepare monolog for public OSS release (phase 1: packaging & docs)

## Overview
Make monolog adoptable by strangers, not just its author. The tool is already
feature-rich (TUI + CLI, git sync, tags, schedules, recurrence, notes, fuzzy
search, themes, Gmail import, Telegram bot). What blocks a public OSS release is
**packaging and presentation**, not capability:

- No LICENSE (legally unusable by anyone today).
- Install is `go install` only, on bleeding-edge Go 1.26 — excludes non-Go users.
- No prebuilt binaries, no Homebrew.
- No CI signal, no contributor docs.
- README is reference-first; the TUI (the best feature) is invisible.
- Minor repo hygiene (`tasks.txt` tracked).

This plan delivers: **MIT license, goreleaser-driven cross-platform binaries + a
Homebrew tap, CI + release GitHub workflows, a value-first README with a
reproducible vhs demo GIF, a CONTRIBUTING guide, and repo cleanup.**

Out of scope (feature gaps — captured separately as `someday` tasks in the
author's own monolog backlog, IDs `01KWME0W…`): bulk operations and export.

## Context (from discovery)
- **Module**: `github.com/mmaksmas/monolog`, Go 1.26.2. Entry `main.go` → `cmd.Execute()`.
- **Version surface already exists**: `cmd/root.go` has `var Version = "dev"` wired
  into cobra's `Version:` field, so `monolog --version` already works. Only ldflags
  injection is missing.
- **.gitignore already covers** `monolog` (binary), `.DS_Store`, `/docs/.DS_Store`,
  `.idea`, `.claude`, `/.monolog/`, `/dist/`, `/.env.deploy`. Only stray tracked file
  is `tasks.txt`.
- **Makefile** already has `build` / `test` / `vet` + the bot deploy pipeline; new
  targets can slot in alongside.
- **Test conventions**: table-driven `cmd/*_test.go` (e.g. `root_test.go`); every
  change ships tests (CLAUDE.md rule).
- No `.github/`, no `.goreleaser.yaml`, no LICENSE, no CONTRIBUTING today.

## Development Approach
- **Testing approach**: Regular (artifact first, then test/verify). Most deliverables
  here are non-code artifacts (LICENSE, YAML, markdown, `.tape`) that have no unit
  surface; each such task lists an explicit **verification** step (tool check or
  build) in lieu of a unit test. The one code change (ldflags version wiring) gets a
  real Go test.
- Complete each task fully before the next; keep changes small and focused.
- **Every code change includes tests; all tests pass before the next task.**
- Update this plan if scope shifts during implementation.
- Maintain backward compatibility (existing CLI/flags unchanged).

## Testing Strategy
- **Unit tests**: required for code changes. Here that is Task 3 (version wiring) —
  assert `cmd.Version` defaults sanely and `--version` output renders.
- **Artifact verification** (non-code deliverables), treated with the same
  must-pass rigor before advancing:
  - `.goreleaser.yaml` → `goreleaser check` and a local `goreleaser release
    --snapshot --clean` dry run (produces `dist/` without publishing).
  - GitHub workflows → `actionlint` if available, else YAML parse + manual review.
  - README / LICENSE / CONTRIBUTING → `go build` still green + manual read-through.
  - `demo.tape` → `vhs demo.tape` renders `demo.gif` without error (manual, needs vhs).
- No project e2e suite exists; none added.

## Solution Overview
- **License**: standard MIT text at repo root; README gains a License section.
- **Distribution**: one `.goreleaser.yaml` produces darwin+linux / amd64+arm64
  archives + `checksums.txt` on every `v*` tag, and publishes a Homebrew formula to a
  separate tap repo (`mmaksmas/homebrew-tap`) → users `brew install
  mmaksmas/tap/monolog`. Version injected into `cmd.Version` via ldflags.
- **CI**: a light `ci.yml` (build + test + vet on push/PR) for the green check;
  `release.yml` runs goreleaser on tags.
- **README**: restructured value-first / visual-early / reference-last, fronted by a
  vhs-generated `demo.gif`.
- **Docs/hygiene**: CONTRIBUTING.md; remove tracked `tasks.txt`.

## Technical Details
- **ldflags target**: `-X github.com/mmaksmas/monolog/cmd.Version={{.Version}}`.
- **goreleaser build**: `main: .`, `binary: monolog`, `env: CGO_ENABLED=0`,
  `goos: [darwin, linux]`, `goarch: [amd64, arm64]`.
- **brews block**: `repository: {owner: mmaksmas, name: homebrew-tap}`, token from
  `HOMEBREW_TAP_GITHUB_TOKEN`, homepage/description/install/test stanza.
- **release.yml**: trigger `push: tags: ['v*']`, `goreleaser/goreleaser-action`,
  `permissions: contents: write` (the default `GITHUB_TOKEN` is read-only; goreleaser
  needs write to create the Release + upload assets), secrets `GITHUB_TOKEN` (release)
  + `HOMEBREW_TAP_GITHUB_TOKEN` (tap push).
- **ci.yml**: trigger push + pull_request; `actions/setup-go` with
  `go-version-file: go.mod` (single source of truth, no drift); run
  `go build ./...`, `go test ./...`, `go vet ./...`.
- **.goreleaser.yaml** must start with an explicit `version: 2` key (v2 schema).
- **Copyright holder**: git handle `mmaksmas` / `maksims@metisjean.com`.
  ⚠️ Confirm the legal name to appear in the MIT copyright line before publishing.

## What Goes Where
- **Implementation Steps** (checkboxes): all files creatable in this repo — LICENSE,
  YAML configs, workflows, `.tape`, README/CONTRIBUTING, test, `git rm`.
- **Post-Completion** (no checkboxes): manual GitHub steps that cannot be automated —
  creating the tap repo, the PAT secret, tagging the first release, and running `vhs`
  to render the GIF.

## Implementation Steps

### Task 1: Add MIT LICENSE

**Files:**
- Create: `LICENSE`

- [x] create `LICENSE` with standard MIT text, `Copyright (c) 2026 <legal name>`
- [x] leave an inline note / confirm the exact copyright holder name (handle: mmaksmas) — used `Copyright (c) 2026 mmaksmas`; legal name must be confirmed before publishing
- [x] verify: `go build -o monolog .` still succeeds (sanity, no code touched)

### Task 2: Repo hygiene — untrack stray artifacts

**Files:**
- Delete (from git): `tasks.txt`
- Modify (verify only): `.gitignore`

- [x] `git rm --cached tasks.txt` (drop from tracking; also delete working copy if it is a stray)
- [x] confirm `.gitignore` already covers `monolog`, `.DS_Store`, `.idea`, `.claude`, `dist/` (it does — add anything missing); add `tasks.txt` to `.gitignore` so it can't creep back in
- [x] verify: `git status` shows a clean tree except intended changes; `git ls-files | grep tasks.txt` returns nothing

### Task 3: Inject build version via ldflags

**Files:**
- Modify: `Makefile`
- Modify: `cmd/root_test.go` (or create `cmd/version_test.go`)

- [x] add ldflags to the Makefile `build` target: `-ldflags "-X github.com/mmaksmas/monolog/cmd.Version=$(VERSION)"` with a `VERSION ?= dev` default (git-describe optional)
- [x] apply the same ldflags to the `build-bot-linux-arm64` target (or add a note in the Makefile that the bot binary intentionally stays `dev`) so the deployed bot doesn't silently report `Version=dev`
- [x] confirm `cmd/root.go` `Version` var + cobra `Version:` field remain the injection point (no code change needed there)
- [x] write test asserting `NewRootCmd().Version` is non-empty and defaults to `"dev"` when unset
- [x] write test asserting `--version` produces output containing the version string
- [x] run tests: `go test ./cmd/...` — must pass before next task

### Task 4: goreleaser configuration

**Files:**
- Create: `.goreleaser.yaml`

- [x] start `.goreleaser.yaml` with an explicit top-level `version: 2` key (required by the v2 schema; without it `goreleaser check` warns)
- [x] create `builds` for `darwin,linux` × `amd64,arm64`, `binary: monolog`, `main: .`, `env: [CGO_ENABLED=0]`, ldflags injecting `cmd.Version`
- [x] add `archives` (tar.gz) and `checksum` (`checksums.txt`) blocks
- [x] add `homebrew_casks` block targeting `mmaksmas/homebrew-tap` with `HOMEBREW_TAP_GITHUB_TOKEN`, homepage, description, and a quarantine-removal postflight hook (migrated from the deprecated `brews:` block — `goreleaser check` on v2.16.0 rejects both `brews:` and `homebrew_casks.binary`; the cask auto-detects the binary)
- [x] add `changelog` block (git-based, sensible filters)
- [x] verify: (Task 9) `goreleaser 2.16.0` snapshot release builds all 4 targets + archives + `checksums.txt` + the generated Homebrew cask cleanly; `goreleaser check` now PASSES clean (exit 0, no deprecation warnings) after migrating `brews:` → `homebrew_casks:`.

### Task 5: GitHub Actions — CI + release workflows

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/release.yml`

- [x] create `ci.yml`: on push + pull_request, `setup-go` with `go-version-file: go.mod`, run `go build ./...`, `go test ./...`, `go vet ./...`
- [x] create `release.yml`: on `push: tags: ['v*']`, `permissions: contents: write` on the job, checkout with `fetch-depth: 0`, `setup-go` (`go-version-file: go.mod`), `goreleaser-action` (`args: release --clean`), env `GITHUB_TOKEN` + `HOMEBREW_TAP_GITHUB_TOKEN`
- [x] verify: `actionlint 1.7.12` ran clean on both workflows (Task 9) — no findings

### Task 6: vhs demo script + Makefile target

**Files:**
- Create: `docs/demo.tape`
- Modify: `Makefile` (add a `demo` target: `vhs docs/demo.tape`)

> Note: the rendered `docs/img/demo.gif` is a binary artifact that requires `vhs`
> and a recorded TUI run, so its generation + commit is **Post-Completion** (manual,
> pre-first-release). This task only produces the reproducible source (`.tape`) and
> the make target; the gif path is referenced as a placeholder by Task 7.

- [x] write `docs/demo.tape` driving the TUI through a representative flow (launch, add a task, tag/schedule view, fuzzy search, mark done) with readable `Set FontSize` / `Sleep` pacing and `Output docs/img/demo.gif`
- [x] point the tape at an isolated `MONOLOG_DIR` (temp/demo dir) so the author's real backlog is never recorded
- [x] add Makefile `demo` target (`vhs docs/demo.tape`)
- [x] verify: (vhs not installed — tape written and reviewed; `vhs validate` + gif render deferred to Post-Completion)

### Task 7: README rewrite (value-first)

**Files:**
- Modify: `README.md`

- [x] restructure to: (1) title + one-line tagline, (2) hero image referencing `docs/img/demo.gif` (path committed now; the gif itself lands during Post-Completion), (3) "Why monolog" differentiation bullets, (4) Install (brew → binary download → `go install`), (5) 60-second Quick start, (6) Highlights bullets (tag view, fuzzy search, recurrence, themes, Gmail, Telegram), (7) condensed Concepts (schedules/buckets, tags & reserved `active`, storage+sync), (8) full command reference below the fold, (9) Configuration → Contributing → License
- [x] preserve the accurate existing reference/flag content — restructure and add top sections, do not rewrite correct docs
- [x] add the `brew install mmaksmas/tap/monolog` instruction and a binary-download pointer to GitHub Releases
- [x] add a License section linking `LICENSE`; link `CONTRIBUTING.md`
- [x] verify: markdown renders sensibly (headings/anchors/links resolve); LICENSE/CONTRIBUTING links resolve, and the `docs/img/demo.gif` path is correct (the gif file itself is added during Post-Completion first-release)

### Task 8: CONTRIBUTING.md

**Files:**
- Create: `CONTRIBUTING.md`

- [x] write a brief guide: build/test/vet commands (from CLAUDE.md), the "every change needs tests / all tests pass before merge" rule, and the `docs/plans/` planning workflow
- [x] note the Go version requirement and how to run the TUI locally
- [x] verify: links and commands are accurate against the current Makefile

### Task 9: Verify acceptance criteria
- [x] LICENSE present, MIT, correct copyright holder confirmed — MIT text, `Copyright (c) 2026 mmaksmas`; legal-name confirmation remains a Post-Completion item (acceptable)
- [x] `tasks.txt` no longer tracked; tree clean — `git ls-files | grep tasks.txt` returns nothing
- [x] `.goreleaser.yaml` passes `goreleaser check`; snapshot build produces darwin+linux/amd64+arm64 archives + checksums — `goreleaser check` (v2.16.0) now exits 0 clean with NO deprecation warnings after migrating `brews:` → `homebrew_casks:`. Snapshot build PASSES (all 4 archives + `checksums.txt` + generated cask `dist/homebrew/Casks/monolog.rb` produced via `goreleaser release --snapshot --clean`). Still noted: local git remote is `maksmas/monolog` (module/config use `mmaksmas`), so the snapshot cask download URL renders `github.com/maksmas/...` — a local-remote mismatch, not a `.goreleaser.yaml` defect (human to resolve the maksmas vs mmaksmas owner question).
- [x] both workflows lint clean; `ci.yml` mirrors `go build/test/vet` — `actionlint 1.7.12` ran clean on both workflows; `ci.yml` runs `go build/test/vet` with `go-version-file: go.mod`
- [x] README leads with value + demo gif; install order is brew → binary → go install — verified
- [x] CONTRIBUTING present and accurate — verified against Makefile/go.mod
- [x] run full suite: `go test ./...` and `go vet ./...` — all green — build/vet/test all pass

### Task 10: Update documentation
- [x] update `CLAUDE.md` if any new patterns/conventions emerged (e.g. release/versioning workflow, demo regeneration) — added a "Release & distribution" subsection under Build & Test Commands covering ldflags version injection, `v*`-tag → GoReleaser → GitHub Release + Homebrew cask, `make demo` GIF regeneration, and the `ci.yml` build/test/vet check
- [x] ensure README/CONTRIBUTING cross-links are consistent — verified README links to CONTRIBUTING.md + LICENSE and CONTRIBUTING links to LICENSE; all relative paths resolve (no fixes needed)
- [x] move this plan to `docs/plans/completed/` (handled by exec orchestrator in the finalize step)

## Post-Completion
*Manual / external actions — no checkboxes, informational only.*

**Manual GitHub setup (author, one-time, cannot be automated here):**
- Create the tap repo `github.com/mmaksmas/homebrew-tap` (public, empty).
- Create a PAT with `repo` scope on the tap repo; add it as Actions secret
  `HOMEBREW_TAP_GITHUB_TOKEN` on the monolog repo.
- Confirm the legal name for the MIT copyright line before publishing.

**First release (author):**
- Install `vhs` (`brew install vhs`) and run `make demo` to render `docs/img/demo.gif`;
  commit it (this is the deferred generation of the README hero from Task 6).
- Install `goreleaser` locally to dry-run `goreleaser release --snapshot --clean`
  before the first real tag.
- Tag `v0.1.0` and push the tag to trigger `release.yml`; verify the GitHub Release
  archives + checksums and that the formula lands in the tap.
- Smoke-test `brew install mmaksmas/tap/monolog` on a clean machine (darwin + linux).

**Deferred (already captured as `someday` tasks in the author's backlog):**
- Bulk operations (archive / purge / clear completed, multi-select).
- Export command (JSON → md/csv; possible import from other tools).
