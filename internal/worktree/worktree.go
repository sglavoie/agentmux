// Package worktree creates explicitly requested isolated Git worktrees. Git
// and the filesystem are authoritative: successful worktrees are ordinary Git
// worktrees, while callers retain the returned result for failed-launch cleanup.
package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/projectresolve"
)

const (
	gitProbeTimeout   = 2 * time.Second
	gitCommandTimeout = 30 * time.Second
)

var (
	// ErrDirtySource reports the intentional safety refusal made before any
	// branch or worktree is created. Git worktrees start from committed state;
	// silently accepting a dirty source would make the isolated fork omit work.
	ErrDirtySource = errors.New("worktree: source working tree has uncommitted changes")
	// ErrNotGitCheckout reports a source directory Git cannot resolve to a
	// working-tree top level. Configuration is intentionally irrelevant.
	ErrNotGitCheckout = errors.New("worktree: source is not a usable Git checkout")
)

// Request identifies the conversation context that will receive an isolated
// checkout. Config and ProjectLabel are optional naming hints; SourceCWD and
// Git state remain authoritative.
type Request struct {
	Config       *config.Config
	ProjectLabel string
	SourceCWD    string
	SessionID    string
	Profile      string
	Harness      string
	AllowDirty   bool
}

// Result contains all state required to launch in or remove an isolated fork.
type Result struct {
	ProjectLabel string
	Path         string
	Branch       string
	BaseRef      string
	SourceRoot   string
	ID           string
	BaseCommit   string
	SourceBranch string
	CreatedAt    time.Time
	MetadataPath string
}

// Runner is the narrow action-facing worktree lifecycle contract. Removal is
// intentionally named RemoveUnused: callers may invoke it only after setup
// or tab launch has failed before a harness starts.
type Runner interface {
	Create(context.Context, Request) (Result, error)
	RemoveUnused(context.Context, Result) error
}

// Workflow owns the destination-directory and bounded Git command boundaries.
// Its private seams keep integration tests out of the user's Agent Mux tree.
type Workflow struct {
	directory     func() (string, error)
	now           func() time.Time
	newID         func() (string, error)
	writeMetadata func(string, Metadata) error
	rollback      func(context.Context, Result) error
}

// NewWorkflow constructs the production worktree service.
func NewWorkflow() *Workflow {
	return &Workflow{directory: defaultDirectory, now: time.Now, newID: newWorktreeID, writeMetadata: WriteMetadata}
}

