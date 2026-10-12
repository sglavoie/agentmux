package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// This file is what the TUI needs in order to run outside Kitty, or beside a
// second backend inside it. A Kitty session is a tab the surface can ask Kitty
// to bring forward; a session on the private tmux server has no tab anywhere,
// so the surface discovers it itself, reads its screen through the backend,
// and shows it by attaching this terminal to it.
//
// Every surface in this tree was written against integer Kitty tab and window
// IDs: the selection, the preview target, the scheduler keys, and the daemon's
// state maps are all keyed by them. A portable session is therefore given a
// process-local integer of its own, far above any Kitty ID, and the daemon's
// state for it is re-keyed onto that integer as it arrives. Nothing outside
// this process ever sees the number; anything sent out names the session's
// backend reference or its runtime session.

// PortableTerminals is the non-Kitty half of the terminals a surface works
// with. The zero value is a Kitty-only surface.
type PortableTerminals struct {
	// Tmux is the private tmux server. Nil means tmux sessions are neither
	// listed nor launched.
	Tmux backend.TargetLauncher
	// Launch is the backend new agents start on. Empty means Kitty.
	Launch runtimesession.BackendKind
	// Registry records tmux launches. Nil means the default registry file.
	Registry *runtimesession.Store
	// Attach builds the command that attaches this terminal to a portable
	// session. It returns when the operator detaches.
	Attach func(session.RunningSession) (*exec.Cmd, error)
	// Desktop hands a view to the desktop workspace hosting this surface, by
	// way of the daemon. Nil for a surface in an ordinary terminal. When it
	// is set, showing a session, a pager, a shell, or an editor asks the
	// workspace for a view of its own, and only a refusal falls back to
	// taking over this terminal.
	Desktop func(protocol.WorkspaceViewRequest) error
	// Send writes input to a portable session's agent with nothing attached,
	// under every rule `amx send` holds it to. Nil means sessions cannot be
	// sent to from this surface.
	Send func(session.RunningSession, SendRequest) (SendReport, error)
}

// Portable is the process's portable terminal set, installed once by the
// command that starts a surface, before its program runs. Like StateQuery and
// LocalKittySocket it is a package seam rather than a parameter: every effect
// builder here takes the Kitty client it has always taken, and the portable
// half rides beside it.
var Portable PortableTerminals

// ErrNoTerminal reports an action that needs a terminal backend this surface
// was not started with.
var ErrNoTerminal = errors.New("no terminal backend is available for this action")

// errNoAttach reports a portable session with no way to show it here.
var errNoAttach = errors.New("this session cannot be attached from here")

// PortableIDBase is the first process-local ID given to a portable session.
// It sits above the daemon's own synthetic slots so the two can never be
// confused in a log.
const PortableIDBase = 1 << 41

var portableIDs = struct {
	sync.Mutex
	byKey map[string]int
	// listed is the last discovery, by process-local ID. It is what lets an
	// effect that was handed only a tab or window ID find the session behind
	// a portable one.
	listed map[int]session.RunningSession
	// order is the same discovery in the order it was listed, which is what
	// an outage keeps showing.
	order []int
	// lost marks the listed sessions whose server is gone. They stay listed
	// until the operator closes them or a server answers again.
	lost map[int]bool
}{byKey: map[string]int{}}

// PortableOutage says why the portable rows of a refresh are not a fresh
// listing: the backend could not be read (Err), or its server is gone and took
// Lost sessions with it. Either way the rows are the last ones listed, kept so
// that a failing backend degrades the list instead of emptying it.
type PortableOutage struct {
	Err  error
	Kept int
	Lost int
}

func (o *PortableOutage) Error() string {
	if o.Err != nil {
		return fmt.Sprintf("tmux is not answering; showing the last %s listed (%v)", countedSessions(o.Kept), o.Err)
	}
	return fmt.Sprintf("the tmux server is gone and %s with it; close a row to dismiss it", countedSessions(o.Lost))
}

func (o *PortableOutage) Unwrap() error { return o.Err }

