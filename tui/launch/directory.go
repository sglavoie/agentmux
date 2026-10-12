package launch

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
)

// EffectiveDirectory resolves the directory a project would launch in: the
// launcher's override when set, otherwise the project's configured directory.
func (in PickerInput) EffectiveDirectory(origIdx int) string {
	if dir := in.DirOverride[origIdx]; dir != "" {
		return dir
	}
	if origIdx >= 0 && origIdx < len(in.Sessions) {
		return in.Sessions[origIdx].Directory
	}
	return ""
}

// ResolveDirectory turns what was typed at the launcher's directory prompt
// into the directory to launch in. An absolute or ~ path stands as typed; a
// relative one is read from the project's configured directory, because the
// dashboard's own working directory means nothing to the operator. Empty
// input, or the configured directory itself, resolves to "": no override.
func ResolveDirectory(configured, typed string) string {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return ""
	}
	resolved := filepath.Join(configured, typed)
	if typed == "~" || strings.HasPrefix(typed, "~/") || filepath.IsAbs(typed) {
		resolved = filepath.Clean(typed)
	}
	if config.ExpandTilde(resolved) == filepath.Clean(config.ExpandTilde(configured)) {
		return ""
	}
	return resolved
}

// CompleteDirectory extends what was typed at the directory prompt as far as
// the subdirectories on disk agree: to the one match, with a trailing slash,
// or to the prefix several share. Input nothing matches comes back unchanged.
// Hidden directories are offered only once a leading dot is typed.
func CompleteDirectory(configured, typed string) string {
	parent, prefix := "", typed
	if cut := strings.LastIndex(typed, "/"); cut >= 0 {
		parent, prefix = typed[:cut+1], typed[cut+1:]
	}
	if typed == "~" {
		return "~/"
	}
	listed := configured
	if parent != "" {
		listed = filepath.Join(configured, parent)
		if strings.HasPrefix(parent, "~/") || filepath.IsAbs(parent) {
			listed = filepath.Clean(parent)
		}
	}
	entries, err := os.ReadDir(config.ExpandTilde(listed))
	if err != nil {
		return typed
	}
	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".")) {
			continue
		}
		if !entry.IsDir() {
			info, err := os.Stat(filepath.Join(config.ExpandTilde(listed), name))
			if err != nil || !info.IsDir() {
				continue
			}
		}
		matches = append(matches, name)
	}
	if len(matches) == 0 {
		return typed
	}
	if len(matches) == 1 {
		return parent + matches[0] + "/"
	}
	common := matches[0]
	for _, name := range matches[1:] {
		for !strings.HasPrefix(name, common) {
			common = common[:len(common)-1]
		}
	}
	return parent + common
}
