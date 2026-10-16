package session

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestSessionShowPrintsOneConversationBySelector(t *testing.T) {
	resetSessionCommandsTestState(t)
	oldOut := sessionShowCmd.OutOrStdout()
	t.Cleanup(func() {
		sessionShowCmd.SetOut(oldOut)
		sessionShowCmd.SetErr(oldOut)
		_ = sessionShowCmd.Flags().Set("json", "false")
	})
	base, indexPath := sessionCommandMeta(t, harness.Codex)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sessionCommandsNow = func() time.Time { return now }
	meta := base
	meta.ID = "7f67dc81-aaaa"
	meta.Snippet = "fix the\nparser"
	meta.Title = "Parser fix"
	meta.Ending = "The parser\nnow accepts tabs."
	meta.MessageCount = 4
	meta.StartedAt, meta.LastAt = now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	writeSessionCommandIndex(t, indexPath, meta)
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}}}, nil
	}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex, discovered: []transcript.SessionMeta{meta}}}
	}
	run := func(jsonOutput bool, args ...string) (string, error) {
		_ = sessionShowCmd.Flags().Set("json", map[bool]string{true: "true", false: "false"}[jsonOutput])
		var out bytes.Buffer
		sessionShowCmd.SetOut(&out)
		sessionShowCmd.SetErr(&out)
		err := sessionShowCmd.RunE(sessionShowCmd, args)
		return out.String(), err
	}

	out, err := run(false, "7f67")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	for _, want := range []string{
		"session:    codex:7f67dc81-aaaa\n",
		"directory:  " + meta.CWD + "\n",
		"(2h ago)\n",
		"messages:   4\n",
		"transcript: " + meta.Path + "\n",
		"title:      Parser fix\n",
		"summary:    fix the parser\n",
		"ended:      The parser now accepts tabs.\n",
		"continue:   amx session continue codex:7f67dc81-aaaa\n",
		"fork:       amx session fork codex:7f67dc81-aaaa\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("show output missing %q:\n%s", want, out)
		}
	}

	out, err = run(true, "codex:7f67dc81-aaaa")
	if err != nil {
		t.Fatalf("show --json: %v", err)
	}
	var envelope struct {
		SchemaVersion int             `json:"schema_version"`
		Data          sessionShowData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if envelope.SchemaVersion != 1 || envelope.Data.Session.ID != meta.ID || envelope.Data.Session.CWD != meta.CWD {
		t.Fatalf("envelope = %+v", envelope)
	}

	if strings.Contains(out, "live:") || envelope.Data.Session.Live != nil {
		t.Fatalf("show claimed a live tab while the daemon and Kitty could not be asked:\n%s", out)
	}

	// With the lookup answering, show says whether continue would focus a tab
	// or resume, and --json carries the tab.
	sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) {
		return map[string]liveConversation{"codex:7f67dc81-aaaa": {WindowID: 12, TabID: 3}}, true
	}
	if out, err = run(false, "7f67"); err != nil || !strings.Contains(out, "live:       open in Kitty window 12 (continue focuses it)\n") {
		t.Fatalf("show with a live tab = %v:\n%s", err, out)
	}
	if out, err = run(true, "7f67"); err != nil || !strings.Contains(out, `"live":{"window_id":12,"tab_id":3}`) {
		t.Fatalf("show --json with a live tab = %v:\n%s", err, out)
	}
	sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) { return map[string]liveConversation{}, true }
	if out, err = run(false, "7f67"); err != nil || !strings.Contains(out, "live:       no\n") {
		t.Fatalf("show with no live tab = %v:\n%s", err, out)
	}

	if _, err := run(false, "ffffffff"); err == nil || !strings.Contains(err.Error(), "amx session list") {
		t.Fatalf("miss = %v, want the not-found hint", err)
	}
}

// A conversation two profiles share lists both under a plural label; one
// profile keeps the singular.
func TestSessionShowPluralizesSharedProfiles(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	view := sessionView{Harness: "claude", ID: "abc", Profiles: []string{"personal", "work"}, Projects: []string{"api"}}
	if err := writeSessionShow(&out, view, now, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"profiles:   personal, work\n", "project:    api\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("show output missing %q:\n%s", want, out.String())
		}
	}
}

