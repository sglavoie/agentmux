package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestRenderHelpNoHeightShowsAll(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))

	for _, header := range []string{"Navigate", "Act on row", "Launch / open", "Filter", "App", "Past Sessions", "Plans"} {
		if !strings.Contains(out, header) {
			t.Errorf("expected section header %q in help output, got:\n%s", header, out)
		}
	}
	if !strings.Contains(out, "footer overflow") {
		t.Errorf("expected footer overflow line in help output, got:\n%s", out)
	}
}

func TestRenderHelpReflectsDashboardRemap(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	out := stripAnsi(renderHelp(m))

	for _, frag := range []string{
		":         open command palette",
		"x         clear session icon",
		"X         close session (confirm)",
		"C         launch new session with profile selection",
		"f         cycle profile (dashboard live / launcher default)",
		"o         open session directory in a plain shell tab",
		"e         peek scrollback (active rows only)",
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("expected help output to contain %q, got:\n%s", frag, out)
		}
	}
	for _, stale := range []string{
		"x         close session (confirm)",
		"C         clear session icon",
		"F         run an agent-session-loop plan",
		"t         tail active agent-session-loop run",
		// Past Sessions / Plans bindings now come from the footer palettes;
		// the static duplicates must not come back.
		"i         copy selected past session ID",
		"Y         copy current transcript/plan path in search/view modes",
		"cycle plan sort order",
		"open the plan in $EDITOR",
		"handoff selected past session to another profile",
		"also work inside the transcript viewer",
	} {
		if strings.Contains(out, stale) {
			t.Errorf("expected stale help hint %q to be absent, got:\n%s", stale, out)
		}
	}
}

// helpPaletteLineFor returns the generated help line for key, identified by the
// padded key column helpPaletteLines renders.
func helpPaletteLineFor(lines []string, key string) (string, bool) {
	prefix := fmt.Sprintf("  %-10s", key)
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return line, true
		}
	}
	return "", false
}

// TestHelpDocumentsEveryPaletteKey verifies the generated Past Sessions and
// Plans sections document every key of the four mode palettes exactly once.
// Narrow terminals drop the low-priority footer hints, so the overlay is the
// only place those keymaps are guaranteed to be complete.
func TestHelpDocumentsEveryPaletteKey(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))

	for _, sec := range helpPaletteSections {
		var keys []string
		seen := make(map[string]bool)
		for _, palette := range sec.palettes {
			for _, e := range palette.entries {
				if seen[e.key] {
					continue
				}
				seen[e.key] = true
				keys = append(keys, e.key)
			}
		}

		lines := helpPaletteLines(sec.palettes)
		if len(lines) != len(keys) {
			t.Errorf("%s: expected one line per distinct key (%d), got %d: %v", sec.name, len(keys), len(lines), lines)
		}
		for _, key := range keys {
			line, ok := helpPaletteLineFor(lines, key)
			if !ok {
				t.Errorf("%s: expected a generated line for key %q, got %v", sec.name, key, lines)
				continue
			}
			if !strings.Contains(out, line) {
				t.Errorf("%s: expected overlay to render %q, got:\n%s", sec.name, line, out)
			}
		}
	}
}

