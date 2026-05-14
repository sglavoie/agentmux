package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/daemon"
	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/internal/palette"
	"github.com/sglavoie/cc-manager-kitty/internal/session"
)

// Mode represents the current UI mode.
type Mode int

const (
	ModeList          Mode = iota
	ModeSearch             // typing a search query
	ModeAccountFilter      // picking an account filter
	ModeConfirm            // y/n confirmation for a destructive action
	ModePicker             // multi-select session launcher
	ModeConfigPicker       // account picker for config override
	ModeHelp               // help overlay
)

// Model is the root Bubble Tea model for the dashboard.
type Model struct {
	// deps
	client   kitty.KittyClient
	cfg      *config.Config
	interval time.Duration
	sortMode string

	// session state
	sessions []session.RunningSession // sorted full list
	filtered []session.RunningSession // sessions after query+filter applied
	cursor   int

	// mode
	mode Mode

	// search
	searchText   string // live input while in ModeSearch
	activeSearch string // committed search applied to filtered

	// account filter
	accounts      []string // unique account names in current sessions
	acctCursor    int      // cursor within accounts slice (ModeAccountFilter)
	activeAccount string   // "" = all, else filter by this account name

	// terminal dimensions
	width  int
	height int

	// last error from Kitty query
	err error

	// confirm / config-picker target
	targetSession session.RunningSession

	// confirm mode
	confirmAction string

	// picker mode
	pickerSessions []config.Session
	pickerRunning  map[string]bool
	pickerSelected map[int]bool
	pickerCursor   int

	// last error from a close/restart action
	actionErr error

	// config picker mode
	configPickerAccounts []config.Account
	configPickerCursor   int
	configPickerCurrent  string

	// activity tracking: window_id → last activity time, sourced from daemon.
	// Empty when the daemon is unreachable; treated as "Active" by stateGroup.
	lastActivity  map[int]time.Time
	idleThreshold time.Duration

	// preview pane
	previewEnabled       bool
	previewWindowID      int
	previewText          string
	previewError         string
	previewNotice        string
	previewNoticeExpires time.Time
}

// NewModel creates a dashboard model.
func NewModel(cfg *config.Config, client kitty.KittyClient) Model {
	interval := time.Duration(cfg.Defaults.RefreshInterval) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	sort := cfg.Defaults.Sort
	if sort == "" {
		sort = "alpha"
	}
	idle := time.Duration(cfg.Defaults.IdleThreshold) * time.Second
	if idle <= 0 {
		idle = 5 * time.Minute
	}
	return Model{
		client:        client,
		cfg:           cfg,
		interval:      interval,
		sortMode:      sort,
		idleThreshold: idle,
	}
}

// Init starts the refresh ticker and fetches the first session list.
func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchCmd(m.client, m.sortMode), tickCmd(m.interval))
}

