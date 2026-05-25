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
