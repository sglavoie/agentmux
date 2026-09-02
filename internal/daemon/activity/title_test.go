package activity

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
		{name: "star resting title", title: "\u2733 " + ClaudeRestingTitleText},
		{name: "star resting title padded", title: "  \u2733  " + ClaudeRestingTitleText + "  "},
		{name: "braille over generic body", title: "\u2810 " + ClaudeRestingTitleText},
		{name: "quadrant over generic body", title: "\u25d0 " + ClaudeRestingTitleText},
		{name: "plain session title", title: "Fix drawer drag resistance"},
		{name: "blank title"},
		{name: "unrecognized symbol", title: "\u2705 Finished"},
		{name: "marker without separator", title: "\u2733Working"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClaudeWindowTitleHasDescriptionMarker(tc.title); got != tc.want {
				t.Fatalf("ClaudeWindowTitleHasDescriptionMarker(%q) = %v, want %v", tc.title, got, tc.want)
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
		{name: "braille over generic body", title: "\u2810 " + ClaudeRestingTitleText, want: true},
		{name: "star over generic body", title: "\u2733 " + ClaudeRestingTitleText, want: true},
		{name: "quadrant over generic body", title: "\u25d3 " + ClaudeRestingTitleText, want: true},
		{name: "braille over description", title: "\u2802 Plan next steps", want: true},
		{name: "star over description", title: "\u2733 Fix drawer drag resistance", want: true},
		{name: "quadrant over description", title: "\u25d0 Fix drawer drag resistance", want: true},
		{name: "no marker", title: ClaudeRestingTitleText},
		{name: "leading and body whitespace", title: "  \u2810  " + ClaudeRestingTitleText + "  ", want: true},
		{name: "unicode separator", title: "\u2733\u00a0" + ClaudeRestingTitleText, want: true},
		{name: "marker without separator", title: "\u2810" + ClaudeRestingTitleText},
		{name: "unrecognized symbol", title: "\u2705 Finished"},
		{name: "blank title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClaudeTitleHasMarker(tc.title); got != tc.want {
				t.Fatalf("ClaudeTitleHasMarker(%q) = %v, want %v", tc.title, got, tc.want)
			}
		})
	}
}
