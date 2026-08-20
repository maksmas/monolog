package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// skillRepoRoot resolves the repository root from this test file's own path,
// so the tests below work regardless of where `go test` is invoked from.
// Mirrors the approach in telegram_deploy_test.go.
func skillRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate test file path")
	}
	// thisFile is .../monolog/cmd/skill_test.go — repo root is two levels up.
	return filepath.Dir(filepath.Dir(thisFile))
}

// readSkillDoc reads one of the docs/claude-skill/ files, failing the test if
// it is missing or empty.
func readSkillDoc(t *testing.T, name string) string {
	t.Helper()
	full := filepath.Join(skillRepoRoot(t), "docs", "claude-skill", name)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("skill artifact %q missing: %v", name, err)
	}
	if len(data) == 0 {
		t.Fatalf("skill artifact %q is empty", name)
	}
	return string(data)
}

// TestSkillArtifactsExist checks that the shipped Claude Code skill and its
// install README are actually checked in. Both are referenced from CLAUDE.md
// and README.md; renaming or moving one breaks the docs silently.
func TestSkillArtifactsExist(t *testing.T) {
	for _, name := range []string{"SKILL.md", "README.md"} {
		full := filepath.Join(skillRepoRoot(t), "docs", "claude-skill", name)
		info, err := os.Stat(full)
		if err != nil {
			t.Errorf("skill artifact %q missing: %v", name, err)
			continue
		}
		if info.IsDir() {
			t.Errorf("skill artifact %q is a directory, expected a file", name)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("skill artifact %q is empty", name)
		}
	}
}

// skillFrontmatter is the subset of the SKILL.md YAML header this project
// cares about. `description` is the entire trigger mechanism for the skill
// (there is no CLAUDE.md primer), so an empty or unparseable one means the
// skill silently never fires.
type skillFrontmatter struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	AllowedTools string `yaml:"allowed-tools"`
}

// splitFrontmatter separates a leading `---`-delimited YAML block from the
// markdown body. Returns ok=false when the document has no frontmatter.
func splitFrontmatter(src string) (frontmatter, body string, ok bool) {
	const delim = "---"
	// Tolerate a UTF-8 BOM and CRLF line endings.
	src = strings.TrimPrefix(src, string(rune(0xFEFF)))
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if !strings.HasPrefix(src, delim+"\n") {
		return "", src, false
	}
	rest := src[len(delim)+1:]
	end := strings.Index(rest, "\n"+delim)
	if end < 0 {
		return "", src, false
	}
	frontmatter = rest[:end]
	body = rest[end+1+len(delim):]
	return frontmatter, body, true
}

// TestSkillFrontmatterParses pins the SKILL.md header: it must be valid YAML
// with a non-empty name and description, since Claude Code refuses to load a
// skill whose frontmatter does not parse.
func TestSkillFrontmatterParses(t *testing.T) {
	src := readSkillDoc(t, "SKILL.md")

	fmText, body, ok := splitFrontmatter(src)
	if !ok {
		t.Fatal("SKILL.md has no leading --- frontmatter block")
	}

	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		t.Fatalf("SKILL.md frontmatter is not valid YAML: %v", err)
	}

	if fm.Name != "monolog" {
		t.Errorf("frontmatter name = %q, want %q (the skill directory and name must agree)", fm.Name, "monolog")
	}
	if strings.TrimSpace(fm.Description) == "" {
		t.Error("frontmatter description is empty; it is the only thing that triggers the skill")
	}
	// The description carries the trigger phrases and the proactive-use
	// permission. A one-liner means someone paraphrased it away.
	if n := len(fm.Description); n < 200 {
		t.Errorf("frontmatter description is %d chars; expected the full trigger paragraph (>=200)", n)
	}
	// Without a CLAUDE.md primer this paragraph is the entire trigger
	// mechanism, so its load-bearing parts are pinned rather than left to a
	// future editor's judgement: the colloquial trigger phrases users
	// actually say, the explicit permission to act unprompted, and the
	// closing cost-asymmetry line.
	for _, frag := range []string{
		`"add this to my backlog"`,
		`"put that in mlog"`,
		`"what's on my plate"`,
		`"anything in mlog about X"`,
		"ALSO use proactively, without being asked",
		`"not now"`,
		"Filing is cheap and quarantined",
	} {
		if !strings.Contains(fm.Description, frag) {
			t.Errorf("frontmatter description is missing the load-bearing fragment %q", frag)
		}
	}
	checkAllowedTools(t, fm.AllowedTools)

	if strings.TrimSpace(body) == "" {
		t.Error("SKILL.md has frontmatter but no body")
	}
}

