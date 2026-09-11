package dashboard

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/layout"
)

// queueRowFixture is the widest row the vocabulary can produce: a name longer
// than the column cap, a qualifier, the longest status, and a clock. Every
// assertion below is made against it so a column that failed to bind would be
// visible rather than accidentally satisfied by a short cell.
func queueRowFixture() QueueRow {
	return QueueRow{
		Name:      "an-api-gateway-service-with-a-deliberately-long-title",
		Qualifier: "amx/feat-x",
		Status:    statusNeedsTrust,
		Emphasis:  EmphasisAttention,
		Elapsed:   "12h07",
	}
}

// rowTestWidths are the widths every responsive assertion in this file is made
// at: below the row's own floor, at it, both sides of each retired band
// boundary, and well past the point the columns stop growing. The boundaries are
// still here on purpose — the queue row is supposed to be continuous across
// them, and a row that is continuous everywhere except at 80 would pass a test
// that never looked at 80.
var rowTestWidths = []int{12, 18, 20, 36, 79, 80, 119, 120, 160, 240}

// TestQueueRowWidthArithmeticMatchesTheComposedRow is the audit allocateRow
// cannot perform on itself. The allocator sums the three columns and two gaps it
// intends to spend; the composition concatenates them separately, and nothing in
// the compiler relates the two. A gap written into the composition without a term
// in the allocation draws past the viewport edge.
//
// The claim is an inequality rather than an equality because the queue row is
// allowed to be narrower than its budget — the Session column stops growing at
// queueNameMaxWidth, so a very wide pane ends with whitespace to the right of the
// clock rather than a stretched row. What may never happen is the other
// direction, which is what rowSafetySlack buys.
func TestQueueRowWidthArithmeticMatchesTheComposedRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		q    QueueRow
	}{
		{"every column", queueRowFixture()},
		{"no qualifier", QueueRow{Name: "api", Status: statusWorking, Elapsed: "0:42"}},
		{"no clock", QueueRow{Name: "api", Status: statusIdle}},
		{"name only", QueueRow{Name: "api"}},
		{"emoji name", QueueRow{Name: "🚀 api", Status: statusWorking, Elapsed: "0:42"}},
	} {
		for _, width := range rowTestWidths {
			row := FormatRow(RowInput{Queue: tc.q, Width: width})
			got := layout.StyledWidth(row)
			if got > width-rowSafetySlack {
				t.Errorf("%s at width %d: composed row is %d cells, want at most %d",
					tc.name, width, got, width-rowSafetySlack)
			}
			if strings.Contains(row, "\n") {
				t.Errorf("%s at width %d: row wrapped: %q", tc.name, width, row)
			}
		}
	}
}

// TestQueueRowFillsItsBudgetWhileTheNameCanStillGrow is the other half of the
// inequality above. Left alone, "never wider than the budget" is satisfied by a
// renderer that draws nothing; this pins that the row is in fact spending what it
// was given, at every width where the name still has room to take it.
func TestQueueRowFillsItsBudgetWhileTheNameCanStillGrow(t *testing.T) {
	q := queueRowFixture()
	cols := ScanQueueColumns([]QueueRow{q})
	for _, width := range rowTestWidths {
		a := allocateRow(RowInput{Queue: q, Width: width, Columns: cols})
		if a.name >= cols.Name {
			continue // the Session column is at its content; a surplus is real slack
		}
		if got := layout.StyledWidth(FormatRow(RowInput{Queue: q, Width: width, Columns: cols})); got != width-rowSafetySlack {
			t.Errorf("width %d: row is %d cells with a name still short of its %d-cell content, want %d",
				width, got, cols.Name, width-rowSafetySlack)
		}
	}
}

// TestQueueRowDropsTimeBeforeStatus is the session's stated priority order,
// asserted on the rendered row rather than on the allocator: as the budget
// shrinks the clock goes first, then the status, and the name is the last thing
// standing. It walks every width down from spacious so the transitions are found
// rather than guessed at.
func TestQueueRowDropsTimeBeforeStatus(t *testing.T) {
	q := queueRowFixture()
	cols := ScanQueueColumns([]QueueRow{q})
	sawTime, sawStatusAlone, sawNameAlone := false, false, false
	for width := 160; width >= 1; width-- {
		row := stripAnsi(FormatRow(RowInput{Queue: q, Width: width, Columns: cols}))
		hasTime := strings.Contains(row, q.Elapsed)
		hasStatus := strings.Contains(row, q.Status)
		switch {
		case hasTime:
			if !hasStatus {
				t.Fatalf("width %d: row shows a time with no status to say what it is the age of: %q", width, row)
			}
			if sawStatusAlone || sawNameAlone {
				t.Fatalf("width %d: the clock came back after a narrower row had already dropped it: %q", width, row)
			}
			sawTime = true
		case hasStatus:
			if sawNameAlone {
				t.Fatalf("width %d: the status came back after a narrower row had already dropped it: %q", width, row)
			}
			sawStatusAlone = true
		default:
			sawNameAlone = true
		}
		if !strings.Contains(row, "an-api") && width >= MinRowWidth() {
			t.Fatalf("width %d: row gave up its name while it still had room: %q", width, row)
		}
	}
	if !sawTime || !sawStatusAlone || !sawNameAlone {
		t.Fatalf("the walk never observed all three stages: time=%v status-only=%v name-only=%v",
			sawTime, sawStatusAlone, sawNameAlone)
	}
}