func countedSessions(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

// PortableLost reports a listed session whose server is gone.
func PortableLost(rs session.RunningSession) bool {
	if !IsPortable(rs) {
		return false
	}
	portableIDs.Lock()
	defer portableIDs.Unlock()
	return portableIDs.lost[rs.WindowID]
}

// dismissPortable drops a lost session from the list: there is nothing left
// to close, so closing it is taking the row away.
func dismissPortable(id int) {
	portableIDs.Lock()
	defer portableIDs.Unlock()
	delete(portableIDs.listed, id)
	delete(portableIDs.lost, id)
	portableIDs.order = slices.DeleteFunc(portableIDs.order, func(listed int) bool { return listed == id })
}

// lastPortableListing is the previous discovery, in its order.
func lastPortableListing() []session.RunningSession {
	portableIDs.Lock()
	defer portableIDs.Unlock()
	sessions := make([]session.RunningSession, 0, len(portableIDs.order))
	for _, id := range portableIDs.order {
		if rs, ok := portableIDs.listed[id]; ok {
			sessions = append(sessions, rs)
		}
	}
	return sessions
}

func recordPortableListing(sessions []session.RunningSession, lost bool) {
	listed := make(map[int]session.RunningSession, len(sessions))
	order := make([]int, 0, len(sessions))
	marks := map[int]bool{}
	for _, rs := range sessions {
		listed[rs.WindowID] = rs
		order = append(order, rs.WindowID)
		if lost {
			marks[rs.WindowID] = true
		}
	}
	portableIDs.Lock()
	portableIDs.listed, portableIDs.order, portableIDs.lost = listed, order, marks
	portableIDs.Unlock()
}

// lostPortableSessions answers an empty listing that follows a listing with
// sessions in it. A server exits with its last session, so an empty listing
// is the ordinary end of closing everything; it is an outage only for the
// sessions the registry does not say were ended or seen to exit. Those ran on
// a server that is gone, and they are returned to stay on the list, marked.
func lostPortableSessions() []session.RunningSession {
	previous := lastPortableListing()
	if len(previous) == 0 {
		return nil
	}
	store := Portable.Registry
	if store == nil {
		var err error
		if store, err = runtimesession.NewStore(runtimesession.DefaultRegistryPath()); err != nil {
			return nil
		}
	}
	registry, err := store.Load()
	if err != nil {
		return nil
	}
	var lost []session.RunningSession
	for _, rs := range previous {
		if rs.RuntimeSessionID == "" {
			continue
		}
		record, err := registry.Get(rs.RuntimeSessionID)
		if err != nil {
			continue
		}
		// Still live means nothing has reconciled the registry yet; the
		// server it names is gone all the same.
		if record.State != runtimesession.StateLive && record.Outcome() != runtimesession.OutcomeLost {
			continue
		}
		rs.State, rs.Looping, rs.AgentTurn, rs.ASLPhases, rs.Unseen, rs.Reason = "error", false, nil, nil, false, ""
		lost = append(lost, rs)
	}
	return lost
}

// PortableSession returns the portable session the last discovery listed
// under id.
func PortableSession(id int) (session.RunningSession, bool) {
	if id < PortableIDBase {
		return session.RunningSession{}, false
	}
	portableIDs.Lock()
	defer portableIDs.Unlock()
	rs, ok := portableIDs.listed[id]
	return rs, ok
}

// portableID returns the process-local ID of a portable session, stable for
// the life of the process. A registered session is keyed by its runtime
// session, which survives nothing but is never reused; an unregistered one by
// its backend and pane.
func portableID(rs session.RunningSession) int {
	key := string(rs.RuntimeSessionID)
	if key == "" {
		key = rs.PaneTarget().String()
	}
	portableIDs.Lock()
	defer portableIDs.Unlock()
	if id, ok := portableIDs.byKey[key]; ok {
		return id
	}
	id := PortableIDBase + len(portableIDs.byKey)
	portableIDs.byKey[key] = id
	return id
}

// IsPortable reports a session hosted outside Kitty.
func IsPortable(rs session.RunningSession) bool {
	return rs.BackendKind() != runtimesession.BackendKitty
}

// TerminalDependencies is the dependency set every terminal action starts
// from: the Kitty client when there is one, and the portable half beside it.
func TerminalDependencies(client kitty.KittyClient, cfg *config.Config) actions.Dependencies {
	deps := actions.Dependencies{
		Kitty:    client,
		Config:   cfg,
		Backend:  Portable.Launch,
		Tmux:     Portable.Tmux,
		Runtimes: Portable.Registry,
	}
	if desktop := Portable.Desktop; desktop != nil {
		// A Kitty launch leaves the operator in the tab it opened. Hosted by
		// the workspace, the equivalent is the new agent's native view, which
		// the workspace shows as soon as the daemon lists the session.
		deps.Launched = func(launched session.LaunchResult) {
			_ = desktop(protocol.WorkspaceViewRequest{
				Kind:             protocol.WorkspaceViewSession,
				RuntimeSessionID: string(launched.RuntimeSessionID),
				Await:            true,
			})
		}
	}
	if Portable.Tmux != nil {
		// The actions' own live-session probe reads Kitty alone. With a
		// portable backend in play, "is anything running here" has to count
		// its sessions too.
		deps.LiveSessions = func(cfg *config.Config) ([]session.RunningSession, error) {
			var running []session.RunningSession
			if client != nil {
				listed, _, err := session.ListWithConfig(client, cfg)
				if err != nil {
					return nil, err
				}
				running = listed
			}
			// This answers "is it running", so rows an outage kept on the
			// list are not an answer: a backend that cannot be read is an
			// error, and a lost session is not running.
			portable, outage := portableSessions(cfg)
			if outage != nil {
				if outage.Err != nil {
					return nil, outage
				}
				portable = nil
			}
			return append(running, portable...), nil
		}
	}
	return deps
}

// LaunchesInKitty reports whether a launch opens a Kitty tab, which takes the
// operator away from this surface. A portable launch leaves them here.
func LaunchesInKitty() bool {
	return Portable.Launch == "" || Portable.Launch == runtimesession.BackendKitty
}

// portableSessions lists the sessions on the portable backends, each carrying
// its process-local ID as both tab and window ID.
//
// A backend that cannot be read, or whose server went away under sessions
// nobody ended, does not empty the list: the last sessions listed come back
// with a PortableOutage saying which of the two happened.
func portableSessions(cfg *config.Config) ([]session.RunningSession, *PortableOutage) {
	if Portable.Tmux == nil {
		return nil, nil
	}
	panes, err := Portable.Tmux.Discover()
	if err != nil {
		kept := lastPortableListing()
		return kept, &PortableOutage{Err: err, Kept: len(kept)}
	}
	if len(panes) == 0 {
		if lost := lostPortableSessions(); len(lost) > 0 {
			recordPortableListing(lost, true)
			return lost, &PortableOutage{Lost: len(lost)}
		}
	}
	sessions := session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, cfg)
	for i := range sessions {
		id := portableID(sessions[i])
		sessions[i].WindowID, sessions[i].TabID = id, id
	}
	recordPortableListing(sessions, false)
	return sessions, nil
}

