package overseerpolicy

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/projectresolve"
)

// fakeIdentityResolver poses a filesystem and a Git topology directly, so the
// authorization matrix can be written without building a repository per case.
// The real resolver is exercised end to end at the bottom of this file.
type fakeIdentityResolver struct {
	identities map[string]projectresolve.Identity
	failures   map[string]error
}

func (f fakeIdentityResolver) ResolveIdentity(path string) (projectresolve.Identity, error) {
	if err, ok := f.failures[path]; ok {
		return projectresolve.Identity{}, err
	}
	if identity, ok := f.identities[path]; ok {
		return identity, nil
	}
	return projectresolve.Identity{}, fmt.Errorf("%w: %s", projectresolve.ErrPathUnresolved, path)
}

func abs(parts ...string) string {
	return filepath.Join(append([]string{string(filepath.Separator)}, parts...)...)
}

func boolPtr(v bool) *bool { return &v }

func plainDir(path string) projectresolve.Identity {
	return projectresolve.Identity{Path: path}
}

// repoDir describes a directory inside a checkout: its own path, the checkout
// it sits in, and every checkout of the same repository.
func repoDir(path, root string, roots ...string) projectresolve.Identity {
	relative := "."
	if path != root {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			panic(err)
		}
		relative = rel
	}
	if len(roots) == 0 {
		roots = []string{root}
	}
	return projectresolve.Identity{
		Path:          path,
		IsRepository:  true,
		CommonDir:     filepath.Join(roots[0], ".git"),
		WorktreeRoot:  root,
		WorktreeRoots: roots,
		RelativeDir:   relative,
	}
}

func resolving(identities ...projectresolve.Identity) fakeIdentityResolver {
	byPath := make(map[string]projectresolve.Identity, len(identities))
	for _, identity := range identities {
		byPath[identity.Path] = identity
	}
	return fakeIdentityResolver{identities: byPath}
}

func (f fakeIdentityResolver) alias(from string, to projectresolve.Identity) fakeIdentityResolver {
	f.identities[from] = to
	return f
}

func (f fakeIdentityResolver) failing(path string, err error) fakeIdentityResolver {
	if f.failures == nil {
		f.failures = map[string]error{}
	}
	f.failures[path] = err
	return f
}

func requestFor(paths ...string) Request {
	repos := make([]Repository, 0, len(paths))
	for i, path := range paths {
		repos = append(repos, Repository{Alias: fmt.Sprintf("repo%d", i+1), Path: path})
	}
	return Request{Plan: "plan-1", Repositories: repos}
}

func evaluate(t *testing.T, projects []config.Session, resolver identityResolver, req Request) Decision {
	t.Helper()
	return newWithIdentityResolver(projects, resolver).Evaluate(req)
}

func assertDenied(t *testing.T, decision Decision, want Reason) {
	t.Helper()
	if decision.Allowed {
		t.Fatalf("decision allowed the plan, want %s: %s", want, decision.Summary())
	}
	if decision.Reason != want {
		t.Fatalf("reason = %s, want %s: %s", decision.Reason, want, decision.Summary())
	}
}

func assertAllowed(t *testing.T, decision Decision) {
	t.Helper()
	if !decision.Allowed {
		t.Fatalf("decision refused the plan: %s", decision.Summary())
	}
	if decision.Reason != ReasonAllowed {
		t.Fatalf("reason = %s, want %s", decision.Reason, ReasonAllowed)
	}
}

func TestEvaluateAllowsExplicitlyOptedInProject(t *testing.T) {
	repo := abs("repos", "app")
	inside := filepath.Join(repo, "internal", "pkg")
	decision := evaluate(t,
		[]config.Session{{Title: "App", Directory: repo, Profile: "work", ASLOverseer: boolPtr(true)}},
		resolving(repoDir(inside, repo)),
		requestFor(inside),
	)
	assertAllowed(t, decision)
	if got := decision.Repositories[0].ResolvedPath; got != inside {
		t.Errorf("ResolvedPath = %q, want %q", got, inside)
	}
}

