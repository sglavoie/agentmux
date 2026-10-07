package launch

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// PickerInput is everything the launcher needs to render itself and to answer
// a question about its current selection. The root model projects itself onto
// this value; the launcher never reaches back into the model.
//
// PaneHeight is supplied by the caller rather than derived here because
// charging the root footer and the sticky status bar is app orchestration: the
// launcher does not know how many lines the footer reflowed to.
type PickerInput struct {
	// Sessions is the configured project list in launcher order (profile,
	// then normalized title). Every index-keyed map below is keyed by an
	// index into this slice, which is why pinning re-partitions FilteredIdx
	// instead of reordering Sessions.
	Sessions []config.Session
	// Running counts live instances per logical session key.
	Running map[string]int
	// DirProblems says, per index, why a project's directory is unusable
	// ("does not exist", ...); a usable directory has no entry. Launching
	// such a project is refused, so its row and preview say so up front.
	DirProblems map[int]string
	// Selected, Pinned, and ProfileOverride are the launcher's per-project
	// state: multi-select checkboxes, persistent pins, and one-time profile
	// choices.
	Selected        map[int]bool
	Pinned          map[int]bool
	ProfileOverride map[int]string

	// FilteredIdx holds indices into Sessions for the currently visible view.
	FilteredIdx   []int
	Cursor        int
	Offset        int
	Filter        string
	ProfileFilter string
	SearchMode    bool

	PreviewEnabled bool
	PreviewText    string
	PreviewError   string

	Width      int
	Height     int
	PaneHeight int

	Cfg *config.Config
}

// EffectiveProfile resolves the profile a project would launch under: the
// one-time override when set, otherwise the project's configured default.
func (in PickerInput) EffectiveProfile(origIdx int) string {
	if name := in.ProfileOverride[origIdx]; name != "" {
		return name
	}
	if origIdx >= 0 && origIdx < len(in.Sessions) {
		return in.Sessions[origIdx].EffectiveProfile()
	}
	return ""
}

// SessionWithResolvedProfile returns the configured project stamped with the
// profile it would actually launch under.
func (in PickerInput) SessionWithResolvedProfile(origIdx int) config.Session {
	if origIdx < 0 || origIdx >= len(in.Sessions) {
		return config.Session{}
	}
	return sessionWithProfile(in.Sessions[origIdx], in.EffectiveProfile(origIdx))
}

// Render renders the session picker. When preview is enabled and the terminal
// is wide enough, it splits horizontally with a config + running-peek pane on
// the right; otherwise it falls back to a single-pane layout.
func (in PickerInput) Render() string {
	listHeight := 0
	if in.Height > 0 {
		listHeight = in.VisibleHeight()
	}

	if !in.PreviewEnabled {
		return in.RenderList(listHeight)
	}

	cfg := in.Cfg.Defaults.Preview
	paneHeight := in.PaneHeight
	if paneHeight < 4 || in.Width < cfg.MinCols {
		return in.RenderList(listHeight)
	}
	if len(in.FilteredIdx) == 0 {
		return in.RenderList(listHeight)
	}

	previewWidth := int(float64(in.Width) * cfg.Ratio)
	if previewWidth < 20 {
		previewWidth = 20
	}
	if previewWidth > in.Width-20 {
		previewWidth = in.Width - 20
	}
	listWidth := in.Width - previewWidth - 1

	origIdx := in.FilteredIdx[in.Cursor]

	listInput := in
	listInput.Width = listWidth
	listText := listInput.RenderList(listHeight)
	previewView := in.RenderPreview(origIdx, previewWidth, paneHeight)
	divider := strings.Repeat(styleDim.Render("│")+"\n", paneHeight)
	divider = strings.TrimRight(divider, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, listText, divider, previewView) + "\n"
}

