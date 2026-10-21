package session

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// searchCommandReader serves fixed messages per transcript path, so a search
// with no sidecar yet takes the reader fallback the way a fresh index does.
type searchCommandReader struct {
	sessionCommandReader
	messages map[string][]transcript.Message
}

func (r searchCommandReader) ReadMessages(path string) ([]transcript.Message, error) {
	return r.messages[path], nil
}

func TestSessionSearchFindsConversationsByMessageText(t *testing.T) {
	resetSessionCommandsTestState(t)
	oldGetwd := sessionListGetwd
	oldOut := sessionSearchCmd.OutOrStdout()
	resetFlags := func() {
		for _, name := range []string{"here", "harness", "profile", "limit", "since", "json", "source", "all"} {
			flag := sessionSearchCmd.Flags().Lookup(name)
			if slice, ok := flag.Value.(interface{ Replace([]string) error }); ok {
				_ = slice.Replace(nil)
			} else {
				_ = flag.Value.Set(flag.DefValue)
			}
			flag.Changed = false
		}
	}
	t.Cleanup(func() {
		sessionListGetwd = oldGetwd
		sessionSearchCmd.SetOut(oldOut)
		sessionSearchCmd.SetErr(oldOut)
		resetFlags()
	})
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sessionCommandsNow = func() time.Time { return now }
	messages := map[string][]transcript.Message{}
	entry := func(id, harnessName, cwd string, age time.Duration, texts ...string) transcript.SessionMeta {
		source := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		for i, text := range texts {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			messages[source] = append(messages[source], transcript.Message{Role: role, Text: text})
		}
		return transcript.SessionMeta{
			ID: id, Harness: harnessName, CWD: cwd, Path: source, Snippet: texts[0],
			FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: len(texts),
			StartedAt: now.Add(-age), LastAt: now.Add(-age),
		}
	}
	codex := []transcript.SessionMeta{
		entry("codex-race", harness.Codex, repo, time.Minute, "the daemon has a race condition", "fixed the race\tin the daemon"),
		entry("codex-old", harness.Codex, repo+"2", 48*time.Hour, "another race, condition unrelated to the daemon"),
		automated(entry("codex-asl", harness.Codex, repo, 10*time.Second, "# ASL session task packet: audit the race")),
	}
	claude := []transcript.SessionMeta{
		entry("claude-docs", harness.Claude, filepath.Join(repo, "docs"), 2*time.Minute, "rewrite the README", "the race condition is now documented"),
	}
	sessionCommandsSessionIndexPath = func() string { return filepath.Join(dir, "transcripts.json") }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Profiles: []config.Account{
			{Name: "work", Harness: harness.Codex, HomeDir: filepath.Join(dir, "codex-home")},
			{Name: "personal", Harness: harness.Claude, HomeDir: filepath.Join(dir, "claude-home")},
		}}, nil
	}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{
			harness.Codex:  searchCommandReader{sessionCommandReader{harness: harness.Codex, discovered: codex}, messages},
			harness.Claude: searchCommandReader{sessionCommandReader{harness: harness.Claude, discovered: claude}, messages},
		}
	}
	sessionListGetwd = func() (string, error) { return repo, nil }

	run := func(t *testing.T, flags map[string]string, args ...string) (string, error) {
		t.Helper()
		resetFlags()
		for name, value := range flags {
			if err := sessionSearchCmd.Flags().Set(name, value); err != nil {
				t.Fatal(err)
			}
		}
		var out bytes.Buffer
		sessionSearchCmd.SetOut(&out)
		sessionSearchCmd.SetErr(&out)
		err := sessionSearchCmd.RunE(sessionSearchCmd, args)
		return out.String(), err
	}
	ids := func(text string) string {
		var got []string
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			if line == "" {
				continue
			}
			got = append(got, strings.Split(line, "\t")[5])
		}
		return strings.Join(got, ",")
	}

	for _, tc := range []struct {
		name  string
		flags map[string]string
		args  []string
		want  string
	}{
		{"words match in any order, newest first", nil, []string{"Condition", "race"}, "codex-race,claude-docs,codex-old"},
		{"a quoted phrase is kept whole", nil, []string{`"race condition"`}, "codex-race,claude-docs"},
		{"every word must appear", nil, []string{"race", "readme"}, "claude-docs"},
		{"user prompts only", map[string]string{"source": "user"}, []string{"documented"}, ""},
		{"harness output only", map[string]string{"source": "harness"}, []string{"documented"}, "claude-docs"},
		{"here", map[string]string{"here": "true"}, []string{"race"}, "codex-race,claude-docs"},
		{"since", map[string]string{"since": "1h"}, []string{"race"}, "codex-race,claude-docs"},
		{"harness", map[string]string{"harness": "claude"}, []string{"race"}, "claude-docs"},
		{"limit after narrowing", map[string]string{"here": "true", "limit": "1"}, []string{"race"}, "codex-race"},
		{"all includes automated sessions", map[string]string{"all": "true", "limit": "1"}, []string{"race"}, "codex-asl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, tc.flags, tc.args...)
			if err != nil {
				t.Fatalf("search: %v\n%s", err, out)
			}
			if got := ids(out); got != tc.want {
				t.Fatalf("sessions = %q, want %q\n%s", got, tc.want, out)
			}
		})
	}

	t.Run("piped rows keep one line per match", func(t *testing.T) {
		out, err := run(t, nil, "fixed")
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
		if len(fields) != 7 || fields[4] != "1" || fields[6] != "fixed the race in the daemon" {
			t.Fatalf("row = %q", out)
		}
	})

	t.Run("json carries the query, counts, and previews", func(t *testing.T) {
		out, err := run(t, map[string]string{"json": "true"}, "race")
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			SchemaVersion int               `json:"schema_version"`
			Data          sessionSearchData `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatalf("decode %q: %v", out, err)
		}
		if envelope.SchemaVersion != 1 || envelope.Data.Query != "race" || envelope.Data.Source != "any" || len(envelope.Data.Results) != 3 {
			t.Fatalf("envelope = %+v", envelope)
		}
		first := envelope.Data.Results[0]
		if first.ID != "codex-race" || first.Harness != harness.Codex || first.Matches != 2 || first.Preview == "" {
			t.Fatalf("first result = %+v", first)
		}
	})

	t.Run("a terminal gets a table, and says when nothing matched", func(t *testing.T) {
		// resetSessionCommandsTestState restores the terminal seam.
		sessionListIsTerminal = func(io.Writer) bool { return true }
		out, err := run(t, nil, "race")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out, "HARNESS") || !strings.Contains(out, "MATCHES") || !strings.Contains(out, "1m ago") {
			t.Fatalf("table = %q", out)
		}
		out, err = run(t, nil, "nowhere")
		if err != nil || out != "No indexed sessions match \"nowhere\".\n" {
			t.Fatalf("empty = %q, %v", out, err)
		}
		out, err = run(t, map[string]string{"harness": "copilot"}, "race")
		if err != nil || out != "No indexed sessions match \"race\" with --harness copilot.\n" {
			t.Fatalf("empty with a filter = %q, %v", out, err)
		}
	})

	t.Run("a terminal table marks the conversation it runs in as here", func(t *testing.T) {
		// A search run from inside a conversation finds that conversation, so
		// the table says which row it is, as session list does.
		sessionListIsTerminal = func(io.Writer) bool { return true }
		sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) {
			return map[string]liveConversation{"codex:codex-race": {WindowID: 7, TabID: 2}}, true
		}
		sessionCommandsCurrentWindow = func() int { return 7 }
		t.Cleanup(func() {
			sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) { return nil, false }
			sessionCommandsCurrentWindow = func() int { return 0 }
		})
		out, err := run(t, nil, "race")
		if err != nil {
			t.Fatal(err)
		}
		// It comes last, too: the query was typed there, so it matches.
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(lines) != 5 || !strings.Contains(lines[0], " LIVE ") || !strings.Contains(lines[3], " here ") || strings.Contains(strings.Join(lines[:3], "\n"), "here") {
			t.Fatalf("table = %q, want a LIVE column marking only codex-race here, as the last row", out)
		}
		if lines[4] != "1 automated session also matches; --all includes it." {
			t.Fatalf("footer = %q, want the automated session matching race", lines[4])
		}

		sessionListIsTerminal = func(io.Writer) bool { return false }
		out, err = run(t, nil, "race")
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(out); got != "claude-docs,codex-old,codex-race" {
			t.Fatalf("piped sessions = %q, want the conversation it runs in last", got)
		}
	})

	t.Run("a terminal says when only automated sessions match", func(t *testing.T) {
		sessionListIsTerminal = func(io.Writer) bool { return true }
		out, err := run(t, nil, "audit")
		if err != nil || out != "No indexed sessions match \"audit\". Automated sessions do; --all shows them.\n" {
			t.Fatalf("empty = %q, %v", out, err)
		}
		out, err = run(t, nil, "documented")
		if err != nil || strings.Contains(out, "automated") {
			t.Fatalf("table = %q, %v, want no automated line when none match", out, err)
		}
	})

	for _, tc := range []struct {
		name  string
		flags map[string]string
		args  []string
		want  string
	}{
		{"missing query", nil, nil, "query"},
		{"blank query", nil, []string{"  "}, "query"},
		{"unknown source", map[string]string{"source": "tools"}, []string{"race"}, `--source "tools"`},
		{"unknown profile", map[string]string{"profile": "nobody"}, []string{"race"}, `--profile "nobody"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, tc.flags, tc.args...)
			if err == nil || !strings.Contains(out+err.Error(), tc.want) {
				t.Fatalf("err = %v, output %q, want a refusal naming %s", err, out, tc.want)
			}
		})
	}
}

// TestSessionSearchWithoutAQueryShowsOne pins that the bare command answers
// with an example query and the command to use instead, rather than only
// naming the missing parameter.
func TestSessionSearchWithoutAQueryShowsOne(t *testing.T) {
	t.Parallel()

	err := sessionSearchCmd.RunE(sessionSearchCmd, nil)
	if err == nil {
		t.Fatal("RunE error = nil; want a missing query error")
	}
	for _, want := range []string{"a query is required", "amx session search daemon race", "`amx session list`"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, missing %q", err.Error(), want)
		}
	}
}