func TestEvaluateRefusesUnconfiguredRepository(t *testing.T) {
	repo := abs("repos", "app")
	other := abs("repos", "other")
	decision := evaluate(t,
		[]config.Session{{Title: "App", Directory: repo, Profile: "work", ASLOverseer: boolPtr(true)}},
		resolving(repoDir(other, other)),
		requestFor(other),
	)
	assertDenied(t, decision, ReasonUndeclared)
	if len(decision.Repositories[0].Declarations) != 0 {
		t.Fatalf("unconfigured repository reported declarations: %#v", decision.Repositories[0].Declarations)
	}
}

func TestEvaluateRefusesOmittedAndDeclinedOptIn(t *testing.T) {
	repo := abs("repos", "app")
	for name, tc := range map[string]struct {
		optIn *bool
		want  Reason
	}{
		"omitted":  {optIn: nil, want: ReasonUnenrolled},
		"declined": {optIn: boolPtr(false), want: ReasonDeclined},
	} {
		decision := evaluate(t,
			[]config.Session{{Title: "App", Directory: repo, Profile: "work", ASLOverseer: tc.optIn}},
			resolving(repoDir(repo, repo)),
			requestFor(repo),
		)
		if decision.Allowed || decision.Reason != tc.want {
			t.Errorf("%s: decision = %v/%s, want refused %s", name, decision.Allowed, decision.Reason, tc.want)
		}
	}
}

func TestEvaluateDuplicateAliasesForOneDirectory(t *testing.T) {
	repo := abs("repos", "app")
	resolver := resolving(repoDir(repo, repo))

	// The same repository configured once per harness profile is one place with
	// two names. Both names consenting is consent.
	allowed := evaluate(t,
		[]config.Session{
			{Title: "App (claude)", Directory: repo, Profile: "claude", ASLOverseer: boolPtr(true)},
			{Title: "App (codex)", Directory: repo, Profile: "codex", ASLOverseer: boolPtr(true)},
		},
		resolver, requestFor(repo),
	)
	assertAllowed(t, allowed)
	if got := len(allowed.Repositories[0].Declarations); got != 2 {
		t.Errorf("examined %d declarations, want both aliases", got)
	}

	// One alias declining is an applicable exclusion. An alias is not a second
	// opinion the overseer gets to choose between.
	refused := evaluate(t,
		[]config.Session{
			{Title: "App (claude)", Directory: repo, Profile: "claude", ASLOverseer: boolPtr(true)},
			{Title: "App (codex)", Directory: repo, Profile: "codex", ASLOverseer: boolPtr(false)},
		},
		resolver, requestFor(repo),
	)
	assertDenied(t, refused, ReasonDeclined)

	// An alias that never stated an answer is still not consent, and is
	// reported as unenrolled rather than as a decline.
	silent := evaluate(t,
		[]config.Session{
			{Title: "App (claude)", Directory: repo, Profile: "claude", ASLOverseer: boolPtr(true)},
			{Title: "App (codex)", Directory: repo, Profile: "codex"},
		},
		resolver, requestFor(repo),
	)
	assertDenied(t, silent, ReasonUnenrolled)
}

func TestEvaluateConflictingDeclarationsReportTheStrongestExclusion(t *testing.T) {
	repo := abs("repos", "app")
	decision := evaluate(t,
		[]config.Session{
			// Config order deliberately puts the weaker exclusion first: the
			// reported reason must be the strongest one that applies, not the
			// one that happened to be read first.
			{Title: "App (silent)", Directory: repo, Profile: "claude"},
			{Title: "App (declined)", Directory: repo, Profile: "codex", ASLOverseer: boolPtr(false)},
			{Title: "App (opted in)", Directory: repo, Profile: "work", ASLOverseer: boolPtr(true)},
		},
		resolving(repoDir(repo, repo)),
		requestFor(repo),
	)
	assertDenied(t, decision, ReasonDeclined)
	if got := len(decision.Repositories[0].Declarations); got != 3 {
		t.Fatalf("examined %d declarations, want all three", got)
	}
	if detail := decision.Repositories[0].Detail; !strings.Contains(detail, "App (declined)") {
		t.Errorf("detail = %q, want the declining project named", detail)
	}
}

