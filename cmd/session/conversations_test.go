package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/spf13/cobra"
)

type sessionCommandKitty struct {
	windows  []kitty.OSWindow
	listErr  error
	launched []kitty.LaunchOpts
	focuses  []string
}

func resetSessionCommandsTestState(t *testing.T) {
	t.Helper()
	oldLoadConfig := sessionCommandsLoadConfig
	oldLoadIndex := sessionCommandsLoadSessionIndex
	oldIndexPath := sessionCommandsSessionIndexPath
	oldReaders := sessionCommandsSessionReaders
	oldNewKitty := sessionCommandsNewKitty
	oldDaemon := sessionCommandsDaemon
	oldLive := sessionCommandsLiveConversations
	oldCurrentWindow := sessionCommandsCurrentWindow
	oldNewHandoff := sessionCommandsNewHandoff
	oldNewWorktree := sessionCommandsNewWorktree
	oldIsTerminal := sessionListIsTerminal
	oldNow := sessionCommandsNow
	oldListOut := sessionListCmd.OutOrStdout()
	oldContinueOut := sessionContinueCmd.OutOrStdout()
	oldForkOut := sessionForkCmd.OutOrStdout()
	oldForkWorktreeOut := sessionForkWorktreeCmd.OutOrStdout()
	oldHandoffOut := sessionHandoffCmd.OutOrStdout()
	t.Cleanup(func() {
		sessionCommandsLoadConfig = oldLoadConfig
		sessionCommandsLoadSessionIndex = oldLoadIndex
		sessionCommandsSessionIndexPath = oldIndexPath
		sessionCommandsSessionReaders = oldReaders
		sessionCommandsNewKitty = oldNewKitty
		sessionCommandsDaemon = oldDaemon
		sessionCommandsLiveConversations = oldLive
		sessionCommandsCurrentWindow = oldCurrentWindow
		sessionCommandsNewHandoff = oldNewHandoff
		sessionCommandsNewWorktree = oldNewWorktree
		sessionListIsTerminal = oldIsTerminal
		sessionCommandsNow = oldNow
		sessionListCmd.SetOut(oldListOut)
		sessionContinueCmd.SetOut(oldContinueOut)
		sessionForkCmd.SetOut(oldForkOut)
		sessionForkWorktreeCmd.SetOut(oldForkWorktreeOut)
		sessionHandoffCmd.SetOut(oldHandoffOut)
		_ = sessionListCmd.Flags().Set("json", "false")
		_ = sessionListCmd.Flags().Set("all", "false")
		_ = sessionSearchCmd.Flags().Set("all", "false")
		_ = sessionContinueCmd.Flags().Set("json", "false")
		_ = sessionForkCmd.Flags().Set("json", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("allow-dirty", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("yes", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("json", "false")
		_ = sessionHandoffCmd.Flags().Set("profile", "")
		_ = sessionHandoffCmd.Flags().Set("edit", "false")
		_ = sessionHandoffCmd.Flags().Set("no-launch", "false")
		_ = sessionHandoffCmd.Flags().Set("json", "false")
		for _, cmd := range []*cobra.Command{sessionListCmd, sessionSearchCmd} {
			if value, ok := cmd.Flags().Lookup("project").Value.(interface{ Replace([]string) error }); ok {
				_ = value.Replace(nil)
			}
		}
	})
	// Live tabs come from the operator's daemon and Kitty unless a test says
	// otherwise; by default neither can be asked.
	sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) { return nil, false }
	sessionCommandsCurrentWindow = func() int { return 0 }
}

func (c *sessionCommandKitty) ListWindows() ([]kitty.OSWindow, error) {
	return c.windows, c.listErr
}
func (c *sessionCommandKitty) LaunchTab(opts kitty.LaunchOpts) error {
	c.launched = append(c.launched, opts)
	return nil
}
func (*sessionCommandKitty) LaunchOverlay(kitty.LaunchOpts) error                     { return nil }
func (*sessionCommandKitty) CloseTab(string) error                                    { return nil }
func (*sessionCommandKitty) MoveTab(string, int) error                                { return nil }
func (*sessionCommandKitty) SetTabColor(string, string, string, string, string) error { return nil }
func (*sessionCommandKitty) SetTabTitle(string, string) error                         { return nil }
func (*sessionCommandKitty) SetUserVars(string, map[string]string) error              { return nil }
func (c *sessionCommandKitty) FocusTab(match string) error {
	c.focuses = append(c.focuses, match)
	return nil
}
func (*sessionCommandKitty) FocusWindow(string) error               { return nil }
func (*sessionCommandKitty) GetText(string, string) (string, error) { return "", nil }
func (*sessionCommandKitty) LaunchOverlayPager(string) error        { return nil }

type sessionCommandReader struct {
	harness     string
	discovered  []transcript.SessionMeta
	discoverErr error
	calls       *int
}

func (r sessionCommandReader) Harness() string { return r.harness }
func (r sessionCommandReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	if r.calls != nil {
		(*r.calls)++
	}
	// A real reader classifies every session it discovers; an unclassified
	// fixture would read as automated-or-unknown and leave list and search.
	discovered := make([]transcript.SessionMeta, len(r.discovered))
	for i, meta := range r.discovered {
		discovered[i] = withMainClass(meta)
	}
	return discovered, r.discoverErr
}

func automated(meta transcript.SessionMeta) transcript.SessionMeta {
	meta.SessionClass = transcript.SessionClassAutomated
	return meta
}

func withMainClass(meta transcript.SessionMeta) transcript.SessionMeta {
	if meta.SessionClass == "" {
		meta.SessionClass = transcript.SessionClassMain
	}
	return meta
}
func (sessionCommandReader) ReadMessages(string) ([]transcript.Message, error) { return nil, nil }

type sessionCommandHandoff struct {
	requests []handoff.Request
	result   handoff.Result
	err      error
}

type sessionCommandWorktree struct {
	requests []worktree.Request
	result   worktree.Result
}

func (w *sessionCommandWorktree) Create(_ context.Context, request worktree.Request) (worktree.Result, error) {
	w.requests = append(w.requests, request)
	return w.result, nil
}

func (*sessionCommandWorktree) RemoveUnused(context.Context, worktree.Result) error { return nil }

func (h *sessionCommandHandoff) Run(_ context.Context, request handoff.Request) (handoff.Result, error) {
	h.requests = append(h.requests, request)
	return h.result, h.err
}

func TestSessionListRefreshesFilesystemIndexAndEmitsTextAndJSON(t *testing.T) {
	resetSessionCommandsTestState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	source := filepath.Join(dir, "fresh.jsonl")
	if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	meta := transcript.SessionMeta{
		ID: "fresh", Harness: harness.Codex, CWD: dir, Path: source,
		FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
	}
	indexPath := filepath.Join(dir, "transcripts.json")
	discoverCalls := 0
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{
			Profiles: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: dir}},
			Projects: []config.Session{
				{Title: "Primary", Directory: dir, Profile: "work"},
				{Title: "Alias", Directory: dir, Profile: "work"},
			},
		}, nil
	}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{
			harness: harness.Codex, discovered: []transcript.SessionMeta{meta}, calls: &discoverCalls,
		}}
	}

	if err := sessionListCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionListCmd.SetOut(&out)
	if err := sessionListCmd.RunE(sessionListCmd, nil); err != nil {
		t.Fatalf("JSON list: %v", err)
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Data          sessionListData
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != cli.JSONSchemaVersion || len(payload.Data.Sessions) != 1 || payload.Data.Sessions[0].ID != meta.ID {
		t.Fatalf("payload = %+v", payload)
	}
	view := payload.Data.Sessions[0]
	if view.Project != "Primary" || strings.Join(view.Projects, ",") != "Primary,Alias" {
		t.Fatalf("session JSON projects = %q %#v, want Primary [Primary Alias]", view.Project, view.Projects)
	}
	persisted, err := transcript.LoadIndexAt(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := persisted.Get(transcript.IndexKey(meta.Harness, meta.ID)); !found || discoverCalls != 1 {
		t.Fatalf("filesystem refresh found=%t calls=%d", found, discoverCalls)
	}

	if err := sessionListCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := sessionListCmd.RunE(sessionListCmd, nil); err != nil {
		t.Fatalf("text list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "codex\twork\tPrimary\t") || !strings.Contains(got, "\tfresh\t\t\t1\t?\t\n") || strings.Contains(got, "Alias") {
		t.Fatalf("text list = %q", got)
	}
	assertNoRetiredDatabaseState(t, home, "session list")
}

func TestSessionContinueCLIUsesExactNativeInvocation(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Codex)
	client := &sessionCommandKitty{}
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{
			Defaults: config.Defaults{Args: []string{"--model", "test"}},
			Accounts: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: "/tmp/codex-home"}},
			Profiles: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: "/tmp/codex-home"}},
			Projects: []config.Session{{Title: "api", Directory: meta.CWD, Profile: "work", Args: []string{"--project-flag"}}},
		}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	info, err := os.Stat(meta.Path)
	if err != nil {
		t.Fatal(err)
	}
	meta.FileSize, meta.FileModTime = info.Size(), info.ModTime()
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	discoverCalls := 0
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{
			harness: harness.Codex, discovered: []transcript.SessionMeta{meta}, calls: &discoverCalls,
		}}
	}
	sessionCommandsNewKitty = func() kitty.KittyClient { return client }
	sessionCommandsDaemon = func(protocol.Request) (protocol.Response, error) { return protocol.Response{OK: true}, nil }
	if err := sessionContinueCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionContinueCmd.SetOut(&out)

	if err := sessionContinueCmd.RunE(sessionContinueCmd, []string{harness.Codex, meta.ID}); err != nil {
		t.Fatalf("session continue: %v", err)
	}
	if len(client.launched) != 1 {
		t.Fatalf("launches = %+v, want one", client.launched)
	}
	opts := client.launched[0]
	if opts.Directory != meta.CWD || opts.UserVars["amx_harness"] != harness.Codex || opts.UserVars["amx_profile"] != "work" {
		t.Fatalf("launch context = %+v, want indexed source context", opts)
	}
	if got := strings.Join(opts.Cmdline, " "); !strings.Contains(got, "codex --project-flag -c log_dir=") || !strings.Contains(got, " resume session-1") {
		t.Fatalf("command = %q, want Codex resume argv", got)
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Data          sessionActionData
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != cli.JSONSchemaVersion || payload.Data.Action != "session.continue" || payload.Data.Data["continuation"] != "resumed" {
		t.Fatalf("payload = %+v", payload)
	}
	if discoverCalls != 1 {
		t.Fatalf("conversation resolution refresh calls = %d, want 1", discoverCalls)
	}
	if err := sessionContinueCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := sessionContinueCmd.RunE(sessionContinueCmd, []string{harness.Codex, meta.ID}); err != nil {
		t.Fatalf("text session continue: %v", err)
	}
	if got := out.String(); got != "resumed indexed conversation\n" {
		t.Fatalf("text output = %q", got)
	}
}

