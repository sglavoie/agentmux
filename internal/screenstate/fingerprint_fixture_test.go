package screenstate

import (
	"regexp"
	"strings"
	"testing"
)

// This file validates Fingerprint against real captures rather than against
// screens written to make it pass. Layer B's whole claim is that a pane which is
// visibly working cannot hold two consecutive ticks still, and that an idle pane
// can — and neither half of that claim is worth anything unless the screens come
// off live harness panes.
//
// The captures were taken with `kitten @ get-text --extent=screen`, the same
// call and the same extent the daemon reads panes with (internal/kitty:
// GetText(match, "screen")), against Claude Code 2.1.231 and codex-cli 0.149.1.

// TestFingerprintSeparatesConsecutiveWorkingCaptures is the assertion the
// stabilization gate rests on: two captures of the same working pane, taken 3s
// apart at the probe's own cadence, must not agree.
//
// The wide pair and the narrow pair fail the gate for different reasons, which
// is why both are kept. The wide pair moves because Claude renders an elapsed
// timer; the narrow pair moves even though Claude has truncated the timer off
// the screen entirely, because the spinner glyph still animates. Layer A can
// read the first and is blind to the second.
func TestFingerprintSeparatesConsecutiveWorkingCaptures(t *testing.T) {
	cases := []struct {
		name string
		t0   string
		t1   string
	}{
		{
			name: "wide pane advances its elapsed timer",
			t0:   "claude_working_pouncing_t0.txt",
			t1:   "claude_working_pouncing_t1.txt",
		},
		{
			name: "narrow pane advances only its spinner glyph",
			t0:   "claude_working_narrow_t0.txt",
			t1:   "claude_working_narrow_t1.txt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := readScreenFixture(t, tc.t0)
			second := readScreenFixture(t, tc.t1)

			if got, other := Fingerprint(first), Fingerprint(second); got == other {
				t.Fatalf("Fingerprint(%s) == Fingerprint(%s) == %d, want distinct generations so the probe restarts its streak", tc.t0, tc.t1, got)
			}
		})
	}
}

// TestFingerprintJoinsIdleCapturesThatDifferOnlyInTrailingPadding is the other
// half of the gate. If an idle pane's fingerprint flickered, the gate would
// suppress every stop rather than only the false ones, and the tab would stay
// on ⚡ forever.
//
// The pair is one Claude pane captured twice with nothing happening in between.
// The only thing that changed is the cursor cell after the ❯ composer: Kitty
// emits it as a trailing space while the pane holds focus and omits it when the
// pane does not, so a pane the operator merely clicks away from would otherwise
// look like a pane that changed.
func TestFingerprintJoinsIdleCapturesThatDifferOnlyInTrailingPadding(t *testing.T) {
	const (
		plain        = "claude_idle_completed.txt"
		withPadding  = "claude_idle_completed_trailing_space.txt"
		composerLine = 25 // zero-based; the ❯ line the two captures disagree on
	)

	bare := readScreenFixture(t, plain)
	padded := readScreenFixture(t, withPadding)

	// Guard the assertion against going vacuous. If a future capture pass
	// overwrote both files with the same bytes the fingerprint equality below
	// would still pass while pinning nothing at all.
	if bare == padded {
		t.Fatalf("%s and %s are byte-identical, so the right-trim assertion proves nothing", plain, withPadding)
	}
	bareLines := strings.Split(bare, "\n")
	paddedLines := strings.Split(padded, "\n")
	if len(bareLines) != len(paddedLines) {
		t.Fatalf("%s has %d lines and %s has %d, want the same idle screen captured twice", plain, len(bareLines), withPadding, len(paddedLines))
	}
	for i := range bareLines {
		if strings.TrimRight(bareLines[i], " \t ") != strings.TrimRight(paddedLines[i], " \t ") {
			t.Fatalf("line %d differs in more than trailing padding: %q vs %q", i+1, bareLines[i], paddedLines[i])
		}
	}
	if bareLines[composerLine] == paddedLines[composerLine] {
		t.Fatalf("line %d is identical in both captures (%q); the pair no longer carries the composer padding it exists to pin", composerLine+1, bareLines[composerLine])
	}

	if got, want := Fingerprint(padded), Fingerprint(bare); got != want {
		t.Fatalf("Fingerprint(%s) = %d, Fingerprint(%s) = %d, want equal so a focus change cannot restart the streak", withPadding, got, plain, want)
	}
}

