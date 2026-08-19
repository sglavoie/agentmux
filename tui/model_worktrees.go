package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/session"
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

func (m Model) openSelectedWorktree(action actions.ID) (tea.Model, tea.Cmd) {
	if m.worktreeCursor < 0 || m.worktreeCursor >= len(m.worktreeFiltered) {
		return m, nil
	}
	entry := m.worktreeFiltered[m.worktreeCursor]
	if !entry.Exists {
		return m, nil
	}
	m.actionErr = nil
	return m, m.runWorktreeActionCmd(action, actions.Request{Worktree: &entry.Entry})
}

func worktreeLifecycleActionCmd(m Model, action actions.ID, request actions.Request) tea.Cmd {
	readers := m.transcriptReaders
	if readers == nil {
		readers = buildTranscriptReaders()
	}
	return func() tea.Msg {
		result, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{
			Kitty: m.client, Config: m.cfg, Readers: readers,
		}, action, request)
		return worktreeActionDoneMsg{action: action, result: result, err: err}
	}
}

func (m Model) selectedWorktree() (actions.WorktreeView, bool) {
	if m.worktreeCursor < 0 || m.worktreeCursor >= len(m.worktreeFiltered) {
		return actions.WorktreeView{}, false
	}
	return m.worktreeFiltered[m.worktreeCursor], true
}

func (m Model) reopenSelectedWorktree() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedWorktree()
	if !ok {
		return m, nil
	}
	m.worktreeActionTarget = &entry
	return m.reopenWorktreeWithParameters(nil)
}

func (m Model) reopenWorktreeWithParameters(parameters map[string]string) (tea.Model, tea.Cmd) {
	if m.worktreeActionTarget == nil || m.worktreeActionInFlight {
		return m, nil
	}
	request := actions.Request{Worktree: &m.worktreeActionTarget.Entry, Parameters: parameters}
	availability := sharedActionRegistry.Availability(actions.Dependencies{Config: m.cfg}, actions.WorktreeReopen, request)
	if !availability.Available {
		m.actionErr = &actions.Error{Code: "unavailable", Action: actions.WorktreeReopen, Message: availability.Reason}
		m.worktreeActionTarget = nil
		return m, nil
	}
	m.mode = ModeWorktrees
	m.actionErr = nil
	m.worktreeActionInFlight = true
	return m, m.runWorktreeActionCmd(actions.WorktreeReopen, request)
}

func (m Model) beginWorktreeRemove(deleteBranch bool) (tea.Model, tea.Cmd) {
	entry, ok := m.selectedWorktree()
	if !ok || m.worktreeActionInFlight {
		return m, nil
	}
	parameters := map[string]string{}
	if deleteBranch {
		parameters["delete_branch"] = "true"
		if entry.Exists {
			switch {
			case entry.ReachableFromSource == nil:
				return m.worktreeDisabledToast("merge status against the recorded source branch is unavailable")
			case !*entry.ReachableFromSource:
				return m.worktreeDisabledToast("branch is not merged into the recorded source branch")
			}
		}
	}
	request := actions.Request{Worktree: &entry.Entry, Parameters: parameters}
	availability := sharedActionRegistry.Availability(actions.Dependencies{Config: m.cfg}, actions.WorktreeRemove, request)
	if !availability.Available {
		return m.worktreeDisabledToast(availability.Reason)
	}
	m.worktreeActionTarget = &entry
	m.confirmReturnMode = ModeWorktrees
	if deleteBranch {
		m.confirmAction = "worktree delete branch"
		m.confirmConsequence = fmt.Sprintf("Deletes branch %q only after Git proves it is merged into recorded target %q.", entry.Branch, displayWorktreeSource(entry))
	} else {
		m.confirmAction = "worktree remove"
		m.confirmConsequence = fmt.Sprintf("Removes the checkout and metadata; branch %q is retained.", entry.Branch)
	}
	m.mode = ModeConfirm
	m.actionErr = nil
	return m, nil
}

func (m Model) beginWorktreeForget() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedWorktree()
	if !ok || m.worktreeActionInFlight {
		return m, nil
	}
	request := actions.Request{Worktree: &entry.Entry}
	availability := sharedActionRegistry.Availability(actions.Dependencies{Config: m.cfg}, actions.WorktreeForget, request)
	if !availability.Available {
		return m.worktreeDisabledToast(availability.Reason)
	}
	m.worktreeActionTarget = &entry
	m.confirmReturnMode = ModeWorktrees
	m.confirmAction = "worktree forget"
	m.confirmConsequence = "Deletes only Agent Mux sidecar metadata; the checkout and Git branches are unchanged."
	m.mode = ModeConfirm
	m.actionErr = nil
	return m, nil
}