func TestEvaluateNestedProjectCannotBypassEnclosingExclusion(t *testing.T) {
	outer := abs("repos", "outer")
	inner := filepath.Join(outer, "services", "api")
	projects := []config.Session{
		{Title: "Outer", Directory: outer, Profile: "work", ASLOverseer: boolPtr(false)},
		{Title: "API", Directory: inner, Profile: "work", ASLOverseer: boolPtr(true)},
	}
	resolver := resolving(repoDir(outer, outer), repoDir(inner, outer))

	// Display resolution answers with the most specific project, which is the
	// opted-in one. Authorization must not: the enclosing declination applies
	// to the same directory and denies it.
	matches := projectresolve.New(projects).Matches(inner)
	if len(matches) != 1 || matches[0].Title != "API" {
		t.Fatalf("display resolution = %#v, want only the most specific project", matches)
	}

	decision := evaluate(t, projects, resolver, requestFor(inner))
	assertDenied(t, decision, ReasonDeclined)
	if got := len(decision.Repositories[0].Declarations); got != 2 {
		t.Fatalf("examined %d declarations, want the enclosing one too", got)
	}
}

func TestEvaluateNestedExclusionDeniesTheEnclosingRequest(t *testing.T) {
	outer := abs("repos", "outer")
	inner := filepath.Join(outer, "vendor", "inner")
	projects := []config.Session{
		{Title: "Outer", Directory: outer, Profile: "work", ASLOverseer: boolPtr(true)},
		{Title: "Inner", Directory: inner, Profile: "work", ASLOverseer: boolPtr(false)},
	}
	// A plan working at the enclosing path works on the nested project's files
	// too, so the nested answer has to be honored before anything touches them.
	// This holds on the plain-directory path as well as the repository one:
	// nesting is a filesystem fact, not a Git one.
	for name, identity := range map[string]projectresolve.Identity{
		"repository":      repoDir(outer, outer),
		"plain directory": plainDir(outer),
	} {
		decision := evaluate(t, projects, resolving(identity), requestFor(outer))
		if decision.Allowed || decision.Reason != ReasonDeclined {
			t.Errorf("%s: decision = %v/%s, want refused %s",
				name, decision.Allowed, decision.Reason, ReasonDeclined)
		}
	}
}

func TestEvaluateNestedRepositoryStillObeysTheEnclosingDeclaration(t *testing.T) {
	outer := abs("repos", "outer")
	inner := filepath.Join(outer, "vendor", "inner")
	// The two are separate repository families — display grouping keeps them
	// apart — but the inner checkout is still physically inside the outer
	// project, so the outer declination reaches it.
	decision := evaluate(t,
		[]config.Session{
			{Title: "Outer", Directory: outer, Profile: "work", ASLOverseer: boolPtr(false)},
			{Title: "Inner", Directory: inner, Profile: "work", ASLOverseer: boolPtr(true)},
		},
		resolving(repoDir(inner, inner)),
		requestFor(inner),
	)
	assertDenied(t, decision, ReasonDeclined)
}

func TestEvaluateMonorepoSiblingsStayIndependent(t *testing.T) {
	mono := abs("repos", "mono")
	api := filepath.Join(mono, "services", "api")
	web := filepath.Join(mono, "services", "web")
	projects := []config.Session{
		{Title: "API", Directory: api, Profile: "work", ASLOverseer: boolPtr(true)},
		{Title: "Web", Directory: web, Profile: "work", ASLOverseer: boolPtr(false)},
	}
	resolver := resolving(repoDir(api, mono), repoDir(web, mono))

	// Sharing a repository is not sharing an answer: the sibling is a different
	// place, so its declination does not reach here.
	assertAllowed(t, evaluate(t, projects, resolver, requestFor(api)))
	assertDenied(t, evaluate(t, projects, resolver, requestFor(web)), ReasonDeclined)
}

func TestEvaluateSymlinkedPathResolvesToTheDeclaredProject(t *testing.T) {
	repo := abs("repos", "app")
	link := abs("home", "app-link")
	// The plan names the repository through a symlink; the declaration names
	// the real path. Canonicalization has to make them meet, or the symlink is
	// a bypass.
	decision := evaluate(t,
		[]config.Session{{Title: "App", Directory: repo, Profile: "work", ASLOverseer: boolPtr(false)}},
		resolving(repoDir(repo, repo)).alias(link, repoDir(repo, repo)),
		requestFor(link),
	)
	assertDenied(t, decision, ReasonDeclined)
}

