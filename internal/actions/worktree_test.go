package actions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

type actionWorktreeManager struct {
	entries     []worktree.Entry
	listErr     error
	resolveErr  error
	removeErr   error
	forgetErr   error
	removeCalls int
	forgetCalls int
}

func (m *actionWorktreeManager) List(context.Context) ([]worktree.Entry, error) {
	return append([]worktree.Entry(nil), m.entries...), m.listErr
}

func (m *actionWorktreeManager) Resolve(_ context.Context, selector string) (worktree.Entry, error) {
	if m.resolveErr != nil {
		return worktree.Entry{}, m.resolveErr
	}
	return worktree.ResolveSelector(m.entries, selector)
}

func (m *actionWorktreeManager) Remove(context.Context, string, worktree.RemoveOptions) (worktree.MutationResult, error) {
	m.removeCalls++
	return worktree.MutationResult{Entry: m.entries[0], CheckoutRemoved: m.removeErr == nil}, m.removeErr
}

func (m *actionWorktreeManager) Forget(context.Context, string) (worktree.MutationResult, error) {
	m.forgetCalls++
	return worktree.MutationResult{Entry: m.entries[0], MetadataForgotten: m.forgetErr == nil}, m.forgetErr
}

func TestWorktreeDefinitionsDeclareEntitiesParametersAndConfirmation(t *testing.T) {
	registry := DefaultRegistry()
	for _, id := range []ID{WorktreeShow, WorktreeReopen, WorktreeShell, WorktreeEditor, WorktreeRemove, WorktreeForget} {
		definition, ok := registry.Get(id)
		if !ok || len(definition.Requires) != 1 || definition.Requires[0].Kind != EntityWorktree {
			t.Fatalf("definition %q = %+v, want worktree requirement", id, definition)
		}
	}
	remove, _ := registry.Get(WorktreeRemove)
	forget, _ := registry.Get(WorktreeForget)
	if !remove.Confirmation.Required || !forget.Confirmation.Required || remove.Parameters[0].Name != "delete_branch" {
		t.Fatalf("cleanup definitions = remove %+v forget %+v", remove, forget)
	}
	reopen, _ := registry.Get(WorktreeReopen)
	if len(reopen.Parameters) != 2 || reopen.Parameters[0].Name != "session" || reopen.Parameters[1].Name != "profile" {
		t.Fatalf("reopen parameters = %+v", reopen.Parameters)
	}
	entry := worktree.Entry{ID: "wt", Path: t.TempDir(), Exists: true, GitAvailable: true, StatusAvailable: true, Actionable: true}
	if got := registry.Availability(Dependencies{}, WorktreeRemove, Request{Worktree: &entry}); !got.Available {
		t.Fatalf("clean remove availability = %+v", got)
	}
	entry.Dirty = true
	if got := registry.Availability(Dependencies{}, WorktreeRemove, Request{Worktree: &entry}); got.Available || !strings.Contains(got.Reason, "dirty") {
		t.Fatalf("dirty remove availability = %+v", got)
	}
	entry.Dirty = false
	if got := registry.Availability(Dependencies{}, WorktreeForget, Request{Worktree: &entry}); got.Available {
		t.Fatalf("healthy forget availability = %+v", got)
	}
}

func TestWorktreeLifecycleErrorsHaveStableCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{worktree.ErrWorktreeDirty, "worktree_dirty"},
		{worktree.ErrWorktreeLocked, "worktree_locked"},
		{worktree.ErrBranchUnmerged, "branch_unmerged"},
		{worktree.ErrMetadataInvalid, "metadata_invalid"},
		{worktree.ErrGitUnavailable, "git_unavailable"},
	}
	for _, tc := range cases {
		if got := mapLifecycleError(WorktreeRemove, tc.err); got.Code != tc.code {
			t.Fatalf("mapLifecycleError(%v) = %q, want %q", tc.err, got.Code, tc.code)
		}
	}
}

