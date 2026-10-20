package daemon

// The applied reason a synthetic settle writes into the durable per-window event
// ring. It is the ONLY place the settle decision is legible without turning on
// debug logging: `amx daemon export-events` reads it back, and it has to name
// the grade of evidence, the run length that grade bought, and the fingerprint
// every sample in that run agreed on. A settle at samples=2 on a stable
// generation is the designed path; anything else names the fault directly.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// settleReason returns the reason on the last ScreenStopProbe row for a window.
func settleReason(t *testing.T, h *Handler, windowID int) string {
	t.Helper()
	event, ok := lastEventNamed(h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: windowID}).Events, screenStopProbeEvent)
	if !ok {
		t.Fatalf("no %s row on window %d", screenStopProbeEvent, windowID)
	}
	return event.Reason
}

// TestSettleReasonNamesTierSamplesAndGeneration pins the diagnostic suffix on
// both harnesses. The expected values are computed from the fixture's own
// observation rather than written as literals for the generation, because the
// point of the assertion is that the number in the ring is the SAME fingerprint
// the stabilization gate compared — a suffix carrying some other value would be
// worse than none at all. The tier and the sample count are literals, so
// re-tuning either has to be a deliberate edit here.
func TestSettleReasonNamesTierSamplesAndGeneration(t *testing.T) {
	t.Parallel()

	t.Run("claude quiescent", func(t *testing.T) {
		assertClaudeScreenTier(t, claudeQuiescentIdleScreen, screenstate.TierQuiescent)
		base := time.Now()
		windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: harness.Claude,
		})
		fake := &fakeKittyClient{windows: windows, getText: map[string]string{
			kitty.MatchWindowByID(42): claudeQuiescentIdleScreen,
		}}
		h := newTestHandler()
		h.SetKittyClient(fake)
		claudeSeedActiveBolt(t, h, 42, "sess-reason-claude", base.Unix())

		// A bare (marker-free) pane title buys no bump, so this is the tier's own
		// count: three samples for the weakest terminal evidence Claude produces.
		const wantSamples = activity.ClaudeIdleProbeQuiescentSamples
		for sample := 1; sample <= wantSamples; sample++ {
			h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("effective after %d samples = %+v, want stop", wantSamples, eff)
		}
		want := fmt.Sprintf("(tier=quiescent samples=3 generation=%d)",
			activity.ClassifyScreenState(harness.Claude, claudeQuiescentIdleScreen).Fingerprint)
		if got := settleReason(t, h, 42); !strings.HasSuffix(got, want) {
			t.Fatalf("settle reason = %q, want suffix %q", got, want)
		}
	})

	t.Run("codex explicit terminal", func(t *testing.T) {
		observation := activity.ClassifyScreenState(harness.Codex, codexCompletedTurnScreen)
		if observation.Tier != screenstate.TierTerminalExplicit {
			t.Fatalf("fixture tier = %v, want %v", observation.Tier, screenstate.TierTerminalExplicit)
		}
		base := time.Now()
		windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: harness.Codex,
		})
		fake := &fakeKittyClient{windows: windows, getText: map[string]string{
			kitty.MatchWindowByID(42): codexCompletedTurnScreen,
		}}
		h := newTestHandler()
		h.SetKittyClient(fake)
		codexSeedActiveBolt(t, h, 42, "sess-reason-codex", base.Unix())

		probeCodexUntilSettled(t, h, base.Add(time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("effective after the terminal run = %+v, want stop", eff)
		}
		// Codex has exactly one reachable tier and no pane-title evidence, so
		// every Codex settle costs the two-sample floor.
		want := fmt.Sprintf("(tier=terminal_explicit samples=2 generation=%d)", observation.Fingerprint)
		if got := settleReason(t, h, 42); !strings.HasSuffix(got, want) {
			t.Fatalf("settle reason = %q, want suffix %q", got, want)
		}
	})
}