func (m Model) worktreeDisabledToast(reason string) (tea.Model, tea.Cmd) {
	m.actionErr = nil
	cmd := m.pushToast(Toast{Severity: toastWarning, Text: "Unavailable: " + reason})
	return m, cmd
}

func displayWorktreeSource(entry actions.WorktreeView) string {
	if strings.TrimSpace(entry.SourceBranch) == "" {
		return "(unknown)"
	}
	return entry.SourceBranch
}

func (m Model) confirmedWorktreeMutation(deleteBranch bool) (tea.Model, tea.Cmd) {
	if m.worktreeActionTarget == nil {
		return m, nil
	}
	parameters := map[string]string{}
	if deleteBranch {
		parameters["delete_branch"] = "true"
	}
	request := actions.Request{Worktree: &m.worktreeActionTarget.Entry, Parameters: parameters, Confirmed: true}
	m.actionErr = nil
	m.worktreeActionInFlight = true
	return m, m.runWorktreeActionCmd(actions.WorktreeRemove, request)
}

func (m Model) confirmedWorktreeForget() (tea.Model, tea.Cmd) {
	if m.worktreeActionTarget == nil {
		return m, nil
	}
	request := actions.Request{Worktree: &m.worktreeActionTarget.Entry, Confirmed: true}
	m.actionErr = nil
	m.worktreeActionInFlight = true
	return m, m.runWorktreeActionCmd(actions.WorktreeForget, request)
}

func (m Model) handleWorktreeActionDone(msg worktreeActionDoneMsg) (tea.Model, tea.Cmd) {
	m.worktreeActionInFlight = false
	if msg.action == actions.WorktreeReopen {
		return m.handleWorktreeReopenDone(msg)
	}
	if msg.action == actions.WorktreeRemove || msg.action == actions.WorktreeForget {
		return m.handleWorktreeMutationDone(msg)
	}
	if msg.err != nil {
		m.actionErr = msg.err
		return m, nil
	}
	m.actionErr = nil
	text := msg.result.Message
	if text == "" {
		text = "Worktree action completed"
	}
	return m, m.pushToast(Toast{Severity: toastSuccess, Text: text, Expires: time.Now().Add(progressToastTTL)})
}

func (m Model) handleWorktreeReopenDone(msg worktreeActionDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil {
		m.actionErr = nil
		m.worktreeActionTarget = nil
		text := msg.result.Message
		if text == "" {
			text = "Worktree reopened"
		}
		return m, m.pushToast(Toast{Severity: toastSuccess, Text: text})
	}
	var actionErr *actions.Error
	if errors.As(msg.err, &actionErr) {
		switch actionErr.Code {
		case "ambiguous_live_sessions":
			m.openWorktreeSessionPicker(actionErr.Candidates)
			return m, nil
		case "profile_required":
			m.openWorktreeProfilePicker()
			return m, nil
		}
	}
	m.mode = ModeWorktrees
	m.actionErr = msg.err
	m.worktreeActionTarget = nil
	return m, nil
}

func (m *Model) openWorktreeSessionPicker(candidates []actions.Candidate) {
	m.worktreeSessionCandidates = nil
	if m.worktreeActionTarget != nil {
		byID := make(map[string]actions.WorktreeLiveSession, len(m.worktreeActionTarget.LiveSessions))
		for _, candidate := range m.worktreeActionTarget.LiveSessions {
			byID[candidate.ID] = candidate
		}
		for _, candidate := range candidates {
			if live, ok := byID[candidate.ID]; ok {
				m.worktreeSessionCandidates = append(m.worktreeSessionCandidates, live)
			} else {
				m.worktreeSessionCandidates = append(m.worktreeSessionCandidates, actions.WorktreeLiveSession{ID: candidate.ID, Project: candidate.DisplayName, Directory: candidate.CanonicalPath})
			}
		}
	}
	m.worktreeSessionCursor = 0
	m.worktreeSessionOffset = 0
	m.actionErr = nil
	m.mode = ModeWorktreeSessionPicker
}

