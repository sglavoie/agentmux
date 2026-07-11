package plans

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/sglavoie/agentmux/internal/searchstore"
)

const legacyPlanImportSource = "legacy-plans-json-v1"

// LegacyImportResult summarizes a read-only migration from plans.json and
// its search sidecars into SQLite.
type LegacyImportResult struct {
	SourceFound bool
	Imported    bool
	Fingerprint string
	RecordCount int
}

type legacyImportEntry struct {
	key        string
	meta       PlanMeta
	searchText string
}

// ImportLegacyIndex imports current plan metadata and sidecar contents in one
// transaction. Neither plan directories nor compatibility artifacts are ever
// written by this method.
func (r *Repository) ImportLegacyIndex(ctx context.Context, indexPath string) (LegacyImportResult, error) {
	if indexPath == "" {
		return LegacyImportResult{}, errors.New("plans: legacy index path is empty")
	}
	absPath, err := filepath.Abs(indexPath)
	if err != nil {
		return LegacyImportResult{}, fmt.Errorf("plans: resolving legacy index path %q: %w", indexPath, err)
	}
	raw, err := os.ReadFile(absPath)
	if errors.Is(err, fs.ErrNotExist) {
		return LegacyImportResult{}, nil
	}
	if err != nil {
		return LegacyImportResult{}, fmt.Errorf("plans: reading legacy index %s: %w", absPath, err)
	}
	legacy, err := decodeLegacyPlanIndex(raw)
	if err != nil {
		return LegacyImportResult{SourceFound: true}, fmt.Errorf("plans: decoding legacy index %s: %w", absPath, err)
	}
	entries, err := resolveLegacySearchText(absPath, legacy.Entries)
	if err != nil {
		return LegacyImportResult{SourceFound: true}, err
	}
	fingerprint := legacyPlanFingerprint(raw, entries)
	result := LegacyImportResult{SourceFound: true, Fingerprint: fingerprint, RecordCount: len(entries)}

	err = r.store.Transaction(ctx, func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT fingerprint FROM import_records WHERE source_name = ?`, legacyPlanImportSource).Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("plans: checking legacy import record: %w", err)
		}
		if err == nil && existing == fingerprint {
			return nil
		}
		for _, entry := range entries {
			searchText := entry.searchText
			if err := r.upsertTx(ctx, tx, entry.meta, &searchText); err != nil {
				return fmt.Errorf("plans: importing legacy entry %q: %w", entry.key, err)
			}
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO import_records (source_name, source_path, fingerprint, completed_at_ns, record_count)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(source_name) DO UPDATE SET
				source_path = excluded.source_path,
				fingerprint = excluded.fingerprint,
				completed_at_ns = excluded.completed_at_ns,
				record_count = excluded.record_count
		`, legacyPlanImportSource, absPath, fingerprint, r.now().UnixNano(), len(entries))
		if err != nil {
			return fmt.Errorf("plans: recording legacy import completion: %w", err)
		}
		result.Imported = true
		return nil
	})
	if err != nil {
		return LegacyImportResult{SourceFound: true, Fingerprint: fingerprint, RecordCount: len(entries)}, err
	}
	return result, nil
}

// LegacyPlanImportRecord returns the last completed compatibility import.
func (r *Repository) LegacyPlanImportRecord(ctx context.Context) (ImportRecord, bool, error) {
	return r.LastImport(ctx, legacyPlanImportSource)
}

// LastImport returns a named legacy import completion record.
func (r *Repository) LastImport(ctx context.Context, sourceName string) (ImportRecord, bool, error) {
	var record ImportRecord
	var completedAt int64
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT source_name, source_path, fingerprint, completed_at_ns, record_count
		FROM import_records WHERE source_name = ?
	`, sourceName).Scan(&record.SourceName, &record.SourcePath, &record.Fingerprint, &completedAt, &record.RecordCount)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportRecord{}, false, nil
	}
	if err != nil {
		return ImportRecord{}, false, fmt.Errorf("plans: reading import record: %w", err)
	}
	record.CompletedAt = planTimeFromNS(completedAt)
	return record, true, nil
}

func decodeLegacyPlanIndex(raw []byte) (indexFile, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return indexFile{Entries: make(map[string]PlanMeta)}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var legacy indexFile
	if err := dec.Decode(&legacy); err != nil {
		return indexFile{}, err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return indexFile{}, fmt.Errorf("trailing data after top-level object: %v", trailing)
		}
		return indexFile{}, fmt.Errorf("trailing data after top-level object: %w", err)
	}
	if legacy.Entries == nil {
		legacy.Entries = make(map[string]PlanMeta)
	}
	return legacy, nil
}

func resolveLegacySearchText(indexPath string, metas map[string]PlanMeta) ([]legacyImportEntry, error) {
	store := searchstore.New(indexPath)
	keys := make([]string, 0, len(metas))
	for key := range metas {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]legacyImportEntry, 0, len(keys))
	for _, key := range keys {
		meta := metas[key]
		if meta.DirPath == "" {
			return nil, fmt.Errorf("plans: legacy entry %q is missing its directory path", key)
		}
		if expected := PlanKey(meta.DirPath); key != expected {
			return nil, fmt.Errorf("plans: legacy entry %q has directory identity %q", key, expected)
		}
		content, err := store.Read(key)
		if errors.Is(err, fs.ErrNotExist) {
			content = ""
		} else if err != nil {
			return nil, fmt.Errorf("plans: reading legacy search sidecar for %q: %w", key, err)
		}
		entries = append(entries, legacyImportEntry{key: key, meta: meta, searchText: content})
	}
	return entries, nil
}

func legacyPlanFingerprint(raw []byte, entries []legacyImportEntry) string {
	hash := sha256.New()
	_, _ = hash.Write(raw)
	for _, entry := range entries {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(entry.key))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(entry.searchText))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
