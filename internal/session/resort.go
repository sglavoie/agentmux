package session

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// Resort moves managed Kitty tabs within each OS window into the order
// produced by SortRunning(mode). Tabs already in the correct relative position
// are skipped. Unmanaged tabs may shift slots as managed tabs slide past them
// via adjacent swaps, but their contents (windows, processes, user vars) are
// preserved. Returns the first error encountered; partial progress is kept.
func Resort(client kitty.KittyClient, mode string) error {
	osWindows, err := client.ListWindows()
	if err != nil {
		return err
	}
	origFocusedTabID := focusedTabID(osWindows)

	for _, osw := range osWindows {
		if err := resortWindow(client, osw.Tabs, mode); err != nil {
			return err
		}
	}

	if origFocusedTabID != 0 {
		_ = client.FocusTab(kitty.MatchTabByID(origFocusedTabID))
	}
	return nil
}

func resortWindow(client kitty.KittyClient, tabs []kitty.Tab, mode string) error {
	desired := SortRunning(runningSessionsFromTabs(tabs), mode)
	if len(desired) < 2 {
		return nil
	}

	desiredIDs := make([]int, len(desired))
	managedSet := make(map[int]bool, len(desired))
	for i, s := range desired {
		desiredIDs[i] = s.TabID
		managedSet[s.TabID] = true
	}

	allTabs := make([]kitty.Tab, len(tabs))
	copy(allTabs, tabs)

	managedSlots := make([]int, 0, len(desired))
	for i, t := range allTabs {
		if managedSet[t.ID] {
			managedSlots = append(managedSlots, i)
		}
	}
	if len(managedSlots) != len(desired) {
		return fmt.Errorf("session: Resort found %d managed tabs in OS window but %d desired", len(managedSlots), len(desired))
	}

	maxMoves := 2 * len(desired)
	moves := 0

	for i := 0; i < len(desired); i++ {
		targetIdx := managedSlots[i]
		curIdx := physicalIndex(allTabs, desiredIDs[i])
		if curIdx < 0 {
			return fmt.Errorf("session: Resort lost track of tab %d", desiredIDs[i])
		}
		delta := targetIdx - curIdx
		if delta == 0 {
			continue
		}
		if moves >= maxMoves {
			return fmt.Errorf("session: Resort exceeded %d move budget", maxMoves)
		}
		if err := client.MoveTab(kitty.MatchTabByID(desiredIDs[i]), delta); err != nil {
			return err
		}
		moves++
		rotateTab(allTabs, curIdx, targetIdx)
	}
	return nil
}

func physicalIndex(tabs []kitty.Tab, id int) int {
	for i, t := range tabs {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// rotateTab moves the tab at curIdx to targetIdx via adjacent swaps,
// mirroring Kitty's TabManager.swap_tabs chain: tabs between the two indices
// each shift by one slot toward curIdx.
func rotateTab(tabs []kitty.Tab, curIdx, targetIdx int) {
	switch {
	case curIdx < targetIdx:
		for i := curIdx; i < targetIdx; i++ {
			tabs[i], tabs[i+1] = tabs[i+1], tabs[i]
		}
	case curIdx > targetIdx:
		for i := curIdx; i > targetIdx; i-- {
			tabs[i], tabs[i-1] = tabs[i-1], tabs[i]
		}
	}
}
