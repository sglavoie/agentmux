package daemon

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// Showing a runtime session, seen from the daemon.
//
// A click on a notification or a menu row is a request to see one session. A
// Kitty session is a window, and focus_window names it. A runtime session on
// another backend has no window behind its slot, so it is named by its
// runtime session and shown wherever something can show it: a connected
// workspace is asked for its native view of the session, and with none the
// terminal the operator used last on the session's server is switched to it.
//
// The second path selects the session in a terminal; it does not bring that
// terminal's window forward. The daemon knows a tmux client by its tty and
// nothing about the emulator drawing it, so raising the window stays with the
// operator.

// RuntimeSessionSwitcher switches the terminal the operator used last on
// record's backend instance to record's session and names that terminal. It
// runs off stateMu and starts subprocesses. With no terminal attached it
// returns tmux.ErrNoClient; it never starts a server or attaches anything.
type RuntimeSessionSwitcher func(record runtimesession.Record) (client string, err error)

// SetRuntimeSessionSwitcher installs the terminal half of focus_session. It
// is called once at startup; without one a session is shown only by a
// workspace.
func (h *Handler) SetRuntimeSessionSwitcher(switcher RuntimeSessionSwitcher) {
	h.runtimeSwitcher = switcher
}

// NewTmuxRuntimeSwitcher is the production switcher: the private tmux server
// at the record's address, with the registry validation and the owner record
// `amx attach` keeps (tmux.Switcher).
func NewTmuxRuntimeSwitcher(store *runtimesession.Store) RuntimeSessionSwitcher {
	return func(record runtimesession.Record) (string, error) {
		if record.Target == nil || record.Target.Instance.Kind != runtimesession.BackendTmux {
			return "", tmux.ErrNoClient
		}
		server, err := tmux.OpenExisting(record.Target.Instance.Address)
		if err != nil {
			return "", err
		}
		terminal := tmux.NewBackend(server)
		survey, err := terminal.Survey()
		if err != nil {
			return "", err
		}
		client, attached := survey.LastActive()
		if !attached {
			return "", tmux.ErrNoClient
		}
		return client.Name, tmux.Switcher{Store: store, Backend: terminal}.Switch(client.Name, record.ID)
	}
}

// handleFocusSession shows one runtime session on behalf of a companion or a
// command.
func (h *Handler) handleFocusSession(req protocol.Request) protocol.Response {
	if req.FocusSession == nil {
		return protocol.Response{OK: false, Error: protocol.FocusSessionErrorInvalid}
	}
	result, refusal := h.focusRuntimeSession(req.FocusSession.RuntimeSessionID)
	if refusal != "" {
		return protocol.Response{OK: false, Error: refusal}
	}
	return protocol.Response{OK: true, FocusedSession: &result}
}

// focusRuntimeSession shows the runtime session named raw and reports who was
// asked to; the second result is a refusal code, empty on success. The
// registry lookup may stat a file and the switch is a tmux client, so the
// caller must not hold stateMu.
func (h *Handler) focusRuntimeSession(raw string) (protocol.FocusSessionResult, string) {
	id, err := runtimesession.ParseID(strings.TrimSpace(raw))
	if err != nil {
		return protocol.FocusSessionResult{}, protocol.FocusSessionErrorInvalid
	}
	if h.runtimeDirectory == nil {
		return protocol.FocusSessionResult{}, protocol.FocusSessionErrorUnknown
	}
	// A Kitty session is a window of another application, which focus_window
	// names; an unreadable registry is no evidence that a session exists.
	record, found, _ := h.runtimeDirectory.Lookup(id)
	if !found || record.Target == nil || record.Target.Instance.Kind == runtimesession.BackendKitty {
		return protocol.FocusSessionResult{}, protocol.FocusSessionErrorUnknown
	}
	if record.State != runtimesession.StateLive {
		return protocol.FocusSessionResult{}, protocol.FocusErrorStaleTarget
	}
	result := protocol.FocusSessionResult{RuntimeSessionID: string(id)}
	if h.companion != nil {
		// Await covers a session the daemon has not heard from yet: a
		// workspace that does not list it shows it once it does.
		view := protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(id), Await: true, Raise: true}
		if data, err := json.Marshal(view); err == nil {
			if delivered := h.companion.sendWorkspaceView(data); delivered > 0 {
				slog.Debug("daemon: focus_session forwarded", "runtime_session_id", string(id), "workspaces", delivered)
				result.Workspaces = delivered
				return result, ""
			}
		}
	}
	if h.runtimeSwitcher == nil {
		return protocol.FocusSessionResult{}, protocol.FocusSessionErrorNoViewer
	}
	client, err := h.runtimeSwitcher(record)
	var notLive *tmux.NotLiveError
	switch {
	case err == nil:
		slog.Debug("daemon: focus_session switched a terminal", "runtime_session_id", string(id))
		result.Client = client
		return result, ""
	case errors.Is(err, tmux.ErrNoClient), errors.Is(err, tmux.ErrServerNotRunning):
		return protocol.FocusSessionResult{}, protocol.FocusSessionErrorNoViewer
	case errors.As(err, &notLive), errors.Is(err, tmux.ErrPaneGone), errors.Is(err, tmux.ErrPaneExited):
		return protocol.FocusSessionResult{}, protocol.FocusErrorStaleTarget
	default:
		slog.Debug("daemon: focus_session failed", "runtime_session_id", string(id), "failure", tmux.FailureClass(err))
		return protocol.FocusSessionResult{}, protocol.FocusSessionErrorFailed
	}
}

// focusSessionForClick is the notifier's seam onto focusRuntimeSession: a
// clicked notification about a runtime session is the same request.
func (h *Handler) focusSessionForClick(runtimeSessionID string) error {
	_, refusal := h.focusRuntimeSession(runtimeSessionID)
	switch refusal {
	case "":
		return nil
	case protocol.FocusErrorStaleTarget, protocol.FocusSessionErrorUnknown:
		return notification.ErrFocusTargetStale
	case protocol.FocusSessionErrorNoViewer:
		return notification.ErrFocusNoViewer
	default:
		return errors.New(refusal)
	}
}
