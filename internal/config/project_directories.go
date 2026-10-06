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
		problem := ProjectDirectoryProblem(project.Directory)
		if problem == "" {
			continue
		}
		label := project.Title
		if profile := project.EffectiveProfile(); profile != "" {
			label = profile + "/" + project.Title
		}
		errs = append(errs, fmt.Sprintf("project %q: directory %q %s", label, project.Directory, problem))
	}
	return errs
}

// ProjectDirectoryProblem says what is wrong with one configured project
// directory ("does not exist", "is not a directory", or "cannot be inspected:
// ..."), or "" when it is usable. It is the single rule behind
// ProjectDirectoryProblems and `amx project list`, so the list cannot mark a
// project that validate accepts.
func ProjectDirectoryProblem(configuredPath string) string {
	info, err := os.Stat(ExpandTilde(configuredPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "does not exist"
		}
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return fmt.Sprintf("cannot be inspected: %v", pathErr.Err)
		}
		return fmt.Sprintf("cannot be inspected: %v", err)
	}
	if !info.IsDir() {
		return "is not a directory"
	}
	return ""
}
