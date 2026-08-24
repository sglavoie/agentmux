package daemon

import "testing"

func TestClaudeWindowTitleHasDescriptionMarker(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  bool
	}{
		{name: "star work marker", title: "\u2733 Fix drawer drag resistance", want: true},
		{name: "braille work marker", title: "\u2802 Plan next steps", want: true},
		{name: "alternate braille frame", title: "\u28ff Running tests", want: true},
		{name: "quadrant work marker", title: "\u25d0 Running tests", want: true},
		{name: "leading whitespace", title: "  \u2733 Working", want: true},
		{name: "star resting title", title: "\u2733 " + claudeRestingTitleText},
		{name: "star resting title padded", title: "  \u2733  " + claudeRestingTitleText + "  "},
		{name: "braille over generic body", title: "\u2810 " + claudeRestingTitleText},
		{name: "quadrant over generic body", title: "\u25d0 " + claudeRestingTitleText},
		{name: "plain session title", title: "Fix drawer drag resistance"},
		{name: "blank title"},
		{name: "unrecognized symbol", title: "\u2705 Finished"},
		{name: "marker without separator", title: "\u2733Working"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeWindowTitleHasDescriptionMarker(tc.title); got != tc.want {
				t.Fatalf("claudeWindowTitleHasDescriptionMarker(%q) = %v, want %v", tc.title, got, tc.want)
			}
		})
	}
}

func TestClaudeTitleHasMarker(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  bool
	}{
		{name: "braille over generic body", title: "\u2810 " + claudeRestingTitleText, want: true},
		{name: "star over generic body", title: "\u2733 " + claudeRestingTitleText, want: true},
		{name: "quadrant over generic body", title: "\u25d3 " + claudeRestingTitleText, want: true},
		{name: "braille over description", title: "\u2802 Plan next steps", want: true},
		{name: "star over description", title: "\u2733 Fix drawer drag resistance", want: true},
		{name: "quadrant over description", title: "\u25d0 Fix drawer drag resistance", want: true},
		{name: "no marker", title: claudeRestingTitleText},
		{name: "leading and body whitespace", title: "  \u2810  " + claudeRestingTitleText + "  ", want: true},
		{name: "unicode separator", title: "\u2733\u00a0" + claudeRestingTitleText, want: true},
		{name: "marker without separator", title: "\u2810" + claudeRestingTitleText},
		{name: "unrecognized symbol", title: "\u2705 Finished"},
		{name: "blank title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeTitleHasMarker(tc.title); got != tc.want {
				t.Fatalf("claudeTitleHasMarker(%q) = %v, want %v", tc.title, got, tc.want)
			}
		})
	}
}

const claudeIdleScreen = `✻ Cooked for 17m 6s

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
  ? for shortcuts                    (shift+tab to cycle)
`

// claudeQuiescentIdleScreen is the weakest terminal shape Claude produces: a
// bare empty composer with no completion line and no interruption marker at
// all. Claude shows exactly this after /clear, after /compact, at session
// start, and after a turn whose completion line has scrolled away, which is why
// screenstate.TierQuiescent settles at all — and why it is the tier that pays
// the most stabilization samples.
const claudeQuiescentIdleScreen = `╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
  ? for shortcuts                    (shift+tab to cycle)
`

const claudeWorkingScreen = `✳ Thinking… (12s · ↑ 2.1k tokens · esc to interrupt)

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeBackgroundTaskScreen = `Running go test in the background (ctrl+b to manage background tasks)

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudePermissionScreen = `Do you want to make this edit to app.go?

❯ 1. Yes
  2. No, and tell Claude what to do differently

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeQueuedInputScreen = `✻ Cooked for 2m 10s

╭──────────────────────────────────────────────────╮
│ ❯ and now write the tests                          │
╰──────────────────────────────────────────────────╯
  ? for shortcuts
`

const claudeContextLimitStopScreen = `Context limit reached · /compact or /clear to continue

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeInterruptedScreen = `⎿ Interrupted by user

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
  ? for shortcuts
`