// TestHelpPreservesModeSpecificMeaning pins F-002: when a section's search and
// viewer palettes label the same key differently, the generated help must keep
// both meanings and name the scope each belongs to. Dropping either one would
// tell the user the viewer key does something it does not.
func TestHelpPreservesModeSpecificMeaning(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))

	divergent := 0
	for _, sec := range helpPaletteSections {
		declared := make(map[string][]helpKeyMeaning)
		var keys []string
		for _, palette := range sec.palettes {
			for _, e := range palette.entries {
				if _, ok := declared[e.key]; !ok {
					keys = append(keys, e.key)
				}
				declared[e.key] = append(declared[e.key], helpKeyMeaning{palette.scope, e.label})
			}
		}

		lines := helpPaletteLines(sec.palettes)
		for _, key := range keys {
			distinct := make(map[string]bool, len(declared[key]))
			var meanings []string
			for _, d := range declared[key] {
				distinct[d.label] = true
				meanings = append(meanings, fmt.Sprintf("%s (%s)", d.label, d.scope))
			}
			if len(distinct) < 2 {
				continue
			}
			divergent++

			line, ok := helpPaletteLineFor(lines, key)
			if !ok {
				t.Errorf("%s: expected a generated line for key %q, got %v", sec.name, key, lines)
				continue
			}
			for _, meaning := range meanings {
				if !strings.Contains(line, meaning) {
					t.Errorf("%s: key %q line %q dropped the meaning %q", sec.name, key, line, meaning)
				}
			}
			if !strings.Contains(out, line) {
				t.Errorf("%s: expected overlay to render %q, got:\n%s", sec.name, line, out)
			}
		}
	}
	if divergent == 0 {
		t.Fatal("expected at least one key whose search and viewer labels differ; the guard is inert otherwise")
	}

	// The three meanings F-002 reported as suppressed by first-label-wins.
	for _, want := range []string{
		"enter     view (search) · view message (viewer)",
		"/         filter (search) · search (viewer)",
		"j/k       up/down (search) · scroll (viewer)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected overlay to render %q, got:\n%s", want, out)
		}
	}
}

// TestHelpScopesModeExclusiveKeys pins F-002: a key only one palette of a
// combined section declares must name the scope that declares it, or the
// overlay advertises a command that silently does nothing in the sibling mode.
// Keys every palette declares with the same label must stay bare so scoping the
// exclusives does not turn the common case into noise.
func TestHelpScopesModeExclusiveKeys(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))

	exclusive, shared := 0, 0
	for _, sec := range helpPaletteSections {
		declared := make(map[string][]helpKeyMeaning)
		var keys []string
		for _, palette := range sec.palettes {
			for _, e := range palette.entries {
				if _, ok := declared[e.key]; !ok {
					keys = append(keys, e.key)
				}
				declared[e.key] = append(declared[e.key], helpKeyMeaning{palette.scope, e.label})
			}
		}

		lines := helpPaletteLines(sec.palettes)
		for _, key := range keys {
			line, ok := helpPaletteLineFor(lines, key)
			if !ok {
				t.Errorf("%s: expected a generated line for key %q, got %v", sec.name, key, lines)
				continue
			}
			if !strings.Contains(out, line) {
				t.Errorf("%s: expected overlay to render %q, got:\n%s", sec.name, line, out)
			}

			if len(declared[key]) < len(sec.palettes) {
				exclusive++
				for _, d := range declared[key] {
					want := fmt.Sprintf("%s (%s)", d.label, d.scope)
					if !strings.Contains(line, want) {
						t.Errorf("%s: key %q is declared only by %s but line %q does not scope it as %q", sec.name, key, d.scope, line, want)
					}
				}
				continue
			}

			distinct := make(map[string]bool, len(declared[key]))
			for _, d := range declared[key] {
				distinct[d.label] = true
			}
			if len(distinct) > 1 {
				continue
			}
			shared++
			if want := fmt.Sprintf("  %-10s%s", key, declared[key][0].label); line != want {
				t.Errorf("%s: key %q means the same thing in every mode; want the concise %q, got %q", sec.name, key, want, line)
			}
		}
	}
	if exclusive == 0 || shared == 0 {
		t.Fatalf("expected both mode-exclusive (%d) and identically-shared (%d) keys; the guard is inert otherwise", exclusive, shared)
	}

	// The four mode-exclusive keys F-002 named as rendered without a scope.
	for _, want := range []string{
		"d         delete (search)",
		"o         sort (search)",
		"n/N       match (viewer)",
		"y         copy (viewer)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected overlay to render %q, got:\n%s", want, out)
		}
	}
}

