package daemon

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestProbeCodexScreenPrompts_UnmanagedCodexForegroundReplacesBoltWithQuestion(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeForegroundWindows(
			42,
			palette.BuildTitle("W:api", "bolt", false),
			[]string{"/opt/homebrew/bin/codex", "--yolo"},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "question", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if got := h.guard.Current(42); got != "elicitation" {
		t.Fatalf("guard.Current = %q, want elicitation", got)
	}

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.PromptKind != string(activity.CodexPromptRequestUserInput) || ev.ResultIconState != "question" {
		t.Fatalf("event = %+v, want request_user_input/question", ev)
	}
	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	lastHarness := h.store[42].LastHarness
	h.stateMu.Unlock()
	if meta.Durable.Harness != harness.Codex || lastHarness != harness.Codex {
		t.Fatalf("ownerless Codex identity = durable %q LastHarness %q, want explicit codex", meta.Durable.Harness, lastHarness)
	}
}

func TestProbeCodexScreenPrompts_UnmanagedPipeModeCodexForegroundIsNotProbed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cmdline   []string
		wantProbe bool
	}{
		{"interactive codex", []string{"/opt/homebrew/bin/codex", "--yolo"}, true},
		{"codex exec", []string{"/opt/homebrew/bin/codex", "exec", "say hi"}, false},
		{"codex review", []string{"/opt/homebrew/bin/codex", "review"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var reads []string
			fake := &fakeKittyClient{
				windows: codexProbeForegroundWindows(
					42,
					palette.BuildTitle("W:api", "bolt", false),
					tc.cmdline,
				),
				getText: map[string]string{
					kitty.MatchWindowByID(42): codexRequestUserInputScreen,
				},
			}
			fake.beforeGetText = func(match, _ string) {
				mu.Lock()
				defer mu.Unlock()
				reads = append(reads, match)
			}
			h := newTestHandler()
			h.SetKittyClient(fake)

			h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

			mu.Lock()
			gotReads := len(reads)
			mu.Unlock()
			if tc.wantProbe {
				if gotReads == 0 {
					t.Fatal("GetText calls = 0, want the interactive pane probed")
				}
				if got := len(fake.titleCalls()); got != 1 {
					t.Fatalf("SetTabTitle calls = %d, want 1", got)
				}
				return
			}
			if gotReads != 0 {
				t.Fatalf("GetText calls = %v, want none for a non-interactive run", reads)
			}
			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0", got)
			}
			if resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42}); len(resp.Events) != 0 {
				t.Fatalf("events = %d, want 0", len(resp.Events))
			}
		})
	}
}

func TestProbeCodexScreenPrompts_ManagedOwnerSuppressesUnmanagedCodexSiblingNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := activity.NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Permission: true})
		h := NewHandler(guard, notifier)
		fake := &fakeKittyClient{
			windows: codexProbeSplitWindows(
				palette.BuildTitle("W:api", "bolt", false),
				kitty.Window{
					ID:       42,
					UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
				},
				kitty.Window{
					ID:       43,
					IsActive: true,
					ForegroundProcesses: []kitty.ForegroundProcess{
						{Cmdline: []string{"/opt/homebrew/bin/codex", "--yolo"}},
					},
				},
			),
			getText: map[string]string{
				kitty.MatchWindowByID(43): codexRequestUserInputScreen,
			},
		}
		h.SetKittyClient(fake)
		h.guard.TryUpdate(43, "active", false)

		h.ProbeCodexScreenPrompts(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 from unmanaged sibling", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d, want 0", got)
		}
		resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43})
		if len(resp.Events) != 0 {
			t.Fatalf("sibling events = %d, want 0", len(resp.Events))
		}
		if got := h.guard.Current(43); got != "active" {
			t.Fatalf("sibling guard.Current = %q, want active", got)
		}
	})
}

func TestProbeCodexScreenPrompts_IgnoresUnmanagedNonCodexForeground(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeForegroundWindows(
			42,
			palette.BuildTitle("W:api", "bolt", false),
			[]string{"/bin/zsh"},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}

func TestProbeCodexScreenPrompts_UnmanagedTabProbesOnlyActiveCodexForeground(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			palette.BuildTitle("W:api", "bolt", false),
			kitty.Window{
				ID: 42,
				ForegroundProcesses: []kitty.ForegroundProcess{
					{Cmdline: []string{"/opt/homebrew/bin/codex", "--yolo"}},
				},
			},
			kitty.Window{
				ID:       43,
				IsActive: true,
				ForegroundProcesses: []kitty.ForegroundProcess{
					{Cmdline: []string{"/opt/homebrew/bin/codex", "--yolo"}},
				},
			},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexRequestUserInputScreen,
			kitty.MatchWindowByID(43): codexCommandApprovalScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].match != kitty.MatchTabByWindowID(43) {
		t.Fatalf("SetTabTitle match = %q, want %q", calls[0].match, kitty.MatchTabByWindowID(43))
	}
	if want := palette.BuildTitle("W:api", "key", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42}); len(resp.Events) != 0 {
		t.Fatalf("inactive foreground events = %d, want 0", len(resp.Events))
	}
	if resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43}); len(resp.Events) != 1 {
		t.Fatalf("active foreground events = %d, want 1", len(resp.Events))
	}
}
