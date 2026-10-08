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
	pos, fit := terms.PreviewAt(text, first)
	if fit != FitWords || !strings.HasPrefix(text[pos:], "kitty socket listens") {
		t.Fatalf("PreviewAt = %d (%q), %v; want the earliest match on the line holding both", pos, text[pos:], fit)
	}
}

// A file name holds both words of `daemon test`, but the sentence about the
// daemon test is what the searcher meant.
func TestPreviewAtPrefersALineWhereEveryTermStartsAWord(t *testing.T) {
	text := "see daemon_state_acceptance_doc_test.go\nthe latest daemon run\ni could not reproduce the daemon test failure\n"
	terms := Parse("daemon test")
	first := terms.NewTally().Add(text)
	pos, fit := terms.PreviewAt(text, first)
	if fit != FitWords || !strings.HasPrefix(text[pos:], "daemon test failure") {
		t.Fatalf("PreviewAt = %d (%q), %v; want the line where both words start a word", pos, text[pos:], fit)
	}

	single := Parse("test")
	pos, fit = single.PreviewAt(text, single.NewTally().Add(text))
	if fit != FitWords || !strings.HasPrefix(text[pos:], "test failure") {
		t.Fatalf("single term PreviewAt = %d (%q), %v; want the first word-start occurrence", pos, text[pos:], fit)
	}
}

func TestPreviewAtFallsBackToTheFirstLineHoldingEveryTermInsideWords(t *testing.T) {
	text := "daemon tabs\nsee daemon_test.go\nmore daemon_test.go\n"
	terms := Parse("daemon test")
	pos, fit := terms.PreviewAt(text, 0)
	if fit != FitLine || !strings.HasPrefix(text[pos:], "daemon_test.go\nmore") {
		t.Fatalf("PreviewAt = %d (%q), %v; want the first line holding both", pos, text[pos:], fit)
	}
}

func TestPreviewAtFallsBackToAWordWhenNoLineHoldsEveryTerm(t *testing.T) {
	text := "kitty tabs\nsocket closed\n"
	terms := Parse("kitty socket")
	if pos, fit := terms.PreviewAt(text, 0); pos != 0 || fit != FitTerm {
		t.Fatalf("PreviewAt = %d, %v; want first, which starts a word", pos, fit)
	}
	tagged := "<send_user_message_reply>\nthe message was sent\ncount them\n"
	pos, fit := Parse("message count").PreviewAt(tagged, strings.Index(tagged, "message"))
	if fit != FitTerm || !strings.HasPrefix(tagged[pos:], "message was sent") {
		t.Fatalf("PreviewAt = %d (%q), %v; want the first term that starts a word", pos, tagged[pos:], fit)
	}
	inside := "send_user_message\nrecount\n"
	first := strings.Index(inside, "message")
	if pos, fit := Parse("message count").PreviewAt(inside, first); pos != first || fit != FitFirst {
		t.Fatalf("PreviewAt = %d, %v; want first unchanged when no term starts a word", pos, fit)
	}
	if pos, fit := Parse("kitty").PreviewAt(text, 0); pos != 0 || fit != FitWords {
		t.Fatalf("single term PreviewAt = %d, %v; want first", pos, fit)
	}
	if pos, fit := terms.PreviewAt(text, -1); pos != -1 || fit != FitFirst {
		t.Fatalf("no match PreviewAt = %d, %v; want -1", pos, fit)
	}
}
