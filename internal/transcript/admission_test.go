package transcript

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// secretBody appears only inside message content in these fixtures, never in an
// identity record. Any test that finds it in a history answer or in a candidate
// has found content that crossed the admission boundary.
const secretBody = "SECRET-CONVERSATION-BODY"

// guardedReader wraps a real reader and turns "this transcript must not be
// opened" into a test failure at the moment it happens, rather than into an
// assertion about the answer afterwards. The distinction matters: an answer
// that omits excluded content is consistent with having read it and dropped it,
// and reading it is the thing the boundary forbids.
type guardedReader struct {
	Reader
	t *testing.T
	// forbidden is the set of paths that must never be opened for content.
	forbidden map[string]struct{}

	mu   sync.Mutex
	read []string
}

func newGuardedReader(t *testing.T, inner Reader, forbidden ...string) *guardedReader {
	t.Helper()
	set := make(map[string]struct{}, len(forbidden))
	for _, path := range forbidden {
		set[path] = struct{}{}
	}
	return &guardedReader{Reader: inner, t: t, forbidden: set}
}

func (g *guardedReader) ReadMessages(path string) ([]Message, error) {
	g.mu.Lock()
	g.read = append(g.read, path)
	g.mu.Unlock()
	if _, banned := g.forbidden[path]; banned {
		g.t.Errorf("ReadMessages opened forbidden transcript %s", path)
		return nil, errors.New("forbidden transcript opened")
	}
	return g.Reader.ReadMessages(path)
}

func (g *guardedReader) ClassifySession(path string) (SessionClass, error) {
	if _, banned := g.forbidden[path]; banned {
		g.t.Errorf("ClassifySession opened forbidden transcript %s", path)
		return SessionClassUnknown, errors.New("forbidden transcript opened")
	}
	classifier, ok := g.Reader.(sessionClassifier)
	if !ok {
		return SessionClassUnknown, errors.New("inner reader does not classify")
	}
	return classifier.ClassifySession(path)
}

func (g *guardedReader) DiscoverMetadata(homeDir string, opts MetadataOptions) ([]Candidate, error) {
	discoverer, ok := g.Reader.(MetadataDiscoverer)
	if !ok {
		return nil, errors.New("inner reader does not discover metadata")
	}
	return discoverer.DiscoverMetadata(homeDir, opts)
}

func (g *guardedReader) readPaths() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.read...)
}

// claudeTranscript renders a minimal Claude transcript: one identity-bearing
// user record and one assistant record, both carrying cwd, plus a message body
// that must never leave the file without authorization.
func claudeTranscript(sessionID, cwd, body string) string {
	lines := []string{
		`{"type":"user","timestamp":"2026-08-01T10:00:00.000Z","sessionId":"` + sessionID + `","cwd":"` + cwd +
			`","message":{"role":"user","content":[{"type":"text","text":"` + body + ` question"}]}}`,
		`{"type":"assistant","timestamp":"2026-08-01T10:00:05.000Z","sessionId":"` + sessionID + `","cwd":"` + cwd +
			`","message":{"role":"assistant","content":[{"type":"text","text":"` + body + ` answer"}]}}`,
	}
	return strings.Join(lines, "\n") + "\n"
}

// claudeCorpus stages two Claude conversations under one transcript home: one in
// an allowed directory and one in an excluded directory.
type claudeCorpus struct {
	cfg          *config.Config
	home         string
	allowedDir   string
	excludedDir  string
	allowedPath  string
	excludedPath string
}

func stageClaudeCorpus(t *testing.T) claudeCorpus {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	allowedDir := filepath.Join(root, "projects", "allowed")
	excludedDir := filepath.Join(root, "projects", "excluded")

	allowedPath := filepath.Join(home, "projects", "-allowed", "allowed-session.jsonl")
	excludedPath := filepath.Join(home, "projects", "-excluded", "excluded-session.jsonl")
	writeTranscript(t, allowedPath, claudeTranscript("allowed-session", allowedDir, secretBody))
	writeTranscript(t, excludedPath, claudeTranscript("excluded-session", excludedDir, secretBody))

	return claudeCorpus{
		cfg: &config.Config{Profiles: []config.Account{{
			Name: "work", Harness: harness.Claude, HomeDir: home,
		}}},
		home:         home,
		allowedDir:   allowedDir,
		excludedDir:  excludedDir,
		allowedPath:  allowedPath,
		excludedPath: excludedPath,
	}
}

