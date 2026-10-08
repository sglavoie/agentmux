package searchquery

import (
	"strings"
	"testing"
)

// A match deep inside a long line must survive a narrow table column, so the
// line is entered just before it; a match near the start keeps the line whole.
func TestMatchLineEntersALongLineNearTheMatch(t *testing.T) {
	line := "d446830 feat(macos): Add the Agent Mux.app skeleton, toolchain, and packaging for release"
	text := "earlier line\n" + line + "\nlater line"
	got, clipped := MatchLine(text, len("earlier line\n")+strings.Index(line, "packaging"))
	if got != "toolchain, and packaging for release" || !clipped {
		t.Fatalf("MatchLine = %q, %v; want the line entered a few words before the match", got, clipped)
	}

	short := "Fix the packaging step"
	if got, clipped := MatchLine(short, strings.Index(short, "packaging")); got != short || clipped {
		t.Fatalf("MatchLine = %q, %v; want a near-start match to keep its whole line", got, clipped)
	}

	unbroken := strings.Repeat("x", 40) + "needle"
	if got, clipped := MatchLine(unbroken, 40); got != strings.Repeat("x", MatchLeadContext)+"needle" || !clipped {
		t.Fatalf("MatchLine = %q, %v; want a lead without spaces cut by runes", got, clipped)
	}

	if got, clipped := MatchLine("whole text", -1); got != "whole text" || clipped {
		t.Fatalf("MatchLine = %q, %v; want text whole for a position outside it", got, clipped)
	}
}

func TestPreviewAtPrefersTheFirstLineHoldingEveryTerm(t *testing.T) {
	text := "kitty tabs are colored\nthe socket is closed\nthe kitty socket listens\n"
	terms := Parse("socket kitty")
	first := terms.NewTally().Add(text)
	pos, complete := terms.PreviewAt(text, first)
	if !complete || !strings.HasPrefix(text[pos:], "kitty socket listens") {
		t.Fatalf("PreviewAt = %d (%q), %v; want the earliest match on the line holding both", pos, text[pos:], complete)
	}
}

func TestPreviewAtFallsBackToFirstWhenNoLineHoldsEveryTerm(t *testing.T) {
	text := "kitty tabs\nsocket closed\n"
	terms := Parse("kitty socket")
	if pos, complete := terms.PreviewAt(text, 0); pos != 0 || complete {
		t.Fatalf("PreviewAt = %d, %v; want first unchanged and incomplete", pos, complete)
	}
	if pos, complete := Parse("kitty").PreviewAt(text, 0); pos != 0 || !complete {
		t.Fatalf("single term PreviewAt = %d, %v; want first, complete", pos, complete)
	}
	if pos, complete := terms.PreviewAt(text, -1); pos != -1 || complete {
		t.Fatalf("no match PreviewAt = %d, %v; want -1, incomplete", pos, complete)
	}
}
