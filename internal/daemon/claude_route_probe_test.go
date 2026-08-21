package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestProbeClaudeContextLimit_RoutedProducerUpdatesManagedTarget(t *testing.T) {
	h := newTestHandler()
	base := time.Unix(1_700_000_000, 0)
	windows := routedClaudeProbeWindows(true)
	fake := &fakeKittyClient{
		windows: windows,
		getText: map[string]string{
			kitty.MatchWindowByID(42): "managed target has no Claude banner\n",
			kitty.MatchWindowByID(43): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)
	seedRoutedClaudeProbe(t, h, 42, 43, "routed-context", base.Unix())

	var readMatch string
	fake.beforeGetText = func(match, extent string) {
		if extent == "screen" {
			readMatch = match
		}
	}
	h.ProbeClaudeContextLimit(base.Add(time.Second))

	if readMatch != kitty.MatchWindowByID(43) {
		t.Fatalf("GetText match = %q, want routed producer pane 43", readMatch)
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "error" || visible != "error" {
		t.Fatalf("target effective = %+v visible %q, want error/error", eff, visible)
	}
	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	lastHarness := h.store[42].LastHarness
	_, sourceHasState := h.store[43]
	h.stateMu.Unlock()
	if sourceHasState {
		t.Fatal("routed producer unexpectedly acquired managed reducer state")
	}
	if meta.Durable.Harness != harness.Codex || meta.Durable.Profile != "owner" || lastHarness != harness.Codex {
		t.Fatalf("target ownership changed: durable=%+v LastHarness=%q; want codex/owner", meta.Durable, lastHarness)
	}
	if calls := fake.titleCalls(); len(calls) != 1 || calls[0].match != kitty.MatchTabByWindowID(42) {
		t.Fatalf("title calls = %+v, want one managed-target render", calls)
	}
}

func TestProbeClaudeScreenStop_RoutedProducerSettlesManagedTarget(t *testing.T) {
	h := newTestHandler()
	base := time.Unix(1_700_000_000, 0)
	fake := &fakeKittyClient{
		windows: routedClaudeProbeWindows(true),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeWorkingScreen,
			kitty.MatchWindowByID(43): claudeInterruptedScreen,
		},
	}
	h.SetKittyClient(fake)
	seedRoutedClaudeProbe(t, h, 42, 43, "routed-idle", base.Unix())

	var readMatch string
	fake.beforeGetText = func(match, extent string) {
		if extent == "screen" {
			readMatch = match
		}
	}
	h.ProbeClaudeScreenStop(base.Add(time.Second))

	if readMatch != kitty.MatchWindowByID(43) {
		t.Fatalf("GetText match = %q, want routed producer pane 43", readMatch)
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
		t.Fatalf("target effective = %+v visible %q, want stop/stop", eff, visible)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(detail.Events, screenStopProbeEvent); !ok || ev.Action != "stop" {
		t.Fatalf("target ScreenStopProbe = %+v ok=%v, want stop", ev, ok)
	}
	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	lastHarness := h.store[42].LastHarness
	h.stateMu.Unlock()
	if meta.Durable.Harness != harness.Codex || meta.Durable.Profile != "owner" || lastHarness != harness.Codex {
		t.Fatalf("target ownership changed: durable=%+v LastHarness=%q; want codex/owner", meta.Durable, lastHarness)
	}
}

func TestProbeClaudeScreenStop_RoutedBusyProducerTitleKeepsManagedTargetActive(t *testing.T) {
	h := newTestHandler()
	base := time.Unix(1_700_000_000, 0)
	windows := routedClaudeProbeWindows(true)
	setProbeWindowTitle(t, windows, 43, "\u2802 Fix drawer drag resistance")
	fake := &fakeKittyClient{
		windows: windows,
		getText: map[string]string{
			kitty.MatchWindowByID(43): claudeWorkingScreen,
		},
	}
	h.SetKittyClient(fake)
	seedRoutedClaudeProbe(t, h, 42, 43, "routed-title-busy", base.Unix())

	reads := 0
	fake.beforeGetText = func(match, extent string) {
		if match == kitty.MatchWindowByID(43) && extent == "screen" {
			reads++
		}
	}
	h.ProbeClaudeScreenStop(base.Add(time.Second))
	h.ProbeClaudeScreenStop(base.Add(3 * time.Second))

	// The routed producer's screen — not its title — is what holds the target
	// active, so the probe reads it on every tick.
	if reads != 2 {
		t.Fatalf("routed busy producer screen reads = %d, want 2", reads)
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "bolt" || visible != "bolt" {
		t.Fatalf("managed target with busy producer title = %+v/%q, want bolt/bolt", eff, visible)
	}
	if ev, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, screenStopProbeEvent); ok {
		t.Fatalf("target ScreenStopProbe recorded for busy routed producer: %+v", ev)
	}
}

func TestProbeClaudeScreenStop_RoutedBusyTitleInvalidatesStaleIdleRead(t *testing.T) {
	h := newTestHandler()
	base := time.Unix(1_700_000_000, 0)
	idleWindows := routedClaudeProbeWindows(true)
	setProbeWindowTitle(t, idleWindows, 43, "Fix drawer drag resistance")
	busyWindows := routedClaudeProbeWindows(true)
	setProbeWindowTitle(t, busyWindows, 43, "\u2733 Fix drawer drag resistance")
	fake := &fakeKittyClient{
		windows: idleWindows,
		getText: map[string]string{
			kitty.MatchWindowByID(43): claudeInterruptedScreen,
		},
	}
	h.SetKittyClient(fake)
	seedRoutedClaudeProbe(t, h, 42, 43, "routed-title-race", base.Unix())
	h.publishSnapshot(idleWindows, base)
	staleSnapshot, ready := h.cachedScreenProbeSnapshot()
	if !ready {
		t.Fatal("idle screen-probe snapshot is not ready")
	}

	publishedBusy := false
	fake.beforeGetText = func(match, extent string) {
		if publishedBusy || match != kitty.MatchWindowByID(43) || extent != "screen" {
			return
		}
		publishedBusy = true
		h.publishSnapshot(busyWindows, base.Add(time.Second))
	}
	h.probeClaudeScreenStopWithSnapshot(base.Add(time.Second), staleSnapshot)

	if !publishedBusy {
		t.Fatal("stale idle probe never reached the injected screen read")
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("busy-title publication did not invalidate stale idle read: %+v", eff)
	}
	if ev, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, screenStopProbeEvent); ok {
		t.Fatalf("ScreenStopProbe recorded from stale idle-title snapshot: %+v", ev)
	}
}

func TestProbeClaudeContextLimit_RoutedBindingSurvivesFocusAfterStaleReadIsDiscarded(t *testing.T) {
	h := newTestHandler()
	base := time.Unix(1_700_000_000, 0)
	oldWindows := routedClaudeProbeWindows(true)
	newWindows := routedClaudeProbeWindows(false)
	fake := &fakeKittyClient{
		windows: oldWindows,
		getText: map[string]string{
			kitty.MatchWindowByID(43): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)
	seedRoutedClaudeProbe(t, h, 42, 43, "routed-focus", base.Unix())
	h.publishSnapshot(oldWindows, base)
	oldSnapshot, ready := h.cachedScreenProbeSnapshot()
	if !ready {
		t.Fatal("old cached snapshot is not ready")
	}

	changedFocus := false
	fake.beforeGetText = func(match, extent string) {
		if !changedFocus && match == kitty.MatchWindowByID(43) && extent == "screen" {
			changedFocus = true
			h.publishSnapshot(newWindows, base.Add(time.Second))
		}
	}
	h.probeClaudeContextLimitWithSnapshot(base.Add(time.Second), oldSnapshot)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("stale focus snapshot changed target to %+v, want bolt", eff)
	}

	fake.beforeGetText = nil
	newSnapshot, ready := h.cachedScreenProbeSnapshot()
	if !ready {
		t.Fatal("new cached snapshot is not ready")
	}
	h.probeClaudeContextLimitWithSnapshot(base.Add(2*time.Second), newSnapshot)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "error" {
		t.Fatalf("valid routed binding was lost after focus moved: %+v", eff)
	}
}

func TestProbeClaudeContextLimit_RoutedSourceReceiptInvalidatesObservation(t *testing.T) {
	h := newTestHandler()
	base := time.Unix(1_700_000_000, 0)
	windows := routedClaudeProbeWindows(true)
	fake := &fakeKittyClient{
		windows: windows,
		getText: map[string]string{
			kitty.MatchWindowByID(43): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)
	seedRoutedClaudeProbe(t, h, 42, 43, "routed-source-receipt", base.Unix())
	h.publishSnapshot(windows, base)
	snapshot, ready := h.cachedScreenProbeSnapshot()
	if !ready {
		t.Fatal("cached snapshot is not ready")
	}

	fake.beforeGetText = func(match, extent string) {
		if match != kitty.MatchWindowByID(43) || extent != "screen" {
			return
		}
		normalized := NormalizedEvent{
			Source:    SourceID{Kind: SourceLegacy, Key: "window:43"},
			Action:    "active",
			At:        base.Unix() + 1,
			SessionID: "source-local",
			Event:     "UserPromptSubmit",
		}
		if resp := h.handleStateUpdate(Request{
			Type:       TypeStateUpdate,
			WindowID:   43,
			Action:     "active",
			Event:      "UserPromptSubmit",
			normalized: &normalized,
		}); !resp.OK {
			t.Fatalf("source receipt update rejected: %+v", resp)
		}
	}
	h.probeClaudeContextLimitWithSnapshot(base.Add(time.Second), snapshot)

	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("source-receipt-stale observation changed target to %+v, want bolt", eff)
	}
	if _, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, claudeContextLimitProbeEvent); ok {
		t.Fatal("context-limit audit row recorded from stale source receipt")
	}
}

