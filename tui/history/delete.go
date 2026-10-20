package history

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/layout"
)

// DeleteInput carries the three-way transcript delete prompt's state. Target
// is nil while the prompt is opening, which renders the bare header.
type DeleteInput struct {
	Target *transcript.SessionMeta
	Width  int
}

// Render draws the three-way prompt. The "also delete file" option is
// rendered in styleError because the on-disk JSONL removal is irreversible.
func (in DeleteInput) Render() string {
	var sb strings.Builder
	sb.WriteString("  ")
	if in.Target == nil {
		sb.WriteString(styleHeader.Render("Delete transcript?"))
		sb.WriteByte('\n')
		return sb.String()
	}
	t := in.Target
	sb.WriteString(styleHeader.Render(fmt.Sprintf("Delete %s?", DeleteTargetLabel(*t))))
	sb.WriteByte('\n')
	sb.WriteString("  ")
	sb.WriteString(styleDim.Render(deleteTargetDetail(*t)))
	sb.WriteByte('\n')
	sb.WriteByte('\n')
	sb.WriteString("  ")
	sb.WriteString(chrome.Binding("[i]", "index only — hide from search; file kept on disk"))
	sb.WriteByte('\n')
	sb.WriteString("  ")
	sb.WriteString(chrome.BindingWithLabel("[f]", " ", "also delete file — irreversible, removes the JSONL", styleError))
	sb.WriteByte('\n')
	sb.WriteString("  ")
	sb.WriteString(chrome.Binding("[esc]", "cancel"))
	sb.WriteByte('\n')
	return sb.String()
}

// RenderBox wraps the prompt in styleOverlayBox so it can be composited as a
// centered overlay on top of the session search backdrop. The inner width is
// capped at 70 columns (enough room for the longest `[f] also delete file …`
// line) and clamped to Width-4 on narrow terminals.
func (in DeleteInput) RenderBox() string {
	innerWidth := 70
	if avail := in.Width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(in.Render(), "\n"))
}

// DeleteTargetLabel produces a short, scannable description of the transcript
// for the header + toast text. Falls back to the transcript ID when no project
// title was resolved. It is exported because the root's delete-result handler
// names the same target in its toast text.
func DeleteTargetLabel(t transcript.SessionMeta) string {
	proj := t.Project
	if proj == "" {
		proj = t.ID
	}
	return fmt.Sprintf("%q", layout.RenderTitleIcon(proj))
}

func deleteTargetDetail(t transcript.SessionMeta) string {
	parts := make([]string, 0, 3)
	if t.Harness != "" {
		parts = append(parts, t.Harness)
	}
	if profile := sessionProfileLabel(t); profile != "" {
		parts = append(parts, profile)
	}
	if t.Path != "" {
		parts = append(parts, t.Path)
	}
	return strings.Join(parts, " · ")
}
