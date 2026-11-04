package commands

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
)

// liveSessions is one tmux discovery with the daemon state projected onto it.
type liveSessions struct {
	Topology    TopologyFreshness
	Daemon      daemonState
	Sessions    []session.RunningSession
	SourceTabID int
	PortableErr error
}

type daemonState struct {
	Activity map[int]time.Time
	Watchdog map[int]protocol.WatchdogState
	States   map[int]protocol.WindowStateSnapshot
}

func readLiveSessions(cfg *config.Config, sourceWindowID int) (liveSessions, error) {
	sessions, outage := portableSessions(cfg)
	if outage != nil && len(sessions) == 0 && outage.Err != nil {
		return liveSessions{}, outage.Err
	}
	live := liveSessions{
		Topology: TopologyFreshness{Source: TopologyFromDirect, CapturedAt: time.Now()},
		Sessions: sessions,
	}
	if outage != nil {
		live.PortableErr = outage
	}
	if response, err := StateQuery(); err == nil {
		if outage == nil || outage.Lost == 0 {
			overlayPortableState(sessions, &response, &live.Daemon)
		}
	}
	if sourceWindowID > 0 {
		if _, listed := PortableSession(sourceWindowID); listed {
			live.SourceTabID = sourceWindowID
		}
	}
	return live, nil
}

// Refresh discovers tmux sessions and returns the current daemon state.
func Refresh(cfg *config.Config, sortMode string, seq, sourceWindowID int) tea.Cmd {
	return func() tea.Msg {
		live, err := readLiveSessions(cfg, sourceWindowID)
		if err != nil {
			return ErrMsg{Seq: seq, Err: err}
		}
		return SessionsMsg{
			Seq:            seq,
			Sessions:       session.SortRunning(live.Sessions, sortMode),
			Activity:       live.Daemon.Activity,
			Watchdog:       live.Daemon.Watchdog,
			States:         live.Daemon.States,
			SelectionTabID: live.SourceTabID,
			Topology:       live.Topology,
			PortableErr:    live.PortableErr,
		}
	}
}

// RefreshOverview reads the same session state without applying dashboard order.
func RefreshOverview(cfg *config.Config, seq, sourceWindowID int) tea.Cmd {
	return func() tea.Msg {
		live, err := readLiveSessions(cfg, sourceWindowID)
		if err != nil {
			return ErrMsg{Seq: seq, Err: err}
		}
		return SessionsMsg{
			Seq:            seq,
			Sessions:       live.Sessions,
			Activity:       live.Daemon.Activity,
			Watchdog:       live.Daemon.Watchdog,
			States:         live.Daemon.States,
			SelectionTabID: live.SourceTabID,
			Topology:       live.Topology,
			PortableErr:    live.PortableErr,
		}
	}
}

// ApplyDaemonStates overlays daemon-owned state on discovered rows.
func ApplyDaemonStates(sessions []session.RunningSession, states map[int]protocol.WindowStateSnapshot) {
	for i := range sessions {
		state, ok := states[sessions[i].WindowID]
		if !ok {
			sessions[i].State = ""
			sessions[i].AgentTurn = nil
			sessions[i].ASLPhases = nil
			sessions[i].Looping = false
			sessions[i].BusySince = time.Time{}
			sessions[i].PresentationChangedAt = time.Time{}
			sessions[i].Unseen = false
			sessions[i].Reason = ""
			sessions[i].Branch = ""
			sessions[i].Dirty = false
			continue
		}
		sessions[i].State = state.IconState
		sessions[i].AgentTurn = nil
		if state.AgentTurn != nil {
			turn := *state.AgentTurn
			sessions[i].AgentTurn = &turn
		}
		sessions[i].ASLPhases = append([]string(nil), state.ASLPhases...)
		sessions[i].Looping = state.Looping
		sessions[i].Unseen = state.Unseen
		sessions[i].Reason = state.Reason
		sessions[i].Branch = state.Branch
		sessions[i].Dirty = state.Dirty
		sessions[i].BusySince = time.Time{}
		if state.BusySinceAt > 0 {
			sessions[i].BusySince = time.Unix(state.BusySinceAt, 0)
		}
		sessions[i].PresentationChangedAt = time.Time{}
		if state.PresentationChangedAtMS > 0 {
			sessions[i].PresentationChangedAt = time.UnixMilli(state.PresentationChangedAtMS)
		}
	}
}
