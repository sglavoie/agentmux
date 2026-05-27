package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// TestRenderListAt_UnboundedHeightRendersAllRowsNoIndicators verifies the
// height<=0 path: every row is rendered (matching the pre-windowing behavior)
// and neither ▲ nor ▼ indicators appear.
func TestRenderListAt_UnboundedHeightRendersAllRowsNoIndicators(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(30)
	m.listOffset = 5 // offset is ignored when height <= 0

	out := stripAnsi(renderListAt(m, m.width, 0))

	for i := 0; i < len(m.filtered); i++ {
		name := indexedRowName(i)
		if !strings.Contains(out, name) {
			t.Errorf("unbounded render missing %s; got %q", name, out)
		}
	}
	if strings.Contains(out, "▲") {
		t.Errorf("unbounded render should not emit ▲ indicator")
	}
	if strings.Contains(out, "▼") {
		t.Errorf("unbounded render should not emit ▼ indicator")
	}
}

// TestRenderListAt_OffsetZeroFullBudgetRendersAllNoIndicators covers the
// "everything fits" case: when offset=0 and the row count is well within the
// height budget, no indicators are emitted.
func TestRenderListAt_OffsetZeroFullBudgetRendersAllNoIndicators(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(5)
	m.listOffset = 0

	out := stripAnsi(renderListAt(m, m.width, 20))

	for i := 0; i < len(m.filtered); i++ {
		name := indexedRowName(i)
		if !strings.Contains(out, name) {
			t.Errorf("expected %s in full-budget render; got %q", name, out)
		}
	}
	if strings.Contains(out, "▲") || strings.Contains(out, "▼") {
		t.Errorf("no indicators expected when everything fits; got %q", out)
	}
}

// TestRenderListAt_OffsetGreaterThanZeroEmitsTopIndicator verifies that a
// non-zero listOffset skips the leading rows and prepends the ▲ indicator,
// and that the skipped rows are absent from the output.
func TestRenderListAt_OffsetGreaterThanZeroEmitsTopIndicator(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(20)
	m.listOffset = 5

	// Budget big enough that the remaining rows (15) all fit -- only the
	// top indicator should appear, not the bottom one.
	out := stripAnsi(renderListAt(m, m.width, 30))

	// The very first content line must be the ▲ indicator.
	firstLine := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(firstLine, "▲") {
		t.Errorf("expected ▲ as first line; got first line %q (full %q)", firstLine, out)
	}

	// Skipped rows must not appear.
	for i := 0; i < m.listOffset; i++ {
		name := indexedRowName(i)
		if strings.Contains(out, name) {
			t.Errorf("skipped row %s should not appear in windowed output", name)
		}
	}
	// Rows from the window onward should all appear (budget is generous).
	for i := m.listOffset; i < len(m.filtered); i++ {
		name := indexedRowName(i)
		if !strings.Contains(out, name) {
			t.Errorf("expected windowed row %s; got %q", name, out)
		}
	}
}

// TestRenderListAt_OverflowEmitsBottomIndicator verifies that when more rows
// remain past the visible window the renderer reserves a line and emits ▼.
func TestRenderListAt_OverflowEmitsBottomIndicator(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(40)
	m.listOffset = 0

	out := stripAnsi(renderListAt(m, m.width, 8))
	trimmed := strings.TrimRight(out, "\n")
	lines := strings.Split(trimmed, "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "▼") {
		t.Errorf("expected ▼ as last line; got last line %q (full %q)", last, out)
	}
	if strings.Contains(out, "▲") {
		t.Errorf("offset=0 should not emit ▲ indicator; got %q", out)
	}
	// The total emitted lines (including any indicator) must not exceed the
	// passed-in height budget.
	if len(lines) > 8 {
		t.Errorf("renderer exceeded budget: emitted %d lines for budget 8", len(lines))
	}
}

// TestRenderListAt_GroupLabelEmittedAtWindowStart verifies that a window that
// begins inside group 1 still renders the group-1 label as its first row
// (renderListAt's walk initializes prevGroup=-1 at startIdx).
func TestRenderListAt_GroupLabelEmittedAtWindowStart(t *testing.T) {
	m := scrollModel()
	m.filtered = []session.RunningSession{
		{WindowID: 1, Base: "attn1", Account: "w", State: "stop"}, // group 0
		{WindowID: 2, Base: "attn2", Account: "w", State: "stop"}, // group 0
		{WindowID: 3, Base: "act1", Account: "w", State: "bolt"},  // group 1
		{WindowID: 4, Base: "act2", Account: "w", State: "bolt"},  // group 1
		{WindowID: 5, Base: "act3", Account: "w", State: "bolt"},  // group 1
	}
	m.lastActivity = map[int]time.Time{}
	// Offset places the window squarely inside group 1.
	m.listOffset = 3

	out := stripAnsi(renderListAt(m, m.width, 30))

	// Group label should still be present even though the window begins
	// mid-group: this locks in the prevGroup=-1 initializer.
	if !strings.Contains(out, "Active (") {
		t.Errorf("expected Active group label at window start; got %q", out)
	}
	// The "Needs Attention" label belongs to the skipped group 0 rows and
	// should not appear.
	if strings.Contains(out, "Needs Attention") {
		t.Errorf("group-0 label must not appear when window starts inside group 1; got %q", out)
	}
}