// --last reads the transcript and appends its closing user and assistant
// messages, oldest first; tool records and empty messages are not counted.
func TestSessionShowLastPrintsTheClosingMessages(t *testing.T) {
	resetSessionCommandsTestState(t)
	oldOut := sessionShowCmd.OutOrStdout()
	t.Cleanup(func() {
		sessionShowCmd.SetOut(oldOut)
		sessionShowCmd.SetErr(oldOut)
		_ = sessionShowCmd.Flags().Set("json", "false")
		_ = sessionShowCmd.Flags().Set("last", "0")
		_ = sessionShowCmd.Flags().Set("find", "")
		_ = sessionShowCmd.Flags().Set("match", "1")
		_ = sessionShowCmd.Flags().Set("from", "-1")
		_ = sessionShowCmd.Flags().Set("count", "50")
	})
	base, indexPath := sessionCommandMeta(t, harness.Codex)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sessionCommandsNow = func() time.Time { return now }
	meta := base
	meta.ID = "7f67dc81-aaaa"
	writeSessionCommandIndex(t, indexPath, meta)
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}}}, nil
	}
	messages := map[string][]transcript.Message{meta.Path: {
		{Role: "user", Text: "fix the parser", Timestamp: now.Add(-3 * time.Hour)},
		{Role: "tool", Text: "go test ./..."},
		{Role: "assistant", Text: "Fixed it.\n\nTests pass.", Timestamp: now.Add(-2 * time.Hour)},
		{Role: "assistant", Text: "  "},
		{Role: "user", Text: "commit it"},
	}}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: searchCommandReader{
			sessionCommandReader{harness: harness.Codex, discovered: []transcript.SessionMeta{meta}}, messages,
		}}
	}
	run := func(jsonOutput bool, last string) (string, error) {
		_ = sessionShowCmd.Flags().Set("json", map[bool]string{true: "true", false: "false"}[jsonOutput])
		_ = sessionShowCmd.Flags().Set("last", last)
		var out bytes.Buffer
		sessionShowCmd.SetOut(&out)
		sessionShowCmd.SetErr(&out)
		err := sessionShowCmd.RunE(sessionShowCmd, []string{"7f67"})
		return out.String(), err
	}

	out, err := run(false, "2")
	if err != nil {
		t.Fatalf("show --last 2: %v\n%s", err, out)
	}
	want := "\nlast 2 of 3 user and assistant messages:\n" +
		"\n[assistant] " + formatTime(now.Add(-2*time.Hour)) + " (2h ago)\nFixed it.\n\nTests pass.\n" +
		"\n[user]\ncommit it\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("show --last 2 output ends with:\n%s\nwant suffix:\n%s", out, want)
	}

	if out, err = run(false, "10"); err != nil || !strings.Contains(out, "\nall 3 user and assistant messages:\n\n[user] ") {
		t.Fatalf("show --last 10 = %v:\n%s", err, out)
	}

	out, err = run(true, "1")
	if err != nil {
		t.Fatalf("show --last 1 --json: %v", err)
	}
	var envelope struct {
		Data sessionShowData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	got := envelope.Data
	if got.MessagesTotal == nil || *got.MessagesTotal != 3 || len(got.Messages) != 1 || got.Messages[0].Text != "commit it" || got.Messages[0].Role != "user" {
		t.Fatalf("--json tail = %+v", got)
	}
	if strings.Contains(out, `"timestamp"`) {
		t.Fatalf("a message without a time carried a zero timestamp:\n%s", out)
	}

	// Without --last the JSON payload stays exactly as it was.
	if out, err = run(true, "0"); err != nil || strings.Contains(out, "messages_total") || strings.Contains(out, `"messages":[`) {
		t.Fatalf("show --json without --last = %v:\n%s", err, out)
	}

	if _, err := run(false, "-1"); err == nil || !strings.Contains(err.Error(), "--last -1") {
		t.Fatalf("show --last -1 = %v; want a refusal", err)
	}

	// A search result before the tail opens directly, with message ordinals
	// that a desktop client can use to page and navigate without guessing.
	for range 120 {
		messages[meta.Path] = append(messages[meta.Path], transcript.Message{Role: "assistant", Text: "Later reply"})
	}
	_ = sessionShowCmd.Flags().Set("find", `"fix the parser"`)
	out, err = run(true, "0")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	got = envelope.Data
	if got.MessagesStart == nil || *got.MessagesStart != 0 || got.FocusMessage == nil || *got.FocusMessage != 0 || len(got.Messages) != 50 || len(got.MatchMessages) != 1 {
		t.Fatalf("match window: %+v", got)
	}
	if len(got.MatchTerms) != 1 || got.MatchTerms[0] != "fix the parser" {
		t.Fatalf("phrase terms: %v", got.MatchTerms)
	}
	if _, err := run(true, "10"); err == nil {
		t.Fatal("--last and --find must conflict")
	}
	_ = sessionShowCmd.Flags().Set("from", "50")
	out, err = run(true, "0")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if *envelope.Data.MessagesStart != 50 || len(envelope.Data.Messages) != 50 {
		t.Fatalf("page: %s", out)
	}
}

func TestConversationMatchWindowsKeepProseIdentityAndBoundaries(t *testing.T) {
	messages := []transcript.Message{
		{Role: "tool", Text: "needle"},
		{Role: "user", Text: "first Needle"},
		{Role: "assistant", Text: "middle"},
		{Role: "assistant", Text: "last NEEDLE"},
	}
	window := conversationMessageWindow(messages, "needle", 2, -1, 2)
	if window.Total != 3 || window.Start != 1 || *window.Focus != 2 || len(window.Matches) != 2 || window.Messages[1].Text != "last NEEDLE" {
		t.Fatalf("window = %+v", window)
	}
	missing := conversationMessageWindow(messages, "absent", 1, -1, 50)
	if missing.Focus != nil || len(missing.Matches) != 0 || len(missing.Messages) != 3 {
		t.Fatalf("missing = %+v", missing)
	}
	end := conversationMessageWindow(messages, "needle", 99, 100, 50)
	if end.Start != 3 || len(end.Messages) != 0 || *end.Focus != 2 {
		t.Fatalf("end = %+v", end)
	}
}
