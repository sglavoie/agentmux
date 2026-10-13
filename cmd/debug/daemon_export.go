package debug

import (
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"sort"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
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

func ExportLiveMetadata(osWindows []kitty.OSWindow, snapshot protocol.EventsExport, requestedWindowID int) map[int]protocol.WindowLiveMetadata {
	ids := ExportWindowIDs(snapshot, requestedWindowID)
	if len(ids) == 0 {
		return map[int]protocol.WindowLiveMetadata{}
	}

	sessions := session.ListFromOSWindows(osWindows)
	out := make(map[int]protocol.WindowLiveMetadata, len(ids))
	for _, id := range ids {
		live := protocol.WindowLiveMetadata{}
		osw, tab, win, ok := kitty.FindWindowLocationByID(osWindows, id)
		if !ok {
			out[id] = live
			continue
		}

		live.Found = true
		live.TabID = tab.ID
		live.OSWindowID = osw.ID
		live.TabTitle = tab.Title
		live.WindowTitle = win.Title
		if rs, ok := session.FindByWindowID(sessions, id); ok {
			live.BaseTitle = rs.Base
			live.IconState = rs.State
			live.Looping = rs.Looping
			live.Starred = rs.Starred
			live.Project = rs.Project
			live.Profile = rs.Profile
			live.Harness = rs.Harness
			live.Directory = rs.Directory
		} else {
			parts := palette.ParseTitleParts(tab.Title)
			live.BaseTitle = parts.Base
			live.IconState = parts.State
			live.Looping = parts.Looping
			live.Starred = parts.Starred
			live.Profile = win.UserVars.EffectiveProfile()
			live.Harness = win.UserVars.EffectiveHarness()
			live.Directory = win.UserVars.EffectiveDirectory()
			if live.Directory == "" {
				live.Directory = win.CWD
			}
		}
		out[id] = live
	}
	return out
}

// ExportRuntimeLiveMetadata fills the live half of every exported slot a
// runtime session on a backend other than Kitty holds, from what that
// backend's panes say now. Such a slot is a number only the daemon names, so
// it is joined by its runtime session and never looked up as a Kitty window;
// it has no tab and no OS window, and its icon is the daemon's, not a title's.
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
		if _, exported := into[id]; !exported || !RuntimeSlotOffKitty(slot) {
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

// RuntimeSlotOffKitty reports whether a slot's session runs on a backend
// other than Kitty, where the slot is not a window ID.
func RuntimeSlotOffKitty(slot protocol.RuntimeSlot) bool {
	return slot.Backend != "" && slot.Backend != string(runtimesession.BackendKitty)
}
