// Painting the grid.
//
// The surface is composed row by row and never by splicing a box into a canvas.
// Tiles cover the grid area edge to edge with no gutters, so a grid row is the
// horizontal concatenation of one line from each tile in that tile-row, and each
// of those lines is padded to exactly its tile's width before it is joined. That
// is what makes "a tile renders inside its assigned dimensions" a property of
// the composition rather than something the tiles have to be trusted about: a
// segment that measured wrong would have to be wrong about its own width, which
// is checked on the way out.
//
// Every segment is also SEALED — a reset appended whenever it carries an escape
// sequence at all. A capture is arbitrary bytes from somebody else's terminal,
// and an unterminated SGR in the left tile would otherwise paint the right one.
//
// Nothing here reads a clock, a config file, Kitty, or the daemon. Now, the
// sessions, and every preview are supplied.
package overview

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

const (
	// DefaultTitle and DefaultHints are what the first and last rows say when the
	// caller supplies nothing. They are package constants rather than caller
	// defaults so a surface that forgets to set them still tells an operator what
	// they are looking at and how to leave it.
	DefaultTitle = "Live sessions"
	DefaultHints = "←↑↓→/hjkl move · Tab/⇧Tab next · PgUp/PgDn page · Home/End ends"

	// tilePadding is the one cell of space between a tile's border and its
	// contents, on each side. It costs two of the tile's columns and buys the
	// difference between a card and a box with text jammed against its wall.
	tilePadding = 1

	// tileBorderCells and tileBorderRows are what the box itself costs.
	tileBorderCells = 2
	tileBorderRows  = 2

	// minBorderedWidth and minBorderedHeight are the smallest tile that can still
	// wear a box: two border columns, two padding columns, and one cell of
	// content; two border rows and one row of content. Below either, the box is
	// dropped and the preview is drawn bare, because a tile rendered as nothing
	// but its own outline says less than a truncated session name does.
	minBorderedWidth  = tileBorderCells + 2*tilePadding + 1
	minBorderedHeight = tileBorderRows + 1
)

// Preview is what one tile shows below its divider: the state of the read that
// was issued for that session, and whatever it returned.
//
// It is the caller's to supply for the reason dashboard.InspectorContent is the
// caller's: only the controller that issued the read knows whether one is
// outstanding, and an empty Output is a legitimate answer that cannot be told
// from a missing one by looking at it.
type Preview struct {
	Content dashboard.InspectorContent
	// Output is the newest snapshot, already split by dashboard.SplitOutput.
	Output []string
	// Error is the failed read's message, read only under InspectorFailed.
	Error string
}

// readyPreview is a capture that landed.
//
// An empty lines slice is a terminal with nothing in it, and it is deliberately
// NOT the same value as a missing map entry: the renderer draws the first as
// "(empty)" and the second as "(loading…)", and telling a finished read apart
// from an outstanding one is the whole reason Content is the caller's to supply.
func readyPreview(lines []string) Preview {
	return Preview{Content: dashboard.InspectorReady, Output: lines}
}

// failedPreview is a capture that did not land. It occupies its own tile and
// nothing else: a grid in which one window refused to be read still shows the
// other eight.
func failedPreview(message string) Preview {
	return Preview{Content: dashboard.InspectorFailed, Error: message}
}

// clipPreview re-cuts a stored capture to a tile that has changed size.
//
// It runs on a SHRINK, where refetching would be a read the operator cannot see
// the benefit of: the stored lines already contain everything the smaller tile
// can show, so cutting them is both the correct picture and the thing that keeps
// "what is retained is bounded by what is visible" true continuously rather than
// only until the next capture lands. Growth is the other direction and cannot be
// served from memory at all — the missing cells were never captured — so the
// scheduler refetches instead.
//
// The result never aliases the input's slice, because the caller is rebuilding a
// map it may still be sharing with an earlier model.
func clipPreview(p Preview, width, height int) Preview {
	if len(p.Output) == 0 {
		return p
	}
	if width <= 0 || height <= 0 {
		p.Output = nil
		return p
	}
	lines := p.Output
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = layout.StyledTruncate(line, width, "…")
	}
	p.Output = out
	return p
}

// outputExtent is a tile's terminal-output region: how wide one captured line is
// drawn and how many of them fit under the compact header.
//
// It lives beside renderTile because it is the same arithmetic read backwards. A
// capture is clipped to what the tile will actually draw, so a scheduler that
// computed the border's cost or the header's height for itself could clip a read
// one cell narrower than the tile renders it and leave a column of every preview
// permanently blank.
//
// Zero in either dimension means the tile has no room for output at all, which is
// true of a tile shorter than its own header. There is nothing to capture for one
// of those, and nothing is drawn there either.
func outputExtent(t Tile) (width, height int) {
	if t.Width <= 0 || t.Height <= 0 {
		return 0, 0
	}
	if t.Width < minBorderedWidth || t.Height < minBorderedHeight {
		return t.Width, max(t.Height-dashboard.CompactChromeLines, 0)
	}
	return t.Width - tileBorderCells - 2*tilePadding,
		max(t.Height-tileBorderRows-dashboard.CompactChromeLines, 0)
}