// TestMinRowWidthIsTheNarrowestRowThatStillAnswersBothQuestions pins what the
// threshold the root's split view reads actually promises: at MinRowWidth the row
// still carries a name AND a status. One cell below it, it does not.
func TestMinRowWidthIsTheNarrowestRowThatStillAnswersBothQuestions(t *testing.T) {
	q := QueueRow{Name: "api-gateway", Status: statusWorking}
	cols := ScanQueueColumns([]QueueRow{q})

	at := stripAnsi(FormatRow(RowInput{Queue: q, Width: MinRowWidth(), Columns: cols}))
	if !strings.Contains(at, statusWorking) {
		t.Errorf("at MinRowWidth()=%d the row lost its status: %q", MinRowWidth(), at)
	}
	if !strings.Contains(at, "api") {
		t.Errorf("at MinRowWidth()=%d the row lost its name: %q", MinRowWidth(), at)
	}

	below := stripAnsi(FormatRow(RowInput{Queue: q, Width: MinRowWidth() - 1, Columns: cols}))
	if strings.Contains(below, statusWorking) {
		t.Errorf("below MinRowWidth() the status should be dropped whole: %q", below)
	}
}

// TestQueueRowTakesItsQualifierWholeOrNotAtAll pins the rule renderQueueName
// documents. A cut qualifier is worse than an absent one: its only job is to
// separate two rows that read identically, and two rows truncated to the same
// prefix separate nothing while looking as though they do.
func TestQueueRowTakesItsQualifierWholeOrNotAtAll(t *testing.T) {
	q := QueueRow{Name: "api", Qualifier: "amx/feature-x", Status: statusWorking}
	cols := ScanQueueColumns([]QueueRow{q})
	full := " (" + q.Qualifier + ")"
	for width := 1; width <= 120; width++ {
		row := stripAnsi(FormatRow(RowInput{Queue: q, Width: width, Columns: cols}))
		if !strings.Contains(row, "(") {
			continue
		}
		if !strings.Contains(row, full) {
			t.Fatalf("width %d: row carries a partial qualifier: %q", width, row)
		}
	}
}

// TestQueueColumnsScanTheWholePassRatherThanEachRow is what keeps the three
// columns readable as columns. Every row in one render pass is laid out against
// one scan, so the status of a short name and the status of a long one start on
// the same cell — which is the whole reason the operator can read the queue down
// a column instead of hunting along each line.
func TestQueueColumnsScanTheWholePassRatherThanEachRow(t *testing.T) {
	queue := []QueueRow{
		{Name: "api", Status: statusWorking, Elapsed: "0:42"},
		{Name: "an-unusually-long-session-title", Status: statusNeedsTrust, Elapsed: "12h07"},
		{Name: "🚀 web", Status: statusIdle, Elapsed: "3:01"},
	}
	cols := ScanQueueColumns(queue)
	var offsets []int
	for i, q := range queue {
		row := stripAnsi(FormatRow(RowInput{Queue: q, Width: 100, Columns: cols}))
		idx := strings.Index(row, q.Status)
		if idx < 0 {
			t.Fatalf("row %d lost its status: %q", i, row)
		}
		offsets = append(offsets, layout.Width(row[:idx]))
	}
	for i := 1; i < len(offsets); i++ {
		if offsets[i] != offsets[0] {
			t.Errorf("status column starts at cell %d on row %d and cell %d on row 0: the pass is not aligned",
				offsets[i], i, offsets[0])
		}
	}
}

