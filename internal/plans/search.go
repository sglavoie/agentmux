package plans

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
)

// SortMode selects the ordering applied to search results. Zero value is
// SortByMatches so callers that never set Query.Sort keep today's behaviour.
type SortMode int

const (
	// SortByMatches orders by Matches DESC, tie-broken by ModTime DESC.
	SortByMatches SortMode = iota
	// SortByModTime orders by ModTime DESC, tie-broken by Matches DESC.
	SortByModTime
)

// Query describes a plan-search request. An empty Text returns every
// indexed plan with Matches=0 and Preview falling back to the cached
// snippet, sorted by ModTime descending.
type Query struct {
	Text string
	Sort SortMode
}

// Result is one matching plan. Matches is the case-insensitive substring
// count across the plan's search corpus. Preview is the source line
// containing the first match — when the cached snippet matches it is
// preferred as the preview; otherwise sidecar matches use a lowercased
// line (the sidecar is stored lowercased and is the only source available
// without re-reading every .md file).
type Result struct {
	Meta    PlanMeta
	Matches int
	Preview string
}

// Search runs q against idx. The compatibility path tolerates per-plan
// sidecar read failures; SQLite uses the durable repository search document.
func Search(idx *Index, q Query) ([]Result, error) {
	if idx == nil {
		return nil, errors.New("plans: Search requires a non-nil index")
	}
	if idx.UsesSQLite() {
		return searchSQLite(idx, q)
	}

	metas := idx.List()
	needle := strings.ToLower(strings.TrimSpace(q.Text))
	results := make([]Result, 0, len(metas))

	if needle == "" {
		for _, m := range metas {
			results = append(results, Result{Meta: m, Preview: m.Snippet})
		}
		// metas already arrives sorted by ModTime desc from idx.List();
		// SortResults preserves that order (Matches is zero for every
		// result, so the tie-break falls through to ModTime).
		SortResults(results, q.Sort)
		return results, nil
	}

	for _, m := range metas {
		// Snippets are stored as written (not lowercased), so lowercase
		// here for case-insensitive comparison. A matching snippet is a
		// preview preference; when the sidecar exists, it still supplies
		// the exact match count for ranking.
		snippetPreview := ""
		if m.Snippet != "" && strings.Contains(strings.ToLower(m.Snippet), needle) {
			snippetPreview = m.Snippet
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
			if snippetPreview != "" {
				results = append(results, Result{Meta: m, Matches: 1, Preview: snippetPreview})
			}
			continue
		}
		count, preview := countMatchesInSearchText(searchText, needle)
		if count == 0 {
			continue
		}
		if snippetPreview != "" {
			preview = snippetPreview
		}
		results = append(results, Result{Meta: m, Matches: count, Preview: preview})
	}

	SortResults(results, q.Sort)
	return results, nil
}

// searchSQLite preserves the legacy literal substring count, preview, and
// stable ordering behavior while reading the durable search corpus. A missing
// document retains the existing snippet-only fallback; it never rewrites a
// compatibility sidecar or reinterprets a plan directory during a search.
func searchSQLite(idx *Index, q Query) ([]Result, error) {
	repo := idx.repository()
	if repo == nil {
		return nil, errors.New("plans: SQLite repository is not enabled")
	}
	metas, err := repo.List(context.Background())
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(q.Text))
	results := make([]Result, 0, len(metas))
	if needle == "" {
		for _, meta := range metas {
			results = append(results, Result{Meta: meta, Preview: meta.Snippet})
		}
		SortResults(results, q.Sort)
		return results, nil
	}
	for _, meta := range metas {
		snippetPreview := ""
		if meta.Snippet != "" && strings.Contains(strings.ToLower(meta.Snippet), needle) {
			snippetPreview = meta.Snippet
		}
		searchText, found, err := repo.SearchText(context.Background(), PlanKey(meta.DirPath))
		if err != nil {
			return nil, err
		}
		if !found {
			if snippetPreview != "" {
				results = append(results, Result{Meta: meta, Matches: 1, Preview: snippetPreview})
			}
			continue
		}
		count, preview := countMatchesInSearchText(searchText, needle)
		if count == 0 {
			continue
		}
		if snippetPreview != "" {
			preview = snippetPreview
		}
		results = append(results, Result{Meta: meta, Matches: count, Preview: preview})
	}
	SortResults(results, q.Sort)
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

// SortResults orders rs in place according to mode. Output is stable so
// tests and the TUI list see deterministic ordering on every call.
func SortResults(rs []Result, mode SortMode) {
	switch mode {
	case SortByModTime:
		sort.SliceStable(rs, func(a, b int) bool {
			if !rs[a].Meta.ModTime.Equal(rs[b].Meta.ModTime) {
				return rs[a].Meta.ModTime.After(rs[b].Meta.ModTime)
			}
			return rs[a].Matches > rs[b].Matches
		})
	default:
		sort.SliceStable(rs, func(a, b int) bool {
			if rs[a].Matches != rs[b].Matches {
				return rs[a].Matches > rs[b].Matches
			}
			return rs[a].Meta.ModTime.After(rs[b].Meta.ModTime)
		})
	}
}
