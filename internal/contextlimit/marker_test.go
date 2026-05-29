package contextlimit

import "testing"

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
			name: "quoted text earlier in scrollback does not trip fallback",
			text: "Context limit reached · /compact or /clear to continue\nuser was discussing the warning\n\nnormal latest output\nstill running fine\n",
			want: false,
		},
	}
	for _, tc := range cases {
		if got := ScreenShowsMarker(tc.text); got != tc.want {
			t.Errorf("%s: ScreenShowsMarker(%q) = %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
}
