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
		return fmt.Sprintf("worktree: selector %q is ambiguous (%d candidates)", e.Selector, len(e.Candidates))
	}
	return fmt.Sprintf("worktree: selector %q was not found", e.Selector)
}

func (e *SelectorError) Unwrap() error { return e.Err }

// ResolveSelector resolves exact IDs before unique prefixes, branches, and
// canonical paths. Ambiguous candidates are sorted independently of input.
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
		return Entry{}, &SelectorError{Selector: selector, Err: ErrSelectorNotFound}
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