// overlayPortableState copies the daemon's state for each portable session
// onto the ID this process gave it, and applies it to the rows. The daemon
// keys that state by a slot only it can name; RuntimeSlots is what ties a slot
// to a runtime session, and a runtime session is globally unique, so this
// needs none of the instance proof Kitty window IDs do. A session the daemon
// has not heard from yet simply has no state.
func overlayPortableState(sessions []session.RunningSession, resp *protocol.Response, into *daemonState) {
	if resp == nil || len(sessions) == 0 {
		return
	}
	slots := make(map[string]int, len(resp.RuntimeSlots))
	for slot, bound := range resp.RuntimeSlots {
		slots[bound.RuntimeSessionID] = slot
	}
	states := make(map[int]protocol.WindowStateSnapshot, len(sessions))
	conversations := make(map[conversation.Identity]int)
	defer func() {
		portableConversations.Lock()
		portableConversations.byIdentity = conversations
		portableConversations.Unlock()
	}()
	for _, rs := range sessions {
		slot, ok := slots[string(rs.RuntimeSessionID)]
		if !ok || rs.RuntimeSessionID == "" {
			continue
		}
		if state, ok := resp.States[slot]; ok {
			states[rs.WindowID] = state
			if state.Conversation != nil {
				conversations[*state.Conversation] = rs.WindowID
			}
			if into.States == nil {
				into.States = map[int]protocol.WindowStateSnapshot{}
			}
			into.States[rs.WindowID] = state
		}
		if secs, ok := resp.Activity[slot]; ok {
			if into.Activity == nil {
				into.Activity = map[int]time.Time{}
			}
			into.Activity[rs.WindowID] = time.Unix(secs, 0)
		}
		if watchdog, ok := resp.Watchdog[slot]; ok {
			if into.Watchdog == nil {
				into.Watchdog = map[int]protocol.WatchdogState{}
			}
			into.Watchdog[rs.WindowID] = watchdog
		}
	}
	ApplyDaemonStates(sessions, states)
	// A Kitty row learns its ASL run from a user variable the daemon writes
	// on the window. A portable row has no such variable, so the run comes
	// from the same state entry.
	for i := range sessions {
		if runs := states[sessions[i].WindowID].ASLRunIDs; len(runs) > 0 {
			sessions[i].ASLRunID = runs[0]
		}
	}
}

// AttachDoneMsg reports that an attached view ended: the operator detached,
// or the attach could not start.
type AttachDoneMsg struct{ Err error }

