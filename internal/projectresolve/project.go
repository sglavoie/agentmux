// Package projectresolve maps working directories back to configured projects.
//
// The resolver understands plain ancestor matches and linked Git worktrees so
// callers can consistently group sessions launched from a repository's main
// checkout or any registered worktree.
package projectresolve

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
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

const (
	maxConcurrentGitProjectMetadataProbes = 4
	gitMetadataCommandTimeout             = 2 * time.Second
)

// Match is a configured project matched to a working directory.
type Match struct {
	Title     string
	Profile   string
	Directory string
	Project   config.Session
}

// Resolver holds normalized configured projects and their optional Git
// worktree metadata.
type Resolver struct {
	projects []resolvedProject
}

type resolvedProject struct {
	title   string
	profile string
	dir     string
	project config.Session
	git     resolvedGitProject
}

type resolvedGitProject struct {
	worktreeRoot  string
	worktreeRoots []string
	relativeDir   string
}

type gitMetadataProvider interface {
	GitMetadata(dir string) (gitMetadata, error)
}

type gitMetadata struct {
	worktreeRoot  string
	worktreeRoots []string
}

// New returns a resolver that only considers configured directory paths.
func New(projects []config.Session) Resolver {
	return newWithGitProvider(projects, nil)
}

// NewWithGit returns a resolver that also maps configured projects into every
// registered linked worktree for their repository.
func NewWithGit(projects []config.Session) Resolver {
	return newWithGitProvider(projects, realGitMetadataProvider{timeout: gitMetadataCommandTimeout})
}

func newWithGitProvider(projects []config.Session, gitProvider gitMetadataProvider) Resolver {
	resolver := Resolver{
		projects: make([]resolvedProject, 0, len(projects)),
	}
	for _, p := range projects {
		if p.Directory == "" {
			continue
		}
		dir := AbsPath(p.Directory)
		if dir == "" {
			continue
		}
		resolver.projects = append(resolver.projects, resolvedProject{
			title:   p.Title,
			profile: p.EffectiveProfile(),
			dir:     dir,
			project: p,
		})
	}
	resolveGitProjects(resolver.projects, gitProvider)
	return resolver
}

func resolveGitProjects(projects []resolvedProject, provider gitMetadataProvider) {
	if provider == nil || len(projects) == 0 {
		return
	}

	workerCount := maxConcurrentGitProjectMetadataProbes
	if len(projects) < workerCount {
		workerCount = len(projects)
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if gitProject, ok := resolveGitProject(projects[idx].dir, provider); ok {
					projects[idx].git = gitProject
				}
			}
		}()
	}
	for idx := range projects {
		jobs <- idx
	}
	close(jobs)
	wg.Wait()
}

func resolveGitProject(dir string, provider gitMetadataProvider) (resolvedGitProject, bool) {
	meta, err := provider.GitMetadata(dir)
	if err != nil {
		return resolvedGitProject{}, false
	}
	root := AbsPath(meta.worktreeRoot)
	if root == "" {
		return resolvedGitProject{}, false
	}
	relative, ok := relativePathWithin(dir, root)
	if !ok {
		return resolvedGitProject{}, false
	}
	return resolvedGitProject{
		worktreeRoot:  root,
		worktreeRoots: normalizeWorktreeRoots(root, meta.worktreeRoots),
		relativeDir:   relative,
	}, true
}

