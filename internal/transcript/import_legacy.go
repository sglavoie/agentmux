package transcript

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sglavoie/agentmux/internal/compatimport"
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
	manifest    compatimport.ManifestEntry
	meta        SessionMeta
	searchText  string
	action      compatimport.Action
	contentHash string
}

// ImportLegacyIndex transactionally imports transcripts.json and only the
// new or changed sidecars. Missing sidecars are rebuilt through the harness
// Reader seam. Compatibility artifacts remain read-only throughout.
func (r *Repository) ImportLegacyIndex(
	ctx context.Context,
	indexPath string,
	readers map[string]Reader,
) (LegacyImportResult, error) {
	return r.importLegacyIndex(ctx, indexPath, readers, false)
}

// ForceImportLegacyIndex re-reads every compatibility entry and refreshes its
// derived SQLite rows. It is reserved for explicit compact/rebuild surfaces.
func (r *Repository) ForceImportLegacyIndex(
	ctx context.Context,
	indexPath string,
	readers map[string]Reader,
) (LegacyImportResult, error) {
	return r.importLegacyIndex(ctx, indexPath, readers, true)
}

func (r *Repository) importLegacyIndex(
	ctx context.Context,
	indexPath string,
	readers map[string]Reader,
	force bool,
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
	entries, err := prepareLegacyEntries(absPath, legacy.Entries)
	if err != nil {
		return LegacyImportResult{SourceFound: true}, err
	}
	manifest := make([]compatimport.ManifestEntry, len(entries))
	for i := range entries {
		manifest[i] = entries[i].manifest
	}
	fingerprint := compatimport.ManifestFingerprint(raw, manifest)
	result := LegacyImportResult{
		SourceFound: true,
		Fingerprint: fingerprint,
		RecordCount: len(entries),
	}

	prior, hasPrior, err := r.LastImport(ctx, legacyTranscriptImportSource)
	if err != nil {
		return result, err
	}
	if !force && hasPrior && prior.Fingerprint == fingerprint {
		return result, nil
	}
	ledger, err := compatimport.LoadLedger(ctx, r.store.DB(), legacyTranscriptImportSource)
	if err != nil {
		return result, fmt.Errorf("transcript: reading legacy entry ledger: %w", err)
	}

	priorCompletedAtNS := int64(0)
	if hasPrior {
		priorCompletedAtNS = prior.CompletedAt.UnixNano()
	}
	for i := range entries {
		entry := &entries[i]
		if force {
			entry.action = compatimport.Refresh
			rebuilt, err := r.resolveLegacySearchText(absPath, entry, readers)
			if err != nil {
				return result, err
			}
			if rebuilt {
				result.RebuiltSearchDocs++
			}
			entry.contentHash = compatimport.ContentHash(entry.searchText)
			continue
		}
		storedContent := ""
		canSeed := false
		ledgerEntry, hasLedger := ledger[entry.manifest.Key]
		if !hasLedger && hasPrior {
			storedContent, canSeed, err = r.canSeedLegacyEntry(ctx, *entry)
			if err != nil {
				return result, err
			}
		}
		entry.action = compatimport.Decide(entry.manifest, ledgerEntry, hasLedger, canSeed, priorCompletedAtNS)
		switch entry.action {
		case compatimport.Unchanged:
			continue
		case compatimport.Seed:
			entry.searchText = storedContent
		case compatimport.Refresh:
			rebuilt, err := r.resolveLegacySearchText(absPath, entry, readers)
			if err != nil {
				return result, err
			}
			if rebuilt {
				result.RebuiltSearchDocs++
			}
		}
		entry.contentHash = compatimport.ContentHash(entry.searchText)
	}

	importedAtNS := r.now().UnixNano()
	err = r.legacyImportTransaction(ctx, func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT fingerprint FROM import_records WHERE source_name = ?`,
			legacyTranscriptImportSource).Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("transcript: checking legacy import record: %w", err)
		}
		if !force && err == nil && existing == fingerprint {
			return nil
		}
		for i := range entries {
			entry := &entries[i]
			if entry.action != compatimport.Refresh {
				continue
			}
			searchText := entry.searchText
			if err := r.upsertTx(ctx, tx, entry.meta, &searchText); err != nil {
				return fmt.Errorf("transcript: importing legacy entry %q: %w", entry.manifest.Key, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO import_records (
				source_name, source_path, fingerprint, completed_at_ns, record_count
			) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(source_name) DO UPDATE SET
				source_path = excluded.source_path,
				fingerprint = excluded.fingerprint,
				completed_at_ns = excluded.completed_at_ns,
				record_count = excluded.record_count
		`, legacyTranscriptImportSource, absPath, fingerprint, importedAtNS, len(entries)); err != nil {
			return fmt.Errorf("transcript: recording legacy import completion: %w", err)
		}
		for _, entry := range entries {
			if entry.action == compatimport.Unchanged {
				continue
			}
			if err := compatimport.UpsertLedgerTx(ctx, tx, legacyTranscriptImportSource, compatimport.LedgerEntry{
				ManifestEntry:    entry.manifest,
				ContentHash:      entry.contentHash,
				LastImportedAtNS: importedAtNS,
			}); err != nil {
				return fmt.Errorf("transcript: %w", err)
			}
		}
		result.Imported = true
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func prepareLegacyEntries(indexPath string, metas map[string]SessionMeta) ([]legacyImportEntry, error) {
	store := searchstore.New(indexPath)
	keys := sortedKeys(metas)
	entries := make([]legacyImportEntry, 0, len(keys))
	for _, key := range keys {
		meta := metas[key]
		if meta.Harness == "" || meta.ID == "" {
			return nil, fmt.Errorf("transcript: legacy entry %q is missing harness or session id", key)
		}
		expectedKey := IndexKey(meta.Harness, meta.ID)
		if key != expectedKey {
			return nil, fmt.Errorf("transcript: legacy entry %q has metadata identity %q", key, expectedKey)
		}
		searchText := meta.SearchText
		meta = normalizeLegacyMetadata(meta)
		metadataHash, err := compatimport.MetadataHash(meta)
		if err != nil {
			return nil, fmt.Errorf("transcript: hashing legacy metadata for %q: %w", key, err)
		}
		stat, err := compatimport.StatSidecar(store.Path(key))
		if err != nil {
			return nil, fmt.Errorf("transcript: stating legacy search sidecar for %q: %w", key, err)
		}
		entries = append(entries, legacyImportEntry{
			manifest:   compatimport.ManifestEntry{Key: key, MetadataHash: metadataHash, Sidecar: stat},
			meta:       meta,
			searchText: searchText,
		})
	}
	return entries, nil
}

func normalizeLegacyMetadata(meta SessionMeta) SessionMeta {
	displayProfile := meta.DisplayProfile()
	meta.Profiles = meta.ProfileNames()
	meta.Profile = displayProfile
	meta.FileModTime = meta.FileModTime.UTC()
	meta.StartedAt = meta.StartedAt.UTC()
	meta.LastAt = meta.LastAt.UTC()
	meta.SearchText = ""
	return meta
}

func (r *Repository) canSeedLegacyEntry(ctx context.Context, entry legacyImportEntry) (string, bool, error) {
	stored, ok, err := r.Get(ctx, entry.meta.Harness, entry.meta.ID)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	stored = normalizeLegacyMetadata(stored)
	hash, err := compatimport.MetadataHash(stored)
	if err != nil {
		return "", false, fmt.Errorf("transcript: hashing stored metadata for %q: %w", entry.manifest.Key, err)
	}
	if hash != entry.manifest.MetadataHash {
		return "", false, nil
	}
	content, found, err := r.SearchText(ctx, entry.meta.Harness, entry.meta.ID)
	if err != nil {
		return "", false, err
	}
	if entry.meta.HiddenFromIndex {
		return "", !found, nil
	}
	return content, found, nil
}

func (r *Repository) resolveLegacySearchText(indexPath string, entry *legacyImportEntry, readers map[string]Reader) (bool, error) {
	if entry.meta.HiddenFromIndex || entry.searchText != "" {
		return false, nil
	}
	content, err := r.legacySidecarRead(indexPath, entry.manifest.Key)
	if err == nil {
		entry.searchText = content
		return false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("transcript: reading legacy search sidecar for %q: %w", entry.manifest.Key, err)
	}
	if entry.meta.MessageCount == 0 {
		return false, nil
	}
	reader, ok := readers[entry.meta.Harness]
	if !ok || reader == nil {
		return false, fmt.Errorf("transcript: rebuilding missing search sidecar for %q: no reader for harness %q", entry.manifest.Key, entry.meta.Harness)
	}
	messages, err := reader.ReadMessages(entry.meta.Path)
	if err != nil {
		return false, fmt.Errorf("transcript: rebuilding missing search sidecar for %q from %s: %w", entry.manifest.Key, entry.meta.Path, err)
	}
	entry.searchText = messagesSearchText(messages)
	return true, nil
}

func readLegacySidecar(indexPath, key string) (string, error) {
	return searchstore.New(indexPath).Read(key)
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

// LegacyTranscriptImportRecord exposes the stable import source name for
// status/maintenance callers without coupling them to the table encoding.
func (r *Repository) LegacyTranscriptImportRecord(ctx context.Context) (ImportRecord, bool, error) {
	return r.LastImport(ctx, legacyTranscriptImportSource)
}
