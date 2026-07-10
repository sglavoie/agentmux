package daemon

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// WindowState is the daemon's mutable per-window state. Handler.stateMu remains
// the single guard for all access.
type WindowState struct {
	Activity                   time.Time
	LastAction                 string
	LastAccount                string
	LastHarness                string
	LastActiveEvent            string
	CopilotToolPermissionState copilotToolPermissionState
	SubagentCount              int
	PendingReset               resetFlowState
	NotifiedStale              bool
	ContextLimitNotified       bool
	LastSessionTitle           string
	LastPipeMode               bool
	Muted                      bool
	EventLog                   []SessionEvent
	Looping                    bool
	LoopingSet                 bool
	DesiredIcon                string
	StateSet                   bool
	Dirty                      bool
	Seq                        uint64
	RenderFailures             int
	LastRenderError            string
	GuardAction                string
	GuardExpiresAt             time.Time
}

type windowStateUserVar struct {
	Action                     string                     `json:"a,omitempty"`
	GuardAction                string                     `json:"g,omitempty"`
	GuardExpiresAt             int64                      `json:"gx,omitempty"`
	CopilotToolPermissionState copilotToolPermissionState `json:"cp,omitempty"`
	SubagentCount              int                        `json:"sc,omitempty"`
	LastActiveEvent            string                     `json:"lae,omitempty"`
	Looping                    bool                       `json:"l,omitempty"`
	NotifiedStale              bool                       `json:"ns,omitempty"`
	ContextLimitNotified       bool                       `json:"cln,omitempty"`
	PendingReset               resetFlowState             `json:"pr,omitempty"`
	PipeMode                   bool                       `json:"pm,omitempty"`
	Account                    string                     `json:"acct,omitempty"`
	Harness                    string                     `json:"h,omitempty"`
	LastEventAt                int64                      `json:"t,omitempty"`
}

