package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/sglavoie/agentmux/internal/config"
)

// StoreStatus summarizes the on-disk transcript metadata file and search
// sidecar store without exposing per-session search text to daemon callers.
type StoreStatus struct {
	MetadataPath      string
	MetadataSize      int64
	SidecarDir        string
	SidecarCount      int
	SidecarSize       int64
	OrphanSidecars    int
	OrphanSidecarSize int64
	VisibleEntries    int
	HiddenEntries     int
	// EmptyEntries counts the visible entries that hold no conversation yet
	// (SessionMeta.Empty), which lists leave out until a prompt arrives.
	EmptyEntries              int
	VisibleProfileMemberships int
	HiddenProfileMemberships  int
	SharedProfileEntries      int
	MissingSidecars           int
	MissingSidecarKeys        []string
	Warnings                  []string
}

// Status returns filesystem and entry counts for a transcript index.
func Status(path string) (StoreStatus, error) {
	idx, loadWarning, err := loadMaintenanceIndex(path)
	if err != nil {
		return StoreStatus{}, err
	}
	status, err := statusForIndex(path, idx)
	if err != nil {
		return StoreStatus{}, err
	}
	status.Warnings = appendWarning(status.Warnings, loadWarning)
	return status, nil
}

func statusForIndex(path string, idx *Index) (StoreStatus, error) {
	status := StoreStatus{
		MetadataPath: path,
		SidecarDir:   idx.SearchStoreDir(),
	}
	if info, err := os.Stat(path); err == nil {
		status.MetadataSize = info.Size()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return StoreStatus{}, err
	}
	idx.mu.Lock()
	keys := make([]string, 0, len(idx.entries))
	for key := range idx.entries {
		keys = append(keys, key)
	}
	idx.mu.Unlock()
	inventory, err := idx.store.Inventory(keys)
	if err != nil {
		return StoreStatus{}, err
	}
	status.SidecarCount = inventory.Count
	status.SidecarSize = inventory.Size
	status.OrphanSidecars = len(inventory.Orphans)
	status.OrphanSidecarSize = inventory.OrphanSize

	for _, meta := range idx.List(ListFilter{IncludeHidden: true}) {
		key := IndexKey(meta.Harness, meta.ID)
		profileMemberships := len(meta.ProfileNames())
		if meta.HiddenFromIndex {
			status.HiddenEntries++
			status.HiddenProfileMemberships += profileMemberships
			continue
		}
		status.VisibleEntries++
		if meta.Empty {
			status.EmptyEntries++
		}
		status.VisibleProfileMemberships += profileMemberships
		if profileMemberships > 1 {
			status.SharedProfileEntries++
		}
		if meta.MessageCount == 0 {
			continue
		}
		exists, err := idx.SearchTextExists(key)
		if err != nil {
			return StoreStatus{}, err
		}
		if !exists {
			status.MissingSidecars++
			status.MissingSidecarKeys = append(status.MissingSidecarKeys, key)
		}
	}
	return status, nil
}

// CompactResult reports what compact observed and rewrote.
type CompactResult struct {
	StatusBefore             StoreStatus
	StatusAfter              StoreStatus
	LegacyFields             int
	RemovedOrphanSidecars    int
	RemovedOrphanSidecarSize int64
	Warnings                 []string
}

// Compact migrates legacy inline search_text, rewrites metadata, and removes
// regular hashed sidecars that are absent from the latest cache snapshot.
func Compact(path string) (CompactResult, error) {
	idx, loadWarning, err := loadMaintenanceIndex(path)
	if err != nil {
		return CompactResult{}, err
	}
	before, err := statusForIndex(path, idx)
	if err != nil {
		return CompactResult{}, err
	}
	before.Warnings = appendWarning(before.Warnings, loadWarning)
	compact, compactErr := idx.compactFilesystem()
	warnings := append([]string(nil), before.Warnings...)
	warnings = appendWarning(warnings, compact.Warning)
	result := CompactResult{
		StatusBefore:             before,
		LegacyFields:             compact.LegacyFields,
		RemovedOrphanSidecars:    compact.RemovedOrphanSidecars,
		RemovedOrphanSidecarSize: compact.RemovedOrphanSidecarSize,
		Warnings:                 warnings,
	}
	if compactErr != nil {
		return result, compactErr
	}
	after, err := Status(path)
	if err != nil {
		return result, err
	}
	warnings = append(warnings, after.Warnings...)
	result.StatusAfter = after
	result.Warnings = warnings
	return result, nil
}

// RebuildResult reports the state after a sidecar rebuild.
type RebuildResult struct {
	Status   StoreStatus
	Warnings []string
}

// Rebuild refreshes configured profiles and rebuilds search sidecars. When
// includeAll is true, the configured scan age cutoff is disabled for this pass.
func Rebuild(path string, cfg *config.Config, readers map[string]Reader, includeAll bool) (RebuildResult, error) {
	if cfg == nil {
		return RebuildResult{}, errors.New("transcript: Rebuild requires a non-nil config")
	}
	idx, loadWarning, err := loadMaintenanceIndex(path)
	if err != nil {
		return RebuildResult{}, err
	}
	refreshCfg := *cfg
	if includeAll {
		refreshCfg.Defaults.SessionArchive.ScanMaxAgeDays = -1
	}
	// rebuildFilesystem reparses every source, then resets and rewrites the
	// sidecar store inside the same cachefile.WithExclusive section.
	if err := idx.rebuildFilesystem(&refreshCfg, readers); err != nil {
		return RebuildResult{}, err
	}
	status, err := Status(path)
	if err != nil {
		return RebuildResult{}, err
	}
	warnings := appendWarning(nil, loadWarning)
	warnings = append(warnings, status.Warnings...)
	return RebuildResult{Status: status, Warnings: warnings}, nil
}

// loadMaintenanceIndex separates a usable recovering load from a hard cache
// read or lock failure. Corruption warnings are usable because LoadIndexAt has
// quarantined the malformed cache; legacy sidecar migration warnings are also
// usable because their inline search text remains in the dirty snapshot.
func loadMaintenanceIndex(path string) (*Index, error, error) {
	idx, warning := LoadIndexAt(path)
	if warning == nil {
		return idx, nil, nil
	}
	if idx == nil {
		return nil, nil, warning
	}
	var decodeErr *indexDecodeError
	if errors.As(warning, &decodeErr) || idx.filesystemDirty {
		return idx, warning, nil
	}
	return nil, nil, warning
}

func appendWarning(warnings []string, warning error) []string {
	if warning == nil {
		return warnings
	}
	return append(warnings, warning.Error())
}

func countLegacySearchTextFields(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var raw struct {
		Entries map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, fmt.Errorf("decoding transcript index %s: %w", path, err)
	}
	count := 0
	for _, entry := range raw.Entries {
		var probe struct {
			SearchText string `json:"search_text"`
		}
		if err := json.Unmarshal(entry, &probe); err != nil {
			return 0, err
		}
		if probe.SearchText != "" {
			count++
		}
	}
	return count, nil
}