func TestConversationIdentityAcceptsBothSelectorShapes(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"codex", "session-1"},
		{"codex:session-1"},
		{" Codex:session-1"},
	} {
		identity, err := conversationIdentityFromArgs(sessionContinueCmd, false, "continue", args)
		if err != nil {
			t.Fatalf("args %q: %v", args, err)
		}
		if identity.Harness != harness.Codex || identity.SessionID != "session-1" {
			t.Fatalf("args %q: identity = %+v", args, identity)
		}
	}
	if _, err := conversationIdentityFromArgs(sessionContinueCmd, false, "continue", []string{"codex:"}); err == nil {
		t.Fatal("an empty session ID after the colon must be refused")
	}
}

// A command run with no selector at all asks for the conversation rather than
// for a harness, since a bare session ID or prefix no longer needs one.
func TestMissingSelectorAsksForTheConversation(t *testing.T) {
	t.Parallel()

	_, err := conversationIdentityFromArgs(sessionContinueCmd, false, "continue", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "conversation is required: ") {
		t.Fatalf("text error = %v, want conversation is required with its selector forms", err)
	}
	for _, want := range []string{"<harness>:<session-id>", "unique prefix", "`amx session list`"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("text error = %v, want it to name %q", err, want)
		}
	}

	var out bytes.Buffer
	sessionShowCmd.SetOut(&out)
	t.Cleanup(func() { sessionShowCmd.SetOut(nil) })
	if _, err := conversationIdentityFromArgs(sessionShowCmd, true, "show", []string{" "}); err == nil {
		t.Fatal("a blank selector must be refused")
	}
	var payload cli.JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.Error.Code != "missing_parameter" || len(payload.Error.Parameters) != 1 || payload.Error.Parameters[0].Name != "conversation" {
		t.Fatalf("payload = %+v, want missing_parameter naming conversation", payload.Error)
	}
	if !strings.Contains(payload.Error.Parameters[0].Description, "unique prefix") {
		t.Fatalf("description = %q, want every selector form named", payload.Error.Parameters[0].Description)
	}
}