// directoryAuthorizer admits exactly the directories it is given and counts how
// often it was asked, so a test can prove the decision is taken per request
// rather than cached.
type directoryAuthorizer struct {
	mu      sync.Mutex
	allowed map[string]struct{}
	asked   int
}

func newDirectoryAuthorizer(dirs ...string) *directoryAuthorizer {
	set := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		set[dir] = struct{}{}
	}
	return &directoryAuthorizer{allowed: set}
}

func (a *directoryAuthorizer) Authorize(directory string) Admission {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked++
	if _, ok := a.allowed[directory]; ok {
		return Admission{Allowed: true, Reason: AdmissionAllowed, Projects: []string{filepath.Base(directory)}}
	}
	return Admission{Reason: "declined", Detail: "project is not enrolled"}
}

func (a *directoryAuthorizer) revoke(directory string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.allowed, directory)
}

func (a *directoryAuthorizer) askCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.asked
}

func findConversation(result HistoryResult, id string) (Conversation, bool) {
	for _, conv := range result.Conversations {
		if conv.ID == id {
			return conv, true
		}
	}
	return Conversation{}, false
}

func findWithheld(result HistoryResult, id string) (Withheld, bool) {
	for _, w := range result.Withheld {
		if w.ID == id {
			return w, true
		}
	}
	return Withheld{}, false
}

func TestHistoryReadsOnlyAuthorizedTranscripts(t *testing.T) {
	t.Parallel()

	corpus := stageClaudeCorpus(t)
	reader := newGuardedReader(t, NewClaudeReader(), corpus.excludedPath)
	readers := map[string]Reader{harness.Claude: reader}

	result, err := History(corpus.cfg, readers, newDirectoryAuthorizer(corpus.allowedDir), HistoryRequest{})
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	if result.Examined != 2 {
		t.Fatalf("Examined = %d, want 2 (both conversations must be discovered by metadata)", result.Examined)
	}
	if len(result.Conversations) != 1 {
		t.Fatalf("Conversations = %d, want 1", len(result.Conversations))
	}
	conv := result.Conversations[0]
	if conv.ID != "allowed-session" {
		t.Fatalf("admitted conversation ID = %q, want allowed-session", conv.ID)
	}
	if conv.Directory != corpus.allowedDir {
		t.Fatalf("Directory = %q, want %q", conv.Directory, corpus.allowedDir)
	}
	if len(conv.Excerpts) != 2 {
		t.Fatalf("Excerpts = %d, want 2", len(conv.Excerpts))
	}

	withheld, ok := findWithheld(result, "excluded-session")
	if !ok {
		t.Fatalf("excluded conversation missing from Withheld: %+v", result.Withheld)
	}
	if withheld.Reason != "declined" {
		t.Fatalf("withheld reason = %q, want declined", withheld.Reason)
	}
	if withheld.Harness != harness.Claude {
		t.Fatalf("withheld harness = %q, want %q", withheld.Harness, harness.Claude)
	}

	for _, path := range reader.readPaths() {
		if path == corpus.excludedPath {
			t.Fatalf("excluded transcript was opened for content: %s", path)
		}
	}
}

// A withheld entry names the conversation and the reason and nothing that
// locates it. Reporting the path, the working directory, or the project would
// hand back under "why" precisely what the exclusion withheld.
func TestHistoryWithheldEntriesCarryNoLocation(t *testing.T) {
	t.Parallel()

	corpus := stageClaudeCorpus(t)
	readers := map[string]Reader{harness.Claude: newGuardedReader(t, NewClaudeReader(), corpus.excludedPath)}

	result, err := History(corpus.cfg, readers, newDirectoryAuthorizer(corpus.allowedDir), HistoryRequest{})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	encoded, err := json.Marshal(result.Withheld)
	if err != nil {
		t.Fatalf("marshal withheld: %v", err)
	}
	for _, leak := range []string{corpus.excludedDir, corpus.excludedPath, secretBody} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("withheld payload leaks %q: %s", leak, encoded)
		}
	}
}

