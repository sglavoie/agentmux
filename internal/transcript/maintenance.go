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
	MetadataPath       string
	MetadataSize       int64
	SidecarDir         string
	SidecarCount       int
	SidecarSize        int64
	VisibleEntries     int
	HiddenEntries      int
	MissingSidecars    int
	MissingSidecarKeys []string
}

// Status returns filesystem and entry counts for a transcript index.
func Status(path string) (StoreStatus, error) {
	idx, err := LoadIndexAt(path)
	if err != nil {
		return StoreStatus{}, err
	}
	status := StoreStatus{
		MetadataPath: path,
		SidecarDir:   idx.SearchStoreDir(),
	}
	if info, err := os.Stat(path); err == nil {
		status.MetadataSize = info.Size()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return StoreStatus{}, err
	}
	count, size, err := idx.SearchStoreStats()
	if err != nil {
		return StoreStatus{}, err
	}
	status.SidecarCount = count
	status.SidecarSize = size

	for _, meta := range idx.List(ListFilter{IncludeHidden: true}) {
		key := IndexKey(meta.Harness, meta.ID)
		if meta.HiddenFromIndex {
			status.HiddenEntries++
			continue
		}
		status.VisibleEntries++
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
	StatusBefore StoreStatus
	StatusAfter  StoreStatus
	LegacyFields int
}

// Compact migrates legacy inline search_text into sidecars and rewrites the
// metadata file without search text.
func Compact(path string) (CompactResult, error) {
	before, err := Status(path)
	if err != nil {
		return CompactResult{}, err
	}
	legacyFields, err := countLegacySearchTextFields(path)
	if err != nil {
		return CompactResult{}, err
	}
	idx, err := LoadIndexAt(path)
	if err != nil {
		return CompactResult{}, err
	}
	if err := idx.Save(); err != nil {
		return CompactResult{}, err
	}
	after, err := Status(path)
	if err != nil {
		return CompactResult{}, err
	}
	return CompactResult{StatusBefore: before, StatusAfter: after, LegacyFields: legacyFields}, nil
}

// RebuildResult reports the state after a sidecar rebuild.
type RebuildResult struct {
	Status StoreStatus
}

// Rebuild refreshes configured profiles and rebuilds search sidecars. When
// includeAll is true, the configured scan age cutoff is disabled for this pass.
func Rebuild(path string, cfg *config.Config, readers map[string]Reader, includeAll bool) (RebuildResult, error) {
	if cfg == nil {
		return RebuildResult{}, errors.New("transcript: Rebuild requires a non-nil config")
	}
	idx, err := LoadIndexAt(path)
	if err != nil {
		return RebuildResult{}, err
	}
	if err := os.RemoveAll(idx.SearchStoreDir()); err != nil {
		return RebuildResult{}, err
	}
	refreshCfg := *cfg
	if includeAll {
		refreshCfg.Defaults.SessionArchive.ScanMaxAgeDays = -1
	}
	if err := idx.Refresh(&refreshCfg, readers); err != nil {
		return RebuildResult{}, err
	}
	status, err := Status(path)
	if err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{Status: status}, nil
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
