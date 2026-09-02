package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestProbeCodexClearWindow_DiscardsSnapshotPredatingReducerEvent closes the
// earlier race test's missing interval: eligibility came from a cached stopped
// title, then a prompt or protected terminal landed before the per-window
// clear probe began. The snapshot receipt must reject that route before either
// screen read and leave every state family exactly as the newer event set it.
func TestProbeCodexClearWindow_DiscardsSnapshotPredatingReducerEvent(t *testing.T) {
	cases := []struct {
		name     string
		action   string
		event    string
		wantIcon string
	}{
		{name: "new prompt", action: "active", event: "UserPromptSubmit", wantIcon: "bolt"},
		{name: "protected terminal", action: "stop_failure", event: "Stop", wantIcon: "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			base := time.Unix(1_700_000_000, 0)
			windows := codexProbeWindows(42, palette.BuildTitle("W:api", "stop", false), kitty.UserVars{
				Profile: "work",
				Harness: "codex",
			})
			fake := &fakeKittyClient{
				windows: windows,
				getTextByExtent: map[getTextKey]string{
					{match: kitty.MatchWindowByID(42), extent: "screen"}: codexReportedClearedScreen,
					{match: kitty.MatchWindowByID(42), extent: "all"}:    codexReportedClearedScreen,
				},
			}
			h.SetKittyClient(fake)
			h.publishSnapshot(windows, base)
			snapshot, ready := h.cachedScreenProbeSnapshot()
			if !ready {
				t.Fatal("cached probe snapshot is not ready")
			}

			codexInjectSessionEvent(t, h, 42, "new", tc.action, tc.event, base.Unix()+1)
			reads := 0
			fake.beforeGetText = func(string, string) { reads++ }
			before := screenProbeMutationFingerprint(t, h, 42)
			h.probeCodexScreenPromptsWithSnapshot(base.Add(time.Second), snapshot, h.screenTextCacheFor())

			if reads != 0 {
				t.Fatalf("GetText calls = %d, want 0 for stale stopped snapshot", reads)
			}
			if after := screenProbeMutationFingerprint(t, h, 42); after != before {
				t.Fatalf("state mutated after stale clear snapshot:\n before %s\n after  %s", before, after)
			}
			if eff, _ := windowEffective(t, h, 42); eff.Icon != tc.wantIcon {
				t.Fatalf("effective = %+v, want newer %q preserved", eff, tc.wantIcon)
			}
		})
	}
}

