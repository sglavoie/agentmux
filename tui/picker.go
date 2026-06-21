package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

// pickerRunningPeekLines caps the scrollback tail rendered below the
// "── running ──" divider in the picker preview pane.
const pickerRunningPeekLines = 20

// pickerHarnessCellText renders the picker's harness cell text for a single
// account: "<emoji> <profile>/<harness>", or just "<profile>/<harness>" when
// no emoji is configured for the harness, so unconfigured users do not see a
// stray leading space.
func pickerHarnessCellText(cfg *config.Config, acc config.Account) string {
	label := acc.Name + "/" + acc.EffectiveHarness()
	emoji := ""
	if cfg != nil {
		emoji = cfg.HarnessEmoji(acc.EffectiveHarness())
	}
	if emoji == "" {
		return label
	}
	return emoji + " " + label
}

// harnessDisplayText renders a harness name with its configured emoji prefix,
// falling back to the bare harness name when no emoji is configured.
func harnessDisplayText(cfg *config.Config, harness string) string {
	emoji := ""
	if cfg != nil {
		emoji = cfg.HarnessEmoji(harness)
	}
	if emoji == "" {
		return harness
	}
	return emoji + " " + harness
}

// renderPicker renders the session picker. When preview is enabled and the
// terminal is wide enough, it splits horizontally with a config + running-peek
// pane on the right; otherwise it falls back to a single-pane layout.
func renderPicker(m Model) string {
	listHeight := 0
	if m.height > 0 {
		listHeight = pickerVisibleHeight(m)
	}

	if !m.pickerPreviewEnabled {
		return renderPickerList(m, listHeight)
	}

	cfg := m.cfg.Defaults.Preview
	paneHeight := pickerPaneHeight(m)
	if paneHeight < 4 || m.width < cfg.MinCols {
		return renderPickerList(m, listHeight)
	}
	if len(m.pickerFilteredIdx) == 0 {
		return renderPickerList(m, listHeight)
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

	listModel := m
	listModel.width = listWidth
	listText := renderPickerList(listModel, listHeight)
	previewView := renderPickerPreview(m, origIdx, previewWidth, paneHeight)
	divider := strings.Repeat(styleDim.Render("│")+"\n", paneHeight)
	divider = strings.TrimRight(divider, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, listText, divider, previewView) + "\n"
}

// renderPickerList renders the header, sticky filter prompt, and rows
// constrained by a row-line budget. A non-positive height means "unbounded":
// every row in m.pickerFilteredIdx is rendered and the ▲/▼ scroll indicators
// are suppressed. Mirrors renderListAt's windowing contract.
func renderPickerList(m Model, height int) string {
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
	sb.WriteString(renderPickerHeader(m, header))
	switch {
	case m.pickerSearchMode:
		sb.WriteString(styleSearch.Render("/") + m.pickerFilter + "█" + "\n\n")
	case m.pickerFilter != "":
		sb.WriteString(styleSearch.Render("/") + m.pickerFilter + "\n\n")
	default:
		sb.WriteByte('\n')
	}

	if len(m.pickerSessions) == 0 {
		sb.WriteString(styleDim.Render("  (no sessions in config)") + "\n")
		return sb.String()
	}
	if len(m.pickerFilteredIdx) == 0 {
		sb.WriteString(styleDim.Render("  (no sessions match)") + "\n")
		return sb.String()
	}

	acctColor := buildProfileColorMap(m)
	harnessColor := buildHarnessColorMap(m)

	// Pre-pass: compute column widths over the filtered set so the harness
	// cell and state-flag columns line up vertically. The checkbox prefix
	// ("[ ] " / "[*] ") is always 4 cells wide regardless of selection.
	titleColW := 0
	harnessColW := 0
	for _, origIdx := range m.pickerFilteredIdx {
		sess := m.pickerSessionWithResolvedProfile(origIdx)
		w := 4 + displayWidth(displayRenderTitleIcon(configSessionDisplayTitle(sess)))
		if w > titleColW {
			titleColW = w
		}
		if acc, ok := findAccountByName(m.cfg.Accounts, sess.EffectiveProfile()); ok {
			hw := displayWidth(pickerHarnessCellText(m.cfg, acc))
			if hw > harnessColW {
				harnessColW = hw
			}
		}
	}

	total := len(m.pickerFilteredIdx)
	startIdx := 0
	endIdx := total
	showTop := false
	showBottom := false

	if height > 0 {
		// Defensive clamp for a stale offset (e.g. after a refresh shrank
		// pickerFilteredIdx). Mirrors renderListAt.
		offset := m.pickerOffset
		if offset < 0 {
			offset = 0
		}
		if offset > total {
			offset = total
		}
		startIdx = offset

		budget := height
		if startIdx > 0 {
			showTop = true
			budget--
		}
		if budget < 0 {
			budget = 0
		}
		// Two-pass fit: try without bottom indicator, then re-fit with it
		// reserved if rows still remain past the window.
		endIdx = startIdx + budget
		if endIdx > total {
			endIdx = total
		}
		if endIdx < total {
			showBottom = true
			if budget > 0 {
				budget--
			}
			endIdx = startIdx + budget
			if endIdx > total {
				endIdx = total
			}
		}
	}

	if showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	for i := startIdx; i < endIdx; i++ {
		origIdx := m.pickerFilteredIdx[i]
		sess := m.pickerSessionWithResolvedProfile(origIdx)
		defaultProfile := m.pickerSessions[origIdx].EffectiveProfile()
		runningCount := m.pickerRunning[session.SessionKey(sess.EffectiveProfile(), sess.Title)]

		prefix := "[ ] "
		if m.pickerSelected[origIdx] {
			prefix = "[*] "
		}

		title := displayRenderTitleIcon(configSessionDisplayTitle(sess))
		coloredTitle := accountStyle(acctColor[defaultProfile]).Render(title)
		titlePad := titleColW - (4 + displayWidth(title))
		if titlePad < 0 {
			titlePad = 0
		}

		var harnessCell string
		if acc, ok := findAccountByName(m.cfg.Accounts, sess.EffectiveProfile()); ok {
			text := pickerHarnessCellText(m.cfg, acc)
			pad := harnessColW - displayWidth(text)
			if pad < 0 {
				pad = 0
			}
			harnessCell = accountStyle(harnessColor[acc.EffectiveHarness()]).Render(text) + strings.Repeat(" ", pad)
		} else {
			harnessCell = strings.Repeat(" ", harnessColW)
		}

		line := "  " + prefix + coloredTitle + strings.Repeat(" ", titlePad) + "  " + harnessCell
		if sess.EffectiveProfile() != defaultProfile {
			line += "  " + styleDim.Render("(override)")
		}
		if runningCount == 1 {
			line += "  " + styleDim.Render("(running)")
		} else if runningCount > 1 {
			line += "  " + styleDim.Render(fmt.Sprintf("(%d running)", runningCount))
		}

		if i == m.pickerCursor {
			sb.WriteString(styleSelected.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if showBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return sb.String()
}

func renderPickerHeader(m Model, header string) string {
	left := styleHeader.Render(header)
	right := ""
	if !m.pickerSearchMode && m.pickerFilter == "" {
		right = styleDim.Render("press / to filter")
	}
	if right == "" || m.width <= 0 {
		return left + "\n"
	}
	pad := m.width - displayStyledWidth(left) - displayStyledWidth(right)
	if pad < 1 {
		return left + "\n"
	}
	return left + strings.Repeat(" ", pad) + right + "\n"
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
	acctColor := buildProfileColorMap(m)

	lines := make([]string, 0, height)
	header := accountStyle(acctColor[defaultProfile]).Render(
		displayTruncate(displayRenderTitleIcon(configSessionDisplayTitle(sess)), width, "…"),
	)
	lines = append(lines, header)

	addField := func(label, value string) {
		if value == "" {
			return
		}
		body := styleDim.Render(label+": ") + value
		lines = append(lines, displayStyledTruncate(body, width, "…"))
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

	if m.pickerRunning[session.SessionKey(sess.EffectiveProfile(), sess.Title)] > 0 {
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
		return []string{styleError.Render(displayTruncate("error: "+errText, width, "…"))}
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
		out[i] = displayTruncate(ln, width, "…")
	}
	return out
}

// sortPickerSessions returns a sorted copy: by profile, then by normalized
// project title (emoji/punctuation prefix stripped, case-insensitive).
func sortPickerSessions(sessions []config.Session) []config.Session {
	sorted := slices.Clone(sessions)
	slices.SortStableFunc(sorted, func(a, b config.Session) int {
		if c := strings.Compare(
			session.ProjectSortKey(a.EffectiveProfile()),
			session.ProjectSortKey(b.EffectiveProfile()),
		); c != 0 {
			return c
		}
		return strings.Compare(
			session.ProjectSortKey(a.Title),
			session.ProjectSortKey(b.Title),
		)
	})
	return sorted
}

// buildPickerRunning returns logical session keys and their current instance counts.
func buildPickerRunning(sessions []session.RunningSession) map[string]int {
	m := make(map[string]int, len(sessions))
	for _, s := range sessions {
		m[runningSessionKey(s)]++
	}
	return m
}

func runningSessionKey(sess session.RunningSession) string {
	if sess.Key != "" {
		return sess.Key
	}
	return session.SessionKey(sess.EffectiveProfile(), sess.EffectiveProject())
}
