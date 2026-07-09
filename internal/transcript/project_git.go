package transcript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const gitMetadataCommandTimeout = 2 * time.Second

type realGitProjectMetadataProvider struct {
	timeout time.Duration
}

func newGitProjectMetadataProvider() gitProjectMetadataProvider {
	return realGitProjectMetadataProvider{timeout: gitMetadataCommandTimeout}
}

func (p realGitProjectMetadataProvider) GitMetadata(dir string) (gitProjectMetadata, error) {
	meta, err := p.gitMetadata(dir)
	if err != nil {
		slog.Debug("transcript.project: git metadata skipped", "dir", dir, "err", err)
		return gitProjectMetadata{}, err
	}
	return meta, nil
}

func (p realGitProjectMetadataProvider) gitMetadata(dir string) (gitProjectMetadata, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return gitProjectMetadata{}, fmt.Errorf("stat project dir: %w", err)
	}
	if !info.IsDir() {
		return gitProjectMetadata{}, errors.New("project path is not a directory")
	}

	revParse, err := p.gitOutput(dir, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return gitProjectMetadata{}, err
	}
	root, err := parseGitRevParseRoot(revParse)
	if err != nil {
		return gitProjectMetadata{}, err
	}

	worktreeList, err := p.gitOutput(dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return gitProjectMetadata{}, err
	}
	worktreeRoots, err := parseGitWorktreeListPorcelainZ(worktreeList)
	if err != nil {
		return gitProjectMetadata{}, err
	}

	return gitProjectMetadata{
		worktreeRoot:  root,
		worktreeRoots: worktreeRoots,
	}, nil
}

func (p realGitProjectMetadataProvider) gitOutput(dir string, args ...string) ([]byte, error) {
	timeout := p.timeout
	if timeout <= 0 {
		timeout = gitMetadataCommandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	gitArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("git %s timed out after %s", args[0], timeout)
		}
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText == "" {
			return nil, fmt.Errorf("git %s failed: %w", args[0], err)
		}
		return nil, fmt.Errorf("git %s failed: %w: %s", args[0], err, stderrText)
	}
	return stdout.Bytes(), nil
}

func parseGitRevParseRoot(out []byte) (string, error) {
	text := strings.TrimRight(string(out), "\n")
	lines := strings.Split(text, "\n")
	if len(lines) != 1 {
		return "", fmt.Errorf("malformed git rev-parse output: got %d lines", len(lines))
	}
	root := strings.TrimSuffix(lines[0], "\r")
	if root == "" {
		return "", errors.New("malformed git rev-parse output: empty path")
	}
	return filepath.Clean(root), nil
}

func parseGitWorktreeListPorcelainZ(out []byte) ([]string, error) {
	fields := strings.Split(string(out), "\x00")
	roots := make([]string, 0, len(fields))
	expectWorktree := true
	for _, field := range fields {
		if field == "" {
			expectWorktree = true
			continue
		}
		if expectWorktree {
			if !strings.HasPrefix(field, "worktree ") {
				return nil, fmt.Errorf("malformed git worktree output: expected worktree record, got %q", field)
			}
			root := strings.TrimPrefix(field, "worktree ")
			if root == "" {
				return nil, errors.New("malformed git worktree output: empty worktree path")
			}
			roots = append(roots, filepath.Clean(root))
			expectWorktree = false
			continue
		}
		if strings.HasPrefix(field, "worktree ") {
			return nil, fmt.Errorf("malformed git worktree output: missing entry separator before %q", field)
		}
	}
	if len(roots) == 0 {
		return nil, errors.New("malformed git worktree output: no worktree records")
	}
	return roots, nil
}
