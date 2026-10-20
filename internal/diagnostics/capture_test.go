package diagnostics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureRequiresExplicitScopedAuthorizationAndSurvivesRestart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	store, err := NewCaptureStore(paths, CaptureOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(t.Context(), CaptureStartRequest{Duration: time.Minute, IncludeHookPayloads: true}); err == nil {
		t.Fatal("unscoped capture unexpectedly started")
	}
	if _, err := store.Start(t.Context(), CaptureStartRequest{Scope: CaptureScope{WindowID: 42}, Duration: time.Minute}); err == nil {
		t.Fatal("capture without explicit payload inclusion unexpectedly started")
	}
	descriptor, err := store.Start(t.Context(), CaptureStartRequest{
		Scope: CaptureScope{WindowID: 42, Harness: "Codex"}, Duration: time.Minute, IncludeHookPayloads: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !descriptor.Active() || descriptor.QuotaBytes != CaptureMaximumBytes || descriptor.ExpiresAt.Sub(descriptor.StartedAt) != time.Minute {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if info, err := os.Stat(paths.CaptureDir); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o700 {
		t.Fatalf("capture directory mode = %v", info.Mode().Perm())
	}
	if info, err := os.Stat(filepath.Join(paths.CaptureDir, "capture.json")); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("capture descriptor mode = %v", info.Mode().Perm())
	}

	write, err := store.Record(t.Context(), CapturePayloadRequest{
		WindowID: 42, Harness: "codex", Event: "PreToolUse", RequestID: "request-1", Payload: []byte(`{"secret":"raw-local"}`),
	})
	if err != nil || !write.Captured || write.Duplicate {
		t.Fatalf("write = %+v, err = %v", write, err)
	}
	duplicate, err := store.Record(t.Context(), CapturePayloadRequest{
		WindowID: 42, Harness: "codex", Event: "PreToolUse", RequestID: "request-1", Payload: []byte(`{"secret":"raw-local"}`),
	})
	if err != nil || !duplicate.Captured || !duplicate.Duplicate {
		t.Fatalf("duplicate = %+v, err = %v", duplicate, err)
	}
	outside, err := store.Record(t.Context(), CapturePayloadRequest{WindowID: 99, Harness: "codex", RequestID: "request-2", Payload: []byte("no")})
	if err != nil || outside.Captured || outside.Reason != "scope_mismatch" {
		t.Fatalf("outside = %+v, err = %v", outside, err)
	}

	now = now.Add(2 * time.Minute)
	restarted, err := NewCaptureStore(paths, CaptureOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	status, err := restarted.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Descriptor == nil || status.Descriptor.Status != "expired" || status.Descriptor.EndedAt == nil {
		t.Fatalf("restart status = %+v", status)
	}
	late, err := restarted.Record(t.Context(), CapturePayloadRequest{WindowID: 42, Harness: "codex", RequestID: "late", Payload: []byte("no")})
	if err != nil || late.Captured {
		t.Fatalf("late write = %+v, err = %v", late, err)
	}
}

func TestCaptureDedupeUsesEventAwareLocalHMACIdentity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	newActiveStore := func(t *testing.T) *CaptureStore {
		t.Helper()
		paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
		store, err := NewCaptureStore(paths, CaptureOptions{Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Start(t.Context(), CaptureStartRequest{
			Scope: CaptureScope{Harness: "codex"}, Duration: time.Minute, IncludeHookPayloads: true,
		}); err != nil {
			t.Fatal(err)
		}
		return store
	}
	record := func(t *testing.T, store *CaptureStore, event string) CaptureWriteResult {
		t.Helper()
		result, err := store.Record(t.Context(), CapturePayloadRequest{
			Harness: "codex", Event: event, RequestID: "request-sensitive", Payload: []byte(`{"secret":"raw-local"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	firstStore := newActiveStore(t)
	pre := record(t, firstStore, "PreToolUse")
	post := record(t, firstStore, "PostToolUse")
	duplicate := record(t, firstStore, "PreToolUse")
	if !pre.Captured || pre.Duplicate || !post.Captured || post.Duplicate || pre.Path == post.Path {
		t.Fatalf("event-aware captures = pre:%+v post:%+v", pre, post)
	}
	if !duplicate.Captured || !duplicate.Duplicate || duplicate.Path != pre.Path {
		t.Fatalf("same-event duplicate = %+v, want path %q", duplicate, pre.Path)
	}

	secondStore := newActiveStore(t)
	otherMachineKey := record(t, secondStore, "PreToolUse")
	if filepath.Base(otherMachineKey.Path) == filepath.Base(pre.Path) {
		t.Fatalf("capture filename did not vary with the local HMAC key: %q", pre.Path)
	}
}

func TestCaptureQuotaStopsWithoutPartialWriteAndPurgesAfterDay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	store, err := NewCaptureStore(paths, CaptureOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := store.Start(t.Context(), CaptureStartRequest{
		Scope: CaptureScope{Harness: "claude"}, Duration: time.Hour, IncludeHookPayloads: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptor.QuotaBytes = 128
	data, _ := json.MarshalIndent(descriptor, "", "  ")
	if err := os.WriteFile(filepath.Join(paths.CaptureDir, "capture.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := store.Record(context.Background(), CapturePayloadRequest{
		Harness: "claude", RequestID: "too-large", Payload: make([]byte, 256),
	})
	if err != nil || result.Captured || result.Reason != "quota_reached" {
		t.Fatalf("quota result = %+v, err = %v", result, err)
	}
	status, err := store.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Descriptor == nil || status.Descriptor.Status != "quota_reached" || status.Descriptor.CapturedPayloads != 0 {
		t.Fatalf("quota status = %+v", status)
	}
	files, err := os.ReadDir(filepath.Join(paths.CaptureDir, descriptor.ID))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("quota rejection published partial files: %v", files)
	}

	oldDir := filepath.Join(paths.CaptureDir, "11111111-1111-4111-8111-111111111111")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldPayload := filepath.Join(oldDir, "old.raw.json")
	if err := os.WriteFile(oldPayload, []byte("raw"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-25 * time.Hour)
	if err := os.Chtimes(oldPayload, old, old); err != nil {
		t.Fatal(err)
	}
	maintenance, err := store.Maintain(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if maintenance.PurgedPayloads != 1 {
		t.Fatalf("maintenance = %+v", maintenance)
	}
	if _, err := os.Stat(oldPayload); !os.IsNotExist(err) {
		t.Fatalf("old raw payload still exists: %v", err)
	}
}

func TestCaptureQuarantinesUnsafeDescriptorOnRestart(t *testing.T) {
	t.Parallel()

	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	store, err := NewCaptureStore(paths, CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(t.Context(), CaptureStartRequest{
		Scope: CaptureScope{Harness: "codex"}, Duration: time.Minute, IncludeHookPayloads: true,
	}); err != nil {
		t.Fatal(err)
	}
	descriptorPath := filepath.Join(paths.CaptureDir, "capture.json")
	data, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor CaptureDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor.QuotaBytes = CaptureMaximumBytes + 1
	data, _ = json.Marshal(descriptor)
	if err := os.WriteFile(descriptorPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewCaptureStore(paths, CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	status, err := restarted.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Descriptor != nil {
		t.Fatalf("unsafe descriptor remained active: %+v", status.Descriptor)
	}
	matches, err := filepath.Glob(descriptorPath + ".corrupt-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("quarantined descriptors = %v, err = %v", matches, err)
	}
}

// A capture scoped to a runtime session takes that session's payloads and no
// other's: a hook from another session, or a Kitty hook that carries none, is
// outside it. A scope cannot name a window and a runtime session at once.
func TestCaptureScopedToARuntimeSession(t *testing.T) {
	t.Parallel()

	const scoped, other = "rs-0123456789abcdef0123456789abcdef", "rs-ffffffffffffffffffffffffffffffff"
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store, err := NewCaptureStore(PathsForRoot(filepath.Join(t.TempDir(), "state")), CaptureOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(t.Context(), CaptureStartRequest{
		Scope: CaptureScope{WindowID: 42, RuntimeSessionID: scoped}, Duration: time.Minute, IncludeHookPayloads: true,
	}); err == nil {
		t.Fatal("a capture scoped to a window and a runtime session unexpectedly started")
	}
	descriptor, err := store.Start(t.Context(), CaptureStartRequest{
		Scope: CaptureScope{RuntimeSessionID: " " + scoped + " "}, Duration: time.Minute, IncludeHookPayloads: true,
	})
	if err != nil || descriptor.Scope != (CaptureScope{RuntimeSessionID: scoped}) {
		t.Fatalf("descriptor = %+v, err = %v", descriptor, err)
	}
	inside, err := store.Record(t.Context(), CapturePayloadRequest{RuntimeSessionID: scoped, Harness: "claude", RequestID: "request-1", Payload: []byte("yes")})
	if err != nil || !inside.Captured {
		t.Fatalf("inside = %+v, err = %v", inside, err)
	}
	for name, request := range map[string]CapturePayloadRequest{
		"another session": {RuntimeSessionID: other, Harness: "claude", RequestID: "request-2", Payload: []byte("no")},
		"a Kitty window":  {WindowID: 42, Harness: "claude", RequestID: "request-3", Payload: []byte("no")},
	} {
		outside, err := store.Record(t.Context(), request)
		if err != nil || outside.Captured || outside.Reason != "scope_mismatch" {
			t.Errorf("%s: %+v, err = %v; want a scope mismatch", name, outside, err)
		}
	}
	// The scope survives a restart of whatever reads it.
	reopened, err := NewCaptureStore(PathsForRoot(filepath.Dir(store.paths.CaptureDir)), CaptureOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Record(t.Context(), CapturePayloadRequest{RuntimeSessionID: scoped, Harness: "claude", RequestID: "request-4", Payload: []byte("yes")})
	if err != nil || !again.Captured {
		t.Fatalf("after reopening: %+v, err = %v", again, err)
	}
}
