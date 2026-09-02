package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// restartWithAccountCmd closes and relaunches a session under a different account.
func (m Model) restartWithAccountCmd(acc config.Account) tea.Cmd {
	sess := dashboardSessionTemplate(m.cfg, m.targetSession)
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = actionDoneMsg{err: fmt.Errorf("panic in restartWithAccount: %v", r)}
			}
		}()
		_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Kitty: m.client, Config: m.cfg}, actions.Restart, actions.Request{
			Session: &m.targetSession, Project: &sess, Parameters: map[string]string{"profile": acc.Name}, Confirmed: true,
		})
		return actionDoneMsg{err: err}
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

	// The toast-tick cmd (returned by replaceToastByID when the transient
	// status list was empty) is a tea.Tick that blocks for toastTickInterval
	// before it emits. Keeping it OUTSIDE tea.Sequence below is critical:
	// inside the sequence it would gate the first launch on the tick's 250ms timer.
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
	for _, q := range todo {
		sess := q.sess
		title := q.title
		launchCmds = append(launchCmds, func() tea.Msg {
			_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Kitty: m.client, Config: m.cfg}, actions.Launch, actions.Request{Project: &sess})
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
	sess := dashboardSessionTemplate(m.cfg, m.targetSession)
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
	for i := range m.pickerSessions {
		if !m.pickerSelected[i] {
			continue
		}
		sess := m.pickerSessionWithResolvedProfile(i)
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

// pickerOpenTargets returns every selected launcher project in stable picker
// order. With no explicit selection, it preserves the launcher's historical
// behavior by returning only the highlighted project.
func (m Model) pickerOpenTargets() []config.Session {
	targets := make([]config.Session, 0, len(m.pickerSelected))
	for i := range m.pickerSessions {
		if m.pickerSelected[i] {
			targets = append(targets, m.pickerSessionWithResolvedProfile(i))
		}
	}
	if len(targets) > 0 {
		return targets
	}
	if m.pickerCursor < 0 || m.pickerCursor >= len(m.pickerFilteredIdx) {
		return nil
	}
	origIdx := m.pickerFilteredIdx[m.pickerCursor]
	if origIdx < 0 || origIdx >= len(m.pickerSessions) {
		return nil
	}
	return []config.Session{m.pickerSessionWithResolvedProfile(origIdx)}
}

// pickerOpenCmd opens every target directory in a plain shell tab. All
// targets are attempted even when one fails; project-labelled errors are
// joined into the single completion message consumed by the existing open
// handler.
func pickerOpenCmd(m Model, targets []config.Session) tea.Cmd {
	if len(targets) == 0 {
		return nil
	}
	return func() tea.Msg {
		errs := make([]error, 0)
		for i := range targets {
			sess := targets[i]
			_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Kitty: m.client, Config: m.cfg}, actions.OpenDirectory, actions.Request{Project: &sess})
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", configSessionDisplayTitle(sess), err))
			}
		}
		return pickerOpenDoneMsg{err: errors.Join(errs...)}
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
	f := dashboard.Parse(query)
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

// pickerFilteredIndices applies the launcher DSL and cycled default-profile
// filter, then performs a stable partition so pinned projects float above
// unpinned projects. Because pickerSessions is already in profile/title order,
// both partitions retain that existing order.
func (m Model) pickerFilteredIndices(query string) []int {
	indices := applyPickerFilter(m.pickerSessions, query)
	indices = filterPickerIndicesByProfile(m.pickerSessions, indices, m.pickerProfileFilter)
	return pinPickerIndices(indices, m.pickerPinned)
}

func filterPickerIndicesByProfile(sessions []config.Session, indices []int, profile string) []int {
	if profile == "" {
		return indices
	}
	out := make([]int, 0, len(indices))
	for _, idx := range indices {
		if idx < 0 || idx >= len(sessions) {
			continue
		}
		if strings.EqualFold(sessions[idx].EffectiveProfile(), profile) {
			out = append(out, idx)
		}
	}
	return out
}

// pickerProjectProfiles returns unique configured default profiles in the
// order they first appear in pickerSessions. The picker sorts that slice by
// profile before calling this helper, so unused configured profiles are
// omitted and the cycle follows the visible launcher group order.
func pickerProjectProfiles(sessions []config.Session) []string {
	profiles := make([]string, 0)
	seen := make(map[string]bool)
	for _, sess := range sessions {
		profile := sess.EffectiveProfile()
		key := strings.ToLower(profile)
		if profile == "" || seen[key] {
			continue
		}
		seen[key] = true
		profiles = append(profiles, profile)
	}
	return profiles
}

func nextPickerProfileFilter(sessions []config.Session, current string) string {
	profiles := pickerProjectProfiles(sessions)
	if len(profiles) == 0 {
		return ""
	}
	if current == "" {
		return profiles[0]
	}
	for i, profile := range profiles {
		if !strings.EqualFold(profile, current) {
			continue
		}
		if i+1 < len(profiles) {
			return profiles[i+1]
		}
		return ""
	}
	return profiles[0]
}

func pinPickerIndices(indices []int, pinned map[int]bool) []int {
	if len(indices) == 0 || len(pinned) == 0 {
		return indices
	}
	out := make([]int, 0, len(indices))
	for _, idx := range indices {
		if pinned[idx] {
			out = append(out, idx)
		}
	}
	for _, idx := range indices {
		if !pinned[idx] {
			out = append(out, idx)
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

// loadPickerState loads persisted profile overrides and project pins. Profile
// overrides retain their legacy title-only identity; pins use the configured
// project's default (profile, title) identity so duplicate titles across
// profiles remain independent. Stale and duplicate entries are pruned.
func loadPickerState(sessions []config.Session) (map[int]string, map[int]bool) {
	profileOverrides := make(map[int]string)
	pinned := make(map[int]bool)
	f, err := overrides.Load()
	if err != nil {
		slog.Debug("overrides: load error", "err", err)
		return profileOverrides, pinned
	}
	titleIdx := make(map[string]int, len(sessions))
	projectIdx := make(map[string]int, len(sessions))
	for i, s := range sessions {
		titleIdx[s.Title] = i
		projectIdx[session.SessionKey(s.EffectiveProfile(), s.Title)] = i
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
		profileOverrides[idx] = e.ProfileName
	}
	freshPins := make([]overrides.PinnedProject, 0, len(f.PinnedProjects))
	seenPins := make(map[string]bool, len(f.PinnedProjects))
	for _, pin := range f.PinnedProjects {
		key := session.SessionKey(pin.ProfileName, pin.ProjectTitle)
		idx, ok := projectIdx[key]
		if !ok || seenPins[key] {
			slog.Debug("overrides: pruning stale or duplicate pin",
				"profile", pin.ProfileName, "title", pin.ProjectTitle)
			pruned = true
			continue
		}
		seenPins[key] = true
		canonical := overrides.PinnedProject{
			ProfileName:  sessions[idx].EffectiveProfile(),
			ProjectTitle: sessions[idx].Title,
		}
		if canonical != pin {
			pruned = true
		}
		freshPins = append(freshPins, canonical)
		pinned[idx] = true
	}
	if pruned {
		f.Version = overrides.CurrentVersion
		f.Entries = fresh
		f.PinnedProjects = freshPins
		if err := overrides.Save(f); err != nil {
			slog.Debug("overrides: save after prune error", "err", err)
		}
	}
	return profileOverrides, pinned
}

// savePickerState writes profile overrides and pins together so changing one
// launcher preference cannot discard the other.
func (m Model) savePickerState(profileOverrides map[int]string, pinned map[int]bool) error {
	f := overrides.File{Version: overrides.CurrentVersion}
	for idx := range m.pickerSessions {
		if profile := profileOverrides[idx]; profile != "" {
			f.Entries = append(f.Entries, overrides.Entry{
				ProjectTitle: m.pickerSessions[idx].Title,
				ProfileName:  profile,
			})
		}
		if pinned[idx] {
			f.PinnedProjects = append(f.PinnedProjects, overrides.PinnedProject{
				ProfileName:  m.pickerSessions[idx].EffectiveProfile(),
				ProjectTitle: m.pickerSessions[idx].Title,
			})
		}
	}
	return overrides.Save(f)
}

// savePickerOverrides persists an updated one-time profile choice while
// retaining all project pins.
func (m Model) savePickerOverrides() {
	if err := m.savePickerState(m.pickerProfileOverride, m.pickerPinned); err != nil {
		slog.Debug("overrides: save error", "err", err)
	}
}
