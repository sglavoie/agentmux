package transcript

import (
	"errors"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
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
	if idx.UsesSQLite() {
		return idx.pruneSQLite(cfg, now)
	}
	if cfg == nil {
		return 0, errors.New("transcript: Prune requires a non-nil config")
	}

	keepDays := cfg.Defaults.SessionArchive.KeepDays
	if keepDays <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-time.Duration(keepDays) * 24 * time.Hour)

	hidden := 0
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for key, meta := range idx.entries {
		if meta.HiddenFromIndex {
			continue
		}
		if meta.LastAt.IsZero() || !meta.LastAt.Before(cutoff) {
			continue
		}
		meta.HiddenFromIndex = true
		idx.entries[key] = meta
		// Prune's legacy branch holds idx.mu, so do not call the
		// SQLite-aware facade (which also inspects that mutex).
		if err := idx.store.Delete(key); err != nil {
			return hidden, err
		}
		hidden++
	}
	if hidden == 0 {
		return 0, nil
	}
	if err := idx.saveLocked(); err != nil {
		return hidden, err
	}
	return hidden, nil
}
