package tui

import (
	"slices"
	"strings"
	"testing"

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

// TestEnterFoldsOnlyOnASummary walks what is left of the fold vocabulary now
// that it is one key. The interesting part used to be the asymmetry between Left
// on a summary and Left on a child; with a toggle there is no asymmetry to have,
// so the claims that matter are that the key toggles, that it does so without
// targeting a terminal, and that on every line which is not a summary it is
// FocusSession and nothing else.
func TestEnterFoldsOnlyOnASummary(t *testing.T) {
	t.Parallel()

	t.Run("enter toggles the selected family without targeting a session", func(t *testing.T) {
		m := listViewModel(dashboard.ViewProjects)
		m = selectLine(t, m, "summary:monorepo(open)")

		m = foldToggle(m)
		wantLines(t, m, []string{
			"summary:monorepo(folded)", "session:etl",
			"summary:toolbox(open)", "session:cli", "session:lib",
		})
		if got := selectedLine(m); got != "summary:monorepo(folded)" {
			t.Errorf("folding moved the cursor to %q", got)
		}

		m = foldToggle(m)
		wantLines(t, m, projectLines)
		if got := selectedLine(m); got != "summary:monorepo(open)" {
			t.Errorf("unfolding moved the cursor to %q", got)
		}
	})

	t.Run("enter on a child is a focus, not a fold of its family", func(t *testing.T) {
		m := listViewModel(dashboard.ViewProjects)
		m = selectLine(t, m, "session:web")

		after := foldToggle(m)
		wantLines(t, after, projectLines) // nothing folded
		if len(after.collapsedFamilies) != 0 {
			t.Errorf("enter on a child folded %v", after.collapsedFamilies)
		}
		if got := selectedLine(after); got != "session:web" {
			t.Errorf("enter on a child moved the cursor to %q", got)
		}
	})

	t.Run("the retired arrow keys fold nothing", func(t *testing.T) {
		// `←`/`→` were the fold and unfold keys. They are retired outright rather
		// than rebound, so on a summary — the one line they used to act on — they
		// must now change neither the list nor the cursor.
		for _, view := range []dashboard.ViewMode{dashboard.ViewProjects, dashboard.ViewUrgency} {
			m := listViewModel(view)
			line := "summary:monorepo(open)"
			if view == dashboard.ViewUrgency {
				line = "session:api"
			}
			m = selectLine(t, m, line)
			before, cursor := itemLines(m), m.cursor

			for _, key := range []string{keyLeft, keyRight} {
				after := pressKey(m, key)
				wantLines(t, after, before)
				if after.cursor != cursor {
					t.Errorf("view %v: %q moved the cursor to %q", view, key, selectedLine(after))
				}
				if len(after.collapsedFamilies) != 0 {
					t.Errorf("view %v: %q folded %v", view, key, after.collapsedFamilies)
				}
			}
		}
	})

	t.Run("a top-level leaf has no family to fold", func(t *testing.T) {
		m := listViewModel(dashboard.ViewProjects)
		m = selectLine(t, m, "session:etl")
		before, cursor := itemLines(m), m.cursor

		after := foldToggle(m)
		wantLines(t, after, before)
		if after.cursor != cursor {
			t.Errorf("enter moved the cursor off the unfamilied leaf to %q", selectedLine(after))
		}
		if len(after.collapsedFamilies) != 0 {
			t.Errorf("enter folded %v from an unfamilied leaf", after.collapsedFamilies)
		}
	})

	t.Run("the urgency view has nothing to fold", func(t *testing.T) {
		m := listViewModel(dashboard.ViewUrgency)
		m = selectLine(t, m, "session:api")
		after := foldToggle(m)
		wantLines(t, after, urgencyLines)
		if len(after.collapsedFamilies) != 0 {
			t.Errorf("enter folded %v in the urgency view", after.collapsedFamilies)
		}
	})
}

// TestFoldsSurviveRefreshFilterAndViewSwitch is the memory claim. A fold is a
// choice about the list, not about the projection that happened to be in the
// model when it was made, so every rebuild has to carry it — otherwise the next
// two-second topology refresh silently unfolds the screen.
func TestFoldsSurviveRefreshFilterAndViewSwitch(t *testing.T) {
	t.Parallel()

	m := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "summary:monorepo(open)")
	m = foldToggle(m)
	folded := []string{
		"summary:monorepo(folded)", "session:etl",
		"summary:toolbox(open)", "session:cli", "session:lib",
	}
	wantLines(t, m, folded)

	// A refresh delivering the same topology.
	m = loadSessions(m, listViewRows())
	wantLines(t, m, folded)
	if got := selectedLine(m); got != "summary:monorepo(folded)" {
		t.Errorf("the refresh moved the cursor to %q", got)
	}

	// A filter that hides the folded family entirely, then its removal. The
	// fold outlives the family's absence from the list.
	filtered := m
	cmd := filtered.applyFilterChange(dashboard.Parse("tools"))
	_ = cmd
	for _, line := range itemLines(filtered) {
		if strings.Contains(line, "monorepo") {
			t.Fatalf("the filter left %q in the list %v", line, itemLines(filtered))
		}
	}
	filtered.applyFilterChange(dashboard.Filter{})
	wantLines(t, filtered, folded)

	// And a round trip through the urgency view, which draws no summaries at
	// all and so has nowhere to record a fold except the map.
	m = toggleView(t, m)
	wantLines(t, m, urgencyLines)
	m = toggleView(t, m)
	wantLines(t, m, folded)
}

// TestFoldingCopiesTheCollapseMap guards the aliasing hazard behind that memory.
// Model is copied by value on every Update, so a fold written into a shared map
// would reach into every copy that still holds it — including the pre-fold model
// a test, or an in-flight command, is holding.
func TestFoldingCopiesTheCollapseMap(t *testing.T) {
	t.Parallel()

	before := listViewModel(dashboard.ViewProjects)
	before = selectLine(t, before, "summary:monorepo(open)")

	after := foldToggle(before)
	if !after.collapsedFamilies["git:/repo"] {
		t.Fatal("the fold did not reach the model that pressed the key")
	}
	if before.collapsedFamilies["git:/repo"] {
		t.Error("the fold reached back into the model held before the keypress")
	}

	// The second fold is the one that can alias: by then the map exists, so a
	// write in place would land in every copy that shares it rather than only in
	// the one the update loop is about to return.
	second := selectLine(t, after, "summary:toolbox(open)")
	second = foldToggle(second)
	if !second.collapsedFamilies["git:/tools"] {
		t.Fatal("the second fold did not reach the model that pressed the key")
	}
	if after.collapsedFamilies["git:/tools"] {
		t.Error("the second fold reached back into the model held before the keypress")
	}
	if got := itemLines(after); !slices.Equal(got, []string{
		"summary:monorepo(folded)", "session:etl",
		"summary:toolbox(open)", "session:cli", "session:lib",
	}) {
		t.Errorf("the earlier model's own projection changed under it: %v", got)
	}

	// Unfolding deletes rather than storing false, so an unfolded family leaves
	// no entry claiming anything about it.
	restored := foldToggle(selectLine(t, after, "summary:monorepo(folded)"))
	if _, present := restored.collapsedFamilies["git:/repo"]; present {
		t.Errorf("unfolding left %v behind; absent must mean expanded", restored.collapsedFamilies)
	}
}
