package history

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/layout"
)

// messageDetailChrome is the number of vertical lines the message-detail
// overlay reserves inside the root body but outside the scrollable body
// window: title (1) + blank after title (1) + ▲ slot (1) + ▼ slot (1) = 4.
const messageDetailChrome = 4

// messageDetailMinVisible is the floor on the visible body window so the
// overlay stays usable on short terminals. Mirrors helpMinVisible.
const messageDetailMinVisible = 3

// MessageBodyLines builds the flat list of body lines for the current
// message: each \n-separated paragraph is wrapped to the viewport width via
// lipgloss.NewStyle().Width(...).Render, preserving completely-blank
// paragraphs as a single empty line. Internal whitespace inside each
// paragraph is NOT collapsed — full fidelity is the whole point of the
// overlay. Returns nil when the overlay is closed or the captured index is
// out of range so the renderer treats nil as "no body".
func (in ViewerInput) MessageBodyLines() []string {
	if in.MessageIdx < 0 || in.MessageIdx >= len(in.Messages) {
		return nil
	}
	msg := in.Messages[in.MessageIdx]
	maxWidth := in.Width
	if maxWidth <= 0 {
		maxWidth = 120
	}

	paragraphs := strings.Split(msg.Text, "\n")
	var lines []string
	wrapStyle := lipgloss.NewStyle().Width(maxWidth)
	for _, p := range paragraphs {
		if p == "" {
			lines = append(lines, "")
			continue
		}
		wrapped := wrapStyle.Render(p)
		for _, line := range strings.Split(wrapped, "\n") {
			lines = append(lines, layout.WrapHard(line, maxWidth)...)
		}
	}
	return lines
}

// MessageScrollBounds returns the total number of body lines and the number of
// those lines visible at the input's current height. When the input has no
// height yet (pre-WindowSizeMsg) the visible window equals the total so scroll
// keys become no-ops naturally.
func (in ViewerInput) MessageScrollBounds() (total, visible int) {
	total = len(in.MessageBodyLines())
	if in.Height == 0 {
		return total, total
	}
	visible = max(messageDetailMinVisible, min(total, in.BodyHeight-messageDetailChrome))
	return total, visible
}

// RenderMessage renders the full-message overlay with a manual viewport. On
// short terminals the body is clipped to the visible window and the ▲ / ▼
// indicators reveal off-screen content; the layout reserves a slot for each
// indicator unconditionally so the overlay does not jitter as the user
// scrolls.
func (in ViewerInput) RenderMessage() string {
	var sb strings.Builder

	header := "Message"
	if in.MessageIdx >= 0 && in.MessageIdx < len(in.Messages) {
		msg := in.Messages[in.MessageIdx]
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "?"
		}
		ts := msg.Timestamp.Format("2006-01-02 15:04:05")
		header = fmt.Sprintf("Message %d/%d  [%s]  %s",
			in.MessageIdx+1, len(in.Messages), role, ts)
	}
	sb.WriteString(styleHeader.Render(header))
	sb.WriteString("\n\n")

	lines := in.MessageBodyLines()

	if in.Height == 0 {
		// Pre-resize: emit every line verbatim so behaviour matches the
		// post-resize render until the first WindowSizeMsg lands.
		sb.WriteString(strings.Join(lines, "\n"))
		sb.WriteByte('\n')
		return sb.String()
	}

	n := len(lines)
	_, visible := in.MessageScrollBounds()
	offset := in.MessageScroll
	if offset+visible > n {
		offset = n - visible
	}
	if offset < 0 {
		offset = 0
	}

	if offset > 0 {
		sb.WriteString(styleDim.Render("  ▲"))
	}
	sb.WriteByte('\n')

	end := offset + visible
	if end > n {
		end = n
	}
	for _, line := range lines[offset:end] {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	if end < n {
		sb.WriteString(styleDim.Render("  ▼"))
	}
	sb.WriteByte('\n')

	return sb.String()
}
