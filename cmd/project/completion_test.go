package project

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// completeTitles runs the --title completion against cfg with --profile set to
// profile, restoring the seam and the flag afterwards.
func completeTitles(t *testing.T, cfg *config.Config, loadErr error, profile, toComplete string) []string {
	t.Helper()
	oldLoad := projectCompletionLoadConfig
	t.Cleanup(func() {
		projectCompletionLoadConfig = oldLoad
		_ = projectLaunchCmd.Flags().Set("profile", "")
	})
	projectCompletionLoadConfig = func() (*config.Config, error) { return cfg, loadErr }
	if err := projectLaunchCmd.Flags().Set("profile", profile); err != nil {
		t.Fatal(err)
	}
	got, _ := completeProjectTitles(projectLaunchCmd, nil, toComplete)
	return got
}

func TestCompleteProjectTitlesOffersEachTitleOnce(t *testing.T) {
	got := completeTitles(t, testConfig(), nil, "", "")
	if want := []string{"API\twork", "Web\twork"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("completions = %q, want %q", got, want)
	}
}

func TestCompleteProjectTitlesNarrowsByProfileAndPrefix(t *testing.T) {
	if got, want := completeTitles(t, testConfig(), nil, "CODEX-WORK", ""), []string{"API\tcodex-work"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("profile-narrowed completions = %q, want %q", got, want)
	}
	if got, want := completeTitles(t, testConfig(), nil, "", "W"), []string{"Web\twork"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prefix completions = %q, want %q", got, want)
	}
}

// A launch reads "raspberry" as "🥧Raspberry Pi", so completion reaches it
// the same way, offering what was typed plus the rest of the title so the
// shell's prefix filter keeps it.
func TestCompleteProjectTitlesReachesTitlesPastTheirEmoji(t *testing.T) {
	cfg := testConfig()
	cfg.Projects = append(cfg.Projects, config.Session{Title: "🥧Raspberry Pi", Directory: "/tmp/pi", Profile: "work"})
	if got, want := completeTitles(t, cfg, nil, "", "ras"), []string{"raspberry Pi\twork"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("completions = %q, want %q", got, want)
	}
	if got, want := completeTitles(t, cfg, nil, "", "🥧R"), []string{"🥧Raspberry Pi\twork"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("completions = %q, want %q", got, want)
	}
}

func TestCompleteProjectTitlesDegradesToNothingOnBrokenConfig(t *testing.T) {
	if got := completeTitles(t, nil, errors.New("broken"), "", ""); got != nil {
		t.Fatalf("completions = %q, want none", got)
	}
}

func TestCompleteProjectTitleArgStopsAfterOneTitle(t *testing.T) {
	oldLoad := projectCompletionLoadConfig
	t.Cleanup(func() { projectCompletionLoadConfig = oldLoad })
	projectCompletionLoadConfig = func() (*config.Config, error) { return testConfig(), nil }
	if got, want := func() []string { c, _ := completeProjectTitleArg(projectLaunchCmd, nil, "W"); return c }(), []string{"Web\twork"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first-argument completions = %q, want %q", got, want)
	}
	if got, _ := completeProjectTitleArg(projectLaunchCmd, []string{"Web"}, ""); got != nil {
		t.Fatalf("second-argument completions = %q, want none", got)
	}
}
