package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unicode"

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
	Name         string       `yaml:"name"`
	Description  string       `yaml:"description"`
	AllowedTools allowedTools `yaml:"allowed-tools"`
}

// allowedTools is the parsed allowed-tools grant, one entry per permission
// rule.
//
// Claude Code's frontmatter reference states the field "accepts a space- or
// comma-separated string, or a YAML list", and shipped skills use all three
// shapes. Decoding into a plain string would therefore make this test's
// verdict depend on which shape the author picked — a YAML list would fail
// with "not valid YAML" when the YAML is perfectly fine — so the entries are
// normalized first and every check below runs against whole entries rather
// than a substring search over a raw blob.
type allowedTools []string

// UnmarshalYAML accepts either documented shape: a scalar string or a YAML
// sequence.
func (a *allowedTools) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		*a = splitToolRules(s)
		return nil
	case yaml.SequenceNode:
		var entries []string
		if err := value.Decode(&entries); err != nil {
			return err
		}
		out := make(allowedTools, 0, len(entries))
		for _, e := range entries {
			if e = strings.TrimSpace(e); e != "" {
				out = append(out, e)
			}
		}
		*a = out
		return nil
	default:
		return fmt.Errorf("allowed-tools must be a string or a list, got YAML node kind %d", value.Kind)
	}
}