func TestWorktreeReopenDisambiguatesFreshLiveMatches(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	entry := worktree.Entry{ID: "wt-1", Path: root, SourceRoot: source, ProjectLabel: "api", Exists: true}
	manager := &actionWorktreeManager{entries: []worktree.Entry{entry}}
	client := &continuityKitty{}
	running := []session.RunningSession{
		{TabID: 22, WindowID: 220, Project: "api", Profile: "work", Directory: filepath.Join(root, "nested")},
		{TabID: 11, WindowID: 110, Project: "api", Profile: "work", Directory: root},
		{TabID: 33, Directory: source},
	}
	deps := Dependencies{
		Kitty: client, Config: worktreeActionConfig(source), WorktreeManager: manager,
		LiveSessions: func(*config.Config) ([]session.RunningSession, error) { return running, nil },
	}
	_, err := DefaultRegistry().Execute(context.Background(), deps, WorktreeReopen, Request{Selector: entry.ID})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "ambiguous_live_sessions" || len(actionErr.Candidates) != 2 || actionErr.Candidates[0].ID != "11" {
		t.Fatalf("ambiguous error = %#v", err)
	}
	result, err := DefaultRegistry().Execute(context.Background(), deps, WorktreeReopen, Request{Selector: entry.ID, Parameters: map[string]string{"session": "22"}})
	if err != nil {
		t.Fatalf("selected reopen: %v", err)
	}
	if result.Data["continuation"] != "focused" || len(client.focuses) != 1 || client.focuses[0] != "id:22" {
		t.Fatalf("selected result = %+v focuses=%v", result, client.focuses)
	}
}

func TestWorktreeRemoveFailsClosedOnProbeErrorsAndActivity(t *testing.T) {
	root := t.TempDir()
	entry := worktree.Entry{ID: "wt-1", Path: root, Exists: true, Actionable: true}
	manager := &actionWorktreeManager{entries: []worktree.Entry{entry}}
	loopCalled := false
	deps := Dependencies{
		WorktreeManager: manager,
		LiveSessions:    func(*config.Config) ([]session.RunningSession, error) { return nil, errors.New("Kitty down") },
		ActiveLoops:     func() ([]agentloop.Candidate, error) { loopCalled = true; return nil, nil },
	}
	_, err := DefaultRegistry().Execute(context.Background(), deps, WorktreeRemove, Request{Selector: entry.ID, Confirmed: true})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "probe_unavailable" || !loopCalled || manager.removeCalls != 0 {
		t.Fatalf("probe refusal = %#v loopCalled=%v removeCalls=%d", err, loopCalled, manager.removeCalls)
	}

	deps.LiveSessions = func(*config.Config) ([]session.RunningSession, error) {
		return []session.RunningSession{{TabID: 9, Directory: filepath.Join(root, "child")}}, nil
	}
	_, err = DefaultRegistry().Execute(context.Background(), deps, WorktreeRemove, Request{Selector: entry.ID, Confirmed: true})
	if !errors.As(err, &actionErr) || actionErr.Code != "worktree_active" || len(actionErr.Candidates) != 1 || manager.removeCalls != 0 {
		t.Fatalf("activity refusal = %#v removeCalls=%d", err, manager.removeCalls)
	}

	deps.LiveSessions = func(*config.Config) ([]session.RunningSession, error) { return nil, nil }
	result, err := DefaultRegistry().Execute(context.Background(), deps, WorktreeRemove, Request{Selector: entry.ID, Confirmed: true})
	if err != nil || !result.Changed || manager.removeCalls != 1 {
		t.Fatalf("clean removal = %+v err=%v calls=%d", result, err, manager.removeCalls)
	}
}

func TestWorktreeReopenUsesNewestVisibleTranscriptAndKeepsRefreshWarning(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	entry := worktree.Entry{ID: "wt-1", Path: root, SourceRoot: source, ProjectLabel: "api", Profile: "work", Harness: harness.Claude, Exists: true}
	manager := &actionWorktreeManager{entries: []worktree.Entry{entry}}
	reader := &continuityReader{harness: harness.Claude, err: errors.New("refresh warning")}
	now := time.Now().UTC()
	older := writeActionTranscript(t, root, "older", now.Add(-time.Hour), false)
	newer := writeActionTranscript(t, filepath.Join(root, "nested"), "newer", now, false)
	hidden := writeActionTranscript(t, root, "hidden", now.Add(time.Hour), true)
	indexPath := filepath.Join(t.TempDir(), "transcripts.json")
	writeActionTranscriptIndex(t, indexPath, older, newer, hidden)
	client := &continuityKitty{}
	deps := Dependencies{
		Kitty: client, Config: worktreeActionConfig(source), WorktreeManager: manager,
		Readers:      map[string]transcript.Reader{harness.Claude: reader},
		LiveSessions: func(*config.Config) ([]session.RunningSession, error) { return nil, nil },
		TranscriptIndex: func() (*transcript.Index, error) {
			idx, err := transcript.LoadIndexAt(indexPath)
			return idx, errors.Join(err, errors.New("cache warning"))
		},
	}
	result, err := DefaultRegistry().Execute(context.Background(), deps, WorktreeReopen, Request{Selector: entry.ID})
	if err == nil || !strings.Contains(err.Error(), "validating indexed transcript source") {
		// The injected read error verifies resume failures are surfaced instead
		// of silently turning into a fresh launch.
		t.Fatalf("resume validation error = %v result=%+v", err, result)
	}
	if len(client.launched) != 0 {
		t.Fatalf("resume failure launched fresh: %+v", client.launched)
	}

	reader.err = nil
	result, err = DefaultRegistry().Execute(context.Background(), deps, WorktreeReopen, Request{Selector: entry.ID})
	if err != nil {
		t.Fatalf("resume newest: %v", err)
	}
	if result.Data["session_id"] != "newer" || result.Data["continuation"] != "resumed" || len(client.launched) != 1 {
		t.Fatalf("resume result = %+v launches=%+v", result, client.launched)
	}
	warnings, _ := result.Data["warnings"].([]string)
	if len(warnings) != 1 || warnings[0] != "cache warning" {
		t.Fatalf("resume warnings = %v", warnings)
	}
}

