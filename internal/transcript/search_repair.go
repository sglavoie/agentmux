package transcript

import (
	"errors"
	"sync"

	"github.com/sglavoie/agentmux/internal/cachefile"
)

type searchRepair struct {
	meta SessionMeta
	text string
}

// Search reads sources outside the cache lock. Collect repairs from those
// reads, including parallel source-filtered reads, then revalidate them under
// the same lock refresh, hide, and delete use. Batches avoid rereading the index
// per transcript without holding the entire searchable corpus in memory.
type searchRepairs struct {
	mu    sync.Mutex
	index *Index
	items []searchRepair
	bytes int
}

const maxSearchRepairBytes = 8 << 20

func (r *searchRepairs) add(meta SessionMeta, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, searchRepair{meta: meta, text: text})
	r.bytes += len(text)
	if r.bytes >= maxSearchRepairBytes || len(r.items) >= 128 {
		return r.persistLocked()
	}
	return nil
}

// persist flushes the final batch after every source reader has finished.
func (r *searchRepairs) persist() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persistLocked()
}

// A search may still return its original snapshot, but must never overwrite a
// newer generation's sidecar or recreate one removed by a concurrent hide/delete.
func (r *searchRepairs) persistLocked() error {
	if len(r.items) == 0 {
		return nil
	}
	defer func() {
		r.items = nil
		r.bytes = 0
	}()
	idx := r.index
	return cachefile.WithExclusive(idx.path, func() error {
		current, err := loadIndexAtStrict(idx.path)
		if err != nil {
			return err
		}
		var warning error
		for _, repair := range r.items {
			meta := repair.meta
			key := IndexKey(meta.Harness, meta.ID)
			latest, exists := current.entries[key]
			if !exists || latest.HiddenFromIndex || latest.Path != meta.Path ||
				latest.FileSize != meta.FileSize || !latest.FileModTime.Equal(meta.FileModTime) ||
				latest.ReaderVersion != meta.ReaderVersion {
				continue
			}
			warning = errors.Join(warning, idx.WriteSearchText(key, repair.text))
		}
		return warning
	})
}
