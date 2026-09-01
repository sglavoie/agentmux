package aslbridge

// This file holds the per-session work a supervised bridge does once it is
// running: the refresh loop, the snapshot read, the handshake, and the event
// plumbing the two share. The lifecycle that spawns and restarts the child is
// in manager.go.

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

func (m *Manager) refreshLoop(ctx context.Context, c *conn) {
	retry := time.NewTicker(SnapshotRetryInterval)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.readerDone:
			return
		case <-m.refresh:
			m.refreshSnapshot(ctx, c)
		case <-retry.C:
			if !m.mutationsEnabled() {
				m.refreshSnapshot(ctx, c)
			}
		}
	}
}

// mutationsEnabled reports whether the daemon currently holds a snapshot taken
// under this connection and a handshake grant against that exact snapshot. A
// connected child without both is the one state the refresh loop cannot wait
// out, because no corpus change is owed to it: a first snapshot that never
// arrived and a grant a moved digest withdrew are both re-earned by asking.
func (m *Manager) mutationsEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutationsEnabledLocked()
}

// failSession ends one session with a cause the manager decided. It withdraws
// the grant before condemning the connection, because Status is read
// continuously and supervision only clears the grant once teardown completes:
// for the whole of that gap the capability block would otherwise keep promising
// mutations over a connection the daemon has already given up on.
func (m *Manager) failSession(c *conn, err error) {
	m.revokeMutations(sanitizeReason(err))
	c.fail(err)
}

func (m *Manager) refreshSnapshot(ctx context.Context, c *conn) {
	callCtx, cancel := context.WithTimeout(ctx, SnapshotTimeout)
	raw, err := c.call(callCtx, MethodRunsSnapshot, nil)
	cancel()
	if err != nil {
		m.log.Debug("daemon: asl snapshot refresh failed", "reason", sanitizeReason(err))
		return
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		// A response body the daemon cannot decode is the same class of
		// failure as contamination: the child is not speaking protocol 2.
		m.failSession(c, contaminationError("snapshot result"))
		return
	}
	// The snapshot restates the set it was built under, so a bridge that changed
	// corpora without restarting cannot slip one past the hello check.
	if snapshot.ContractSetID != ContractSetID {
		m.log.Warn("daemon: asl snapshot named a foreign contract set",
			"offered", snapshot.ContractSetID, "expected", ContractSetID)
		m.failSession(c, ErrContractSetMismatch)
		return
	}
	m.storeSnapshot(snapshot)
	m.handshake(ctx, c, snapshot.Digest)
}

// handshake re-authorizes mutations against the digest just stored. It runs
// after every refresh rather than once per session because the grant is against
// one projection: the bridge accepts it only while it still holds that digest,
// which is what stops an operator acting on a row that moved between the
// projection and the click.
func (m *Manager) handshake(ctx context.Context, c *conn, digest string) {
	callCtx, cancel := context.WithTimeout(ctx, HandshakeTimeout)
	raw, err := c.call(callCtx, MethodSessionHandshake, map[string]any{
		"contract_set_id": ContractSetID,
		"snapshot_digest": digest,
	})
	cancel()
	if err != nil {
		var refusal *Error
		if errors.As(err, &refusal) && refusal.Code == CodeContractSetMismatch {
			m.log.Warn("daemon: asl bridge refused the handshake as incompatible",
				"expected", ContractSetID)
			m.failSession(c, ErrContractSetMismatch)
			return
		}
		m.log.Debug("daemon: asl handshake failed", "reason", sanitizeReason(err))
		m.revokeMutations(reasonHandshakeIncomplete)
		return
	}
	var result Handshake
	if err := json.Unmarshal(raw, &result); err != nil {
		m.failSession(c, contaminationError("session.handshake result"))
		return
	}
	if result.ContractSetID != ContractSetID {
		m.log.Warn("daemon: asl handshake echoed a foreign contract set",
			"offered", result.ContractSetID, "expected", ContractSetID)
		m.failSession(c, ErrContractSetMismatch)
		return
	}
	if !result.MutationsEnabled || result.SnapshotDigest != digest {
		// The corpus moved between the snapshot and the handshake. Reads keep
		// working against what the daemon holds; acting on it does not, until a
		// refresh catches up to the digest the bridge just named.
		m.revokeMutations(reasonHandshakeStale)
		m.requestRefresh()
		return
	}
	m.grantMutations(digest, result.HandshakeID)
}

// requestRefresh asks the refresh loop for a new snapshot without blocking. A
// full channel already means a refresh is pending, so dropping is correct.
func (m *Manager) requestRefresh() {
	select {
	case m.refresh <- struct{}{}:
	default:
	}
}

// handleEvent runs on the reader goroutine and must never block: it records
// what it can and signals the refresher rather than issuing a request itself.
func (m *Manager) handleEvent(e event) {
	switch e.Name {
	case EventRunsChanged:
		m.requestRefresh()
	case EventOperationUpdated:
		var op Operation
		if err := json.Unmarshal(e.Data, &op); err != nil {
			return
		}
		m.recordReceipt(op)
	}
}

func (m *Manager) wait(ctx context.Context, d time.Duration) bool {
	if m.sleep != nil {
		return m.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func hasCapability(capabilities []string, name string) bool {
	for _, c := range capabilities {
		if c == name {
			return true
		}
	}
	return false
}
