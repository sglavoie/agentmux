package plans

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
	"sort"

	"github.com/sglavoie/agentmux/internal/compatimport"
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
	manifest    compatimport.ManifestEntry
	meta        PlanMeta
	searchText  string
	action      compatimport.Action
	contentHash string
}

// ImportLegacyIndex imports current plan metadata and only new or changed
// sidecar contents in one transaction. Plan directories and compatibility
// artifacts are never written by this method.
func (r *Repository) ImportLegacyIndex(ctx context.Context, indexPath string) (LegacyImportResult, error) {
	return r.importLegacyIndex(ctx, indexPath, false)
}

// ForceImportLegacyIndex re-reads every compatibility entry and refreshes its
// derived SQLite rows. It is reserved for explicit maintenance commands.
func (r *Repository) ForceImportLegacyIndex(ctx context.Context, indexPath string) (LegacyImportResult, error) {
	return r.importLegacyIndex(ctx, indexPath, true)
}

func (r *Repository) importLegacyIndex(ctx context.Context, indexPath string, force bool) (LegacyImportResult, error) {
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
	entries, err := prepareLegacyPlanEntries(absPath, legacy.Entries)
	if err != nil {
		return LegacyImportResult{SourceFound: true}, err
	}
	manifest := make([]compatimport.ManifestEntry, len(entries))
	for i := range entries {
		manifest[i] = entries[i].manifest
	}
	fingerprint := compatimport.ManifestFingerprint(raw, manifest)
	result := LegacyImportResult{SourceFound: true, Fingerprint: fingerprint, RecordCount: len(entries)}

	prior, hasPrior, err := r.LastImport(ctx, legacyPlanImportSource)
	if err != nil {
		return result, err
	}
	if !force && hasPrior && prior.Fingerprint == fingerprint {
		return result, nil
	}
	ledger, err := compatimport.LoadLedger(ctx, r.store.DB(), legacyPlanImportSource)
	if err != nil {
		return result, fmt.Errorf("plans: reading legacy entry ledger: %w", err)
	}
	priorCompletedAtNS := int64(0)
	if hasPrior {
		priorCompletedAtNS = prior.CompletedAt.UnixNano()
	}
	for i := range entries {
		entry := &entries[i]
		if force {
			entry.action = compatimport.Refresh
			entry.searchText, err = r.legacySidecarRead(absPath, entry.manifest.Key)
			if errors.Is(err, fs.ErrNotExist) {
				entry.searchText = ""
				err = nil
			}
			if err != nil {
				return result, fmt.Errorf("plans: reading legacy search sidecar for %q: %w", entry.manifest.Key, err)
			}
			entry.contentHash = compatimport.ContentHash(entry.searchText)
			continue
		}
		storedContent := ""
		canSeed := false
		ledgerEntry, hasLedger := ledger[entry.manifest.Key]
		if !hasLedger && hasPrior {
			storedContent, canSeed, err = r.canSeedLegacyPlanEntry(ctx, *entry)
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
			entry.searchText, err = r.legacySidecarRead(absPath, entry.manifest.Key)
			if errors.Is(err, fs.ErrNotExist) {
				entry.searchText = ""
				err = nil
			}
			if err != nil {
				return result, fmt.Errorf("plans: reading legacy search sidecar for %q: %w", entry.manifest.Key, err)
			}
		}
		entry.contentHash = compatimport.ContentHash(entry.searchText)
	}

	importedAtNS := r.now().UnixNano()
	err = r.legacyImportTransaction(ctx, func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT fingerprint FROM import_records WHERE source_name = ?`, legacyPlanImportSource).Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("plans: checking legacy import record: %w", err)
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
				return fmt.Errorf("plans: importing legacy entry %q: %w", entry.manifest.Key, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO import_records (source_name, source_path, fingerprint, completed_at_ns, record_count)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(source_name) DO UPDATE SET
				source_path = excluded.source_path,
				fingerprint = excluded.fingerprint,
				completed_at_ns = excluded.completed_at_ns,
				record_count = excluded.record_count
		`, legacyPlanImportSource, absPath, fingerprint, importedAtNS, len(entries)); err != nil {
			return fmt.Errorf("plans: recording legacy import completion: %w", err)
		}
		for _, entry := range entries {
			if entry.action == compatimport.Unchanged {
				continue
			}
			if err := compatimport.UpsertLedgerTx(ctx, tx, legacyPlanImportSource, compatimport.LedgerEntry{
				ManifestEntry:    entry.manifest,
				ContentHash:      entry.contentHash,
				LastImportedAtNS: importedAtNS,
			}); err != nil {
				return fmt.Errorf("plans: %w", err)
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

func prepareLegacyPlanEntries(indexPath string, metas map[string]PlanMeta) ([]legacyImportEntry, error) {
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
		meta = normalizeLegacyPlanMetadata(meta)
		metadataHash, err := compatimport.MetadataHash(meta)
		if err != nil {
			return nil, fmt.Errorf("plans: hashing legacy metadata for %q: %w", key, err)
		}
		stat, err := compatimport.StatSidecar(store.Path(key))
		if err != nil {
			return nil, fmt.Errorf("plans: stating legacy search sidecar for %q: %w", key, err)
		}
		entries = append(entries, legacyImportEntry{
			manifest: compatimport.ManifestEntry{Key: key, MetadataHash: metadataHash, Sidecar: stat},
			meta:     meta,
		})
	}
	return entries, nil
}

func normalizeLegacyPlanMetadata(meta PlanMeta) PlanMeta {
	if meta.Name == "" {
		meta.Name = DisplayName(filepath.Base(meta.DirPath))
	}
	meta.Files = append([]string(nil), meta.Files...)
	sort.Strings(meta.Files)
	meta.ModTime = meta.ModTime.UTC()
	meta.SearchText = ""
	return meta
}

func (r *Repository) canSeedLegacyPlanEntry(ctx context.Context, entry legacyImportEntry) (string, bool, error) {
	stored, ok, err := r.Get(ctx, entry.manifest.Key)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	stored = normalizeLegacyPlanMetadata(stored)
	hash, err := compatimport.MetadataHash(stored)
	if err != nil {
		return "", false, fmt.Errorf("plans: hashing stored metadata for %q: %w", entry.manifest.Key, err)
	}
	if hash != entry.manifest.MetadataHash {
		return "", false, nil
	}
	content, found, err := r.SearchText(ctx, entry.manifest.Key)
	if err != nil {
		return "", false, err
	}
	return content, found, nil
}

func readLegacySidecar(indexPath, key string) (string, error) {
	return searchstore.New(indexPath).Read(key)
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
