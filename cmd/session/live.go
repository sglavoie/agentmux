package session

import (
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// liveConversation is where an indexed conversation is open right now: a
// Kitty tab, or a session on the tmux backend, which has no tab and is named
// by its runtime session. It is what tells `session continue` focusing apart
// from resuming.
type liveConversation struct {
	WindowID int `json:"window_id"`
	TabID    int `json:"tab_id"`
	// RuntimeSessionID names a session with no Kitty tab; WindowID and TabID
	// are then zero.
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
	// Here is set when that tab is the Kitty window this command runs in, or
	// that session the one it runs in, so the conversation is the one the
	// operator is typing into. It is a label for people only: JSON consumers
	// compare window_id and runtime_session_id themselves.
	Here bool `json:"-"`
}

// sessionCommandsCurrentWindow is the seam the Kitty window this command runs
// in is read through, so tests never inherit the terminal's KITTY_WINDOW_ID.
var sessionCommandsCurrentWindow = cli.WindowIDFromEnv

// sessionCommandsLiveConversations is the seam list and show read live tabs
// through, so their tests never reach the operator's daemon or Kitty.
var sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) {
	return liveConversations(sessionCommandsDaemon, sessionCommandsNewKitty())
}

// liveConversations maps each conversation open in exactly one place to that
// place, by the rule `session continue` focuses with. For a Kitty tab the
// daemon's state names the conversation, and a fresh Kitty listing still
// shows that window owning its tab under the same harness. A session on
// another backend has no window to check: the daemon binds its state slot to
// one runtime session and drops the binding when the session ends, so the
// binding is the evidence. A conversation two places claim is left out, as
// continue refuses it.
//
// The second result is false when the daemon could not be asked, or when
// Kitty could not and the daemon holds a conversation only Kitty can confirm;
// callers report that as unknown rather than as nothing being open. With no
// Kitty conversation in the daemon's state Kitty is not asked at all, which
// is what lets the answer be known in a terminal that is not Kitty. The
// daemon is never started for this.
func liveConversations(request func(protocol.Request) (protocol.Response, error), client kitty.KittyClient) (map[string]liveConversation, bool) {
	response, err := request(protocol.Request{Type: protocol.TypeStateQuery})
	if err != nil || !response.OK {
		return nil, false
	}
	portable := func(slot int) (protocol.RuntimeSlot, bool) {
		bound, ok := response.RuntimeSlots[slot]
		return bound, ok && bound.RuntimeSessionID != "" && bound.Backend != string(runtimesession.BackendKitty)
	}
	// Who claims each conversation: the portable sessions, and whether any
	// Kitty window does.
	type claim struct {
		identity     conversation.Identity
		sessions     []string
		kitty        bool
		foreignKitty bool
	}
	claims := map[string]*claim{}
	needKitty := false
	localSocket := kitty.NormalizedListenAddressFromEnv()
	localStates := make(map[int]protocol.WindowStateSnapshot, len(response.States))
	for slot, state := range response.States {
		foreignKitty := false
		if key, mapped := response.KittySlots[slot]; mapped {
			// Kitty numbers have meaning only inside the instance the caller
			// can list. An unnamed caller cannot prove an aliased pane local.
			if localSocket == "" || key.Socket != localSocket {
				foreignKitty = true
			} else {
				localStates[key.WindowID] = state
			}
		} else {
			localStates[slot] = state
		}
		if state.Conversation == nil {
			continue
		}
		key, ok := liveConversationKey(state.Conversation.Harness, state.Conversation.SessionID)
		if !ok {
			continue
		}
		c := claims[key]
		if c == nil {
			c = &claim{identity: *state.Conversation}
			claims[key] = c
		}
		if bound, ok := portable(slot); ok {
			c.sessions = append(c.sessions, bound.RuntimeSessionID)
		} else if foreignKitty {
			c.foreignKitty = true
		} else {
			c.kitty, needKitty = true, true
		}
	}
	var windows []kitty.OSWindow
	if needKitty {
		if windows, err = client.ListWindows(); err != nil {
			return nil, false
		}
	}
	live := map[string]liveConversation{}
	for key, c := range claims {
		switch {
		case c.foreignKitty:
			// A conversation also open in another Kitty instance cannot be
			// declared unique from this instance's listing.
		case c.kitty && len(c.sessions) == 0:
			running, status := session.ResolveLiveConversationStatus(c.identity, localStates, windows)
			if status == session.LiveConversationExact {
				live[key] = liveConversation{WindowID: running.WindowID, TabID: running.TabID}
			}
		case !c.kitty && len(c.sessions) == 1:
			live[key] = liveConversation{RuntimeSessionID: c.sessions[0]}
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

// markLive records on each view where its conversation is open, noting the
// one open in currentWindow, the window this command runs in (0 when it runs
// outside Kitty), or in the runtime session it runs in.
func markLive(views []sessionView, live map[string]liveConversation, currentWindow int) {
	for i := range views {
		key, ok := liveConversationKey(views[i].Harness, views[i].ID)
		if !ok {
			continue
		}
		if tab, ok := live[key]; ok {
			tab.Here = currentWindow != 0 && tab.WindowID == currentWindow
			if tab.RuntimeSessionID != "" {
				tab.Here = tab.RuntimeSessionID == currentRuntimeSession()
			}
			views[i].Live = &tab
		}
	}
}
