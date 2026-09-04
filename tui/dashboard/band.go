// This file holds the one question every responsive decision in the dashboard
// asks first: how much room is there, in the coarse sense?
//
// It is deliberately a classifier and nothing else. It reads a width and names
// a band; it does not know what a row is, what chrome costs, or which fields a
// band drops. Keeping it pure is what lets the row allocator, the selected-item
// detail strip, the legend gate, and the footer all be responsive to the *same*
// three presets instead of each rediscovering the boundaries with its own
// inequality — the failure mode where a pane calls itself narrow while the
// footer under it still thinks it is wide.
//
// Which width to hand it is the other half of the contract, and the two halves
// are not the same number. Row bands take the width of the list pane, because a
// row is drawn inside that pane and a horizontal preview split can leave a wide
// terminal with a narrow list. Root chrome — the legend, the footer, the header
// — takes the terminal width, because that is the surface it is drawn on.

package dashboard

// Band is a coarse width preset. Every responsive decision in the dashboard is
// keyed by one of these three rather than by a bare column count, so a change to
// the boundaries lands in one place.
type Band int

const (
	// BandCompact keeps the operational prefix and a title and drops the rest.
	BandCompact Band = iota
	// BandStandard is the historical dashboard layout: profile initial,
	// harness cell, fixed-ish title, then the optional tail columns.
	BandStandard
	// BandSpacious spends its surplus on identity — the full profile name and
	// a title grown to its content — before it widens the directory.
	BandSpacious
)

const (
	// bandStandardMinWidth and bandSpaciousMinWidth are the approved band
	// boundaries: compact below 80, standard through 119, spacious at 120 and
	// above. They are half-open on the left, so 80 is the first standard width
	// and 120 the first spacious one.
	bandStandardMinWidth = 80
	bandSpaciousMinWidth = 120
)

// BandFor classifies a width.
//
// A non-positive width is standard, not compact. Zero means "not measured yet"
// — a caller that has seen no WindowSizeMsg, or a direct FormatRow call with no
// budget — and such a caller wants the historical unbounded row, not the
// stripped-down one it would get if absence were read as scarcity.
func BandFor(width int) Band {
	switch {
	case width <= 0:
		return BandStandard
	case width < bandStandardMinWidth:
		return BandCompact
	case width < bandSpaciousMinWidth:
		return BandStandard
	default:
		return BandSpacious
	}
}

// String names the band for diagnostics and test failure messages.
func (b Band) String() string {
	switch b {
	case BandCompact:
		return "compact"
	case BandSpacious:
		return "spacious"
	default:
		return "standard"
	}
}
