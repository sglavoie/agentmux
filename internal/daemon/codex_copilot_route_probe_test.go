package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestProbeCodexScreenPrompts_RoutedPromptReadsProducerAndPreservesTargetOwner(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	windows := routedHarnessProbeWindows("bolt", harness.Claude, true)
	fake := &fakeKittyClient{
		windows: windows,
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexWorkingTurnScreen,
			kitty.MatchWindowByID(43): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)
	seedRoutedHarnessProbe(t, h, 42, 43, harness.Codex, harness.Claude, "codex-prompt", base.Unix(), "active")

	var reads []string
	fake.beforeGetText = func(match, extent string) {
		if extent == "screen" {
			reads = append(reads, match)
		}
	}
	h.ProbeCodexScreenPrompts(base.Add(time.Second))

	if len(reads) != 1 || reads[0] != kitty.MatchWindowByID(43) {
		t.Fatalf("screen reads = %+v, want only routed producer pane 43", reads)
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "question" || visible != "question" {
		t.Fatalf("target effective = %+v visible %q, want question/question", eff, visible)
	}
	assertRoutedProbeOwnership(t, h, 42, harness.Claude)
}

func TestProbeCodexScreenPrompts_RoutedPromptSurvivesUnrelatedTabPublication(t *testing.T) {
	h := newTestHandler()
	base := time.Unix(1_700_000_000, 0)
	windows := routedCodexProbeWithEarlierTab(
		palette.BuildTitle("W:earlier", "bolt", false),
		palette.BuildTitle("W:owner", "bolt", false),
	)
	fake := &fakeKittyClient{
		windows: windows,
		getText: map[string]string{
			kitty.MatchWindowByID(41): codexWorkingTurnScreen,
			kitty.MatchWindowByID(43): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 41, "earlier", base.Unix())
	seedRoutedHarnessProbe(t, h, 42, 43, harness.Codex, harness.Claude, "katana", base.Unix(), "active")
	h.publishSnapshot(windows, base)
	snapshot, ready := h.cachedScreenProbeSnapshot()
	if !ready {
		t.Fatal("cached screen-probe snapshot is not ready")
	}

	var reads []string
	republished := false
	fake.beforeGetText = func(match, extent string) {
		if extent != "screen" {
			return
		}
		reads = append(reads, match)
		if match == kitty.MatchWindowByID(41) && !republished {
			republished = true
			h.publishSnapshot(
				routedCodexProbeWithEarlierTab(
					palette.BuildTitle("W:earlier", "question", false),
					palette.BuildTitle("W:owner", "bolt", false),
				),
				base.Add(time.Second),
			)
		}
	}
	h.probeCodexScreenPromptsWithSnapshot(base.Add(2*time.Second), snapshot, h.screenTextCacheFor())

	if !republished {
		t.Fatal("earlier tab did not publish a competing snapshot")
	}
	if len(reads) != 2 ||
		reads[0] != kitty.MatchWindowByID(41) ||
		reads[1] != kitty.MatchWindowByID(43) {
		t.Fatalf("screen reads = %+v, want earlier pane 41 then routed Katana source pane 43", reads)
	}
	if eff, _ := windowEffective(t, h, 41); eff.Icon != "bolt" {
		t.Fatalf("stale earlier-tab observation changed its state to %+v, want bolt", eff)
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "question" || visible != "question" {
		t.Fatalf("Katana target effective = %+v visible %q, want question/question", eff, visible)
	}
	ev, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, screenPromptProbeEvent)
	if !ok || ev.PromptKind != string(CodexPromptRequestUserInput) || ev.OriginWindowID != 43 {
		t.Fatalf("Katana routed prompt event = %+v ok=%v, want request_user_input from origin 43", ev, ok)
	}
}

// TestProbeCodexScreenPrompts_RoutedPromptSurvivesSiblingPaneChurn is the
// within-tab counterpart of the unrelated-tab case above: an ordinary shell
// command in the plain split must not invalidate a probe already reading the
// routed producer pane beside it.
func TestProbeCodexScreenPrompts_RoutedPromptSurvivesSiblingPaneChurn(t *testing.T) {
	h := newTestHandler()
	base := time.Unix(1_700_000_000, 0)
	windows := routedCodexProbeWithPlainSibling(
		palette.BuildTitle("W:owner", "bolt", false),
		[]string{"/bin/ls", "-la"},
	)
	fake := &fakeKittyClient{
		windows: windows,
		getText: map[string]string{
			kitty.MatchWindowByID(43): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)
	seedRoutedHarnessProbe(t, h, 42, 43, harness.Codex, harness.Claude, "katana", base.Unix(), "active")
	h.publishSnapshot(windows, base)
	snapshot, ready := h.cachedScreenProbeSnapshot()
	if !ready {
		t.Fatal("cached screen-probe snapshot is not ready")
	}

	republished := false
	fake.beforeGetText = func(match, extent string) {
		if extent != "screen" || match != kitty.MatchWindowByID(43) || republished {
			return
		}
		republished = true
		h.publishSnapshot(
			routedCodexProbeWithPlainSibling(
				palette.BuildTitle("W:owner", "bolt", false),
				[]string{"/usr/bin/rg", "needle"},
			),
			base.Add(time.Second),
		)
	}
	h.probeCodexScreenPromptsWithSnapshot(base.Add(2*time.Second), snapshot, h.screenTextCacheFor())

	if !republished {
		t.Fatal("plain sibling pane did not publish a competing snapshot")
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "question" || visible != "question" {
		t.Fatalf("routed target effective = %+v visible %q, want question/question", eff, visible)
	}
	ev, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, screenPromptProbeEvent)
	if !ok || ev.PromptKind != string(CodexPromptRequestUserInput) || ev.OriginWindowID != 43 {
		t.Fatalf("routed prompt event = %+v ok=%v, want request_user_input from origin 43", ev, ok)
	}
}

func routedCodexProbeWithPlainSibling(routedTitle string, plainForeground []string) []kitty.OSWindow {
	return []kitty.OSWindow{{
		ID:        1,
		IsActive:  true,
		IsFocused: true,
		Tabs: []kitty.Tab{{
			ID:       100,
			IsActive: true,
			Title:    routedTitle,
			Windows: []kitty.Window{
				{
					ID:       42,
					IsActive: false,
					UserVars: kitty.UserVars{Profile: "owner", Harness: harness.Claude},
				},
				{
					ID:       43,
					IsActive: true,
					ForegroundProcesses: []kitty.ForegroundProcess{{
						Cmdline: []string{"/opt/homebrew/bin/codex"},
					}},
				},
				{
					ID:                  44,
					ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: plainForeground}},
				},
			},
		}},
	}}
}

func routedCodexProbeWithEarlierTab(earlierTitle, routedTitle string) []kitty.OSWindow {
	return []kitty.OSWindow{{
		ID:        1,
		IsActive:  true,
		IsFocused: true,
		Tabs: []kitty.Tab{
			{
				ID:    99,
				Title: earlierTitle,
				Windows: []kitty.Window{{
					ID:       41,
					IsActive: true,
					UserVars: kitty.UserVars{Profile: "earlier", Harness: harness.Codex},
				}},
			},
			{
				ID:    100,
				Title: routedTitle,
				Windows: []kitty.Window{
					{
						ID:       42,
						IsActive: false,
						UserVars: kitty.UserVars{Profile: "owner", Harness: harness.Claude},
					},
					{
						ID:       43,
						IsActive: true,
						ForegroundProcesses: []kitty.ForegroundProcess{{
							Cmdline: []string{"/opt/homebrew/bin/codex"},
						}},
					},
				},
			},
		},
	}}
}

func TestProbeCodexScreenPrompts_RoutedClearReadsProducerAndClearsTarget(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	windows := routedHarnessProbeWindows("stop", harness.Claude, true)
	fake := &fakeKittyClient{
		windows: windows,
		getTextByExtent: map[getTextKey]string{
			{match: kitty.MatchWindowByID(43), extent: "screen"}: codexReportedClearedScreen,
			{match: kitty.MatchWindowByID(43), extent: "all"}:    codexReportedClearedScreen,
		},
	}
	h.SetKittyClient(fake)
	seedRoutedHarnessProbe(t, h, 42, 43, harness.Codex, harness.Claude, "codex-clear", base.Unix(), "active")
	seedRoutedHarnessProbe(t, h, 42, 43, harness.Codex, harness.Claude, "codex-clear", base.Unix()+1, "stop")

	var reads []getTextKey
	fake.beforeGetText = func(match, extent string) {
		reads = append(reads, getTextKey{match: match, extent: extent})
	}
	h.ProbeCodexScreenPrompts(base.Add(2 * time.Second))

	if len(reads) != 2 || reads[0].match != kitty.MatchWindowByID(43) || reads[1].match != kitty.MatchWindowByID(43) {
		t.Fatalf("clear reads = %+v, want screen+all from routed producer pane 43", reads)
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "" || visible != "" {
		t.Fatalf("target effective = %+v visible %q, want cleared", eff, visible)
	}
	if ev, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, screenClearProbeEvent); !ok || ev.Action != "user_clear" {
		t.Fatalf("target clear event = %+v ok=%v, want routed user_clear", ev, ok)
	}
	assertRoutedProbeOwnership(t, h, 42, harness.Claude)
}

func TestProbeCodexScreenPrompts_RoutedIdleReadsProducerAndSettlesTarget(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	windows := routedHarnessProbeWindows("bolt", harness.Copilot, true)
	fake := &fakeKittyClient{
		windows: windows,
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexWorkingTurnScreen,
			kitty.MatchWindowByID(43): codexCompletedTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	seedRoutedHarnessProbe(t, h, 42, 43, harness.Codex, harness.Copilot, "codex-idle", base.Unix(), "active")

	var reads []string
	fake.beforeGetText = func(match, extent string) {
		if extent == "screen" {
			reads = append(reads, match)
		}
	}
	probeCodexUntilSettled(t, h, base.Add(time.Second))

	// One capture per tick, shared: the prompt classifier and the idle classifier
	// both read that tick's single scrape of the routed producer pane, and
	// neither ever touches the managed target pane 42. The stabilization run is
	// what makes it more than one read here — the count still says exactly one
	// per pass.
	if len(reads) != codexIdleProbeTerminalRun {
		t.Fatalf("screen reads = %+v, want one per probe pass over a run of %d", reads, codexIdleProbeTerminalRun)
	}
	for i, match := range reads {
		if match != kitty.MatchWindowByID(43) {
			t.Fatalf("screen read %d = %q, want routed producer pane 43", i, match)
		}
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
		t.Fatalf("target effective = %+v visible %q, want stop/stop", eff, visible)
	}
	assertRoutedProbeOwnership(t, h, 42, harness.Copilot)
}

func TestProbeCopilotTranscriptStop_RoutedProducerSettlesAndRecoversManagedTarget(t *testing.T) {
	home := t.TempDir()
	writeCopilotTranscript(t, home, "copilot-route", copilotTurnEndTranscript)
	h := newTestHandler()
	h.SetCopilotTranscriptHomes([]string{home}, "")
	base := time.Now()
	windows := routedHarnessProbeWindows("bolt", harness.Codex, true)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)
	seedRoutedHarnessProbe(t, h, 42, 43, harness.Copilot, harness.Codex, "copilot-route", base.Unix(), "active")

	h.ProbeCopilotTranscriptStop(base.Add(time.Second))
	h.ProbeCopilotTranscriptStop(base.Add(time.Second))

	if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
		t.Fatalf("target effective = %+v visible %q, want routed stop/stop", eff, visible)
	}
	assertRoutedProbeOwnership(t, h, 42, harness.Codex)
	h.stateMu.Lock()
	_, sourceHasState := h.store[43]
	h.stateMu.Unlock()
	if sourceHasState {
		t.Fatal("routed Copilot producer unexpectedly acquired managed reducer state")
	}

	appendCopilotTranscript(t, home, "copilot-route", `{"type":"assistant.turn_start","data":{"turnId":"next"}}`+"\n")
	h.ProbeCopilotTranscriptStop(base.Add(2 * time.Second))
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "bolt" || visible != "bolt" {
		t.Fatalf("target effective = %+v visible %q, want routed recovery to bolt/bolt", eff, visible)
	}
	assertRoutedProbeOwnership(t, h, 42, harness.Codex)
}