// The global index refresh reads every message body and writes the text into a
// search sidecar, so a refresh that has already run has the excluded
// conversation's content on disk in a second place. A history request must not
// be able to reach it: the admission path never consults the index, and the
// guarded reader proves the source file is not reopened either.
func TestHistoryDoesNotServeContentCachedByIndexRefresh(t *testing.T) {
	t.Parallel()

	corpus := stageClaudeCorpus(t)
	realReaders := map[string]Reader{harness.Claude: NewClaudeReader()}

	idx := newTestIndex(t)
	if err := idx.Refresh(corpus.cfg, realReaders); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// The refresh must really have cached the excluded conversation, or the test
	// below would prove nothing.
	cachedExcluded := false
	for _, meta := range idx.List(ListFilter{}) {
		if meta.Path == corpus.excludedPath {
			cachedExcluded = true
			if !strings.Contains(meta.SearchText+meta.Snippet, secretBody) {
				t.Fatalf("refresh cached %s without its message content; the premise of this test is gone", meta.Path)
			}
		}
	}
	if !cachedExcluded {
		t.Fatalf("refresh did not cache the excluded transcript; the premise of this test is gone")
	}

	reader := newGuardedReader(t, NewClaudeReader(), corpus.excludedPath)
	result, err := History(corpus.cfg, map[string]Reader{harness.Claude: reader}, newDirectoryAuthorizer(corpus.allowedDir), HistoryRequest{})
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	if _, ok := findConversation(result, "excluded-session"); ok {
		t.Fatalf("history returned the excluded conversation after an index refresh")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Count(string(encoded), secretBody) != 2 {
		// Two hits: the allowed conversation's user and assistant excerpts. Any
		// other count means either the allowed content is missing or the
		// excluded content came through.
		t.Fatalf("unexpected message content in history answer: %s", encoded)
	}
	if strings.Contains(string(encoded), corpus.excludedDir) {
		t.Fatalf("history answer names the excluded directory: %s", encoded)
	}
}

// Authorization is asked again on every request, so withdrawing an opt-in stops
// the next read rather than the next process.
func TestHistoryRevocationStopsSubsequentReads(t *testing.T) {
	t.Parallel()

	corpus := stageClaudeCorpus(t)
	authorizer := newDirectoryAuthorizer(corpus.allowedDir)
	reader := newGuardedReader(t, NewClaudeReader(), corpus.excludedPath)
	readers := map[string]Reader{harness.Claude: reader}

	first, err := History(corpus.cfg, readers, authorizer, HistoryRequest{})
	if err != nil {
		t.Fatalf("first History: %v", err)
	}
	if _, ok := findConversation(first, "allowed-session"); !ok {
		t.Fatalf("first request did not admit the allowed conversation")
	}
	askedAfterFirst := authorizer.askCount()

	authorizer.revoke(corpus.allowedDir)
	reader.forbidden[corpus.allowedPath] = struct{}{}

	second, err := History(corpus.cfg, readers, authorizer, HistoryRequest{})
	if err != nil {
		t.Fatalf("second History: %v", err)
	}
	if len(second.Conversations) != 0 {
		t.Fatalf("revoked request returned %d conversations, want 0", len(second.Conversations))
	}
	if _, ok := findWithheld(second, "allowed-session"); !ok {
		t.Fatalf("revoked conversation missing from Withheld: %+v", second.Withheld)
	}
	if authorizer.askCount() <= askedAfterFirst {
		t.Fatalf("authorizer was not consulted on the second request (%d asks total)", authorizer.askCount())
	}
	encoded, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(encoded), secretBody) {
		t.Fatalf("revoked request still returned message content: %s", encoded)
	}
}

