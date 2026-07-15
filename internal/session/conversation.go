package session

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// LiveConversationStatus records whether an authoritative identity has no
// current title owner, exactly one owner, or more than one valid owner. The
// latter is deliberately distinct from absent: continuity callers must not
// turn an ambiguous live identity into a second resume.
type LiveConversationStatus string

const (
	LiveConversationAbsent    LiveConversationStatus = "absent"
	LiveConversationExact     LiveConversationStatus = "exact"
	LiveConversationAmbiguous LiveConversationStatus = "ambiguous"
)

// ResolveLiveConversation joins daemon-derived conversation snapshots with a
// fresh Kitty topology. A matching daemon entry alone is deliberately
// insufficient: a pane can close, move into a split, or be reused before the
// next daemon sweep. The identity resolves only when that same window is still
// the managed title owner of a live tab and its current Kitty harness agrees.
//
// More than one valid owner is ambiguous (for example, a manually duplicated
// harness session), so it returns false rather than selecting an arbitrary tab.
// Callers that will act on the result must obtain osWindows immediately before
// this call, keeping the Kitty re-query at the action boundary.
func ResolveLiveConversation(
	identity conversation.Identity,
	states map[int]daemon.WindowStateSnapshot,
	osWindows []kitty.OSWindow,
) (RunningSession, bool) {
	resolved, status := ResolveLiveConversationStatus(identity, states, osWindows)
	return resolved, status == LiveConversationExact
}

// ResolveLiveConversationStatus is ResolveLiveConversation with an explicit
// ambiguous outcome for action callers. A duplicate valid owner is not merely
// a failed lookup: resuming in that case would create another concurrent
// instance of a conversation whose live ownership is already unclear.
func ResolveLiveConversationStatus(
	identity conversation.Identity,
	states map[int]daemon.WindowStateSnapshot,
	osWindows []kitty.OSWindow,
) (RunningSession, LiveConversationStatus) {
	target, ok := conversation.New(identity.Harness, identity.SessionID)
	if !ok {
		return RunningSession{}, LiveConversationAbsent
	}

	var resolved RunningSession
	found := false
	for windowID, state := range states {
		if state.Conversation == nil || !target.Equal(*state.Conversation) {
			continue
		}
		osWindow, tab, window, ok := kitty.FindWindowLocationByID(osWindows, windowID)
		if !ok {
			// The daemon snapshot is stale: Kitty no longer reports its window.
			continue
		}
		owner, ok := kitty.FindTabTitleOwner(tab)
		if !ok || owner.ID != window.ID {
			// State on a split-pane sibling must never claim the shared tab.
			continue
		}
		if !strings.EqualFold(owner.UserVars.EffectiveHarness(), target.Harness) {
			// User variables can change when a pane is reused before the daemon
			// receives a new hook, making the snapshot owner-mismatched.
			continue
		}

		candidate := runningSessionFromWindow(*tab, *owner)
		candidate.TabNumber = tabNumberByID(osWindow.Tabs, tab.ID)
		if found {
			return RunningSession{}, LiveConversationAmbiguous
		}
		resolved = candidate
		found = true
	}
	if !found {
		return RunningSession{}, LiveConversationAbsent
	}
	return resolved, LiveConversationExact
}