// Input is every fact the overview is drawn from.
type Input struct {
	Width, Height int

	// Sessions is the WHOLE live set, already in Order's tile order. It is the
	// whole set and not a page of it because the name disambiguator is measured
	// across all of it — a session whose name collides with one on page three
	// must carry its qualifier on page one, or the qualifier would appear and
	// disappear as the operator paged and the tile would look like a different
	// session each time.
	Sessions []session.RunningSession
	// Selected is the index into Sessions the keyboard is on. It is clamped, and
	// the page shown is derived from it.
	Selected int
	// Previews is keyed by Kitty window ID. A session with no entry is showing a
	// read that has not landed yet — InspectorLoading — which is the true state of
	// every tile on the first frame and is distinct from a read that came back
	// empty.
	Previews map[int]Preview

	Cfg *config.Config
	// LastActivity and IdleThreshold are StateGroup's inputs, passed through to
	// every tile so the grid and the dashboard bucket a session identically.
	LastActivity  map[int]time.Time
	IdleThreshold time.Duration
	// Now is the clock every elapsed time is measured against.
	Now time.Time

	// Title, Feedback, and Hints are the three reserved rows. Title and Hints
	// fall back to their package defaults; Feedback is empty until something has
	// happened worth reporting, and its row stays reserved so the grid below it
	// does not shift when it does.
	Title    string
	Feedback string
	Hints    string
}

// Render returns exactly Height lines (no trailing newline), each at most Width
// cells wide.
//
// It is the same fixed-size contract dashboard.RenderInspector keeps, and for
// the same reason: whatever is hosting the surface sized it, and a renderer that
// returned a different number of lines than it was given would move everything
// around it.
func Render(in Input) string {
	if in.Width <= 0 || in.Height <= 0 {
		return ""
	}
	g := Measure(in.Width, in.Height)
	count := len(in.Sessions)
	selected := clamp(in.Selected, 0, max(count-1, 0))

	rows := make([]string, in.Height)
	for i := range rows {
		rows[i] = ""
	}
	paintChrome(rows, in, g, count, selected)
	paintGrid(rows, in, g, count, selected)

	for i, row := range rows {
		rows[i] = fitCell(row, in.Width)
	}
	return strings.Join(rows, "\n")
}

// paintChrome fills the reserved rows, dropping them from the least useful up
// when the terminal cannot hold all three.
//
// The clipping order is title, then hints, then feedback. A surface that cannot
// say WHAT it is is unreadable; a surface that cannot say how to LEAVE it is a
// trap; a surface that cannot report the last thing that happened is merely
// quieter than it meant to be. So the feedback row is the one that goes first.
func paintChrome(rows []string, in Input, g Geometry, count, selected int) {
	title := in.Title
	if title == "" {
		title = DefaultTitle
	}
	hints := in.Hints
	if hints == "" {
		hints = DefaultHints
	}

	rows[0] = titleLine(title, g, count, selected)
	if len(rows) < 2 {
		return
	}
	rows[len(rows)-1] = styleDim.Render(hints)
	if len(rows) < 3 {
		return
	}
	if in.Feedback != "" {
		rows[1] = styleFeedback.Render(in.Feedback)
	}
}

// titleLine is the first row: what this is, how many sessions are in it, and
// which page of them is on screen.
//
// The page indicator is drawn only when there is more than one page. On a
// terminal that shows everything at once it would be a permanent `page 1/1`,
// which is a count of a thing the operator can already see all of.
func titleLine(title string, g Geometry, count, selected int) string {
	line := styleTitle.Render(title) + styleDim.Render(fmt.Sprintf("  %d", count))
	if pages := g.PageCount(count); pages > 1 {
		line += styleDim.Render(fmt.Sprintf("  ·  page %d/%d", g.PageOf(selected)+1, pages))
	}
	return line
}

// paintGrid lays the selection's page out over the reserved rows.
//
// Tiles are composed a tile-row at a time so the segments of one row can be
// concatenated at their exact widths. A slot past the end of the list is filled
// with blanks rather than an empty box: a border around nothing reads as a
// session whose preview failed, which is a thing that can actually happen and
// must not be imitated by an empty slot.
func paintGrid(rows []string, in Input, g Geometry, count, selected int) {
	if g.Capacity() == 0 || count == 0 {
		return
	}
	pageStart := g.PageStart(g.PageOf(selected))

	for row := 0; row < g.Rows; row++ {
		segments := make([][]string, 0, g.Cols)
		var tileTop, tileHeight int
		for col := 0; col < g.Cols; col++ {
			pos := row*g.Cols + col
			t, ok := g.Tile(pos)
			if !ok {
				continue
			}
			tileTop, tileHeight = t.Y, t.Height
			index := pageStart + pos
			if index >= count {
				segments = append(segments, blankTile(t))
				continue
			}
			segments = append(segments, renderTile(in, t, in.Sessions[index], index == selected))
		}
		if len(segments) == 0 {
			continue
		}
		for line := 0; line < tileHeight; line++ {
			y := tileTop + line
			if y < 0 || y >= len(rows) {
				continue
			}
			var b strings.Builder
			for _, seg := range segments {
				if line < len(seg) {
					b.WriteString(seg[line])
				}
			}
			rows[y] = b.String()
		}
	}
}

