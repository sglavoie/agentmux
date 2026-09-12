package transcript

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sglavoie/agentmux/internal/cachefile"
)

var writeTranscriptIndex = cachefile.WriteFileAtomic

// saveLocked persists metadata even when one or more legacy inline search
// bodies cannot be migrated to sidecars. Successfully migrated bodies are
// removed from i.entries; failed bodies remain inline in both memory and the
// metadata JSON so a later load/save can retry without losing search data.
// The first return value contains those retryable sidecar warnings, while the
// second reports whether the authoritative metadata write itself failed.
func (i *Index) saveLocked() (warning, persistErr error) {
	for key, meta := range i.entries {
		if meta.SearchText == "" {
			continue
		}
		// The caller already holds i.mu, so persist directly through the
		// sidecar store before writing the metadata cache.
		if err := i.WriteSearchText(key, meta.SearchText); err != nil {
			warning = errors.Join(warning, fmt.Errorf("migrating transcript search sidecar %s: %w", key, err))
			continue
		}
		meta.SearchText = ""
		i.entries[key] = meta
	}
	data, err := json.MarshalIndent(indexFile{Entries: i.entries}, "", "  ")
	if err != nil {
		return warning, err
	}
	if err := writeTranscriptIndex(i.path, data, 0o644); err != nil {
		return warning, err
	}
	if saveLockedHookForTest != nil {
		saveLockedHookForTest()
	}
	return warning, nil
}
