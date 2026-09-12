package transcript

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// Query describes a search request against the transcript index. Empty
// slices mean "no filter on that dimension". MessageSource narrows text
// matches to user prompts or harness output; it does not affect an empty
// text query. MainOnly excludes automated, unknown, and unmigrated legacy
// entries. An empty Text returns metadata with no message-level scan.
type Query struct {
	Text          string
	MessageSource string // "", "any", "user", or "harness"
	Harness       []string
	Profile       []string
	Project       []string
	MainOnly      bool
}

const sourceSidecarPrefix = "AMXSEARCH2\n"

type sourceSidecar struct {
	Version int    `json:"version"`
	Any     string `json:"any"`
	User    string `json:"user"`
	Harness string `json:"harness"`
}

func decodeSourceSidecar(raw string) (sourceSidecar, bool) {
	if !strings.HasPrefix(raw, sourceSidecarPrefix) {
		return sourceSidecar{}, false
	}
	var data sourceSidecar
	if json.Unmarshal([]byte(strings.TrimPrefix(raw, sourceSidecarPrefix)), &data) != nil || data.Version != 2 {
		return sourceSidecar{}, false
	}
	return data, true
}

func sourceSidecarFromMessages(msgs []Message) sourceSidecar {
	var user, harness strings.Builder
	for _, msg := range msgs {
		appendSearchText(&user, msg.SearchTextForSource("user"))
		appendSearchText(&harness, msg.SearchTextForSource("harness"))
	}
	return sourceSidecar{Version: 2, Any: messagesSearchText(msgs), User: user.String(), Harness: harness.String()}
}

func encodeSourceSidecar(data sourceSidecar) string {
	encoded, _ := json.Marshal(data)
	return sourceSidecarPrefix + string(encoded)
}

