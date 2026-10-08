package transcript

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/searchquery"
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
	Since         time.Time // drops sessions last active before it; zero → no bound
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
		Since:    q.Since,
	})

	terms := searchquery.Parse(q.Text)
	source := q.MessageSource
	if source == "" {
		source = "any"
	}
	if source != "any" && source != "user" && source != "harness" {
		return nil, errors.New("transcript: invalid message source")
	}
	results := make([]Result, 0, len(metas))
	var pending []SessionMeta

	if terms.Empty() {
		for _, m := range metas {
			results = append(results, Result{Meta: m, Preview: m.Summary()})
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
		if source == "any" && m.Snippet != "" && terms.ContainsAll(strings.ToLower(m.Snippet)) {
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
				if !terms.ContainsAll(searchText) {
					continue
				}
				// Legacy sidecars lack source boundaries; rebuild once.
				err = fs.ErrNotExist
			}
		}
		if err == nil {
			count, preview := countMatchesInSearchText(searchText, terms)
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
		if result, ok := searchFallback(idx, readers, m, terms, source); ok {
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
		results = append(results, searchFallbacksParallel(idx, readers, pending, terms, source)...)
	}

	sortResults(results)
	return results, nil
}

// Source-aware upgrades can require parsing many legacy transcripts. Bound
// concurrent reads so the first broad search uses available cores without
// overwhelming the disk or the UI process.
const sourceFallbackWorkers = 4

func searchFallbacksParallel(idx *Index, readers map[string]Reader, pending []SessionMeta, terms searchquery.Terms, source string) []Result {
	jobs := make(chan SessionMeta)
	found := make(chan Result, len(pending))
	var workers sync.WaitGroup
	n := min(sourceFallbackWorkers, len(pending))
	workers.Add(n)
	for range n {
		go func() {
			defer workers.Done()
			for meta := range jobs {
				if result, ok := searchFallback(idx, readers, meta, terms, source); ok {
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

func searchFallback(idx *Index, readers map[string]Reader, meta SessionMeta, terms searchquery.Terms, source string) (Result, bool) {
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
	count, preview := countMatchesForSource(msgs, terms, source)
	if count == 0 {
		return Result{}, false
	}
	return Result{Meta: meta, Matches: count, Preview: preview}, true
}

// RecasePreviews gives each result the preview its transcript would give it in
// the case it was written in. Search reads previews out of the search
// sidecars, which hold lowercased text, so a preview found there is
// lowercased; a caller about to show a bounded set of results passes them
// here to re-read just those transcripts. A preview that already carries an
// upper-case letter (the session snippet, or one found by reading the
// transcript) is kept, as is any preview whose transcript cannot be read
// again or no longer matches. Re-reading is bounded by previewRereadBudget
// bytes of transcript, spent on results in order, so an unbounded search over
// a large corpus re-cases its leading rows and leaves the rest as found. The
// budget is counted from indexed file sizes, so the same index and query
// always re-case the same rows. The results keep their order.
func RecasePreviews(readers map[string]Reader, results []Result, q Query) []Result {
	terms := searchquery.Parse(q.Text)
	source := q.MessageSource
	if source == "" {
		source = "any"
	}
	out := append([]Result(nil), results...)
	if terms.Empty() || readers == nil {
		return out
	}
	var pending []int
	budget := int64(previewRereadBudget)
	for i, result := range out {
		if result.Matches > 0 && result.Preview != "" && result.Preview == strings.ToLower(result.Preview) {
			if budget -= max(result.Meta.FileSize, 1); budget < 0 {
				break
			}
			pending = append(pending, i)
		}
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	n := min(previewWorkers, len(pending))
	workers.Add(n)
	for range n {
		go func() {
			defer workers.Done()
			for i := range jobs {
				meta := out[i].Meta
				reader, ok := readers[meta.Harness]
				if !ok {
					continue
				}
				msgs, err := reader.ReadMessages(meta.Path)
				if err != nil {
					slog.Debug("transcript.search: preview reread failed", "path", meta.Path, "err", err)
					continue
				}
				if _, preview := countMatchesForSource(msgs, terms, source); preview != "" {
					out[i].Preview = preview
				}
			}
		}()
	}
	for _, i := range pending {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	return out
}

// previewWorkers bounds the concurrent transcript reads RecasePreviews makes,
// and previewRereadBudget the transcript bytes it reads in all: about a third
// of a second of parsing on a laptop.
const (
	previewWorkers      = 8
	previewRereadBudget = 64 << 20
)

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

func countMatchesForSource(msgs []Message, terms searchquery.Terms, source string) (int, string) {
	if source == "any" {
		return countMatches(msgs, terms)
	}
	tally := terms.NewTally()
	var pick previewPick
	for _, msg := range msgs {
		text := msg.SearchTextForSource(source)
		lower := strings.ToLower(text)
		pick.offer(terms, text, lower, tally.Add(lower))
	}
	total := tally.Total()
	if total == 0 {
		return 0, ""
	}
	return total, pick.preview
}

// previewPick chooses a transcript's preview across its messages: the first
// line where every term starts a word, else the first line holding every
// term, else the first line holding any.
type previewPick struct {
	preview string
	fit     searchquery.PreviewFit
}

// offer considers one message, given its lowered text and the earliest term
// occurrence in it.
func (p *previewPick) offer(terms searchquery.Terms, text, lower string, first int) {
	if p.fit == searchquery.FitWords || first < 0 {
		return
	}
	pos, fit := terms.PreviewAt(lower, first)
	if p.preview != "" && fit <= p.fit {
		return
	}
	p.preview = firstMatchLine(text, originalOffsetForFoldedByte(text, pos))
	p.fit = fit
}

func messagesSearchText(msgs []Message) string {
	var buf strings.Builder
	for _, msg := range msgs {
		appendSearchText(&buf, msg.Text)
	}
	return buf.String()
}

// countMatchesInSearchText counts case-insensitive occurrences of terms
// inside the precomputed (already-lowercased) search text, and 0 when any term
// is absent. The preview is the line searchquery.PreviewAt prefers, else the line
// containing the first match — necessarily lowercased, since the sidecar is the
// only source available without re-reading the file.
func countMatchesInSearchText(searchText string, terms searchquery.Terms) (int, string) {
	if terms.Empty() {
		return 0, ""
	}
	tally := terms.NewTally()
	first := tally.Add(searchText)
	total := tally.Total()
	if total == 0 {
		return 0, ""
	}
	pos, _ := terms.PreviewAt(searchText, first)
	return total, firstMatchLine(searchText, pos)
}

// countMatches walks msgs in order, counting case-insensitive occurrences
// of terms across each message's Text, and returns 0 when any term is absent
// from the transcript as a whole. The line searchquery.PreviewAt prefers, else the
// first line that contains a match, is captured as the preview, kept stable so
// callers see deterministic output for a given input.
func countMatches(msgs []Message, terms searchquery.Terms) (int, string) {
	tally := terms.NewTally()
	var pick previewPick
	for _, msg := range msgs {
		lower := strings.ToLower(msg.Text)
		pick.offer(terms, msg.Text, lower, tally.Add(lower))
	}
	total := tally.Total()
	if total == 0 {
		return 0, ""
	}
	return total, pick.preview
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

// firstMatchLine returns the line of text containing the byte offset pos,
// truncated like a snippet, entered just before a deep match the way
// searchquery.MatchLine decides. Callers pass the original (non-lowered) text
// so the returned preview preserves the user's case.
func firstMatchLine(text string, pos int) string {
	line, clipped := searchquery.MatchLine(text, pos)
	if clipped {
		return "…" + truncateSnippet(line)
	}
	return truncateSnippet(line)
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