func routedClaudeProbeWindows(sourceActive bool) []kitty.OSWindow {
	return codexProbeSplitWindows(
		palette.BuildTitle("W:owner", "bolt", false),
		kitty.Window{
			ID:       42,
			IsActive: false,
			UserVars: kitty.UserVars{Profile: "owner", Harness: harness.Codex},
		},
		kitty.Window{
			ID:       43,
			IsActive: sourceActive,
		},
		kitty.Window{
			ID:       44,
			IsActive: !sourceActive,
		},
	)
}

func setProbeWindowTitle(t *testing.T, windows []kitty.OSWindow, windowID int, title string) {
	t.Helper()
	_, _, win, ok := kitty.FindWindowLocationByID(windows, windowID)
	if !ok {
		t.Fatalf("probe window %d not found", windowID)
	}
	win.Title = title
}

func seedRoutedClaudeProbe(t *testing.T, h *Handler, targetWindowID, sourceWindowID int, sessionID string, at int64) uint64 {
	t.Helper()
	normalized := NormalizedEvent{
		Source:         SourceID{Kind: SourceHarness, Key: harness.Claude + ":" + sessionID},
		Action:         "active",
		At:             at,
		OriginWindowID: sourceWindowID,
		Prompt:         true,
		SessionID:      sessionID,
		Event:          "UserPromptSubmit",
	}
	resp := h.handleStateUpdate(Request{
		Type:             TypeStateUpdate,
		WindowID:         targetWindowID,
		Action:           "active",
		Event:            "UserPromptSubmit",
		SessionTitle:     "W:owner",
		Account:          "owner",
		Harness:          harness.Claude,
		targetHarness:    harness.Codex,
		targetHarnessSet: true,
		normalized:       &normalized,
	})
	if !resp.OK {
		t.Fatalf("seed routed Claude session %q rejected: %+v", sessionID, resp)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[targetWindowID]
	st.Dirty = false
	st.DirtySince = time.Time{}
	return st.ShadowSeq
}