func TestEvaluateDeclarationThatCannotBeCanonicalizedGovernsNothing(t *testing.T) {
	repo := abs("repos", "app")
	link := abs("home", "app-link")
	// The declaration is written through a path that does not exist on this
	// machine, so it cannot be canonicalized onto the repository it was meant
	// to name. It grants nothing rather than granting on the literal spelling —
	// a configured directory whose target is unknown must not authorize.
	// (The resolvable case, where a symlinked declaration does reach the real
	// checkout, is covered end to end in TestEvaluateAgainstRealWorktreesAndSymlinks.)
	decision := evaluate(t,
		[]config.Session{{Title: "App", Directory: link, Profile: "work", ASLOverseer: boolPtr(true)}},
		resolving(repoDir(repo, repo)),
		requestFor(repo),
	)
	assertDenied(t, decision, ReasonUndeclared)
}

func TestEvaluateLinkedWorktreeInheritsTheMainCheckoutExclusion(t *testing.T) {
	repo := abs("repos", "app")
	linked := abs("worktrees", "app-security")
	// Nothing is configured at the linked worktree at all. It is the same
	// repository, in the same position inside it, so the main checkout's
	// declination applies.
	decision := evaluate(t,
		[]config.Session{{Title: "App", Directory: repo, Profile: "work", ASLOverseer: boolPtr(false)}},
		resolving(repoDir(linked, linked, repo, linked)),
		requestFor(linked),
	)
	assertDenied(t, decision, ReasonDeclined)
}

func TestEvaluateLinkedWorktreeOfOptedInRepositoryIsAllowed(t *testing.T) {
	repo := abs("repos", "app")
	linked := abs("worktrees", "app-security")
	decision := evaluate(t,
		[]config.Session{{Title: "App", Directory: repo, Profile: "work", ASLOverseer: boolPtr(true)}},
		resolving(repoDir(filepath.Join(linked, "internal"), linked, repo, linked)),
		requestFor(filepath.Join(linked, "internal")),
	)
	assertAllowed(t, decision)
}

func TestEvaluateLinkedWorktreeExclusionReachesAcrossCheckouts(t *testing.T) {
	repo := abs("repos", "app")
	linked := abs("worktrees", "app-security")
	// The exclusion is declared on the linked worktree and the plan names the
	// main checkout. The direction must not matter.
	decision := evaluate(t,
		[]config.Session{
			{Title: "App", Directory: repo, Profile: "work", ASLOverseer: boolPtr(true)},
			{Title: "App Security", Directory: linked, Profile: "work", ASLOverseer: boolPtr(false)},
		},
		resolving(repoDir(repo, repo, repo, linked)),
		requestFor(repo),
	)
	assertDenied(t, decision, ReasonDeclined)
}

func TestEvaluateMonorepoWorktreeSiblingsStayIndependent(t *testing.T) {
	mono := abs("repos", "mono")
	linked := abs("worktrees", "mono-feature")
	api := filepath.Join(mono, "services", "api")
	web := filepath.Join(linked, "services", "web")
	// The excluded sibling lives in another checkout of the same repository.
	// Family identity alone must not collapse the two into one answer.
	decision := evaluate(t,
		[]config.Session{
			{Title: "API", Directory: api, Profile: "work", ASLOverseer: boolPtr(true)},
			{Title: "Web", Directory: web, Profile: "work", ASLOverseer: boolPtr(false)},
		},
		resolving(repoDir(api, mono, mono, linked)),
		requestFor(api),
	)
	assertAllowed(t, decision)
}

func TestEvaluateRefusesMissingPath(t *testing.T) {
	repo := abs("repos", "app")
	decision := evaluate(t,
		[]config.Session{{Title: "App", Directory: repo, Profile: "work", ASLOverseer: boolPtr(true)}},
		resolving(repoDir(repo, repo)),
		requestFor(filepath.Join(repo, "gone")),
	)
	assertDenied(t, decision, ReasonPathUnresolved)
	if decision.Repositories[0].ResolvedPath != "" {
		t.Error("an unresolved path reported a resolved path")
	}
	if decision.Repositories[0].Detail == "" {
		t.Error("an unresolved path reported no detail")
	}
}

