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
