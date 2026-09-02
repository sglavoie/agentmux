package rendering

import (
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// DesiredTabTitle computes the tab title the renderer wants for the owning
// window's state, preserving every foreign segment (base, star, mute) from the
// live title.
func DesiredTabTitle(tab *kitty.Tab, st *state.WindowState) string {
	parts := palette.ParseTitleParts(tab.Title)
	parts.State = DesiredIcon(st)
	if st.LoopingSet || st.Looping {
		parts.Looping = st.Looping
	}
	return palette.BuildTitleParts(parts)
}

// TitleOwner resolves the window whose state the tab's title presents. It
// prefers Kitty's own owner, then the tab's active managed window, and finally
// the first managed window in the tab. managed reports whether the daemon
// tracks state for a window ID; a tab with no managed window has no title
// owner and is left alone.
func TitleOwner(tab *kitty.Tab, managed func(int) bool) (*kitty.Window, bool) {
	if owner, ok := kitty.FindTabTitleOwner(tab); ok {
		return owner, true
	}
	if tab == nil || managed == nil {
		return nil, false
	}
	var fallback *kitty.Window
	for i := range tab.Windows {
		win := &tab.Windows[i]
		if !managed(win.ID) {
			continue
		}
		if fallback == nil {
			fallback = win
		}
		if win.IsActive {
			return win, true
		}
	}
	if fallback == nil {
		return nil, false
	}
	return fallback, true
}
