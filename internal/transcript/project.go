package transcript

import (
	"path/filepath"

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

func matchProject(cwd string, projects []config.Session) projectMatch {
	abs := absPath(cwd)
	for _, p := range projects {
		if p.Directory == "" {
			continue
		}
		if absPath(p.Directory) == abs {
			return projectMatch{title: p.Title, profile: p.EffectiveProfile()}
		}
	}
	if abs == "" {
		return projectMatch{}
	}
	return projectMatch{title: filepath.Base(abs)}
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
