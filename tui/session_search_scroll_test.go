package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// sessionSearchScrollFixture returns a Model with `rows` synthetic session
// results and a viewport sized so the visible budget is strictly smaller
// than the row count. It fatals when that constraint is broken so future
// chrome-math drift surfaces immediately.
func sessionSearchScrollFixture(t *testing.T, rows, height int) Model {
	t.Helper()
	results := make([]transcript.Result, rows)
	for i := range results {
		results[i] = transcript.Result{
			Meta: transcript.SessionMeta{
				ID:      indexedRowName(i),
				Project: indexedRowName(i),
			},
		}
	}
	m := Model{
		mode:           ModeSessionSearch,
		width:          100,
		height:         height,
		sessionResults: results,
		cfg:            &config.Config{},
	}
	if visible := sessionSearchVisibleHeight(m); visible >= rows {
		t.Fatalf("sessionSearchScrollFixture: visible budget %d not smaller than rows %d; tighten the height", visible, rows)
	}
	return m
}

func TestScrollSessionSearchToCursor_CursorRemainsRenderedAtBottomEdge(t *testing.T) {
	// Bug repro: pressing j past the last visible row left the cursor one
	// row below the rendered window, because the scroll budget did not
	// reserve a row for the ▼ indicator that renderSessionSearchList emits
	// when more items follow.
	m := sessionSearchScrollFixture(t, 40, 15)
	visible := sessionSearchVisibleHeight(m)

	for step := 0; step < visible+5; step++ {
		m.sessionCursor = step
		scrollSessionSearchToCursor(&m)

		out := stripAnsi(renderSessionSearchList(m, visible))
		cursorLine := indexedRowName(m.sessionCursor)
		if !strings.Contains(out, cursorLine) {
			t.Fatalf("cursor row %q dropped from render at step %d (cursor=%d offset=%d visible=%d)\nrender:\n%s",
				cursorLine, step, m.sessionCursor, m.sessionOffset, visible, out)
		}
	}
}

func TestScrollSessionSearchToCursor_LastRowVisible(t *testing.T) {
	m := sessionSearchScrollFixture(t, 40, 15)
	visible := sessionSearchVisibleHeight(m)

	m.sessionCursor = len(m.sessionResults) - 1
	scrollSessionSearchToCursor(&m)

	out := stripAnsi(renderSessionSearchList(m, visible))
	last := indexedRowName(len(m.sessionResults) - 1)
	if !strings.Contains(out, last) {
		t.Fatalf("last row %q not rendered (offset=%d visible=%d)\n%s", last, m.sessionOffset, visible, out)
	}
	// No ▼ indicator should be present when the cursor sits on the final
	// row — there is nothing past it to scroll to.
	if strings.Contains(out, "▼") {
		t.Errorf("did not expect ▼ when cursor is on the last row\n%s", out)
	}
}
