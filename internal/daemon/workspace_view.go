package daemon

import (
	"encoding/json"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Workspace views, seen from the daemon.
//
// The desktop workspace embeds the dashboard and the overview as terminals of
// its own. When one of them launches an agent or focuses a session, the right
// place to show it is the workspace's native view of that session, not the
// terminal the dashboard happens to be drawn in. The surface cannot reach the
// workspace directly and should not need to: it asks the daemon, which knows
// which runtime sessions exist and which subscriber is a workspace, and the
// daemon forwards the request on the companion stream.
//
// The daemon decides nothing about presentation and runs nothing. It checks
// that the request is one a workspace can act on — a known kind, a live
// session on a backend a workspace embeds, absolute paths — and that a
// workspace is connected to receive it; a refusal names which of those failed
// so the surface can fall back to showing the view in its own terminal.

// workspaceViewTitleLimit bounds the label a request may put in a sidebar.
const workspaceViewTitleLimit = 96

// handleWorkspaceView validates one view request and forwards it to every
// connected workspace. The registry lookup may stat a file and so runs off
// stateMu; the lock is taken only to read whether the session is listed.
func (h *Handler) handleWorkspaceView(req protocol.Request) protocol.Response {
	refuse := func(code string) protocol.Response { return protocol.Response{OK: false, Error: code} }
	if req.WorkspaceView == nil {
		return refuse(protocol.WorkspaceViewErrorInvalid)
	}
	view := *req.WorkspaceView
	view.Kind = strings.TrimSpace(view.Kind)
	view.Title = clampWorkspaceViewTitle(view.Title)
	// Only focus_session, an operator's click, may bring a workspace forward.
	view.Raise = false
	if !slices.Contains(protocol.WorkspaceViewKinds, view.Kind) {
		return refuse(protocol.WorkspaceViewErrorInvalid)
	}
	if h.companion == nil {
		return refuse(protocol.WorkspaceViewErrorUnavailable)
	}

	switch view.Kind {
	case protocol.WorkspaceViewSession, protocol.WorkspaceViewPager:
		id, err := runtimesession.ParseID(strings.TrimSpace(view.RuntimeSessionID))
		if err != nil {
			return refuse(protocol.WorkspaceViewErrorInvalid)
		}
		view.RuntimeSessionID = string(id)
		view.Directory, view.Path = "", ""
		if !h.workspaceCanEmbed(id) {
			return refuse(protocol.WorkspaceViewErrorSessionUnknown)
		}
		if view.Kind == protocol.WorkspaceViewPager {
			view.Await = false
			break
		}
		// A workspace lists a session once the daemon holds state for it. One
		// that was just launched may not have reported yet; the caller says so
		// with Await, and the workspace shows it when it appears.
		if !view.Await && !h.runtimeSessionListed(string(id)) {
			return refuse(protocol.WorkspaceViewErrorSessionNotListed)
		}
	case protocol.WorkspaceViewShell:
		if !cleanAbsolutePath(view.Directory) {
			return refuse(protocol.WorkspaceViewErrorInvalid)
		}
		view.RuntimeSessionID, view.Path, view.Await = "", "", false
	case protocol.WorkspaceViewEditor:
		if !cleanAbsolutePath(view.Path) {
			return refuse(protocol.WorkspaceViewErrorInvalid)
		}
		view.RuntimeSessionID, view.Directory, view.Await = "", "", false
	default:
		// The dashboard and the overview are singletons that name nothing.
		view = protocol.WorkspaceViewRequest{Kind: view.Kind}
	}

	data, err := json.Marshal(view)
	if err != nil {
		return refuse(protocol.WorkspaceViewErrorInvalid)
	}
	delivered := h.companion.sendWorkspaceView(data)
	if delivered == 0 {
		return refuse(protocol.WorkspaceViewErrorUnavailable)
	}
	slog.Debug("daemon: workspace view forwarded", "kind", view.Kind, "runtime_session_id", view.RuntimeSessionID, "workspaces", delivered)
	return protocol.Response{OK: true, WorkspaceView: &protocol.WorkspaceViewResult{Delivered: delivered}}
}

// workspaceCanEmbed reports whether id is a live session on a backend whose
// terminal a workspace embeds. A Kitty session is a window of another app; an
// unreadable registry is no evidence that a session exists.
func (h *Handler) workspaceCanEmbed(id runtimesession.ID) bool {
	if h.runtimeDirectory == nil {
		return false
	}
	record, found, _ := h.runtimeDirectory.Lookup(id)
	if !found || record.State != runtimesession.StateLive || record.Target == nil {
		return false
	}
	return record.Target.Instance.Kind != runtimesession.BackendKitty
}

// runtimeSessionListed reports whether the daemon holds state for a runtime
// session, which is exactly when the v2 companion snapshot lists it.
func (h *Handler) runtimeSessionListed(id string) bool {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	slot, bound := h.runtimeRoutes.byRuntime[id]
	if !bound {
		return false
	}
	_, held := h.windowStateLocked(slot)
	return held
}

// cleanAbsolutePath accepts only a path that names one place: absolute, in
// canonical form, and free of control characters.
func cleanAbsolutePath(path string) bool {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	return !strings.ContainsFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// clampWorkspaceViewTitle makes a caller's label safe to show: one line, of
// bounded length.
func clampWorkspaceViewTitle(title string) string {
	title = strings.Join(strings.FieldsFunc(title, func(r rune) bool { return r < 0x20 || r == 0x7f }), " ")
	title = strings.TrimSpace(title)
	if len(title) <= workspaceViewTitleLimit {
		return title
	}
	cut := workspaceViewTitleLimit
	for cut > 0 && !utf8.RuneStart(title[cut]) {
		cut--
	}
	return title[:cut]
}

// sendWorkspaceView hands one encoded view request to every subscriber that
// hosts workspace views and reports how many took it. The sinks are copied
// under hub.mu and written to after it is released, as heartbeat does.
func (hub *companionHub) sendWorkspaceView(data json.RawMessage) int {
	hub.mu.Lock()
	sinks := make([]*ipc.StreamSink, 0, len(hub.sinks))
	for sink, client := range hub.sinks {
		if client.WorkspaceViews {
			sinks = append(sinks, sink)
		}
	}
	hub.mu.Unlock()
	delivered := 0
	for _, sink := range sinks {
		if sink.Send(protocol.FrameWorkspaceView, data) {
			delivered++
		}
	}
	return delivered
}