// RenderList renders the header, sticky filter prompt, and rows constrained by
// a row-line budget. A non-positive height means "unbounded": every row in
// FilteredIdx is rendered and the ▲/▼ scroll indicators are suppressed.
// Mirrors the dashboard list's windowing contract.
func (in PickerInput) RenderList(height int) string {
	var sb strings.Builder
	selectedCount := 0
	for _, v := range in.Selected {
		if v {
			selectedCount++
		}
	}
	header := "Launch Sessions"
	if selectedCount > 0 {
		header += fmt.Sprintf("  [%d selected]", selectedCount)
	}
	profileLabel := "All"
	if in.ProfileFilter != "" {
		profileLabel = in.ProfileFilter
	}
	header += "  [profile: " + profileLabel + "]"
	sb.WriteString(in.renderHeader(header))
	switch {
	case in.SearchMode:
		sb.WriteString(styleSearch.Render("/") + in.Filter + "█" + "\n\n")
	case in.Filter != "":
		sb.WriteString(styleSearch.Render("/") + in.Filter + "\n\n")
	default:
		sb.WriteByte('\n')
	}

	if len(in.Sessions) == 0 {
		sb.WriteString(styleDim.Render(layout.ClampLine("  (no projects configured — add [[projects]] with `amx config edit`)", in.Width)) + "\n")
		return sb.String()
	}
	if len(in.FilteredIdx) == 0 {
		sb.WriteString(styleDim.Render(layout.ClampLine("  (no projects match — esc clears the filter)", in.Width)) + "\n")
		return sb.String()
	}

	acctColor := dashboard.ProfileColorMap(in.Cfg)
	harnessColor := dashboard.HarnessColorMap(in.Cfg)

	// Pre-pass: compute column widths over the filtered set so the harness
	// cell and state-flag columns line up vertically. The checkbox prefix
	// ("[ ] " / "[*] ") is always 4 cells wide and the fixed pin cell plus
	// trailing space is layout.IconCellWidth+1 cells.
	titleColW := 0
	harnessColW := 0
	for _, origIdx := range in.FilteredIdx {
		sess := in.SessionWithResolvedProfile(origIdx)
		w := 4 + layout.IconCellWidth + 1 + layout.Width(layout.RenderTitleIcon(configSessionDisplayTitle(sess)))
		if w > titleColW {
			titleColW = w
		}
		if acc, ok := findAccountByName(in.Cfg.Accounts, sess.EffectiveProfile()); ok {
			hw := layout.Width(harnessCellText(in.Cfg, acc))
			if hw > harnessColW {
				harnessColW = hw
			}
		}
	}

	total := len(in.FilteredIdx)
	startIdx := 0
	endIdx := total
	showTop := false
	showBottom := false

	if height > 0 {
		// Defensive clamp for a stale offset (e.g. after a refresh shrank
		// FilteredIdx). Mirrors the dashboard list renderer.
		offset := in.Offset
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
		origIdx := in.FilteredIdx[i]
		sess := in.SessionWithResolvedProfile(origIdx)
		defaultProfile := in.Sessions[origIdx].EffectiveProfile()
		runningCount := in.Running[session.SessionKey(sess.EffectiveProfile(), sess.Title)]

		prefix := "[ ] "
		if in.Selected[origIdx] {
			prefix = "[*] "
		}
		pinIcon := ""
		if in.Pinned[origIdx] {
			pinIcon = "📌"
		}
		prefix += layout.PadRight(pinIcon, layout.IconCellWidth) + " "

		title := layout.RenderTitleIcon(configSessionDisplayTitle(sess))
		coloredTitle := accountStyle(acctColor[defaultProfile]).Render(title)
		titlePad := titleColW - (4 + layout.IconCellWidth + 1 + layout.Width(title))
		if titlePad < 0 {
			titlePad = 0
		}

		var harnessCell string
		if acc, ok := findAccountByName(in.Cfg.Accounts, sess.EffectiveProfile()); ok {
			text := harnessCellText(in.Cfg, acc)
			pad := harnessColW - layout.Width(text)
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
		if in.DirProblems[origIdx] != "" {
			line += "  " + styleError.Render("(missing)")
		}
		if runningCount == 1 {
			line += "  " + styleDim.Render("(running)")
		} else if runningCount > 1 {
			line += "  " + styleDim.Render(fmt.Sprintf("(%d running)", runningCount))
		}

		// Clamp the composed row to the effective list width. Render threads
		// a narrowed Width when the preview pane is on, so the left column
		// never overruns the "list │ preview" split; with the preview off,
		// Width is the full terminal width. Without this the row (built by
		// concatenation with content-derived column widths) can draw past the
		// terminal edge and corrupt the layout.
		if in.Width > 0 {
			line = layout.StyledTruncate(line, in.Width, "…")
		}

		if i == in.Cursor {
			sb.WriteString(renderSelectedRow(line))
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

func (in PickerInput) renderHeader(header string) string {
	left := styleHeader.Render(header)
	if in.Width > 0 && layout.StyledWidth(left) > in.Width {
		return layout.StyledTruncate(left, in.Width, "…") + "\n"
	}
	right := ""
	if !in.SearchMode && in.Filter == "" {
		right = styleDim.Render("press / to filter")
	}
	if right == "" || in.Width <= 0 {
		return left + "\n"
	}
	pad := in.Width - layout.StyledWidth(left) - layout.StyledWidth(right)
	if pad < 1 {
		return left + "\n"
	}
	return left + strings.Repeat(" ", pad) + right + "\n"
}

// DirectoryProblems applies problem (config.ProjectDirectoryProblem, or a
// test's replacement) to every project's directory once, when the launcher
// opens, so rendering never touches the filesystem.
func DirectoryProblems(sessions []config.Session, problem func(string) string) map[int]string {
	problems := make(map[int]string)
	for i, sess := range sessions {
		if p := problem(sess.Directory); p != "" {
			problems[i] = p
		}
	}
	return problems
}
