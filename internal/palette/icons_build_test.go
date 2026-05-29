package palette

import "testing"

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
		{"W:api", "warning", true},
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

func TestBuildParseWarning(t *testing.T) {
	title := BuildTitle("base", "warning", false)
	if title != "⚠️base" {
		t.Errorf("BuildTitle(warning) = %q, want %q", title, "⚠️base")
	}
	base, state, starred := ParseTitle("⚠️base")
	if base != "base" || state != "warning" || starred {
		t.Errorf("ParseTitle(⚠️base) = (%q, %q, %v), want (base, warning, false)", base, state, starred)
	}
}

func TestParseWarningGlyphWithoutVariationSelector(t *testing.T) {
	base, state, starred := ParseTitle("⚠base")
	if base != "base" || state != "warning" || starred {
		t.Errorf("ParseTitle(⚠base) = (%q, %q, %v), want (base, warning, false)", base, state, starred)
	}
	if got := StripIcons("⚠base"); got != "base" {
		t.Errorf("StripIcons(⚠base) = %q, want base", got)
	}
	if got := DetectState("⚠base"); got != "warning" {
		t.Errorf("DetectState(⚠base) = %q, want warning", got)
	}
}

func TestParseWarningGlyphWithTextVariationSelector(t *testing.T) {
	base, state, starred := ParseTitle("⚠︎base")
	if base != "base" || state != "warning" || starred {
		t.Errorf("ParseTitle(⚠︎base) = (%q, %q, %v), want (base, warning, false)", base, state, starred)
	}
	if got := StripIcons("⚠︎base"); got != "base" {
		t.Errorf("StripIcons(⚠︎base) = %q, want base", got)
	}
	if got := DetectState("⚠︎base"); got != "warning" {
		t.Errorf("DetectState(⚠︎base) = %q, want warning", got)
	}
}

func TestParseTitleErrorGlyph(t *testing.T) {
	// Regression: terminal failures keep the ❗ error glyph after warning was
	// split back out into its own ⚠️ state.
	base, state, starred := ParseTitle("❗base")
	if base != "base" || state != "error" || starred {
		t.Errorf("ParseTitle(❗base) = (%q, %q, %v), want (base, error, false)", base, state, starred)
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
