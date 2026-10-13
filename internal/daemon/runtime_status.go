package daemon

import (
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// A Kitty window's state is rendered into its tab: the title carries the
// state icon and the tab bar paints the color. A tmux session has no tab, but
// the terminal attached to it has a title and room for one line, and the
// private server builds both from two options on the agent's pane
// (tmux.StatusOption, tmux.StatusColorOption). The daemon writes those
// options, and is the only thing that does: what a terminal shows of a
// session is the same reduced state every other surface shows.
//
// A status is written once the checkpoint holds the state it renders, which
// is what settles a slot of another backend (settleRuntimeSlots), and again
// when the session's title or color changes. Everything a pass has to write
// goes out in one tmux client per server, and a pass with nothing new starts
// none, so a quiet maintenance pass stays the one reconciliation client.

// RuntimeStatus is the rendered status of one runtime session: what the
// terminal attached to it shows as its title and on its status line.
type RuntimeStatus struct {
	Instance runtimesession.BackendInstance
	Pane     string
	// Text is the state icon and title, as a Kitty tab would title it.
	Text string
	// Color is the session's color as #rrggbb, empty for none.
	Color string
}

// RuntimeStatusWriter writes rendered statuses to their backends. It runs off
// stateMu and may start a subprocess: one per backend instance, however many
// statuses it is given.
type RuntimeStatusWriter func(statuses []RuntimeStatus) error

// SetRuntimeStatusWriter installs the write behind rendered statuses. It is
// called once at startup, before any pass runs; without one a session's
// terminal shows no state.
func (h *Handler) SetRuntimeStatusWriter(write RuntimeStatusWriter) {
	h.runtimeStatusWriter = write
}

// NewTmuxRuntimeStatusWriter is the production writer: one client of the
// private tmux server at each instance's address. It never starts a server.
func NewTmuxRuntimeStatusWriter() RuntimeStatusWriter {
	return func(statuses []RuntimeStatus) error {
		var addresses []string
		byAddress := make(map[string][]tmux.Status)
		for _, status := range statuses {
			if status.Instance.Kind != runtimesession.BackendTmux {
				continue
			}
			address := status.Instance.Address
			if _, ok := byAddress[address]; !ok {
				addresses = append(addresses, address)
			}
			byAddress[address] = append(byAddress[address], tmux.Status{
				Pane: status.Pane, Generation: status.Instance.Generation, Text: status.Text, Color: status.Color,
			})
		}
		var errs []error
		for _, address := range addresses {
			server, err := tmux.OpenExisting(address)
			if err == nil {
				err = tmux.NewBackend(server).WriteStatuses(byAddress[address])
			}
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	}
}

// runtimeStatusText is the title a Kitty tab would carry for this state: the
// session's own title with the state the reducer decided in front of it.
func runtimeStatusText(label runtimeLabel, st *state.WindowState) string {
	title := label.title
	if title == "" {
		// The reconciliation pass has not read the title yet.
		title = label.project
	}
	if title == "" {
		return ""
	}
	parts := palette.ParseTitleParts(title)
	parts.State = rendering.DesiredIcon(st)
	parts.Muted = parts.Muted || st.Muted
	if st.LoopingSet || st.Looping {
		parts.Looping = st.Looping
	}
	return palette.BuildTitleParts(parts)
}

// renderRuntimeStatuses writes the status of every tmux session whose settled
// state, title, or color is not what was last written for it. It runs on the
// maintenance schedule, after the checkpoint that settles the slots.
//
// What was written is remembered per session, so an unchanged session costs
// nothing. A failed write is not retried by the next pass, which would be a
// client every pass against a server that keeps refusing: everything is
// written again once, runtimeLabelRetry later.
func (h *Handler) renderRuntimeStatuses(now time.Time) {
	if h.runtimeStatusWriter == nil {
		return
	}
	if !h.runtimeStatusResendAt.IsZero() && !now.Before(h.runtimeStatusResendAt) {
		h.runtimeStatusSent = nil
		h.runtimeStatusResendAt = time.Time{}
	}
	type rendered struct {
		runtime string
		status  RuntimeStatus
	}
	var due []rendered
	h.stateMu.Lock()
	bound := make(map[string]struct{}, len(h.runtimeRoutes.bySlot))
	for slot, b := range h.runtimeRoutes.bySlot {
		if !isRuntimeSyntheticSlot(slot) || b.target.backend != string(runtimesession.BackendTmux) {
			continue
		}
		bound[b.runtime] = struct{}{}
		st, ok := h.windowStateLocked(slot)
		if !ok || st.Dirty {
			// Not settled: the pass that checkpoints this state renders it.
			continue
		}
		label := h.runtimeLabels[b.runtime]
		status := RuntimeStatus{Instance: b.target.instanceOf(), Pane: b.target.pane, Text: runtimeStatusText(label, st)}
		if status.Text == "" {
			continue
		}
		if color, err := palette.Lookup(h.runtimeColorLocked(label)); err == nil {
			status.Color = color.BrightBg
		}
		if sent, ok := h.runtimeStatusSent[b.runtime]; ok && sent == status {
			continue
		}
		due = append(due, rendered{b.runtime, status})
	}
	h.stateMu.Unlock()
	for runtime := range h.runtimeStatusSent {
		if _, ok := bound[runtime]; !ok {
			delete(h.runtimeStatusSent, runtime)
		}
	}
	if len(due) == 0 {
		return
	}
	sort.Slice(due, func(i, j int) bool { return due[i].runtime < due[j].runtime })
	statuses := make([]RuntimeStatus, len(due))
	if h.runtimeStatusSent == nil {
		h.runtimeStatusSent = make(map[string]RuntimeStatus, len(due))
	}
	for i, r := range due {
		statuses[i] = r.status
		h.runtimeStatusSent[r.runtime] = r.status
	}
	if err := h.runtimeStatusWriter(statuses); err != nil {
		slog.Debug("maintenance: runtime session status not written", "err", err)
		h.runtimeStatusResendAt = now.Add(runtimeLabelRetry)
	}
}
