// Package overseerpolicy decides whether the ASL overseer is authorized to act
// on the repositories of one plan.
//
// It is deliberately a second, stricter reader of the same configured projects
// that internal/projectresolve reads for display, and the two differ in the
// three places where display is allowed to guess:
//
//   - Display picks the most specific match; authorization examines EVERY
//     declaration that applies to a directory. A nested project cannot grant
//     what an enclosing declaration withheld, because both are examined and any
//     exclusion denies.
//   - Display resolves a directory through symlinks, `~`, and linked worktrees
//     best-effort, and falls back to a basename when nothing works;
//     authorization has no fallback. An unresolvable path or an unresolvable
//     Git identity is a denial with a reason, never a permissive default.
//   - Display answers per directory; authorization answers per plan. Every
//     repository a plan participates in must be permitted, so one excluded
//     repository denies the whole request rather than shrinking it.
//
// Nothing here reads project or conversation content, and nothing here writes
// configuration: enrolling a project is an operator edit, and this package only
// reports what the operator already wrote.
package overseerpolicy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/projectresolve"
)

// Reason names why a decision came out the way it did. The values are stable
// diagnostic vocabulary: an operator asking "why was my plan refused" gets one
// of these plus the declaration or path that produced it.
type Reason string

const (
	// ReasonAllowed is the only permitting reason.
	ReasonAllowed Reason = "allowed"
	// ReasonNoRepositories is a request that named no repository at all. A plan
	// with nothing to authorize is refused rather than trivially permitted.
	ReasonNoRepositories Reason = "no_repositories"
	// ReasonPathUnresolved is a directory that does not exist, is not a
	// directory, or sits behind a dangling symlink.
	ReasonPathUnresolved Reason = "path_unresolved"
	// ReasonIdentityUnresolved is a directory whose Git identity could not be
	// established. It is refused because an unresolved identity cannot be shown
	// to be outside an excluded repository.
	ReasonIdentityUnresolved Reason = "identity_unresolved"
	// ReasonUndeclared is a resolved directory that no configured project
	// declaration applies to.
	ReasonUndeclared Reason = "undeclared"
	// ReasonUnenrolled is an applicable declaration that never stated an
	// answer — "never asked", which is not consent.
	ReasonUnenrolled Reason = "unenrolled"
	// ReasonDeclined is an applicable declaration that stated asl_overseer =
	// false.
	ReasonDeclined Reason = "declined"
)

// Declaration is one configured project as authorization sees it: a directory,
// the identity it was configured under, and the opt-in answer it carries.
type Declaration struct {
	Title   string
	Profile string
	// Directory is the canonicalized configured directory.
	Directory string
	// ConfiguredDirectory is the directory exactly as written in the config
	// file, which is what an operator has to edit to change the answer.
	ConfiguredDirectory string
	// Configured is true when the project stated asl_overseer at all, which
	// separates "asked and declined" from "never asked" in diagnostics.
	Configured bool
	// OptIn is true only for an explicit asl_overseer = true.
	OptIn bool
}

func (d Declaration) exclusionReason() Reason {
	if d.Configured {
		return ReasonDeclined
	}
	return ReasonUnenrolled
}

// Repository is one repository a plan participates in. Alias is the plan's own
// name for it and is carried through untouched so a refusal can be reported in
// the caller's vocabulary.
type Repository struct {
	Alias string
	Path  string
}

// Request is one authorization question: may the overseer act on this plan,
// given the complete set of repositories it participates in.
type Request struct {
	Plan         string
	Repositories []Repository
}

// RepositoryDecision is the outcome for a single repository in a request.
type RepositoryDecision struct {
	Alias string
	// Path is the requested path, unmodified.
	Path string
	// ResolvedPath is the canonical absolute, symlink-evaluated directory, or
	// empty when resolution itself failed.
	ResolvedPath string
	Allowed      bool
	Reason       Reason
	// Detail carries the resolution error or the declaration that refused, for
	// operator-facing diagnostics.
	Detail string
	// Declarations is every applicable declaration in configuration order —
	// the full set that was examined, not just the one that decided.
	Declarations []Declaration
}