// Update handles incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.previewEnabled {
			cfg := m.cfg.Defaults.Preview
			if !previewFits(m.width, m.height, previewFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
				m.disablePreviewWithNotice("preview disabled: terminal too small")
			}
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(fetchCmd(m.client, m.sortMode), tickCmd(m.interval))

	case previewTickMsg:
		if !m.previewEnabled {
			return m, nil
		}
		d := time.Duration(m.cfg.Defaults.Preview.RefreshMs) * time.Millisecond
		return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))

	case previewContentMsg:
		if !m.previewEnabled || msg.windowID != m.previewWindowID {
			return m, nil
		}
		if msg.err != nil {
			m.previewError = msg.err.Error()
		} else {
			m.previewError = ""
			m.previewText = msg.text
		}
		return m, nil

	case sessionsMsg:
		m.err = nil
		m.lastActivity = msg.activity
		m.sessions = msg.sessions
		m.accounts = uniqueAccounts(m.sessions)
		m.filtered = applyFilter(m.sessions, m.activeSearch, m.activeAccount)
		m.sortFiltered()
		m.cursor = clamp(m.cursor, 0, len(m.filtered)-1)
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, nil

	case actionDoneMsg:
		m.actionErr = msg.err
		// Trigger a refresh so the list reflects the new state.
		return m, fetchCmd(m.client, m.sortMode)

	case pickerLaunchDoneMsg:
		// Trigger a refresh so newly launched sessions appear.
		return m, fetchCmd(m.client, m.sortMode)

	case pickerOpenDoneMsg:
		if msg.err != nil {
			m.actionErr = msg.err
		}
		return m, fetchCmd(m.client, m.sortMode)

	case focusDoneMsg:
		if msg.err != nil {
			m.actionErr = msg.err
			return m, nil
		}
		return m, tea.Quit

	case peekDoneMsg:
		if msg.err != nil {
			m.actionErr = msg.err
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey dispatches key events by current mode.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeSearch:
		return m.handleSearchKey(msg)
	case ModeAccountFilter:
		return m.handleAccountFilterKey(msg)
	case ModeConfirm:
		return m.handleConfirmKey(msg)
	case ModePicker:
		return m.handlePickerKey(msg)
	case ModeConfigPicker:
		return m.handleConfigPickerKey(msg)
	case ModeHelp:
		return m.handleHelpKey(msg)
	default:
		return m.handleListKey(msg)
	}
}

func (m Model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC, keyQ:
		return m, tea.Quit

	case keyJ, keyDown:
		m.cursor = clamp(m.cursor+1, 0, len(m.filtered)-1)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyK, keyUp:
		m.cursor = clamp(m.cursor-1, 0, len(m.filtered)-1)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyTogglePreview:
		return m.togglePreview()

	case keyG:
		m.cursor = 0
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyGG:
		m.cursor = len(m.filtered) - 1
		if m.cursor < 0 {
			m.cursor = 0
		}
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keySlash:
		m.mode = ModeSearch
		m.searchText = m.activeSearch // pre-populate with current search

	case keyF:
		m.mode = ModeAccountFilter
		m.acctCursor = accountIndex(m.accounts, m.activeAccount)

	case keyE:
		if len(m.filtered) > 0 {
			s := m.filtered[m.cursor]
			return m, peekCmd(m.client, s.WindowID)
		}

	case keySpace, keyEnter:
		if len(m.filtered) > 0 {
			return m, focusCmd(m.client, m.filtered[m.cursor].TabID)
		}

	case keyX:
		if len(m.filtered) > 0 {
			m.targetSession = m.filtered[m.cursor]
			m.confirmAction = "close"
			m.actionErr = nil
			m.mode = ModeConfirm
		}

	case keyR:
		if len(m.filtered) > 0 {
			m.targetSession = m.filtered[m.cursor]
			m.confirmAction = "restart"
			m.actionErr = nil
			m.mode = ModeConfirm
		}

	case keyC:
		if len(m.filtered) > 0 {
			m.targetSession = m.filtered[m.cursor]
			m.configPickerAccounts = m.cfg.Accounts
			m.configPickerCurrent = m.targetSession.Account
			m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.targetSession.Account)
			m.actionErr = nil
			m.mode = ModeConfigPicker
		}

	case keyClearStatus:
		if len(m.filtered) > 0 {
			s := m.filtered[m.cursor]
			m.actionErr = nil
			return m, clearStatusCmd(m.client, s)
		}

	case keyQuestion:
		m.mode = ModeHelp

	case keyL:
		m.pickerSessions = sortPickerSessions(m.cfg.Sessions)
		m.pickerRunning = buildPickerRunning(m.sessions)
		m.pickerSelected = make(map[int]bool)
		m.pickerCursor = 0
		m.mode = ModePicker

	case keyEsc:
		// Clear active filters.
		m.activeSearch = ""
		m.activeAccount = ""
		m.filtered = applyFilter(m.sessions, "", "")
		m.sortFiltered()
		m.cursor = 0
	}

	return m, nil
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		// Cancel: restore previous search without applying new text.
		m.searchText = m.activeSearch
		m.mode = ModeList

	case keyEnter:
		// Apply: commit the live text as the active search.
		m.activeSearch = m.searchText
		m.filtered = applyFilter(m.sessions, m.activeSearch, m.activeAccount)
		m.sortFiltered()
		m.cursor = 0
		m.mode = ModeList

	case keyBackspace:
		if len(m.searchText) > 0 {
			m.searchText = m.searchText[:len(m.searchText)-1]
		}

	default:
		// Append printable character.
		if t := msg.Key().Text; t != "" {
			m.searchText += t
		}
	}

	return m, nil
}