// Attach shows a portable session. Hosted by the desktop workspace, that is
// the workspace's own view of the session, and this surface carries on at
// once. Anywhere else, or when the workspace cannot show it, this terminal is
// handed to the session until the operator detaches.
func Attach(rs session.RunningSession) tea.Cmd {
	here := attachHere(rs)
	desktop := Portable.Desktop
	if desktop == nil || rs.RuntimeSessionID == "" {
		return here
	}
	view := protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(rs.RuntimeSessionID)}
	return func() tea.Msg {
		if err := desktop(view); err != nil {
			return here()
		}
		return AttachDoneMsg{}
	}
}

// attachHere hands this terminal to a portable session until the operator
// detaches, then gives it back to the surface.
func attachHere(rs session.RunningSession) tea.Cmd {
	if Portable.Attach == nil {
		return func() tea.Msg { return AttachDoneMsg{Err: errNoAttach} }
	}
	cmd, err := Portable.Attach(rs)
	if err != nil {
		return func() tea.Msg { return AttachDoneMsg{Err: err} }
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return AttachDoneMsg{Err: err} })
}

// inWorkspace asks the desktop workspace for a utility view and reports done
// when it took it; otherwise it runs here, which shows the same thing in this
// terminal. With no workspace hosting the surface it is here unchanged.
func inWorkspace(view protocol.WorkspaceViewRequest, done func(error) tea.Msg, here tea.Cmd) tea.Cmd {
	desktop := Portable.Desktop
	if desktop == nil {
		return here
	}
	return func() tea.Msg {
		if err := desktop(view); err != nil {
			return here()
		}
		return done(nil)
	}
}

// utilityPath is a directory or file as a workspace view names it: absolute
// and clean, or empty when it cannot be made so.
func utilityPath(path string) string {
	abs, err := filepath.Abs(config.ExpandTilde(path))
	if err != nil {
		return ""
	}
	return abs
}

// ReadScrollback reads the whole scrollback of the pane a window ID names: a
// Kitty window through Kitty, a portable session through its backend.
func ReadScrollback(client kitty.KittyClient, windowID int) (string, error) {
	if rs, ok := PortableSession(windowID); ok {
		if Portable.Tmux == nil {
			return "", ErrNoTerminal
		}
		return Portable.Tmux.Capture(rs.PaneRef(), backend.ExtentAll)
	}
	if client == nil || windowID >= PortableIDBase {
		return "", ErrNoTerminal
	}
	return client.GetText(kitty.MatchWindowByID(windowID), "all")
}

// SessionDetailRequest is the event-log request for the session a window ID
// names. A portable session is named to the daemon by its runtime session.
func SessionDetailRequest(windowID int) protocol.Request {
	req := protocol.Request{Type: protocol.TypeSessionDetail, WindowID: windowID}
	if rs, ok := PortableSession(windowID); ok {
		req.WindowID, req.RuntimeSessionID = 0, string(rs.RuntimeSessionID)
	}
	return req
}

// UtilityDoneMsg reports that a utility view run in this terminal ended.
type UtilityDoneMsg struct{ Err error }

// pagerCommand is the pager a portable scrollback view runs. It is less, the
// program Kitty's own overlay pager runs, opened at the end of the text;
// $PAGER is not consulted because it is routinely set to something that is
// not a general pager.
func pagerCommand(path string) *exec.Cmd {
	// #nosec G204 -- fixed program; path is a file this process just wrote.
	return exec.Command("less", "-R", "+G", path)
}

// peekPortable pages a portable session's scrollback: the text is captured
// through the backend and handed to a pager, in a view of the desktop
// workspace when one hosts this surface and in this terminal otherwise.
func peekPortable(rs session.RunningSession) tea.Cmd {
	done := func(err error) tea.Msg { return PeekDoneMsg{Err: err} }
	here := func() tea.Msg { return PeekDoneMsg{Err: ErrNoTerminal} }
	if capturer := Portable.Tmux; capturer != nil {
		here = tea.Exec(NewScrollbackView(func() (string, error) { return capturer.Capture(rs.PaneRef(), backend.ExtentAll) }), done)
	}
	if rs.RuntimeSessionID == "" {
		return here
	}
	return inWorkspace(protocol.WorkspaceViewRequest{
		Kind:             protocol.WorkspaceViewPager,
		RuntimeSessionID: string(rs.RuntimeSessionID),
		Title:            rs.EffectiveProject(),
	}, done, here)
}

