package tui

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/session"
)

// restartWithAccountCmd closes and relaunches a session under a different account.
func (m Model) restartWithAccountCmd(acc config.Account) tea.Cmd {
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = actionDoneMsg{err: fmt.Errorf("panic in restartWithAccount: %v", r)}
			}
		}()
		return actionDoneMsg{err: session.RestartWithAccount(m.client, m.targetSession, acc, m.cfg)}
	}
}

type queuedLaunch struct {
	sess  config.Session
	title string
}

func (m *Model) launchBatchCmd(todo []queuedLaunch, quitOnSuccess bool) tea.Cmd {
	if len(todo) == 0 {
		return nil
	}

	m.launchBatchID++
	batchID := m.launchBatchID
	total := len(todo)
	m.launchBatchDone = 0
	m.launchBatchErrors = 0
	m.launchBatchQuitOnSuccess = quitOnSuccess

	// The toast-tick cmd (returned by replaceToastByID when the toast strip
	// was empty) is a tea.Tick that blocks for toastTickInterval before it
	// emits. Keeping it OUTSIDE tea.Sequence below is critical: inside the
	// sequence it would gate the first launch on the tick's 250ms timer.
	var toastCmd tea.Cmd
	startText := fmt.Sprintf("Launching %d session(s)…", total)
	if c := m.replaceToastByID(batchID, Toast{
		Severity: toastInfo,
		Text:     startText,
		Expires:  time.Now().Add(progressToastTTL),
	}); c != nil {
		toastCmd = c
	}

	launchCmds := make([]tea.Cmd, 0, total)
	accounts := m.cfg.Accounts
	client := m.client
	cfg := m.cfg
	for _, q := range todo {
		sess := q.sess
		title := q.title
		launchCmds = append(launchCmds, func() tea.Msg {
			acc, ok := findAccountByName(accounts, sess.EffectiveProfile())
			if !ok {
				err := fmt.Errorf("no matching account for profile %q", sess.EffectiveProfile())
				return launchProgressMsg{batchID: batchID, total: total, title: title, err: err}
			}
			err := session.Launch(client, sess, acc, cfg, session.LaunchPolicyAlwaysNewInstance)
			return launchProgressMsg{batchID: batchID, total: total, title: title, err: err}
		})
	}
	seq := tea.Sequence(launchCmds...)
	if toastCmd != nil {
		return tea.Batch(toastCmd, seq)
	}
	return seq
}

func (m *Model) dashboardLaunchWithAccountCmd(acc config.Account) tea.Cmd {
	sess := session.SessionFromRunning(m.targetSession)
	sess = sessionWithProfile(sess, acc.Name)
	return m.launchBatchCmd([]queuedLaunch{{
		sess:  sess,
		title: configSessionDisplayTitle(sess),
	}}, false)
}

// dispatchPickerLaunch starts a multi-session launch and returns a batch of
// per-session commands. Each command emits a launchProgressMsg the model
// uses to drive the progress toast (1/N, 2/N, …). Returns nil when no session
// is selected so the caller doesn't dispatch a dud cmd. Running picker rows
// are explicit requests for another same-key tab.
func (m *Model) dispatchPickerLaunch() tea.Cmd {
	var todo []queuedLaunch
	for i, sess := range m.pickerSessions {
		if !m.pickerSelected[i] {
			continue
		}
		sess = m.pickerSessionWithResolvedProfile(i)
		todo = append(todo, queuedLaunch{sess: sess, title: configSessionDisplayTitle(sess)})
	}
	return m.launchBatchCmd(todo, true)
}

// handleLaunchProgress updates the progress toast for a finished
// per-session launch and triggers a session list refresh so the newly
// running session appears.
func (m Model) handleLaunchProgress(msg launchProgressMsg) (tea.Model, tea.Cmd) {
	if msg.batchID != m.launchBatchID {
		// Stale launch from a prior batch; ignore so we don't clobber a
		// fresh batch's toast.
		return m, fetchCmd(m.client, m.cfg, m.sortMode)
	}
	m.launchBatchDone++
	var cmds []tea.Cmd
	if msg.err != nil {
		m.launchBatchErrors++
		if c := m.pushToast(Toast{
			Severity: toastError,
			Text:     fmt.Sprintf("launch failed: %s — %s", msg.title, msg.err),
		}); c != nil {
			cmds = append(cmds, c)
		}
	}
	batchDone := m.launchBatchDone >= msg.total
	severity := toastInfo
	if batchDone {
		severity = toastSuccess
	}
	text := fmt.Sprintf("Launched %d/%d · %s", m.launchBatchDone, msg.total, msg.title)
	expires := time.Now().Add(progressToastTTL)
	if batchDone {
		expires = time.Now().Add(defaultToastTTL)
	}
	if c := m.replaceToastByID(msg.batchID, Toast{
		Severity: severity,
		Text:     text,
		Expires:  expires,
	}); c != nil {
		cmds = append(cmds, c)
	}
	if batchDone && m.launchBatchErrors == 0 && m.launchBatchQuitOnSuccess {
		cmds = append(cmds, tea.Quit)
	} else {
		cmds = append(cmds, fetchCmd(m.client, m.cfg, m.sortMode))
	}
	return m, tea.Batch(cmds...)
}

