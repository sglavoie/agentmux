package plans

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/datastore"
)

// LoadSQLiteIndex loads the compatibility cache, then activates SQLite after
// a successful read-only import. SQLite failures retain the existing
// JSON/sidecar behavior rather than hiding plans from the TUI.
func LoadSQLiteIndex(path string) (*Index, error) {
	idx, err := LoadIndexAt(path)
	if err != nil {
		return nil, err
	}
	store, err := datastore.Open(context.Background())
	if err != nil {
		idx.setDegradedReason(err)
		return idx, nil
	}
	return EnableSQLiteIndex(context.Background(), idx, store), nil
}

// EnableSQLiteIndex activates SQLite for an existing compatibility index.
// Import failures leave that index usable and never alter plan directories,
// plans.json, or its sidecars.
func EnableSQLiteIndex(ctx context.Context, idx *Index, store *datastore.Store) *Index {
	if idx == nil {
		return nil
	}
	if store == nil {
		idx.setDegradedReason(errors.New("SQLite datastore is unavailable"))
		return idx
	}
	repo, err := NewRepository(store)
	if err != nil {
		idx.setDegradedReason(err)
		_ = store.Close()
		return idx
	}
	if _, err := repo.ImportLegacyIndex(ctx, idx.path); err != nil {
		idx.setDegradedReason(err)
		_ = store.Close()
		return idx
	}
	idx.mu.Lock()
	idx.repo = repo
	idx.degradedReason = nil
	idx.mu.Unlock()
	return idx
}

// UsesSQLite reports whether this index is currently served from SQLite.
func (i *Index) UsesSQLite() bool {
	if i == nil {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.repo != nil
}

// DegradedReason explains why the compatibility cache remains active.
func (i *Index) DegradedReason() error {
	if i == nil {
		return errors.New("plans: nil index")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.degradedReason
}

func (i *Index) setDegradedReason(err error) {
	if err == nil {
		return
	}
	i.mu.Lock()
	i.degradedReason = fmt.Errorf("SQLite unavailable; using legacy plan index: %w", err)
	i.mu.Unlock()
}

func (i *Index) repository() *Repository {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.repo
}

func (i *Index) deleteSQLite(key string) error {
	repo := i.repository()
	if repo == nil {
		return errors.New("plans: SQLite repository is not enabled")
	}
	if err := repo.Delete(context.Background(), key); err != nil {
		return err
	}
	i.mu.Lock()
	delete(i.entries, key)
	i.mu.Unlock()
	return nil
}

// refreshSQLite mirrors legacy scan rules while writing only database rows.
// Compatibility JSON and search sidecars remain untouched after cutover.
func (i *Index) refreshSQLite(root string) error {
	if root == "" {
		return errors.New("plans: Refresh requires a non-empty root path")
	}
	repo := i.repository()
	if repo == nil {
		return errors.New("plans: SQLite repository is not enabled")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolving plans root %s: %w", root, err)
	}
	dirEntries, err := os.ReadDir(absRoot)
	if err != nil {
		return fmt.Errorf("reading plans root %s: %w", absRoot, err)
	}

	ctx := context.Background()
	for _, entry := range dirEntries {
		if !entry.IsDir() {
			continue
		}
		planDir := filepath.Join(absRoot, entry.Name())
		key := PlanKey(planDir)
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "sources" {
			if _, found, err := repo.Get(ctx, key); err != nil {
				return err
			} else if found {
				if err := i.removeDerivedPlan(ctx, repo, key); err != nil {
					return err
				}
			}
			continue
		}
		meta, content, err := scanPlanDirectory(planDir)
		if err != nil {
			slog.Debug("plans.index: refresh plan failed", "dir", planDir, "err", err)
			continue
		}
		if meta == nil {
			if _, found, err := repo.Get(ctx, key); err != nil {
				return err
			} else if found {
				if err := i.removeDerivedPlan(ctx, repo, key); err != nil {
					return err
				}
			}
			continue
		}
		if err := repo.Upsert(ctx, *meta, content); err != nil {
			return fmt.Errorf("plans: refreshing %q in SQLite: %w", key, err)
		}
		i.mu.Lock()
		i.entries[key] = *meta
		i.mu.Unlock()
	}

	// Preserve legacy behavior: remove an indexed plan only after its directory
	// disappears. Valid entries outside this refresh root stay globally visible.
	metas, err := repo.List(ctx)
	if err != nil {
		return err
	}
	for _, meta := range metas {
		if _, statErr := os.Stat(meta.DirPath); statErr == nil {
			continue
		}
		if err := i.removeDerivedPlan(ctx, repo, PlanKey(meta.DirPath)); err != nil {
			return err
		}
	}
	return nil
}

// removeDerivedPlan prunes stale derived metadata only when doing so cannot
// cascade into an explicit link or durable handoff. Referenced rows remain as
// metadata placeholders with no search document until their source returns.
func (i *Index) removeDerivedPlan(ctx context.Context, repo *Repository, key string) error {
	referenced, err := repo.HasDurableReferences(ctx, key)
	if err != nil {
		return err
	}
	if referenced {
		return repo.DeleteSearchText(ctx, key)
	}
	if err := repo.Delete(ctx, key); err != nil {
		return err
	}
	i.mu.Lock()
	delete(i.entries, key)
	i.mu.Unlock()
	return nil
}

// scanPlanDirectory applies the existing depth, file-list, snippet, and
// lowercased literal-search rules without writing any sidecars.
func scanPlanDirectory(planDir string) (*PlanMeta, string, error) {
	type fileInfo struct {
		rel  string
		size int64
		mod  int64
	}
	files := make([]fileInfo, 0)
	walkErr := filepath.WalkDir(planDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == planDir {
			return nil
		}
		rel, err := filepath.Rel(planDir, path)
		if err != nil {
			return err
		}
		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if d.IsDir() {
			if depth >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if depth > 2 || filepath.Ext(d.Name()) != ".md" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, fileInfo{rel: rel, size: info.Size(), mod: info.ModTime().UnixNano()})
		return nil
	})
	if walkErr != nil {
		return nil, "", walkErr
	}
	if len(files) == 0 {
		return nil, "", nil
	}
	sort.Slice(files, func(a, b int) bool { return files[a].rel < files[b].rel })

	var size int64
	var modTime int64
	rels := make([]string, 0, len(files))
	for _, file := range files {
		size += file.size
		if file.mod > modTime {
			modTime = file.mod
		}
		rels = append(rels, file.rel)
	}

	var content strings.Builder
	var manifestSnippet, firstSnippet string
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(planDir, file.rel))
		if err != nil {
			return nil, "", err
		}
		if content.Len() > 0 {
			content.WriteByte('\n')
		}
		content.WriteString(strings.ToLower(string(data)))
		if firstSnippet == "" {
			firstSnippet = extractSnippet(string(data))
		}
		if manifestSnippet == "" && filepath.Base(file.rel) == "manifest.md" {
			manifestSnippet = extractSnippet(string(data))
		}
	}
	snippet := manifestSnippet
	if snippet == "" {
		snippet = firstSnippet
	}
	return &PlanMeta{
		Name: DisplayName(filepath.Base(planDir)), DirPath: planDir, Files: rels,
		Size: size, ModTime: planTimeFromNS(modTime), Snippet: snippet,
	}, content.String(), nil
}
