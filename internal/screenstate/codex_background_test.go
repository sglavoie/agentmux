package screenstate

import (
	"strings"
	"testing"
)

// TestCodexBackgroundTerminalVeto covers the one Codex screen where every
// settling gate is satisfied HONESTLY and settling is still wrong.
//
// An interrupted or completed turn with a detached background terminal still
// running renders a recognized terminal marker, no live status line, and an
// accepted empty composer. The model's turn really did end, so nothing in the
// classifier's evidence is stale or misread — the footer is the only thing on
// screen that says work the pane started is still going, and without a veto of
// its own the pane paints 🛑 over live background work.
//
// This was masked for one release: codex-cli 0.149.x's composer placeholder was
// unrecognized, so testdata/codex_interrupted.txt carried VetoComposerBusy and
// could not settle for an unrelated reason. Session 10.1 taught the composer
// that placeholder and the mask came off.
func TestCodexBackgroundTerminalVeto(t *testing.T) {
	// The real capture. A live 0.149.1 pane, interrupted mid-`sleep 200`, with
	// the background terminal still running.
	t.Run("real interrupted capture with a background terminal", func(t *testing.T) {
		got := codexObservation(t, readScreenFixture(t, "codex_interrupted.txt"))
		if got.Vetoes&VetoBackgroundTask == 0 {
			t.Fatalf("Vetoes = %s, want background_task among them", got.Vetoes)
		}
		if got.SettleableTerminal() {
			t.Fatal("SettleableTerminal() = true over a running background terminal")
		}
		if got.State != Working {
			t.Errorf("State = %s, want %s", got.State, Working)
		}
	})

	// The isolation. These two screens differ by the footer line and nothing
	// else, so the refusal cannot be coming from the marker, the composer or any
	// other veto.
	t.Run("footer alone decides", func(t *testing.T) {
		with := codexObservation(t, codexCompletedWithBackgroundTerminalScreen)
		if with.SettleableTerminal() {
			t.Fatalf("SettleableTerminal() = true with the footer present (vetoes %s)", with.Vetoes)
		}
		if with.Vetoes != VetoBackgroundTask {
			t.Fatalf("Vetoes = %s, want background_task alone; another veto is doing the work", with.Vetoes)
		}
		without := codexObservation(t, codexFramedCompletedScreen)
		if !without.SettleableTerminal() {
			t.Fatalf("SettleableTerminal() = false without the footer (state %s, vetoes %s); "+
				"the same screen minus one line must still settle", without.State, without.Vetoes)
		}
	})

	// The veto is independent of composer recognition, which is the finding's
	// actual claim: an unknown placeholder must not be what saves this screen.
	t.Run("independent of composer recognition", func(t *testing.T) {
		unknown := strings.ReplaceAll(codexCompletedWithBackgroundTerminalScreen,
			"› Ask Codex to do anything", "› Tell Codex what to build")
		got := codexObservation(t, unknown)
		if got.Vetoes&VetoBackgroundTask == 0 {
			t.Fatalf("Vetoes = %s, want background_task with an unrecognized placeholder too", got.Vetoes)
		}
	})

	// Prose about background terminals is not a running background terminal. The
	// two completed captures both contain "• Stopping all background terminals."
	// and both must keep settling; a veto that pinned them would trade a false 🛑
	// for a permanent ⚡.
	t.Run("narration does not veto", func(t *testing.T) {
		for _, name := range []string{"codex_idle_completed_t0.txt", "codex_idle_completed_t1.txt"} {
			got := codexObservation(t, readScreenFixture(t, name))
			if got.Vetoes&VetoBackgroundTask != 0 {
				t.Errorf("%s: Vetoes = %s, want no background_task over transcript prose", name, got.Vetoes)
			}
			if !got.SettleableTerminal() {
				t.Errorf("%s: SettleableTerminal() = false (vetoes %s)", name, got.Vetoes)
			}
		}
	})

	t.Run("footer regex", func(t *testing.T) {
		accepted := []string{
			// Both real shapes: standalone under a finished turn, and appended to
			// a live status line. Verbatim from the two captures.
			"  1 background terminal running · /ps to view · /stop to close",
			"• Working (10s • esc to interrupt) · 1 background terminal running · /ps to view · /stop to close",
			"  3 background terminals running · /ps to view · /stop to close",
			// A rename of the noun must not silently disable a one-way veto.
			"  2 background tasks running",
		}
		for _, line := range accepted {
			if !codexBackgroundTerminalFooterRE.MatchString(line) {
				t.Errorf("codexBackgroundTerminalFooterRE did not match %q", line)
			}
		}

		rejected := []string{
			"• Stopping all background terminals.",
			"• I started the build in a background terminal.",
			"  0 background terminals running · /ps to view · /stop to close",
			"› Ask Codex to do anything",
			"• Completed.",
		}
		for _, line := range rejected {
			if codexBackgroundTerminalFooterRE.MatchString(line) {
				t.Errorf("codexBackgroundTerminalFooterRE matched %q, want no match", line)
			}
		}
	})
}
