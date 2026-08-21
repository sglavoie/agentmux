package daemon

import "testing"

func TestClassifyClaudeIdleScreen(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   bool
	}{
		{name: "completed turn with empty composer", screen: claudeIdleScreen, want: true},
		{name: "interrupted turn with empty composer", screen: claudeInterruptedScreen, want: true},
		{name: "installed 2.1.195 composer uses non-breaking space", screen: claudeInstalledIdleScreen, want: true},
		{name: "active spinner keeps busy", screen: claudeWorkingScreen, want: false},
		{name: "background task running", screen: claudeBackgroundTaskScreen, want: false},
		{name: "permission prompt is blocking", screen: claudePermissionScreen, want: false},
		{name: "queued input in composer", screen: claudeQueuedInputScreen, want: false},
		{name: "context limit marker", screen: claudeContextLimitStopScreen, want: false},
		{name: "no composer at all", screen: claudeNoComposerScreen, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyClaudeIdleScreen(tc.screen); got != tc.want {
				t.Fatalf("ClassifyClaudeIdleScreen() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyClaudeInterruptedScreen(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   bool
	}{
		{name: "interrupted marker with composer", screen: claudeInterruptedScreen, want: true},
		{name: "request interrupted bracket form", screen: claudeRequestInterruptedScreen, want: true},
		{name: "completed turn is not an interruption", screen: claudeIdleScreen, want: false},
		{name: "interrupted then resumed working", screen: claudeInterruptedThenWorkingScreen, want: false},
		{name: "interrupted without composer", screen: claudeNoComposerScreen, want: false},
		{name: "transcript prose mentioning interrupted", screen: claudeInterruptedProseScreen, want: false},
		{name: "permission prompt is not an interruption", screen: claudePermissionScreen, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyClaudeInterruptedScreen(tc.screen); got != tc.want {
				t.Fatalf("ClassifyClaudeInterruptedScreen() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClaudeWindowTitleHasActiveWork(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  bool
	}{
		{name: "star work marker", title: "\u2733 Fix drawer drag resistance", want: true},
		{name: "braille work marker", title: "\u2802 Plan next steps", want: true},
		{name: "alternate braille frame", title: "\u28ff Running tests", want: true},
		{name: "leading whitespace", title: "  \u2733 Working", want: true},
		{name: "star resting title", title: "\u2733 " + claudeRestingTitleText},
		{name: "star resting title padded", title: "  \u2733  " + claudeRestingTitleText + "  "},
		{name: "braille over generic body", title: "\u2810 " + claudeRestingTitleText},
		{name: "plain session title", title: "Fix drawer drag resistance"},
		{name: "blank title"},
		{name: "unrecognized symbol", title: "\u2705 Finished"},
		{name: "marker without separator", title: "\u2733Working"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeWindowTitleHasActiveWork(tc.title); got != tc.want {
				t.Fatalf("claudeWindowTitleHasActiveWork(%q) = %v, want %v", tc.title, got, tc.want)
			}
		})
	}
}

func TestClaudeWindowTitleHasRestingMarker(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  bool
	}{
		{name: "braille over generic body", title: "\u2810 " + claudeRestingTitleText, want: true},
		{name: "star over generic body", title: "\u2733 " + claudeRestingTitleText, want: true},
		{name: "braille over description", title: "\u2802 Plan next steps"},
		{name: "star over description", title: "\u2733 Fix drawer drag resistance"},
		{name: "no marker", title: claudeRestingTitleText},
		{name: "leading and body whitespace", title: "  \u2810  " + claudeRestingTitleText + "  ", want: true},
		{name: "unicode separator", title: "\u2733\u00a0" + claudeRestingTitleText, want: true},
		{name: "marker without separator", title: "\u2810" + claudeRestingTitleText},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeWindowTitleHasRestingMarker(tc.title); got != tc.want {
				t.Fatalf("claudeWindowTitleHasRestingMarker(%q) = %v, want %v", tc.title, got, tc.want)
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

const claudeInstalledIdleScreen = "⏺ Bash(sleep 30)\n\n❯\u00a0\n\n⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\n"

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

const claudeNoComposerScreen = `⎿ Interrupted by user

Some trailing output with no framed composer at all.
`

const claudeInterruptedScreen = `⎿ Interrupted by user

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
  ? for shortcuts
`

const claudeRequestInterruptedScreen = `[Request interrupted by user]

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeInterruptedThenWorkingScreen = `⎿ Interrupted by user

✳ Thinking… (3s · esc to interrupt)

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeInterruptedProseScreen = `✻ Cooked for 1m 4s

The staging deploy was interrupted for the cluster last week.

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`
