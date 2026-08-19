package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// TestContextualHelpRoundTrip pins the contract the `? more` footer marker
// advertises: every mode that renders a truncatable palette opens the overlay
// with `?`, and closing it lands back in that mode with its state intact.
func TestContextualHelpRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		mode    Mode
		palette []footerEntry
		verify  func(t *testing.T, m Model)
	}{
		{
			name: "session search", mode: ModeSessionSearch, palette: sessionSearchFooterPalette,
			verify: func(t *testing.T, m Model) {
				if m.sessionQuery != "worktree" || len(m.sessionResults) != 1 {
					t.Fatalf("search state = query %q results %d, want the seeded query and result", m.sessionQuery, len(m.sessionResults))
				}
			},
		},
		{
			name: "session view", mode: ModeSessionView, palette: sessionViewFooterPalette,
			verify: func(t *testing.T, m Model) {
				if m.viewerSession == nil || m.viewerSession.ID != "viewer-source" || m.viewerCursor != 1 {
					t.Fatalf("viewer state = session %+v cursor %d, want the seeded transcript at row 1", m.viewerSession, m.viewerCursor)
				}
			},
		},
		{
			name: "plan search", mode: ModePlanSearch, palette: planSearchFooterPalette,
			verify: func(t *testing.T, m Model) {
				if m.planQuery != "review" || len(m.planResults) != 1 {
					t.Fatalf("plan search state = query %q results %d, want the seeded query and result", m.planQuery, len(m.planResults))
				}
			},
		},
		{
			name: "plan view", mode: ModePlanView, palette: planViewFooterPalette,
			verify: func(t *testing.T, m Model) {
				if m.viewerPlan == nil || m.viewerPlan.Name != "plan-a" || m.planViewerCursor != 1 {
					t.Fatalf("plan viewer state = plan %+v cursor %d, want the seeded plan at row 1", m.viewerPlan, m.planViewerCursor)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := pressKey(contextualHelpModel(tc.mode), "?")
			if opened.mode != ModeHelp || opened.helpReturnMode != tc.mode {
				t.Fatalf("? = mode %v return %v, want the help overlay returning to %v", opened.mode, opened.helpReturnMode, tc.mode)
			}

			for _, closeKey := range []string{"?", "esc", "q"} {
				closed := pressKey(opened, closeKey)
				if closed.mode != tc.mode {
					t.Fatalf("%s = mode %v, want the originating mode %v", closeKey, closed.mode, tc.mode)
				}
				tc.verify(t, closed)
			}

			// Truncation is unavoidable at width 20, so the marker the user
			// sees must name the key exercised above.
			out := hintText(footerHintLines(Model{width: 20, mode: tc.mode}, tc.palette, footerHintMaxLines))
			if !strings.Contains(out, footerHintMore) {
				t.Fatalf("truncated %s footer = %q, want the %q marker", tc.name, out, footerHintMore)
			}
		})
	}
}

// TestHelpOverlayDimsOriginatingView verifies the backdrop behind the overlay
// is the view `?` was pressed in, not the dashboard, so the detour never looks
// like it left the transcript viewer or plan pager.
func TestHelpOverlayDimsOriginatingView(t *testing.T) {
	m := pressKey(contextualHelpModel(ModeSessionView), "?")
	out := stripAnsi(m.render())
	if !strings.Contains(out, "Keyboard Shortcuts") {
		t.Fatalf("expected the help overlay, got:\n%s", out)
	}
	if !strings.Contains(out, "Transcript:") {
		t.Fatalf("expected the dimmed transcript view behind the overlay, got:\n%s", out)
	}
	if strings.Contains(out, "No running sessions.") {
		t.Fatalf("expected no dashboard backdrop behind the overlay, got:\n%s", out)
	}
}

// overlayLoadViewerModel enters the transcript viewer from a seeded search so a
// read is genuinely in flight, and returns the result that read will deliver.
// Overlay races have to be exercised against a pending load; a model seeded
// with messages already present proves nothing.
func overlayLoadViewerModel(t *testing.T) (Model, viewerMessagesLoadedMsg) {
	t.Helper()
	meta := viewerActionMeta()
	base := Model{
		mode:              ModeSessionSearch,
		width:             80,
		height:            40,
		cfg:               viewerActionConfig(),
		sessionResults:    []transcript.Result{{Meta: meta}},
		transcriptReaders: map[string]transcript.Reader{harness.Claude: transcript.NewClaudeReader()},
	}
	updated, cmd := base.enterSessionView(meta)
	m := updated.(Model)
	if !m.viewerLoading || cmd == nil {
		t.Fatalf("enterSessionView = loading %v cmd %v, want an in-flight transcript read", m.viewerLoading, cmd != nil)
	}
	return m, viewerMessagesLoadedMsg{
		seq:      m.viewerLoadSeq,
		messages: []transcript.Message{{Role: "user", Text: "hello"}, {Role: "assistant", Text: "hi"}},
	}
}

