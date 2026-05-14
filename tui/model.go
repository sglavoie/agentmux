package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/internal/session"
)

// Mode represents the current UI mode.
type Mode int

const (
	ModeList          Mode = iota
	ModeSearch             // typing a search query
	ModeAccountFilter      // picking an account filter
	ModeActions            // action overlay (close / restart / override config)
	ModeConfirm            // y/n confirmation for a destructive action
	ModePicker             // multi-select session launcher
	ModeConfigPicker       // account picker for config override
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

	// actions mode
	actionCursor int
	actionTarget session.RunningSession

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
	return Model{
		client:   client,
		cfg:      cfg,
		interval: interval,
		sortMode: sort,
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
		return m, nil

	case tickMsg:
		return m, tea.Batch(fetchCmd(m.client, m.sortMode), tickCmd(m.interval))

	case sessionsMsg:
		m.err = nil
		m.sessions = msg.sessions
		m.accounts = uniqueAccounts(m.sessions)
		m.filtered = applyFilter(m.sessions, m.activeSearch, m.activeAccount)
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
	case ModeActions:
		return m.handleActionsKey(msg)
	case ModeConfirm:
		return m.handleConfirmKey(msg)
	case ModePicker:
		return m.handlePickerKey(msg)
	case ModeConfigPicker:
		return m.handleConfigPickerKey(msg)
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

	case keyK, keyUp:
		m.cursor = clamp(m.cursor-1, 0, len(m.filtered)-1)

	case keyG:
		m.cursor = 0

	case keyGG:
		m.cursor = len(m.filtered) - 1
		if m.cursor < 0 {
			m.cursor = 0
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

	case keySpace:
		if len(m.filtered) > 0 {
			return m, focusCmd(m.client, m.filtered[m.cursor].TabID)
		}

	case keyEnter, keyA:
		if len(m.filtered) > 0 {
			m.actionTarget = m.filtered[m.cursor]
			m.actionCursor = 0
			m.actionErr = nil
			m.mode = ModeActions
		}

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

func (m Model) handleActionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyQ:
		m.mode = ModeList
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.actionCursor = clamp(m.actionCursor+1, 0, len(actionLabels)-1)
	case keyK, keyUp:
		m.actionCursor = clamp(m.actionCursor-1, 0, len(actionLabels)-1)
	case keyEnter:
		if actionLabels[m.actionCursor] == "override config" {
			m.configPickerAccounts = m.cfg.Accounts
			m.configPickerCurrent = m.actionTarget.Account
			m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.actionTarget.Account)
			m.mode = ModeConfigPicker
		} else {
			m.confirmAction = actionLabels[m.actionCursor]
			m.mode = ModeConfirm
		}
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

func (m Model) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyY:
		m.mode = ModeList
		switch m.confirmAction {
		case "close":
			return m, closeCmd(m.client, m.actionTarget.TabID)
		case "restart":
			return m, restartCmd(m.client, m.actionTarget, m.cfg)
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
		case ModeActions:
			sb.WriteString(renderActions(m))
		case ModeConfirm:
			sb.WriteString(renderConfirm(m))
		case ModeConfigPicker:
			sb.WriteString(renderConfigPicker(m))
		default:
			sb.WriteString(renderHeader(m))
		}
		sb.WriteString(renderList(m))
	}

	// Selected session directory between list and footer (not in picker).
	dirLine := ""
	if m.mode != ModePicker && len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		dirLine = styleDir.Render(" " + m.filtered[m.cursor].Directory)
	}
	sb.WriteString("\n" + dirLine + "\n")

	sb.WriteString(renderFooter(m))

	return sb.String()
}

// fetchCmd queries Kitty for running sessions and returns sessionsMsg or errMsg.
func fetchCmd(client kitty.KittyClient, sortMode string) tea.Cmd {
	return func() tea.Msg {
		sessions, err := session.List(client)
		if err != nil {
			return errMsg{err: err}
		}
		return sessionsMsg{sessions: session.SortRunning(sessions, sortMode)}
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
		return actionDoneMsg{err: session.RestartWithAccount(m.client, m.actionTarget, acc, m.cfg)}
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