func TestParseSinceWindow(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "12h": 12 * time.Hour, " 90m ": 90 * time.Minute, "2w": 14 * 24 * time.Hour} {
		if got, err := parseSinceWindow(in); err != nil || got != want {
			t.Errorf("parseSinceWindow(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0d", "-1d", "1.5d", "-2h", "week", "0w", "1.5w"} {
		if _, err := parseSinceWindow(bad); err == nil {
			t.Errorf("parseSinceWindow(%q) accepted a bad window", bad)
		}
	}
}

func TestBareSessionIDResolvesAcrossHarnessesAndRefusesToGuess(t *testing.T) {
	resetSessionCommandsTestState(t)
	codexMeta, indexPath := sessionCommandMeta(t, harness.Codex)
	claudeMeta := codexMeta
	claudeMeta.Harness = harness.Claude
	uniqueMeta := codexMeta
	uniqueMeta.ID = "only-codex"
	writeSessionCommandIndex(t, indexPath, codexMeta, claudeMeta, uniqueMeta)
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}, {Name: "home", Harness: harness.Claude}}}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	readers := map[string]transcript.Reader{
		harness.Codex:  sessionCommandReader{harness: harness.Codex, discovered: []transcript.SessionMeta{codexMeta, uniqueMeta}},
		harness.Claude: sessionCommandReader{harness: harness.Claude, discovered: []transcript.SessionMeta{claudeMeta}},
	}

	identity, err := conversationIdentityFromArgs(sessionContinueCmd, false, "continue", []string{"only-codex"})
	if err != nil || identity.Harness != "" || identity.SessionID != "only-codex" {
		t.Fatalf("bare ID identity = %+v, %v", identity, err)
	}
	meta, err := resolveIndexedConversation(cfg, readers, identity)
	if err != nil || meta == nil || meta.Harness != harness.Codex || meta.ID != "only-codex" {
		t.Fatalf("unique bare ID resolved to %+v, %v", meta, err)
	}

	_, err = resolveIndexedConversation(cfg, readers, conversation.Identity{SessionID: "session-1"})
	var actionErr *actions.Error
	if !errors.As(err, &actionErr) || actionErr.Code != "ambiguous_conversation" {
		t.Fatalf("shared bare ID error = %v, want ambiguous_conversation", err)
	}
	var ids []string
	for _, candidate := range actionErr.Candidates {
		ids = append(ids, candidate.ID)
	}
	if got := strings.Join(ids, ","); got != "claude:session-1,codex:session-1" {
		t.Fatalf("candidates = %s, want ordered harness-qualified selectors", got)
	}

	if meta, err := resolveIndexedConversation(cfg, readers, conversation.Identity{SessionID: "missing"}); err != nil || meta != nil {
		t.Fatalf("unknown bare ID = %+v, %v; want a plain miss", meta, err)
	}

	// A lone harness name is still a harness missing its session ID.
	if _, err := conversationIdentityFromArgs(sessionContinueCmd, false, "continue", []string{"Codex"}); err == nil {
		t.Fatal("a lone harness name must report the missing session ID")
	}
}