func (m Model) handleAccountFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// accounts list has a virtual "All" entry at index -1 mapped to index 0.
	// acctCursor: 0 = All, 1..N = accounts[0..N-1]
	total := len(m.accounts) + 1 // +1 for "All"

	switch msg.String() {
	case keyEsc:
		m.mode = ModeList

	case keyEnter:
		if m.acctCursor == 0 {
			m.activeAccount = ""
		} else {
			m.activeAccount = m.accounts[m.acctCursor-1]
		}
		m.filtered = applyFilter(m.sessions, m.activeSearch, m.activeAccount)
		m.sortFiltered()
		m.cursor = 0
		m.mode = ModeList

	case keyJ, keyDown:
		m.acctCursor = clamp(m.acctCursor+1, 0, total-1)

	case keyK, keyUp:
		m.acctCursor = clamp(m.acctCursor-1, 0, total-1)

	case keyCtrlC, keyQ:
		return m, tea.Quit
	}

	return m, nil
}

func (m Model) handleConfigPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyQ:
		m.mode = ModeList
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.configPickerCursor = clamp(m.configPickerCursor+1, 0, len(m.configPickerAccounts)-1)
	case keyK, keyUp:
		m.configPickerCursor = clamp(m.configPickerCursor-1, 0, len(m.configPickerAccounts)-1)
	case keyEnter:
		if m.configPickerCursor < len(m.configPickerAccounts) {
			acc := m.configPickerAccounts[m.configPickerCursor]
			m.mode = ModeList
			if acc.Name == m.configPickerCurrent {
				return m, nil
			}
			return m, m.restartWithAccountCmd(acc)
		}
	}
	return m, nil
}

func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyQuestion, keyQ:
		m.mode = ModeList
	case keyCtrlC:
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyY:
		m.mode = ModeList
		switch m.confirmAction {
		case "close":
			return m, closeCmd(m.client, m.targetSession.TabID)
		case "restart":
			return m, restartCmd(m.client, m.targetSession, m.cfg)
		}
	case keyN, keyEsc:
		m.mode = ModeList
	case keyCtrlC, keyQ:
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyQ:
		m.mode = ModeList
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.pickerCursor = clamp(m.pickerCursor+1, 0, len(m.pickerSessions)-1)
	case keyK, keyUp:
		m.pickerCursor = clamp(m.pickerCursor-1, 0, len(m.pickerSessions)-1)
	case keySpace:
		if m.pickerSelected == nil {
			m.pickerSelected = make(map[int]bool)
		}
		m.pickerSelected[m.pickerCursor] = !m.pickerSelected[m.pickerCursor]
	case keyO:
		if len(m.pickerSessions) > 0 {
			return m, pickerOpenCmd(m)
		}
	case keyEnter:
		cmd := pickerLaunchCmd(m)
		m.mode = ModeList
		m.pickerSelected = nil
		return m, cmd
	}
	return m, nil
}

