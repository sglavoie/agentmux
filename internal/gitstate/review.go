package gitstate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Changes is a checkout-wide comparison, never an attribution to one agent.
// Base is the resolved commit so subsequent file reads can use the same base.
type Changes struct {
	Root      string        `json:"root"`
	Base      string        `json:"base"`
	Files     []ChangedFile `json:"files"`
	Patch     string        `json:"patch,omitempty"`
	Truncated bool          `json:"truncated"`
}

type ChangedFile struct {
	Path      string `json:"path"`
	Added     int    `json:"added"`
	Deleted   int    `json:"deleted"`
	Binary    bool   `json:"binary"`
	Untracked bool   `json:"untracked"`
}

const reviewOutputLimit = 2 * 1024 * 1024

type reviewBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *reviewBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := reviewOutputLimit - b.Len()
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func reviewGit(ctx context.Context, dir string, args ...string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// A diff against the work tree refreshes the index and writes it back
	// under index.lock whatever --no-optional-locks says, which can fail an
	// agent's own commit in the same checkout; diff.autoRefreshIndex=false
	// stops the write and changes no output.
	// The native parser needs a context marker even for blank lines so it
	// can keep old/new line numbers aligned with the hunk header.
	full := append([]string{"--no-optional-locks", "-c", "diff.autoRefreshIndex=false", "-c", "diff.suppressBlankEmpty=false", "--literal-pathspecs", "-C", dir}, args...)
	// #nosec G204 -- argv contains git operations and literal selectors, never shell code.
	cmd := exec.CommandContext(ctx, "git", full...)
	var out, stderr reviewBuffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	if err != nil {
		return out.Bytes(), out.truncated, newReviewGitError(stderr.String(), err)
	}
	return out.Bytes(), out.truncated, nil
}

// reviewGitError is a git invocation that failed, worded by git's own first
// line of complaint rather than the process's exit status, which the operator
// can do nothing with. The exit error stays reachable through Unwrap.
type reviewGitError struct {
	message string
	err     error
}

func newReviewGitError(stderr string, err error) *reviewGitError {
	line, _, _ := strings.Cut(strings.TrimSpace(stderr), "\n")
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "fatal:"), "error:"))
	if line == "" {
		line = err.Error()
	}
	return &reviewGitError{message: "git: " + line, err: err}
}

func (e *reviewGitError) Error() string { return e.message }
func (e *reviewGitError) Unwrap() error { return e.err }

// ReadChanges compares tracked content (including staged and unstaged changes)
// to base, and lists untracked paths. Only the selected file's patch is loaded.
// Git hooks, external diff drivers, and textconv are never run.
func ReadChanges(ctx context.Context, directory, base, file string) (Changes, error) {
	result := Changes{Files: []ChangedFile{}}
	root, _, err := reviewGit(ctx, directory, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return result, fmt.Errorf("%s is not inside a Git checkout, so it has no changes to review", directory)
		}
		return result, err
	}
	result.Root = strings.TrimSuffix(string(root), "\n")
	if base == "" {
		base = "HEAD"
	}
	commit, _, err := reviewGit(ctx, result.Root, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return result, fmt.Errorf("comparison base %q is not a commit: %w", base, err)
	}
	result.Base = strings.TrimSpace(string(commit))
	// Reviews must show changed submodules even when Git's display settings
	// hide them. Use the same policy for the file list and selected patch.
	flags := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--ignore-submodules=none"}
	stats, truncated, err := reviewGit(ctx, result.Root, append(flags, "--numstat", "-z", result.Base, "--")...)
	if err != nil || truncated {
		if err == nil {
			err = errors.New("changed-file list is too large to display")
		}
		return result, err
	}
	for _, record := range bytes.Split(stats, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		parts := strings.SplitN(string(record), "\t", 3)
		if len(parts) != 3 {
			return result, errors.New("git returned an unreadable change record")
		}
		f := ChangedFile{Path: parts[2], Binary: parts[0] == "-"}
		if !f.Binary {
			f.Added, _ = strconv.Atoi(parts[0])
			f.Deleted, _ = strconv.Atoi(parts[1])
		}
		result.Files = append(result.Files, f)
	}
	untracked, truncated, err := reviewGit(ctx, result.Root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil || truncated {
		if err == nil {
			err = errors.New("untracked-file list is too large to display")
		}
		return result, err
	}
	for _, path := range bytes.Split(untracked, []byte{0}) {
		if len(path) > 0 {
			result.Files = append(result.Files, ChangedFile{Path: string(path), Untracked: true})
		}
	}
	if file == "" {
		return result, nil
	}
	var selected *ChangedFile
	for i := range result.Files {
		if result.Files[i].Path == file {
			selected = &result.Files[i]
			break
		}
	}
	if selected == nil {
		return result, errors.New("the selected file no longer has changes; refresh the file list")
	}
	var patch []byte
	if selected.Untracked {
		path := filepath.Join(result.Root, file)
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return result, statErr
		}
		if !info.Mode().IsRegular() {
			result.Patch = "Untracked symbolic link or special file; open the checkout to inspect it."
			return result, nil
		}
		patch, truncated, err = reviewGit(ctx, result.Root, append(flags, "--no-index", "--", os.DevNull, path)...)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			err = nil // git diff --no-index reports differences with status 1.
		}
	} else {
		patch, truncated, err = reviewGit(ctx, result.Root, append(flags, result.Base, "--", file)...)
	}
	if err != nil {
		return result, err
	}
	result.Patch, result.Truncated = string(patch), truncated
	return result, nil
}