func normalizeWorktreeRoots(root string, roots []string) []string {
	normalized := make([]string, 0, len(roots)+1)
	seen := map[string]bool{}
	for _, candidate := range append([]string{root}, roots...) {
		candidate = AbsPath(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		normalized = append(normalized, candidate)
	}
	return normalized
}

// Resolve returns the most specific configured project match for cwd.
func (r Resolver) Resolve(cwd string) (Match, bool) {
	matches := r.Matches(cwd)
	if len(matches) == 0 {
		return Match{}, false
	}
	return matches[0], true
}

// ResolveOrFallback returns the most specific configured project match for
// cwd, or a fallback pseudo-project using the cwd basename when none matches.
func (r Resolver) ResolveOrFallback(cwd string) Match {
	if match, ok := r.Resolve(cwd); ok {
		return match
	}
	abs := AbsPath(cwd)
	if abs == "" {
		return Match{}
	}
	return Match{Title: filepath.Base(abs)}
}

// Matches returns every configured project tied for the most specific match.
// Callers that need ambiguity detection should use this instead of Resolve.
func (r Resolver) Matches(cwd string) []Match {
	abs := AbsPath(cwd)
	if abs == "" {
		return nil
	}
	if matches := r.physicalMatches(abs); len(matches) > 0 {
		return matches
	}
	return r.gitEquivalentMatches(abs)
}

func (r Resolver) physicalMatches(abs string) []Match {
	bestLen := -1
	var matches []Match
	for _, p := range r.projects {
		if !pathMatchesOrHasAncestor(abs, p.dir) {
			continue
		}
		if len(p.dir) > bestLen {
			bestLen = len(p.dir)
			matches = matches[:0]
		}
		if len(p.dir) == bestLen {
			matches = append(matches, p.match())
		}
	}
	return matches
}

func (r Resolver) gitEquivalentMatches(abs string) []Match {
	bestLen := -1
	var matches []Match
	for _, p := range r.projects {
		if p.git.relativeDir == "" {
			continue
		}
		for _, root := range p.git.worktreeRoots {
			mapped := mappedWorktreeProjectDir(root, p.git.relativeDir)
			if mapped == "" || !pathMatchesOrHasAncestor(abs, mapped) {
				continue
			}
			if len(mapped) > bestLen {
				bestLen = len(mapped)
				matches = matches[:0]
			}
			if len(mapped) == bestLen {
				matches = append(matches, p.match())
			}
		}
	}
	return matches
}

func (p resolvedProject) match() Match {
	return Match{
		Title:     p.title,
		Profile:   p.profile,
		Directory: p.dir,
		Project:   p.project,
	}
}

func mappedWorktreeProjectDir(root, relative string) string {
	if root == "" || relative == "" {
		return ""
	}
	if relative == "." {
		return root
	}
	return filepath.Clean(filepath.Join(root, relative))
}

func relativePathWithin(path, root string) (string, bool) {
	if path == root {
		return ".", true
	}
	if !pathHasAncestor(path, root) {
		return "", false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	relative = filepath.Clean(relative)
	if relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || relative == ".." {
		return "", false
	}
	return relative, true
}

func pathMatchesOrHasAncestor(path, ancestor string) bool {
	return path == ancestor || pathHasAncestor(path, ancestor)
}

func pathHasAncestor(path, ancestor string) bool {
	if ancestor == "" || path == "" {
		return false
	}
	sep := string(os.PathSeparator)
	prefix := ancestor
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	return strings.HasPrefix(path, prefix)
}

// AbsPath expands `~` and converts to an absolute, symlink-evaluated path.
// All steps are best-effort so callers always get a comparable path.
func AbsPath(p string) string {
	if p == "" {
		return ""
	}
	p = config.ExpandTilde(p)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return filepath.Clean(p)
}

type realGitMetadataProvider struct {
	timeout time.Duration
}

func (p realGitMetadataProvider) GitMetadata(dir string) (gitMetadata, error) {
	meta, err := p.gitMetadata(dir)
	if err != nil {
		slog.Debug("projectresolve: git metadata skipped", "dir", dir, "err", err)
		return gitMetadata{}, err
	}
	return meta, nil
}

func (p realGitMetadataProvider) gitMetadata(dir string) (gitMetadata, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return gitMetadata{}, fmt.Errorf("stat project dir: %w", err)
	}
	if !info.IsDir() {
		return gitMetadata{}, errors.New("project path is not a directory")
	}

	revParse, err := p.gitOutput(dir, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return gitMetadata{}, err
	}
	root, err := parseGitRevParseRoot(revParse)
	if err != nil {
		return gitMetadata{}, err
	}

	worktreeList, err := p.gitOutput(dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return gitMetadata{}, err
	}
	worktreeRoots, err := parseGitWorktreeListPorcelainZ(worktreeList)
	if err != nil {
		return gitMetadata{}, err
	}

	return gitMetadata{
		worktreeRoot:  root,
		worktreeRoots: worktreeRoots,
	}, nil
}

func (p realGitMetadataProvider) gitOutput(dir string, args ...string) ([]byte, error) {
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