func (m *Model) openWorktreeProfilePicker() {
	m.configPickerAccounts = append(m.configPickerAccounts[:0], m.cfg.Accounts...)
	m.configPickerPurpose = ConfigPickerWorktreeReopen
	m.configPickerReturnMode = ModeWorktrees
	m.configPickerProjectIdx = -1
	m.configPickerCurrent = ""
	m.configPickerCursor = 0
	m.targetSession = sessionForWorktreePicker(m.worktreeActionTarget)
	if m.worktreeActionTarget != nil {
		m.configPickerCurrent = m.worktreeActionTarget.Profile
		m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.configPickerCurrent)
	}
	m.actionErr = nil
	m.mode = ModeConfigPicker
}

func sessionForWorktreePicker(target *actions.WorktreeView) session.RunningSession {
	if target == nil {
		return session.RunningSession{}
	}
	return session.RunningSession{Base: target.ProjectLabel, Project: target.ProjectLabel, Profile: target.Profile, Harness: target.Harness, Directory: target.Path}
}

func (m Model) handleWorktreeSessionPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyQ:
		m.mode = ModeWorktrees
		m.worktreeSessionCandidates = nil
		m.worktreeActionTarget = nil
		return m, nil
	case keyJ, keyDown:
		m.worktreeSessionCursor = clamp(m.worktreeSessionCursor+1, 0, len(m.worktreeSessionCandidates)-1)
	case keyK, keyUp:
		m.worktreeSessionCursor = clamp(m.worktreeSessionCursor-1, 0, len(m.worktreeSessionCandidates)-1)
	case keyEnter:
		if m.worktreeSessionCursor < 0 || m.worktreeSessionCursor >= len(m.worktreeSessionCandidates) {
			return m, nil
		}
		id := m.worktreeSessionCandidates[m.worktreeSessionCursor].ID
		m.worktreeSessionCandidates = nil
		return m.reopenWorktreeWithParameters(map[string]string{"session": id})
	}
	scrollWorktreeSessionsToCursor(&m)
	return m, nil
}

func scrollWorktreeSessionsToCursor(m *Model) {
	total := len(m.worktreeSessionCandidates)
	visible := worktreeSessionVisibleRows(*m)
	if total == 0 || visible <= 0 {
		m.worktreeSessionOffset = 0
		return
	}
	m.worktreeSessionCursor = clamp(m.worktreeSessionCursor, 0, total-1)
	if m.worktreeSessionCursor < m.worktreeSessionOffset {
		m.worktreeSessionOffset = m.worktreeSessionCursor
	}
	if m.worktreeSessionCursor >= m.worktreeSessionOffset+visible {
		m.worktreeSessionOffset = m.worktreeSessionCursor - visible + 1
	}
}

func (m Model) handleWorktreeMutationDone(msg worktreeActionDoneMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeWorktrees
	m.worktreeActionTarget = nil
	if msg.err != nil {
		var partial *actions.PartialResultError
		if errors.As(msg.err, &partial) {
			m.actionErr = nil
			text := "Partial cleanup: " + worktreeMutationSummary(msg.result) + "; recovery required: " + msg.err.Error()
			return m.refreshWorktreesWithToast(Toast{Severity: toastWarning, Text: text})
		}
		m.actionErr = msg.err
		updated, refresh := m.startWorktreeRefresh()
		return updated, refresh
	}
	m.actionErr = nil
	text := msg.result.Message
	if text == "" {
		text = "Worktree lifecycle action completed"
	}
	return m.refreshWorktreesWithToast(Toast{Severity: toastSuccess, Text: text})
}

func worktreeMutationSummary(result actions.Result) string {
	mutation, ok := result.Data["mutation"].(worktree.MutationResult)
	if !ok {
		return "durable state changed"
	}
	var steps []string
	if mutation.CheckoutRemoved {
		steps = append(steps, "checkout removed")
	}
	if mutation.BranchDeleted {
		steps = append(steps, "branch deleted")
	}
	if mutation.MetadataForgotten {
		steps = append(steps, "metadata forgotten")
	}
	if len(steps) == 0 {
		return "no durable step reported"
	}
	return strings.Join(steps, ", ")
}

func (m Model) refreshWorktreesWithToast(toast Toast) (tea.Model, tea.Cmd) {
	updated, refresh := m.startWorktreeRefresh()
	m = updated.(Model)
	toastCmd := m.pushToast(toast)
	return m, tea.Batch(refresh, toastCmd)
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