// blankTile is an unoccupied slot: exactly its tile's dimensions, in spaces.
func blankTile(t Tile) []string {
	lines := make([]string, t.Height)
	blank := strings.Repeat(" ", max(t.Width, 0))
	for i := range lines {
		lines[i] = blank
	}
	return lines
}

// renderTile draws one session inside one tile: exactly Height lines, each
// exactly Width cells.
//
// A tile too small for a box loses the box rather than the session. That is the
// same priority the compact preview keeps inside the box — identity before
// output — carried one level up: at four cells wide there is room for a
// truncated project name and nothing else, and the name is what the operator
// needs to know a session is there at all.
func renderTile(in Input, t Tile, s session.RunningSession, selected bool) []string {
	if t.Width <= 0 || t.Height <= 0 {
		return nil
	}
	preview := in.preview(s)
	body := dashboard.CompactInput{
		Content:       preview.Content,
		Session:       s,
		Qualifier:     in.qualifier(s),
		Cfg:           in.Cfg,
		LastActivity:  in.LastActivity,
		IdleThreshold: in.IdleThreshold,
		Now:           in.Now,
		Output:        preview.Output,
		Error:         preview.Error,
	}

	if t.Width < minBorderedWidth || t.Height < minBorderedHeight {
		body.Width, body.Height = t.Width, t.Height
		return cells(splitExactly(dashboard.RenderCompactPreview(body), t.Height), t.Width)
	}

	inner := t.Width - tileBorderCells - 2*tilePadding
	body.Width, body.Height = inner, t.Height-tileBorderRows
	content := splitExactly(dashboard.RenderCompactPreview(body), body.Height)

	glyphs, style := borderFor(selected)
	pad := strings.Repeat(" ", tilePadding)
	rule := strings.Repeat(glyphs.horizontal, t.Width-tileBorderCells)
	left := style.Render(glyphs.vertical)
	right := style.Render(glyphs.vertical)

	lines := make([]string, 0, t.Height)
	lines = append(lines, style.Render(glyphs.topLeft+rule+glyphs.topRight))
	for _, line := range content {
		lines = append(lines, left+pad+fitCell(line, inner)+pad+right)
	}
	lines = append(lines, style.Render(glyphs.bottomLeft+rule+glyphs.bottomRight))
	return cells(lines, t.Width)
}

// preview is the nil-safe lookup of what a session's tile is showing. An absent
// entry is a read that has not landed, which is the true state of every tile
// before the first one comes back.
func (in Input) preview(s session.RunningSession) Preview {
	if p, ok := in.Previews[s.WindowID]; ok {
		return p
	}
	return Preview{Content: dashboard.InspectorLoading}
}

// qualifier is the whole-live-set collision analysis, built here rather than
// taken from the caller so the "entire live set, including other pages" rule is
// a property of the renderer instead of an instruction to whoever calls it.
//
// It is rebuilt per tile, which is one pass over the session list per tile
// drawn. That is deliberate at this size — a page holds single-digit tiles and
// the analysis is arithmetic over a slice already in memory — and it is what
// keeps Render a pure function of Input with no cached value to invalidate when
// the session set changes under it.
func (in Input) qualifier(s session.RunningSession) string {
	return dashboard.NewNameDisambiguator(in.Sessions).Qualifier(s)
}

// splitExactly turns a renderer's output into exactly height lines, padding with
// blanks and dropping any surplus. The renderers it is given honor their height
// contract; this is the guard that keeps a future one that does not from
// shifting every tile to its right.
func splitExactly(s string, height int) []string {
	if height <= 0 {
		return nil
	}
	out := make([]string, height)
	if s == "" {
		return out
	}
	lines := strings.Split(s, "\n")
	copy(out, lines)
	return out
}

// cells normalizes every line of a tile to exactly width cells, sealed.
func cells(lines []string, width int) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = fitCell(line, width)
	}
	return out
}

// fitCell truncates a styled line to width display cells, pads it out to exactly
// that many, and seals it.
//
// The order matters. Truncation is measured in cells so a wide grapheme is never
// split and an escape sequence is never cut in half; the padding is appended
// after the content's own trailing reset so the spaces are unstyled; and the
// seal is what stops a capture's dangling SGR from reaching the next tile.
func fitCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = layout.StyledTruncate(s, width, "…")
	if w := layout.StyledWidth(s); w < width {
		s += strings.Repeat(" ", width-w)
	}
	return seal(s)
}

// seal appends a reset to a line that carries any escape sequence at all.
//
// It is unconditional on the line's content rather than a check for an already
// balanced style, because a tile's body is a capture of somebody else's terminal
// and there is no parse of it that is worth trusting. An extra reset on an
// already reset line costs four bytes and paints nothing.
func seal(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return s + "\x1b[0m"
}
