package dashboard

import (
	"strings"
	"testing"
)

func TestBandForBoundariesAndUnmeasuredWidth(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  Band
	}{
		{1, BandCompact},
		{36, BandCompact},
		{79, BandCompact},
		{80, BandStandard},
		{119, BandStandard},
		{120, BandSpacious},
		{160, BandSpacious},
		// A width nothing has measured yet is not a narrow terminal; it is no
		// terminal. Classifying it as scarcity would make the first paint drop
		// fields it has no reason to drop.
		{0, BandStandard},
		{-4, BandStandard},
	} {
		if got := BandFor(tc.width); got != tc.want {
			t.Errorf("BandFor(%d) = %s, want %s", tc.width, got, tc.want)
		}
	}
}

// TestBandsNoLongerReachTheSessionRow is the negative half of the queue
// redesign, and it is a test rather than a comment because the regression it
// guards is silent: a future band-keyed branch inside the row would restore the
// step function the queue exists to remove, and every other assertion in the
// package would still pass. FormatRow takes no band at all now — the type system
// carries most of this — so what is left to pin is that the row's OUTPUT does not
// change in kind across a boundary, which TestQueueRowIsContinuousAcrossTheRetiredBandBoundaries
// asserts, and that the surviving callers are chrome rather than rows.
//
// The band remains a real classifier for the footer and the legend; this file
// keeps its boundary test above for them.
func TestBandsNoLongerReachTheSessionRow(t *testing.T) {
	q := QueueRow{Name: "api-gateway-service", Status: statusWorking, Elapsed: "0:42"}
	cols := ScanQueueColumns([]QueueRow{q})

	// Same tokens at every width that can pay for them: what a band used to decide
	// is now decided by the row's own budget, so two widths in different bands that
	// afford the same columns produce the same row.
	wide := stripAnsi(FormatRow(RowInput{Queue: q, Width: 160, Columns: cols}))
	standard := stripAnsi(FormatRow(RowInput{Queue: q, Width: 100, Columns: cols}))
	compact := stripAnsi(FormatRow(RowInput{Queue: q, Width: 60, Columns: cols}))
	for _, want := range []string{"api-gateway-service", statusWorking, "0:42"} {
		for name, row := range map[string]string{"spacious": wide, "standard": standard, "compact": compact} {
			if !strings.Contains(row, want) {
				t.Errorf("%s row dropped %q: %q", name, want, row)
			}
		}
	}
}
