package tui

// Worktrees view: loading the managed-worktree list, sorting and filtering it,
// and moving the cursor through it. The lifecycle actions a row can trigger
// live in model_worktrees_lifecycle.go, and the ambiguity picker a reopen can
// raise lives in model_live_session_picker.go.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/worktree"
)

type worktreesLoadedMsg struct {
	seq      int
	entries  []actions.WorktreeView
	warnings []string
	err      error
}

type worktreeActionDoneMsg struct {
	action actions.ID
	result actions.Result
	err    error
}

func worktreeListCmd(m Model, seq int) tea.Cmd {
	return func() tea.Msg {
		result, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{
			Kitty:  m.client,
			Config: m.cfg,
		}, actions.WorktreeList, actions.Request{})
		if err != nil {
			return worktreesLoadedMsg{seq: seq, err: err}
		}
		entries, ok := result.Data["worktrees"].([]actions.WorktreeView)
		if !ok {
			return worktreesLoadedMsg{seq: seq, err: fmt.Errorf("worktree list returned an invalid worktrees payload")}
		}
		warnings, _ := result.Data["warnings"].([]string)
		return worktreesLoadedMsg{
			seq:      seq,
			entries:  append([]actions.WorktreeView(nil), entries...),
			warnings: append([]string(nil), warnings...),
		}
	}
}

func (m Model) enterWorktrees() (tea.Model, tea.Cmd) {
	m.clearJumpChord()
	m.mode = ModeWorktrees
	m.worktreeFilterMode = false
	m.worktreeErr = nil
	if len(m.worktreeFiltered) == 0 {
		m.worktreeCursor = 0
		m.worktreeOffset = 0
	}
	return m.startWorktreeRefresh()
}

func (m Model) startWorktreeRefresh() (tea.Model, tea.Cmd) {
	m.captureWorktreeSelection()
	m.worktreeLoading = true
	m.worktreeErr = nil
	m.worktreeRefreshSeq++
	return m, m.runWorktreeListCmd(m.worktreeRefreshSeq)
}

func (m *Model) captureWorktreeSelection() {
	if m.worktreeCursor >= 0 && m.worktreeCursor < len(m.worktreeFiltered) {
		m.worktreeSelectedID = m.worktreeFiltered[m.worktreeCursor].ID
	}
}

func (m Model) handleWorktreesLoaded(msg worktreesLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.worktreeRefreshSeq || m.activeRootMode() != ModeWorktrees {
		return m, nil
	}
	m.worktreeLoading = false
	if msg.err != nil {
		m.worktreeErr = msg.err
		return m, nil
	}
	m.worktreeErr = nil
	m.worktreeWarnings = append([]string(nil), msg.warnings...)
	m.worktreeEntries = sortWorktreeViews(msg.entries)
	m.reapplyWorktreeFilter()
	return m, nil
}

func sortWorktreeViews(views []actions.WorktreeView) []actions.WorktreeView {
	if len(views) < 2 {
		return append([]actions.WorktreeView(nil), views...)
	}
	byKey := make(map[string][]actions.WorktreeView, len(views))
	entries := make([]worktree.Entry, 0, len(views))
	for _, view := range views {
		key := worktreeViewKey(view)
		byKey[key] = append(byKey[key], view)
		entries = append(entries, view.Entry)
	}
	worktree.SortEntries(entries)
	ordered := make([]actions.WorktreeView, 0, len(views))
	for _, entry := range entries {
		key := entry.ID + "\x00" + entry.Path
		bucket := byKey[key]
		ordered = append(ordered, bucket[0])
		byKey[key] = bucket[1:]
	}
	return ordered
}

func worktreeViewKey(view actions.WorktreeView) string {
	return view.ID + "\x00" + view.Path
}

func (m *Model) reapplyWorktreeFilter() {
	filtered := make([]actions.WorktreeView, 0, len(m.worktreeEntries))
	for _, entry := range m.worktreeEntries {
		if worktreeMatchesFilter(entry, m.worktreeFilter) {
			filtered = append(filtered, entry)
		}
	}
	m.worktreeFiltered = filtered
	m.restoreWorktreeSelection()
}

func (m *Model) restoreWorktreeSelection() {
	previous := m.worktreeCursor
	m.worktreeCursor = clamp(previous, 0, len(m.worktreeFiltered)-1)
	if len(m.worktreeFiltered) == 0 {
		m.worktreeCursor = 0
	}
	if m.worktreeSelectedID != "" {
		for i := range m.worktreeFiltered {
			if m.worktreeFiltered[i].ID == m.worktreeSelectedID {
				m.worktreeCursor = i
				break
			}
		}
	}
	if len(m.worktreeFiltered) == 0 {
		m.worktreeOffset = 0
	}
	scrollWorktreesToCursor(m)
}