// TestProbeCodexClearWindow_DiscardsObservationAfterConcurrentReducerEvent
// pins both sides of the two-read receipt boundary: the prompt case lands on
// the first read (so capture must happen before it), while the protected error
// lands on the second read (so application must re-check after both). A stale
// clear is a complete no-op across reducer, guard, audit, mute, notification,
// persistence, and renderer state.
func TestProbeCodexClearWindow_DiscardsObservationAfterConcurrentReducerEvent(t *testing.T) {
	type mutationSnapshot struct {
		shadowSeq           uint64
		stateSeq            uint64
		queuedGen           uint64
		effectiveGen        uint64
		eventSeq            uint64
		eventCount          int
		reducerTraceCount   int
		factCount           int
		durableActivityAt   int64
		durableProfile      string
		durableLastAction   string
		effectiveIcon       string
		lastSessionTitle    string
		lastAccount         string
		guard               string
		pendingReset        state.ResetFlowState
		permissionState     state.CopilotToolPermissionState
		dirty               bool
		muted               bool
		renderPending       bool
		pendingNotification bool
	}

	cases := []struct {
		name       string
		injectRead int
		action     string
		event      string
		prompt     bool
		wantIcon   string
		wantGuard  string
	}{
		{
			name:       "prompt during first read",
			injectRead: 1,
			action:     "active",
			event:      "UserPromptSubmit",
			prompt:     true,
			wantIcon:   "bolt",
			wantGuard:  "active",
		},
		{
			name:       "protected terminal during second read",
			injectRead: 2,
			action:     "stop_failure",
			event:      "Stop",
			wantIcon:   "error",
			wantGuard:  "stop_failure",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			no := false
			notifier, rec := newTestNotifier(&config.NotificationConfig{
				Permission:           true,
				PermissionDebounceMs: 60_000,
				StopFailure:          &no,
			})
			h := NewHandler(NewStateGuard(time.Hour), notifier)
			parts := palette.ParseTitleParts(palette.BuildTitle("W:api", "stop", false))
			userVars := kitty.UserVars{Profile: "work", Harness: "codex"}
			fake := &fakeKittyClient{
				getTextByExtent: map[getTextKey]string{
					{match: kitty.MatchWindowByID(42), extent: "screen"}: codexReportedClearedScreen,
					{match: kitty.MatchWindowByID(42), extent: "all"}:    codexReportedClearedScreen,
				},
			}
			h.SetKittyClient(fake)
			h.guard.TryUpdate(42, "stop", false)
			h.stateMu.Lock()
			h.ensureWindowStateLocked(42).Muted = true
			h.stateMu.Unlock()

			// A stale user_clear would cancel this pending permission. The prompt
			// injection itself does not, so that case directly proves the clear
			// made no notification mutation.
			if tc.prompt {
				h.maybeNotify(42, "W:api", "work", "permission", false)
			}

			snapshot := func() mutationSnapshot {
				var got mutationSnapshot
				h.stateMu.Lock()
				st := h.store[42]
				got.shadowSeq = st.ShadowSeq
				got.stateSeq = st.Seq
				got.queuedGen = st.QueuedGen
				got.effectiveGen = st.EffectiveGen
				got.eventSeq = h.eventSeq
				got.eventCount = len(st.EventLog)
				got.effectiveIcon = st.Effective.Icon
				got.lastSessionTitle = st.LastSessionTitle
				got.lastAccount = st.LastAccount
				got.pendingReset = st.PendingReset
				got.permissionState = st.CopilotToolPermissionState
				got.dirty = st.Dirty
				got.muted = st.Muted
				if st.Reducer != nil {
					got.reducerTraceCount = len(st.Reducer.Traces())
					got.factCount = len(st.Reducer.Facts())
				}
				meta := h.windowMetadata[42]
				got.durableActivityAt = meta.Durable.ActivityAt
				got.durableProfile = meta.Durable.Profile
				got.durableLastAction = meta.Durable.LastAction
				h.stateMu.Unlock()

				got.guard = h.guard.Current(42)
				h.renderQueue.mu.Lock()
				got.renderPending = h.renderQueue.pending[42]
				h.renderQueue.mu.Unlock()
				notifier.mu.Lock()
				_, got.pendingNotification = notifier.pending[42]
				notifier.mu.Unlock()
				return got
			}

			var afterInjection mutationSnapshot
			var reads int
			injected := false
			fake.beforeGetText = func(match, extent string) {
				if match != kitty.MatchWindowByID(42) {
					return
				}
				reads++
				if injected || reads != tc.injectRead {
					return
				}
				injected = true
				ev := protocol.NormalizedEvent{
					Source:      protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:sess-new"},
					Action:      tc.action,
					SessionID:   "sess-new",
					At:          time.Now().Unix(),
					Event:       tc.event,
					Prompt:      tc.prompt,
					EndsSession: tc.action == "stop_failure",
				}
				resp := h.handleStateUpdate(protocol.Request{
					Type:         protocol.TypeStateUpdate,
					WindowID:     42,
					Action:       tc.action,
					Event:        tc.event,
					IsPrompt:     tc.prompt,
					Account:      "work",
					Harness:      "codex",
					SessionTitle: "W:api",
					Normalized:   &ev,
				})
				if !resp.OK {
					t.Fatalf("concurrent %s rejected: %+v", tc.action, resp)
				}
				afterInjection = snapshot()
			}

			h.probeCodexClearWindow(time.Now(), directCodexProbeBinding(42, h.captureWindowReceipt(42), 0), parts, userVars,
				h.screenTextCacheFor())
			defer h.cancelPendingNotification(42)

			if !injected || reads != 2 {
				t.Fatalf("injected=%v reads=%d, want true/2", injected, reads)
			}
			if got := snapshot(); got != afterInjection {
				t.Fatalf("state mutated after stale clear:\n got  %+v\n want %+v", got, afterInjection)
			}
			if got := h.guard.Current(42); got != tc.wantGuard {
				t.Fatalf("guard.Current = %q, want concurrent %q", got, tc.wantGuard)
			}
			if eff, _ := windowEffective(t, h, 42); eff.Icon != tc.wantIcon {
				t.Fatalf("effective = %+v, want concurrent %q preserved", eff, tc.wantIcon)
			}

			h.stateMu.Lock()
			fact := factOf(h.store[42].Reducer, protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:sess-new"})
			events := append([]protocol.SessionEvent(nil), h.store[42].EventLog...)
			h.stateMu.Unlock()
			if fact == nil || fact.Acked {
				t.Fatalf("concurrent fact = %+v, want live and unacknowledged", fact)
			}
			for _, ev := range events {
				if ev.Event == screenClearProbeEvent {
					t.Fatalf("stale clear recorded an audit row: %+v", ev)
				}
			}
			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0 from discarded clear", got)
			}
			if got := len(fake.userVarsCalls()); got != 0 {
				t.Fatalf("SetUserVars calls = %d, want 0 from discarded clear", got)
			}
			if got := rec.len(); got != 0 {
				t.Fatalf("dispatches = %d, want 0", got)
			}
		})
	}
}

func TestProbeCodexScreenPrompts_ManagedOwnerSuppressesUnmanagedCodexSiblingClearProbe(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			palette.BuildTitle("W:api", "stop", false),
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
		getTextByExtent: map[getTextKey]string{
			{match: kitty.MatchWindowByID(43), extent: "screen"}: codexReportedClearedScreen,
			{match: kitty.MatchWindowByID(43), extent: "all"}:    codexReportedClearedScreen,
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(43, "stop", false)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	if got := h.guard.Current(43); got != "stop" {
		t.Fatalf("sibling guard.Current = %q, want stop", got)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43})
	if len(resp.Events) != 0 {
		t.Fatalf("sibling events = %d, want 0", len(resp.Events))
	}
}
