package debug

import (
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"sort"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
)

// ExportWindowIDs is the sorted union of every window ID a daemon export
// snapshot mentions, plus the caller's own window when it asked for one.
func ExportWindowIDs(snapshot protocol.EventsExport, requestedWindowID int) []int {
	seen := make(map[int]struct{}, len(snapshot.EventsByWindow)+len(snapshot.WindowMetadata)+1)
	ids := make([]int, 0, len(snapshot.EventsByWindow)+len(snapshot.WindowMetadata)+1)
	add := func(id int) {
		if id == 0 {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range snapshot.EventsByWindow {
		add(id)
	}
	for id := range snapshot.WindowMetadata {
		add(id)
	}
	add(requestedWindowID)
	sort.Ints(ids)
	return ids
}

// ExportRuntimeLiveMetadata fills the live half of each exported tmux slot
// from its current pane. The daemon slot joins to a runtime session ID.
// A session whose pane is gone, or whose backend was not read, stays
// not-found.
func ExportRuntimeLiveMetadata(into map[int]protocol.WindowLiveMetadata, running []session.RunningSession, snapshot protocol.EventsExport) {
	byRuntime := make(map[string]session.RunningSession, len(running))
	for _, rs := range running {
		if rs.RuntimeSessionID != "" {
			byRuntime[string(rs.RuntimeSessionID)] = rs
		}
	}
	for id, slot := range snapshot.RuntimeSlots {
		if _, exported := into[id]; !exported || !RuntimeSlotOnTmux(slot) {
			continue
		}
		rs, ok := byRuntime[slot.RuntimeSessionID]
		if !ok {
			continue
		}
		into[id] = protocol.WindowLiveMetadata{
			Found: true, TabTitle: rs.Title, BaseTitle: rs.Base, IconState: rs.State,
			Looping: rs.Looping, Starred: rs.Starred, Project: rs.Project,
			Profile: rs.Profile, Harness: rs.Harness, Directory: rs.Directory,
		}
	}
}

// RuntimeSlotOnTmux reports whether the daemon slot belongs to a tmux session.
func RuntimeSlotOnTmux(slot protocol.RuntimeSlot) bool {
	return slot.Backend == string(runtimesession.BackendTmux)
}
