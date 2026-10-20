package layout

import "testing"

func TestListStateWording(t *testing.T) {
	cases := map[string]string{
		LoadingText(""):                                "loading…",
		LoadingText("worktrees"):                       "loading worktrees…",
		NoMatchText("worktrees", ClearFilterHint):      "no worktrees match — esc clears the filter",
		NoMatchText("sessions", ""):                    "no sessions match",
		EmptyText("managed worktrees", "w forks one"):  "no managed worktrees — w forks one",
		Placeholder(LoadingText("runs")):               "(loading runs…)",
		Sentence(LoadingText("sessions")):              "Loading sessions…",
		Sentence(""):                                   "",
		NoMatchText("projects", SearchEditHint(true)):  "no projects match — backspace edits the search",
		NoMatchText("projects", SearchEditHint(false)): "no projects match — / edits the search",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
