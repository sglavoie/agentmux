package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestDaemonCapturesLegacyHookPayloadAndTrustsClientCaptureMarker(t *testing.T) {
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	store, err := diagnostics.NewCaptureStore(paths, diagnostics.CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(t.Context(), diagnostics.CaptureStartRequest{
		Scope: diagnostics.CaptureScope{WindowID: 42}, Duration: time.Minute, IncludeHookPayloads: true,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(NewStateGuard(0), nil)
	handler.setCaptureStore(store)
	handler.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse", RequestID: "legacy-capture",
		PayloadJSON: `{"tool_name":"Bash"}`,
	})
	handler.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse", RequestID: "already-captured",
		PayloadJSON: `{"tool_name":"Read"}`, PayloadCaptured: true,
	})
	status, err := store.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Descriptor == nil || status.Descriptor.CapturedPayloads != 1 {
		t.Fatalf("capture status = %+v", status)
	}
}

func TestDaemonMaintenanceExpiresCaptureWithoutKittyOrFurtherHooks(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	store, err := diagnostics.NewCaptureStore(paths, diagnostics.CaptureOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(t.Context(), diagnostics.CaptureStartRequest{
		Scope: diagnostics.CaptureScope{Harness: "codex"}, Duration: time.Minute, IncludeHookPayloads: true,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(NewStateGuard(0), nil)
	handler.setCaptureStore(store)
	now = now.Add(2 * time.Minute)
	handler.maintenanceTick(now)
	status, err := store.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Descriptor == nil || status.Descriptor.Status != "expired" {
		t.Fatalf("capture status = %+v", status)
	}
}
