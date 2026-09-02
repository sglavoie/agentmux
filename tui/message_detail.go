package tui

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

// messageDetailBodyLines builds the flat list of body lines for the current
// message: each \n-separated paragraph is wrapped to the viewport width via
// lipgloss.NewStyle().Width(...).Render, preserving completely-blank
// paragraphs as a single empty line. Internal whitespace inside each
// paragraph is NOT collapsed — full fidelity is the whole point of the
// overlay. Returns nil when the overlay is closed or the captured index is
// out of range so the renderer treats nil as "no body".
func messageDetailBodyLines(m Model) []string {
	if m.messageDetailIdx < 0 || m.messageDetailIdx >= len(m.viewerMessages) {
		return nil
	}
	msg := m.viewerMessages[m.messageDetailIdx]
	maxWidth := m.width
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

// messageDetailScrollBounds returns the total number of body lines and the
// number of those lines visible at the model's current height. When the
// model has no height yet (pre-WindowSizeMsg) the visible window equals the
// total so scroll keys become no-ops naturally.
func messageDetailScrollBounds(m Model) (total, visible int) {
	total = len(messageDetailBodyLines(m))
	if m.height == 0 {
		return total, total
	}
	visible = max(messageDetailMinVisible, min(total, rootBodyHeight(m)-messageDetailChrome))
	return total, visible
}

// renderMessageDetail renders the full-message overlay with a manual
// viewport. On short terminals the body is clipped to the visible window
// and the ▲ / ▼ indicators reveal off-screen content; the layout reserves
// a slot for each indicator unconditionally so the overlay does not jitter
// as the user scrolls.
func renderMessageDetail(m Model) string {
	var sb strings.Builder

	header := "Message"
	if m.messageDetailIdx >= 0 && m.messageDetailIdx < len(m.viewerMessages) {
		msg := m.viewerMessages[m.messageDetailIdx]
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "?"
		}
		ts := msg.Timestamp.Format("2006-01-02 15:04:05")
		header = fmt.Sprintf("Message %d/%d  [%s]  %s",
			m.messageDetailIdx+1, len(m.viewerMessages), role, ts)
	}
	sb.WriteString(styleHeader.Render(header))
	sb.WriteString("\n\n")

	lines := messageDetailBodyLines(m)

	if m.height == 0 {
		// Pre-resize: emit every line verbatim so behaviour matches the
		// post-resize render until the first WindowSizeMsg lands.
		sb.WriteString(strings.Join(lines, "\n"))
		sb.WriteByte('\n')
		return sb.String()
	}

	n := len(lines)
	_, visible := messageDetailScrollBounds(m)
	offset := m.messageDetailScroll
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