// View renders the full dashboard.
func (m Model) View() tea.View {
	content := m.render()
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// render builds the string content for View.
func (m Model) render() string {
	var sb strings.Builder

	if m.mode == ModePicker {
		// Picker replaces both header and list.
		sb.WriteString(renderPicker(m))
	} else {
		// Header area varies by mode; list is always shown below.
		switch m.mode {
		case ModeSearch:
			sb.WriteString(renderSearch(m))
		case ModeAccountFilter:
			sb.WriteString(renderAccountFilter(m))
		case ModeConfirm:
			sb.WriteString(renderConfirm(m))
		case ModeConfigPicker:
			sb.WriteString(renderConfigPicker(m))
		case ModeHelp:
			sb.WriteString(renderHelp(m))
		default:
			sb.WriteString(renderHeader(m))
		}
		if m.mode == ModeList && m.previewEnabled {
			sb.WriteString(renderSplit(m))
		} else {
			sb.WriteString(renderList(m))
		}
	}

	sb.WriteByte('\n')

	sb.WriteString(renderFooter(m))

	return sb.String()
}

// renderSplit renders the list and preview pane side-by-side or stacked
// according to the configured direction. Falls back to the plain list when
// dimensions or selection make the preview unusable.
func renderSplit(m Model) string {
	cfg := m.cfg.Defaults.Preview
	footerLines := previewFooterLines(m)
	// Vertical space available for the list+preview area (header consumes 1
	// line, then a blank line separates the content from the footer).
	paneHeight := m.height - 1 - 1 - footerLines
	if paneHeight < 4 || m.width < 20 {
		return renderList(m)
	}

	var sess session.RunningSession
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		sess = m.filtered[m.cursor]
	}
	acctColor := buildAccountColorMap(m)

	if cfg.Direction == "vertical" {
		previewHeight := int(float64(paneHeight) * cfg.Ratio)
		if previewHeight < 3 {
			previewHeight = 3
		}
		if previewHeight > paneHeight-2 {
			previewHeight = paneHeight - 2
		}
		listHeight := paneHeight - previewHeight - 1
		listView := clipHeight(renderListAt(m, m.width), listHeight)
		divider := styleDim.Render(strings.Repeat("─", m.width))
		previewView := renderPreview(m.width, previewHeight, sess, m.previewText, m.previewError, acctColor)
		return lipgloss.JoinVertical(lipgloss.Left, listView, divider, previewView) + "\n"
	}

	previewWidth := int(float64(m.width) * cfg.Ratio)
	if previewWidth < 20 {
		previewWidth = 20
	}
	if previewWidth > m.width-20 {
		previewWidth = m.width - 20
	}
	listWidth := m.width - previewWidth - 1
	listView := clipHeight(renderListAt(m, listWidth), paneHeight)
	previewView := renderPreview(previewWidth, paneHeight, sess, m.previewText, m.previewError, acctColor)
	divider := strings.Repeat(styleDim.Render("│")+"\n", paneHeight)
	divider = strings.TrimRight(divider, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, listView, divider, previewView) + "\n"
}

// clipHeight forces s to exactly n visual lines: extra lines are dropped and
// missing lines are padded with empty strings.
func clipHeight(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// previewFooterLines returns the number of lines the footer will occupy in
// ModeList (used for layout math).
func previewFooterLines(m Model) int {
	noticeActive := m.previewNotice != "" &&
		(m.previewNoticeExpires.IsZero() || time.Now().Before(m.previewNoticeExpires))
	if m.previewEnabled {
		// Footer keybindings are suppressed; only the notice (if any) takes a line.
		if noticeActive {
			return 1
		}
		return 0
	}
	if noticeActive {
		return 3
	}
	return 2
}

// togglePreview flips the preview pane on/off. Returns the model and any cmd
// (immediate fetch + ticker) to dispatch on enable.
func (m Model) togglePreview() (tea.Model, tea.Cmd) {
	if m.previewEnabled {
		m.previewEnabled = false
		m.previewText = ""
		m.previewError = ""
		m.previewWindowID = 0
		return m, nil
	}
	cfg := m.cfg.Defaults.Preview
	if !previewFits(m.width, m.height, previewFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		m.previewNotice = "preview unavailable: terminal too small"
		m.previewNoticeExpires = time.Now().Add(2 * time.Second)
		return m, nil
	}
	m.previewEnabled = true
	m.previewText = ""
	m.previewError = ""
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		m.previewWindowID = m.filtered[m.cursor].WindowID
	} else {
		m.previewWindowID = 0
	}
	d := time.Duration(cfg.RefreshMs) * time.Millisecond
	return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))
}