// A conversation whose identity records name two working directories may hold
// messages from two projects, and identity alone cannot say which is which.
// Deciding would require the content the decision gates, so it stays closed.
func TestHistoryWithholdsAmbiguousAndUnidentifiedConversations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	first := filepath.Join(root, "projects", "first")
	second := filepath.Join(root, "projects", "second")

	mixedPath := filepath.Join(home, "projects", "-mixed", "mixed-session.jsonl")
	writeTranscript(t, mixedPath, strings.Join([]string{
		`{"type":"user","timestamp":"2026-08-01T10:00:00.000Z","sessionId":"mixed-session","cwd":"` + first +
			`","message":{"role":"user","content":[{"type":"text","text":"` + secretBody + ` one"}]}}`,
		`{"type":"user","timestamp":"2026-08-01T11:00:00.000Z","sessionId":"mixed-session","cwd":"` + second +
			`","message":{"role":"user","content":[{"type":"text","text":"` + secretBody + ` two"}]}}`,
	}, "\n")+"\n")

	cfg := &config.Config{Profiles: []config.Account{{
		Name: "work", Harness: harness.Claude, HomeDir: home,
	}}}
	reader := newGuardedReader(t, NewClaudeReader(), mixedPath)

	// The authorizer permits both directories individually. The refusal must
	// come from the ambiguity itself, not from an exclusion.
	result, err := History(cfg, map[string]Reader{harness.Claude: reader}, newDirectoryAuthorizer(first, second), HistoryRequest{})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	withheld, ok := findWithheld(result, "mixed-session")
	if !ok {
		t.Fatalf("mixed-project conversation missing from Withheld: %+v", result.Withheld)
	}
	if withheld.Reason != AdmissionAmbiguous {
		t.Fatalf("withheld reason = %q, want %q", withheld.Reason, AdmissionAmbiguous)
	}
	if len(result.Conversations) != 0 {
		t.Fatalf("Conversations = %d, want 0", len(result.Conversations))
	}
}

func TestHistoryWithholdsConversationsWithNoWorkingDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "codex-home")
	// A Codex rollout whose session_meta carries no cwd: there is no directory
	// to authorize, so it cannot be shown to be outside an exclusion.
	path := filepath.Join(home, "sessions", "2026", "08", "01",
		"rollout-2026-08-01T10-00-00-anonymous-session.jsonl")
	writeTranscript(t, path, strings.Join([]string{
		`{"timestamp":"2026-08-01T10:00:00.000Z","type":"session_meta","payload":{"id":"anonymous-session","timestamp":"2026-08-01T10:00:00.000Z","originator":"codex-tui"}}`,
		`{"timestamp":"2026-08-01T10:00:02.000Z","type":"event_msg","payload":{"type":"user_message","message":"` + secretBody + `"}}`,
	}, "\n")+"\n")

	cfg := &config.Config{Profiles: []config.Account{{
		Name: "work", Harness: harness.Codex, HomeDir: home,
	}}}
	reader := newGuardedReader(t, NewCodexReader(), path)

	result, err := History(cfg, map[string]Reader{harness.Codex: reader}, newDirectoryAuthorizer(root), HistoryRequest{})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	withheld, ok := findWithheld(result, "anonymous-session")
	if !ok {
		t.Fatalf("unidentified conversation missing from Withheld: %+v", result.Withheld)
	}
	if withheld.Reason != AdmissionUnidentified {
		t.Fatalf("withheld reason = %q, want %q", withheld.Reason, AdmissionUnidentified)
	}
}

// A reader that cannot enumerate identity without reading content contributes
// nothing. Falling back to Discover would be the content-reading path this
// boundary exists to precede.
func TestHistorySkipsHarnessThatCannotEnumerateMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	path := filepath.Join(home, "projects", "-any", "session.jsonl")
	writeTranscript(t, path, "{}\n")

	cfg := &config.Config{Profiles: []config.Account{{
		Name: "work", Harness: harness.Claude, HomeDir: home,
	}}}
	reader := &fakeReader{
		harness: harness.Claude,
		discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
			t.Error("Discover was called; the metadata path must not fall back to content discovery")
			return nil, nil
		},
		readResult: []Message{{Role: "user", Text: secretBody}},
	}

	result, err := History(cfg, map[string]Reader{harness.Claude: reader}, newDirectoryAuthorizer(root), HistoryRequest{})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(result.Conversations) != 0 || result.Examined != 0 {
		t.Fatalf("history returned %d conversations from a reader with no metadata path", len(result.Conversations))
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], harness.Claude) {
		t.Fatalf("Warnings = %v, want one naming the harness", result.Warnings)
	}
}

func TestHistoryRequiresAnAuthorizer(t *testing.T) {
	t.Parallel()

	corpus := stageClaudeCorpus(t)
	readers := map[string]Reader{harness.Claude: newGuardedReader(t, NewClaudeReader(), corpus.allowedPath, corpus.excludedPath)}
	if _, err := History(corpus.cfg, readers, nil, HistoryRequest{}); err == nil {
		t.Fatal("History with a nil authorizer returned no error; there is no safe default")
	}
	if _, err := History(nil, readers, newDirectoryAuthorizer(), HistoryRequest{}); err == nil {
		t.Fatal("History with a nil config returned no error")
	}
}

