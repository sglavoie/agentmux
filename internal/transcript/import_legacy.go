package transcript

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

	"github.com/sglavoie/agentmux/internal/searchstore"
)

const legacyTranscriptImportSource = "legacy-transcripts-json-v1"

// LegacyImportResult summarizes an attempt to copy the JSON transcript index
// and search sidecars into SQLite.
type LegacyImportResult struct {
	SourceFound       bool
	Imported          bool
	Fingerprint       string
	RecordCount       int
	RebuiltSearchDocs int
}

type legacyImportEntry struct {
	key        string
	meta       SessionMeta
	searchText string
}

// ImportLegacyIndex transactionally imports transcripts.json and its current
// sidecars. Missing sidecars are rebuilt through the harness Reader seam. The
// source JSON, sidecars, and transcript files are read-only throughout.
func (r *Repository) ImportLegacyIndex(
	ctx context.Context,
	indexPath string,
	readers map[string]Reader,
) (LegacyImportResult, error) {
	if indexPath == "" {
		return LegacyImportResult{}, errors.New("transcript: legacy index path is empty")
	}
	absPath, err := filepath.Abs(indexPath)
	if err != nil {
		return LegacyImportResult{}, fmt.Errorf("transcript: resolving legacy index path %q: %w", indexPath, err)
	}
	raw, err := os.ReadFile(absPath)
	if errors.Is(err, fs.ErrNotExist) {
		return LegacyImportResult{}, nil
	}
	if err != nil {
		return LegacyImportResult{}, fmt.Errorf("transcript: reading legacy index %s: %w", absPath, err)
	}

	legacy, err := decodeLegacyIndex(raw)
	if err != nil {
		return LegacyImportResult{SourceFound: true}, fmt.Errorf("decoding transcript index %s: %w", absPath, err)
	}
	entries, rebuilt, err := resolveLegacySearchText(absPath, legacy.Entries, readers)
	if err != nil {
		return LegacyImportResult{SourceFound: true}, err
	}
	fingerprint := legacyFingerprint(raw, entries)
	result := LegacyImportResult{
		SourceFound:       true,
		Fingerprint:       fingerprint,
		RecordCount:       len(entries),
		RebuiltSearchDocs: rebuilt,
	}

	err = r.store.Transaction(ctx, func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT fingerprint FROM import_records WHERE source_name = ?`,
			legacyTranscriptImportSource).Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("transcript: checking legacy import record: %w", err)
		}
		if err == nil && existing == fingerprint {
			return nil
		}
		for _, entry := range entries {
			searchText := entry.searchText
			if err := r.upsertTx(ctx, tx, entry.meta, &searchText); err != nil {
				return fmt.Errorf("transcript: importing legacy entry %q: %w", entry.key, err)
			}
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO import_records (
				source_name, source_path, fingerprint, completed_at_ns, record_count
			) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(source_name) DO UPDATE SET
				source_path = excluded.source_path,
				fingerprint = excluded.fingerprint,
				completed_at_ns = excluded.completed_at_ns,
				record_count = excluded.record_count
		`, legacyTranscriptImportSource, absPath, fingerprint, r.now().UnixNano(), len(entries))
		if err != nil {
			return fmt.Errorf("transcript: recording legacy import completion: %w", err)
		}
		result.Imported = true
		return nil
	})
	if err != nil {
		return LegacyImportResult{SourceFound: true, Fingerprint: fingerprint, RecordCount: len(entries)}, err
	}
	return result, nil
}

func decodeLegacyIndex(raw []byte) (indexFile, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return indexFile{Entries: make(map[string]SessionMeta)}, nil
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
		legacy.Entries = make(map[string]SessionMeta)
	}
	return legacy, nil
}

func resolveLegacySearchText(
	indexPath string,
	metas map[string]SessionMeta,
	readers map[string]Reader,
) ([]legacyImportEntry, int, error) {
	store := searchstore.New(indexPath)
	keys := sortedKeys(metas)
	entries := make([]legacyImportEntry, 0, len(keys))
	rebuilt := 0
	for _, key := range keys {
		meta := metas[key]
		if meta.Harness == "" || meta.ID == "" {
			return nil, rebuilt, fmt.Errorf("transcript: legacy entry %q is missing harness or session id", key)
		}
		expectedKey := IndexKey(meta.Harness, meta.ID)
		if key != expectedKey {
			return nil, rebuilt, fmt.Errorf("transcript: legacy entry %q has metadata identity %q", key, expectedKey)
		}
		entry := legacyImportEntry{key: key, meta: meta, searchText: meta.SearchText}
		entry.meta.SearchText = ""
		if meta.HiddenFromIndex || entry.searchText != "" {
			entries = append(entries, entry)
			continue
		}
		content, err := store.Read(key)
		if err == nil {
			entry.searchText = content
			entries = append(entries, entry)
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, rebuilt, fmt.Errorf("transcript: reading legacy search sidecar for %q: %w", key, err)
		}
		if meta.MessageCount == 0 {
			entries = append(entries, entry)
			continue
		}
		reader, ok := readers[meta.Harness]
		if !ok || reader == nil {
			return nil, rebuilt, fmt.Errorf("transcript: rebuilding missing search sidecar for %q: no reader for harness %q", key, meta.Harness)
		}
		messages, err := reader.ReadMessages(meta.Path)
		if err != nil {
			return nil, rebuilt, fmt.Errorf("transcript: rebuilding missing search sidecar for %q from %s: %w", key, meta.Path, err)
		}
		entry.searchText = messagesSearchText(messages)
		rebuilt++
		entries = append(entries, entry)
	}
	return entries, rebuilt, nil
}

func legacyFingerprint(raw []byte, entries []legacyImportEntry) string {
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

// LegacyTranscriptImportRecord exposes the stable import source name for
// status/maintenance callers without coupling them to the table encoding.
func (r *Repository) LegacyTranscriptImportRecord(ctx context.Context) (ImportRecord, bool, error) {
	return r.LastImport(ctx, legacyTranscriptImportSource)
}
