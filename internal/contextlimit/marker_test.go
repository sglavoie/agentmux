package contextlimit

import (
	"os"
	"path/filepath"
	"testing"
)

// Observed Claude UI chrome below the context-limit banner (captured live
// via `kitten @ get-text --extent=screen` on 2026-06-08). The current
// release does NOT use the box-drawing brackets that the original plan
// assumed; the real chrome is five non-empty trailing lines:
//
//  1. A horizontal rule made entirely of U+2500 `─` characters spanning
//     the column width.
//  2. A line whose first non-whitespace character is the input prompt
//     `❯` (U+276F), with the rest of the row whitespace.
//  3. A second horizontal rule identical to (1).
//  4. A status / usage line (e.g.
//     `  27.0 max 5h ✗ 7d ✗ | team 5h 30/26 2:29pm 7d 56/76 jun10|2am snt 4 $0`).
//     Stable signal: leading whitespace + pipe-separated tokens with
//     `max` and `team` substrings.
//  5. A mode / permissions hint line ending with `(shift+tab to cycle)`,
//     prefixed by one of `⏸` (plan mode), `⏵`, or `⏵⏵`
//     (bypass permissions).
//
// Older releases also emitted the box-drawing input frame:
//
//	╭────────────────────────────────────────╮
//	│ >                                      │
//	╰────────────────────────────────────────╯
//	? for shortcuts | model: …
//
// `isUIChromeLine` (added in Session 2) must recognise both shapes:
//   - lines that are entirely box-drawing (`╭ ╮ ╰ ╯ ─ │ ═ ║ ┌ ┐ └ ┘ ┃ ━`)
//     plus whitespace,
//   - lines whose first non-whitespace character is `│`, `╭`, `╰`, or
//     `❯`,
//   - the conservative footer-hint patterns above (`(shift+tab to cycle)`,
//     `? for shortcuts`, or `model:`).
//
// The negative fixture
// `quoted text earlier in scrollback does not trip fallback` below must
// stay green — none of its plain chat lines may be misidentified as
// chrome.

