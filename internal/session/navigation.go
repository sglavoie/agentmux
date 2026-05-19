package session

import "github.com/sglavoie/agentmux/internal/kitty"

// FindManagedTabRelative returns the previous or next managed tab relative to
// the tab containing windowID, scoped to that Kitty OS window. A positive
// direction moves forward, a negative direction moves backward.
func FindManagedTabRelative(osWindows []kitty.OSWindow, windowID, direction int) (kitty.Tab, bool) {
	if direction == 0 {
		return kitty.Tab{}, false
	}

	tabs, currentTabIdx, ok := tabsForWindowID(osWindows, windowID)
	if !ok {
		return kitty.Tab{}, false
	}

	return managedTabRelative(tabs, currentTabIdx, direction)
}

func tabsForWindowID(osWindows []kitty.OSWindow, windowID int) ([]kitty.Tab, int, bool) {
	for _, osw := range osWindows {
		for i, tab := range osw.Tabs {
			for _, win := range tab.Windows {
				if win.ID == windowID {
					return osw.Tabs, i, true
				}
			}
		}
	}
	return nil, -1, false
}

func managedTabRelative(tabs []kitty.Tab, currentTabIdx, direction int) (kitty.Tab, bool) {
	n := len(tabs)
	if n == 0 || currentTabIdx < 0 || currentTabIdx >= n {
		return kitty.Tab{}, false
	}

	step := 1
	if direction < 0 {
		step = -1
	}

	for offset := 1; offset < n; offset++ {
		idx := (currentTabIdx + (offset * step) + n) % n
		if _, _, ok := tabToRunningSession(tabs[idx], nil); ok {
			return tabs[idx], true
		}
	}

	return kitty.Tab{}, false
}