// TestQueueColumnsAreBoundedByTheirCaps pins that neither operator-chosen
// content column can grow without limit. A session title has no natural bound,
// and a Session column grown to one would push the status past where the eye is
// looking; the status vocabulary is closed, so its cap is a ceiling on the scan
// rather than a truncation the operator would ever see.
func TestQueueColumnsAreBoundedByTheirCaps(t *testing.T) {
	cols := ScanQueueColumns([]QueueRow{{
		Name:      strings.Repeat("verylongtitle", 8),
		Qualifier: strings.Repeat("branch", 8),
		Status:    strings.Repeat("status", 8),
		Elapsed:   "0:42",
	}})
	if cols.Name != queueNameMaxWidth {
		t.Errorf("Name column = %d, want it capped at %d", cols.Name, queueNameMaxWidth)
	}
	if cols.Status != queueStatusMaxWidth {
		t.Errorf("Status column = %d, want it capped at %d", cols.Status, queueStatusMaxWidth)
	}
	if cols.Time != queueTimeWidth {
		t.Errorf("Time column = %d, want the fixed %d", cols.Time, queueTimeWidth)
	}

	// Every status the vocabulary can actually produce fits its cap, which is what
	// makes the cap a ceiling rather than a silent truncation. A vocabulary that
	// grew a longer phrase must widen the cap, not lose the phrase's tail.
	for _, status := range []string{
		statusFailed, statusDone, statusToolFailed, statusWorking, statusSubagent,
		statusCompacting, statusActive, statusIdle, statusNeedsPermission,
		statusNeedsEdit, statusNeedsTrust, statusNeedsInput, statusAwaitingReply,
	} {
		if w := layout.Width(status); w > queueStatusMaxWidth {
			t.Errorf("status %q is %d cells, past the %d-cell column cap", status, w, queueStatusMaxWidth)
		}
	}
	if queueStatusMinWidth != layout.Width(statusWorking) {
		t.Errorf("status floor = %d, want %q's width %d — the floor is the sentence it was sized to",
			queueStatusMinWidth, statusWorking, layout.Width(statusWorking))
	}
}

// TestQueueTimeColumnIsReservedOnlyWhenSomeRowHasAClock pins the zero-column
// case. A pass in which no row can report a time must not leave a gap where the
// clock would have been: two trailing blank cells on every line read as a column
// the operator keeps looking at for content that never arrives.
func TestQueueTimeColumnIsReservedOnlyWhenSomeRowHasAClock(t *testing.T) {
	none := ScanQueueColumns([]QueueRow{{Name: "api", Status: statusWorking}})
	if none.Time != 0 {
		t.Errorf("Time column = %d with no row reporting a clock, want it unreserved", none.Time)
	}
	row := FormatRow(RowInput{Queue: QueueRow{Name: "api", Status: statusWorking}, Width: 80, Columns: none})
	if got := stripAnsi(row); got != strings.TrimRight(got, " ") {
		t.Errorf("row reserves trailing blanks for a column nothing fills: %q", got)
	}
}

// TestQueueRowPadsAnEmojiNameToTheFixedIconCell is the alignment claim the
// project emoji rests on. A pictographic first grapheme is padded to
// layout.IconCellWidth, so the name behind it starts on the same cell whether the
// terminal measures that emoji as one cell or two — and a name with no emoji at
// all is left exactly as the operator wrote it.
func TestQueueRowPadsAnEmojiNameToTheFixedIconCell(t *testing.T) {
	withIcon := QueueRow{Name: "🚀 api", Status: statusWorking}
	plain := QueueRow{Name: "api", Status: statusWorking}

	icon := stripAnsi(FormatRow(RowInput{Queue: withIcon, Width: 80}))
	if !strings.HasPrefix(icon, layout.PadIconCell("🚀")) {
		t.Errorf("emoji row %q does not lead with the padded icon cell %q", icon, layout.PadIconCell("🚀"))
	}
	if !strings.Contains(icon, "api") {
		t.Errorf("emoji row %q lost the name behind its icon", icon)
	}
	if got := stripAnsi(FormatRow(RowInput{Queue: plain, Width: 80})); !strings.HasPrefix(got, "api") {
		t.Errorf("a name with no pictographic lead should be drawn as written, got %q", got)
	}
}

// TestQueueRowIsContinuousAcrossTheRetiredBandBoundaries is the redesign's claim
// stated directly. The row used to gain the profile cell at 80 and the branch
// column at 120; it now says the same things on both sides of both, and differs
// only in how much of the name it can afford.
func TestQueueRowIsContinuousAcrossTheRetiredBandBoundaries(t *testing.T) {
	q := queueRowFixture()
	cols := ScanQueueColumns([]QueueRow{q})
	for _, boundary := range []int{bandStandardMinWidth, bandSpaciousMinWidth} {
		below := stripAnsi(FormatRow(RowInput{Queue: q, Width: boundary - 1, Columns: cols}))
		at := stripAnsi(FormatRow(RowInput{Queue: q, Width: boundary, Columns: cols}))
		for _, want := range []string{q.Status, q.Elapsed, q.Qualifier} {
			if strings.Contains(below, want) != strings.Contains(at, want) {
				t.Errorf("crossing width %d changes whether the row carries %q: %q vs %q",
					boundary, want, below, at)
			}
		}
	}
}
