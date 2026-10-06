package config

import (
	"errors"
	"fmt"
	"os"
)

// ProjectDirectoryIssue is one configured project whose directory is
// unusable: the project as profile/title, the directory as configured, and
// what is wrong with it.
type ProjectDirectoryIssue struct {
	Project   string
	Directory string
	Problem   string
}

// String renders the issue with the directory exactly as configured.
func (i ProjectDirectoryIssue) String() string {
	return i.Format(i.Directory)
}

// Format renders the issue naming the directory as directory, so a
// human-facing caller can shorten it without re-deriving the sentence.
func (i ProjectDirectoryIssue) Format(directory string) string {
	return fmt.Sprintf("project %q: directory %q %s", i.Project, directory, i.Problem)
}

// ProjectDirectoryIssues lists, in config order, every project whose
// directory is missing, uninspectable, or not a directory. `amx config
// validate` and `amx doctor` both warn about them (validate refuses on them
// only under --strict), so the two cannot disagree about which projects are
// broken. Each entry names its project as
// profile/title, since a project is identified by both and the same title
// commonly appears under several profiles.
func ProjectDirectoryIssues(projects []Session) []ProjectDirectoryIssue {
	var issues []ProjectDirectoryIssue
	for _, project := range projects {
		problem := ProjectDirectoryProblem(project.Directory)
		if problem == "" {
			continue
		}
		label := project.Title
		if profile := project.EffectiveProfile(); profile != "" {
			label = profile + "/" + project.Title
		}
		issues = append(issues, ProjectDirectoryIssue{Project: label, Directory: project.Directory, Problem: problem})
	}
	return issues
}

// ProjectDirectoryProblems is ProjectDirectoryIssues rendered as sentences
// naming each directory as configured.
func ProjectDirectoryProblems(projects []Session) []string {
	var problems []string
	for _, issue := range ProjectDirectoryIssues(projects) {
		problems = append(problems, issue.String())
	}
	return problems
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