func TestHistoryBoundsExcerptsAndReportsLocation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	dir := filepath.Join(root, "projects", "bounded")
	path := filepath.Join(home, "projects", "-bounded", "bounded-session.jsonl")

	var lines []string
	for i := range 6 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		lines = append(lines, `{"type":"`+role+`","timestamp":"2026-08-01T10:0`+string(rune('0'+i))+
			`:00.000Z","sessionId":"bounded-session","cwd":"`+dir+
			`","message":{"role":"`+role+`","content":[{"type":"text","text":"`+strings.Repeat("x", 50)+`"}]}}`)
	}
	writeTranscript(t, path, strings.Join(lines, "\n")+"\n")

	cfg := &config.Config{Profiles: []config.Account{{
		Name: "work", Harness: harness.Claude, HomeDir: home,
	}}}
	readers := map[string]Reader{harness.Claude: NewClaudeReader()}

	result, err := History(cfg, readers, newDirectoryAuthorizer(dir), HistoryRequest{
		Limits: HistoryLimits{Messages: 2, ExcerptChars: 10},
	})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(result.Conversations) != 1 {
		t.Fatalf("Conversations = %d, want 1", len(result.Conversations))
	}
	conv := result.Conversations[0]
	if conv.MessageCount != 6 {
		t.Fatalf("MessageCount = %d, want 6", conv.MessageCount)
	}
	if !conv.Truncated {
		t.Fatal("Truncated = false, want true: four messages were dropped")
	}
	if len(conv.Excerpts) != 2 {
		t.Fatalf("Excerpts = %d, want 2", len(conv.Excerpts))
	}
	// The tail is what an incident is about, and each excerpt says where in the
	// conversation it came from.
	if conv.Excerpts[0].Index != 4 || conv.Excerpts[1].Index != 5 {
		t.Fatalf("excerpt indices = %d,%d, want 4,5", conv.Excerpts[0].Index, conv.Excerpts[1].Index)
	}
	if conv.Excerpts[0].Role != "user" || conv.Excerpts[1].Role != "assistant" {
		t.Fatalf("excerpt roles = %q,%q, want user,assistant", conv.Excerpts[0].Role, conv.Excerpts[1].Role)
	}
	if conv.Excerpts[1].Timestamp.IsZero() {
		t.Fatal("excerpt timestamp is zero")
	}
	for _, excerpt := range conv.Excerpts {
		if len([]rune(excerpt.Text)) != 10 || !excerpt.Truncated {
			t.Fatalf("excerpt text = %q (truncated=%v), want 10 runes and truncated", excerpt.Text, excerpt.Truncated)
		}
	}
	if conv.SessionClass == "" {
		t.Fatal("SessionClass is empty; the answer must classify the session")
	}
	if result.Limits.Messages != 2 || result.Limits.ExcerptChars != 10 || result.Limits.Conversations != DefaultHistoryConversations {
		t.Fatalf("reported limits = %+v, want the applied bounds", result.Limits)
	}
}

func TestHistoryLimitsNormalizeAndClamp(t *testing.T) {
	t.Parallel()

	zero := HistoryLimits{}.Normalized()
	if zero.Conversations != DefaultHistoryConversations || zero.Messages != DefaultHistoryMessages || zero.ExcerptChars != DefaultHistoryExcerptChars {
		t.Fatalf("zero limits normalized to %+v, want the defaults", zero)
	}
	huge := HistoryLimits{Conversations: 1 << 20, Messages: 1 << 20, ExcerptChars: 1 << 20}.Normalized()
	if huge.Conversations != MaxHistoryConversations || huge.Messages != MaxHistoryMessages || huge.ExcerptChars != MaxHistoryExcerptChars {
		t.Fatalf("oversized limits clamped to %+v, want the ceilings", huge)
	}
}