// NewScrollbackView returns a utility view that pages what capture reads. Its
// Run needs the terminal: give it one with SetStdin, SetStdout and SetStderr.
func NewScrollbackView(capture func() (string, error)) tea.ExecCommand {
	return &scrollbackView{capture: capture}
}

// scrollbackView is a utility view that captures a pane's scrollback and
// pages it. The capture happens inside Run, once the surface has released the
// terminal, so one command covers the read, the pager, and the cleanup.
type scrollbackView struct {
	capture        func() (string, error)
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (v *scrollbackView) SetStdin(r io.Reader)  { v.stdin = r }
func (v *scrollbackView) SetStdout(w io.Writer) { v.stdout = w }
func (v *scrollbackView) SetStderr(w io.Writer) { v.stderr = w }

func (v *scrollbackView) Run() error {
	text, err := v.capture()
	if err != nil {
		return err
	}
	file, err := os.CreateTemp("", "amx-scrollback-*.txt")
	if err != nil {
		return err
	}
	path := file.Name()
	defer func() { _ = os.Remove(path) }()
	_, err = file.WriteString(text)
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	pager := pagerCommand(path)
	pager.Stdin, pager.Stdout, pager.Stderr = v.stdin, v.stdout, v.stderr
	return pager.Run()
}

// utilityShell is the program a directory view runs.
func utilityShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// utilityEditor resolves the editor the way the Kitty path does:
// defaults.editor, then $VISUAL, then $EDITOR, then vi.
func utilityEditor(cfg *config.Config) []string {
	candidates := []string{os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi"}
	if cfg != nil {
		candidates = append([]string{cfg.Defaults.Editor}, candidates...)
	}
	for _, candidate := range candidates {
		if fields := strings.Fields(candidate); len(fields) > 0 {
			return fields
		}
	}
	return []string{"vi"}
}

// openedHere is how a utility view opened for a project reports: through
// PickerOpenDoneMsg with Stay set, because nothing took the operator away from
// this surface.
func openedHere(err error) tea.Msg { return PickerOpenDoneMsg{Err: err, Stay: true} }

// RunHere runs argv in dir as a utility view in this terminal: the surface is
// suspended while it runs and done reports how it ended.
func RunHere(dir string, argv []string, done func(error) tea.Msg) tea.Cmd {
	// #nosec G204 -- the operator's configured shell or editor.
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = config.ExpandTilde(dir)
	return tea.ExecProcess(cmd, done)
}

// ShellHere opens a shell in dir: a shell view of the desktop workspace when
// one hosts this surface, and otherwise a shell in this terminal.
func ShellHere(dir string, done func(error) tea.Msg) tea.Cmd {
	here := RunHere(dir, []string{utilityShell()}, done)
	target := utilityPath(dir)
	if target == "" {
		return here
	}
	return inWorkspace(protocol.WorkspaceViewRequest{
		Kind: protocol.WorkspaceViewShell, Directory: target, Title: filepath.Base(target),
	}, done, here)
}

// EditHere opens path in the configured editor: an editor view of the desktop
// workspace when one hosts this surface, and otherwise in this terminal.
func EditHere(cfg *config.Config, path string, done func(error) tea.Msg) tea.Cmd {
	path = config.ExpandTilde(path)
	here := RunHere(path, append(utilityEditor(cfg), path), done)
	target := utilityPath(path)
	if target == "" {
		return here
	}
	return inWorkspace(protocol.WorkspaceViewRequest{
		Kind: protocol.WorkspaceViewEditor, Path: target, Title: filepath.Base(target),
	}, done, here)
}

var portableConversations = struct {
	sync.Mutex
	byIdentity map[conversation.Identity]int
}{}

// LivePortableConversation returns the portable session the daemon last
// reported as running a conversation. It is what lets "continue" show a
// conversation that is already live on a backend Kitty cannot see, instead of
// trying to resume it a second time.
func LivePortableConversation(identity conversation.Identity) (session.RunningSession, bool) {
	portableConversations.Lock()
	id, ok := portableConversations.byIdentity[identity]
	portableConversations.Unlock()
	if !ok {
		return session.RunningSession{}, false
	}
	return PortableSession(id)
}

// OverviewCaptureSources is what a grid of live tiles reads panes through:
// Kitty for Kitty windows and the portable backend for its own panes.
func OverviewCaptureSources(client kitty.KittyClient) CaptureSources {
	sources := CaptureSources{Kitty: client}
	if Portable.Tmux != nil {
		sources.Panes = map[runtimesession.BackendKind]backend.Capturer{runtimesession.BackendTmux: Portable.Tmux}
	}
	return sources
}
