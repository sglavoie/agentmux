package dashboard

// HitRect is a semantic region measured in terminal cells relative to the
// renderer that produced it. Coordinates are zero-based.
type HitRect struct {
	X, Y          int
	Width, Height int
}

// ListHit names one selectable list line and the chevron cell when that line
// is a family summary. Identity is captured from the same item projection that
// produced the rendered row.
type ListHit struct {
	Rect       HitRect
	Identity   CursorIdentity
	ChevronX   int
	HasChevron bool
	Collapsed  bool
}

// ListRender is the hit-aware form of RenderList.
type ListRender struct {
	Text string
	Hits []ListHit
}

// ChipHit names one rendered filter component.
type ChipHit struct {
	Rect  HitRect
	Index int
	Label string
}

// ChipRender is the hit-aware form of RenderChips.
type ChipRender struct {
	Text string
	Hits []ChipHit
}