// overlayLoadPlanViewerModel mirrors overlayLoadViewerModel for the plan pager.
func overlayLoadPlanViewerModel(t *testing.T) (Model, planFilesLoadedMsg) {
	t.Helper()
	plan := plans.PlanMeta{Name: "plan-a", DirPath: "/tmp/plan-a"}
	base := Model{
		mode:        ModePlanSearch,
		width:       80,
		height:      40,
		cfg:         viewerActionConfig(),
		planResults: []plans.Result{{Meta: plan}},
	}
	updated, cmd := base.enterPlanView(plan)
	m := updated.(Model)
	if !m.planViewerLoading || cmd == nil {
		t.Fatalf("enterPlanView = loading %v cmd %v, want an in-flight file read", m.planViewerLoading, cmd != nil)
	}
	return m, planFilesLoadedMsg{
		seq:   m.planViewerLoadSeq,
		lines: []string{"--- plan.md ---", "body"},
	}
}

// TestHelpOverlayInFlightLoadsReachUnderlyingView pins F-001 for the `?`
// detour. Help replaces the active mode but returns to the view it was raised
// from, so a load completing behind it belongs to that view: dropping the
// result leaves the viewer spinning forever, and dropping the initial plan
// cache also skips the source refresh the warm rows are only a preview of.
func TestHelpOverlayInFlightLoadsReachUnderlyingView(t *testing.T) {
	t.Run("transcript", func(t *testing.T) {
		m, loaded := overlayLoadViewerModel(t)

		help := pressKey(m, "?")
		if help.mode != ModeHelp || help.helpReturnMode != ModeSessionView {
			t.Fatalf("? = mode %v return %v, want help raised over the transcript viewer", help.mode, help.helpReturnMode)
		}

		updated, _ := help.Update(loaded)
		during := updated.(Model)
		if during.mode != ModeHelp || during.viewerLoading || len(during.viewerMessages) != len(loaded.messages) {
			t.Fatalf("load behind help = mode %v loading %v messages %d, want the transcript applied under the overlay", during.mode, during.viewerLoading, len(during.viewerMessages))
		}

		closed := pressKey(during, "esc")
		if closed.mode != ModeSessionView || closed.viewerLoading || len(closed.viewerMessages) != len(loaded.messages) {
			t.Fatalf("after help = mode %v loading %v messages %d, want the loaded transcript", closed.mode, closed.viewerLoading, len(closed.viewerMessages))
		}
	})

	t.Run("plan files", func(t *testing.T) {
		m, loaded := overlayLoadPlanViewerModel(t)

		help := pressKey(m, "?")
		if help.mode != ModeHelp || help.helpReturnMode != ModePlanView {
			t.Fatalf("? = mode %v return %v, want help raised over the plan viewer", help.mode, help.helpReturnMode)
		}

		updated, _ := help.Update(loaded)
		during := updated.(Model)
		if during.planViewerLoading || len(during.planViewerLines) != len(loaded.lines) {
			t.Fatalf("load behind help = loading %v lines %d, want the plan applied under the overlay", during.planViewerLoading, len(during.planViewerLines))
		}

		closed := pressKey(during, "esc")
		if closed.mode != ModePlanView || closed.planViewerLoading || len(closed.planViewerLines) != len(loaded.lines) {
			t.Fatalf("after help = mode %v loading %v lines %d, want the loaded plan", closed.mode, closed.planViewerLoading, len(closed.planViewerLines))
		}
	})

	t.Run("plan cache still starts the source refresh", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		planRoot := t.TempDir()
		seedPlanDir(t, planRoot, "warm-aabb112233cc", map[string]string{
			"manifest.md": "# Warm plan\n\nfilesystem cache row",
		})
		seed, err := plans.LoadIndex()
		if err != nil {
			t.Fatal(err)
		}
		if err := seed.Refresh(planRoot); err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{}
		cfg.Defaults.PlansDirectory = planRoot

		updated, loadCmd := Model{mode: ModeList, width: 80, height: 40, cfg: cfg}.enterPlanSearch()
		m := updated.(Model)
		if loadCmd == nil || !m.planCacheLoadInFlight {
			t.Fatalf("enterPlanSearch = cmd %v cache-loading %v, want an in-flight cache load", loadCmd != nil, m.planCacheLoadInFlight)
		}

		help := pressKey(m, "?")
		if help.mode != ModeHelp || help.helpReturnMode != ModePlanSearch {
			t.Fatalf("? = mode %v return %v, want help raised over the plan search", help.mode, help.helpReturnMode)
		}

		loaded, ok := loadCmd().(planCacheLoadDoneMsg)
		if !ok || loaded.idx == nil || len(loaded.results) != 1 {
			t.Fatalf("cache load = %T index %p results %d, want one warm row", loaded, loaded.idx, len(loaded.results))
		}
		updated, refreshCmd := help.Update(loaded)
		warm := updated.(Model)
		if refreshCmd == nil || len(warm.planResults) != 1 {
			t.Fatalf("cache load behind help = refresh %v results %d, want the warm rows plus a source refresh", refreshCmd != nil, len(warm.planResults))
		}

		// The refresh chain must run through to published search results, not
		// just start: the warm rows are a preview of the source scan.
		updated, searchCmd := warm.Update(plansRefreshDoneMsg{seq: warm.planSearchSeq})
		refreshed := updated.(Model)
		if searchCmd == nil {
			t.Fatal("refresh did not dispatch the follow-up search")
		}
		done, ok := searchCmd().(planSearchDoneMsg)
		if !ok {
			t.Fatalf("follow-up search returned %T, want planSearchDoneMsg", done)
		}
		updated, _ = refreshed.Update(done)
		published := updated.(Model)
		if published.planLoading || len(published.planResults) != 1 {
			t.Fatalf("published search = loading %v results %d, want the source rows", published.planLoading, len(published.planResults))
		}

		closed := pressKey(published, "esc")
		if closed.mode != ModePlanSearch || len(closed.planResults) != 1 {
			t.Fatalf("after help = mode %v results %d, want the plan search with its rows", closed.mode, len(closed.planResults))
		}
	})
}

