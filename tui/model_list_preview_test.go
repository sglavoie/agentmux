package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// This file is the interaction half of the Projects view: the palette entry
// that switches the arrangement, Enter's folding, and the three things that
// have to survive a rebuild — which line the cursor is on, which families are
// folded, and what the preview pane is pointed at.
//
// Both of the vocabularies it used to drive are retired. `v` switched the
// arrangement and is a palette command now; `←`/`→` folded and unfolded and are
// Enter alone, which toggles. Enter is unambiguous here because a summary is the
// one line where it has no session to focus, so the key that means "act on this
// row" already meant nothing else on the row folding applies to.
//
// Every assertion here is written against the item list rather than against a
// session index. That is the whole point of the projection: under Projects a
// line is not a session, so a test that counted sessions would agree with the
// model right up to the first fold and then quietly stop describing the screen.

// TestSummaryPreviewPaintsACard is the pane's half of the
// fail-closed rule. A summary owns no window, so there is nothing to read; the
// card answers the question the selection actually asked instead, out of facts
// already on the rows.
func TestSummaryPreviewPaintsACard(t *testing.T) {
	t.Parallel()

	m := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "summary:monorepo(open)")
	if cmd := m.refreshPreviewForCursor(); cmd != nil {
		if msg, ok := cmd().(previewContentMsg); !ok || msg.windowID != 0 {
			t.Fatalf("the summary retargeted the preview at %#v, want the empty window", msg)
		}
	}
	if m.previewWindowID != 0 {
		t.Errorf("the preview still tracks window %d on a summary", m.previewWindowID)
	}
	card := stripAnsi(renderPreviewPane(m, 60, 12))

	if !strings.Contains(card, "monorepo") {
		t.Errorf("the card never named the family:\n%s", card)
	}
	if !strings.Contains(card, "1 attention") || !strings.Contains(card, "1 active") {
		t.Errorf("the card lost its tally:\n%s", card)
	}
	for _, want := range []string{"api", "web", "/repo/api", "/repo/web", "claude", "main", "feat"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card lost %q, which is what tells two checkouts apart:\n%s", want, card)
		}
	}
	if strings.Contains(card, "git:/repo") {
		t.Errorf("the family key leaked into the card:\n%s", card)
	}
	if got, want := len(strings.Split(card, "\n")), 12; got != want {
		t.Errorf("the card rendered %d lines, want exactly the %d it was given", got, want)
	}

	// A pane too short for the members says how many it dropped rather than
	// overflowing the height the split gave it.
	short := stripAnsi(renderPreviewPane(m, 60, 4))
	if got, want := len(strings.Split(short, "\n")), 4; got != want {
		t.Fatalf("the short card rendered %d lines, want %d:\n%s", got, want, short)
	}
	if !strings.Contains(short, "more") {
		t.Errorf("the short card dropped members silently:\n%s", short)
	}

	// The whole split renders the card in place of scrollback, still without
	// issuing a session action.
	split := stripAnsi(renderSplitFrame(m).text)
	if !strings.Contains(split, "monorepo") {
		t.Errorf("the split did not paint the project card:\n%s", split)
	}
}

// TestRefreshRetargetsThePreviewAtTheSelectionItEndedOn pins the order inside a
// rebuild. A refresh can move the cursor — a row can vanish, and in Projects a
// row can be replaced by the summary standing for it — so the pane has to be
// pointed at the selection the refresh finished with, not the one it started
// from and not whichever line the cursor passed over on the way.
func TestRefreshRetargetsThePreviewAtTheSelectionItEndedOn(t *testing.T) {
	t.Parallel()

	m := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "session:api")
	m.refreshPreviewForCursor()
	if m.previewWindowID != 11 {
		t.Fatalf("setup: the preview tracks window %d, want api's 11", m.previewWindowID)
	}

	// The selected checkout goes away, which also drops its family to one
	// visible member and so shortens every line index above it. The cursor
	// index the refresh started from now names a different session entirely —
	// which is exactly the row a pane retargeted too early would show.
	m = loadSessions(m, listViewRows()[1:])
	wantLines(t, m, []string{"session:web", "session:etl", "summary:toolbox(open)", "session:cli", "session:lib"})
	if got := selectedLine(m); got != "session:web" {
		t.Fatalf("the refresh landed the cursor on %q, want session:web", got)
	}
	if m.previewWindowID != 12 {
		t.Errorf("the preview tracks window %d but the cursor ended on %q (window 12)",
			m.previewWindowID, selectedLine(m))
	}

	// And when the refresh ends on a summary — here because the family the
	// cursor was inside is folded — the pane drops its window rather than
	// keeping the one it had.
	m = loadSessions(m, listViewRows())
	m = selectLine(t, m, "summary:monorepo(open)")
	m = foldToggle(m)
	m = toggleView(t, m)
	m = selectLine(t, m, "session:api")
	m.refreshPreviewForCursor()
	if m.previewWindowID != 11 {
		t.Fatalf("setup: the preview tracks window %d, want api's 11", m.previewWindowID)
	}
	m = toggleView(t, m)
	if got := selectedLine(m); got != "summary:monorepo(folded)" {
		t.Fatalf("the fold did not swallow the selection; cursor on %q", got)
	}
	if m.previewWindowID != 0 {
		t.Errorf("the preview still tracks window %d with a summary selected", m.previewWindowID)
	}
}

