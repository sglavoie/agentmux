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

func TestCompleteProjectTitlesDegradesToNothingOnBrokenConfig(t *testing.T) {
	if got := completeTitles(t, nil, errors.New("broken"), "", ""); got != nil {
		t.Fatalf("completions = %q, want none", got)
	}
}