// pickerOpenCmd opens the given session's directory in a plain shell tab.
func pickerOpenCmd(m Model, sess config.Session) tea.Cmd {
	accounts := m.cfg.Accounts
	client := m.client
	cfg := m.cfg
	return func() tea.Msg {
		acc, ok := findAccountByName(accounts, sess.EffectiveProfile())
		if !ok {
			return pickerOpenDoneMsg{err: fmt.Errorf("no matching account for profile %q", sess.EffectiveProfile())}
		}
		return pickerOpenDoneMsg{err: session.OpenDirectory(client, sess, acc, cfg)}
	}
}

// applyPickerFilter returns indices into sessions matching the DSL query.
// Reuses the list-filter DSL (@account, !state, substring) so the same
// shortcut behaves the same in both contexts. An empty query returns the full
// index range. Config sessions are matched as synthetic running sessions —
// !state filters won't match anything since config rows have no live state.
func applyPickerFilter(sessions []config.Session, query string) []int {
	out := make([]int, 0, len(sessions))
	if strings.TrimSpace(query) == "" {
		for i := range sessions {
			out = append(out, i)
		}
		return out
	}
	f := Parse(query)
	for i, cs := range sessions {
		synthetic := session.RunningSession{
			Base:      configSessionDisplayTitle(cs),
			Account:   cs.EffectiveProfile(),
			Profile:   cs.EffectiveProfile(),
			Project:   cs.Title,
			Directory: cs.Directory,
		}
		if f.Match(synthetic, nil, 0) {
			out = append(out, i)
		}
	}
	return out
}

// pickerAccountIndex returns the index of the named account in the slice, or 0 if not found.
func pickerAccountIndex(accounts []config.Account, name string) int {
	for i, a := range accounts {
		if strings.EqualFold(a.Name, name) {
			return i
		}
	}
	return 0
}

func (m Model) pickerEffectiveProfile(origIdx int) string {
	if name := m.pickerProfileOverride[origIdx]; name != "" {
		return name
	}
	if origIdx >= 0 && origIdx < len(m.pickerSessions) {
		return m.pickerSessions[origIdx].EffectiveProfile()
	}
	return ""
}

func (m Model) pickerSessionWithResolvedProfile(origIdx int) config.Session {
	if origIdx < 0 || origIdx >= len(m.pickerSessions) {
		return config.Session{}
	}
	return sessionWithProfile(m.pickerSessions[origIdx], m.pickerEffectiveProfile(origIdx))
}

// loadPickerOverrides loads persisted profile overrides from disk and maps
// them to the current pickerSessions by title. Stale entries (titles no
// longer in the session list) are pruned and the file is re-saved.
func loadPickerOverrides(sessions []config.Session) map[int]string {
	out := make(map[int]string)
	f, err := overrides.Load()
	if err != nil {
		slog.Debug("overrides: load error", "err", err)
		return out
	}
	titleIdx := make(map[string]int, len(sessions))
	for i, s := range sessions {
		titleIdx[s.Title] = i
	}
	var pruned bool
	var fresh []overrides.Entry
	for _, e := range f.Entries {
		idx, ok := titleIdx[e.ProjectTitle]
		if !ok {
			slog.Debug("overrides: pruning stale entry", "title", e.ProjectTitle)
			pruned = true
			continue
		}
		fresh = append(fresh, e)
		out[idx] = e.ProfileName
	}
	if pruned {
		f.Entries = fresh
		if err := overrides.Save(f); err != nil {
			slog.Debug("overrides: save after prune error", "err", err)
		}
	}
	return out
}

// savePickerOverrides writes the current pickerProfileOverride map to disk.
func (m Model) savePickerOverrides() {
	f := overrides.File{Version: 1}
	for idx, profile := range m.pickerProfileOverride {
		if idx >= 0 && idx < len(m.pickerSessions) {
			f.Entries = append(f.Entries, overrides.Entry{
				ProjectTitle: m.pickerSessions[idx].Title,
				ProfileName:  profile,
			})
		}
	}
	if err := overrides.Save(f); err != nil {
		slog.Debug("overrides: save error", "err", err)
	}
}
