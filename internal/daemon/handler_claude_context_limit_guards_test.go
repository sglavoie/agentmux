package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestProbeClaudeContextLimit_IgnoresNonClaudeHarnesses(t *testing.T) {
	for _, harnessName := range []string{"codex", "copilot"} {
		t.Run(harnessName, func(t *testing.T) {
			h := newTestHandler()
			fake := &fakeKittyClient{
				windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
					Harness: harnessName,
				}),
				getText: map[string]string{
					kitty.MatchWindowByID(42): claudeContextLimitScreen,
				},
			}
			h.SetKittyClient(fake)

			h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0", got)
			}
			resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
			if len(resp.Events) != 0 {
				t.Fatalf("events = %d, want 0", len(resp.Events))
			}
		})
	}
}

func TestProbeClaudeContextLimit_IgnoresProtectedIcons(t *testing.T) {
	protected := []string{"stop", "warning", "error", "key", "question", "compact"}
	for _, state := range protected {
		t.Run("state="+state, func(t *testing.T) {
			h := newTestHandler()
			fake := &fakeKittyClient{
				windows: codexProbeWindows(42, palette.BuildTitle("api", state, false), kitty.UserVars{
					Harness: "claude",
				}),
				getText: map[string]string{
					kitty.MatchWindowByID(42): claudeContextLimitScreen,
				},
			}
			h.SetKittyClient(fake)

			h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0", got)
			}
			resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
			if len(resp.Events) != 0 {
				t.Fatalf("events = %d, want 0", len(resp.Events))
			}
		})
	}
}

func TestProbeClaudeContextLimit_NilKittyClient_NoPanic(t *testing.T) {
	h := newTestHandler()
	// Do NOT call SetKittyClient — h.kittyClient stays nil.

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))
	// Reaching this point without panic is the assertion.
}

func TestProbeClaudeContextLimit_DisabledByConfigSkipsProbe(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)
	h.SetClaudeContextLimitProbe(false)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0 (probe should be a no-op when disabled)", got)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}
