package recurrence

import "strings"

// GrammarHint is the one-line human-readable form reference shown in TUI
// hint lines, YAML edit buffer header comments, and CLI --recur help text.
// Keep this in sync with Parse.
const GrammarHint = "monthly:N | weekly:<day> | workdays | days:N"

// maxSuggestResults caps the number of completion candidates returned by
// Suggest. It mirrors the convention used by model.FilterTags.
const maxSuggestResults = 5

// topLevelCandidates is the deterministic completion set surfaced when the
// user's input does not yet contain a colon. Order is fixed: monthly:N,
// weekly:, workdays, days:N.
var topLevelCandidates = []string{
	"monthly:N",
	"weekly:",
	"workdays",
	"days:N",
}

// weeklyCandidates is the deterministic list of canonical three-letter
// weekday completions for the "weekly:" prefix. Ordered Mon..Sun to match
// typical week conventions.
var weeklyCandidates = []string{
	"weekly:mon",
	"weekly:tue",
	"weekly:wed",
	"weekly:thu",
	"weekly:fri",
	"weekly:sat",
	"weekly:sun",
}

// Suggest returns up to maxSuggestResults completion candidates for the
// given partial input, matching case-insensitively on prefix. The function
// is pure and has no dependency on Model or time.
//
// Empty input returns nil (the caller should not show a dropdown without
// user intent). For non-empty input with no matches the return is also nil.
//
// Completion rules:
//   - Input without a colon: filter topLevelCandidates by prefix.
//   - Input starting with "weekly:": filter weeklyCandidates by prefix.
//   - Input starting with "monthly:" or "days:": surface the single
//     template form ("monthly:N" or "days:N") — the user types the number
//     themselves; numeric suggestions would be noisy.
func Suggest(input string) []string {
	if input == "" {
		return nil
	}
	lower := strings.ToLower(input)

	// Dispatch on the presence of a colon.
	idx := strings.Index(lower, ":")
	if idx < 0 {
		// Top-level: filter by prefix.
		return filterByPrefix(topLevelCandidates, lower)
	}

	kind := lower[:idx]
	switch kind {
	case "weekly":
		return filterByPrefix(weeklyCandidates, lower)
	case "monthly":
		// Single template; only surface it while it still prefix-matches
		// the canonical template ("monthly:n"). Any non-digit or partial
		// digit entry still matches the template because the template ends
		// with a literal "N" — we compare only the kind prefix.
		return []string{"monthly:N"}
	case "days":
		return []string{"days:N"}
	default:
		return nil
	}
}

// filterByPrefix returns candidates whose lowercased form starts with the
// already-lowercased prefix, capped at maxSuggestResults. Preserves input
// ordering from candidates.
func filterByPrefix(candidates []string, prefixLower string) []string {
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), prefixLower) {
			out = append(out, c)
			if len(out) >= maxSuggestResults {
				break
			}
		}
	}
	return out
}