func TestHistoryTruncatesAtTheConversationBoundWithoutReadingTheRest(t *testing.T) {
	t.Parallel()

	corpus := stageClaudeCorpus(t)
	// Both conversations are authorized; only one fits the bound. The one that
	// does not fit must not be opened.
	newest := corpus.allowedPath
	oldest := corpus.excludedPath
	if err := os.Chtimes(oldest, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := os.Chtimes(newest, time.Now(), time.Now()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	reader := newGuardedReader(t, NewClaudeReader(), oldest)
	result, err := History(corpus.cfg, map[string]Reader{harness.Claude: reader},
		newDirectoryAuthorizer(corpus.allowedDir, corpus.excludedDir),
		HistoryRequest{Limits: HistoryLimits{Conversations: 1}})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(result.Conversations) != 1 || result.Conversations[0].ID != "allowed-session" {
		t.Fatalf("Conversations = %+v, want only the newest", result.Conversations)
	}
	if !result.Truncated {
		t.Fatal("Truncated = false, want true: an admitted conversation was dropped")
	}
}

func TestHistoryFiltersByHarnessAndSince(t *testing.T) {
	t.Parallel()

	corpus := stageClaudeCorpus(t)
	readers := map[string]Reader{harness.Claude: newGuardedReader(t, NewClaudeReader(), corpus.allowedPath, corpus.excludedPath)}

	result, err := History(corpus.cfg, readers, newDirectoryAuthorizer(corpus.allowedDir), HistoryRequest{
		Harnesses: []string{harness.Codex},
	})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if result.Examined != 0 || len(result.Conversations) != 0 {
		t.Fatalf("harness filter admitted %d conversations, want 0", len(result.Conversations))
	}

	result, err = History(corpus.cfg, readers, newDirectoryAuthorizer(corpus.allowedDir), HistoryRequest{
		Since: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if result.Examined != 0 {
		t.Fatalf("since filter examined %d conversations, want 0", result.Examined)
	}
}

// --- metadata discovery -----------------------------------------------------

// The candidate a decision is taken on must contain no message content at all.
// This asserts the whole serialized candidate rather than a chosen field, so a
// future field that happened to carry content would fail here.
func TestDiscoverMetadataYieldsNoMessageContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	claudeHome := filepath.Join(root, "claude-home")
	writeTranscript(t, filepath.Join(claudeHome, "projects", "-work", "claude-session.jsonl"),
		claudeTranscript("claude-session", "/work/repo", secretBody))

	codexHome := filepath.Join(root, "codex-home")
	writeTranscript(t, filepath.Join(codexHome, "sessions", "2026", "08", "01",
		"rollout-2026-08-01T10-00-00-codex-session.jsonl"), strings.Join([]string{
		`{"timestamp":"2026-08-01T10:00:00.000Z","type":"session_meta","payload":{"id":"codex-session","timestamp":"2026-08-01T10:00:00.000Z","cwd":"/work/repo","originator":"codex-tui"}}`,
		`{"timestamp":"2026-08-01T10:00:02.000Z","type":"event_msg","payload":{"type":"user_message","message":"` + secretBody + `"}}`,
		`{"timestamp":"2026-08-01T10:00:04.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + secretBody + `"}]}}`,
	}, "\n")+"\n")

	copilotHome := filepath.Join(root, "copilot-home")
	writeTranscript(t, filepath.Join(copilotHome, "session-state", "copilot-session", "events.jsonl"), strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-08-01T10:00:00.000Z","data":{"sessionId":"copilot-session","startTime":"2026-08-01T10:00:00.000Z","context":{"cwd":"/work/repo"}}}`,
		`{"type":"user.message","timestamp":"2026-08-01T10:00:02.000Z","data":{"content":"` + secretBody + `"}}`,
		`{"type":"assistant.message","timestamp":"2026-08-01T10:00:04.000Z","data":{"content":"` + secretBody + `"}}`,
	}, "\n")+"\n")

	cases := []struct {
		name       string
		discoverer MetadataDiscoverer
		home       string
		wantID     string
	}{
		{"claude", NewClaudeReader(), claudeHome, "claude-session"},
		{"codex", NewCodexReader(), codexHome, "codex-session"},
		{"copilot", NewCopilotReader(), copilotHome, "copilot-session"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidates, err := tc.discoverer.DiscoverMetadata(tc.home, MetadataOptions{})
			if err != nil {
				t.Fatalf("DiscoverMetadata: %v", err)
			}
			if len(candidates) != 1 {
				t.Fatalf("candidates = %d, want 1", len(candidates))
			}
			candidate := candidates[0]
			if candidate.ID != tc.wantID {
				t.Fatalf("ID = %q, want %q", candidate.ID, tc.wantID)
			}
			dir, single := candidate.Directory()
			if !single || dir != "/work/repo" {
				t.Fatalf("Directory() = %q,%v, want /work/repo,true", dir, single)
			}
			if candidate.FileSize == 0 || candidate.FileModTime.IsZero() {
				t.Fatalf("candidate is missing file metadata: %+v", candidate)
			}
			encoded, err := json.Marshal(candidate)
			if err != nil {
				t.Fatalf("marshal candidate: %v", err)
			}
			if strings.Contains(string(encoded), secretBody) {
				t.Fatalf("candidate carries message content: %s", encoded)
			}
		})
	}
}

