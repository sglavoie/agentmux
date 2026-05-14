package palette

import "testing"

func TestStarIcon(t *testing.T) {
	if got := StarIcon(); got == "" {
		t.Error("StarIcon() is empty")
	}
}

func TestStateIconAllStates(t *testing.T) {
	states := []string{"bolt", "stop", "key", "error"}
	for _, s := range states {
		if got := StateIcon(s); got == "" {
			t.Errorf("StateIcon(%q) is empty", s)
		}
	}
}

func TestStateIconUnknown(t *testing.T) {
	if got := StateIcon("unknown"); got != "" {
		t.Errorf("StateIcon(\"unknown\") = %q, want empty", got)
	}
}

func TestParseTitlePlain(t *testing.T) {
	base, state, starred := ParseTitle("W:project")
	if base != "W:project" || state != "" || starred {
		t.Errorf("ParseTitle(plain) = (%q, %q, %v), want (W:project, , false)", base, state, starred)
	}
}

func TestParseTitleStarred(t *testing.T) {
	title := BuildTitle("W:project", "", true)
	base, state, starred := ParseTitle(title)
	if base != "W:project" || state != "" || !starred {
		t.Errorf("ParseTitle(starred) = (%q, %q, %v)", base, state, starred)
	}
}

func TestParseTitleWithState(t *testing.T) {
	states := []string{"bolt", "stop", "key", "error"}
	for _, want := range states {
		title := BuildTitle("P:api", want, false)
		base, state, starred := ParseTitle(title)
		if base != "P:api" || state != want || starred {
			t.Errorf("ParseTitle(state=%q) = (%q, %q, %v)", want, base, state, starred)
		}
	}
}

func TestParseTitleStarredWithState(t *testing.T) {
	title := BuildTitle("W:foo", "bolt", true)
	base, state, starred := ParseTitle(title)
	if base != "W:foo" || state != "bolt" || !starred {
		t.Errorf("ParseTitle(starred+bolt) = (%q, %q, %v)", base, state, starred)
	}
}

func TestBuildTitleRoundTrip(t *testing.T) {
	cases := []struct {
		base    string
		state   string
		starred bool
	}{
		{"W:project", "", false},
		{"W:project", "", true},
		{"P:dotfiles", "bolt", false},
		{"P:dotfiles", "bolt", true},
		{"W:api", "error", true},
	}
	for _, tc := range cases {
		title := BuildTitle(tc.base, tc.state, tc.starred)
		base, state, starred := ParseTitle(title)
		if base != tc.base || state != tc.state || starred != tc.starred {
			t.Errorf("round-trip(%q,%q,%v) = (%q,%q,%v)",
				tc.base, tc.state, tc.starred, base, state, starred)
		}
	}
}

func TestStripIcons(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"W:project", "W:project"},
		{BuildTitle("W:project", "", true), "W:project"},
		{BuildTitle("W:project", "bolt", false), "W:project"},
		{BuildTitle("W:project", "bolt", true), "W:project"},
		{BuildTitle("P:api", "error", true), "P:api"},
	}
	for _, tc := range cases {
		got := StripIcons(tc.input)
		if got != tc.want {
			t.Errorf("StripIcons(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDetectState(t *testing.T) {
	states := []string{"bolt", "stop", "key", "error"}
	for _, want := range states {
		title := BuildTitle("W:x", want, false)
		if got := DetectState(title); got != want {
			t.Errorf("DetectState(%q) = %q, want %q", title, got, want)
		}

		titleStarred := BuildTitle("W:x", want, true)
		if got := DetectState(titleStarred); got != want {
			t.Errorf("DetectState(%q, starred) = %q, want %q", titleStarred, got, want)
		}
	}
}

func TestDetectStateNone(t *testing.T) {
	if got := DetectState("W:project"); got != "" {
		t.Errorf("DetectState(no state) = %q, want empty", got)
	}
}
