// Package worktree owns bounded managed Git worktree creation and cleanup: it
// creates explicitly requested isolated worktrees, inventories them, and
// performs the bounded destructive mutations (Inventory.Remove and
// Inventory.Forget) that the amx worktree command family surfaces. Git and the
// filesystem are authoritative: successful worktrees are ordinary Git
// worktrees that survive launch, while callers retain the returned result for
// failed-launch cleanup, and compensation may remove only the worktree the
// failed request created (Workflow.RemoveUnused).
//
// It also answers what a worktree did and brings that work back.
// Inventory.Review compares a worktree with the source branch recorded when
// it was created and predicts how it would land, reading only. Inventory.Land
// is the one mutation here that reaches outside the managed root: it moves
// the source branch, and only by fast-forward. This package never merges,
// rebases, squashes, or resolves a conflict; a source branch that has moved
// on is a refusal.
//
// Its answers about a checkout are AUTHORITATIVE and EXPENSIVE, because they
// gate destructive actions. internal/gitstate also reports a branch and a dirty
// bit, but cheaply, unscoped, and advisory, for painting a dashboard column;
// use this package to decide something and gitstate to paint a column, never
// the reverse.
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
	// ErrUnusedBranchMoved protects commits made after an isolated checkout
	// was created, even if its caller still considers launch unsuccessful.
	ErrUnusedBranchMoved = errors.New("worktree: unused checkout branch moved from its creation commit")
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
		return Result{}, fmt.Errorf("%w: %w", ErrNotGitCheckout, err)
	}
	sourceRoot, err = canonicalDirectory(sourceRoot)
	if err != nil {
		return Result{}, fmt.Errorf("%w: resolving source checkout: %w", ErrNotGitCheckout, err)
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
			rollbackResult := Result{Path: destination, Branch: branch, BaseCommit: baseCommit, SourceRoot: sourceRoot, MetadataPath: SidecarPath(destination)}
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
		// A missing checkout can still have recovery metadata. Reserve its
		// name until that record is explicitly cleaned up or forgotten.
		metadataExists, err := pathOccupied(SidecarPath(path))
		if err != nil {
			return "", "", err
		}
		branchExists, err := gitBranchExists(ctx, sourceRoot, branch)
		if err != nil {
			return "", "", err
		}
		if !occupied && !metadataExists && !branchExists {
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

// statusArgs is every status probe's argument list: porcelain, NUL
// separated, and with --no-optional-locks. Without that option status takes
// the index lock to write back a refreshed index, and the checkout it reads
// is often one an agent is committing in at that moment; the agent's commit
// then fails on index.lock. Safety checks must also see untracked files and
// submodule changes regardless of the user's Git display preferences. extra
// can override the untracked mode for tracked-only or full-path probes.
func statusArgs(extra ...string) []string {
	return append([]string{"--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignore-submodules=none"}, extra...)
}

// diffArgs is a diff that leaves the index alone. A diff against the work
// tree refreshes the index and writes it back under index.lock whatever
// --no-optional-locks says; diff.autoRefreshIndex=false is what stops that,
// and it changes no output, since files whose content matches are skipped
// either way. Like statusArgs, reviews include submodule changes regardless
// of Git's display preferences.
func diffArgs(args ...string) []string {
	return append([]string{"-c", "diff.autoRefreshIndex=false", "diff", "--ignore-submodules=none"}, args...)
}

func gitDirty(ctx context.Context, directory string) (bool, error) {
	out, err := runGit(ctx, directory, gitProbeTimeout, statusArgs()...)
	if err != nil {
		return false, fmt.Errorf("worktree: checking source status: %w", err)
	}
	return len(out) > 0, nil
}

// Git removes ignored files with a worktree even without --force. Keep this
// separate from ordinary dirty status so ignored build output does not block
// creating or landing a worktree.
func hasIgnoredFiles(ctx context.Context, git gitExecutor, directory string) (bool, error) {
	out, err := git(ctx, directory, gitProbeTimeout, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	return len(out) > 0, err
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
	branch, branchRef, err := unusedBranchAtBase(ctx, result)
	if err != nil {
		return err
	}
	if err := revalidateUnusedCheckout(ctx, result, branch); err != nil {
		return err
	}
	// Keep the sidecar until every Git step succeeds. A failed removal or
	// branch deletion must remain discoverable and retryable by stable ID.
	removeArgs := []string{"worktree", "remove"}
	info, err := os.Lstat(result.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// A failed creation can leave a Git registration after its checkout
		// disappeared. Git requires --force to unregister that missing path.
		removeArgs = append(removeArgs, "--force")
	case err != nil:
		return fmt.Errorf("worktree: inspecting unused checkout %s: %w", result.Path, err)
	case !info.IsDir():
		return fmt.Errorf("worktree: unused checkout %s is not a directory", result.Path)
	default:
		operation, err := checkoutOperation(ctx, runGit, result.Path)
		if err != nil {
			return err
		}
		if operation != "" {
			return fmt.Errorf("%w: finish or abort %s in %s first", ErrWorktreeBusy, operation, result.Path)
		}
		status, err := runGit(ctx, result.Path, gitProbeTimeout, statusArgs()...)
		if err != nil {
			return fmt.Errorf("%w: checking unused checkout status: %w", ErrGitUnavailable, err)
		}
		if len(status) != 0 {
			return ErrWorktreeDirty
		}
		if err := checkRemovalIndex(ctx, runGit, result.Path); err != nil {
			return err
		}
		ignored, err := hasIgnoredFiles(ctx, runGit, result.Path)
		if err != nil {
			return fmt.Errorf("worktree: checking ignored files before removing unused checkout %s: %w", result.Path, err)
		}
		if ignored {
			return fmt.Errorf("%w: unused checkout contains ignored files that Git would delete", ErrWorktreeDirty)
		}
	}
	removeArgs = append(removeArgs, result.Path)
	if _, err := runGit(ctx, result.SourceRoot, gitCommandTimeout, removeArgs...); err != nil {
		return fmt.Errorf("worktree: removing unused checkout %s: %w", result.Path, err)
	}
	if branchRef != "" {
		if err := deleteUnusedBranch(ctx, result.SourceRoot, branch, branchRef, result.BaseCommit); err != nil {
			return err
		}
	}
	metadataPath := strings.TrimSpace(result.MetadataPath)
	if metadataPath == "" {
		metadataPath = SidecarPath(result.Path)
	}
	if err := os.Remove(metadataPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("worktree: removing metadata %s: %w", metadataPath, err)
	}
	return nil
}

// Check before removing the checkout so a branch with new commits keeps its
// checkout too. The later conditional delete covers movement after this read.
func unusedBranchAtBase(ctx context.Context, result Result) (string, string, error) {
	if strings.TrimSpace(result.Branch) == "" {
		return "", "", nil
	}
	branch, ref, err := localBranch(result.Branch)
	if err != nil {
		return "", "", fmt.Errorf("worktree: invalid unused branch: %w", err)
	}
	if strings.TrimSpace(result.BaseCommit) == "" {
		return "", "", fmt.Errorf("worktree: cleanup is missing the creation commit for %s", branch)
	}
	current, err := gitLineWith(ctx, runGit, result.SourceRoot, gitProbeTimeout, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", "", fmt.Errorf("worktree: resolving unused branch %s: %w", branch, err)
	}
	if current != result.BaseCommit {
		return "", "", fmt.Errorf("%w: %s is at %s, expected %s", ErrUnusedBranchMoved, branch, current, result.BaseCommit)
	}
	return branch, ref, nil
}

func deleteUnusedBranch(ctx context.Context, sourceRoot, branch, ref, baseCommit string) error {
	porcelain, err := runGit(ctx, sourceRoot, gitProbeTimeout, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return fmt.Errorf("worktree: checking unused branch %s: %w", branch, err)
	}
	records, err := parseWorktreePorcelain(porcelain)
	if err != nil {
		return fmt.Errorf("worktree: checking unused branch %s: %w", branch, err)
	}
	for _, record := range records {
		if record.Branch == branch {
			return fmt.Errorf("worktree: keeping unused branch %s because it is checked out at %s", branch, record.Path)
		}
		operation, err := branchOperation(ctx, runGit, record.Path, ref)
		if err != nil {
			return err
		}
		if operation != "" {
			return fmt.Errorf("worktree: keeping unused branch %s because %s uses it at %s", branch, operation, record.Path)
		}
	}
	// Git compares the current tip with baseCommit under its ref lock. A
	// concurrent commit makes this fail while preserving the branch and sidecar.
	if _, err := runGit(ctx, sourceRoot, gitCommandTimeout, "update-ref", "-d", ref, baseCommit); err != nil {
		return fmt.Errorf("worktree: deleting unused branch %s only at its creation commit: %w", branch, err)
	}
	return nil
}

func runGit(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
	return runGitInput(ctx, directory, timeout, "", args...)
}

func runGitInput(ctx context.Context, directory string, timeout time.Duration, input string, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", append([]string{"-C", directory}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		name := gitSubcommand(args)
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("git %s timed out after %s: %w", name, timeout, context.DeadlineExceeded)
		}
		// What Git printed before failing is returned with the error: for a
		// command whose exit status is an answer (merge-tree's 1 for a
		// conflict), the output is the rest of that answer.
		if text := strings.TrimSpace(stderr.String()); text != "" {
			return stdout.Bytes(), fmt.Errorf("git %s failed: %w: %s", name, err, text)
		}
		return stdout.Bytes(), fmt.Errorf("git %s failed: %w", name, err)
	}
	return stdout.Bytes(), nil
}

// gitSubcommand names a failed command by its subcommand, past any global
// option such as --no-optional-locks or -c and its setting.
func gitSubcommand(args []string) string {
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "-c":
			index++
		case !strings.HasPrefix(args[index], "-"):
			return args[index]
		}
	}
	if len(args) == 0 {
		return ""
	}
	return args[0]
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
