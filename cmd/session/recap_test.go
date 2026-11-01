package session

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/recap"
	"github.com/sglavoie/agentmux/internal/transcript"
)

type recapReader struct {
	sessionCommandReader
	messages []transcript.Message
}

func TestRecapRejectsInvalidArgumentsBeforeReadingSources(t *testing.T) {
	resetSessionCommandsTestState(t)
	oldOutput := sessionRecapCmd.OutOrStdout()
	t.Cleanup(func() {
		sessionRecapCmd.SetOut(oldOutput)
		for _, flag := range []string{"json", "cached", "refresh"} {
			_ = sessionRecapCmd.Flags().Set(flag, "false")
		}
	})
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		t.Fatal("invalid request reached configuration/source loading")
		return nil, nil
	}
	for _, args := range [][]string{nil, {"one", "two", "three"}} {
		var out bytes.Buffer
		sessionRecapCmd.SetOut(&out)
		_ = sessionRecapCmd.Flags().Set("json", "true")
		code := "invalid_arguments"
		if len(args) == 0 {
			code = "missing_parameter"
		}
		if err := sessionRecapCmd.RunE(sessionRecapCmd, args); err == nil || !strings.Contains(out.String(), code) {
			t.Fatalf("args %v: %s, %v", args, out.String(), err)
		}
	}
	var out bytes.Buffer
	sessionRecapCmd.SetOut(&out)
	_ = sessionRecapCmd.Flags().Set("cached", "true")
	_ = sessionRecapCmd.Flags().Set("refresh", "true")
	if err := sessionRecapCmd.RunE(sessionRecapCmd, []string{"codex:example"}); err == nil || !strings.Contains(out.String(), "invalid_arguments") {
		t.Fatalf("conflicting flags: %s, %v", out.String(), err)
	}
}

func (r recapReader) ReadMessages(string) ([]transcript.Message, error) { return r.messages, nil }

type recapGenerator struct{ calls *int }

func (g recapGenerator) Generate(_ context.Context, input recap.Input) (recap.Content, error) {
	*g.calls++
	return recap.Content{Goal: recap.Claim{Text: "Implement a feature", Messages: []int{input.Messages[0].Index}}}, nil
}

func TestRecapCommandSharesCacheAcrossHarnessesAndHonorsSettings(t *testing.T) {
	resetSessionCommandsTestState(t)
	oldService, oldOutput := sessionRecapService, sessionRecapCmd.OutOrStdout()
	oldContext := sessionRecapCmd.Context()
	sessionRecapCmd.SetContext(t.Context())
	t.Cleanup(func() {
		sessionRecapCmd.SetContext(oldContext)
		sessionRecapService = oldService
		sessionRecapCmd.SetOut(oldOutput)
		for _, flag := range []string{"json", "cached", "refresh"} {
			_ = sessionRecapCmd.Flags().Set(flag, "false")
		}
	})
	dir := t.TempDir()
	source := filepath.Join(dir, "conversation.jsonl")
	if err := os.WriteFile(source, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Profiles: []config.Account{{Name: "summarizer", Harness: "claude", HomeDir: dir}, {Name: "source", Harness: "codex", HomeDir: dir}}}
	cfg.Defaults.Recaps.Profile = "summarizer"
	sessionCommandsLoadConfig = func() (*config.Config, error) { return cfg, nil }
	sessionCommandsSessionIndexPath = func() string { return filepath.Join(dir, "index.json") }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{"codex": recapReader{sessionCommandReader: sessionCommandReader{harness: "codex", discovered: []transcript.SessionMeta{
			{ID: "example", Harness: "codex", Path: source, CWD: dir, MessageCount: 1, FileSize: info.Size(), FileModTime: info.ModTime()},
		}}, messages: []transcript.Message{{Role: "user", Text: "Implement a feature"}}}}
	}
	calls := 0
	sessionRecapService = func(profile config.Account, model string) recap.Service {
		if profile.Name != "summarizer" || model != "haiku" {
			t.Fatalf("wrong summarizer %+v, %s", profile, model)
		}
		return recap.Service{Directory: filepath.Join(dir, "recaps"), Generator: recapGenerator{calls: &calls}}
	}
	run := func(cached, force bool) recap.Report {
		t.Helper()
		var output bytes.Buffer
		sessionRecapCmd.SetOut(&output)
		_ = sessionRecapCmd.Flags().Set("json", "true")
		if cached {
			_ = sessionRecapCmd.Flags().Set("cached", "true")
		} else {
			_ = sessionRecapCmd.Flags().Set("cached", "false")
		}
		if force {
			_ = sessionRecapCmd.Flags().Set("refresh", "true")
		} else {
			_ = sessionRecapCmd.Flags().Set("refresh", "false")
		}
		if err := sessionRecapCmd.RunE(sessionRecapCmd, []string{"codex:example"}); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Schema int          `json:"schema_version"`
			Data   recap.Report `json:"data"`
		}
		if err := json.Unmarshal(output.Bytes(), &envelope); err != nil || envelope.Schema != 1 {
			t.Fatalf("invalid JSON %s, %v", output.String(), err)
		}
		return envelope.Data
	}
	if got := run(true, false); got.Status != "missing" || calls != 0 {
		t.Fatalf("cached %+v, calls %d", got, calls)
	}
	if got := run(false, false); got.Status != "ready" || got.Conversation != "codex:example" || calls != 1 {
		t.Fatalf("generated %+v, calls %d", got, calls)
	}
	if got := run(false, false); got.Recap.Profile != "summarizer" || calls != 1 {
		t.Fatalf("reused %+v, calls %d", got, calls)
	}
	if got := run(false, true); got.Status != "ready" || calls != 2 {
		t.Fatalf("refresh %+v, calls %d", got, calls)
	}
	disabled := false
	cfg.Defaults.Recaps.Enabled = &disabled
	if got := run(false, false); got.Status != "disabled" || calls != 2 {
		t.Fatalf("disabled %+v, calls %d", got, calls)
	}
}