func sidecarText(data sourceSidecar, source string) string {
	switch source {
	case "user":
		return data.User
	case "harness":
		return data.Harness
	default:
		return data.Any
	}
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
// the in-memory index, then (only when Text is non-empty) a sidecar search
// or JSONL scan of each survivor via the matching Reader.ReadMessages. Per-session
// read errors are logged at debug level and the session is dropped from
// the results rather than aborting the whole query.
func Search(idx *Index, readers map[string]Reader, q Query) ([]Result, error) {
	if idx == nil {
		return nil, errors.New("transcript: Search requires a non-nil index")
	}
	metas := idx.List(ListFilter{
		Harness:  q.Harness,
		Profile:  q.Profile,
		Project:  q.Project,
		MainOnly: q.MainOnly,
	})

	needle := strings.ToLower(strings.TrimSpace(q.Text))
	source := q.MessageSource
	if source == "" {
		source = "any"
	}
	if source != "any" && source != "user" && source != "harness" {
		return nil, errors.New("transcript: invalid message source")
	}
	results := make([]Result, 0, len(metas))
	var pending []SessionMeta

	if needle == "" {
		for _, m := range metas {
			results = append(results, Result{Meta: m, Preview: m.Snippet})
		}
		sortResults(results)
		return results, nil
	}

	for _, m := range metas {
		// Snippets are stored as written (not lowercased), so lowercase here
		// for case-insensitive comparison. A matching snippet is only a preview
		// preference; the result still needs the sidecar or reader scan below so
		// Matches stays exact.
		snippetPreview := ""
		if source == "any" && m.Snippet != "" && strings.Contains(strings.ToLower(m.Snippet), needle) {
			snippetPreview = m.Snippet
		}
		// Fast path: per-session sidecars hold lowercased transcript text.
		// Version 2 also stores source-specific text; legacy sidecars are
		// upgraded when a source-filtered search first needs them.
		key := IndexKey(m.Harness, m.ID)
		searchText := m.SearchText
		var err error
		if searchText == "" || source != "any" {
			var sourceAware bool
			searchText, sourceAware, err = idx.readSearchCorpus(key, m, source)
			if err == nil && source != "any" && !sourceAware {
				// The legacy body contains every source. A miss there cannot
				// become a hit after splitting it, so avoid parsing/upgrading
				// unrelated transcripts for selective queries.
				if !strings.Contains(searchText, needle) {
					continue
				}
				// Legacy sidecars lack source boundaries; rebuild once.
				err = fs.ErrNotExist
			}
		}
		if err == nil {
			count, preview := countMatchesInSearchText(searchText, needle)
			if count == 0 {
				continue
			}
			if snippetPreview != "" {
				preview = snippetPreview
			}
			results = append(results, Result{Meta: m, Matches: count, Preview: preview})
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("transcript.search: sidecar read failed",
				"path", idx.SearchTextPath(key), "err", err)
		}
		if source != "any" {
			pending = append(pending, m)
			continue
		}
		// Fallback: older/corrupt indexes may lack sidecars. Stream the
		// file for those — slow but correct — and repair the sidecar after
		// a successful read so subsequent searches use the fast path.
		if readers == nil {
			return nil, errors.New("transcript: Search with text requires readers")
		}
		if result, ok := searchFallback(idx, readers, m, needle, source); ok {
			if snippetPreview != "" {
				result.Preview = snippetPreview
			}
			results = append(results, result)
		}
	}
	if len(pending) > 0 {
		if readers == nil {
			return nil, errors.New("transcript: Search with text requires readers")
		}
		results = append(results, searchFallbacksParallel(idx, readers, pending, needle, source)...)
	}

	sortResults(results)
	return results, nil
}

// Source-aware upgrades can require parsing many legacy transcripts. Bound
// concurrent reads so the first broad search uses available cores without
// overwhelming the disk or the UI process.
const sourceFallbackWorkers = 4

func searchFallbacksParallel(idx *Index, readers map[string]Reader, pending []SessionMeta, needle, source string) []Result {
	jobs := make(chan SessionMeta)
	found := make(chan Result, len(pending))
	var workers sync.WaitGroup
	n := min(sourceFallbackWorkers, len(pending))
	workers.Add(n)
	for range n {
		go func() {
			defer workers.Done()
			for meta := range jobs {
				if result, ok := searchFallback(idx, readers, meta, needle, source); ok {
					found <- result
				}
			}
		}()
	}
	for _, meta := range pending {
		jobs <- meta
	}
	close(jobs)
	workers.Wait()
	close(found)
	results := make([]Result, 0, len(found))
	for result := range found {
		results = append(results, result)
	}
	return results
}

func searchFallback(idx *Index, readers map[string]Reader, meta SessionMeta, needle, source string) (Result, bool) {
	reader, ok := readers[meta.Harness]
	if !ok {
		slog.Debug("transcript.search: no reader for harness", "harness", meta.Harness, "id", meta.ID)
		return Result{}, false
	}
	msgs, err := reader.ReadMessages(meta.Path)
	if err != nil {
		slog.Debug("transcript.search: read failed", "path", meta.Path, "err", err)
		return Result{}, false
	}
	updatedText := messagesSearchText(msgs)
	if source != "any" {
		updatedText = encodeSourceSidecar(sourceSidecarFromMessages(msgs))
	}
	key := IndexKey(meta.Harness, meta.ID)
	if err := idx.WriteSearchText(key, updatedText); err != nil {
		slog.Debug("transcript.search: sidecar repair failed", "path", idx.SearchTextPath(key), "err", err)
	}
	count, preview := countMatchesForSource(msgs, needle, source)
	if count == 0 {
		return Result{}, false
	}
	return Result{Meta: meta, Matches: count, Preview: preview}, true
}

// readSearchCorpus reuses decoded sidecars between keystrokes. A changed
// transcript metadata generation or sidecar write invalidates the cached text.
func (idx *Index) readSearchCorpus(key string, meta SessionMeta, source string) (string, bool, error) {
	if text, sourceAware, ok := idx.searchCache.getSource(key, meta, source); ok {
		return text, sourceAware, nil
	}
	generation := idx.searchCache.version()
	raw, err := idx.ReadSearchText(key)
	if err != nil {
		return "", false, err
	}
	if data, ok := decodeSourceSidecar(raw); ok {
		idx.searchCache.put(key, meta, raw, data, true, generation)
		return sidecarText(data, source), true, nil
	}
	if strings.HasPrefix(raw, sourceSidecarPrefix) {
		return "", false, fs.ErrNotExist
	}
	idx.searchCache.put(key, meta, raw, sourceSidecar{}, false, generation)
	return raw, false, nil
}

func countMatchesForSource(msgs []Message, needle, source string) (int, string) {
	if source == "any" {
		return countMatches(msgs, needle)
	}
	var total int
	var preview string
	for _, msg := range msgs {
		count, line := countMatchesInSearchText(strings.ToLower(msg.SearchTextForSource(source)), needle)
		total += count
		if preview == "" && count > 0 {
			preview = line
		}
	}
	return total, preview
}

func messagesSearchText(msgs []Message) string {
	var buf strings.Builder
	for _, msg := range msgs {
		appendSearchText(&buf, msg.Text)
	}
	return buf.String()
}

// countMatchesInSearchText counts case-insensitive occurrences of needle
// inside the precomputed (already-lowercased) search text. The preview is
// the single line of text containing the first match — necessarily
// lowercased, since the sidecar is the only source available
// without re-reading the file. needle must already be lower-cased.
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
				preview = firstMatchLine(msg.Text, originalOffsetForFoldedByte(msg.Text, idx+pos))
			}
			idx += pos + len(needle)
		}
	}
	return total, preview
}

// originalOffsetForFoldedByte maps a byte offset in strings.ToLower(text)
// back to the corresponding byte offset in text. Some Unicode case mappings
// change UTF-8 byte length, so offsets from the folded haystack cannot be
// used directly to slice the original text for previews.
func originalOffsetForFoldedByte(text string, foldedPos int) int {
	if foldedPos <= 0 {
		return 0
	}
	foldedOffset := 0
	for originalOffset, r := range text {
		lower := strings.ToLower(string(r))
		nextFolded := foldedOffset + len(lower)
		size := utf8.RuneLen(r)
		if size < 0 {
			size = 1
		}
		if foldedPos < nextFolded {
			return originalOffset
		}
		if foldedPos == nextFolded {
			return originalOffset + size
		}
		foldedOffset = nextFolded
	}
	return len(text)
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
		if !rs[a].Meta.StartedAt.Equal(rs[b].Meta.StartedAt) {
			return rs[a].Meta.StartedAt.After(rs[b].Meta.StartedAt)
		}
		if rs[a].Meta.Harness != rs[b].Meta.Harness {
			return rs[a].Meta.Harness < rs[b].Meta.Harness
		}
		return rs[a].Meta.ID < rs[b].Meta.ID
	})
}
