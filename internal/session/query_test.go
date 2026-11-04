package session_test

import (
	"github.com/sglavoie/agentmux/internal/session"
	"testing"
)

func TestSessionKey_DifferentHarnessesDoNotCollide(t *testing.T) {
	t.Parallel()

	claudeKey := session.SessionKey("claude-work", "api")
	copilotKey := session.SessionKey("copilot-work", "api")
	if claudeKey == copilotKey {
		t.Errorf("SessionKey collision: claude-work/api and copilot-work/api share key %q", claudeKey)
	}
}

func TestSessionKey_CaseInsensitive(t *testing.T) {
	t.Parallel()

	a := session.SessionKey("Work", "API")
	b := session.SessionKey("work", "api")
	if a != b {
		t.Errorf("SessionKey case sensitivity: %q != %q", a, b)
	}
}