// MarshalUserVar returns compact JSON for Kitty's amx_state user var. The
// event ring buffer is intentionally not persisted.
func (s WindowState) MarshalUserVar() (string, error) {
	uv := windowStateUserVar{
		Action:                     s.LastAction,
		GuardAction:                s.GuardAction,
		CopilotToolPermissionState: s.CopilotToolPermissionState,
		SubagentCount:              s.SubagentCount,
		LastActiveEvent:            s.LastActiveEvent,
		Looping:                    s.Looping,
		NotifiedStale:              s.NotifiedStale,
		ContextLimitNotified:       s.ContextLimitNotified,
		PendingReset:               s.PendingReset,
		PipeMode:                   s.LastPipeMode,
		Account:                    s.LastAccount,
		Harness:                    s.LastHarness,
	}
	if !s.GuardExpiresAt.IsZero() {
		uv.GuardExpiresAt = s.GuardExpiresAt.Unix()
	}
	if !s.Activity.IsZero() {
		uv.LastEventAt = s.Activity.Unix()
	}
	data, err := json.Marshal(uv)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// UnmarshalUserVar restores WindowState from Kitty's amx_state user var.
// The event ring buffer is intentionally left empty.
func (s *WindowState) UnmarshalUserVar(value string) error {
	if strings.TrimSpace(value) == "" {
		*s = WindowState{}
		return nil
	}
	var uv windowStateUserVar
	if err := json.Unmarshal([]byte(value), &uv); err != nil {
		return err
	}
	*s = WindowState{
		LastAction:                 uv.Action,
		GuardAction:                uv.GuardAction,
		CopilotToolPermissionState: uv.CopilotToolPermissionState,
		SubagentCount:              uv.SubagentCount,
		LastActiveEvent:            uv.LastActiveEvent,
		Looping:                    uv.Looping,
		NotifiedStale:              uv.NotifiedStale,
		ContextLimitNotified:       uv.ContextLimitNotified,
		PendingReset:               uv.PendingReset,
		LastPipeMode:               uv.PipeMode,
		LastAccount:                uv.Account,
		LastHarness:                uv.Harness,
	}
	if uv.GuardExpiresAt != 0 {
		s.GuardExpiresAt = time.Unix(uv.GuardExpiresAt, 0)
	}
	if uv.LastEventAt != 0 {
		s.Activity = time.Unix(uv.LastEventAt, 0)
	}
	s.LoopingSet = true
	s.StateSet = true
	s.DesiredIcon = IconState(s.LastAction)
	return nil
}

func (h *Handler) ensureWindowStateLocked(windowID int) *WindowState {
	if h.store == nil {
		h.store = make(map[int]*WindowState)
	}
	st := h.store[windowID]
	if st == nil {
		st = &WindowState{}
		h.store[windowID] = st
	}
	return st
}

func (h *Handler) windowStateLocked(windowID int) (*WindowState, bool) {
	st, ok := h.store[windowID]
	return st, ok
}

func (h *Handler) markWindowDirtyLocked(windowID int) {
	st := h.ensureWindowStateLocked(windowID)
	st.Dirty = true
	st.Seq++
	if h.guard != nil {
		if action, expiresAt, ok := h.guard.Snapshot(windowID); ok {
			st.GuardAction = action
			st.GuardExpiresAt = expiresAt
		} else {
			st.GuardAction = ""
			st.GuardExpiresAt = time.Time{}
		}
	}
	h.signalRender()
}

func (h *Handler) signalRender() {
	if h.renderCh == nil {
		return
	}
	select {
	case h.renderCh <- struct{}{}:
	default:
	}
}

func (h *Handler) rememberKittySocketLocked(windowID int, socket string) {
	socket = strings.TrimSpace(socket)
	// Never let an unusable value (empty or a process-local "fd:" descriptor)
	// replace an existing stable socket in per-window or fallback storage.
	if !isReusableKittySocket(socket) {
		return
	}
	if h.kittySockets == nil {
		h.kittySockets = make(map[int]string)
	}
	h.kittySockets[windowID] = socket
	h.lastKittyListenOn = socket
	h.syncRendererMetadataLocked(windowID)
}

// isReusableKittySocket reports whether socket is a transferable Kitty
// remote-control endpoint that a different process (the detached daemon) can
// dial. It defers to kitty.IsReusableListenAddress so per-window/fallback
// storage and the listener normalization path share one validation rule:
// empty values, process-local "fd:" descriptors, prefix-only "unix:"/"tcp:"
// endpoints, and unknown forms are all rejected rather than guessed at.
func isReusableKittySocket(socket string) bool {
	return kitty.IsReusableListenAddress(socket)
}

func (h *Handler) restoreFromUserVars(windows []kitty.OSWindow, now time.Time) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	for i := range windows {
		for j := range windows[i].Tabs {
			tab := windows[i].Tabs[j]
			for k := range windows[i].Tabs[j].Windows {
				win := windows[i].Tabs[j].Windows[k]
				if win.UserVars.EffectiveProfile() == "" && win.UserVars.EffectiveHarness() == "" {
					continue
				}
				if strings.TrimSpace(win.UserVars.AMXState) == "" {
					continue
				}
				var st WindowState
				if err := st.UnmarshalUserVar(win.UserVars.AMXState); err != nil {
					slog.Warn("renderer: discarded invalid restored state", "window_id", win.ID, "err", err)
					continue
				}
				if desired := desiredIconForState(&st); desired != palette.ParseTitleParts(tab.Title).State {
					slog.Warn("renderer: restored state differs from live title", "window_id", win.ID, "desired_icon", desired, "title_icon", palette.ParseTitleParts(tab.Title).State)
				}
				if st.GuardExpiresAt.IsZero() || !st.GuardExpiresAt.After(now) {
					st.GuardAction = ""
					st.GuardExpiresAt = time.Time{}
				} else if h.guard != nil {
					h.guard.Restore(win.ID, st.GuardAction, st.GuardExpiresAt)
				}
				if st.DesiredIcon == "" && st.LastAction != "" {
					st.DesiredIcon = IconState(st.LastAction)
				}
				st.Dirty = true
				st.Seq++
				h.store[win.ID] = &st
				h.syncRendererMetadataLocked(win.ID)
			}
		}
	}
}
