package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Each runtime session has its own durable operation history. It lives beside
// the send lock, which serializes every read and write across CLI processes.
// Keeping the history for the session's lifetime means an intervening send
// cannot make an older retry eligible to write again.
type sendLedger struct {
	Version          int               `json:"version"`
	RuntimeSessionID runtimesession.ID `json:"runtime_session_id"`
	Operations       map[string]string `json:"operations"`
	// InputDigests binds new operation IDs to their input without persisting
	// prompt text. Older ledgers and pane-only markers have no digest.
	InputDigests map[string]string `json:"input_digests,omitempty"`
}

func digestSendInput(input backend.Input) (string, error) {
	if len(input.Keys) == 0 {
		input.Keys = nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encoding send input identity: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func sendLedgerPath(lockPath string, id runtimesession.ID) string {
	digest := sha256.Sum256([]byte(id))
	return filepath.Join(lockPath+".operations", fmt.Sprintf("%x.json", digest))
}

func loadSendLedger(lockPath string, id runtimesession.ID) (sendLedger, error) {
	ledger := sendLedger{Version: 2, RuntimeSessionID: id, Operations: make(map[string]string), InputDigests: make(map[string]string)}
	path := sendLedgerPath(lockPath, id)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ledger, nil
	}
	if err != nil {
		return sendLedger{}, fmt.Errorf("inspecting send history: %w", err)
	}
	if !info.Mode().IsRegular() {
		return sendLedger{}, errors.New("send history is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return sendLedger{}, fmt.Errorf("reading send history: %w", err)
	}
	if err := json.Unmarshal(data, &ledger); err != nil {
		return sendLedger{}, fmt.Errorf("decoding send history: %w", err)
	}
	if (ledger.Version != 1 && ledger.Version != 2) || ledger.RuntimeSessionID != id || ledger.Operations == nil {
		return sendLedger{}, errors.New("send history has an invalid version or runtime session")
	}
	if ledger.InputDigests == nil {
		ledger.InputDigests = make(map[string]string)
	}
	for operation, status := range ledger.Operations {
		if !ValidSendOperationID(operation) || status != sendPending && status != sendSent {
			return sendLedger{}, errors.New("send history contains an invalid operation")
		}
	}
	for operation, digest := range ledger.InputDigests {
		decoded, err := hex.DecodeString(digest)
		if _, ok := ledger.Operations[operation]; !ok || err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != digest {
			return sendLedger{}, errors.New("send history contains an invalid input identity")
		}
	}
	ledger.Version = 2
	return ledger, nil
}

func (l sendLedger) save(lockPath string) error {
	data, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("encoding send history: %w", err)
	}
	path := sendLedgerPath(lockPath, l.RuntimeSessionID)
	if err := atomicfile.Write(path, append(data, '\n'), 0o600,
		atomicfile.ParentMode(0o700), atomicfile.RequireRegularTarget()); err != nil {
		return fmt.Errorf("writing send history: %w", err)
	}
	return nil
}

func (l sendLedger) rememberPaneMarker(marker string) bool {
	operation, status, ok := strings.Cut(marker, ":")
	if !ok || !ValidSendOperationID(operation) || status != sendPending && status != sendSent {
		return false
	}
	previous, known := l.Operations[operation]
	if !known || previous == sendPending && status == sendSent {
		l.Operations[operation] = status
		return true
	}
	return false
}
