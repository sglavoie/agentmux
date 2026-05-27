package palette

import "testing"

func TestMuteIcon(t *testing.T) {
	if got := MuteIcon(); got != "🔇" {
		t.Errorf("MuteIcon() = %q, want %q", got, "🔇")
	}
}

func TestBuildTitleFull_StarredMutedBolt(t *testing.T) {
	title := BuildTitleFull("api", "bolt", true, true)
	if title != "★🔇⚡api" {
		t.Errorf("BuildTitleFull(starred+muted+bolt) = %q, want %q", title, "★🔇⚡api")
	}
}

func TestParseTitleFull_StarredMutedBolt(t *testing.T) {
	base, state, starred, muted := ParseTitleFull("★🔇⚡api")
	if base != "api" || state != "bolt" || !starred || !muted {
		t.Errorf("ParseTitleFull(★🔇⚡api) = (%q, %q, %v, %v), want (api, bolt, true, true)",
			base, state, starred, muted)
	}
}

func TestBuildParseFull_RoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		state   string
		starred bool
		muted   bool
	}{
		{"plain", "api", "", false, false},
		{"starred", "api", "", true, false},
		{"muted", "api", "", false, true},
		{"starred+muted", "api", "", true, true},
		{"bolt", "api", "bolt", false, false},
		{"muted+bolt", "api", "bolt", false, true},
		{"starred+bolt", "api", "bolt", true, false},
		{"starred+muted+bolt", "api", "bolt", true, true},
		{"muted+stop", "api", "stop", false, true},
		{"starred+muted+key", "api", "key", true, true},
		{"starred+muted+error", "api", "error", true, true},
		{"starred+muted+subagent", "api", "subagent", true, true},
		{"starred+muted+question", "api", "question", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title := BuildTitleFull(tc.base, tc.state, tc.starred, tc.muted)
			base, state, starred, muted := ParseTitleFull(title)
			if base != tc.base || state != tc.state || starred != tc.starred || muted != tc.muted {
				t.Errorf("round-trip(%q,%q,%v,%v) = (%q,%q,%v,%v)",
					tc.base, tc.state, tc.starred, tc.muted, base, state, starred, muted)
			}
		})
	}
}

func TestStripIcons_PreservesMutePrefix(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"🔇api", "api"},
		{"🔇⚡api", "api"},
		{"★🔇api", "api"},
		{"★🔇⚡api", "api"},
		{BuildTitleFull("api", "bolt", true, true), "api"},
		{BuildTitleFull("api", "stop", false, true), "api"},
		{BuildTitleFull("api", "", true, true), "api"},
	}
	for _, tc := range cases {
		if got := StripIcons(tc.input); got != tc.want {
			t.Errorf("StripIcons(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDetectState_SeesThroughMute(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"🔇⚡api", "bolt"},
		{"★🔇⚡api", "bolt"},
		{"🔇🛑api", "stop"},
		{"🔇api", ""},
	}
	for _, tc := range cases {
		if got := DetectState(tc.input); got != tc.want {
			t.Errorf("DetectState(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestParseTitle_WrapperDiscardsMute ensures the 3-return ParseTitle wrapper
// still operates correctly on titles that include the new mute prefix: the
// base must be stripped of all icons even when the caller never sees the
// muted flag.
func TestParseTitle_WrapperDiscardsMute(t *testing.T) {
	title := BuildTitleFull("api", "bolt", true, true)
	base, state, starred := ParseTitle(title)
	if base != "api" || state != "bolt" || !starred {
		t.Errorf("ParseTitle wrapper over muted title = (%q, %q, %v), want (api, bolt, true)",
			base, state, starred)
	}
}

// TestBuildTitle_WrapperOmitsMute ensures the 3-arg BuildTitle wrapper never
// emits the mute prefix even when the underlying glyph is configured.
func TestBuildTitle_WrapperOmitsMute(t *testing.T) {
	title := BuildTitle("api", "bolt", true)
	if title != "★⚡api" {
		t.Errorf("BuildTitle wrapper = %q, want %q (no mute prefix)", title, "★⚡api")
	}
}
