package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
)

func (m Model) enterLoopRunPlanPath() (tea.Model, tea.Cmd) {
	m.mode = ModeLoopRunPlanPath
	m.loopRunPlanPath = ""
	m.loopRunPlanPathErr = nil
	return m, nil
}

func (m *Model) exitLoopRunPlanPath() {
	m.mode = ModeLoopRunPlanSearch
	m.loopRunPlanPath = ""
	m.loopRunPlanPathErr = nil
}

func (m Model) handleLoopRunPlanPathKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc:
		m.exitLoopRunPlanPath()
		return m, nil
	case keyEnter:
		return m.submitLoopRunPlanPath()
	case keyBackspace:
		if r := []rune(m.loopRunPlanPath); len(r) > 0 {
			m.loopRunPlanPath = string(r[:len(r)-1])
			m.loopRunPlanPathErr = nil
		}
		return m, nil
	case keySpace:
		m.loopRunPlanPath += " "
		m.loopRunPlanPathErr = nil
		return m, nil
	default:
		if t := msg.Key().Text; t != "" {
			m.loopRunPlanPath += t
			m.loopRunPlanPathErr = nil
		}
		return m, nil
	}
}

func (m Model) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.mode != ModeLoopRunPlanPath {
		return m, nil
	}
	return m.handleLoopRunPlanPathPaste(msg.String())
}

func (m Model) handleLoopRunPlanPathPaste(text string) (tea.Model, tea.Cmd) {
	if text == "" {
		return m, nil
	}
	m.loopRunPlanPath += text
	m.loopRunPlanPathErr = nil
	return m, nil
}

func (m Model) submitLoopRunPlanPath() (tea.Model, tea.Cmd) {
	cleaned := cleanLoopRunPlanPath(m.loopRunPlanPath)
	m.loopRunPlanPath = cleaned
	m.loopRunPlanPathErr = nil

	if cleaned == "" {
		m.loopRunPlanPathErr = errors.New("plan directory is required")
		return m, nil
	}
	if strings.TrimSpace(m.loopRunProjectDir) == "" {
		m.loopRunPlanPathErr = errors.New("selected project cwd is required")
		return m, nil
	}

	meta := plans.PlanMeta{
		Name:    plans.DisplayName(filepath.Base(cleaned)),
		DirPath: cleaned,
	}
	return m, loopRunPlanLaunchRequestCmd(m.loopRunProjectDir, cleaned, meta)
}

func cleanLoopRunPlanPath(raw string) string {
	path := strings.TrimSpace(raw)
	if len(path) >= 2 {
		first := path[0]
		if (first == '\'' || first == '"' || first == '`') && path[len(path)-1] == first {
			path = strings.TrimSpace(path[1 : len(path)-1])
		}
	}
	return config.ExpandTilde(path)
}

func renderLoopRunPlanPath(m Model) string {
	var sb strings.Builder
	title := "Run Plan Path"
	if m.loopRunProjectTitle != "" {
		title = fmt.Sprintf("Run Plan Path: %s", m.loopRunProjectTitle)
	}
	sb.WriteString(styleHeader.Render(title))
	sb.WriteByte('\n')
	sb.WriteString(styleSearch.Render("path: ") + m.loopRunPlanPath + "█")
	sb.WriteByte('\n')
	sb.WriteByte('\n')
	if m.loopRunProjectDir != "" {
		sb.WriteString(styleDim.Render("project: " + m.loopRunProjectDir))
		sb.WriteByte('\n')
	}
	if m.loopRunPlanPathErr != nil {
		sb.WriteString(styleError.Render("path error: " + m.loopRunPlanPathErr.Error()))
		sb.WriteByte('\n')
	}
	return sb.String()
}
