package worktree

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

var (
	ErrSelectorNotFound  = errors.New("worktree selector not found")
	ErrSelectorAmbiguous = errors.New("worktree selector is ambiguous")
)

// SelectorError preserves deterministic candidates for CLI and TUI callers.
type SelectorError struct {
	Selector   string
	Candidates []Entry
	Err        error
}

func (e *SelectorError) Error() string {
	if errors.Is(e.Err, ErrSelectorAmbiguous) {
		return fmt.Sprintf("%q matches %d managed worktrees", e.Selector, len(e.Candidates))
	}
	return fmt.Sprintf("no managed worktree matches %q", e.Selector)
}

func (e *SelectorError) Unwrap() error { return e.Err }

// ResolveSelector resolves exact IDs before unique prefixes, branches, and
// canonical paths. Ambiguous candidates are sorted independently of input. A
// selector that matches nothing still carries candidates when some branch is a
// near miss of it, so a mistyped branch is answered with the one meant rather
// than only with a pointer at the full list. They are suggestions and nothing
// more: a not-found selector never resolves to one.
func ResolveSelector(entries []Entry, selector string) (Entry, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return Entry{}, &SelectorError{Selector: selector, Err: ErrSelectorNotFound}
	}
	var exact []Entry
	for _, entry := range entries {
		if strings.EqualFold(entry.ID, selector) {
			exact = append(exact, entry)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		sortSelectorCandidates(exact)
		return Entry{}, &SelectorError{Selector: selector, Candidates: exact, Err: ErrSelectorAmbiguous}
	}

	pathSelector := ""
	if filepath.IsAbs(selector) {
		pathSelector = canonicalAdjacentPath(selector)
	}
	matches := make(map[string]Entry)
	for _, entry := range entries {
		if strings.HasPrefix(strings.ToLower(entry.ID), strings.ToLower(selector)) ||
			entry.Branch == selector || (pathSelector != "" && filepath.Clean(entry.Path) == pathSelector) {
			matches[entry.ID+"\x00"+entry.Path] = entry
		}
	}
	if len(matches) == 1 {
		for _, entry := range matches {
			return entry, nil
		}
	}
	if len(matches) == 0 {
		return Entry{}, &SelectorError{Selector: selector, Candidates: nearSelectorMisses(entries, selector), Err: ErrSelectorNotFound}
	}
	candidates := make([]Entry, 0, len(matches))
	for _, entry := range matches {
		candidates = append(candidates, entry)
	}
	sortSelectorCandidates(candidates)
	return Entry{}, &SelectorError{Selector: selector, Candidates: candidates, Err: ErrSelectorAmbiguous}
}

func sortSelectorCandidates(candidates []Entry) {
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].ID != candidates[b].ID {
			return candidates[a].ID < candidates[b].ID
		}
		return candidates[a].Path < candidates[b].Path
	})
}

// nearSelectorMisses returns the worktrees whose branch the selector most
// likely meant: first every branch containing it, ignoring case, then every
// branch within a few edits of it — two, or a third of what was typed when that
// is more — closest first, with ties in selector order.
func nearSelectorMisses(entries []Entry, selector string) []Entry {
	typed := strings.ToLower(selector)
	limit := max(2, len([]rune(typed))/3)
	rank := make(map[string]int)
	var near []Entry
	for _, entry := range entries {
		branch := strings.ToLower(entry.Branch)
		if branch == "" {
			continue
		}
		score := -1
		if strings.Contains(branch, typed) {
			score = 0
		} else if d := editDistance(typed, branch); d <= limit {
			score = d
		}
		if score >= 0 {
			near = append(near, entry)
			rank[entry.ID+"\x00"+entry.Path] = score
		}
	}
	sortSelectorCandidates(near)
	sort.SliceStable(near, func(a, b int) bool {
		return rank[near[a].ID+"\x00"+near[a].Path] < rank[near[b].ID+"\x00"+near[b].Path]
	})
	return near
}

// editDistance is the Levenshtein distance between a and b, counted in runes.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	row := make([]int, len(br)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		diagonal := row[0]
		row[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			diagonal, row[j] = row[j], min(row[j]+1, row[j-1]+1, diagonal+cost)
		}
	}
	return row[len(br)]
}
