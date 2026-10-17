package sendform

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"

	"github.com/sglavoie/agentmux/tui/layout"
)

// Draft formats without its text, including inside a model or operation dump.
type Draft string

func (d Draft) String() string   { return fmt.Sprintf("sendform.Draft{bytes:%d}", len(d)) }
func (d Draft) GoString() string { return d.String() }

// Drafts is process-local storage. Formatting it never exposes prompt text.
// Pointer values keep recursive formatting of an enclosing model opaque too:
// fmt does not invoke String methods on a model's unexported fields.
type Drafts struct{ values map[string]*string }

func (d Drafts) String() string   { return fmt.Sprintf("sendform.Drafts{count:%d}", len(d.values)) }
func (d Drafts) GoString() string { return d.String() }
func (d Drafts) Get(id string) string {
	if value := d.values[id]; value != nil {
		return *value
	}
	return ""
}
func (d *Drafts) Put(id, text string) {
	if text == "" {
		delete(d.values, id)
		return
	}
	if d.values == nil {
		d.values = make(map[string]*string)
	}
	d.values[id] = &text
}

// Insert treats pasted control characters as text input, never as commands.
func (s *State) Insert(text string) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r != '\n' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	s.Cursor = min(max(s.Cursor, 0), len(s.Draft))
	s.Draft = s.Draft[:s.Cursor] + text + s.Draft[s.Cursor:]
	s.Cursor += len(text)
	s.Confirmed = false
}

func boundaries(text string) []int {
	out := []int{0}
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		_, end := g.Positions()
		out = append(out, end)
	}
	return out
}

func (s *State) previous() int {
	prev := 0
	for _, pos := range boundaries(s.Draft) {
		if pos >= s.Cursor {
			break
		}
		prev = pos
	}
	return prev
}
func (s *State) next() int {
	for _, pos := range boundaries(s.Draft) {
		if pos > s.Cursor {
			return pos
		}
	}
	return len(s.Draft)
}

// Edit handles only editor keys; the form owns submit, dismissal and focus.
func (s *State) Edit(key string, width int) bool {
	s.Cursor = min(max(s.Cursor, 0), len(s.Draft))
	switch key {
	case "left":
		s.Cursor = s.previous()
	case "right":
		s.Cursor = s.next()
	case "home", "ctrl+a":
		s.Cursor = strings.LastIndex(s.Draft[:s.Cursor], "\n") + 1
	case "end", "ctrl+e":
		if n := strings.IndexByte(s.Draft[s.Cursor:], '\n'); n >= 0 {
			s.Cursor += n
		} else {
			s.Cursor = len(s.Draft)
		}
	case "ctrl+home":
		s.Cursor = 0
	case "ctrl+end":
		s.Cursor = len(s.Draft)
	case "up":
		s.moveVertical(-1, width)
	case "down":
		s.moveVertical(1, width)
	case "pgup":
		s.moveVertical(-editorLines, width)
	case "pgdown":
		s.moveVertical(editorLines, width)
	case "backspace":
		start := s.previous()
		s.Draft = s.Draft[:start] + s.Draft[s.Cursor:]
		s.Cursor = start
		s.Confirmed = false
	case "delete", "ctrl+d":
		s.Draft = s.Draft[:s.Cursor] + s.Draft[s.next():]
		s.Confirmed = false
	case "ctrl+w", "alt+backspace":
		end := s.Cursor
		points := boundaries(s.Draft[:end])
		i := len(points) - 1
		for i > 0 && strings.TrimSpace(s.Draft[points[i-1]:points[i]]) == "" {
			i--
		}
		for i > 0 && strings.TrimSpace(s.Draft[points[i-1]:points[i]]) != "" {
			i--
		}
		s.Cursor = points[i]
		s.Draft = s.Draft[:s.Cursor] + s.Draft[end:]
		s.Confirmed = false
	case "enter":
		s.Insert("\n")
	default:
		return false
	}
	return true
}

type editorRow struct{ start, end int }

// Rows retain byte offsets while wrapping whole graphemes. One cell is reserved
// for the caret, including when it follows a full-width line.
func editorRows(text string, width int) []editorRow {
	width = max(1, width-1)
	rows := []editorRow{}
	start, cells := 0, 0
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		a, b := g.Positions()
		if g.Str() == "\n" {
			rows = append(rows, editorRow{start, a})
			start, cells = b, 0
			continue
		}
		w := layout.Width(g.Str())
		if cells > 0 && cells+w > width {
			rows = append(rows, editorRow{start, a})
			start, cells = a, 0
		}
		cells += w
	}
	return append(rows, editorRow{start, len(text)})
}
func cursorRow(rows []editorRow, cursor int) int {
	for i := len(rows) - 1; i >= 0; i-- {
		if cursor >= rows[i].start {
			return i
		}
	}
	return 0
}
func (s *State) moveVertical(delta, width int) {
	rows := editorRows(s.Draft, width)
	current := cursorRow(rows, s.Cursor)
	column := layout.Width(s.Draft[rows[current].start:s.Cursor])
	target := min(max(current+delta, 0), len(rows)-1)
	row := rows[target]
	s.Cursor = row.start
	g := uniseg.NewGraphemes(s.Draft[row.start:row.end])
	cells := 0
	for g.Next() {
		cells += layout.Width(g.Str())
		if cells > column {
			break
		}
		_, end := g.Positions()
		s.Cursor = row.start + end
	}
	// A soft-wrap boundary belongs to the next row. Keep an upward move
	// on its intended row even when that row is shorter than the source.
	if target+1 < len(rows) && s.Cursor == rows[target+1].start && s.Cursor > row.start {
		s.Cursor = s.previous()
	}
}
