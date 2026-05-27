package session

import "github.com/sglavoie/agentmux/internal/kitty"

// Lock marks the session anchored at windowID as locked. Locked sessions
// reject close/restart/override from the dashboard until unlocked.
func Lock(client kitty.KittyClient, windowID int) error {
	return client.SetUserVars(kitty.MatchWindowByID(windowID), map[string]string{"amx_locked": "true"})
}

// Unlock clears the lock state on the session anchored at windowID. The user
// var is set to empty rather than deleted because Kitty's set-user-vars surface
// only supports set, not unset.
func Unlock(client kitty.KittyClient, windowID int) error {
	return client.SetUserVars(kitty.MatchWindowByID(windowID), map[string]string{"amx_locked": ""})
}

// Mute marks the session anchored at windowID as muted. Muted sessions render
// a 🔇 glyph on the tab title and suppress all macOS notifications originating
// from that window.
func Mute(client kitty.KittyClient, windowID int) error {
	return client.SetUserVars(kitty.MatchWindowByID(windowID), map[string]string{"cc_mute": "1"})
}

// Unmute clears the mute state on the session anchored at windowID. As with
// Unlock, the user var is set to empty rather than deleted.
func Unmute(client kitty.KittyClient, windowID int) error {
	return client.SetUserVars(kitty.MatchWindowByID(windowID), map[string]string{"cc_mute": ""})
}