// TestOverlayLoadResultsSurviveViewerDetours pins F-001 for the viewer's own
// overlays. The worktree confirmation and the handoff profile picker both
// return to the transcript viewer, so a read completing behind either must
// still be there when the user comes back.
func TestOverlayLoadResultsSurviveViewerDetours(t *testing.T) {
	for _, tc := range []struct {
		name    string
		open    string
		overlay Mode
		dismiss string
	}{
		{name: "worktree confirmation", open: "w", overlay: ModeConfirm, dismiss: "n"},
		{name: "handoff picker", open: "H", overlay: ModeConfigPicker, dismiss: "esc"},
		{name: "handoff edit picker", open: "E", overlay: ModeConfigPicker, dismiss: "esc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, loaded := overlayLoadViewerModel(t)

			opened := pressKey(m, tc.open)
			if opened.mode != tc.overlay {
				t.Fatalf("%s = mode %v, want %v", tc.open, opened.mode, tc.overlay)
			}

			updated, _ := opened.Update(loaded)
			during := updated.(Model)
			if during.mode != tc.overlay || during.viewerLoading {
				t.Fatalf("load behind %s = mode %v loading %v, want the overlay with the transcript applied", tc.name, during.mode, during.viewerLoading)
			}

			back := pressKey(during, tc.dismiss)
			if back.mode != ModeSessionView || back.viewerLoading || len(back.viewerMessages) != len(loaded.messages) {
				t.Fatalf("return from %s = mode %v loading %v messages %d, want the loaded transcript", tc.name, back.mode, back.viewerLoading, len(back.viewerMessages))
			}
		})
	}
}

// TestOverlayLoadGuardsStillRejectStaleResults verifies the overlay-aware
// ownership check did not weaken the two guards it replaced: a result arriving
// after the viewer was truly left, or after a newer request superseded it, is
// still dropped.
func TestOverlayLoadGuardsStillRejectStaleResults(t *testing.T) {
	t.Run("after leaving the viewer", func(t *testing.T) {
		m, loaded := overlayLoadViewerModel(t)

		left := pressKey(m, "esc")
		if left.mode != ModeSessionSearch {
			t.Fatalf("esc = mode %v, want ModeSessionSearch", left.mode)
		}
		updated, _ := left.Update(loaded)
		got := updated.(Model)
		if len(got.viewerMessages) != 0 || got.viewerSession != nil {
			t.Fatalf("stale load repopulated the closed viewer: messages %d session %+v", len(got.viewerMessages), got.viewerSession)
		}
	})

	t.Run("under help raised over another view", func(t *testing.T) {
		m, loaded := overlayLoadViewerModel(t)

		help := pressKey(pressKey(m, "esc"), "?")
		if help.mode != ModeHelp || help.helpReturnMode != ModeSessionSearch {
			t.Fatalf("? = mode %v return %v, want help raised over the search", help.mode, help.helpReturnMode)
		}
		updated, _ := help.Update(loaded)
		got := updated.(Model)
		if len(got.viewerMessages) != 0 {
			t.Fatalf("stale load repopulated a viewer help does not return to: %+v", got.viewerMessages)
		}
	})

	t.Run("superseded by a newer viewer request", func(t *testing.T) {
		m, stale := overlayLoadViewerModel(t)

		updated, _ := pressKey(m, "esc").enterSessionView(viewerActionMeta())
		fresh := updated.(Model)
		if fresh.viewerLoadSeq == stale.seq {
			t.Fatal("second viewer entry reused the first request's sequence")
		}

		help := pressKey(fresh, "?")
		updated, _ = help.Update(stale)
		got := updated.(Model)
		if !got.viewerLoading || len(got.viewerMessages) != 0 {
			t.Fatalf("superseded load = loading %v messages %d, want the newer request still pending", got.viewerLoading, len(got.viewerMessages))
		}
	})
}