// contextualHelpModel seeds a Past Sessions / Plans model with the search and
// viewer state a help detour must leave untouched.
func contextualHelpModel(mode Mode) Model {
	meta := viewerActionMeta()
	plan := plans.PlanMeta{Name: "plan-a", DirPath: "/tmp/plan-a"}
	return Model{
		mode:             mode,
		width:            80,
		height:           40,
		cfg:              viewerActionConfig(),
		sessionQuery:     "worktree",
		sessionResults:   []transcript.Result{{Meta: meta}},
		sessionCursor:    0,
		viewerSession:    &meta,
		viewerMessages:   []transcript.Message{{Role: "user", Text: "hello"}, {Role: "assistant", Text: "hi"}},
		viewerCursor:     1,
		planQuery:        "review",
		planResults:      []plans.Result{{Meta: plan}},
		viewerPlan:       &plan,
		planViewerLines:  []string{"--- plan.md ---", "body"},
		planViewerCursor: 1,
	}
}

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

func TestRenderHelpClipsToVisible(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 12
	m.helpOffset = 0

	out := stripAnsi(renderHelp(m))

	if strings.Contains(out, "App") {
		t.Errorf("expected last-section header %q to be clipped at height=12, got:\n%s", "App", out)
	}
	if !strings.Contains(out, "▼") {
		t.Errorf("expected scroll-down indicator at height=12 with offset=0, got:\n%s", out)
	}
}

func TestRenderHelpIndicatorsAtTopAndBottom(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 12

	m.helpOffset = 0
	topOut := stripAnsi(renderHelp(m))
	if strings.Contains(topOut, "▲") {
		t.Errorf("expected no scroll-up indicator at offset=0, got:\n%s", topOut)
	}

	total, visible := helpScrollBounds(m, helpVisibleHeight(m))
	m.helpOffset = total - visible
	if m.helpOffset < 0 {
		m.helpOffset = 0
	}
	bottomOut := stripAnsi(renderHelp(m))
	if strings.Contains(bottomOut, "▼") {
		t.Errorf("expected no scroll-down indicator at max offset, got:\n%s", bottomOut)
	}
	if !strings.Contains(bottomOut, "▲") {
		t.Errorf("expected scroll-up indicator at max offset, got:\n%s", bottomOut)
	}
}

func TestHelpScrollKeys(t *testing.T) {
	base := func() Model {
		m := loadSessions(testModel(), makeSessions())
		m.mode = ModeHelp
		m.height = 12
		return m
	}

	total, visible := helpScrollBounds(base(), helpVisibleHeight(base()))
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}

	t.Run("j advances by 1", func(t *testing.T) {
		m := base()
		m = pressKey(m, "j")
		if m.helpOffset != 1 {
			t.Fatalf("expected helpOffset=1 after j, got %d", m.helpOffset)
		}
	})

	t.Run("k clamps at 0", func(t *testing.T) {
		m := base()
		m = pressKey(m, "k")
		if m.helpOffset != 0 {
			t.Fatalf("expected helpOffset=0 after k from 0, got %d", m.helpOffset)
		}
	})

	t.Run("g jumps to 0", func(t *testing.T) {
		m := base()
		m.helpOffset = maxOffset
		m = pressKey(m, "g")
		if m.helpOffset != 0 {
			t.Fatalf("expected helpOffset=0 after g, got %d", m.helpOffset)
		}
	})

	t.Run("G jumps to max", func(t *testing.T) {
		m := base()
		m = pressKey(m, "G")
		if m.helpOffset != maxOffset {
			t.Fatalf("expected helpOffset=%d after G, got %d", maxOffset, m.helpOffset)
		}
	})

	t.Run("j clamps at max", func(t *testing.T) {
		m := base()
		m.helpOffset = maxOffset
		m = pressKey(m, "j")
		if m.helpOffset != maxOffset {
			t.Fatalf("expected helpOffset to stay at %d, got %d", maxOffset, m.helpOffset)
		}
	})
}