func TestEvaluateRefusesFailedGitIdentityResolution(t *testing.T) {
	repo := abs("repos", "app")
	unknown := abs("worktrees", "app-security")
	// The probe failed, so this directory cannot be shown to be outside the
	// declined repository — it could be a linked worktree of it. Consent for
	// some other project in the config does not answer the question.
	decision := evaluate(t,
		[]config.Session{
			{Title: "App", Directory: repo, Profile: "work", ASLOverseer: boolPtr(false)},
			{Title: "Elsewhere", Directory: abs("repos", "elsewhere"), Profile: "work", ASLOverseer: boolPtr(true)},
		},
		resolving(repoDir(repo, repo)).failing(unknown,
			fmt.Errorf("%w: %s: git rev-parse timed out after 2s", projectresolve.ErrIdentityUnresolved, unknown)),
		requestFor(unknown),
	)
	assertDenied(t, decision, ReasonIdentityUnresolved)
}

func TestEvaluateRefusesUnknownResolverFailures(t *testing.T) {
	repo := abs("repos", "app")
	// An error the resolver did not classify is still a refusal, and lands on
	// the identity side rather than being mistaken for a merely absent path.
	decision := evaluate(t,
		[]config.Session{{Title: "App", Directory: repo, Profile: "work", ASLOverseer: boolPtr(true)}},
		resolving(repoDir(repo, repo)).failing(repo, errors.New("resolver exploded")),
		requestFor(repo),
	)
	assertDenied(t, decision, ReasonIdentityUnresolved)
}

func TestEvaluateAllowsAPlainDirectoryProject(t *testing.T) {
	notes := abs("notes")
	decision := evaluate(t,
		[]config.Session{{Title: "Notes", Directory: notes, Profile: "claude", ASLOverseer: boolPtr(true)}},
		resolving(plainDir(filepath.Join(notes, "inbox"))),
		requestFor(filepath.Join(notes, "inbox")),
	)
	assertAllowed(t, decision)
}

func TestEvaluateWholePlanIsRefusedWhenOneRepositoryIsExcluded(t *testing.T) {
	permitted := abs("repos", "app")
	excluded := abs("repos", "secrets")
	decision := evaluate(t,
		[]config.Session{
			{Title: "App", Directory: permitted, Profile: "work", ASLOverseer: boolPtr(true)},
			{Title: "Secrets", Directory: excluded, Profile: "work", ASLOverseer: boolPtr(false)},
		},
		resolving(repoDir(permitted, permitted), repoDir(excluded, excluded)),
		requestFor(permitted, excluded),
	)
	assertDenied(t, decision, ReasonDeclined)
	// Every repository is still evaluated, so an operator sees which one is
	// permitted and which one refused rather than only the first failure.
	if len(decision.Repositories) != 2 {
		t.Fatalf("evaluated %d repositories, want both", len(decision.Repositories))
	}
	if !decision.Repositories[0].Allowed || decision.Repositories[1].Allowed {
		t.Fatalf("per-repository decisions = %v/%v, want allowed/refused",
			decision.Repositories[0].Allowed, decision.Repositories[1].Allowed)
	}
	denials := decision.Denials()
	if len(denials) != 1 || denials[0].Alias != "repo2" {
		t.Fatalf("Denials() = %#v, want the excluded repository", denials)
	}
	if summary := decision.Summary(); !strings.Contains(summary, "repo2") {
		t.Errorf("Summary() = %q, want the refused alias named", summary)
	}
}

func TestEvaluateReportsTheFirstRefusalAndCountsTheRest(t *testing.T) {
	first := abs("repos", "first")
	second := abs("repos", "second")
	decision := evaluate(t,
		[]config.Session{{Title: "First", Directory: first, Profile: "work"}},
		resolving(repoDir(first, first), repoDir(second, second)),
		requestFor(first, second),
	)
	assertDenied(t, decision, ReasonUnenrolled)
	if got := len(decision.Denials()); got != 2 {
		t.Fatalf("Denials() returned %d, want both refusals", got)
	}
	if summary := decision.Summary(); !strings.Contains(summary, "+1 more") {
		t.Errorf("Summary() = %q, want the remaining refusal counted", summary)
	}
}

