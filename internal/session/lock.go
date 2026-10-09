package session

import "github.com/sglavoie/agentmux/internal/backend"

// Lock marks the session anchored at pane as locked. Locked sessions reject
// close/restart/override from the dashboard until unlocked.
func Lock(term backend.MetadataWriter, pane backend.Ref) error {
	return term.SetMetadata(pane, map[string]string{"amx_locked": "true"})
}

// Unlock clears the lock state on the session anchored at pane. The metadata
// key is set to empty rather than deleted because Kitty's set-user-vars
// surface only supports set, not unset.
func Unlock(term backend.MetadataWriter, pane backend.Ref) error {
	return term.SetMetadata(pane, map[string]string{"amx_locked": ""})
}

// Mute marks the session anchored at pane as muted. Muted sessions render a
// 🔇 glyph on the tab title and suppress all macOS notifications originating
// from that window.
func Mute(term backend.MetadataWriter, pane backend.Ref) error {
	return term.SetMetadata(pane, map[string]string{"cc_mute": "1"})
}

// Unmute clears the mute state on the session anchored at pane. As with
// Unlock, the key is set to empty rather than deleted.
func Unmute(term backend.MetadataWriter, pane backend.Ref) error {
	return term.SetMetadata(pane, map[string]string{"cc_mute": ""})
}