func TestHasMarker(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "exact message",
			text: "Context limit reached · /compact or /clear to continue",
			want: true,
		},
		{
			name: "context limit compact hint",
			text: "Context limit reached. Run /compact to continue.",
			want: true,
		},
		{
			name: "window full variant",
			text: "Context window is full\n/compact or /clear to continue",
			want: true,
		},
		{
			name: "ansi-styled message",
			text: "\x1b[31mContext limit reached\x1b[0m · \x1b[1m/compact or /clear\x1b[0m to continue",
			want: true,
		},
		{
			name: "osc-wrapped command hint",
			text: "Context limit reached · \x1b]8;;https://example.invalid\x1b\\/compact or /clear\x1b]8;;\x1b\\ to continue",
			want: true,
		},
		{
			name: "missing recovery hint",
			text: "Context limit reached",
			want: false,
		},
		{
			name: "compact hint without context limit",
			text: "Run /compact when you want to summarize the conversation.",
			want: false,
		},
		{
			name: "unrelated text",
			text: "All good here",
			want: false,
		},
	}
	for _, tc := range cases {
		if got := HasMarker(tc.text); got != tc.want {
			t.Errorf("%s: HasMarker(%q) = %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
}

func TestScreenShowsMarker(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "warning at screen tail",
			text: "earlier line\n\nContext limit reached · /compact or /clear to continue\n\n✻ Cooked for 17m 6s\n",
			want: true,
		},
		{
			name: "warning wrapped across narrow screen tail",
			text: "earlier line\n\nContext limit reached\n/compact or /clear\nto continue\n\n✻ Cooked for 17m 6s\n",
			want: true,
		},
		{
			name: "warning wrapped with compact-only hint at screen tail",
			text: "earlier line\n\nContext\nlimit\nreached\n/compact\n\n✻ Cooked for 17m 6s\n",
			want: true,
		},
		{
			name: "warning wrapped with ansi styling",
			text: "earlier line\n\n\x1b[31mContext limit reached\x1b[0m\n\x1b[1m/compact or /clear\x1b[0m\nto continue\n\n✻ Cooked for 17m 6s\n",
			want: true,
		},
		{
			name: "warning wrapped across very narrow screen tail",
			text: "earlier line\n\nContext limit\nreached\n/compact or /clear\nto continue\n\n✻ Cooked for 17m 6s\n",
			want: true,
		},
		{
			name: "warning wrapped across very narrow screen bottom",
			text: "earlier line\n\nContext limit\nreached\n/compact or /clear\nto continue\n",
			want: true,
		},
		{
			name: "warning wrapped across extremely narrow screen bottom",
			text: "earlier line\n\nContext\nlimit\nreached\n/compact or /clear\nto continue\n",
			want: true,
		},
		{
			name: "warning wrapped with command hint split across screen bottom",
			text: "earlier line\n\nContext\nlimit\nreached\n/compact\nor /clear\nto continue\n",
			want: true,
		},
		{
			name: "warning wrapped across extremely narrow screen tail",
			text: "earlier line\n\nContext\nlimit\nreached\n/compact or /clear\nto continue\n\n✻ Cooked for 17m 6s\n",
			want: true,
		},
		{
			name: "warning wrapped with command hint split across screen tail",
			text: "earlier line\n\nContext\nlimit\nreached\n/compact\nor /clear\nto continue\n\n✻ Cooked for 17m 6s\n",
			want: true,
		},
		{
			name: "window full variant wrapped across extremely narrow screen bottom",
			text: "earlier line\n\nContext\nwindow\nis\nfull\n/compact\nor /clear\nto continue\n",
			want: true,
		},
		{
			name: "window full variant wrapped across extremely narrow screen tail",
			text: "earlier line\n\nContext\nwindow\nis\nfull\n/compact\nor /clear\nto continue\n\n✻ Cooked for 17m 6s\n",
			want: true,
		},
		{
			name: "window full variant wrapped one word per line at screen tail",
			text: "earlier line\n\nContext\nwindow\nis\nfull\n/compact\nor\n/clear\nto\ncontinue\n\n✻ Cooked for 17m 6s\n",
			want: true,
		},
		{
			name: "real banner with trailing spinner between banner and chrome",
			text: "earlier line\n\nContext limit reached · /compact or /clear to continue\n\n✻ Cooked for 17m 6s\n\n─────────────────────────────────────────────\n❯\n─────────────────────────────────────────────\n  27.0 max 5h ✗ 7d ✗ | team 5h 30/26 2:29pm\n  ⏸ plan mode on (shift+tab to cycle)\n",
			want: true,
		},
		{
			name: "real banner with legacy box wider than captured fixture",
			text: "earlier line\n\nContext limit reached · /compact or /clear to continue\n╭──────────────────────────────────────────────────────────────────────────────────────────────────╮\n│ >                                                                                                │\n╰──────────────────────────────────────────────────────────────────────────────────────────────────╯\n? for shortcuts | model: claude-opus-4-7\n",
			want: true,
		},
		{
			name: "quoted text earlier in scrollback does not trip fallback",
			text: "Context limit reached · /compact or /clear to continue\nuser was discussing the warning\n\nnormal latest output\nstill running fine\n",
			want: false,
		},
		{
			name: "incidental pipe rune in chat below marker stays no-match",
			text: "Context limit reached · /compact or /clear to continue\nthe │ symbol means pipe in shells\nnormal latest output\nstill running fine\n",
			want: false,
		},
		{
			name: "entirely chrome screen has no banner to match",
			text: "─────────────────────────────────────────────\n❯\n─────────────────────────────────────────────\n  ⏸ plan mode on (shift+tab to cycle)\n",
			want: false,
		},
	}
	for _, tc := range cases {
		if got := ScreenShowsMarker(tc.text); got != tc.want {
			t.Errorf("%s: ScreenShowsMarker(%q) = %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
}

func TestIsUIChromeLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		// Positives — current Claude release.
		{
			name: "new horizontal rule",
			line: "─────────────────────────────────────────────────────────────",
			want: true,
		},
		{
			name: "new prompt bare",
			line: "❯",
			want: true,
		},
		{
			name: "new prompt with trailing whitespace",
			line: "❯                                                                                                                                           ",
			want: true,
		},
		{
			name: "new footer hint plan mode",
			line: "⏸ plan mode on (shift+tab to cycle)",
			want: true,
		},
		{
			name: "new footer hint bypass mode",
			line: "⏵⏵ bypass permissions on (shift+tab to cycle)",
			want: true,
		},
		// Positives — legacy box-drawing UI.
		{
			name: "legacy box top",
			line: "╭────────────────────────────────────────╮",
			want: true,
		},
		{
			name: "legacy input row",
			line: "│ >                                      │",
			want: true,
		},
		{
			name: "legacy box bottom",
			line: "╰────────────────────────────────────────╯",
			want: true,
		},
		{
			name: "legacy footer hint with model",
			line: "? for shortcuts | model: …",
			want: true,
		},
		{
			name: "indented prompt rune still matches",
			line: "  ❯ some input",
			want: true,
		},
		// Negatives — banner must never be chrome.
		{
			name: "banner exact",
			line: "Context limit reached · /compact or /clear to continue",
			want: false,
		},
		// Negatives — chat content from the existing scrollback fixture.
		{
			name: "chat line one",
			line: "user was discussing the warning",
			want: false,
		},
		{
			name: "chat line two",
			line: "normal latest output",
			want: false,
		},
		{
			name: "chat line three",
			line: "still running fine",
			want: false,
		},
		// Negatives — must not over-match on incidental box-drawing.
		{
			name: "embedded pipe in plain text",
			line: "the │ symbol means pipe in shells",
			want: false,
		},
		// Negatives — whitespace handling.
		{
			name: "empty line",
			line: "",
			want: false,
		},
		{
			name: "whitespace only",
			line: "    \t  ",
			want: false,
		},
		// Negatives — status / usage line is explicitly NOT chrome in v1.
		{
			name: "status usage line",
			line: "  27.0 max 5h ✗ 7d ✗ | team 5h 30/26 2:29pm 7d 56/76 jun10|2am snt 4 $0",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUIChromeLine(tc.line); got != tc.want {
				t.Errorf("isUIChromeLine(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// TestScreenShowsMarker_RealClaudeFixture loads screen captures from
// `testdata/` that mirror what a live Claude session shows when the
// context-limit banner is on screen. Each capture pairs the banner with
// the trailing UI chrome (`─` rule + `❯` prompt + `─` rule + status +
// hint) that today's detection logic cannot see past.
//
// Until `ScreenShowsMarker` is taught to ignore trailing UI chrome
// (Sessions 2 and 3), every case here should fail with the banner
// clearly visible in the fixture. The failure message includes the
// fixture filename so the red signal is unambiguous.
func TestScreenShowsMarker_RealClaudeFixture(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
	}{
		{
			name:    "real_full_screen",
			fixture: "claude_context_limit_full.txt",
		},
		{
			name:    "real_full_screen_with_spinner",
			fixture: "claude_context_limit_with_spinner.txt",
		},
		{
			name:    "real_narrow_wrapped",
			fixture: "claude_context_limit_narrow.txt",
		},
	}

	const fixtureRoot = "testdata"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(fixtureRoot, tc.fixture)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture %s: %v", path, err)
			}
			if got := ScreenShowsMarker(string(data)); !got {
				t.Errorf("ScreenShowsMarker(%s) = false, want true (banner is present in fixture)", tc.fixture)
			}
		})
	}
}