// bareBashGrantRe matches an unscoped `Bash` entry in an allowed-tools list —
// a `Bash` token that is not followed by a `(...)` argument pattern.
var bareBashGrantRe = regexp.MustCompile(`(^|[\s,])Bash([\s,]|$)`)

// skillGrantedSubcommands are the read/write commands the skill documents, and
// the only ones its allowed-tools list may pre-approve.
var skillGrantedSubcommands = []string{"add", "note", "search", "ls", "show", "log"}

// skillProhibitedSubcommands are the commands SKILL.md tells Claude never to
// run unprompted (or at all). None may appear in allowed-tools.
var skillProhibitedSubcommands = []string{"done", "edit", "rm", "mv", "sync", "init", "email", "telegram"}

// checkAllowedTools pins the frontmatter grant to the exact command surface
// the skill documents.
//
// This matters because allowed-tools PRE-APPROVES the listed calls; it does
// not restrict anything. A bare `Bash` entry therefore waives the permission
// prompt for every shell command while the skill is loaded, which would leave
// SKILL.md's "never run done/edit/rm/mv/sync" prose with no enforcement behind
// it at all. Scoping the grant to `Bash(monolog <sub> *)` keeps the prohibited
// subcommands hitting the normal prompt.
func checkAllowedTools(t *testing.T, allowed string) {
	t.Helper()

	if strings.TrimSpace(allowed) == "" {
		t.Fatal("frontmatter allowed-tools is empty; the skill only ever shells out, so it should declare its monolog commands")
	}
	if bareBashGrantRe.MatchString(allowed) {
		t.Errorf("allowed-tools grants bare Bash (%q). allowed-tools pre-approves rather than restricts, so this waives the permission prompt for every shell command — including the done/edit/rm/mv/sync calls the skill body forbids. Scope it to Bash(monolog <sub> *) entries.", allowed)
	}
	for _, sub := range skillGrantedSubcommands {
		want := "Bash(monolog " + sub + " *)"
		if !strings.Contains(allowed, want) {
			t.Errorf("allowed-tools is missing %q, so that documented command still prompts; got %q", want, allowed)
		}
	}
	for _, sub := range skillProhibitedSubcommands {
		if strings.Contains(allowed, "Bash(monolog "+sub) {
			t.Errorf("allowed-tools pre-approves `monolog %s`, which the skill body forbids; got %q", sub, allowed)
		}
	}
}

// inlineCodeRe matches a single-line inline code span (`like this`).
var inlineCodeRe = regexp.MustCompile("`([^`\n]+)`")

// stripShellComment drops a trailing `# ...` comment. Comments are prose and
// may legitimately mention flags that are not being invoked.
//
// A `#` only starts a comment at a word boundary and outside quotes, so a
// documented command like `monolog add "fix #123"` keeps its argument.
func stripShellComment(line string) string {
	var quote rune
	atBoundary := true // start of line is a word boundary
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '#' && atBoundary:
			return line[:i]
		}
		atBoundary = r == ' ' || r == '\t'
	}
	return line
}