// TestHelpScrollBoundsCountWrappedRows verifies that at a narrow width the
// scroll bounds count wrapped display rows, not logical lines. Fix #5: the box
// wraps each help entry to its content width, so total must exceed the logical
// line count or the modal renders taller than the visible window budgeted for
// it (the modal_height_overflow the harness caught at W=30 H=40).
func TestHelpScrollBoundsCountWrappedRows(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.width = 30
	m.height = 40

	logical := len(helpContentLines())
	wrapped := len(helpWrappedContentLines(m))
	if wrapped <= logical {
		t.Fatalf("expected wrapped row count (%d) to exceed logical line count (%d) at width 30", wrapped, logical)
	}

	total, _ := helpScrollBounds(m, helpVisibleHeight(m))
	if total != wrapped {
		t.Fatalf("expected helpScrollBounds total (%d) to equal wrapped row count (%d)", total, wrapped)
	}
}

// TestHelpScrollReachesBottom verifies that pressing G at a narrow width scrolls
// to the last wrapped help row so the bottom of the overlay is reachable. With
// the pre-fix logical-line scroll math the offset stopped short and the footer
// overflow footnote stayed clipped off-screen.
func TestHelpScrollReachesBottom(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 40})
	m = updated.(Model)

	m = pressKey(m, "G")

	total, visible := helpScrollBounds(m, helpVisibleHeight(m))
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.helpOffset != maxOffset {
		t.Fatalf("expected G to scroll to max offset %d, got %d", maxOffset, m.helpOffset)
	}

	// The final help row (footer-overflow footnote) must render within the
	// visible window once scrolled to the bottom.
	out := stripAnsi(renderHelp(m))
	if !strings.Contains(out, "least-frequent") {
		t.Errorf("expected the last help row to be visible after G, got:\n%s", out)
	}
}

// TestHelpOffsetClampsOnResize verifies a viewport expansion drops
// m.helpOffset into the new [0, maxOffset] range. When the terminal grows
// taller, more content fits on-screen and maxOffset shrinks; without the
// clamp the persisted offset would still point past the bottom of the
// scrollable region and j/k would have to scroll up through dead rows.
func TestHelpOffsetClampsOnResize(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp

	// Small viewport: maxOffset is large; scroll to bottom.
	m.height = 12
	total, visible := helpScrollBounds(m, helpVisibleHeight(m))
	maxOffset := total - visible
	if maxOffset <= 0 {
		t.Skipf("help content too short at height=12 (maxOffset=%d)", maxOffset)
	}
	m.helpOffset = maxOffset

	// Grow the viewport — visible expands, maxOffset shrinks. The stale
	// helpOffset now sits past the bottom of the scroll region.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	m2 := updated.(Model)

	total2, visible2 := helpScrollBounds(m2, helpVisibleHeight(m2))
	maxOffset2 := total2 - visible2
	if maxOffset2 < 0 {
		maxOffset2 = 0
	}
	if maxOffset2 >= maxOffset {
		t.Skipf("resize did not shrink maxOffset (was %d, now %d) — nothing to clamp", maxOffset, maxOffset2)
	}
	if m2.helpOffset > maxOffset2 {
		t.Fatalf("expected helpOffset clamped to <= %d after resize, got %d", maxOffset2, m2.helpOffset)
	}
	if m2.helpOffset < 0 {
		t.Errorf("expected helpOffset non-negative after resize, got %d", m2.helpOffset)
	}

	// After clamping, pressing k must decrement by exactly 1 — confirming
	// the offset is in sync with the renderer.
	if m2.helpOffset == 0 {
		return
	}
	before := m2.helpOffset
	m3 := pressKey(m2, "k")
	if m3.helpOffset != before-1 {
		t.Errorf("expected k to decrement to %d, got %d", before-1, m3.helpOffset)
	}
}