// A Codex conversation that changed directory mid-run writes a turn_context per
// turn, and the metadata scan must see every one of them. Stopping at the first
// identity record would report a mixed-project conversation as single-project
// and authorize it against a directory covering only part of its content.
func TestDiscoverMetadataSeesEveryWorkingDirectory(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), "codex-home")
	writeTranscript(t, filepath.Join(home, "sessions", "2026", "08", "01",
		"rollout-2026-08-01T10-00-00-moved-session.jsonl"), strings.Join([]string{
		`{"timestamp":"2026-08-01T10:00:00.000Z","type":"session_meta","payload":{"id":"moved-session","timestamp":"2026-08-01T10:00:00.000Z","cwd":"/work/first","originator":"codex-tui"}}`,
		`{"timestamp":"2026-08-01T10:00:02.000Z","type":"event_msg","payload":{"type":"user_message","message":"` + secretBody + `"}}`,
		`{"timestamp":"2026-08-01T10:05:00.000Z","type":"turn_context","payload":{"cwd":"/work/second","approval_policy":"on-request"}}`,
	}, "\n")+"\n")

	candidates, err := NewCodexReader().DiscoverMetadata(home, MetadataOptions{})
	if err != nil {
		t.Fatalf("DiscoverMetadata: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if got := candidates[0].Directories; len(got) != 2 || got[0] != "/work/first" || got[1] != "/work/second" {
		t.Fatalf("Directories = %v, want both directories in first-seen order", got)
	}
	if _, single := candidates[0].Directory(); single {
		t.Fatal("Directory() reported a single directory for a conversation that moved")
	}
}

func TestClaudeDiscoverMetadataFallsBackToTheEncodedDirectory(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), "claude-home")
	// No record carries a cwd, so the encoded project directory name is the only
	// identity available.
	writeTranscript(t, filepath.Join(home, "projects", "-Users-dev-projects-foo", "fallback-session.jsonl"),
		`{"type":"user","timestamp":"2026-08-01T10:00:00.000Z","message":{"role":"user","content":"`+secretBody+`"}}`+"\n")

	candidates, err := NewClaudeReader().DiscoverMetadata(home, MetadataOptions{})
	if err != nil {
		t.Fatalf("DiscoverMetadata: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if candidates[0].ID != "fallback-session" {
		t.Fatalf("ID = %q, want the filename stem", candidates[0].ID)
	}
	dir, single := candidates[0].Directory()
	if !single || dir != "/Users/dev/projects/foo" {
		t.Fatalf("Directory() = %q,%v, want the decoded directory", dir, single)
	}
}

func TestDiscoverMetadataOnAbsentHomeIsEmpty(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "absent")
	for name, discoverer := range map[string]MetadataDiscoverer{
		"claude":  NewClaudeReader(),
		"codex":   NewCodexReader(),
		"copilot": NewCopilotReader(),
	} {
		candidates, err := discoverer.DiscoverMetadata(missing, MetadataOptions{})
		if err != nil {
			t.Fatalf("%s DiscoverMetadata on absent home: %v", name, err)
		}
		if len(candidates) != 0 {
			t.Fatalf("%s returned %d candidates for an absent home", name, len(candidates))
		}
		empty, err := discoverer.DiscoverMetadata("", MetadataOptions{})
		if err != nil || len(empty) != 0 {
			t.Fatalf("%s DiscoverMetadata on an empty home = %v, %v", name, empty, err)
		}
	}
}

func TestClipRunesDoesNotSplitMultiByteCharacters(t *testing.T) {
	t.Parallel()

	text, truncated := clipRunes("héllo wörld", 5)
	if !truncated || text != "héllo" {
		t.Fatalf("clipRunes = %q,%v, want héllo,true", text, truncated)
	}
	if got, truncated := clipRunes("short", 100); truncated || got != "short" {
		t.Fatalf("clipRunes = %q,%v, want short,false", got, truncated)
	}
}