func TestWorktreeReopenFreshFallbackHandlesLegacyDefaultsAndProfileDrift(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	entry := worktree.Entry{ID: "wt-1", Path: root, SourceRoot: source, ProjectLabel: "api", Profile: "removed", Harness: harness.Claude, Exists: true}
	manager := &actionWorktreeManager{entries: []worktree.Entry{entry}}
	client := &continuityKitty{}
	cfg := worktreeActionConfig(source)
	reader := &continuityReader{harness: harness.Claude}
	indexPath := filepath.Join(t.TempDir(), "missing-index.json")
	deps := Dependencies{
		Kitty: client, Config: cfg, WorktreeManager: manager,
		Readers:         map[string]transcript.Reader{harness.Claude: reader},
		LiveSessions:    func(*config.Config) ([]session.RunningSession, error) { return nil, nil },
		TranscriptIndex: func() (*transcript.Index, error) { return transcript.LoadIndexAt(indexPath) },
	}
	_, err := DefaultRegistry().Execute(context.Background(), deps, WorktreeReopen, Request{Selector: entry.ID})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "profile_required" || len(actionErr.MissingParameters) != 1 {
		t.Fatalf("removed profile error = %#v", err)
	}
	result, err := DefaultRegistry().Execute(context.Background(), deps, WorktreeReopen, Request{Selector: entry.ID, Parameters: map[string]string{"profile": "work"}})
	if err != nil || result.Data["continuation"] != "fresh" || len(client.launched) != 1 {
		t.Fatalf("replacement profile result = %+v err=%v launches=%d", result, err, len(client.launched))
	}

	manager.entries[0].Profile = ""
	result, err = DefaultRegistry().Execute(context.Background(), deps, WorktreeReopen, Request{Selector: entry.ID})
	if err != nil || result.Data["profile"] != "work" || len(client.launched) != 2 {
		t.Fatalf("legacy default result = %+v err=%v launches=%d", result, err, len(client.launched))
	}
}

func worktreeActionConfig(source string) *config.Config {
	return &config.Config{
		Defaults: config.Defaults{Editor: "true"},
		Accounts: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: "/tmp/agentmux-worktree-profile"}},
		Projects: []config.Session{{Title: "api", Directory: source, Profile: "work"}},
	}
}

func writeActionTranscript(t *testing.T, cwd, id string, lastAt time.Time, hidden bool) transcript.SessionMeta {
	t.Helper()
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cwd, id+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return transcript.SessionMeta{ID: id, Harness: harness.Claude, Profile: "work", Project: "api", CWD: cwd, Path: path, StartedAt: lastAt.Add(-time.Minute), LastAt: lastAt, FileSize: info.Size(), FileModTime: info.ModTime(), HiddenFromIndex: hidden}
}

func writeActionTranscriptIndex(t *testing.T, path string, metas ...transcript.SessionMeta) {
	t.Helper()
	entries := make(map[string]transcript.SessionMeta, len(metas))
	for _, meta := range metas {
		entries[transcript.IndexKey(meta.Harness, meta.ID)] = meta
	}
	body, err := json.Marshal(struct {
		Entries map[string]transcript.SessionMeta `json:"entries"`
	}{Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}