// refreshPreviewForCursor returns an immediate fetch cmd when the cursor
// has moved to a session backed by a different window. Returns nil when
// preview is off or the selection still points at the same window.
func (m *Model) refreshPreviewForCursor() tea.Cmd {
	if !m.previewEnabled {
		return nil
	}
	var winID int
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		winID = m.filtered[m.cursor].WindowID
	}
	if winID == m.previewWindowID {
		return nil
	}
	m.previewWindowID = winID
	m.previewText = ""
	m.previewError = ""
	return fetchPreviewCmd(m.client, winID)
}

// disablePreviewWithNotice turns the preview off and shows a transient footer
// notice. Used both by the resize guard and any other auto-disable path.
func (m *Model) disablePreviewWithNotice(msg string) {
	m.previewEnabled = false
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = 0
	m.previewNotice = msg
	m.previewNoticeExpires = time.Now().Add(2 * time.Second)
}

// fetchCmd queries Kitty for running sessions and the daemon for per-window
// last-activity timestamps. Daemon errors are non-fatal — when unreachable
// the activity map is nil and stateGroup treats every session as Active.
func fetchCmd(client kitty.KittyClient, sortMode string) tea.Cmd {
	return func() tea.Msg {
		sessions, err := session.List(client)
		if err != nil {
			return errMsg{err: err}
		}
		return sessionsMsg{
			sessions: session.SortRunning(sessions, sortMode),
			activity: fetchActivity(),
		}
	}
}

// fetchActivity queries the daemon for last-activity timestamps. Returns nil
// when the daemon is unreachable or the query fails — callers must tolerate.
func fetchActivity() map[int]time.Time {
	resp, err := daemon.NewClient().Send(daemon.Request{Type: daemon.TypeStateQuery})
	if err != nil || resp.Activity == nil {
		return nil
	}
	out := make(map[int]time.Time, len(resp.Activity))
	for id, secs := range resp.Activity {
		out[id] = time.Unix(secs, 0)
	}
	return out
}

// clearStatusCmd wipes a session's tab-title icon (via Kitty) and asks the
// daemon to drop its guard via the user_clear action. The Kitty title write
// is the user-visible result; daemon errors are swallowed because the
// dashboard already updated the title directly.
func clearStatusCmd(client kitty.KittyClient, s session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		newTitle := palette.BuildTitle(s.Base, "", s.Starred)
		err := client.SetTabTitle(kitty.MatchTabByWindowID(s.WindowID), newTitle)
		_, _ = daemon.NewClient().Send(daemon.Request{
			Type:         daemon.TypeStateUpdate,
			Action:       "user_clear",
			WindowID:     s.WindowID,
			SessionTitle: s.Base,
		})
		return actionDoneMsg{err: err}
	}
}

// focusCmd focuses a tab by ID and returns focusDoneMsg.
func focusCmd(client kitty.KittyClient, tabID int) tea.Cmd {
	return func() tea.Msg {
		return focusDoneMsg{err: client.FocusTab(kitty.MatchTabByID(tabID))}
	}
}

// closeCmd closes a tab by ID and returns actionDoneMsg.
func closeCmd(client kitty.KittyClient, tabID int) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{err: session.Close(client, tabID)}
	}
}

// restartCmd closes and relaunches a session and returns actionDoneMsg.
func restartCmd(client kitty.KittyClient, running session.RunningSession, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{err: session.Restart(client, running, cfg)}
	}
}

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

