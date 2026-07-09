package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sglavoie/agentmux/internal/config"
)

const maxConcurrentGitProjectMetadataProbes = 4

// resolveProjectTitle is the physical-path-only title wrapper used by tests
// and legacy compatibility coverage. Production refresh paths use
// newProjectResolverWithGit so linked worktrees can match configured projects.
func resolveProjectTitle(cwd string, projects []config.Session) string {
	return matchProject(cwd, projects).title
}

type projectMatch struct {
	title   string
	profile string
}

type projectResolver struct {
	projects []resolvedProject
}

type resolvedProject struct {
	title   string
	profile string
	dir     string
	git     resolvedGitProject
}

func newProjectResolver(projects []config.Session) projectResolver {
	return newProjectResolverWithGit(projects, nil)
}

// gitProjectMetadataProvider keeps Git discovery injectable so resolver tests
// do not shell out and production discovery can be added behind the same model.
type gitProjectMetadataProvider interface {
	GitMetadata(dir string) (gitProjectMetadata, error)
}

type gitProjectMetadata struct {
	worktreeRoot  string
	worktreeRoots []string
}

type resolvedGitProject struct {
	worktreeRoot  string
	worktreeRoots []string
	relativeDir   string
}

func newProjectResolverWithGit(projects []config.Session, gitProvider gitProjectMetadataProvider) projectResolver {
	resolver := projectResolver{
		projects: make([]resolvedProject, 0, len(projects)),
	}
	for _, p := range projects {
		if p.Directory == "" {
			continue
		}
		dir := absPath(p.Directory)
		if dir == "" {
			continue
		}
		project := resolvedProject{
			title:   p.Title,
			profile: p.EffectiveProfile(),
			dir:     dir,
		}
		resolver.projects = append(resolver.projects, project)
	}
	resolveGitProjects(resolver.projects, gitProvider)
	return resolver
}

func resolveGitProjects(projects []resolvedProject, provider gitProjectMetadataProvider) {
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

func resolveGitProject(dir string, provider gitProjectMetadataProvider) (resolvedGitProject, bool) {
	meta, err := provider.GitMetadata(dir)
	if err != nil {
		return resolvedGitProject{}, false
	}
	root := absPath(meta.worktreeRoot)
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
		candidate = absPath(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		normalized = append(normalized, candidate)
	}
	return normalized
}

// matchProject maps a session's working directory to a configured project.
func matchProject(cwd string, projects []config.Session) projectMatch {
	return newProjectResolver(projects).resolve(cwd)
}

// resolve maps a session's working directory to a configured project.
// An exact directory match wins outright. Otherwise the project whose
// directory is a path-boundary ancestor of the cwd with the longest
// normalized path is chosen. When no configured directory matches, Git-aware
// resolvers map configured projects into registered worktrees before falling
// back to the cwd base name.
func (r projectResolver) resolve(cwd string) projectMatch {
	abs := absPath(cwd)
	if abs == "" {
		return projectMatch{}
	}

	var (
		best    projectMatch
		bestLen = -1
	)
	for _, p := range r.projects {
		if p.dir == abs {
			// Exact match is the most specific possible result.
			return projectMatch{title: p.title, profile: p.profile}
		}
		if !pathHasAncestor(abs, p.dir) {
			continue
		}
		if len(p.dir) > bestLen {
			bestLen = len(p.dir)
			best = projectMatch{title: p.title, profile: p.profile}
		}
	}
	if bestLen >= 0 {
		return best
	}
	if best, ok := r.gitEquivalentMatch(abs); ok {
		return best
	}
	return projectMatch{title: filepath.Base(abs)}
}

func (r projectResolver) gitEquivalentMatch(abs string) (projectMatch, bool) {
	var (
		best    projectMatch
		bestLen = -1
	)
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
				best = projectMatch{title: p.title, profile: p.profile}
			}
		}
	}
	if bestLen < 0 {
		return projectMatch{}, false
	}
	return best, true
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

// pathHasAncestor reports whether ancestor is a proper directory prefix of
// path on a path-segment boundary. Both arguments must already be normalized
// (absolute, symlink-evaluated, cleaned). The boundary check prevents
// "/foo/bar" from matching "/foo/bar-baz".
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

// absPath expands `~` and converts to an absolute, symlink-evaluated path.
// All steps are best-effort: any failure returns the input cleaned with
// filepath.Clean so callers always get a comparable string.
func absPath(p string) string {
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
