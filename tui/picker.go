package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

// pickerRunningPeekLines caps the scrollback tail rendered below the
// "── running ──" divider in the picker preview pane.
const pickerRunningPeekLines = 20

// renderPicker renders the session picker. When preview is enabled and the
// terminal is wide enough, it splits horizontally with a config + running-peek
// pane on the right; otherwise it falls back to a single-pane layout.
func renderPicker(m Model) string {
	listText := renderPickerList(m)

	if !m.pickerPreviewEnabled {
		return listText
	}

	cfg := m.cfg.Defaults.Preview
	// Picker render is followed by "\n" + a 2-line footer in render(),
	// so the available content height is height - 3.
	paneHeight := m.height - 3
	if paneHeight < 4 || m.width < cfg.MinCols {
		return listText
	}
	if len(m.pickerFilteredIdx) == 0 {
		return listText
	}

	previewWidth := int(float64(m.width) * cfg.Ratio)
	if previewWidth < 20 {
		previewWidth = 20
	}
	if previewWidth > m.width-20 {
		previewWidth = m.width - 20
	}
	listWidth := m.width - previewWidth - 1

	origIdx := m.pickerFilteredIdx[m.pickerCursor]

	listView := clipHeight(listText, paneHeight)
	previewView := renderPickerPreview(m, origIdx, previewWidth, paneHeight)
	divider := strings.Repeat(styleDim.Render("│")+"\n", paneHeight)
	divider = strings.TrimRight(divider, "\n")
	_ = listWidth // picker rows are short; no per-row truncation needed
	return lipgloss.JoinHorizontal(lipgloss.Top, listView, divider, previewView) + "\n"
}

