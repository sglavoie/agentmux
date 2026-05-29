package palette

import "testing"

func TestStateOrderExtended(t *testing.T) {
	want := []string{"bolt", "stop", "key", "warning", "error", "subagent", "compact", "question"}
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

// TestParseTitlePrefersLongestStateIcon ensures overlapping configured glyphs
// do not break the BuildTitle/ParseTitle round-trip. state_order still wins
// ties, but a shorter glyph must not steal a title built with a longer glyph.
func TestParseTitlePrefersLongestStateIcon(t *testing.T) {
	prevOrder := ico.StateOrder
	prevStates := ico.States
	t.Cleanup(func() {
		ico.StateOrder = prevOrder
		ico.States = prevStates
	})

	ico.StateOrder = []string{"short", "long"}
	ico.States = map[string]string{
		"short": "!",
		"long":  "!!",
	}

	title := BuildTitle("base", "long", false)
	if title != "!!base" {
		t.Fatalf("BuildTitle(long) = %q, want !!base", title)
	}

	base, state, starred := ParseTitle(title)
	if base != "base" || state != "long" || starred {
		t.Fatalf("ParseTitle(%q) = (%q, %q, %v), want (base, long, false)", title, base, state, starred)
	}
	if got := StripIcons(title); got != "base" {
		t.Fatalf("StripIcons(%q) = %q, want base", title, got)
	}
	if got := DetectState(title); got != "long" {
		t.Fatalf("DetectState(%q) = %q, want long", title, got)
	}
}

// TestParseTitleIgnoresEmptyStar guards against an empty star glyph causing
// every title to be reported as starred — strings.HasPrefix(s, "") is true, so
// the naive prefix check would otherwise set starred unconditionally.
func TestParseTitleIgnoresEmptyStar(t *testing.T) {
	prevStar := ico.Star
	t.Cleanup(func() { ico.Star = prevStar })
	ico.Star = ""

	base, state, starred := ParseTitle("W:project")
	if base != "W:project" || state != "" || starred {
		t.Errorf("ParseTitle(plain w/ empty star) = (%q, %q, %v), want (W:project, , false)", base, state, starred)
	}
	if got := StripIcons("W:project"); got != "W:project" {
		t.Errorf("StripIcons(plain w/ empty star) = %q, want %q", got, "W:project")
	}
	if got := DetectState("W:project"); got != "" {
		t.Errorf("DetectState(plain w/ empty star) = %q, want empty", got)
	}

	boltTitle := StateIcon("bolt") + "W:project"
	base, state, starred = ParseTitle(boltTitle)
	if base != "W:project" || state != "bolt" || starred {
		t.Errorf("ParseTitle(bolt w/ empty star) = (%q, %q, %v), want (W:project, bolt, false)", base, state, starred)
	}
	if got := StripIcons(boltTitle); got != "W:project" {
		t.Errorf("StripIcons(bolt w/ empty star) = %q, want %q", got, "W:project")
	}
	if got := DetectState(boltTitle); got != "bolt" {
		t.Errorf("DetectState(bolt w/ empty star) = %q, want bolt", got)
	}
}

// TestStateOrderReturnsCopy ensures mutating the returned slice cannot corrupt
// the package-internal state-detection order shared by ParseTitle, StripIcons,
// and DetectState.
func TestStateOrderReturnsCopy(t *testing.T) {
	first := StateOrder()
	if len(first) == 0 {
		t.Fatal("StateOrder() returned empty slice")
	}
	original := first[0]
	first[0] = "MUTATED"
	second := StateOrder()
	if second[0] != original {
		t.Errorf("StateOrder()[0] = %q after caller mutation, want %q (returned slice must be a copy)", second[0], original)
	}
}