// splitToolRules splits a scalar allowed-tools value into entries.
//
// The split has to be paren-aware. Both a space and a comma separate entries,
// but permission patterns legitimately contain spaces — `Bash(monolog add *)`
// is one rule, not three — so a separator only ends an entry at paren depth 0.
func splitToolRules(s string) []string {
	var (
		out   []string
		cur   strings.Builder
		depth int
	)
	flush := func() {
		if e := strings.TrimSpace(cur.String()); e != "" {
			out = append(out, e)
		}
		cur.Reset()
	}
	for _, r := range s {
		switch {
		case r == '(':
			depth++
			cur.WriteRune(r)
		case r == ')':
			if depth > 0 {
				depth--
			}
			cur.WriteRune(r)
		case depth == 0 && (r == ',' || unicode.IsSpace(r)):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// parseToolRule splits one allowed-tools entry into its tool name and its
// optional argument pattern. `Bash(monolog add *)` yields ("Bash",
// "monolog add *", true); a bare `Bash` yields ("Bash", "", false).
func parseToolRule(entry string) (name, pattern string, scoped bool) {
	open := strings.Index(entry, "(")
	if open < 0 || !strings.HasSuffix(entry, ")") {
		return strings.TrimSpace(entry), "", false
	}
	return strings.TrimSpace(entry[:open]), entry[open+1 : len(entry)-1], true
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

// skillGrantedSubcommands are the read/write commands the skill documents, and
// the only ones its allowed-tools list may pre-approve.
var skillGrantedSubcommands = []string{"add", "note", "search", "ls", "show", "log"}

// skillProhibitedSubcommands are the commands SKILL.md tells Claude never to
// run unprompted (or at all). None may appear in allowed-tools.
var skillProhibitedSubcommands = []string{"done", "edit", "rm", "mv", "sync", "init", "email", "telegram"}

// grantFor renders the allowed-tools entry that scopes a single monolog
// subcommand. The trailing " *" is a word-boundary wildcard: per Claude Code's
// permission rules it requires the prefix to be followed by a space or
// end-of-string, so it covers `monolog ls --tag x` and bare `monolog ls`
// alike, while never matching a different binary.
func grantFor(sub string) string { return "Bash(monolog " + sub + " *)" }

// allowedToolsProblems reports every reason an allowed-tools grant is not an
// acceptable surface for this skill, as human-readable messages.
//
// It is split out from checkAllowedTools so the bypass forms below can be
// unit-tested directly instead of being trusted.
//
// The check matters because allowed-tools PRE-APPROVES the listed calls; it
// does not restrict anything. Any grant that covers the whole Bash tool
// therefore waives the permission prompt for every shell command while the
// skill is loaded, leaving SKILL.md's "never run done/edit/rm/mv/sync" prose
// with no enforcement behind it. Three shapes do that and all are rejected:
// bare `Bash`, `Bash(*)` (which Claude Code's permission docs state "is
// equivalent to `Bash` and matches all Bash commands"), and any entry inside a
// YAML list, which an earlier substring-over-a-blob check could not see.
//
// Scoping is enforced as an allowlist rather than a blocklist: every Bash
// entry must be exactly one of the six documented grants. A blocklist of
// forbidden subcommands cannot catch `Bash(monolog *)`, which pre-approves
// every subcommand there is.
func allowedToolsProblems(allowed allowedTools) []string {
	var problems []string

	granted := make(map[string]bool, len(skillGrantedSubcommands))
	for _, sub := range skillGrantedSubcommands {
		granted[grantFor(sub)] = false
	}

	for _, entry := range allowed {
		name, pattern, scoped := parseToolRule(entry)
		if name != "Bash" {
			continue
		}
		if !scoped || strings.TrimSpace(pattern) == "*" {
			problems = append(problems, fmt.Sprintf(
				"entry %q grants the whole Bash tool (`Bash` and `Bash(*)` are equivalent). allowed-tools pre-approves rather than restricts, so this waives the permission prompt for every shell command — including the done/edit/rm/mv/sync calls the skill body forbids. Scope it to Bash(monolog <sub> *) entries.",
				entry))
			continue
		}
		if sub, ok := prohibitedSubcommand(pattern); ok {
			problems = append(problems, fmt.Sprintf(
				"entry %q pre-approves `monolog %s`, which the skill body forbids", entry, sub))
			continue
		}
		if _, ok := granted[entry]; !ok {
			problems = append(problems, fmt.Sprintf(
				"entry %q is outside the documented command surface; the only Bash grants allowed are %v", entry, allGrants()))
			continue
		}
		granted[entry] = true
	}

	for _, sub := range skillGrantedSubcommands {
		if !granted[grantFor(sub)] {
			problems = append(problems, fmt.Sprintf(
				"missing %q, so that documented command still prompts", grantFor(sub)))
		}
	}

	return problems
}

// allGrants lists the six acceptable Bash entries, for error messages.
func allGrants() []string {
	out := make([]string, 0, len(skillGrantedSubcommands))
	for _, sub := range skillGrantedSubcommands {
		out = append(out, grantFor(sub))
	}
	return out
}

// prohibitedSubcommand reports whether a Bash pattern targets one of the
// subcommands SKILL.md forbids. It exists purely for the sharper error
// message; the allowlist above would reject these anyway.
func prohibitedSubcommand(pattern string) (string, bool) {
	p := strings.TrimSpace(pattern)
	for _, sub := range skillProhibitedSubcommands {
		prefix := "monolog " + sub
		if p == prefix || strings.HasPrefix(p, prefix+" ") || strings.HasPrefix(p, prefix+":") {
			return sub, true
		}
	}
	return "", false
}

// checkAllowedTools pins the frontmatter grant to the exact command surface
// the skill documents.
func checkAllowedTools(t *testing.T, allowed allowedTools) {
	t.Helper()

	if len(allowed) == 0 {
		t.Fatal("frontmatter allowed-tools is empty; the skill only ever shells out, so it should declare its monolog commands")
	}
	for _, p := range allowedToolsProblems(allowed) {
		t.Errorf("allowed-tools %v: %s", []string(allowed), p)
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
// as <id> or <query> rather than a literal argument. Both brackets are
// required, so a stray "<" is treated as a real (and therefore checked)
// argument rather than waved through.
func isPlaceholder(tok string) bool {
	return strings.HasPrefix(tok, "<") && strings.HasSuffix(tok, ">") && len(tok) > 2
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

		// A fresh tree per candidate: ParseFlags below writes the parsed values
		// into the command's flag set, so reusing one root would leak `-n 25`
		// from one documented invocation into the next.
		root := NewRootCmd()

		target, rest, err := root.Find(fields[1:])
		if err != nil {
			t.Errorf("%s documents %q, which does not resolve: %v", label, cand, err)
			continue
		}
		if target == root && !strings.HasPrefix(fields[1], "-") {
			t.Errorf("%s documents %q but %q is not a monolog subcommand (in: %s)", label, "monolog "+fields[1], fields[1], cand)
			continue
		}
		seen[target.CommandPath()] = true

		// Cobra registers --help and --version during Execute, not at
		// construction, so the parser below would reject them without this.
		// The root README documents `monolog --version`.
		target.InitDefaultHelpFlag()
		target.InitDefaultVersionFlag()
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
				t.Errorf("%s documents %q but %q is not a subcommand of %q", label, cand, pos, target.CommandPath())
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
			t.Errorf("%s no longer documents %q (or the extractor stopped seeing it); found: %v", label, path, sortedKeys(seen))
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

// TestRootReadmeDocumentsOnlyRealCommands runs the same cross-check over the
// project README, which is the reference documentation for the whole CLI —
// every subcommand, every flag table, every example. It drifts for exactly the
// same reasons the skill docs do, and until now was outside the net.
func TestRootReadmeDocumentsOnlyRealCommands(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(skillRepoRoot(t), "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	seen := checkDocumentedCommands(t, "README.md", string(src))

	// A floor spanning the reference sections, so an extractor that stops
	// matching fails instead of passing vacuously.
	assertFloor(t, "README.md", seen, append(append([]string{}, skillFloorCommands...),
		"monolog show", "monolog log", "monolog done", "monolog rm", "monolog edit",
		"monolog mv", "monolog init", "monolog sync",
	))
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

func TestIsPlaceholder(t *testing.T) {
	tests := map[string]bool{
		"<id>":        true,
		"<query>":     true,
		"<id-prefix>": true,
		// Both brackets required: a lone "<" is a shell redirect, not a
		// placeholder, and waving it through would skip checking the command.
		"<":     false,
		"<id":   false,
		"id>":   false,
		"<>":    false,
		"login": false,
	}
	for tok, want := range tests {
		if got := isPlaceholder(tok); got != want {
			t.Errorf("isPlaceholder(%q) = %v, want %v", tok, got, want)
		}
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

// sortedKeys returns the map's keys in lexical order, for stable messages.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- allowed-tools parsing and grant scoping --------------------------------

// TestSplitToolRules pins the paren-aware scalar split. Claude Code documents
// both a space- and a comma-separated string, and its own example grant
// (`Bash(git add *) Bash(git commit *)`) contains spaces inside the patterns,
// so a naive strings.Fields would shred one rule into three.
func TestSplitToolRules(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"space separated with inner spaces",
			"Bash(monolog add *) Bash(monolog note *)",
			[]string{"Bash(monolog add *)", "Bash(monolog note *)"}},
		{"comma separated",
			"Read, Write, Bash(monolog ls *)",
			[]string{"Read", "Write", "Bash(monolog ls *)"}},
		{"comma and space mixed",
			"Read,  Bash(monolog add *),Bash(monolog log *)",
			[]string{"Read", "Bash(monolog add *)", "Bash(monolog log *)"}},
		{"single bare tool", "Bash", []string{"Bash"}},
		{"empty", "   ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitToolRules(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitToolRules(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

// documentedGrantsYAML renders the six documented grants in each frontmatter
// shape Claude Code accepts.
func documentedGrantsYAML() map[string]string {
	scalarSpace := strings.Join(allGrants(), " ")
	scalarComma := strings.Join(allGrants(), ", ")
	var block strings.Builder
	block.WriteString("allowed-tools:\n")
	for _, g := range allGrants() {
		block.WriteString("  - " + g + "\n")
	}
	var flow strings.Builder
	flow.WriteString("allowed-tools: [")
	for i, g := range allGrants() {
		if i > 0 {
			flow.WriteString(", ")
		}
		flow.WriteString(`"` + g + `"`)
	}
	flow.WriteString("]\n")

	return map[string]string{
		"space-separated scalar": "allowed-tools: " + scalarSpace + "\n",
		"comma-separated scalar": "allowed-tools: " + scalarComma + "\n",
		"block sequence":         block.String(),
		"flow sequence":          flow.String(),
	}
}

// TestAllowedToolsUnmarshal_AcceptsEveryDocumentedShape is the guard that
// blocked a legitimate edit before: the field used to decode into a plain
// string, so switching the frontmatter to a YAML list — a shape Claude Code
// explicitly accepts — failed with "SKILL.md frontmatter is not valid YAML"
// when the YAML was fine and the test's own struct was wrong.
func TestAllowedToolsUnmarshal_AcceptsEveryDocumentedShape(t *testing.T) {
	for name, src := range documentedGrantsYAML() {
		t.Run(name, func(t *testing.T) {
			var fm skillFrontmatter
			if err := yaml.Unmarshal([]byte(src), &fm); err != nil {
				t.Fatalf("shape %s should unmarshal, got: %v\nsource:\n%s", name, err, src)
			}
			if got := []string(fm.AllowedTools); !reflect.DeepEqual(got, allGrants()) {
				t.Errorf("shape %s parsed as %#v, want %#v", name, got, allGrants())
			}
			if problems := allowedToolsProblems(fm.AllowedTools); len(problems) != 0 {
				t.Errorf("shape %s should be an acceptable grant, got problems: %v", name, problems)
			}
		})
	}
}

// TestAllowedToolsProblems_RejectsWholeBashGrants is the bypass mutation check
// written down as a test.
//
// Every entry below pre-approves the whole Bash tool while still listing all
// six documented commands, so it satisfies a naive "are the six present?"
// check and grants unprompted access to every shell command — including the
// done/edit/rm/mv/sync calls SKILL.md forbids. The previous regex missed
// `Bash(*)` (which Claude Code's permission docs call equivalent to bare
// `Bash`) and could not see inside a YAML list at all.
func TestAllowedToolsProblems_RejectsWholeBashGrants(t *testing.T) {
	sneaky := []string{"Bash", "Bash(*)", "Bash( * )"}

	for _, bad := range sneaky {
		t.Run("scalar/"+bad, func(t *testing.T) {
			src := "allowed-tools: " + bad + " " + strings.Join(allGrants(), " ") + "\n"
			var fm skillFrontmatter
			if err := yaml.Unmarshal([]byte(src), &fm); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			problems := allowedToolsProblems(fm.AllowedTools)
			if len(problems) == 0 {
				t.Errorf("%q grants the whole Bash tool but was accepted; parsed entries: %#v",
					bad, []string(fm.AllowedTools))
			}
		})

		t.Run("sequence/"+bad, func(t *testing.T) {
			src := "allowed-tools:\n  - " + bad + "\n"
			for _, g := range allGrants() {
				src += "  - " + g + "\n"
			}
			var fm skillFrontmatter
			if err := yaml.Unmarshal([]byte(src), &fm); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if problems := allowedToolsProblems(fm.AllowedTools); len(problems) == 0 {
				t.Errorf("%q inside a YAML list grants the whole Bash tool but was accepted; parsed entries: %#v",
					bad, []string(fm.AllowedTools))
			}
		})
	}

	// The flow-list form the old regex also could not see.
	t.Run("flow list", func(t *testing.T) {
		var fm skillFrontmatter
		src := "allowed-tools: [Bash, Read]\n"
		if err := yaml.Unmarshal([]byte(src), &fm); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if problems := allowedToolsProblems(fm.AllowedTools); len(problems) == 0 {
			t.Errorf("flow list [Bash, Read] grants the whole Bash tool but was accepted")
		}
	})
}

// TestAllowedToolsProblems_RejectsBroadMonologGrant closes the hole a
// blocklist of forbidden subcommands cannot: `Bash(monolog *)` names none of
// them and pre-approves all of them. This is why the check is an allowlist.
func TestAllowedToolsProblems_RejectsBroadMonologGrant(t *testing.T) {
	allowed := append(allowedTools{"Bash(monolog *)"}, allGrants()...)
	if problems := allowedToolsProblems(allowed); len(problems) == 0 {
		t.Error("Bash(monolog *) pre-approves every subcommand but was accepted")
	}
}

// TestAllowedToolsProblems_RejectsProhibitedSubcommand covers the plain case:
// a grant for a command the skill body says never to run.
func TestAllowedToolsProblems_RejectsProhibitedSubcommand(t *testing.T) {
	for _, sub := range skillProhibitedSubcommands {
		allowed := append(allowedTools{grantFor(sub)}, allGrants()...)
		problems := allowedToolsProblems(allowed)
		if len(problems) == 0 {
			t.Errorf("grant for forbidden subcommand %q was accepted", sub)
			continue
		}
		if !strings.Contains(strings.Join(problems, "\n"), "monolog "+sub) {
			t.Errorf("problem for %q should name the subcommand, got: %v", sub, problems)
		}
	}
	// The `:*` spelling is documented as equivalent to a trailing ` *`.
	if problems := allowedToolsProblems(append(allowedTools{"Bash(monolog rm:*)"}, allGrants()...)); len(problems) == 0 {
		t.Error("Bash(monolog rm:*) was accepted")
	}
}

// TestAllowedToolsProblems_RequiresEveryDocumentedCommand is the other
// direction: dropping a grant leaves a documented command prompting on every
// use, which is the failure that makes the skill feel broken rather than
// unsafe.
func TestAllowedToolsProblems_RequiresEveryDocumentedCommand(t *testing.T) {
	if problems := allowedToolsProblems(allowedTools(allGrants())); len(problems) != 0 {
		t.Fatalf("the exact documented surface should be clean, got: %v", problems)
	}
	for i, sub := range skillGrantedSubcommands {
		short := append(allowedTools{}, allGrants()...)
		short = append(short[:i], short[i+1:]...)
		problems := allowedToolsProblems(short)
		if len(problems) == 0 {
			t.Errorf("dropping the grant for %q was accepted", sub)
			continue
		}
		if !strings.Contains(strings.Join(problems, "\n"), grantFor(sub)) {
			t.Errorf("problem for missing %q should name the grant, got: %v", sub, problems)
		}
	}
}