// Decision is the answer to one Request.
type Decision struct {
	Plan    string
	Allowed bool
	// Reason is ReasonAllowed, or the reason of the first refused repository in
	// request order.
	Reason       Reason
	Repositories []RepositoryDecision
}

// Denials returns every refused repository, in request order.
func (d Decision) Denials() []RepositoryDecision {
	var denials []RepositoryDecision
	for _, repo := range d.Repositories {
		if !repo.Allowed {
			denials = append(denials, repo)
		}
	}
	return denials
}

// Summary is a single operator-facing line explaining the decision.
func (d Decision) Summary() string {
	if d.Allowed {
		return fmt.Sprintf("authorized: %d repositories permitted", len(d.Repositories))
	}
	denials := d.Denials()
	if len(denials) == 0 {
		return fmt.Sprintf("refused: %s", d.Reason)
	}
	first := denials[0]
	line := fmt.Sprintf("refused: %s (%s)", first.reference(), first.Reason)
	if first.Detail != "" {
		line += ": " + first.Detail
	}
	if len(denials) > 1 {
		line += fmt.Sprintf(" (+%d more)", len(denials)-1)
	}
	return line
}

func (r RepositoryDecision) reference() string {
	if r.Alias != "" {
		return r.Alias
	}
	if r.Path != "" {
		return r.Path
	}
	return "repository"
}

// identityResolver is the seam over strict directory resolution, so tests can
// pose a failing Git probe or a linked-worktree topology without a filesystem.
type identityResolver interface {
	ResolveIdentity(path string) (projectresolve.Identity, error)
}

// Evaluator answers authorization requests against a fixed set of configured
// projects. It is immutable after construction and safe for concurrent use.
type Evaluator struct {
	declarations []Declaration
	identities   identityResolver
}

// New returns an Evaluator over the configured projects, probing Git through
// the real `git` binary.
func New(projects []config.Session) *Evaluator {
	return newWithIdentityResolver(projects, projectresolve.NewIdentityResolver())
}

func newWithIdentityResolver(projects []config.Session, identities identityResolver) *Evaluator {
	declarations := make([]Declaration, 0, len(projects))
	for _, p := range projects {
		// A project with no directory names no place on the filesystem, so it
		// can neither grant nor exclude one. Dropping it here keeps an empty
		// directory from canonicalizing into the process working directory and
		// silently governing whatever the daemon happened to start in.
		if p.Directory == "" {
			continue
		}
		dir := projectresolve.AbsPath(p.Directory)
		if dir == "" {
			continue
		}
		declarations = append(declarations, Declaration{
			Title:               p.Title,
			Profile:             p.EffectiveProfile(),
			Directory:           dir,
			ConfiguredDirectory: p.Directory,
			Configured:          p.ASLOverseerConfigured(),
			OptIn:               p.ASLOverseerValue(),
		})
	}
	return &Evaluator{declarations: declarations, identities: identities}
}

// Evaluate answers the whole request at once. Every repository is evaluated so
// the caller can report all refusals, but a single refusal denies the plan:
// participating in a plan is enough to be acted on, so partial permission is
// not a usable answer.
func (e *Evaluator) Evaluate(req Request) Decision {
	decision := Decision{Plan: req.Plan}
	if len(req.Repositories) == 0 {
		decision.Reason = ReasonNoRepositories
		return decision
	}

	decision.Repositories = make([]RepositoryDecision, 0, len(req.Repositories))
	allowed := true
	reason := ReasonAllowed
	for _, repo := range req.Repositories {
		result := e.evaluateRepository(repo)
		decision.Repositories = append(decision.Repositories, result)
		if !result.Allowed && allowed {
			allowed = false
			reason = result.Reason
		}
	}
	decision.Allowed = allowed
	decision.Reason = reason
	return decision
}

