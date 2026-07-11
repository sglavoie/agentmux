// Package worktree creates and tracks explicitly requested isolated Git
// worktrees. It deliberately owns Git subprocesses and durable metadata so
// callers can make a single create/launch/cleanup decision without reaching
// into a repository's .git directory.
package worktree

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/workspace"
)

const (
	gitProbeTimeout            = 2 * time.Second
	gitCommandTimeout          = 30 * time.Second
	managedStatusActive        = "active"
	managedStatusCleanupFailed = "cleanup_failed"
)

var (
	// ErrDirtySource reports the intentional safety refusal made before any
	// branch or worktree is created. Git worktrees start from committed state;
	// silently accepting a dirty source would make the isolated fork omit work.
	ErrDirtySource = errors.New("worktree: source working tree has uncommitted changes")
	ErrNoWorkspace = errors.New("worktree: source directory is not in a configured workspace")
)

// Request identifies the indexed conversation context that will receive an
// isolated checkout. SourceCWD must be in a configured Git workspace.
type Request struct {
	Config     *config.Config
	SourceCWD  string
	SessionID  string
	AllowDirty bool
}

// Result is the durable managed context created for an isolated fork.
type Result struct {
	ID                string
	WorkspaceID       string
	WorkspaceName     string
	WorktreeContextID string
	Path              string
	Branch            string
	BaseRef           string
	SourceRoot        string
}

// Runner is the narrow action-facing worktree lifecycle contract. Removal is
// intentionally named RemoveUnused: callers may invoke it only after setup
// or tab launch has failed before a harness starts.
type Runner interface {
	Create(context.Context, Request) (Result, error)
	RemoveUnused(context.Context, Result) error
}

// Workflow owns the default durable store and bounded Git command boundary.
// Its private seams make integration behavior testable without a user's
// default Agent Mux directory.
type Workflow struct {
	openStore func(context.Context) (*datastore.Store, error)
	now       func() time.Time
	newID     func(string) (string, error)
}

// NewWorkflow constructs the production worktree service.
func NewWorkflow() *Workflow {
	return &Workflow{openStore: datastore.Open, now: time.Now, newID: generatedID}
}

