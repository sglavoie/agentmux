package plans

import (
	"errors"

	"github.com/sglavoie/agentmux/internal/cachefile"
)

type searchRepair struct {
	meta PlanMeta
	text string
}

// Source reads happen outside the cache lock. Bound pending repairs so a large
// fallback search neither retains the whole corpus nor reloads plans.json for
// every plan. Search is sequential, so this batch needs no separate mutex.
type searchRepairs struct {
	index *Index
	items []searchRepair
	bytes int
}

func (r *searchRepairs) add(meta PlanMeta, text string) error {
	r.items = append(r.items, searchRepair{meta: meta, text: text})
	r.bytes += len(text)
	if r.bytes >= 8<<20 || len(r.items) >= 128 {
		return r.persist()
	}
	return nil
}

func (r *searchRepairs) persist() error {
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
			key := PlanKey(meta.DirPath)
			latest, exists := current.entries[key]
			// A search may return its original snapshot, but must not restore
			// a deleted sidecar or overwrite another generation's corpus.
			if !exists || latest.DirPath != meta.DirPath || latest.Size != meta.Size ||
				!latest.ModTime.Equal(meta.ModTime) || !equalStrings(latest.Files, meta.Files) {
				continue
			}
			warning = errors.Join(warning, idx.store.Write(key, repair.text))
		}
		return warning
	})
}