// TestFingerprintHoldsStillOnConsecutiveCodexIdleCaptures records, as a test
// rather than as a note, the empirical result that decides whether Codex can be
// raised to two required samples.
//
// Codex's only terminal hook is Stop. If its idle screen were not byte-stable
// over the probe's cadence, giving it a screen-derived generation would mean
// Codex tabs could never settle — a strict regression traded for Claude's fix.
// The two captures are 3s apart on a finished Codex turn and were byte-identical
// even before normalization.
func TestFingerprintHoldsStillOnConsecutiveCodexIdleCaptures(t *testing.T) {
	first := readScreenFixture(t, "codex_idle_completed_t0.txt")
	second := readScreenFixture(t, "codex_idle_completed_t1.txt")

	if got, want := Fingerprint(second), Fingerprint(first); got != want {
		t.Fatalf("Fingerprint(codex_idle_completed_t1.txt) = %d, Fingerprint(codex_idle_completed_t0.txt) = %d, want equal; an unstable Codex idle screen means Codex must not take a screen-derived generation", got, want)
	}
}

// legacyBusyHintRE is the entire "is Claude working?" test the daemon shipped
// with before this plan: two literal hints, either of which meant "not idle".
// Claude Code 2.1.x renders neither, which is why a working pane settled a stop.
var legacyBusyHintRE = regexp.MustCompile(`(?i)esc to interrupt|ctrl\+b to`)

// liveTimerRE is a progress ellipsis followed by a parenthesized elapsed timer —
// the shape a wording-independent working-line veto can key on. It is declared
// here only to certify what the captures do and do not show; the veto Layer A
// grows later owns the real pattern.
var liveTimerRE = regexp.MustCompile(`\x{2026}\s*\(\s*\d+[hms]`)

// TestWorkingCapturesStillDefeatTheClassifierTheyWereTakenFor certifies the
// provenance of the three working captures. Each exists to be a counterexample
// to something, and a later re-capture that quietly lost that property would
// leave the suite green while removing the only evidence the bug was real.
func TestWorkingCapturesStillDefeatTheClassifierTheyWereTakenFor(t *testing.T) {
	t.Run("the regression captures carry no legacy busy hint", func(t *testing.T) {
		// Both wide working captures came off a pane mid-turn with an empty
		// composer. The old classifier asked only whether a busy hint was
		// visible, so on these screens it answered "idle" and settled 🛑 over a
		// pane that was visibly still working.
		for _, name := range []string{
			"claude_working_pouncing_t0.txt",
			"claude_working_pouncing_t1.txt",
			"claude_working_reticulating.txt",
		} {
			if hint := legacyBusyHintRE.FindString(readScreenFixture(t, name)); hint != "" {
				t.Errorf("%s contains %q, so the pre-fix classifier would have caught it and the capture is not a regression fixture", name, hint)
			}
		}
	})

	t.Run("the reticulating capture keeps the in-paren completed-line trap", func(t *testing.T) {
		// "● Reticulating… (4m 29s · … · thought for 5s)" is a working line that
		// contains a finished-marker shape. A completed-line regex that is not
		// anchored, or that is consulted before the vetoes, matches it and
		// settles a stop on a pane that is mid-turn.
		screen := readScreenFixture(t, "claude_working_reticulating.txt")
		trap := ""
		for _, line := range NormalizeLines(screen) {
			if strings.Contains(line, "for 5s") {
				trap = line
				break
			}
		}
		if trap == "" {
			t.Fatal("claude_working_reticulating.txt no longer shows a \"thought for 5s\" segment; the trap it exists to pose is gone")
		}
		if !liveTimerRE.MatchString(trap) {
			t.Errorf("the trap line %q carries no live timer, so it is not a working line", trap)
		}
		if strings.HasSuffix(trap, "5s") {
			t.Errorf("the trap line %q ends in the duration; the segment must stay parenthesized or an end-anchored completed-line regex is never tested against it", trap)
		}
	})

	t.Run("the narrow captures hide the timer a veto would key on", func(t *testing.T) {
		// Below roughly 20 columns Claude Code 2.1.231 drops the parenthesized
		// timer from its status line rather than wrapping it, leaving a bare
		// gerund and an ellipsis. Nothing a working-line veto can match survives,
		// which is the concrete reason Layer A must not ship without Layer B.
		for _, name := range []string{"claude_working_narrow_t0.txt", "claude_working_narrow_t1.txt"} {
			screen := readScreenFixture(t, name)
			if timer := liveTimerRE.FindString(screen); timer != "" {
				t.Errorf("%s still shows a live timer (%q); it no longer stands for the narrow pane a veto cannot read", name, timer)
			}
			if hint := legacyBusyHintRE.FindString(screen); hint != "" {
				t.Errorf("%s contains %q, so it is not a screen the classifiers are blind to", name, hint)
			}
			if !strings.Contains(screen, "…") {
				t.Errorf("%s shows no progress ellipsis at all, so it is not a capture of a working pane", name)
			}
		}
	})
}