// Create refuses a dirty source unless explicitly overridden, adds a new Git
// branch/worktree, and records both its generic context and managed metadata
// in one datastore transaction. Any failure after Git created the worktree is
// compensated before the error returns.
func (w *Workflow) Create(ctx context.Context, request Request) (result Result, retErr error) {
	if w == nil {
		w = NewWorkflow()
	}
	if request.Config == nil {
		return Result{}, errors.New("worktree: configuration is required")
	}
	if strings.TrimSpace(request.SourceCWD) == "" {
		return Result{}, errors.New("worktree: source working directory is required")
	}
	if strings.TrimSpace(request.SessionID) == "" {
		return Result{}, errors.New("worktree: conversation session ID is required")
	}

	store, err := w.openStore(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("worktree: opening datastore: %w", err)
	}
	defer func() { _ = store.Close() }()

	match, found, err := workspace.New(store).MatchCWD(ctx, request.Config, request.SourceCWD)
	if err != nil {
		return Result{}, fmt.Errorf("worktree: resolving source workspace: %w", err)
	}
	if !found {
		return Result{}, fmt.Errorf("%w: %s", ErrNoWorkspace, request.SourceCWD)
	}

	sourceRoot, err := gitLine(ctx, request.SourceCWD, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return Result{}, fmt.Errorf("worktree: source is not a usable Git checkout: %w", err)
	}
	sourceRoot, err = canonicalDirectory(sourceRoot)
	if err != nil {
		return Result{}, fmt.Errorf("worktree: resolving source checkout: %w", err)
	}
	dirty, err := gitDirty(ctx, sourceRoot)
	if err != nil {
		return Result{}, err
	}
	if dirty && !request.AllowDirty {
		return Result{}, fmt.Errorf("%w; rerun with --allow-dirty only if you accept that uncommitted changes will not be copied", ErrDirtySource)
	}

	parent, err := managedParent(store, match.Workspace.ID)
	if err != nil {
		return Result{}, err
	}
	branch, destination, err := w.nextName(ctx, sourceRoot, parent, match.Workspace.ID, request.SessionID)
	if err != nil {
		return Result{}, err
	}
	if _, err := runGit(ctx, sourceRoot, gitCommandTimeout, "worktree", "add", "-b", branch, destination, "HEAD"); err != nil {
		return Result{}, fmt.Errorf("worktree: creating isolated checkout: %w", err)
	}

	result = Result{
		WorkspaceID:   match.Workspace.ID,
		WorkspaceName: match.Workspace.DisplayName,
		Path:          destination,
		Branch:        branch,
		BaseRef:       "HEAD",
		SourceRoot:    sourceRoot,
	}
	createdResult := result
	created := true
	defer func() {
		if retErr == nil || !created {
			return
		}
		if cleanupErr := removeGitWorktree(context.Background(), createdResult); cleanupErr != nil {
			retErr = fmt.Errorf("%w; additionally removing untracked worktree failed: %v", retErr, cleanupErr)
		}
	}()

	commonDir, err := gitLine(ctx, destination, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Result{}, fmt.Errorf("worktree: inspecting created checkout: %w", err)
	}
	commonDir, err = canonicalPath(commonDir)
	if err != nil {
		return Result{}, fmt.Errorf("worktree: resolving Git common directory: %w", err)
	}
	contextID, err := w.newID("wt")
	if err != nil {
		return Result{}, err
	}
	managedID, err := w.newID("mwt")
	if err != nil {
		return Result{}, err
	}
	now := w.now().UTC().UnixNano()
	if err := store.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO worktree_contexts (id, workspace_id, canonical_path, git_common_dir, branch, is_primary, created_at_ns, updated_at_ns)
			VALUES (?, ?, ?, ?, ?, 0, ?, ?)
		`, contextID, result.WorkspaceID, result.Path, commonDir, result.Branch, now, now); err != nil {
			return fmt.Errorf("record worktree context: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO managed_worktrees (id, workspace_id, worktree_context_id, path, branch, base_ref, status, created_at_ns, updated_at_ns)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, managedID, result.WorkspaceID, contextID, result.Path, result.Branch, result.BaseRef, managedStatusActive, now, now); err != nil {
			return fmt.Errorf("record managed worktree: %w", err)
		}
		return nil
	}); err != nil {
		return Result{}, fmt.Errorf("worktree: recording created checkout: %w", err)
	}
	result.ID = managedID
	result.WorktreeContextID = contextID
	created = false
	return result, nil
}

// RemoveUnused removes a managed checkout only after a caller knows no
// harness was started. A failed cleanup remains tracked as cleanup_failed so
// it can never disappear from durable state while it still exists on disk.
func (w *Workflow) RemoveUnused(ctx context.Context, result Result) error {
	if w == nil {
		w = NewWorkflow()
	}
	if strings.TrimSpace(result.ID) == "" {
		return errors.New("worktree: managed worktree ID is required for cleanup")
	}
	store, err := w.openStore(ctx)
	if err != nil {
		return fmt.Errorf("worktree: opening datastore for cleanup: %w", err)
	}
	defer func() { _ = store.Close() }()

	var path, branch, contextID string
	if err := store.DB().QueryRowContext(ctx, `
		SELECT path, COALESCE(branch, ''), COALESCE(worktree_context_id, '')
		FROM managed_worktrees WHERE id = ?
	`, result.ID).Scan(&path, &branch, &contextID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("worktree: managed worktree %q is not tracked", result.ID)
		}
		return fmt.Errorf("worktree: loading managed checkout for cleanup: %w", err)
	}
	if result.SourceRoot == "" {
		return errors.New("worktree: cleanup requires the original source checkout")
	}
	cleanup := Result{ID: result.ID, Path: path, Branch: branch, SourceRoot: result.SourceRoot}
	if err := removeGitWorktree(ctx, cleanup); err != nil {
		_ = markCleanupFailed(ctx, store, result.ID)
		return err
	}
	if err := store.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM managed_worktrees WHERE id = ?`, result.ID); err != nil {
			return err
		}
		if contextID != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM worktree_contexts WHERE id = ?`, contextID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("worktree: removing cleanup metadata: %w", err)
	}
	return nil
}

func (w *Workflow) nextName(ctx context.Context, sourceRoot, parent, workspaceID, sessionID string) (string, string, error) {
	timestamp := w.now().UTC().Format("20060102T150405Z")
	workspaceSlug := slug(workspaceID, "workspace")
	sessionSlug := shortSlug(sessionID)
	for attempt := 1; attempt < 10_000; attempt++ {
		suffix := ""
		if attempt > 1 {
			suffix = fmt.Sprintf("-%d", attempt)
		}
		leaf := sessionSlug + "-" + timestamp + suffix
		branch := "amx/" + workspaceSlug + "/" + leaf
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

func managedParent(store *datastore.Store, workspaceID string) (string, error) {
	if store == nil || strings.TrimSpace(store.Path()) == "" {
		return "", errors.New("worktree: datastore path is required")
	}
	parent := filepath.Join(filepath.Dir(store.Path()), "worktrees", workspaceDirectoryName(workspaceID))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("worktree: creating managed directory %s: %w", parent, err)
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return "", fmt.Errorf("worktree: inspecting managed directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("worktree: managed directory %s is not a directory", parent)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return "", fmt.Errorf("worktree: securing managed directory: %w", err)
	}
	return filepath.Clean(parent), nil
}

func gitDirty(ctx context.Context, directory string) (bool, error) {
	out, err := runGit(ctx, directory, gitProbeTimeout, "status", "--porcelain=v1", "-z")
	if err != nil {
		return false, fmt.Errorf("worktree: checking source status: %w", err)
	}
	return len(out) > 0, nil
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
	if _, err := runGit(ctx, result.SourceRoot, gitCommandTimeout, "worktree", "remove", "--force", result.Path); err != nil {
		return fmt.Errorf("worktree: removing unused checkout %s: %w", result.Path, err)
	}
	if branch := strings.TrimSpace(result.Branch); branch != "" {
		if _, err := runGit(ctx, result.SourceRoot, gitCommandTimeout, "branch", "-D", branch); err != nil {
			return fmt.Errorf("worktree: removing unused branch %s: %w", branch, err)
		}
	}
	return nil
}

func markCleanupFailed(ctx context.Context, store *datastore.Store, id string) error {
	return store.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE managed_worktrees SET status = ?, updated_at_ns = ? WHERE id = ?`, managedStatusCleanupFailed, time.Now().UTC().UnixNano(), id)
		return err
	})
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

func generatedID(prefix string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("worktree: generate %s ID: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(buf), nil
}

func shortSlug(value string) string {
	value = slug(value, "session")
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func workspaceDirectoryName(value string) string {
	value = strings.TrimSpace(value)
	if value != "" && filepath.Base(value) == value && value != "." && value != ".." && !strings.ContainsRune(value, os.PathSeparator) {
		return value
	}
	return slug(value, "workspace")
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
