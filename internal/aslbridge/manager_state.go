package aslbridge

// This file owns the manager's guarded state: the transitions supervision
// writes and the reads the daemon serves from them. Three rules live here and
// nowhere else — a snapshot is only fresh under the connection that produced
// it, a mutation grant is only valid against the exact digest it was handshaked
// for, and data outside the disconnect window is dropped rather than served.

import "time"

// ----------------------------------------------------------------
// State transitions

func (m *Manager) setConnected(executable, source string, hello Hello, c *conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conn = c
	m.hello = hello
	m.connected = true
	m.handshakeGen++
	m.executable = executable
	m.executableSource = source
	m.disconnectedAt = time.Time{}
	m.reason = ""
	// A new child has granted nothing yet. Carrying a grant across a reconnect
	// would authorize acting on a projection the previous process built.
	m.clearGrantLocked()
}

// setDisconnected disables mutations at once. The last snapshot survives as
// stale read-only data for StaleWindow and is then dropped by pruneLocked.
func (m *Manager) setDisconnected(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearGrantLocked()
	if !m.connected && !m.disconnectedAt.IsZero() {
		m.reason = reason
		return
	}
	m.connected = false
	m.conn = nil
	m.disconnectedAt = m.now()
	m.reason = reason
}

func (m *Manager) clearGrantLocked() {
	m.mutationsGranted = false
	m.grantedDigest = ""
	m.handshakeID = ""
}

// grantMutations records that session.handshake authorized acting on exactly
// this digest. It also clears the published reason, because the condition that
// reason described is the one that just resolved.
func (m *Manager) grantMutations(digest, handshakeID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mutationsGranted = true
	m.grantedDigest = digest
	m.handshakeID = handshakeID
	m.reason = ""
}

// revokeMutations withdraws the grant without ending the session. Reads stay
// available: the daemon still has data it may show, it just may no longer act
// on it. The reason is daemon-authored and stable, so it is safe to publish.
func (m *Manager) revokeMutations(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearGrantLocked()
	m.reason = reason
}

func (m *Manager) setReason(reason string) {
	m.mu.Lock()
	m.reason = reason
	m.mu.Unlock()
}

func (m *Manager) storeSnapshot(s Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := s
	m.snapshot = &snapshot
	m.snapshotGen++
	m.snapshotAt = m.now()
	m.snapshotHandshake = m.handshakeGen
	// Storing a projection does not authorize acting on it. The grant is earned
	// again, against this digest, by the handshake the refresh issues next — so
	// the window between "new data arrived" and "the bridge agreed it is
	// current" is read-only rather than optimistically mutable.
	if m.grantedDigest != snapshot.Digest {
		m.clearGrantLocked()
	}
}

func (m *Manager) recordReceipt(op Operation) {
	m.counters.operation(op.Status)
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.receipts {
		if m.receipts[i].OperationID == op.OperationID {
			m.receipts[i] = op
			return
		}
	}
	m.receipts = append(m.receipts, op)
	if len(m.receipts) > maxReceipts {
		m.receipts = m.receipts[len(m.receipts)-maxReceipts:]
	}
}

// pruneLocked removes data the daemon is no longer allowed to serve. Callers
// hold m.mu.
func (m *Manager) pruneLocked(now time.Time) {
	if m.connected || m.snapshot == nil || m.disconnectedAt.IsZero() {
		return
	}
	if now.Sub(m.disconnectedAt) > StaleWindow {
		m.snapshot = nil
		m.snapshotAt = time.Time{}
		m.snapshotHandshake = 0
	}
}

// mutationsEnabledLocked is the single answer to "may the daemon act right
// now". Every clause is load-bearing: a live child, a snapshot taken under this
// child, and a handshake grant pinned to that snapshot's exact digest.
func (m *Manager) mutationsEnabledLocked() bool {
	return m.connected &&
		m.snapshot != nil &&
		m.snapshotHandshake == m.handshakeGen &&
		m.mutationsGranted &&
		m.grantedDigest == m.snapshot.Digest
}

// ----------------------------------------------------------------
// Reads

// Snapshot returns the last projection the daemon holds. Meta.Stale marks data
// served from the disconnect window; a caller must present it read-only.
func (m *Manager) Snapshot() (Snapshot, SnapshotMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mode == ModeDisabled {
		return Snapshot{}, SnapshotMeta{}, ErrDisabled
	}
	now := m.now()
	m.pruneLocked(now)
	if m.snapshot == nil {
		return Snapshot{}, SnapshotMeta{}, ErrUnavailable
	}
	meta := SnapshotMeta{
		Generation: m.snapshotGen,
		ReceivedAt: m.snapshotAt,
		AgeSeconds: int64(now.Sub(m.snapshotAt) / time.Second),
		Stale:      !m.mutationsEnabledLocked(),
	}
	return *m.snapshot, meta, nil
}

// Receipts returns the bounded, body-free operation receipt window.
func (m *Manager) Receipts() []Operation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Operation, len(m.receipts))
	copy(out, m.receipts)
	return out
}

// Status returns the ASL capability block.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.pruneLocked(now)
	status := Status{
		Mode:             string(m.mode),
		Command:          m.command,
		Executable:       m.executable,
		ExecutableSource: m.executableSource,
		Available:        m.connected,
		MutationsEnabled: m.mutationsEnabledLocked(),
		RestartCount:     m.restarts,
		Reason:           m.reason,
	}
	if m.connected {
		status.Protocol = m.hello.Protocol
		status.Version = m.hello.ASLVersion
		status.ContractSetID = m.hello.ContractSetID
		status.Capabilities = append([]string(nil), m.hello.Capabilities...)
	}
	if m.snapshot != nil {
		age := int64(now.Sub(m.snapshotAt) / time.Second)
		status.SnapshotAgeSeconds = &age
		status.SnapshotGeneration = m.snapshotGen
		status.SnapshotStale = !m.mutationsEnabledLocked()
	}
	counters := m.counters.snapshot()
	status.Metrics = &counters
	return status
}

// Metrics returns the byte, frame, and method counters for this manager.
func (m *Manager) Metrics() Metrics { return m.counters.snapshot() }
