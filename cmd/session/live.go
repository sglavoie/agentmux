package session

import (
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// liveConversation is the Kitty tab an indexed conversation is open in right
// now, which is what tells `session continue` focusing apart from resuming.
type liveConversation struct {
	WindowID int `json:"window_id"`
	TabID    int `json:"tab_id"`
}

// sessionCommandsLiveConversations is the seam list and show read live tabs
// through, so their tests never reach the operator's daemon or Kitty.
var sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) {
	return liveConversations(sessionCommandsDaemon, sessionCommandsNewKitty())
}

// liveConversations maps each conversation open in exactly one Kitty tab to
// that tab, by the rule `session continue` focuses with: the daemon's state
// names the conversation, and a fresh Kitty listing still shows that window
// owning its tab under the same harness. A conversation two tabs claim is left
// out, as continue refuses it. The second result is false when the daemon or
// Kitty could not be asked, which callers report as unknown rather than as
// nothing being open; the daemon is never started for this.
func liveConversations(request func(protocol.Request) (protocol.Response, error), client kitty.KittyClient) (map[string]liveConversation, bool) {
	response, err := request(protocol.Request{Type: protocol.TypeStateQuery})
	if err != nil || !response.OK {
		return nil, false
	}
	windows, err := client.ListWindows()
	if err != nil {
		return nil, false
	}
	live := map[string]liveConversation{}
	seen := map[string]bool{}
	for _, state := range response.States {
		if state.Conversation == nil {
			continue
		}
		key, ok := liveConversationKey(state.Conversation.Harness, state.Conversation.SessionID)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		running, status := session.ResolveLiveConversationStatus(*state.Conversation, response.States, windows)
		if status == session.LiveConversationExact {
			live[key] = liveConversation{WindowID: running.WindowID, TabID: running.TabID}
		}
	}
	return live, true
}

// liveConversationKey is the canonical identity both the daemon's state and
// an indexed session are looked up by.
func liveConversationKey(harnessName, sessionID string) (string, bool) {
	identity, ok := conversation.New(harnessName, sessionID)
	if !ok {
		return "", false
	}
	return identity.Harness + ":" + identity.SessionID, true
}

// markLive records on each view the tab its conversation is open in.
func markLive(views []sessionView, live map[string]liveConversation) {
	for i := range views {
		key, ok := liveConversationKey(views[i].Harness, views[i].ID)
		if !ok {
			continue
		}
		if tab, ok := live[key]; ok {
			views[i].Live = &tab
		}
	}
}
