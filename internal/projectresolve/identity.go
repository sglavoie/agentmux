package projectresolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sglavoie/agentmux/internal/config"
)

// Identity is what a directory *is*, resolved strictly enough to authorize on.
//
// It is deliberately not a Match. A Match answers "which configured project
// should this row be labelled with", and the resolver behind it is allowed to
// pick the most specific candidate and to fall back to a basename when nothing
// fits, because a mislabelled dashboard row is a cosmetic defect. An Identity
// answers "which repository is this, exactly", and it has no degraded form: a
// path that cannot be resolved, or a Git probe that cannot be believed, is an
// error rather than an Identity with empty fields.
//
// RelativeDir is the directory's position inside its own checkout ("." at the
// root). Together with WorktreeRoots it is what lets a caller decide whether
// two directories in different checkouts of one repository are the same place,
// which is the only way an exclusion on a main checkout can reach the linked
// worktree that would otherwise bypass it.
type Identity struct {
	// Path is the canonical absolute, symlink-evaluated directory.
	Path string
	// IsRepository is true only when Git answered with a complete identity.
	IsRepository bool
	// CommonDir is the Git common directory shared by every checkout of the
	// repository — the family identity.
	CommonDir string
	// WorktreeRoot is the root of the checkout Path sits in.
	WorktreeRoot string
	// WorktreeRoots is every registered checkout of the repository, main
	// checkout first, as reported from Path.
	WorktreeRoots []string
	// RelativeDir is Path relative to WorktreeRoot, or "." at the root.
	RelativeDir string
}

var (
	// ErrPathUnresolved means the directory itself could not be established:
	// it was empty, it does not exist, it is not a directory, or a symlink on
	// the way to it dangles.
	ErrPathUnresolved = errors.New("projectresolve: path does not resolve to an existing directory")
	// ErrIdentityUnresolved means the directory exists but Git could not say
	// what repository it belongs to. A caller authorizing on identity must
	// treat this as a refusal, not as "no repository": a probe that times out
	// inside a linked worktree looks exactly like a plain directory, and the
	// two must not be allowed to collapse into each other.
	ErrIdentityUnresolved = errors.New("projectresolve: git identity could not be resolved")
)

// IdentityResolver resolves directories to strict identities. It holds the same
// Git probe the display resolver uses, so the two agree about what a repository
// family is; they differ only in what they do when the probe comes back empty.
type IdentityResolver struct {
	git gitMetadataProvider
}

// NewIdentityResolver returns a resolver backed by the real `git` probe.
func NewIdentityResolver() IdentityResolver {
	return newIdentityResolver(realGitMetadataProvider{timeout: gitMetadataCommandTimeout})
}

func newIdentityResolver(provider gitMetadataProvider) IdentityResolver {
	return IdentityResolver{git: provider}
}

// ResolveIdentity resolves path to its physical location and its Git family.
//
// Every step that could paper over a difference between two directories is an
// error instead: `~` expansion and symlink evaluation must succeed, the target
// must exist and be a directory, and a repository must report both a checkout
// root and a common directory. A directory that Git positively reports is not a
// repository is not an error — it resolves to a non-repository Identity, which
// is a fact about it rather than a missing answer.
func (r IdentityResolver) ResolveIdentity(path string) (Identity, error) {
	if path == "" {
		return Identity{}, fmt.Errorf("%w: empty path", ErrPathUnresolved)
	}
	abs, err := filepath.Abs(config.ExpandTilde(path))
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %s: %w", ErrPathUnresolved, path, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %s: %w", ErrPathUnresolved, abs, err)
	}
	real = filepath.Clean(real)
	info, err := os.Stat(real)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %s: %w", ErrPathUnresolved, real, err)
	}
	if !info.IsDir() {
		return Identity{}, fmt.Errorf("%w: %s is not a directory", ErrPathUnresolved, real)
	}

	meta, err := r.git.GitMetadata(real)
	if err != nil {
		if errors.Is(err, errNotARepository) {
			return Identity{Path: real}, nil
		}
		return Identity{}, fmt.Errorf("%w: %s: %w", ErrIdentityUnresolved, real, err)
	}

	root := AbsPath(meta.worktreeRoot)
	commonDir := AbsPath(meta.commonDir)
	if root == "" || commonDir == "" {
		return Identity{}, fmt.Errorf("%w: %s: git reported an incomplete repository identity", ErrIdentityUnresolved, real)
	}
	relative, ok := relativePathWithin(real, root)
	if !ok {
		return Identity{}, fmt.Errorf("%w: %s: git reported checkout root %s that does not contain it", ErrIdentityUnresolved, real, root)
	}
	return Identity{
		Path:          real,
		IsRepository:  true,
		CommonDir:     commonDir,
		WorktreeRoot:  root,
		WorktreeRoots: normalizeWorktreeRoots(root, meta.worktreeRoots),
		RelativeDir:   relative,
	}, nil
}