func TestSessionIDPrefixResolvesLikeAShortGitHash(t *testing.T) {
	resetSessionCommandsTestState(t)
	base, indexPath := sessionCommandMeta(t, harness.Codex)
	meta := func(harnessName, id string) transcript.SessionMeta {
		m := base
		m.Harness, m.ID = harnessName, id
		return m
	}
	codex := []transcript.SessionMeta{meta(harness.Codex, "7f67dc81-aaaa"), meta(harness.Codex, "7f67dc81-bbbb"), meta(harness.Codex, "abcd")}
	claude := []transcript.SessionMeta{meta(harness.Claude, "7f6799ee"), meta(harness.Claude, "abcdef")}
	writeSessionCommandIndex(t, indexPath, append(append([]transcript.SessionMeta{}, codex...), claude...)...)
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}, {Name: "home", Harness: harness.Claude}}}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	readers := map[string]transcript.Reader{
		harness.Codex:  sessionCommandReader{harness: harness.Codex, discovered: codex},
		harness.Claude: sessionCommandReader{harness: harness.Claude, discovered: claude},
	}

	for _, tc := range []struct {
		name, harness, id, want string
	}{
		{"unique prefix", "", "7f67dc81-a", "codex:7f67dc81-aaaa"},
		{"prefix within a harness", harness.Claude, "7f67", "claude:7f6799ee"},
		{"an exact ID beats a longer one it prefixes", "", "abcd", "codex:abcd"},
		{"the harness still scopes the prefix", harness.Claude, "abcd", "claude:abcdef"},
		{"too short to be a prefix", "", "7f6", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveIndexedConversation(cfg, readers, conversation.Identity{Harness: tc.harness, SessionID: tc.id})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			selector := ""
			if got != nil {
				selector = got.Harness + ":" + got.ID
			}
			if selector != tc.want {
				t.Fatalf("resolved %q, want %q", selector, tc.want)
			}
		})
	}

	_, err := resolveIndexedConversation(cfg, readers, conversation.Identity{SessionID: "7f67"})
	var actionErr *actions.Error
	if !errors.As(err, &actionErr) || actionErr.Code != "ambiguous_conversation" {
		t.Fatalf("shared prefix error = %v, want ambiguous_conversation", err)
	}
	var ids []string
	for _, candidate := range actionErr.Candidates {
		ids = append(ids, candidate.ID)
	}
	if got := strings.Join(ids, ","); got != "claude:7f6799ee,codex:7f67dc81-aaaa,codex:7f67dc81-bbbb" {
		t.Fatalf("candidates = %s", got)
	}
	if len(actionErr.MissingParameters) != 1 || actionErr.MissingParameters[0].Name != "session-id" {
		t.Fatalf("missing parameters = %+v, want a longer session-id", actionErr.MissingParameters)
	}

	if !actionErr.MissingParameters[0].Positional {
		t.Fatal("the session ID is an argument, not a flag; its hint must say so")
	}

	// A miss says what to do next: a too-short prefix is named as such, and
	// anything else points at the list.
	for id, hint := range map[string]string{"7f6": "at least 4 characters", "ffffffff": "amx session list"} {
		_, err := resolveConversationOrFail(sessionContinueCmd, false, cfg, readers, conversation.Identity{SessionID: id})
		if err == nil || !strings.Contains(err.Error(), hint) {
			t.Fatalf("miss for %q = %v, want hint %q", id, err, hint)
		}
	}
}

