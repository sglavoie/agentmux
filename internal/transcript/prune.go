package transcript

import (
	"errors"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/cachefile"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/timeutil"
)

// Prune marks every index entry whose LastAt is older than
// cfg.Defaults.SessionArchive.KeepDays as hidden. The transcript files
// on disk are never touched — Prune only flips HiddenFromIndex so the
// TUI's session search no longer surfaces stale conversations. Use
// Index.Delete with alsoFile=true to remove the underlying file.
//
// Prune returns the number of entries that flipped from visible to
// hidden during this call. Entries already hidden are left untouched.
func Prune(idx *Index, cfg *config.Config, now time.Time) (int, error) {
	if idx == nil {
		return 0, errors.New("transcript: Prune requires a non-nil index")
	}
	return idx.pruneFilesystem(cfg, now)
}

func (i *Index) pruneFilesystem(cfg *config.Config, now time.Time) (hidden int, retErr error) {
	if cfg == nil {
		return 0, errors.New("transcript: Prune requires a non-nil config")
	}
	keepDays := cfg.Defaults.SessionArchive.KeepDays
	if keepDays <= 0 {
		return 0, nil
	}
	cutoff := timeutil.DaysAgo(now, keepDays)
	err := cachefile.WithExclusive(i.path, func() error {
		i.mu.Lock()
		defer i.mu.Unlock()
		canPersist, warning := i.reloadFilesystemLocked()
		if !canPersist {
			return warning
		}
		for key, meta := range i.entries {
			if meta.HiddenFromIndex || meta.LastAt.IsZero() || !meta.LastAt.Before(cutoff) {
				continue
			}
			// A failed sidecar cleanup must leave this entry retryable without
			// preventing the rest of the batch from being hidden and saved.
			if sidecarErr := i.DeleteSearchText(key); sidecarErr != nil {
				warning = errors.Join(warning, fmt.Errorf("removing transcript search sidecar %s: %w", i.SearchTextPath(key), sidecarErr))
				continue
			}
			meta.HiddenFromIndex = true
			meta.SearchText = ""
			meta.SearchTextPending = false
			i.entries[key] = meta
			i.filesystemDirty = true
			hidden++
		}
		// A previous save may have failed after hiding all eligible entries.
		// Retrying must persist that pending state even with no new changes.
		if !i.filesystemDirty {
			return warning
		}
		persistWarning, persistErr := i.persistFilesystemLocked()
		return errors.Join(warning, persistWarning, persistErr)
	})
	return hidden, err
}
