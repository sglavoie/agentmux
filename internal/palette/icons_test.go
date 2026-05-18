package palette

import "testing"

func TestStarIcon(t *testing.T) {
	if got := StarIcon(); got == "" {
		t.Error("StarIcon() is empty")
	}
}

func TestStateIconAllStates(t *testing.T) {
	states := []string{"bolt", "stop", "key", "error", "subagent", "compact", "question"}
	for _, s := range states {
		if got := StateIcon(s); got == "" {
			t.Errorf("StateIcon(%q) is empty", s)
		}
	}
}

func TestStateIconQuestion(t *testing.T) {
	if got := StateIcon("question"); got != "❓" {
		t.Errorf("StateIcon(\"question\") = %q, want %q", got, "❓")
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
	states := []string{"bolt", "stop", "key", "error", "subagent", "compact", "question"}
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
	states := []string{"bolt", "stop", "key", "error", "subagent", "compact", "question"}
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

func TestBuildParseSubagent(t *testing.T) {
	title := BuildTitle("base", "subagent", false)
	if title != "🤖base" {
		t.Errorf("BuildTitle(subagent) = %q, want %q", title, "🤖base")
	}
	base, state, starred := ParseTitle("🤖base")
	if base != "base" || state != "subagent" || starred {
		t.Errorf("ParseTitle(🤖base) = (%q, %q, %v), want (base, subagent, false)", base, state, starred)
	}
}

func TestBuildParseCompact(t *testing.T) {
	title := BuildTitle("base", "compact", false)
	if title != "🌀base" {
		t.Errorf("BuildTitle(compact) = %q, want %q", title, "🌀base")
	}
	base, state, starred := ParseTitle("🌀base")
	if base != "base" || state != "compact" || starred {
		t.Errorf("ParseTitle(🌀base) = (%q, %q, %v), want (base, compact, false)", base, state, starred)
	}
}

func TestBuildParseQuestion(t *testing.T) {
	title := BuildTitle("base", "question", false)
	if title != "❓base" {
		t.Errorf("BuildTitle(question) = %q, want %q", title, "❓base")
	}
	base, state, starred := ParseTitle("❓base")
	if base != "base" || state != "question" || starred {
		t.Errorf("ParseTitle(❓base) = (%q, %q, %v), want (base, question, false)", base, state, starred)
	}
}

func TestParseTitleErrorGlyphSwap(t *testing.T) {
	// Regression: ❗ is the new "error" glyph (was ⚠️).
	base, state, starred := ParseTitle("❗base")
	if base != "base" || state != "error" || starred {
		t.Errorf("ParseTitle(❗base) = (%q, %q, %v), want (base, error, false)", base, state, starred)
	}
}

func TestStateOrderExtended(t *testing.T) {
	want := []string{"bolt", "stop", "key", "error", "subagent", "compact", "question"}
	got := StateOrder()
	if len(got) != len(want) {
		t.Fatalf("StateOrder() len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("StateOrder()[%d] = %q, want %q", i, got[i], name)
		}
	}
}

// TestParseTitleIgnoresEmptyStateIcon ensures that a state_order entry whose
// state name has no matching glyph in `states` cannot prefix-match every
// title and report a phantom state. strings.HasPrefix(s, "") is true, so the
// pre-fix iteration would incorrectly label any title as carrying that state
// and leave the title bytes untouched, breaking ParseTitle's base extraction.
func TestParseTitleIgnoresEmptyStateIcon(t *testing.T) {
	prevOrder := ico.StateOrder
	prevStates := ico.States
	t.Cleanup(func() {
		ico.StateOrder = prevOrder
		ico.States = prevStates
	})

	// Prepend a state with no glyph; the real "bolt" entry follows.
	ico.StateOrder = append([]string{"phantom"}, prevOrder...)
	ico.States = make(map[string]string, len(prevStates))
	for k, v := range prevStates {
		ico.States[k] = v
	}
	// "phantom" deliberately absent from States — its lookup returns "".

	base, state, starred := ParseTitle("W:project")
	if base != "W:project" || state != "" || starred {
		t.Fatalf("ParseTitle(plain with phantom state) = (%q, %q, %v), want (W:project, , false)", base, state, starred)
	}

	boltTitle := BuildTitle("W:project", "bolt", false)
	base, state, starred = ParseTitle(boltTitle)
	if base != "W:project" || state != "bolt" || starred {
		t.Fatalf("ParseTitle(bolt with phantom state) = (%q, %q, %v), want (W:project, bolt, false)", base, state, starred)
	}

	if got := DetectState("W:project"); got != "" {
		t.Fatalf("DetectState(plain with phantom state) = %q, want empty", got)
	}
}

func TestBuildParseStarredSubagent(t *testing.T) {
	title := BuildTitle("base", "subagent", true)
	if title != "★🤖base" {
		t.Errorf("BuildTitle(starred+subagent) = %q, want %q", title, "★🤖base")
	}
	base, state, starred := ParseTitle(title)
	if base != "base" || state != "subagent" || !starred {
		t.Errorf("ParseTitle(★🤖base) = (%q, %q, %v), want (base, subagent, true)", base, state, starred)
	}
}