// commandCandidates pulls every shell-ish snippet out of a markdown document:
// each command inside a fenced code block, plus the contents of every inline
// code span. Backslash continuations inside fences are joined so one command
// stays one candidate, and trailing comments are dropped.
func commandCandidates(md string) []string {
	var (
		out     []string
		inFence bool
		pending string
	)
	// flushPending emits a continuation that never got its final unescaped
	// line — e.g. a fence whose last line ends in a backslash. Dropping it
	// would silently skip the command instead of failing on it.
	flushPending := func() {
		if full := strings.TrimSpace(pending); full != "" {
			out = append(out, full)
		}
		pending = ""
	}

	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				flushPending()
			}
			inFence = !inFence
			pending = ""
			continue
		}
		if inFence {
			cur := strings.TrimSpace(stripShellComment(line))
			if strings.HasSuffix(cur, "\\") {
				pending += strings.TrimSpace(strings.TrimSuffix(cur, "\\")) + " "
				continue
			}
			full := strings.TrimSpace(pending + cur)
			pending = ""
			if full != "" {
				out = append(out, full)
			}
			continue
		}
		for _, m := range inlineCodeRe.FindAllStringSubmatch(line, -1) {
			if span := strings.TrimSpace(stripShellComment(m[1])); span != "" {
				out = append(out, span)
			}
		}
	}
	flushPending()
	return out
}

// isPlaceholder reports whether a token is a documentation placeholder such
// as <id> or <query> rather than a literal argument.
func isPlaceholder(tok string) bool {
	return strings.HasPrefix(tok, "<")
}

// checkDocumentedCommands validates every `monolog ...` invocation in md
// against the live cobra tree, returning the set of subcommand paths it
// actually checked so the caller can prove the extraction was not vacuous.
//
// Resolution is delegated to cobra itself — Find walks the command tree to any
// depth and honours aliases, and ParseFlags is the same pflag parser the real
// binary runs, so long flags, shorthands, clusters, `--flag=value` and `--`
// all behave exactly as they would on the command line. A hand-rolled
// equivalent drifts from the CLI, which is the one thing this test exists to
// prevent.
func checkDocumentedCommands(t *testing.T, label, md string) map[string]bool {
	t.Helper()

	seen := map[string]bool{}

	for _, cand := range commandCandidates(md) {
		fields := strings.Fields(cand)
		if len(fields) == 0 || fields[0] != "monolog" {
			continue
		}
		// A bare `monolog` reference is the TUI; there is nothing to resolve.
		if len(fields) == 1 {
			continue
		}
		// `monolog <subcommand>` is a template, not an invocation.
		if isPlaceholder(fields[1]) {
			continue
		}

		root := NewRootCmd()

		target, rest, err := root.Find(fields[1:])
		if err != nil {
			t.Errorf("%s documents %q, which does not resolve: %v (in: %s)", label, cand, err, cand)
			continue
		}
		if target == root && !strings.HasPrefix(fields[1], "-") {
			t.Errorf("%s documents %q but %q is not a monolog subcommand (in: %s)", label, "monolog "+fields[1], fields[1], cand)
			continue
		}
		seen[target.CommandPath()] = true

		// --help is not documented anywhere, but registering it keeps the
		// parser's flag set identical to the one a real invocation sees.
		target.InitDefaultHelpFlag()
		if err := target.ParseFlags(rest); err != nil {
			t.Errorf("%s documents flags the CLI rejects on %q: %v (in: %s)", label, target.CommandPath(), err, cand)
			continue
		}

		// Find stops at the deepest command it recognises and hands back the
		// rest, and cobra only reports an unknown subcommand for the *root*.
		// A grouping command like `email` or `telegram` takes no positional
		// args of its own, so anything left over there is a subcommand that
		// does not exist (`monolog email snyc`).
		if target.HasSubCommands() {
			for _, pos := range target.Flags().Args() {
				if isPlaceholder(pos) {
					continue
				}
				t.Errorf("%s documents %q but %q is not a subcommand of %q (in: %s)", label, cand, pos, target.CommandPath(), cand)
				break
			}
		}
	}

	return seen
}

// skillFloorCommands are the invocations every shipped skill doc must still
// contain. Without this floor an extractor that stops matching (a fence-style
// change, a renamed binary) turns the cross-check into a vacuous pass.
var skillFloorCommands = []string{
	"monolog add",
	"monolog ls",
	"monolog search",
	"monolog note",
}

// assertFloor fails when the extractor did not reach the named commands.
func assertFloor(t *testing.T, label string, seen map[string]bool, want []string) {
	t.Helper()
	for _, path := range want {
		if !seen[path] {
			t.Errorf("%s no longer documents %q (or the extractor stopped seeing it); found: %s", label, path, sortedKeys(seen))
		}
	}
}