func TestProbeCopilotTranscriptStop_RoutedSourceReceiptInvalidatesBinding(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	windows := routedHarnessProbeWindows("bolt", harness.Codex, true)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)
	seedRoutedHarnessProbe(t, h, 42, 43, harness.Copilot, harness.Codex, "copilot-stale-source", base.Unix(), "active")
	h.publishSnapshot(windows, base)
	snapshot, ready := h.cachedScreenProbeSnapshot()
	if !ready {
		t.Fatal("cached snapshot is not ready")
	}

	normalized := NormalizedEvent{
		Source:    SourceID{Kind: SourceLegacy, Key: "window:43"},
		Action:    "active",
		At:        base.Unix() + 1,
		SessionID: "source-local",
		Event:     "UserPromptSubmit",
	}
	if resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 43, Action: "active", Event: "UserPromptSubmit", normalized: &normalized}); !resp.OK {
		t.Fatalf("source receipt update rejected: %+v", resp)
	}
	readReached := false
	h.copilotLifecycleReader = func(string) (transcript.CopilotLifecycleEvidence, error) {
		readReached = true
		return transcript.CopilotLifecycleEvidence{State: transcript.CopilotLifecycleTerminal}, nil
	}
	h.probeCopilotTranscriptStopWithSnapshot(base.Add(2*time.Second), snapshot)

	if readReached {
		t.Fatal("stale routed source receipt reached Copilot transcript I/O")
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("stale routed binding changed target to %+v, want bolt", eff)
	}
}

