package transcript

import (
	"errors"
	"log/slog"
	"sort"
	"strings"
)

// Query describes a search request against the transcript index. Empty
// slices mean "no filter on that dimension". An empty Text returns the
// filtered metadata with no message-level content scan.
type Query struct {
	Text    string
	Harness []string
	Profile []string
	Project []string
}

// Result is one matching session. Matches counts the total number of
// case-insensitive substring hits across all message texts in the session
// (or 0 when Query.Text is empty). Preview is the source line that
// contained the first match, truncated to snippetMaxLen runes; when
// Query.Text is empty, Preview falls back to the cached session snippet.
type Result struct {
	Meta    SessionMeta
	Matches int
	Preview string
}

// Search runs q against idx using readers (keyed by canonical harness
// name) to stream message content. Hidden entries are always excluded —
// search is a user-facing surface and the user has already chosen to
// hide them.
//
// Filtering happens in two passes: first the cheap chip filters against
// the in-memory index, then (only when Text is non-empty) a JSONL scan
// of each survivor via the matching Reader.ReadMessages. Per-session
// read errors are logged at debug level and the session is dropped from
// the results rather than aborting the whole query.
func Search(idx *Index, readers map[string]Reader, q Query) ([]Result, error) {
	if idx == nil {
		return nil, errors.New("transcript: Search requires a non-nil index")
	}

	metas := idx.List(ListFilter{
		Harness: q.Harness,
		Profile: q.Profile,
		Project: q.Project,
	})

	needle := strings.ToLower(strings.TrimSpace(q.Text))
	results := make([]Result, 0, len(metas))

	if needle == "" {
		for _, m := range metas {
			results = append(results, Result{Meta: m, Preview: m.Snippet})
		}
		sortResults(results)
		return results, nil
	}

	if readers == nil {
		return nil, errors.New("transcript: Search with text requires readers")
	}

	for _, m := range metas {
		reader, ok := readers[m.Harness]
		if !ok {
			slog.Debug("transcript.search: no reader for harness",
				"harness", m.Harness, "id", m.ID)
			continue
		}
		msgs, err := reader.ReadMessages(m.Path)
		if err != nil {
			slog.Debug("transcript.search: read failed",
				"path", m.Path, "err", err)
			continue
		}
		count, preview := countMatches(msgs, needle)
		if count == 0 {
			continue
		}
		results = append(results, Result{Meta: m, Matches: count, Preview: preview})
	}

	sortResults(results)
	return results, nil
}

// countMatches walks msgs in order, counting case-insensitive occurrences
// of needle (which is already lower-cased) across each message's Text.
// The first line that contains a match is captured as the preview, kept
// stable so callers see deterministic output for a given input.
func countMatches(msgs []Message, needle string) (int, string) {
	total := 0
	var preview string
	for _, msg := range msgs {
		lower := strings.ToLower(msg.Text)
		idx := 0
		for {
			pos := strings.Index(lower[idx:], needle)
			if pos < 0 {
				break
			}
			total++
			if preview == "" {
				preview = firstMatchLine(msg.Text, idx+pos)
			}
			idx += pos + len(needle)
		}
	}
	return total, preview
}

// firstMatchLine returns the single line of text containing the byte
// offset pos, truncated like a snippet. Callers pass the original
// (non-lowered) text so the returned preview preserves the user's case.
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

// sortResults orders results by LastAt desc, breaking ties with
// StartedAt desc so output is deterministic for tests and the TUI list.
func sortResults(rs []Result) {
	sort.SliceStable(rs, func(a, b int) bool {
		if !rs[a].Meta.LastAt.Equal(rs[b].Meta.LastAt) {
			return rs[a].Meta.LastAt.After(rs[b].Meta.LastAt)
		}
		return rs[a].Meta.StartedAt.After(rs[b].Meta.StartedAt)
	})
}
