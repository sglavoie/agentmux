package session

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// stubCompletionIndex points completion at an index holding metas, and at a
// configuration declaring projects.
func stubCompletionIndex(t *testing.T, projects []string, metas ...transcript.SessionMeta) {
	t.Helper()
	resetSessionCommandsTestState(t)
	oldConfig := sessionCompletionLoadConfig
	t.Cleanup(func() { sessionCompletionLoadConfig = oldConfig })
	cfg := &config.Config{}
	for _, title := range projects {
		cfg.Projects = append(cfg.Projects, config.Session{Title: title})
	}
	sessionCompletionLoadConfig = func() (*config.Config, error) { return cfg, nil }
	indexPath := filepath.Join(t.TempDir(), "transcripts.json")
	writeSessionCommandIndex(t, indexPath, metas...)
	sessionCommandsSessionIndexPath = func() string { return indexPath }
}

func completionMetas() []transcript.SessionMeta {
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	return []transcript.SessionMeta{
		{Harness: "claude", ID: "fb1ec0eb-0000", Project: "🐱amx", Title: "Project improvements", LastAt: at},
		{Harness: "codex", ID: "01a11269-0000", Project: "📝tt", Snippet: "Supervise\nthe run", LastAt: at.Add(-time.Hour)},
		{Harness: "codex", ID: "01a112bc-0000", Project: "dev-helpers", Title: "Resolve bootstrap", LastAt: at.Add(-2 * time.Hour)},
	}
}

func TestCompleteConversationOffersHarnessesThenRecentSessions(t *testing.T) {
	stubCompletionIndex(t, nil, completionMetas()...)
	got, _ := completeConversation(nil, nil, "")
	want := []string{
		"claude", "copilot", "codex",
		"fb1ec0eb\tclaude · 🐱amx · Project improvements",
		"01a11269\tcodex · 📝tt · Supervise the run",
		"01a112bc\tcodex · dev-helpers · Resolve bootstrap",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("completions =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCompleteConversationNarrowsToTheNamedHarness(t *testing.T) {
	stubCompletionIndex(t, nil, completionMetas()...)
	got, _ := completeConversation(nil, []string{"claude"}, "")
	if len(got) != 1 || !strings.HasPrefix(got[0], "fb1ec0eb\t") {
		t.Fatalf("completions = %q, want only the claude conversation", got)
	}
	if got, _ := completeConversation(nil, []string{"claude", "fb1ec0eb"}, ""); len(got) != 0 {
		t.Fatalf("completions after the session ID = %q, want none", got)
	}
}

// A shell keeps only suggestions starting with what was typed, so once the
// typed text runs past the short prefix the whole ID is offered instead.
func TestCompleteConversationOffersTheWholeIDPastItsShortPrefix(t *testing.T) {
	stubCompletionIndex(t, nil, completionMetas()...)
	got, _ := completeConversation(nil, nil, "01a112b")
	if len(got) != 1 || !strings.HasPrefix(got[0], "01a112bc\t") {
		t.Fatalf("completions = %q, want the short prefix", got)
	}
	got, _ = completeConversation(nil, nil, "01a112bc-0")
	if len(got) != 1 || !strings.HasPrefix(got[0], "01a112bc-0000\t") {
		t.Fatalf("completions = %q, want the whole ID", got)
	}
}

func TestCompleteOneConversationOffersNoHarnessNames(t *testing.T) {
	stubCompletionIndex(t, nil, completionMetas()...)
	got, _ := completeOneConversation(nil, nil, "c")
	if len(got) != 0 {
		t.Fatalf("completions = %q, want no harness names for a single selector", got)
	}
}

func TestCompleteSessionProjectsJoinsConfiguredAndIndexedNames(t *testing.T) {
	stubCompletionIndex(t, []string{"🐱amx", "spec"}, completionMetas()...)
	got, _ := completeSessionProjects(nil, nil, "")
	if want := []string{"🐱amx", "spec", "📝tt", "dev-helpers"}; !slices.Equal(got, want) {
		t.Fatalf("completions = %q, want %q", got, want)
	}
	if got, _ := completeSessionProjects(nil, nil, "d"); !slices.Equal(got, []string{"dev-helpers"}) {
		t.Fatalf("completions for d = %q", got)
	}
}

// A shell keeps only suggestions starting with what was typed, so a title
// reached past its leading emoji is offered as typed plus the rest of it.
func TestCompleteSessionProjectsReachesTitlesPastTheirEmoji(t *testing.T) {
	stubCompletionIndex(t, []string{"🐱amx", "🥧Raspberry Pi"}, completionMetas()...)
	for _, tc := range []struct {
		typed string
		want  []string
	}{
		{"am", []string{"amx"}},
		{"🐱", []string{"🐱amx"}},
		{"ras", []string{"raspberry Pi"}},
		{"T", []string{"Tt"}},
		{"x", nil},
	} {
		if got, _ := completeSessionProjects(nil, nil, tc.typed); !slices.Equal(got, tc.want) {
			t.Errorf("completions for %q = %q, want %q", tc.typed, got, tc.want)
		}
	}
}