func worktreeMatchesFilter(entry actions.WorktreeView, query string) bool {
	state := strings.Join(worktreeStateLabels(entry), " ")
	haystack := strings.ToLower(strings.Join([]string{
		entry.ID, entry.ProjectLabel, entry.Branch, entry.Path,
		entry.Profile, entry.Harness, string(entry.Classification), state,
	}, "\n"))
	for _, token := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

func worktreeStateLabels(entry actions.WorktreeView) []string {
	labels := []string{string(entry.Classification)}
	switch {
	case !entry.Exists || entry.Classification == worktree.ClassificationMissing:
		labels = append(labels, "missing")
	case entry.Classification == worktree.ClassificationMetadataInvalid ||
		entry.Classification == worktree.ClassificationGitUnavailable ||
		!entry.GitAvailable || !entry.StatusAvailable:
		labels = append(labels, "invalid")
	}
	if entry.Usage.Live || len(entry.LiveSessions) > 0 {
		labels = append(labels, "live")
	}
	if entry.Usage.Looping || entry.LoopCount > 0 {
		labels = append(labels, "looping")
	}
	if entry.Dirty {
		labels = append(labels, "dirty")
	} else if entry.Exists && entry.StatusAvailable {
		labels = append(labels, "clean")
	}
	if entry.Locked {
		labels = append(labels, "locked")
	}
	if entry.ReachableFromSource != nil && *entry.ReachableFromSource {
		labels = append(labels, "merged")
	}
	if len(entry.Warnings) > 0 {
		labels = append(labels, "warning")
	}
	return labels
}

func (m Model) handleWorktreesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.worktreeFilterMode {
		return m.handleWorktreeFilterKey(msg)
	}
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyQ:
		if m.worktreeFilter != "" {
			m.worktreeFilter = ""
			m.captureWorktreeSelection()
			m.reapplyWorktreeFilter()
			return m, nil
		}
		m.mode = ModeList
		m.worktreeFilterMode = false
		return m, nil
	case keySlash:
		m.worktreeFilterMode = true
		return m, nil
	case keyQuestion:
		m.openHelp(ModeWorktrees)
		return m, nil
	case keyR:
		return m.startWorktreeRefresh()
	case keyEnter:
		return m.reopenSelectedWorktree()
	case keyJ, keyDown:
		m.worktreeCursor = clamp(m.worktreeCursor+1, 0, len(m.worktreeFiltered)-1)
	case keyK, keyUp:
		m.worktreeCursor = clamp(m.worktreeCursor-1, 0, len(m.worktreeFiltered)-1)
	case keyG:
		m.worktreeCursor = 0
	case keyGG:
		m.worktreeCursor = len(m.worktreeFiltered) - 1
		if m.worktreeCursor < 0 {
			m.worktreeCursor = 0
		}
	case keyCtrlD:
		m.worktreeCursor = clamp(m.worktreeCursor+halfPageStep(worktreeVisibleHeight(m)), 0, len(m.worktreeFiltered)-1)
	case keyCtrlU:
		m.worktreeCursor = clamp(m.worktreeCursor-halfPageStep(worktreeVisibleHeight(m)), 0, len(m.worktreeFiltered)-1)
	case keyPgDown, keyCtrlF:
		m.worktreeCursor = clamp(m.worktreeCursor+fullPageStep(worktreeVisibleHeight(m)), 0, len(m.worktreeFiltered)-1)
	case keyPgUp, keyCtrlB:
		m.worktreeCursor = clamp(m.worktreeCursor-fullPageStep(worktreeVisibleHeight(m)), 0, len(m.worktreeFiltered)-1)
	case keyO:
		return m.openSelectedWorktree(actions.WorktreeShell)
	case keyOpenEditor:
		return m.openSelectedWorktree(actions.WorktreeEditor)
	case keyCopyPath:
		if path := selectedWorktreePath(m); path != "" {
			return m, copyTextCmd(path)
		}
		return m, nil
	case keyD:
		return m.beginWorktreeRemove(false)
	case keyDeleteWorktreeBranch:
		return m.beginWorktreeRemove(true)
	case keyForgetWorktree:
		return m.beginWorktreeForget()
	default:
		return m, nil
	}
	m.captureWorktreeSelection()
	scrollWorktreesToCursor(&m)
	return m, nil
}

func (m Model) handleWorktreeFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc:
		m.worktreeFilterMode = false
		return m, nil
	case keyEnter:
		m.worktreeFilterMode = false
		return m, nil
	case keyBackspace:
		if runes := []rune(m.worktreeFilter); len(runes) > 0 {
			m.worktreeFilter = string(runes[:len(runes)-1])
			m.captureWorktreeSelection()
			m.reapplyWorktreeFilter()
		}
		return m, nil
	default:
		if text := msg.Key().Text; text != "" {
			m.worktreeFilter += text
			m.captureWorktreeSelection()
			m.reapplyWorktreeFilter()
		}
		return m, nil
	}
}

func (m Model) selectedWorktree() (actions.WorktreeView, bool) {
	if m.worktreeCursor < 0 || m.worktreeCursor >= len(m.worktreeFiltered) {
		return actions.WorktreeView{}, false
	}
	return m.worktreeFiltered[m.worktreeCursor], true
}

func scrollWorktreesToCursor(m *Model) {
	total := len(m.worktreeFiltered)
	height := worktreeVisibleHeight(*m)
	if height <= 0 || total == 0 {
		m.worktreeOffset = 0
		return
	}
	m.worktreeCursor = clamp(m.worktreeCursor, 0, total-1)
	if m.worktreeCursor < m.worktreeOffset {
		m.worktreeOffset = m.worktreeCursor
	}
	for m.worktreeOffset < m.worktreeCursor {
		budget := height
		if m.worktreeOffset > 0 {
			budget--
		}
		if m.worktreeCursor < total-1 {
			budget--
		}
		if budget < 1 {
			budget = 1
		}
		if m.worktreeCursor-m.worktreeOffset+1 <= budget {
			break
		}
		m.worktreeOffset++
	}
	maxOffset := total - 1
	if m.worktreeOffset > maxOffset {
		m.worktreeOffset = maxOffset
	}
	if m.worktreeOffset < 0 {
		m.worktreeOffset = 0
	}
}

// sortStrings is used only for deterministic global warning display.
func sortStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
