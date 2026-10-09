// Package runtimesession owns AgentMux runtime identity: the opaque ID a
// running agent gets when AgentMux launches it, and the durable registry that
// maps that ID to the backend target the agent runs in.
//
// Four identities stay separate here, because each one changes on its own
// schedule:
//
//   - The configured project (Record.Project) is the operator's name for the
//     work and outlives every launch.
//   - The harness conversation (conversation.Identity) is (harness,
//     session_id) and survives resume, fork, and relaunch.
//   - The runtime session (ID) is one agent launch. Relaunching an agent,
//     even into the same conversation, creates a new ID; Record.Parent and
//     Record.Relation keep the launch relationship.
//   - The presentation attachment (Attachment) is whichever view currently
//     owns interactive input. Viewing and detaching never change the runtime
//     session.
//
// A backend target alone is not identity. Pane and window IDs restart with
// their server, and PIDs are reused by the kernel, so a target is only
// trusted while its backend-instance generation and the process fingerprint
// at the target both still match what was registered.
package runtimesession

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// idPrefix makes runtime IDs recognizable in logs and CLI arguments and keeps
// them visibly distinct from harness session IDs and Kitty window numbers.
const idPrefix = "rs-"

const idHexLength = 32

// ID is an opaque AgentMux runtime-session identifier. It carries no backend,
// project, or conversation information; callers resolve it through the
// registry.
type ID string

// NewID returns a fresh random runtime-session ID.
func NewID() (ID, error) {
	var raw [idHexLength / 2]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("runtimesession: generating ID: %w", err)
	}
	return ID(idPrefix + hex.EncodeToString(raw[:])), nil
}

// ParseID validates a runtime-session ID from an untrusted source such as a
// CLI argument or a hook environment variable.
func ParseID(value string) (ID, error) {
	id := ID(value)
	if err := id.Validate(); err != nil {
		return "", err
	}
	return id, nil
}

// Validate reports whether id has the exact canonical form NewID produces.
func (id ID) Validate() error {
	value := string(id)
	digits, ok := strings.CutPrefix(value, idPrefix)
	if !ok || len(digits) != idHexLength {
		return fmt.Errorf("runtimesession: malformed ID %q", value)
	}
	for _, r := range digits {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("runtimesession: malformed ID %q", value)
		}
	}
	return nil
}

// BackendKind names the terminal backend that hosts a runtime session.
type BackendKind string

const (
	BackendKitty BackendKind = "kitty"
	BackendTmux  BackendKind = "tmux"
)

func (k BackendKind) valid() bool {
	return k == BackendKitty || k == BackendTmux
}

// BackendInstance identifies one running backend server. Address is where it
// is reached (a Kitty listen_on socket, a private tmux socket path); it is
// reused when the server restarts. Generation is an opaque token the backend
// derives from the running server itself (for example its PID and start
// time), so a restarted server at the same address is a different instance.
type BackendInstance struct {
	Kind       BackendKind `json:"kind"`
	Address    string      `json:"address"`
	Generation string      `json:"generation"`
}

// Validate rejects an instance that could not distinguish a restart.
func (b BackendInstance) Validate() error {
	if !b.Kind.valid() {
		return fmt.Errorf("runtimesession: unsupported backend %q", b.Kind)
	}
	if strings.TrimSpace(b.Address) == "" {
		return errors.New("runtimesession: backend address is required")
	}
	if strings.TrimSpace(b.Generation) == "" {
		return errors.New("runtimesession: backend generation is required")
	}
	return nil
}

// SameServer reports whether two instances name the same address on the same
// backend, regardless of generation.
func (b BackendInstance) SameServer(other BackendInstance) bool {
	return b.Kind == other.Kind && b.Address == other.Address
}

// Target is a terminal location inside one backend instance: a tmux pane ID
// or a Kitty window ID, kept as the backend's own string.
type Target struct {
	Instance BackendInstance `json:"instance"`
	Ref      string          `json:"ref"`
}

// Validate requires a complete instance and a non-empty reference.
func (t Target) Validate() error {
	if err := t.Instance.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(t.Ref) == "" {
		return errors.New("runtimesession: backend target reference is required")
	}
	return nil
}

// ProcessFingerprint identifies one process lifetime. StartToken is the
// opaque start time the process inspector reports; together with the PID it
// survives PID reuse, which a PID alone does not.
type ProcessFingerprint struct {
	PID        int    `json:"pid"`
	StartToken string `json:"start_token"`
}

// Validate requires a usable PID and a start token.
func (p ProcessFingerprint) Validate() error {
	if p.PID <= 1 {
		return fmt.Errorf("runtimesession: unusable process ID %d", p.PID)
	}
	if strings.TrimSpace(p.StartToken) == "" {
		return errors.New("runtimesession: process start token is required")
	}
	return nil
}

// Attachment is a presentation view holding interactive ownership of a
// runtime session. Its ID belongs to the presenting client; the registry
// records it only so a later attachment elsewhere can transfer ownership and a
// stale detach cannot release someone else's.
type Attachment struct {
	ID     string `json:"id"`
	Client string `json:"client"`
}

// Validate requires both an attachment ID and the client that owns it.
func (a Attachment) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("runtimesession: attachment ID is required")
	}
	if strings.TrimSpace(a.Client) == "" {
		return errors.New("runtimesession: attachment client is required")
	}
	return nil
}
