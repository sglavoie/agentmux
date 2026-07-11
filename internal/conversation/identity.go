// Package conversation defines the stable identity shared by transcript,
// daemon, and session-continuity features. A harness session ID is only unique
// within its harness, so callers must always carry both fields together.
package conversation

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

// Identity identifies one harness conversation. Harness is canonical
// lowercase; SessionID is preserved exactly because it is passed back to the
// harness when resuming or forking a conversation.
type Identity struct {
	Harness   string `json:"harness"`
	SessionID string `json:"session_id"`
}

// New validates and canonicalizes a conversation identity. An empty harness
// is rejected instead of applying harness.Normalize's legacy Claude fallback:
// identity must come from an explicit authoritative source, never a guess.
func New(harnessName, sessionID string) (Identity, bool) {
	harnessName = strings.TrimSpace(harnessName)
	if harnessName == "" || strings.TrimSpace(sessionID) == "" {
		return Identity{}, false
	}
	return Identity{Harness: harness.Normalize(harnessName), SessionID: sessionID}, true
}

// Valid reports whether id contains a usable, canonical identity.
func (id Identity) Valid() bool {
	canonical, ok := New(id.Harness, id.SessionID)
	return ok && canonical == id
}

// Equal reports whether two valid identities identify the same conversation.
// Harness comparison follows its canonical form while session IDs remain exact.
func (id Identity) Equal(other Identity) bool {
	left, ok := New(id.Harness, id.SessionID)
	if !ok {
		return false
	}
	right, ok := New(other.Harness, other.SessionID)
	return ok && left == right
}