// renderPickerList renders the header, sticky filter prompt, and rows.
func renderPickerList(m Model) string {
	var sb strings.Builder
	selectedCount := 0
	for _, v := range m.pickerSelected {
		if v {
			selectedCount++
		}
	}
	header := "Launch Sessions"
	if selectedCount > 0 {
		header += fmt.Sprintf("  [%d selected]", selectedCount)
	}
	sb.WriteString(styleHeader.Render(header) + "\n")
	switch {
	case m.pickerSearchMode:
		sb.WriteString(styleSearch.Render("/") + m.pickerFilter + "█" + "\n\n")
	case m.pickerFilter != "":
		sb.WriteString(styleSearch.Render("/") + m.pickerFilter + "\n\n")
	default:
		sb.WriteString(styleDim.Render("press / to filter") + "\n\n")
	}

	if len(m.pickerSessions) == 0 {
		sb.WriteString(styleDim.Render("  (no sessions in config)") + "\n")
		return sb.String()
	}
	if len(m.pickerFilteredIdx) == 0 {
		sb.WriteString(styleDim.Render("  (no sessions match)") + "\n")
		return sb.String()
	}

	acctColor := buildAccountColorMap(m)

	for cursor, origIdx := range m.pickerFilteredIdx {
		sess := m.pickerSessionWithResolvedProfile(origIdx)
		defaultProfile := m.pickerSessions[origIdx].EffectiveProfile()
		running := m.pickerRunning[session.SessionKey(sess.EffectiveProfile(), sess.Title)]

		prefix := "[ ] "
		if m.pickerSelected[origIdx] {
			prefix = "[*] "
		}

		baseTitle := session.FormatTitle(sess)
		coloredTitle := accountStyle(acctColor[sess.EffectiveProfile()]).Render(baseTitle)
		line := "  " + prefix + coloredTitle
		if acc, ok := findAccountByName(m.cfg.Accounts, sess.EffectiveProfile()); ok {
			line += "  " + styleDim.Render("["+acc.Name+"/"+acc.EffectiveHarness()+"]")
		}
		if sess.EffectiveProfile() != defaultProfile {
			line += "  " + styleDim.Render("(override)")
		}
		if running {
			line += "  " + styleDim.Render("(running)")
		}

		if cursor == m.pickerCursor {
			sb.WriteString(styleSelected.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// renderPickerPreview renders the right-hand pane for the picker: a labeled
// config block, then (if the highlighted session is currently running) a
// divider and the tail of its scrollback. Output is exactly height lines.
func renderPickerPreview(m Model, origIdx, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	sess := m.pickerSessionWithResolvedProfile(origIdx)
	defaultProfile := m.pickerSessions[origIdx].EffectiveProfile()
	acctColor := buildAccountColorMap(m)

	lines := make([]string, 0, height)
	header := accountStyle(acctColor[sess.EffectiveProfile()]).Render(
		runewidth.Truncate(session.FormatTitle(sess), width, "…"),
	)
	lines = append(lines, header)

	addField := func(label, value string) {
		if value == "" {
			return
		}
		body := styleDim.Render(label+": ") + value
		lines = append(lines, runewidth.Truncate(body, width, "…"))
	}
	addField("title", sess.Title)
	addField("dir", sess.Directory)
	if sess.EffectiveProfile() != defaultProfile {
		addField("default profile", defaultProfile)
		addField("launch profile", sess.EffectiveProfile())
	} else {
		addField("profile", sess.EffectiveProfile())
	}
	if acc, ok := findAccountByName(m.cfg.Accounts, sess.EffectiveProfile()); ok {
		addField("harness", acc.EffectiveHarness())
	}
	if len(sess.Args) > 0 {
		addField("args", strings.Join(sess.Args, " "))
	}
	if len(sess.Env) > 0 {
		keys := make([]string, 0, len(sess.Env))
		for k := range sess.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		kvs := make([]string, 0, len(keys))
		for _, k := range keys {
			kvs = append(kvs, k+"="+sess.Env[k])
		}
		addField("env", strings.Join(kvs, " "))
	}
	if len(sess.Groups) > 0 {
		addField("groups", strings.Join(sess.Groups, ", "))
	}

	if m.pickerRunning[session.SessionKey(sess.EffectiveProfile(), sess.Title)] {
		lines = append(lines, styleDim.Render("── running ──"))
		remaining := height - len(lines)
		if remaining > pickerRunningPeekLines {
			remaining = pickerRunningPeekLines
		}
		if remaining > 0 {
			lines = append(lines, pickerScrollbackTail(m.previewText, m.previewError, remaining, width)...)
		}
	}

	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// pickerScrollbackTail returns up to height lines from the end of text,
// truncated to width. Mirrors previewBody's tail logic without the padding
// (renderPickerPreview owns the final padding to exactly height).
func pickerScrollbackTail(text, errText string, height, width int) []string {
	if height <= 0 {
		return nil
	}
	if errText != "" {
		return []string{styleError.Render(runewidth.Truncate("error: "+errText, width, "…"))}
	}
	if text == "" {
		return []string{styleDim.Render("(loading…)")}
	}
	raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(raw) > height {
		raw = raw[len(raw)-height:]
	}
	out := make([]string, len(raw))
	for i, ln := range raw {
		out[i] = runewidth.Truncate(ln, width, "…")
	}
	return out
}

// sortPickerSessions returns a sorted copy: by account name, then by title.
func sortPickerSessions(sessions []config.Session) []config.Session {
	sorted := slices.Clone(sessions)
	slices.SortStableFunc(sorted, func(a, b config.Session) int {
		if c := strings.Compare(
			strings.ToLower(a.EffectiveProfile()),
			strings.ToLower(b.EffectiveProfile()),
		); c != 0 {
			return c
		}
		return strings.Compare(
			strings.ToLower(a.Title),
			strings.ToLower(b.Title),
		)
	})
	return sorted
}

// buildPickerRunning returns a set of logical session keys for all currently running sessions.
func buildPickerRunning(sessions []session.RunningSession) map[string]bool {
	m := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		m[runningSessionKey(s)] = true
	}
	return m
}

func runningSessionKey(sess session.RunningSession) string {
	if sess.Key != "" {
		return sess.Key
	}
	return session.SessionKey(sess.EffectiveProfile(), sess.EffectiveProject())
}