// TestSkillDocumentsOnlyRealCommands is the anti-rot check: every
// `monolog <subcommand>` and every `--flag`/`-x` the shipped skill documents
// is resolved against the live cobra tree. Renaming or dropping a flag fails
// this test instead of leaving the skill quietly wrong in every future
// Claude Code session.
func TestSkillDocumentsOnlyRealCommands(t *testing.T) {
	src := readSkillDoc(t, "SKILL.md")
	_, body, ok := splitFrontmatter(src)
	if !ok {
		t.Fatal("SKILL.md has no leading --- frontmatter block")
	}

	seen := checkDocumentedCommands(t, "SKILL.md", body)

	// Guard against a silently vacuous pass: if the extractor stops finding
	// commands (a fence style change, say) the loop above would report
	// nothing at all.
	assertFloor(t, "SKILL.md", seen, append(append([]string{}, skillFloorCommands...), "monolog show", "monolog log"))
}

// TestSkillReadmeDocumentsOnlyRealCommands applies the same cross-check to
// the install README, which shows the quarantine and queue-draining commands.
func TestSkillReadmeDocumentsOnlyRealCommands(t *testing.T) {
	seen := checkDocumentedCommands(t, "docs/claude-skill/README.md", readSkillDoc(t, "README.md"))

	// Same floor as SKILL.md, minus show/log which the README has no reason to
	// mention. Without it this half of the check passes vacuously: rewriting
	// every `monolog ` in the README to something else would still be green.
	assertFloor(t, "docs/claude-skill/README.md", seen, skillFloorCommands)
}

func TestStripShellComment(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no comment", `monolog ls -a`, `monolog ls -a`},
		{"trailing comment", `monolog ls -a    # all open tasks`, `monolog ls -a    `},
		{"whole-line comment", `# just prose`, ``},
		{"indented whole-line comment", `   # just prose`, `   `},
		{"hash inside double quotes", `monolog add "fix #123 in parser" -s week`, `monolog add "fix #123 in parser" -s week`},
		{"hash inside single quotes", `monolog add 'ticket #7' # real comment`, `monolog add 'ticket #7' `},
		{"hash glued to a word", `monolog search abc#def`, `monolog search abc#def`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripShellComment(tt.in); got != tt.want {
				t.Errorf("stripShellComment(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCommandCandidates(t *testing.T) {
	t.Run("joins backslash continuations", func(t *testing.T) {
		md := "```sh\nmonolog add \"t\" \\\n  --tags claude\n```\n"
		got := commandCandidates(md)
		want := []string{`monolog add "t" --tags claude`}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("flushes a continuation left dangling at the closing fence", func(t *testing.T) {
		// A trailing backslash on the last line of a fence used to discard the
		// whole command, so a bogus flag in it would never be checked.
		md := "```sh\nmonolog search \"x\" \\\n```\n"
		got := commandCandidates(md)
		if len(got) != 1 || !strings.HasPrefix(got[0], "monolog search") {
			t.Errorf("got %q, want the dangling command to survive", got)
		}
	})

	t.Run("keeps quoted hashes out of comment stripping", func(t *testing.T) {
		md := "```sh\nmonolog add \"fix #123\" --tags claude   # provenance\n```\n"
		got := commandCandidates(md)
		if len(got) != 1 {
			t.Fatalf("got %q, want 1 candidate", got)
		}
		if !strings.Contains(got[0], "#123") {
			t.Errorf("quoted hash was stripped: %q", got[0])
		}
		if strings.Contains(got[0], "provenance") {
			t.Errorf("trailing comment survived: %q", got[0])
		}
	})

	t.Run("reads inline code spans outside fences", func(t *testing.T) {
		got := commandCandidates("Use `monolog log` to see recent work.\n")
		if len(got) != 1 || got[0] != "monolog log" {
			t.Errorf("got %q, want [monolog log]", got)
		}
	})
}

func sortedKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%v", keys)
}