func routedHarnessProbeWindows(iconState, targetHarness string, sourceActive bool) []kitty.OSWindow {
	return codexProbeSplitWindows(
		palette.BuildTitle("W:owner", iconState, false),
		kitty.Window{
			ID:       42,
			IsActive: false,
			UserVars: kitty.UserVars{Profile: "owner", Harness: targetHarness},
		},
		kitty.Window{ID: 43, IsActive: sourceActive},
		kitty.Window{ID: 44, IsActive: !sourceActive},
	)
}

func seedRoutedHarnessProbe(
	t *testing.T,
	h *Handler,
	targetWindowID int,
	sourceWindowID int,
	producerHarness string,
	targetHarness string,
	sessionID string,
	at int64,
	action string,
) uint64 {
	t.Helper()
	event := "UserPromptSubmit"
	prompt := true
	endsSession := false
	if action == "stop" {
		event = "Stop"
		prompt = false
		endsSession = true
	}
	normalized := NormalizedEvent{
		Source:         SourceID{Kind: SourceHarness, Key: producerHarness + ":" + sessionID},
		Action:         action,
		At:             at,
		OriginWindowID: sourceWindowID,
		Prompt:         prompt,
		EndsSession:    endsSession,
		SessionID:      sessionID,
		Event:          event,
	}
	resp := h.handleStateUpdate(Request{
		Type:             TypeStateUpdate,
		WindowID:         targetWindowID,
		Action:           action,
		Event:            event,
		SessionTitle:     "W:owner",
		Account:          "owner",
		Harness:          producerHarness,
		targetHarness:    targetHarness,
		targetHarnessSet: true,
		normalized:       &normalized,
	})
	if !resp.OK {
		t.Fatalf("seed routed %s session %q action %q rejected: %+v", producerHarness, sessionID, action, resp)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[targetWindowID]
	st.Dirty = false
	st.DirtySince = time.Time{}
	return st.ShadowSeq
}

func assertRoutedProbeOwnership(t *testing.T, h *Handler, windowID int, wantHarness string) {
	t.Helper()
	h.stateMu.Lock()
	meta := h.windowMetadata[windowID]
	lastHarness := h.store[windowID].LastHarness
	h.stateMu.Unlock()
	if meta.Durable.Harness != wantHarness || meta.Durable.Profile != "owner" || lastHarness != wantHarness {
		t.Fatalf("target ownership changed: durable=%+v LastHarness=%q; want %s/owner", meta.Durable, lastHarness, wantHarness)
	}
}