func TestEvaluateRefusesARequestWithNoRepositories(t *testing.T) {
	decision := evaluate(t,
		[]config.Session{{Title: "App", Directory: abs("repos", "app"), Profile: "work", ASLOverseer: boolPtr(true)}},
		resolving(),
		Request{Plan: "plan-1"},
	)
	assertDenied(t, decision, ReasonNoRepositories)
}

func TestEvaluateIgnoresDeclarationsWithoutADirectory(t *testing.T) {
	repo := abs("repos", "app")
	// A project with no directory names no place, so it must not be picked up
	// as governing whatever the process happens to be running in.
	evaluator := newWithIdentityResolver(
		[]config.Session{
			{Title: "Template", Profile: "work", ASLOverseer: boolPtr(false)},
			{Title: "App", Directory: repo, Profile: "work", ASLOverseer: boolPtr(true)},
		},
		resolving(repoDir(repo, repo)),
	)
	if got := len(evaluator.declarations); got != 1 {
		t.Fatalf("kept %d declarations, want only the one with a directory", got)
	}
	assertAllowed(t, evaluator.Evaluate(requestFor(repo)))
}

func TestEvaluateAgainstRealWorktreesAndSymlinks(t *testing.T) {
	git := requireGit(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo with space")
	linked := filepath.Join(root, "linked worktree")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	runGit(t, git, repo, "init")
	runGit(t, git, repo, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "--no-verify", "-m", "initial")
	runGit(t, git, repo, "worktree", "add", "-b", "feature/policy", linked)
	nested := filepath.Join(linked, "services", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	symlink := filepath.Join(root, "app link")
	if err := os.Symlink(repo, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The project is declared through a symlink to the main checkout; the plan
	// runs in a subdirectory of a linked worktree. Every hop — `~`-free symlink
	// resolution, the checkout family, and the position inside it — has to hold
	// for the declared answer to reach the plan.
	for name, tc := range map[string]struct {
		optIn      *bool
		wantAllow  bool
		wantReason Reason
	}{
		"opted in": {optIn: boolPtr(true), wantAllow: true, wantReason: ReasonAllowed},
		"declined": {optIn: boolPtr(false), wantReason: ReasonDeclined},
		"omitted":  {optIn: nil, wantReason: ReasonUnenrolled},
	} {
		evaluator := New([]config.Session{{
			Title:       "App",
			Directory:   symlink,
			Profile:     "work",
			ASLOverseer: tc.optIn,
		}})
		decision := evaluator.Evaluate(Request{
			Plan:         "plan-real",
			Repositories: []Repository{{Alias: "app", Path: nested}},
		})
		if decision.Allowed != tc.wantAllow || decision.Reason != tc.wantReason {
			t.Errorf("%s: decision = %v/%s, want %v/%s (%s)",
				name, decision.Allowed, decision.Reason, tc.wantAllow, tc.wantReason, decision.Summary())
		}
	}

	// A directory outside the repository is unconfigured, not inherited.
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	evaluator := New([]config.Session{{Title: "App", Directory: symlink, Profile: "work", ASLOverseer: boolPtr(true)}})
	decision := evaluator.Evaluate(Request{
		Plan:         "plan-real",
		Repositories: []Repository{{Alias: "outside", Path: outside}},
	})
	assertDenied(t, decision, ReasonUndeclared)

	// And a path that does not exist is refused before any declaration is
	// consulted.
	missing := evaluator.Evaluate(Request{
		Plan:         "plan-real",
		Repositories: []Repository{{Alias: "gone", Path: filepath.Join(root, "gone")}},
	})
	assertDenied(t, missing, ReasonPathUnresolved)
}

func requireGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	return git
}

func runGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Agent Mux",
		"GIT_AUTHOR_EMAIL=agentmux@example.invalid",
		"GIT_COMMITTER_NAME=Agent Mux",
		"GIT_COMMITTER_EMAIL=agentmux@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