func TestSessionListSelectorsNarrowTheList(t *testing.T) {
	resetSessionCommandsTestState(t)
	oldGetwd := sessionListGetwd
	t.Cleanup(func() {
		sessionListGetwd = oldGetwd
		for _, name := range []string{"here", "live", "harness", "profile", "project", "limit", "json"} {
			flag := sessionListCmd.Flags().Lookup(name)
			if slice, ok := flag.Value.(interface{ Replace([]string) error }); ok {
				_ = slice.Replace(nil)
			} else {
				_ = flag.Value.Set(flag.DefValue)
			}
			flag.Changed = false
		}
	})
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sessionCommandsNow = func() time.Time { return now }
	entry := func(id, harnessName, cwd string, age time.Duration, snippet string) transcript.SessionMeta {
		source := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		return transcript.SessionMeta{
			ID: id, Harness: harnessName, CWD: cwd, Path: source, Snippet: snippet,
			FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
			StartedAt: now.Add(-age), LastAt: now.Add(-age),
		}
	}
	codex := []transcript.SessionMeta{
		entry("codex-root", harness.Codex, repo, time.Minute, "fix the parser"),
		// "repo2" shares a string prefix with "repo" and must not count as inside it.
		entry("codex-sibling", harness.Codex, repo+"2", 3*time.Minute, "unrelated"),
		automated(entry("codex-asl", harness.Codex, repo, 30*time.Second, "# ASL session task packet")),
	}
	claude := []transcript.SessionMeta{
		entry("claude-sub", harness.Claude, filepath.Join(repo, "sub"), 2*time.Minute, strings.Repeat("long prompt ", 20)),
	}
	sessionCommandsSessionIndexPath = func() string { return filepath.Join(dir, "transcripts.json") }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Profiles: []config.Account{
			{Name: "work", Harness: harness.Codex, HomeDir: filepath.Join(dir, "codex-home")},
			{Name: "personal", Harness: harness.Claude, HomeDir: filepath.Join(dir, "claude-home")},
		}, Projects: []config.Session{{Title: "🐱repo", Directory: repo, Profile: "work"}}}, nil
	}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{
			harness.Codex:  sessionCommandReader{harness: harness.Codex, discovered: codex},
			harness.Claude: sessionCommandReader{harness: harness.Claude, discovered: claude},
		}
	}
	sessionListGetwd = func() (string, error) { return repo, nil }

	run := func(t *testing.T, flags map[string]string) (string, error) {
		t.Helper()
		for _, name := range []string{"since", "here", "live", "harness", "profile", "project", "limit", "all"} {
			flag := sessionListCmd.Flags().Lookup(name)
			if slice, ok := flag.Value.(interface{ Replace([]string) error }); ok {
				_ = slice.Replace(nil)
			} else {
				_ = flag.Value.Set(flag.DefValue)
			}
			flag.Changed = false
		}
		for name, value := range flags {
			if err := sessionListCmd.Flags().Set(name, value); err != nil {
				t.Fatal(err)
			}
		}
		var out bytes.Buffer
		sessionListCmd.SetOut(&out)
		sessionListCmd.SetErr(&out)
		err := sessionListCmd.RunE(sessionListCmd, nil)
		return out.String(), err
	}
	ids := func(text string) string {
		var got []string
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			got = append(got, strings.Split(line, "\t")[4])
		}
		return strings.Join(got, ",")
	}

	for _, tc := range []struct {
		name  string
		flags map[string]string
		want  string
	}{
		{"everything, newest first", nil, "codex-root,claude-sub,codex-sibling"},
		{"harness", map[string]string{"harness": "CODEX"}, "codex-root,codex-sibling"},
		{"profile", map[string]string{"profile": "personal"}, "claude-sub"},
		{"here includes subdirectories but not prefix siblings", map[string]string{"here": "true"}, "codex-root,claude-sub"},
		{"limit keeps the most recent", map[string]string{"limit": "1"}, "codex-root"},
		{"selectors combine", map[string]string{"here": "true", "harness": "claude"}, "claude-sub"},
		{"all includes automated sessions", map[string]string{"all": "true"}, "codex-asl,codex-root,claude-sub,codex-sibling"},
		{"all combines with selectors", map[string]string{"all": "true", "limit": "1"}, "codex-asl"},
		// "repo" is no project's exact name, so it is read the way a launch
		// reads a title and reaches "🐱repo".
		{"project by typed title", map[string]string{"project": "repo"}, "codex-root,claude-sub"},
		{"project by exact title", map[string]string{"project": "🐱repo"}, "codex-root,claude-sub"},
		// A session outside every configured project is named after its
		// directory, and that name selects it too.
		{"project named by its directory", map[string]string{"project": "repo2"}, "codex-sibling"},
		{"projects combine", map[string]string{"project": "repo,repo2", "harness": "codex"}, "codex-root,codex-sibling"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, tc.flags)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if got := ids(out); got != tc.want {
				t.Fatalf("sessions = %s, want %s\n%s", got, tc.want, out)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		flags map[string]string
		want  string
	}{
		{"unknown harness", map[string]string{"harness": "gemini"}, `--harness "gemini"`},
		{"unknown profile", map[string]string{"profile": "nobody"}, `--profile "nobody": want one of`},
		{"negative limit", map[string]string{"limit": "-1"}, "--limit -1"},
		{"unknown project", map[string]string{"project": "nowhere"}, `--project "nowhere": no project or indexed session has this title; ` + "`amx project list`"},
		// A value that names nothing still suggests what it most likely
		// meant, without selecting it: a part of a title, then a typo of one.
		{"part of a project title", map[string]string{"project": "rep"}, `did you mean "🐱repo" or "repo2"?`},
		{"typo of a project title", map[string]string{"project": "rpeo"}, `did you mean "🐱repo"?`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, tc.flags)
			if err == nil || !strings.Contains(out+err.Error(), tc.want) {
				t.Fatalf("err = %v, output %q, want a refusal naming %s", err, out, tc.want)
			}
		})
	}

	// --live keeps the conversations open in Kitty, and does so before
	// --limit, so the limit counts open conversations rather than cutting the
	// list first and filtering what was left.
	t.Run("live keeps open conversations before the limit", func(t *testing.T) {
		sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) {
			return map[string]liveConversation{
				"claude:claude-sub":   {WindowID: 7, TabID: 3},
				"codex:codex-sibling": {WindowID: 8, TabID: 4},
			}, true
		}
		t.Cleanup(func() {
			sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) { return nil, false }
		})
		out, err := run(t, map[string]string{"live": "true"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if got := ids(out); got != "claude-sub,codex-sibling" {
			t.Fatalf("sessions = %s, want claude-sub,codex-sibling\n%s", got, out)
		}
		out, err = run(t, map[string]string{"live": "true", "limit": "1"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if got := ids(out); got != "claude-sub" {
			t.Fatalf("sessions = %s, want claude-sub\n%s", got, out)
		}
	})

	// With nobody to ask, --live refuses rather than printing no rows, which
	// would read as "nothing is open".
	t.Run("live refuses when nobody can say what is open", func(t *testing.T) {
		out, err := run(t, map[string]string{"live": "true"})
		if err == nil || !strings.Contains(out+err.Error(), "--live: cannot tell which conversations are open") {
			t.Fatalf("err = %v, output %q, want a refusal", err, out)
		}
	})

	// An empty table names the filters that emptied it, and says when the
	// automated sessions it hid would have matched.
	t.Run("terminal says which filters matched nothing", func(t *testing.T) {
		sessionListIsTerminal = func(io.Writer) bool { return true }
		t.Cleanup(func() { sessionListIsTerminal = func(io.Writer) bool { return false } })
		out, err := run(t, map[string]string{"harness": "claude", "profile": "work"})
		if err != nil || out != "No indexed sessions match --harness claude --profile work.\n" {
			t.Fatalf("empty = %q, %v", out, err)
		}
		out, err = run(t, map[string]string{"since": "45s"})
		if err != nil || out != "No indexed sessions match --since 45s. Automated sessions do; --all shows them.\n" {
			t.Fatalf("empty = %q, %v", out, err)
		}
		out, err = run(t, map[string]string{"project": "🐱repo", "harness": "copilot", "here": "true"})
		if err != nil || out != "No indexed sessions match --here --harness copilot --project 🐱repo.\n" {
			t.Fatalf("empty = %q, %v", out, err)
		}
	})

	// A table that hid automated sessions says how many --all would add,
	// counted under the same selectors, and says nothing once --all is given.
	t.Run("terminal table counts the automated sessions it hid", func(t *testing.T) {
		sessionListIsTerminal = func(io.Writer) bool { return true }
		t.Cleanup(func() { sessionListIsTerminal = func(io.Writer) bool { return false } })
		out, err := run(t, nil)
		if err != nil || !strings.HasSuffix(out, "\n1 automated session is hidden; --all includes it.\n") {
			t.Fatalf("list = %q, %v", out, err)
		}
		for _, flags := range []map[string]string{{"all": "true"}, {"harness": "claude"}} {
			out, err = run(t, flags)
			if err != nil || strings.Contains(out, "hidden") {
				t.Fatalf("list %v = %q, %v", flags, out, err)
			}
		}
	})

	// --here alone says which directory it looked in, the way continue --here
	// refuses, since "match --here" names no directory.
	t.Run("terminal here alone names the directory", func(t *testing.T) {
		sessionListIsTerminal = func(io.Writer) bool { return true }
		empty := filepath.Join(dir, "empty")
		sessionListGetwd = func() (string, error) { return empty, nil }
		t.Cleanup(func() {
			sessionListIsTerminal = func(io.Writer) bool { return false }
			sessionListGetwd = func() (string, error) { return repo, nil }
		})
		out, err := run(t, map[string]string{"here": "true"})
		want := "No conversation was started in " + cli.DisplayPath(empty) + " or below it; `amx session list` shows recent ones elsewhere.\n"
		if err != nil || out != want {
			t.Fatalf("empty = %q, %v; want %q", out, err, want)
		}
	})

	t.Run("terminal table carries a bounded summary", func(t *testing.T) {
		sessionListIsTerminal = func(io.Writer) bool { return true }
		t.Cleanup(func() { sessionListIsTerminal = func(io.Writer) bool { return false } })
		out, err := run(t, map[string]string{"profile": "personal"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 || !strings.HasSuffix(lines[0], "SUMMARY") {
			t.Fatalf("table = %q, want a header ending in SUMMARY and one row", out)
		}
		// The ID is cut to the prefix that still names it alone in the index.
		_, summary, found := strings.Cut(lines[1], " claude-s  ")
		if !found {
			t.Fatalf("row = %q, want the session ID abbreviated to claude-s", lines[1])
		}
		if !strings.HasPrefix(summary, "long prompt") || !strings.HasSuffix(summary, "…") || layout.Width(summary) > sessionSummaryWidth {
			t.Fatalf("summary = %q, want the prompt cut to %d cells", summary, sessionSummaryWidth)
		}
	})
}

// `session continue --here` acts on the newest interactive conversation
// started in the current directory or below it, and says which one before
// acting. Newer conversations elsewhere, and automated ones here, are not it.
func TestSessionContinueHerePicksTheNewestConversationHere(t *testing.T) {
	resetSessionCommandsTestState(t)
	oldGetwd := sessionListGetwd
	t.Cleanup(func() {
		sessionListGetwd = oldGetwd
		_ = sessionContinueCmd.Flags().Set("here", "false")
		sessionContinueCmd.SetErr(nil)
	})
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sessionCommandsNow = func() time.Time { return now }
	entry := func(id, cwd string, age time.Duration, title string) transcript.SessionMeta {
		source := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		return transcript.SessionMeta{
			ID: id, Harness: harness.Codex, CWD: cwd, Path: source, Title: title, Snippet: "prompt " + id,
			FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
			StartedAt: now.Add(-age), LastAt: now.Add(-age),
		}
	}
	discovered := []transcript.SessionMeta{
		entry("elsewhere", filepath.Join(dir, "other"), time.Minute, "Other work"),
		automated(entry("asl-run", repo, 2*time.Minute, "")),
		entry("wanted", filepath.Join(repo, "sub"), 5*time.Minute, "Parser fix"),
		entry("older", repo, time.Hour, "Older"),
	}
	sessionCommandsSessionIndexPath = func() string { return filepath.Join(dir, "transcripts.json") }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{
			Profiles: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: filepath.Join(dir, "codex-home")}},
			Projects: []config.Session{{Title: "repo", Directory: repo, Profile: "work"}},
		}, nil
	}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex, discovered: discovered}}
	}
	client := &sessionCommandKitty{}
	sessionCommandsNewKitty = func() kitty.KittyClient { return client }
	sessionCommandsDaemon = func(protocol.Request) (protocol.Response, error) { return protocol.Response{OK: true}, nil }
	sessionListGetwd = func() (string, error) { return repo, nil }
	if err := sessionContinueCmd.Flags().Set("here", "true"); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	sessionContinueCmd.SetOut(&out)
	sessionContinueCmd.SetErr(&errOut)

	if err := sessionContinueCmd.RunE(sessionContinueCmd, nil); err != nil {
		t.Fatalf("continue --here: %v\n%s", err, out.String())
	}
	if len(client.launched) != 1 || !strings.Contains(strings.Join(client.launched[0].Cmdline, " "), " resume wanted") {
		t.Fatalf("launches = %+v, want a resume of the conversation named wanted", client.launched)
	}
	if got, want := errOut.String(), "Picked codex:wanted, active 5m ago: Parser fix\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}

	t.Run("a selector beside --here is refused", func(t *testing.T) {
		err := sessionContinueCmd.RunE(sessionContinueCmd, []string{"older"})
		if err == nil || !strings.Contains(err.Error(), "pass either --here or a selector") {
			t.Fatalf("err = %v, want a refusal", err)
		}
	})

	t.Run("nothing here is a miss that says where it looked", func(t *testing.T) {
		sessionListGetwd = func() (string, error) { return filepath.Join(dir, "empty"), nil }
		err := sessionContinueCmd.RunE(sessionContinueCmd, nil)
		if err == nil || !strings.Contains(err.Error(), "--here: no conversation was started in") {
			t.Fatalf("err = %v, want a miss", err)
		}
	})
}

