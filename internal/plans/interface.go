// Package plans discovers, indexes, and searches agent-plan directories
// stored on disk under a configurable root (default
// ~/.local/share/agent-plans). Each top-level directory is treated as a
// single plan whose search corpus is the concatenation of every .md file
// at depth <= 2 inside it. SQLite is the active metadata/search repository;
// JSON and sidecars remain read-only compatibility sources for this release.
package plans

import (
	"regexp"
	"time"
)

// PlanMeta describes a single agent-plan directory. SearchText is the
// transient, lowercased concatenation of every .md file body. It is stored in
// SQLite after the cutover and remains absent from compatibility JSON, which
// is why its JSON tag is "-".
type PlanMeta struct {
	Name       string    `json:"name"`
	DirPath    string    `json:"dir_path"`
	Files      []string  `json:"files"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	Snippet    string    `json:"snippet"`
	SearchText string    `json:"-"`
}

// PlanKey returns the stable lookup key for a plan directory. The
// absolute directory path is used directly so a rename is treated as a
// distinct entry rather than the same plan under a new name.
func PlanKey(dirPath string) string { return dirPath }

// snippetMaxLen caps the per-plan snippet length stored in the index,
// matching the limit used by internal/transcript so the dashboard
// surfaces a consistent slice of text across S and P views.
const snippetMaxLen = 160

// trailingHashRegex matches one or more trailing "-<hex12>" suffixes
// (lowercase, exactly 12 hex characters each). Plan directories are
// generated with one or two such hashes; DisplayName strips them so the
// human-friendly title remains.
var trailingHashRegex = regexp.MustCompile(`(?:-[0-9a-f]{12})+$`)

// DisplayName strips one or more trailing "-<hex12>" suffixes from a
// directory basename. Inputs without a matching suffix are returned
// unchanged.
func DisplayName(dirBase string) string {
	return trailingHashRegex.ReplaceAllString(dirBase, "")
}

// truncateSnippet collapses interior whitespace and trims to
// snippetMaxLen runes, appending an ellipsis when truncated. Behaviour
// matches transcript.truncateSnippet; duplicated rather than imported to
// keep the plans package free of transcript dependencies.
func truncateSnippet(s string) string {
	out := make([]rune, 0, snippetMaxLen)
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		if r == ' ' {
			if prevSpace {
				continue
			}
		}
		if len(out) >= snippetMaxLen {
			return string(out) + "…"
		}
		prevSpace = r == ' '
		out = append(out, r)
	}
	return string(out)
}
