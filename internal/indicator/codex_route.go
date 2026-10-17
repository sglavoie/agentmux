package indicator

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

var readCodexHookRuntimes = func() ([]runtimesession.Record, error) {
	store, err := runtimesession.NewStore(runtimesession.DefaultRegistryPath())
	if err != nil {
		return nil, err
	}
	return runtimesession.NewReader(store).Live()
}

// A shared app-server inherits the first client's AMX_RUNTIME_SESSION_ID and
// keeps using it for hooks from other clients. Only an exact live conversation
// binding can replace that inherited identity. Directory, focus and the most
// recently active client cannot distinguish simultaneous conversations.
func resolveCodexHookOwner(sessionID string) (runtimesession.Record, string) {
	if sessionID == "" {
		return runtimesession.Record{}, "codex_conversation_missing"
	}
	records, err := readCodexHookRuntimes()
	if err != nil {
		return runtimesession.Record{}, protocol.OriginReasonRuntimeRegistryUnavailable
	}
	var owner runtimesession.Record
	for _, record := range records {
		if record.State != runtimesession.StateLive || record.Conversation == nil ||
			record.Conversation.Harness != harness.Codex || record.Conversation.SessionID != sessionID {
			continue
		}
		if owner.ID != "" {
			return runtimesession.Record{}, "codex_conversation_ambiguous"
		}
		owner = record
	}
	if owner.ID == "" {
		return runtimesession.Record{}, "codex_conversation_unbound"
	}
	return owner, ""
}