func TestAmbiguousPrefixCandidatesListTheNewestFirst(t *testing.T) {
	resetSessionCommandsTestState(t)
	base, indexPath := sessionCommandMeta(t, harness.Codex)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	meta := func(id string, age time.Duration) transcript.SessionMeta {
		m := base
		m.ID, m.LastAt = id, now.Add(-age)
		return m
	}
	metas := []transcript.SessionMeta{meta("beef-old", 48*time.Hour), meta("beef-new", time.Hour), meta("beef-mid", 24*time.Hour), meta("beef-tie", time.Hour)}
	writeSessionCommandIndex(t, indexPath, metas...)
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}}}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	readers := map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex, discovered: metas}}

	_, err := resolveIndexedConversation(cfg, readers, conversation.Identity{SessionID: "beef"})
	var actionErr *actions.Error
	if !errors.As(err, &actionErr) {
		t.Fatalf("error = %v, want an ambiguity", err)
	}
	var ids []string
	for _, candidate := range actionErr.Candidates {
		ids = append(ids, candidate.ID)
	}
	if got, want := strings.Join(ids, ","), "codex:beef-new,codex:beef-tie,codex:beef-mid,codex:beef-old"; got != want {
		t.Fatalf("candidates = %s, want %s", got, want)
	}
}

// fork shares continue's --here path, so it offers the same flag.
func TestSessionForkOffersHereLikeContinue(t *testing.T) {
	t.Parallel()

	for _, cmd := range []*cobra.Command{sessionContinueCmd, sessionForkCmd} {
		if cmd.Flags().Lookup("here") == nil {
			t.Errorf("session %s does not declare --here", cmd.Name())
		}
	}
}