func (e *Evaluator) evaluateRepository(repo Repository) RepositoryDecision {
	result := RepositoryDecision{Alias: repo.Alias, Path: repo.Path}

	identity, err := e.identities.ResolveIdentity(repo.Path)
	if err != nil {
		// Anything the resolver did not classify as a merely absent path lands
		// on the identity side, which is the refusal that also covers "we could
		// not tell what repository this is".
		result.Reason = ReasonIdentityUnresolved
		if errors.Is(err, projectresolve.ErrPathUnresolved) {
			result.Reason = ReasonPathUnresolved
		}
		result.Detail = err.Error()
		return result
	}
	result.ResolvedPath = identity.Path
	result.Declarations = e.applicableDeclarations(identity)

	if len(result.Declarations) == 0 {
		result.Reason = ReasonUndeclared
		result.Detail = fmt.Sprintf("%s matches no configured project", identity.Path)
		return result
	}
	// An explicit decline outranks a never-asked one so the reported reason is
	// the strongest exclusion present, not whichever declaration came first in
	// the config file.
	if excluded, ok := strongestExclusion(result.Declarations); ok {
		result.Reason = excluded.exclusionReason()
		result.Detail = fmt.Sprintf("excluded by configured project %s", describe(excluded))
		return result
	}
	result.Allowed = true
	result.Reason = ReasonAllowed
	return result
}

func strongestExclusion(declarations []Declaration) (Declaration, bool) {
	var found Declaration
	ok := false
	for _, d := range declarations {
		if d.OptIn {
			continue
		}
		if !ok || (d.Configured && !found.Configured) {
			found, ok = d, true
		}
	}
	return found, ok
}

func describe(d Declaration) string {
	label := d.Title
	if label == "" {
		label = filepath.Base(d.Directory)
	}
	if d.Profile != "" {
		label = fmt.Sprintf("%s/%s", d.Profile, label)
	}
	return fmt.Sprintf("%q (%s)", label, d.ConfiguredDirectory)
}

// applicableDeclarations returns every configured project that governs the
// directory, in configuration order.
//
// A declaration applies when it overlaps the directory in either direction.
// Both directions matter and for different reasons. An ancestor declaration
// governs because the directory is inside that project, which is what stops a
// nested opt-in from carving an exception out of an enclosing exclusion. A
// descendant declaration governs because a plan working at the enclosing path
// works on that nested project's files too, so its answer has to be honored
// before anything touches them.
//
// Overlap is measured twice. Physically, against the canonical paths, which is
// what makes a symlinked configured directory and a symlinked plan path meet at
// the same place. And within the repository family, against each registered
// checkout, which is what makes an exclusion on a main checkout reach the
// linked worktree of the same repository — the position inside the checkout is
// compared, so sibling projects in one monorepo stay independent instead of
// collapsing into "same repository, therefore same answer".
func (e *Evaluator) applicableDeclarations(identity projectresolve.Identity) []Declaration {
	var applicable []Declaration
	for _, d := range e.declarations {
		if declarationApplies(d, identity) {
			applicable = append(applicable, d)
		}
	}
	return applicable
}

func declarationApplies(d Declaration, identity projectresolve.Identity) bool {
	if pathsOverlap(d.Directory, identity.Path) {
		return true
	}
	if !identity.IsRepository {
		return false
	}
	for _, root := range identity.WorktreeRoots {
		relative, ok := relativeWithin(d.Directory, root)
		if !ok {
			continue
		}
		if relativePathsOverlap(relative, identity.RelativeDir) {
			return true
		}
	}
	return false
}

// pathsOverlap reports whether two canonical paths are the same directory or
// one contains the other.
func pathsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || hasAncestor(a, b) || hasAncestor(b, a)
}

// relativePathsOverlap is pathsOverlap for checkout-relative positions, where
// "." is the checkout root and therefore overlaps everything in it.
func relativePathsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == "." || b == "." {
		return true
	}
	return pathsOverlap(a, b)
}

// relativeWithin returns path's position inside root, or false when it is not
// inside it. The root itself is ".".
func relativeWithin(path, root string) (string, bool) {
	if path == "" || root == "" {
		return "", false
	}
	if path == root {
		return ".", true
	}
	if !hasAncestor(path, root) {
		return "", false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	relative = filepath.Clean(relative)
	if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return relative, true
}

func hasAncestor(path, ancestor string) bool {
	if path == "" || ancestor == "" {
		return false
	}
	prefix := ancestor
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	return strings.HasPrefix(path, prefix)
}
