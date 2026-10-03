package debugger

import "testing"

func TestHookProfileDetailSummarizesARepeatedIssue(t *testing.T) {
	profile := HookProfile{Issues: []HookIssue{{Code: "cli_newer", Message: "CLI is newer"}}}
	for _, project := range []string{"a", "b", "c", "d", "e"} {
		profile.Issues = append(profile.Issues, HookIssue{Code: "codex_hook_untrusted", Message: project + ": Stop has no exact-definition trust record"})
	}
	want := "CLI is newer; a: Stop has no exact-definition trust record; b: Stop has no exact-definition trust record; " +
		"c: Stop has no exact-definition trust record; and 2 more like these"
	if got := profile.Detail(); got != want {
		t.Fatalf("Detail =\n%s\nwant\n%s", got, want)
	}
}
