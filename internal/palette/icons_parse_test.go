package palette

import "testing"

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
	states := []string{"bolt", "stop", "key", "warning", "error", "subagent", "compact", "question"}
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

func TestStripIcons(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"W:project", "W:project"},
		{BuildTitle("W:project", "", true), "W:project"},
		{BuildTitle("W:project", "bolt", false), "W:project"},
		{BuildTitle("W:project", "bolt", true), "W:project"},
		{BuildTitle("P:api", "warning", true), "P:api"},
		{BuildTitle("P:api", "error", true), "P:api"},
	}
	for _, tc := range cases {
		got := StripIcons(tc.input)
		if got != tc.want {
			t.Errorf("StripIcons(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStripIcons_StopsAfterFirstStateIcon(t *testing.T) {
	title := StateIcon("bolt") + StateIcon("stop") + "W:project"
	if got := StripIcons(title); got != StateIcon("stop")+"W:project" {
		t.Errorf("StripIcons(%q) = %q, want %q", title, got, StateIcon("stop")+"W:project")
	}

	base, _, _ := ParseTitle(title)
	if got := StripIcons(title); got != base {
		t.Errorf("StripIcons(%q) = %q, want ParseTitle base %q", title, got, base)
	}
}

func TestDetectState(t *testing.T) {
	states := []string{"bolt", "stop", "key", "warning", "error", "subagent", "compact", "question"}
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
