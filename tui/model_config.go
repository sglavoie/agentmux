package tui

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/launch"
)

type configReloadMsg struct {
	info os.FileInfo
	cfg  *config.Config
	err  error
}

// Poll by pathname so editors that atomically replace config.toml are seen
// too. A slow filesystem never holds up input or starts overlapping reads.
func (m *Model) refreshConfig() tea.Cmd {
	if m.configPath == "" || m.configInFlight {
		return nil
	}
	m.configInFlight = true
	path, previous := m.configPath, m.configInfo
	return func() tea.Msg {
		info, err := os.Stat(path)
		if err != nil {
			return configReloadMsg{err: err}
		}
		if previous != nil && os.SameFile(previous, info) && previous.Size() == info.Size() && previous.ModTime().Equal(info.ModTime()) {
			return configReloadMsg{info: info}
		}
		cfg, err := config.Load(path)
		if err == nil {
			err = config.Validate(cfg)
		}
		return configReloadMsg{info: info, cfg: cfg, err: err}
	}
}

func (m Model) handleConfigReload(msg configReloadMsg) (tea.Model, tea.Cmd) {
	m.configInFlight = false
	if msg.err != nil {
		// Keep the last valid config while an editor is mid-save. Repeated
		// failures of the same edit get one notice, and are retried next tick.
		m.pendingConfig = nil
		m.configInfo = nil
		if m.configError == msg.err.Error() {
			return m, nil
		}
		m.configError = msg.err.Error()
		return m, m.pushToast(Toast{Severity: toastError, Text: "Could not reload config; keeping previous settings: " + m.configError})
	}
	m.configError = ""
	m.configInfo = msg.info
	if msg.cfg != nil {
		m.pendingConfig = msg.cfg
	}
	return m, nil
}

// Forms and confirmations retain their target until the interaction ends.
// An open launcher can refresh safely by remapping every index by identity.
func (m *Model) applyPendingConfig() tea.Cmd {
	if m.pendingConfig == nil || (m.mode != ModeList && m.mode != ModePicker && m.mode != ModeSessionSearch) || m.pickerDirMode {
		return nil
	}
	next := m.pendingConfig
	m.pendingConfig = nil
	if reflect.DeepEqual(next, m.cfg) {
		return nil
	}
	if m.cfg != nil && m.sortMode == m.cfg.Defaults.Sort {
		m.sortMode = next.Defaults.Sort
	}
	m.cfg = next
	m.deprecations = next.Deprecations
	m.interval = time.Duration(next.Defaults.RefreshInterval) * time.Second
	if m.interval <= 0 {
		m.interval = 2 * time.Second
	}
	m.idleThreshold = time.Duration(next.Defaults.IdleThreshold) * time.Second
	if m.idleThreshold <= 0 {
		m.idleThreshold = 5 * time.Minute
	}
	if m.mode == ModePicker {
		m.reloadPickerProjects()
		return tea.Batch(m.requestTopologyRefresh(), m.refreshPreviewForPickerCursor())
	}
	return m.requestTopologyRefresh()
}

func (m *Model) reloadPickerProjects() {
	old := m.pickerSessions
	key := func(s config.Session) string { return session.SessionKey(s.EffectiveProfile(), s.Title) }
	selectedKey := ""
	if m.pickerCursor >= 0 && m.pickerCursor < len(m.pickerFilteredIdx) {
		if idx := m.pickerFilteredIdx[m.pickerCursor]; idx >= 0 && idx < len(old) {
			selectedKey = key(old[idx])
		}
	}
	m.pickerSessions = launch.SortSessions(config.ActiveProjects(m.cfg.Sessions))
	indices := make(map[string]int, len(m.pickerSessions))
	for i, project := range m.pickerSessions {
		indices[key(project)] = i
	}
	remap := make(map[int]int, len(old))
	for i, project := range old {
		if j, ok := indices[key(project)]; ok {
			remap[i] = j
		}
	}
	m.pickerSelected = remapProjectValues(m.pickerSelected, remap)
	m.pickerPinned = remapProjectValues(m.pickerPinned, remap)
	m.pickerProfileOverride = remapProjectValues(m.pickerProfileOverride, remap)
	m.pickerDirOverride = remapProjectValues(m.pickerDirOverride, remap)
	for i, profile := range m.pickerProfileOverride {
		if !slices.ContainsFunc(m.cfg.Accounts, func(account config.Account) bool { return strings.EqualFold(account.Name, profile) }) {
			delete(m.pickerProfileOverride, i)
		}
	}
	m.pickerDirProblems = launch.DirectoryProblems(m.pickerSessions, actions.ProjectDirectoryProblem)
	for i := range m.pickerDirOverride {
		m.refreshPickerDirProblem(i)
	}
	m.pickerFilteredIdx = m.pickerFilteredIndices(m.pickerFilter)
	m.pickerCursor = 0
	for cursor, i := range m.pickerFilteredIdx {
		if key(m.pickerSessions[i]) == selectedKey {
			m.pickerCursor = cursor
			break
		}
	}
	scrollPickerToCursor(m)
}

func remapProjectValues[T any](values map[int]T, indices map[int]int) map[int]T {
	result := make(map[int]T, len(values))
	for old, value := range values {
		if next, ok := indices[old]; ok {
			result[next] = value
		}
	}
	return result
}
