package plans

import (
	"errors"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
)

// Query describes a plan-search request. An empty Text returns every
// indexed plan with Matches=0 and Preview falling back to the cached
// snippet, sorted by ModTime descending.
type Query struct {
	Text string
}

// Result is one matching plan. Matches is the case-insensitive substring
// count across the plan's search corpus (snippet short-circuits to 1).
// Preview is the source line containing the first match — for snippet
// short-circuits that's the snippet itself; for sidecar matches it's a
// lowercased line (the sidecar is stored lowercased and is the only
// source available without re-reading every .md file).
type Result struct {
	Meta    PlanMeta
	Matches int
	Preview string
}

// Search runs q against idx. Per-plan sidecar read errors are logged at
// debug level and the plan is dropped from the results rather than
// aborting the whole query — a vanished sidecar (raced with a pruner)
// must not crash the picker.
func Search(idx *Index, q Query) ([]Result, error) {
	if idx == nil {
		return nil, errors.New("plans: Search requires a non-nil index")
	}

	metas := idx.List()
	needle := strings.ToLower(strings.TrimSpace(q.Text))
	results := make([]Result, 0, len(metas))

	if needle == "" {
		for _, m := range metas {
			results = append(results, Result{Meta: m, Preview: m.Snippet})
		}
		// metas already arrives sorted by ModTime desc from idx.List();
		// sortResults preserves that order (Matches is zero for every
		// result, so the tie-break falls through to ModTime).
		sortResults(results)
		return results, nil
	}

	for _, m := range metas {
		// Snippet short-circuit: when the needle already appears in the
		// cached snippet we emit a result with Matches=1 and the snippet
		// as preview, skipping the sidecar read entirely. Trades exact
		// match count for a constant-time lookup. Snippets are stored
		// as written (not lowercased) so lowercase for compare.
		if m.Snippet != "" && strings.Contains(strings.ToLower(m.Snippet), needle) {
			results = append(results, Result{Meta: m, Matches: 1, Preview: m.Snippet})
			continue
		}
		key := PlanKey(m.DirPath)
		searchText, err := idx.store.Read(key)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				slog.Debug("plans.search: sidecar read failed",
					"path", idx.store.Path(key), "err", err)
			}
			// Missing sidecar: skip the entry. Rebuilding sidecars is
			// the index's job (Refresh); doing it here would risk
			// repeated rebuilds on every keystroke.
			continue
		}
		count, preview := countMatchesInSearchText(searchText, needle)
		if count == 0 {
			continue
		}
		results = append(results, Result{Meta: m, Matches: count, Preview: preview})
	}

	sortResults(results)
	return results, nil
}

// countMatchesInSearchText counts case-insensitive occurrences of needle
// inside the precomputed (already-lowercased) plan corpus. The preview
// is the single line containing the first match — necessarily
// lowercased, since the sidecar is the only source available without
// re-reading every .md file. needle must already be lowercased.
func countMatchesInSearchText(searchText, needle string) (int, string) {
	if needle == "" {
		return 0, ""
	}
	idx := 0
	first := -1
	total := 0
	for {
		pos := strings.Index(searchText[idx:], needle)
		if pos < 0 {
			break
		}
		absolute := idx + pos
		if first < 0 {
			first = absolute
		}
		total++
		idx = absolute + len(needle)
	}
	if total == 0 {
		return 0, ""
	}
	return total, firstMatchLine(searchText, first)
}

// firstMatchLine returns the single line of text containing the byte
// offset pos, truncated like a snippet.
func firstMatchLine(text string, pos int) string {
	if pos < 0 || pos >= len(text) {
		return truncateSnippet(text)
	}
	start := strings.LastIndexByte(text[:pos], '\n')
	if start < 0 {
		start = 0
	} else {
		start++
	}
	end := strings.IndexByte(text[pos:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += pos
	}
	return truncateSnippet(text[start:end])
}

// sortResults orders results by Matches desc, breaking ties with
// ModTime desc so output is deterministic for tests and the TUI list.
func sortResults(rs []Result) {
	sort.SliceStable(rs, func(a, b int) bool {
		if rs[a].Matches != rs[b].Matches {
			return rs[a].Matches > rs[b].Matches
		}
		return rs[a].Meta.ModTime.After(rs[b].Meta.ModTime)
	})
}
