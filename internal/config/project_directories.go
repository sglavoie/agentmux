package config

import (
	"errors"
	"fmt"
	"os"
)

// ProjectDirectoryProblems lists, in config order, every project whose
// directory is missing, uninspectable, or not a directory. `amx config
// validate` refuses on any of them and `amx doctor` reports them as one
// warning, so the two cannot disagree about which projects are broken.
// Each entry names its project as profile/title, since a project is
// identified by both and the same title commonly appears under several
// profiles.
func ProjectDirectoryProblems(projects []Session) []string {
	var errs []string
	for _, project := range projects {
		configuredPath := project.Directory
		label := project.Title
		if profile := project.EffectiveProfile(); profile != "" {
			label = profile + "/" + project.Title
		}
		info, err := os.Stat(ExpandTilde(configuredPath))
		if err != nil {
			var reason string
			if errors.Is(err, os.ErrNotExist) {
				reason = "does not exist"
			} else {
				var pathErr *os.PathError
				if errors.As(err, &pathErr) {
					reason = fmt.Sprintf("cannot be inspected: %v", pathErr.Err)
				} else {
					reason = fmt.Sprintf("cannot be inspected: %v", err)
				}
			}
			errs = append(errs, fmt.Sprintf(
				"project %q: directory %q %s",
				label, configuredPath, reason,
			))
			continue
		}
		if !info.IsDir() {
			errs = append(errs, fmt.Sprintf(
				"project %q: directory %q is not a directory",
				label, configuredPath,
			))
		}
	}
	return errs
}
