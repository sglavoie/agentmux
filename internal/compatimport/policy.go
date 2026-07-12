// Package compatimport contains the shared policy primitives used to import
// read-only JSON indexes and their search sidecars into SQLite incrementally.
package compatimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

const missingSidecarValue int64 = -1

// SyncMode controls whether attaching a compatibility facade also imports its
// rollback source. Callers that only inspect SQLite use SyncSkip; ordinary
// consumers use SyncIfChanged; explicit maintenance surfaces use SyncForce.
type SyncMode uint8

const (
	SyncSkip SyncMode = iota
	SyncIfChanged
	SyncForce
)

// Valid reports whether mode is a supported compatibility synchronization
// policy. Rejecting unknown values keeps loader ownership failures explicit.
func (m SyncMode) Valid() bool {
	return m == SyncSkip || m == SyncIfChanged || m == SyncForce
}

// SidecarStat is the cheap portion of a sidecar identity. Missing sidecars use
// sentinel size and mtime values so they cannot collide with an empty file.
type SidecarStat struct {
	Size      int64
	ModTimeNS int64
}

// ManifestEntry is the body-free state used for a source fingerprint and for
// comparison with the durable per-entry ledger.
type ManifestEntry struct {
	Key          string
	MetadataHash string
	Sidecar      SidecarStat
}

// LedgerEntry is the durable state of one successfully imported entry.
type LedgerEntry struct {
	ManifestEntry
	ContentHash      string
	LastImportedAtNS int64
}

// Action describes the work needed after comparing a manifest entry with the
// ledger and, for upgraded databases, existing SQLite data.
type Action uint8

const (
	Unchanged Action = iota
	Seed
	Refresh
)

// StatSidecar inspects a sidecar without opening or reading its body.
func StatSidecar(path string) (SidecarStat, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return SidecarStat{Size: missingSidecarValue, ModTimeNS: missingSidecarValue}, nil
	}
	if err != nil {
		return SidecarStat{}, err
	}
	if !info.Mode().IsRegular() {
		return SidecarStat{}, &os.PathError{Op: "stat", Path: path, Err: fs.ErrInvalid}
	}
	return SidecarStat{Size: info.Size(), ModTimeNS: info.ModTime().UnixNano()}, nil
}

// MetadataHash returns a stable hash of JSON-serializable compatibility
// metadata. Domain packages normalize transient search text before calling it.
func MetadataHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return hashBytes(raw), nil
}

// ContentHash returns the durable digest stored alongside an imported body.
func ContentHash(content string) string { return hashBytes([]byte(content)) }

// ManifestFingerprint hashes the rollback JSON plus ordered sidecar stat
// metadata. It deliberately excludes sidecar bodies.
func ManifestFingerprint(raw []byte, entries []ManifestEntry) string {
	hash := sha256.New()
	writeFramed(hash, raw)
	ordered := append([]ManifestEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Key < ordered[j].Key })
	var number [8]byte
	for _, entry := range ordered {
		writeFramed(hash, []byte(entry.Key))
		binary.LittleEndian.PutUint64(number[:], uint64(entry.Sidecar.Size))
		_, _ = hash.Write(number[:])
		binary.LittleEndian.PutUint64(number[:], uint64(entry.Sidecar.ModTimeNS))
		_, _ = hash.Write(number[:])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Decide returns the minimum safe work for an entry. Existing SQLite content
// can seed a missing v4 ledger only when its sidecar did not change after the
// prior source import completed.
func Decide(entry ManifestEntry, ledger LedgerEntry, hasLedger, canSeed bool, priorCompletedAtNS int64) Action {
	if hasLedger && ledger.MetadataHash == entry.MetadataHash && ledger.Sidecar == entry.Sidecar {
		return Unchanged
	}
	if !hasLedger && canSeed && entry.Sidecar.ModTimeNS <= priorCompletedAtNS {
		return Seed
	}
	return Refresh
}

// LoadLedger returns every durable entry for sourceName without starting a
// transaction.
func LoadLedger(ctx context.Context, db *sql.DB, sourceName string) (map[string]LedgerEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT entity_key, metadata_hash, sidecar_size, sidecar_mtime_ns,
		       content_hash, last_imported_at_ns
		FROM legacy_import_entries WHERE source_name = ?
	`, sourceName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	entries := make(map[string]LedgerEntry)
	for rows.Next() {
		var entry LedgerEntry
		if err := rows.Scan(&entry.Key, &entry.MetadataHash, &entry.Sidecar.Size,
			&entry.Sidecar.ModTimeNS, &entry.ContentHash, &entry.LastImportedAtNS); err != nil {
			return nil, err
		}
		entries[entry.Key] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// UpsertLedgerTx records one seeded or refreshed entry in the caller's atomic
// compatibility-import transaction.
func UpsertLedgerTx(ctx context.Context, tx *sql.Tx, sourceName string, entry LedgerEntry) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_import_entries (
			source_name, entity_key, metadata_hash, sidecar_size,
			sidecar_mtime_ns, content_hash, last_imported_at_ns
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_name, entity_key) DO UPDATE SET
			metadata_hash = excluded.metadata_hash,
			sidecar_size = excluded.sidecar_size,
			sidecar_mtime_ns = excluded.sidecar_mtime_ns,
			content_hash = excluded.content_hash,
			last_imported_at_ns = excluded.last_imported_at_ns
	`, sourceName, entry.Key, entry.MetadataHash, entry.Sidecar.Size,
		entry.Sidecar.ModTimeNS, entry.ContentHash, entry.LastImportedAtNS)
	if err != nil {
		return fmt.Errorf("recording compatibility entry %q: %w", entry.Key, err)
	}
	return nil
}

func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeFramed(hash hashWriter, value []byte) {
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = hash.Write(size[:])
	_, _ = hash.Write(value)
}
