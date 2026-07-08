package transcript

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
)

// ResolveProject maps a transcript's recorded working directory to the
// human-readable project title from the user's config. It expands `~` and
// makes both sides absolute before comparing so the match works regardless
// of how the harness recorded the path.
//
// When no configured project matches, it falls back to the base name of the
// directory so the search view always has something to display.
func ResolveProject(cwd string, projects []config.Session) string {
	return matchProject(cwd, projects).title
}

type projectMatch struct {
	title   string
	profile string
}

// matchProject maps a session's working directory to a configured project.
// An exact directory match wins outright. Otherwise the project whose
// directory is a path-boundary ancestor of the cwd with the longest
// normalized path is chosen, so a session run in a subdirectory (or git
// worktree nested under the project) groups under that project. When no
// project directory is an ancestor, the base name of the cwd is used as a
// fallback pseudo-project title.
func matchProject(cwd string, projects []config.Session) projectMatch {
	abs := absPath(cwd)
	if abs == "" {
		return projectMatch{}
	}

	var (
		best    projectMatch
		bestLen = -1
	)
	for _, p := range projects {
		if p.Directory == "" {
			continue
		}
		dir := absPath(p.Directory)
		if dir == "" {
			continue
		}
		if dir == abs {
			// Exact match is the most specific possible result.
			return projectMatch{title: p.Title, profile: p.EffectiveProfile()}
		}
		if !pathHasAncestor(abs, dir) {
			continue
		}
		if len(dir) > bestLen {
			bestLen = len(dir)
			best = projectMatch{title: p.Title, profile: p.EffectiveProfile()}
		}
	}
	if bestLen >= 0 {
		return best
	}
	return projectMatch{title: filepath.Base(abs)}
}

// pathHasAncestor reports whether ancestor is a proper directory prefix of
// path on a path-segment boundary. Both arguments must already be normalized
// (absolute, symlink-evaluated, cleaned). The boundary check prevents
// "/foo/bar" from matching "/foo/bar-baz".
func pathHasAncestor(path, ancestor string) bool {
	if ancestor == "" || path == "" {
		return false
	}
	sep := string(os.PathSeparator)
	prefix := ancestor
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	return strings.HasPrefix(path, prefix)
}

// absPath expands `~` and converts to an absolute, symlink-evaluated path.
// All steps are best-effort: any failure returns the input cleaned with
// filepath.Clean so callers always get a comparable string.
func absPath(p string) string {
	if p == "" {
		return ""
	}
	p = config.ExpandTilde(p)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return filepath.Clean(p)
}