// Create refuses a dirty source unless explicitly overridden, adds a new Git
// branch/worktree, and returns the Git-owned checkout. No inventory is opened
// or persisted.
func (w *Workflow) Create(ctx context.Context, request Request) (Result, error) {
	if w == nil {
		w = NewWorkflow()
	}
	if strings.TrimSpace(request.SourceCWD) == "" {
		return Result{}, errors.New("worktree: source working directory is required")
	}
	if strings.TrimSpace(request.SessionID) == "" {
		return Result{}, errors.New("worktree: conversation session ID is required")
	}

	sourceRoot, err := gitLine(ctx, request.SourceCWD, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrNotGitCheckout, err)
	}
	sourceRoot, err = canonicalDirectory(sourceRoot)
	if err != nil {
		return Result{}, fmt.Errorf("%w: resolving source checkout: %v", ErrNotGitCheckout, err)
	}
	baseCommit, sourceBranch, gitCommonDir, err := sourceGitIdentity(ctx, sourceRoot)
	if err != nil {
		return Result{}, err
	}
	dirty, err := gitDirty(ctx, sourceRoot)
	if err != nil {
		return Result{}, err
	}
	if dirty && !request.AllowDirty {
		return Result{}, fmt.Errorf("%w; rerun with --allow-dirty only if you accept that uncommitted changes will not be copied", ErrDirtySource)
	}

	projectLabel := resolveProjectLabel(request, sourceRoot)
	rootDirectory := w.directory
	if rootDirectory == nil {
		rootDirectory = defaultDirectory
	}
	root, err := rootDirectory()
	if err != nil {
		return Result{}, err
	}
	root, err = managedRoot(root)
	if err != nil {
		return Result{}, err
	}
	now := w.now
	if now == nil {
		now = time.Now
	}
	createdAt := now().UTC()
	newID := w.newID
	if newID == nil {
		newID = newWorktreeID
	}
	id, err := newID()
	if err != nil {
		return Result{}, err
	}

	var result Result
	err = withMutationLock(ctx, root, func() error {
		parent, err := worktreeParent(root, projectLabel)
		if err != nil {
			return err
		}
		branch, destination, err := w.nextName(ctx, sourceRoot, parent, projectLabel, request.SessionID, createdAt)
		if err != nil {
			return err
		}
		if _, err := runGit(ctx, sourceRoot, gitCommandTimeout, "worktree", "add", "-b", branch, destination, baseCommit); err != nil {
			return fmt.Errorf("worktree: creating isolated checkout: %w", err)
		}
		canonicalDestination, err := canonicalDirectory(destination)
		if err != nil {
			primary := fmt.Errorf("worktree: resolving created checkout: %w", err)
			rollbackResult := Result{Path: destination, Branch: branch, SourceRoot: sourceRoot, MetadataPath: SidecarPath(destination)}
			if rollbackErr := w.rollbackCreation(context.Background(), rollbackResult); rollbackErr != nil {
				return errors.Join(primary, fmt.Errorf("worktree: rolling back failed creation: %w", rollbackErr))
			}
			return primary
		}
		result = Result{
			ProjectLabel: projectLabel, Path: canonicalDestination, Branch: branch,
			BaseRef: "HEAD", SourceRoot: sourceRoot, ID: id, BaseCommit: baseCommit,
			SourceBranch: sourceBranch, CreatedAt: createdAt, MetadataPath: SidecarPath(canonicalDestination),
		}
		metadata := Metadata{
			SchemaVersion: MetadataSchemaVersion, ID: id, ProjectLabel: projectLabel,
			Path: canonicalDestination, Branch: branch, GitCommonDir: gitCommonDir,
			SourceRoot: sourceRoot, SourceBranch: sourceBranch, BaseCommit: baseCommit,
			Profile: request.Profile, Harness: request.Harness,
			SourceConversationID: request.SessionID, CreatedAt: createdAt,
		}
		writeMetadata := w.writeMetadata
		if writeMetadata == nil {
			writeMetadata = WriteMetadata
		}
		if err := writeMetadata(result.MetadataPath, metadata); err != nil {
			primary := fmt.Errorf("worktree: persisting metadata: %w", err)
			// Compensation must still run if the caller canceled while the
			// sidecar was being persisted; each Git command remains bounded.
			if rollbackErr := w.rollbackCreation(context.Background(), result); rollbackErr != nil {
				return errors.Join(primary, fmt.Errorf("worktree: rolling back failed creation: %w", rollbackErr))
			}
			return primary
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// RemoveUnused removes a checkout only after a caller knows no harness was
// started. The result itself is the complete cleanup authority.
func (w *Workflow) RemoveUnused(ctx context.Context, result Result) error {
	return removeGitWorktree(ctx, result)
}

func (w *Workflow) rollbackCreation(ctx context.Context, result Result) error {
	if w.rollback != nil {
		return w.rollback(ctx, result)
	}
	return removeGitWorktree(ctx, result)
}

func (w *Workflow) nextName(ctx context.Context, sourceRoot, parent, projectLabel, sessionID string, createdAt time.Time) (string, string, error) {
	timestamp := createdAt.UTC().Format("20060102T150405Z")
	projectSlug := slug(projectLabel, "project")
	sessionSlug := shortSlug(sessionID)
	for attempt := 1; attempt < 10_000; attempt++ {
		suffix := ""
		if attempt > 1 {
			suffix = fmt.Sprintf("-%d", attempt)
		}
		leaf := sessionSlug + "-" + timestamp + suffix
		branch := "amx/" + projectSlug + "/" + leaf
		path := filepath.Join(parent, leaf)
		occupied, err := pathOccupied(path)
		if err != nil {
			return "", "", err
		}
		branchExists, err := gitBranchExists(ctx, sourceRoot, branch)
		if err != nil {
			return "", "", err
		}
		if !occupied && !branchExists {
			return branch, path, nil
		}
	}
	return "", "", errors.New("worktree: exhausted deterministic branch/path collision attempts")
}

func defaultDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		if err == nil {
			err = errors.New("home directory is empty")
		}
		return "", fmt.Errorf("worktree: resolving home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "agent-mux", "worktrees"), nil
}

func managedRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("worktree: destination root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("worktree: resolving destination root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("worktree: creating destination root %s: %w", root, err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("worktree: inspecting destination root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("worktree: destination root %s is not a directory", root)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", fmt.Errorf("worktree: securing destination root: %w", err)
	}
	return filepath.Clean(root), nil
}

func worktreeParent(root, projectLabel string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("worktree: destination root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("worktree: resolving destination root: %w", err)
	}
	parent := filepath.Join(root, slug(projectLabel, "project"))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("worktree: creating destination directory %s: %w", parent, err)
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return "", fmt.Errorf("worktree: inspecting destination directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("worktree: destination directory %s is not a directory", parent)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return "", fmt.Errorf("worktree: securing destination directory: %w", err)
	}
	return filepath.Clean(parent), nil
}

func resolveProjectLabel(request Request, sourceRoot string) string {
	if label := strings.TrimSpace(request.ProjectLabel); label != "" {
		return label
	}
	if request.Config != nil {
		projects := request.Config.Projects
		if len(projects) == 0 {
			projects = request.Config.Sessions
		}
		if match, ok := projectresolve.NewWithGit(projects).Resolve(request.SourceCWD); ok {
			if label := strings.TrimSpace(match.Title); label != "" {
				return label
			}
		}
	}
	return filepath.Base(sourceRoot)
}

func gitDirty(ctx context.Context, directory string) (bool, error) {
	out, err := runGit(ctx, directory, gitProbeTimeout, "status", "--porcelain=v1", "-z")
	if err != nil {
		return false, fmt.Errorf("worktree: checking source status: %w", err)
	}
	return len(out) > 0, nil
}

func sourceGitIdentity(ctx context.Context, sourceRoot string) (baseCommit, sourceBranch, gitCommonDir string, err error) {
	baseCommit, err = gitLine(ctx, sourceRoot, gitProbeTimeout, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("worktree: resolving source base commit: %w", err)
	}
	branchOut, err := runGit(ctx, sourceRoot, gitProbeTimeout, "branch", "--show-current")
	if err != nil {
		return "", "", "", fmt.Errorf("worktree: resolving source branch: %w", err)
	}
	sourceBranch = strings.TrimSpace(string(branchOut))
	gitCommonDir, err = gitLine(ctx, sourceRoot, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", "", fmt.Errorf("worktree: resolving Git common directory: %w", err)
	}
	gitCommonDir, err = canonicalDirectory(gitCommonDir)
	if err != nil {
		return "", "", "", fmt.Errorf("worktree: canonicalizing Git common directory: %w", err)
	}
	return baseCommit, sourceBranch, gitCommonDir, nil
}

func gitBranchExists(ctx context.Context, directory, branch string) (bool, error) {
	commandCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", "-C", directory, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return false, fmt.Errorf("worktree: checking branch collision timed out after %s", gitProbeTimeout)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("worktree: checking branch collision: %w", err)
}

func removeGitWorktree(ctx context.Context, result Result) error {
	if strings.TrimSpace(result.Path) == "" || strings.TrimSpace(result.SourceRoot) == "" {
		return errors.New("worktree: cleanup is missing checkout path or source root")
	}
	var cleanupErrors []error
	if _, err := runGit(ctx, result.SourceRoot, gitCommandTimeout, "worktree", "remove", "--force", result.Path); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("worktree: removing unused checkout %s: %w", result.Path, err))
	}
	if branch := strings.TrimSpace(result.Branch); branch != "" {
		if _, err := runGit(ctx, result.SourceRoot, gitCommandTimeout, "branch", "-D", branch); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("worktree: removing unused branch %s: %w", branch, err))
		}
	}
	metadataPath := strings.TrimSpace(result.MetadataPath)
	if metadataPath == "" {
		metadataPath = SidecarPath(result.Path)
	}
	if err := os.Remove(metadataPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("worktree: removing metadata %s: %w", metadataPath, err))
	}
	return errors.Join(cleanupErrors...)
}

func runGit(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", append([]string{"-C", directory}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("git %s timed out after %s", args[0], timeout)
		}
		if text := strings.TrimSpace(stderr.String()); text != "" {
			return nil, fmt.Errorf("git %s failed: %w: %s", args[0], err, text)
		}
		return nil, fmt.Errorf("git %s failed: %w", args[0], err)
	}
	return stdout.Bytes(), nil
}

func gitLine(ctx context.Context, directory string, timeout time.Duration, args ...string) (string, error) {
	out, err := runGit(ctx, directory, timeout, args...)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.Contains(line, "\n") {
		return "", errors.New("unexpected Git output")
	}
	return line, nil
}

func pathOccupied(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("worktree: inspecting destination %s: %w", path, err)
	}
	return true, nil
}

func canonicalDirectory(path string) (string, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", canonical)
	}
	return canonical, nil
}

func canonicalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func shortSlug(value string) string {
	value = slug(value, "session")
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func slug(value, fallback string) string {
	var out []rune
	previousDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out = append(out, r)
			previousDash = false
			continue
		}
		if !previousDash {
			out = append(out, '-')
			previousDash = true
		}
	}
	result := strings.Trim(string(out), "-.")
	if result == "" {
		return fallback
	}
	return result
}
