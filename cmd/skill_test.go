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

	"github.com/spf13/cobra"
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
	if strings.TrimSpace(fm.AllowedTools) == "" {
		t.Error("frontmatter allowed-tools is empty; the skill only ever shells out, so it should declare Bash")
	}
	if strings.TrimSpace(body) == "" {
		t.Error("SKILL.md has frontmatter but no body")
	}
}

// inlineCodeRe matches a single-line inline code span (`like this`).
var inlineCodeRe = regexp.MustCompile("`([^`\n]+)`")

// stripShellComment drops a trailing `# ...` comment. Comments are prose and
// may legitimately mention flags that are not being invoked.
func stripShellComment(line string) string {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return ""
	}
	if i := strings.Index(line, " #"); i >= 0 {
		return line[:i]
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
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			pending = ""
			continue
		}
		if inFence {
			cur := strings.TrimSpace(stripShellComment(line))
			if strings.HasSuffix(cur, "\\") {
				pending += strings.TrimSuffix(cur, "\\") + " "
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
	return out
}

// findSubcommand looks up a direct child of cmd by name or alias.
func findSubcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

// hasFlag reports whether cmd accepts the given long flag, counting flags
// inherited from parents.
func hasFlag(cmd *cobra.Command, name string) bool {
	cmd.InitDefaultHelpFlag()
	if cmd.Flags().Lookup(name) != nil {
		return true
	}
	return cmd.InheritedFlags().Lookup(name) != nil
}

// hasShorthand reports whether cmd accepts the given single-letter flag,
// counting shorthands inherited from parents.
func hasShorthand(cmd *cobra.Command, letter string) bool {
	cmd.InitDefaultHelpFlag()
	if cmd.Flags().ShorthandLookup(letter) != nil {
		return true
	}
	return cmd.InheritedFlags().ShorthandLookup(letter) != nil
}

// isPlaceholder reports whether a token is a documentation placeholder such
// as <id> or <query> rather than a literal argument.
func isPlaceholder(tok string) bool {
	return strings.HasPrefix(tok, "<")
}

// checkDocumentedCommands validates every `monolog ...` invocation in md
// against the live cobra tree, returning the set of subcommand paths it
// actually checked so the caller can prove the extraction was not vacuous.
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

		root := NewRootCmd()
		root.InitDefaultVersionFlag()

		target := root
		rest := fields[1:]

		if !strings.HasPrefix(rest[0], "-") {
			if isPlaceholder(rest[0]) {
				// `monolog <subcommand>` is a template, not an invocation.
				continue
			}
			sub := findSubcommand(root, rest[0])
			if sub == nil {
				t.Errorf("%s documents %q but %q is not a monolog subcommand (in: %s)", label, "monolog "+rest[0], rest[0], cand)
				continue
			}
			target = sub
			rest = rest[1:]
			// Walk one more level for parent commands like `email` /
			// `telegram`, e.g. `monolog telegram status`.
			if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") && !isPlaceholder(rest[0]) && target.HasSubCommands() {
				if child := findSubcommand(target, rest[0]); child != nil {
					target = child
					rest = rest[1:]
				}
			}
		}
		seen[target.CommandPath()] = true

		for _, tok := range rest {
			if !strings.HasPrefix(tok, "-") || tok == "-" || tok == "--" {
				continue
			}
			// `--limit=25` and `--limit 25` are the same flag.
			name := tok
			if i := strings.Index(name, "="); i >= 0 {
				name = name[:i]
			}
			if strings.HasPrefix(name, "--") {
				long := strings.TrimPrefix(name, "--")
				if !hasFlag(target, long) {
					t.Errorf("%s documents flag --%s on %q, which has no such flag (in: %s)", label, long, target.CommandPath(), cand)
				}
				continue
			}
			// Shorthand, possibly clustered (-af).
			for _, r := range strings.TrimPrefix(name, "-") {
				letter := string(r)
				if !hasShorthand(target, letter) {
					t.Errorf("%s documents shorthand -%s on %q, which has no such flag (in: %s)", label, letter, target.CommandPath(), cand)
				}
			}
		}
	}

	return seen
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
	want := []string{
		"monolog add",
		"monolog ls",
		"monolog search",
		"monolog note",
		"monolog show",
		"monolog log",
	}
	for _, path := range want {
		if !seen[path] {
			t.Errorf("SKILL.md no longer documents %q (or the extractor stopped seeing it); found: %s", path, sortedKeys(seen))
		}
	}
	if len(seen) < len(want) {
		t.Errorf("only %d distinct commands extracted from SKILL.md, expected at least %d: %s", len(seen), len(want), sortedKeys(seen))
	}
}

// TestSkillReadmeDocumentsOnlyRealCommands applies the same cross-check to
// the install README, which shows the quarantine and queue-draining commands.
func TestSkillReadmeDocumentsOnlyRealCommands(t *testing.T) {
	checkDocumentedCommands(t, "docs/claude-skill/README.md", readSkillDoc(t, "README.md"))
}

func sortedKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%v", keys)
}
