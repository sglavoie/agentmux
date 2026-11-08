package session

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestSessionListSelectorsNarrowTheList(t *testing.T) {
	resetSessionCommandsTestState(t)
	oldGetwd := sessionListGetwd
	t.Cleanup(func() {
		sessionListGetwd = oldGetwd
		for _, name := range []string{"here", "live", "harness", "profile", "project", "limit", "offset", "json"} {
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
		for _, name := range []string{"since", "here", "live", "harness", "profile", "project", "limit", "offset", "all"} {
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

	// --live keeps the conversations open in tmux, and does so before
	// --limit, so the limit counts open conversations rather than cutting the
	// list first and filtering what was left.
	t.Run("live keeps open conversations before the limit", func(t *testing.T) {
		sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) {
			return map[string]liveConversation{
				"claude:claude-sub":   {RuntimeSessionID: "rs-a"},
				"codex:codex-sibling": {RuntimeSessionID: "rs-b"},
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
		if err != nil || out != "No indexed sessions match --here --harness copilot --project '🐱repo'.\n" {
			t.Fatalf("empty = %q, %v", out, err)
		}
		out, err = run(t, map[string]string{"offset": "3"})
		if err != nil || out != "--offset 3 skips all 3 matching results.\n" {
			t.Fatalf("empty page = %q, %v", out, err)
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