// TestPreviewRejectsAResponseForAnOlderTarget is why the fetch carries a
// generation. A window ID alone cannot decide this: the cursor can leave a
// session, visit a summary that owns no window, and come back, and a read issued
// before the detour would match on window ID and repaint the pane with
// scrollback captured for an older selection.
func TestPreviewRejectsAResponseForAnOlderTarget(t *testing.T) {
	t.Parallel()

	m := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "session:api")
	m.refreshPreviewForCursor()

	stale := previewContentMsg{windowID: m.previewWindowID, targetSeq: m.previewTargetSeq, text: "scrollback for api"}
	updated, _ := m.Update(stale)
	m = updated.(Model)
	if m.previewText != "scrollback for api" {
		t.Fatalf("the current target's response was dropped; previewText = %q", m.previewText)
	}

	// Leave for the summary and come back. The window ID is the same on both
	// sides of the detour; the generation is not.
	m = selectLine(t, m, "summary:monorepo(open)")
	m.refreshPreviewForCursor()
	m = selectLine(t, m, "session:api")
	m.refreshPreviewForCursor()
	if m.previewWindowID != stale.windowID {
		t.Fatalf("the round trip left the preview on window %d, want %d", m.previewWindowID, stale.windowID)
	}
	if m.previewTargetSeq == stale.targetSeq {
		t.Fatal("the round trip reused the generation the stale read was stamped with")
	}

	updated, _ = m.Update(stale)
	m = updated.(Model)
	if m.previewText != "" {
		t.Errorf("a response for an older target repainted the pane with %q", m.previewText)
	}

	// A response for the current generation but the wrong window is refused
	// too: both halves of the identity have to match.
	wrongWindow := previewContentMsg{windowID: stale.windowID + 100, targetSeq: m.previewTargetSeq, text: "someone else's scrollback"}
	updated, _ = m.Update(wrongWindow)
	if got := updated.(Model).previewText; got != "" {
		t.Errorf("a response for another window repainted the pane with %q", got)
	}

	// And the periodic tick re-reads the SAME target under the SAME generation,
	// so a read slower than the tick interval is not superseded before it lands.
	seq := m.previewTargetSeq
	m.previewPollSeq = 1
	ticked, _ := m.handlePreviewTick(previewTickMsg{pollSeq: 1})
	if got := ticked.(Model).previewTargetSeq; got != seq {
		t.Errorf("a preview tick bumped the generation from %d to %d", seq, got)
	}
}

// TestKeyboardHelpDocumentsTheProjectsView covers what the generated half of the
// help cannot reach on its own. Folding is bound in the list handler and appears
// in no palette, so it needs a static section that a test holds in place.
func TestKeyboardHelpDocumentsTheProjectsView(t *testing.T) {
	t.Parallel()

	m := listViewModel(dashboard.ViewProjects)
	m.mode = ModeHelp
	m.height = 0
	out := stripAnsi(renderHelp(m))

	if !strings.Contains(out, "Projects view") {
		t.Fatalf("the help overlay has no Projects section:\n%s", out)
	}
	// Three surfaces that all narrow by project and must stay legible as three:
	// the switcher's own section, the filter component it writes, and the DSL
	// selector the operator can type instead.
	for _, phrase := range []string{"Project switcher", "project filter", "#project"} {
		if !strings.Contains(out, phrase) {
			t.Errorf("the help overlay does not keep %q distinct:\n%s", phrase, out)
		}
	}
	// Enter means something different on a summary than on a session, and the
	// help has to say so rather than promising a focus that will not happen.
	if !strings.Contains(out, "fold") {
		t.Errorf("the help overlay never explains folding:\n%s", out)
	}
	// And it may not still advertise the three keys the fold and the arrangement
	// used to be spelled with. `←`/`→` are gone outright; `v` is a palette
	// command, which the help documents by name rather than by letter.
	for _, retired := range []string{"←", "→"} {
		if strings.Contains(out, retired) {
			t.Errorf("the help overlay still documents the retired fold key %q:\n%s", retired, out)
		}
	}
}

// TestBuiltSummaryOffersOnlyViewActions is the availability claim across all
// three surfaces the operator can reach an action from, made against a summary
// the real builder emitted rather than a spliced one. Enter is excluded because
// on a summary it is no longer a session action at all — TestEnterFoldsOnlyOnASummary
// covers what it does instead.
func TestBuiltSummaryOffersOnlyViewActions(t *testing.T) {
	forbidDaemonMutations(t)
	m := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "summary:monorepo(open)")

	for _, key := range summaryRowActionKeys {
		if key == keyEnter {
			continue
		}
		after, cmd := m.Update(keyPress(key))
		if cmd != nil {
			t.Errorf("key %q dispatched %#v with the cursor on a built summary", key, cmd())
		}
		if got := after.(Model); got.mode != ModeList || got.targetSession.TabID != 0 {
			t.Errorf("key %q left mode %d targeting tab %d", key, got.mode, got.targetSession.TabID)
		}
	}

	ctx := captureCommandContext(m)
	if ctx.HasTarget {
		t.Fatalf("the command context claimed target %+v on a summary", ctx.Target)
	}
	for _, cmd := range commandCatalog() {
		available := cmd.AvailabilityFor(m, ctx).Available
		if cmd.Group == commands.GroupSession && available {
			t.Errorf("session command %q is available on a summary", cmd.ID)
		}
		if cmd.ID == commands.ToggleListView && !available {
			t.Error("the view command is unavailable on a summary; it is not a row action")
		}
	}

	for _, e := range listFooterBindings() {
		if e.section == footerSectionRow && e.availFn(m) {
			t.Errorf("footer entry %q (%s) is available on a summary", e.key, e.label)
		}
	}

	// The footer says what the keys actually do here instead of offering an
	// action that will refuse.
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "fold") {
		t.Errorf("the footer never mentions folding with a summary selected:\n%s", footer)
	}
}
