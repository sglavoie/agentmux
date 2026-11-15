package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

func TestActiveToolHarnessLifecycle(t *testing.T) {
	for _, tc := range []struct{ harness, pre, post, failure string }{
		{"claude", "PreToolUse", "PostToolUse", "PostToolUseFailure"},
		{"codex", "PreToolUse", "PostToolUse", "PostToolUseFailure"},
		{"copilot", "preToolUse", "postToolUse", "postToolUseFailure"},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			for _, end := range []string{tc.post, tc.failure, "PostToolBatch"} {
				t.Run(end, func(t *testing.T) {
					h := newTestHandler()
					send := func(event, tool string) {
						action, ok := hookevent.ResolveActionForHarness(event, hookevent.HookPayload{ToolName: tool}, tc.harness)
						if !ok {
							t.Fatalf("unrecognized event %s", event)
						}
						resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Event: event, Action: action, ToolName: tool, Harness: tc.harness})
						if !resp.OK {
							t.Fatal(resp.Error)
						}
					}
					snapshot := func() protocol.WindowStateSnapshot {
						return h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[7]
					}
					send(tc.pre, "Bash")
					first := snapshot()
					if first.ActiveTool != "Bash" || first.BusySinceAt == 0 {
						t.Fatalf("pre-tool = %+v", first)
					}
					h.stateMu.Lock()
					meta := h.exportEnrichLocked(7, h.windowMetadata[7])
					h.stateMu.Unlock()
					if meta.Shadow.ActiveTool != "Bash" {
						t.Fatal("tool missing from metadata shadow")
					}
					send(tc.post, "Read")
					if snapshot().ActiveTool != "Bash" {
						t.Fatal("unrelated completion cleared current tool")
					}
					tool := "Bash"
					if end == "PostToolBatch" {
						tool = ""
					}
					send(end, tool)
					got := snapshot()
					if got.ActiveTool != "" {
						t.Fatalf("tool survived %s: %+v", end, got)
					}
					if end != tc.failure && got.BusySinceAt != first.BusySinceAt {
						t.Fatal("tool completion reset busy stretch")
					}
					wire, err := json.Marshal(got)
					if err != nil || strings.Contains(string(wire), "active_tool") {
						t.Fatalf("cleared wire = %s, %v", wire, err)
					}
				})
			}
		})
	}
}

func TestActiveToolClearedWithBusyAnchor(t *testing.T) {
	for _, icon := range []string{"stop", "key", "question", "warning", "error", ""} {
		t.Run(icon, func(t *testing.T) {
			h := newTestHandler()
			h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active", Event: "PreToolUse", ToolName: "Bash"})
			publishIcon(h, 7, icon)
			got := h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[7]
			if got.ActiveTool != "" || got.BusySinceAt != 0 {
				t.Fatalf("tool outlived busy anchor: %+v", got)
			}
		})
	}
}

func TestActiveToolDoesNotPublishElicitationOrSuppressedHooks(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "elicitation", Event: "PreToolUse", ToolName: "request_user_input", Harness: "codex"})
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active", Event: "PreToolUse", ToolName: "Bash", Harness: "codex"})
	if got := h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[7]; got.ActiveTool != "" || got.IconState != "question" {
		t.Fatalf("suppressed tool leaked: %+v", got)
	}
}
