package projectresolve

import (
	"path/filepath"
	"strings"
)

const (
	// gitFamilyKeyPrefix and projectFamilyKeyPrefix keep the two key spaces
	// apart, so a configured directory can never collide with a Git common
	// directory that happens to spell the same path.
	gitFamilyKeyPrefix     = "git:"
	projectFamilyKeyPrefix = "project:"
	// familyKeyFieldSeparator is ASCII Unit Separator for the same reason
	// session keys use it: it cannot appear in a path or in a configured
	// title, so joining fields with it cannot be made ambiguous.
	familyKeyFieldSeparator = "\x1f"
)

// Family is the repository or configured project that a working directory
// belongs to, shared by every checkout of one repository.
//
// Key is an internal grouping token and nothing else. It is built out of
// filesystem paths precisely because paths are the only stable identity a
// repository has, which is also why it must never reach a rendered surface:
// Label is the single field in here written to be read by a person. A caller
// that finds itself displaying Key has a bug, not a formatting problem.
//
// The zero Family means "no family" — either nothing matched or the evidence
// disagreed with itself. Callers leave their family fields empty in that case
// rather than grouping on a guess.
type Family struct {
	Key   string
	Label string
	// IsRepository distinguishes a Git family, whose members are the checkouts
	// sharing one common directory, from a lone configured non-Git project.
	IsRepository bool
	// PrimaryRoot is the main checkout root of a Git family, and the canonical
	// configured directory of a non-Git one.
	PrimaryRoot string
}

// ResolveFamily returns the family that cwd belongs to.
//
// It reuses the same match that ghost recovery runs on, so it adds no Git probe
// cycle of its own: every fact it needs was already collected when the resolver
// was built. ok is false when no configured project matches cwd, and when the
// matching projects do not agree on one family — two configured entries rooted
// in the same repository are one family and resolve, two unrelated projects
// tied at the same directory are ambiguous and do not.
func (r Resolver) ResolveFamily(cwd string) (Family, bool) {
	abs := AbsPath(cwd)
	if abs == "" {
		return Family{}, false
	}
	matched := r.matchedProjects(abs)
	if len(matched) == 0 {
		return Family{}, false
	}

	key := ""
	var chosen resolvedProject
	for i, p := range matched {
		candidate := p.familyKey()
		if candidate == "" {
			return Family{}, false
		}
		if i == 0 {
			key, chosen = candidate, p
			continue
		}
		if candidate != key {
			return Family{}, false
		}
	}
	return r.familyFor(key, chosen), true
}

func (r Resolver) familyFor(key string, p resolvedProject) Family {
	if p.git.commonDir == "" {
		return Family{Key: key, Label: p.displayLabel(), PrimaryRoot: p.dir}
	}
	primaryRoot := p.git.primaryRoot
	if primaryRoot == "" {
		primaryRoot = p.git.worktreeRoot
	}
	return Family{
		Key:          key,
		Label:        r.gitFamilyLabel(primaryRoot),
		IsRepository: true,
		PrimaryRoot:  primaryRoot,
	}
}

// gitFamilyLabel names a repository after the configured project rooted at its
// main checkout, but only when exactly one configured project is. Zero such
// projects (the family was reached through a subdirectory or a linked worktree)
// and several of them (the same checkout configured twice) both fall back to
// the checkout's own directory name, which is deterministic where picking one
// of several configured titles would not be.
func (r Resolver) gitFamilyLabel(primaryRoot string) string {
	label := ""
	rooted := 0
	for _, candidate := range r.projects {
		if candidate.dir != primaryRoot {
			continue
		}
		rooted++
		label = candidate.displayLabel()
	}
	if rooted == 1 && label != "" {
		return label
	}
	return filepath.Base(primaryRoot)
}

// familyKey is the grouping identity of one configured project. Checkouts of
// one repository share a Git common directory and therefore one key, whichever
// worktree a session happens to sit in. A project with no Git evidence keys on
// its own configured identity instead, so it groups with itself and nothing
// else.
func (p resolvedProject) familyKey() string {
	if p.git.commonDir != "" {
		return gitFamilyKeyPrefix + p.git.commonDir
	}
	if p.dir == "" {
		return ""
	}
	return projectFamilyKeyPrefix + strings.Join(
		[]string{p.dir, p.profile, p.title},
		familyKeyFieldSeparator,
	)
}

func (p resolvedProject) displayLabel() string {
	if p.title != "" {
		return p.title
	}
	return filepath.Base(p.dir)
}
