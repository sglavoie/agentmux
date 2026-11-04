package session

import (
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// liveConversation records the runtime session currently running an indexed
// conversation.
type liveConversation struct {
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
	Here             bool   `json:"-"`
}

// sessionCommandsLiveConversations is replaceable in command tests.
var sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) {
	return liveConversations(sessionCommandsDaemon)
}

// liveConversations uses the daemon's tmux runtime bindings. A conversation
// claimed by more than one live runtime session is left out, so callers never
// focus an arbitrary agent.
func liveConversations(request func(protocol.Request) (protocol.Response, error)) (map[string]liveConversation, bool) {
	response, err := request(protocol.Request{Type: protocol.TypeStateQuery})
	if err != nil || !response.OK {
		return nil, false
	}
	claims := make(map[string][]string)
	for slot, state := range response.States {
		if state.Conversation == nil {
			continue
		}
		binding, ok := response.RuntimeSlots[slot]
		if !ok || binding.Backend != string(runtimesession.BackendTmux) || binding.RuntimeSessionID == "" {
			continue
		}
		key, ok := liveConversationKey(state.Conversation.Harness, state.Conversation.SessionID)
		if !ok {
			continue
		}
		claims[key] = append(claims[key], binding.RuntimeSessionID)
	}
	live := make(map[string]liveConversation)
	for key, ids := range claims {
		if len(ids) == 1 {
			live[key] = liveConversation{RuntimeSessionID: ids[0]}
		}
	}
	return live, true
}

func liveConversationKey(harnessName, sessionID string) (string, bool) {
	identity, ok := conversation.New(harnessName, sessionID)
	if !ok {
		return "", false
	}
	return identity.Harness + ":" + identity.SessionID, true
}

func markLive(views []sessionView, live map[string]liveConversation) {
	for i := range views {
		key, ok := liveConversationKey(views[i].Harness, views[i].ID)
		if !ok {
			continue
		}
		if current, ok := live[key]; ok {
			current.Here = current.RuntimeSessionID != "" && current.RuntimeSessionID == currentRuntimeSession()
			views[i].Live = &current
		}
	}
}