// pickerLaunchCmd launches all selected non-running sessions and returns pickerLaunchDoneMsg.
func pickerLaunchCmd(m Model) tea.Cmd {
	// Capture only what we need so we don't close over the full model.
	sessions := m.pickerSessions
	selected := m.pickerSelected
	running := m.pickerRunning
	accounts := m.cfg.Accounts
	client := m.client
	cfg := m.cfg
	return func() tea.Msg {
		var errs []error
		for i, sess := range sessions {
			if !selected[i] {
				continue
			}
			baseTitle := session.FormatTitle(sess)
			if running[baseTitle] {
				continue // dedup: skip already-running sessions
			}
			var acc config.Account
			for _, a := range accounts {
				if a.Name == sess.Account {
					acc = a
					break
				}
			}
			if err := session.Launch(client, sess, acc, cfg); err != nil {
				errs = append(errs, err)
			}
		}
		return pickerLaunchDoneMsg{errs: errs}
	}
}

// pickerOpenCmd opens the cursored session's directory in a plain shell tab.
func pickerOpenCmd(m Model) tea.Cmd {
	sess := m.pickerSessions[m.pickerCursor]
	accounts := m.cfg.Accounts
	client := m.client
	cfg := m.cfg
	return func() tea.Msg {
		var acc config.Account
		for _, a := range accounts {
			if a.Name == sess.Account {
				acc = a
				break
			}
		}
		return pickerOpenDoneMsg{err: session.OpenDirectory(client, sess, acc, cfg)}
	}
}

// applyFilter returns sessions matching the search query and account filter.
func applyFilter(sessions []session.RunningSession, query, account string) []session.RunningSession {
	query = strings.ToLower(query)
	var out []session.RunningSession
	for _, s := range sessions {
		if account != "" && s.Account != account {
			continue
		}
		if query != "" {
			if !strings.Contains(strings.ToLower(s.Base), query) &&
				!strings.Contains(strings.ToLower(s.Directory), query) &&
				!strings.Contains(strings.ToLower(s.Account), query) {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

// uniqueAccounts returns sorted unique account names from sessions.
func uniqueAccounts(sessions []session.RunningSession) []string {
	seen := make(map[string]bool)
	var accounts []string
	for _, s := range sessions {
		if !seen[s.Account] {
			seen[s.Account] = true
			accounts = append(accounts, s.Account)
		}
	}
	return accounts
}

// pickerAccountIndex returns the index of the named account in the slice, or 0 if not found.
func pickerAccountIndex(accounts []config.Account, name string) int {
	for i, a := range accounts {
		if a.Name == name {
			return i
		}
	}
	return 0
}

// accountIndex returns the acctCursor value (0=All, 1..N=accounts) for the given account name.
func accountIndex(accounts []string, name string) int {
	if name == "" {
		return 0
	}
	for i, a := range accounts {
		if a == name {
			return i + 1
		}
	}
	return 0
}

// peekCmd launches a Kitty overlay pager for the given window's scrollback.
func peekCmd(client kitty.KittyClient, windowID int) tea.Cmd {
	return func() tea.Msg {
		return peekDoneMsg{err: client.LaunchOverlayPager(kitty.MatchWindowByID(windowID))}
	}
}

// sortFiltered applies a stable sort to m.filtered so attention items come
// first, then active, then idle — preserving existing order within each
// group. Uses the model's activity map + idle threshold to bucket sessions
// with no state icon.
func (m Model) sortFiltered() {
	sort.SliceStable(m.filtered, func(i, j int) bool {
		gi := stateGroup(m.filtered[i], m.lastActivity, m.idleThreshold)
		gj := stateGroup(m.filtered[j], m.lastActivity, m.idleThreshold)
		return gi < gj
	})
}

// clamp constrains v to [lo, hi]. Returns lo when hi < lo.
func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
