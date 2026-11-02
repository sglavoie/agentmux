package conversation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/cachefile"
)

// Link is a recorded continuation, not an inferred relationship between prose.
// TargetKey names the receiving launch until its first conversation is known.
type Link struct {
	ID           string    `json:"id"`
	Source       Identity  `json:"source"`
	TargetKey    string    `json:"target_key"`
	Target       *Identity `json:"target,omitempty"`
	Kind         string    `json:"kind"`
	ArtifactPath string    `json:"artifact_path"`
	CreatedAt    time.Time `json:"created_at"`
}

type threadFile struct {
	SchemaVersion int                 `json:"schema_version"`
	Links         []Link              `json:"links"`
	Bindings      map[string]Identity `json:"bindings"`
}

// ThreadStore is shared durable state. Missing files mean no recorded links;
// unreadable files are preserved and reported, never treated as empty history.
type ThreadStore struct{ Path string }

func DefaultThreadStore() (ThreadStore, error) {
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ThreadStore{}, err
		}
		root = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(root) {
		return ThreadStore{}, errors.New("conversation threads: state directory must be absolute")
	}
	return ThreadStore{Path: filepath.Join(root, "agent-mux", "conversation-links.json")}, nil
}

func (s ThreadStore) load() (threadFile, error) {
	f := threadFile{SchemaVersion: 1, Links: []Link{}, Bindings: map[string]Identity{}}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var decoded threadFile
	if err := d.Decode(&decoded); err != nil {
		return f, fmt.Errorf("conversation threads: %w", err)
	}
	f = decoded
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return f, errors.New("conversation threads: trailing data")
	}
	if f.SchemaVersion != 1 || f.Bindings == nil || f.Links == nil {
		return f, errors.New("conversation threads: unsupported or incomplete file")
	}
	seen := map[string]bool{}
	for _, link := range f.Links {
		if err := validLink(link); err != nil {
			return f, err
		}
		if seen[link.ID] {
			return f, errors.New("conversation threads: duplicate link")
		}
		seen[link.ID] = true
	}
	for key, identity := range f.Bindings {
		if key == "" || !identity.Valid() {
			return f, errors.New("conversation threads: invalid binding")
		}
	}
	return f, nil
}

func validLink(link Link) error {
	if link.ID == "" || !link.Source.Valid() || link.TargetKey == "" || link.Kind != "handoff" || link.CreatedAt.IsZero() || (link.Target != nil && !link.Target.Valid()) {
		return errors.New("conversation threads: invalid continuation")
	}
	return nil
}

func (s ThreadStore) update(change func(*threadFile) (bool, error)) error {
	if !filepath.IsAbs(s.Path) {
		return errors.New("conversation threads: path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	return cachefile.WithExclusive(s.Path, func() error {
		f, err := s.load()
		if err != nil {
			return err
		}
		changed, err := change(&f)
		if err != nil || !changed {
			return err
		}
		b, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			return err
		}
		return atomicfile.Write(s.Path, append(b, '\n'), 0o600, atomicfile.WithDirSync(atomicfile.SyncDirBestEffort))
	})
}

// Record is idempotent for a single artifact/launch and preserves bindings
// that arrived before the launcher finished recording the relationship.
func (s ThreadStore) Record(link Link) error {
	if err := validLink(link); err != nil {
		return err
	}
	return s.update(func(f *threadFile) (bool, error) {
		for _, old := range f.Links {
			if old.ID == link.ID {
				if old.Source != link.Source || old.TargetKey != link.TargetKey || old.ArtifactPath != link.ArtifactPath {
					return false, errors.New("conversation threads: conflicting link")
				}
				return false, nil
			}
		}
		f.Links = append(f.Links, link)
		return true, nil
	})
}

// Observe binds only the first validated conversation of a launch. A later
// /new in the same terminal must not move an earlier handoff to another chat.
func (s ThreadStore) Observe(bindings map[string]Identity) error {
	if len(bindings) == 0 {
		return nil
	}
	return s.update(func(f *threadFile) (bool, error) {
		changed := false
		for key, identity := range bindings {
			if key == "" || !identity.Valid() {
				return false, errors.New("conversation threads: invalid observation")
			}
			if _, ok := f.Bindings[key]; !ok {
				f.Bindings[key] = identity
				changed = true
			}
		}
		return changed, nil
	})
}

func (s ThreadStore) Links() ([]Link, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	for i := range f.Links {
		if identity, ok := f.Bindings[f.Links[i].TargetKey]; ok {
			f.Links[i].Target = &identity
		}
	}
	sort.Slice(f.Links, func(i, j int) bool {
		if f.Links[i].CreatedAt.Equal(f.Links[j].CreatedAt) {
			return f.Links[i].ID < f.Links[j].ID
		}
		return f.Links[i].CreatedAt.Before(f.Links[j].CreatedAt)
	})
	return f.Links, nil
}

// Thread returns the connected component, including pending continuations.
// Identity always includes its harness, and traversal terminates on cycles.
func Thread(links []Link, identity Identity) []Link {
	return Threads(links, []Identity{identity})
}

// Threads returns all components reachable from the supplied conversations in
// linear time, including branches and pending launches.
func Threads(links []Link, seeds []Identity) []Link {
	adjacency := map[Identity][]int{}
	for i, link := range links {
		adjacency[link.Source] = append(adjacency[link.Source], i)
		if link.Target != nil {
			adjacency[*link.Target] = append(adjacency[*link.Target], i)
		}
	}
	known := map[Identity]bool{}
	queue := append([]Identity(nil), seeds...)
	for len(queue) > 0 {
		identity := queue[0]
		queue = queue[1:]
		if known[identity] {
			continue
		}
		known[identity] = true
		for _, i := range adjacency[identity] {
			link := links[i]
			if !known[link.Source] {
				queue = append(queue, link.Source)
			}
			if link.Target != nil && !known[*link.Target] {
				queue = append(queue, *link.Target)
			}
		}
	}
	out := []Link{}
	for _, link := range links {
		if known[link.Source] {
			out = append(out, link)
		}
	}
	return out
}
