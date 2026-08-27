// Package release implements the paired agent-session-loop / Agent Mux release
// gate: the procedure that must pass, in order, before the two repositories are
// tagged as a compatible pair.
//
// The two programs are released together because neither is correct alone. ASL
// owns the compatibility corpus and the bridge protocol; Agent Mux vendors the
// corpus and speaks the protocol. A release that ships one side against an
// untested peer is exactly the failure this gate exists to prevent, so the gate
// refuses to reason about either repository in isolation: it reads both source
// revisions, hashes both artifacts, runs both full suites, installs the pair in
// the documented order, restarts the real daemon, and drives a real Kitty
// window before it will sign anything.
//
// Everything the gate learns is written to a single deterministic JSON evidence
// record and signed with `ssh-keygen -Y sign` under the
// "agentmux-asl-release" namespace. The record is the release artifact that
// outlives the terminal it ran in: it names the revisions, the binary hashes,
// the contract set, the OS/Kitty/harness versions, and every command with its
// result and duration. A step that did not run is recorded as `not-run` rather
// than omitted, so a truncated gate can never read as a complete one.
//
// Every external command goes through a CommandRunner, and time comes from a
// clock function. That seam is what makes the gate testable: the isolated test
// in cmd/ points the whole procedure at throwaway repositories, a fake Kitty,
// an ephemeral signing key, and a temporary evidence directory, so the gate
// logic stays continuously exercised without touching the operator's real
// installation.
package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

const (
	// SchemaVersion is the evidence record's schema version. Consumers must
	// refuse a record they do not recognize rather than reading it optimistically.
	SchemaVersion = 1

	// SigningNamespace is the `ssh-keygen -Y sign` namespace for release
	// evidence. A signature made under any other namespace does not verify as a
	// paired release, which keeps a release key that is also used for commits or
	// Git tags from being replayable as a release approval.
	SigningNamespace = "agentmux-asl-release"

	// PairIDPrefix versions the pair identity itself. It changes only when the
	// inputs folded into the pair ID change, so an ID can never mean two things.
	PairIDPrefix = "amx-asl-1."

	// pairIDDigestLen is how much of the pair pre-image digest the ID carries.
	// Sixteen hex characters is the same width aslcontract uses for the contract
	// set ID, and for the same reason: long enough that a collision is not a
	// practical concern, short enough to read out loud and to fit in a tag name.
	pairIDDigestLen = 16

	// Repository aliases used in evidence and progress output. These are the
	// stable names a consumer keys on; the filesystem roots are recorded
	// separately because they are the operator's, not part of the identity.
	AgentMuxRepo = "agentmux"
	ASLRepo      = "asl"
)

// Step statuses. Every planned step carries one, including the ones that never
// executed.
const (
	StatusPassed = "passed"
	StatusFailed = "failed"
	StatusNotRun = "not-run"
)

// Phase names, in execution order.
const (
	PhaseSource      = "source"
	PhaseEnvironment = "environment"
	PhaseBuild       = "build"
	PhaseVerify      = "verify"
	PhaseInstall     = "install"
	PhaseSmoke       = "smoke"
)

// Step names that the gate itself reads output from. The rest are pass/fail
// only; these three feed the evidence record, so they are referenced by name
// rather than by position.
const (
	StepOSVersion     = "os-version"
	StepKittyVersion  = "kitty-version"
	StepHarnessCompat = "harness-compat"
	StepBuildAgentMux = "build-amx"
)

// ErrGateFailed reports that the gate ran correctly and the pair did not pass.
// It is distinct from an invocation error (a missing repository root, an
// unreadable signing key, a tag that already exists) because the two call for
// different operator responses and different process exit codes.
var ErrGateFailed = errors.New("paired release gate failed")

// aslWrapperPath is the ASL entry point inside an ASL checkout, relative to its
// root. The wrapper resolves the repository root from its own location, so the
// copy in the checkout and the stowed symlink both execute this checkout's
// sources -- which is why the gate can probe harness compatibility before the
// install phase and still be talking about the artifact it is releasing.
var aslWrapperPath = filepath.Join("bin", ".local", "bin", "agent-session-loop")

// aslManifestPath is the compatibility manifest inside an ASL checkout. It is
// excluded from every corpus digest, so its hash is recorded here explicitly:
// nothing else in the evidence would notice a manifest that drifted while the
// schemas held still.
var aslManifestPath = filepath.Join("tests", "fixtures", "agent_mux_contract", "manifest.json")

// CommandRunner executes argv with dir as the working directory and returns the
// combined output together with the process exit status. A non-nil error means
// the command could not be run at all (missing executable, unusable directory);
// a command that ran and failed returns a non-zero code and a nil error, so the
// gate can record a real failure distinctly from a broken invocation.
type CommandRunner func(ctx context.Context, dir string, argv []string) (output string, exitCode int, err error)

// Options configures a gate run. Only the two repository roots are required;
// everything else has a defensible default, except signing, which is off unless
// a key is named.
type Options struct {
	// AgentMuxRoot and ASLRoot are the two source checkouts being released.
	AgentMuxRoot string
	ASLRoot      string

	// SigningKey is the SSH private key used for `ssh-keygen -Y sign`. When it
	// is empty the gate still runs and still writes evidence, but the record is
	// marked unsigned. An unsigned record is a rehearsal, not a release.
	SigningKey string

	// EvidenceDir is where the record and its signature are written. Empty
	// means the default under the Agent Mux data directory.
	EvidenceDir string

	// Namespace overrides the signature namespace. Empty means SigningNamespace.
	// It exists so the isolated test can sign under a throwaway namespace, not
	// so releases can choose their own.
	Namespace string

	// Tag writes the annotated pair tag into both repositories after a passing
	// run. It is off by default: tagging is the one gate side effect that
	// changes history, and it should be asked for.
	Tag bool

	// Overwrite permits replacing an existing evidence record for the same pair
	// ID. Without it, a re-run of an already-recorded pair refuses rather than
	// destroying signed evidence.
	Overwrite bool

	// Run executes external commands. Empty means ExecRunner.
	Run CommandRunner

	// Now supplies the clock. Empty means time.Now. Durations are measured
	// through it, so an injected clock makes a recorded run reproducible.
	Now func() time.Time

	// Progress receives a human-readable line per step as the gate advances. A
	// gate run takes tens of minutes; silence for that long is indistinguishable
	// from a hang.
	Progress io.Writer
}

// Step is one planned command. The plan is data rather than control flow so a
// test can assert the exact ordered command list without executing any of it,
// and so the evidence record can list steps that never ran.
type Step struct {
	Name  string   `json:"name"`
	Repo  string   `json:"repo"`
	Phase string   `json:"phase"`
	Argv  []string `json:"command"`
}

// StepResult is a Step plus what happened to it.
type StepResult struct {
	Name       string   `json:"name"`
	Repo       string   `json:"repo"`
	Phase      string   `json:"phase"`
	Command    []string `json:"command"`
	Status     string   `json:"status"`
	ExitCode   int      `json:"exit_code"`
	DurationMS int64    `json:"duration_ms"`
	// OutputTail is recorded only for a failed step. A passing gate's evidence
	// would otherwise carry megabytes of test output that no consumer reads and
	// that makes two identical runs differ byte for byte; a failing one needs
	// the tail to be diagnosable at all.
	OutputTail string `json:"output_tail,omitempty"`
}

// RepoEvidence records what a source checkout was at gate time.
type RepoEvidence struct {
	Repo     string `json:"repo"`
	Root     string `json:"root"`
	Revision string `json:"revision"`
	Describe string `json:"describe"`
	Clean    bool   `json:"clean"`
	// Dirty lists the porcelain status lines when Clean is false. A release
	// built from an unrecorded working tree is unreproducible, so the gate
	// refuses -- and says exactly which paths made it refuse.
	Dirty []string `json:"dirty,omitempty"`
}

// Artifact is a hashed release artifact. Kind distinguishes a single file's
// digest from a source-tree digest, because they are computed differently and a
// consumer must not compare one against the other.
type Artifact struct {
	Name   string `json:"name"`
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Kind   string `json:"kind"` // "file" or "tree"
	SHA256 string `json:"sha256"`
}

// ContractEvidence pins the compatibility contract the pair agrees on.
type ContractEvidence struct {
	SetID          string `json:"set_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

// HarnessVersion is one row of `asl harness-compat --json`, reduced to the
// fields a release record needs.
type HarnessVersion struct {
	Harness string `json:"harness"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// Environment records the machine the pair was actually exercised on.
type Environment struct {
	OS        string           `json:"os"`
	Arch      string           `json:"arch"`
	OSVersion string           `json:"os_version"`
	Go        string           `json:"go"`
	Kitty     string           `json:"kitty"`
	Harnesses []HarnessVersion `json:"harnesses"`
}

// Signature describes the detached signature beside the record. The signature
// covers the evidence file's bytes, so it cannot live inside them.
type Signature struct {
	Signed    bool   `json:"signed"`
	Namespace string `json:"namespace"`
	Path      string `json:"path,omitempty"`
	KeyPath   string `json:"key_path,omitempty"`
}

// TagEvidence records an annotated tag the gate wrote.
type TagEvidence struct {
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
}

// Evidence is the complete release record.
type Evidence struct {
	SchemaVersion int              `json:"schema_version"`
	PairID        string           `json:"pair_id"`
	GeneratedAt   string           `json:"generated_at"`
	Result        string           `json:"result"`
	Repositories  []RepoEvidence   `json:"repositories"`
	Artifacts     []Artifact       `json:"artifacts"`
	Contract      ContractEvidence `json:"contract"`
	Environment   Environment      `json:"environment"`
	Steps         []StepResult     `json:"steps"`
	Signature     Signature        `json:"signature"`
	Tags          []TagEvidence    `json:"tags,omitempty"`
	// Path is where the record was written. It is not part of the signed
	// content's meaning, but it is what an operator needs to verify it.
	Path string `json:"path,omitempty"`
}

// Passed reports whether every planned step ran and passed.
func (e *Evidence) Passed() bool { return e.Result == StatusPassed }

// Plan returns the ordered command list for a gate run over the two named
// checkouts.
//
// The order is the gate's argument, not an implementation detail:
//
//   - environment first, because a missing Kitty or an unsupported harness
//     should cost seconds rather than surfacing after a twenty-minute stress run;
//   - build next, so the artifact being released exists before anything claims
//     to have tested it;
//   - verify next, cheapest gate first (`just check`) and the long stress and
//     crash/restart runs last, so an ordinary regression fails fast;
//   - install after verify, in the documented order -- ASL first, then Agent Mux
//     -- because Agent Mux's own install smoke restarts the daemon, and a daemon
//     restarted against a stale ASL proves nothing about the pair;
//   - smoke last, against the installation the previous phase just produced.
//
// A step that fails stops the run. Continuing past a failure would produce
// evidence in which later steps ran against a state the record does not
// describe.
func Plan(agentMuxRoot, aslRoot string) []Step {
	aslWrapper := filepath.Join(aslRoot, aslWrapperPath)
	return []Step{
		{Name: StepOSVersion, Repo: AgentMuxRepo, Phase: PhaseEnvironment, Argv: []string{"sw_vers", "-productVersion"}},
		{Name: StepKittyVersion, Repo: AgentMuxRepo, Phase: PhaseEnvironment, Argv: []string{"kitty", "--version"}},
		{Name: StepHarnessCompat, Repo: ASLRepo, Phase: PhaseEnvironment, Argv: []string{aslWrapper, "harness-compat", "--json"}},

		{Name: StepBuildAgentMux, Repo: AgentMuxRepo, Phase: PhaseBuild, Argv: []string{"just", "build"}},

		{Name: "amx-check", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "check"}},
		{Name: "asl-tests", Repo: ASLRepo, Phase: PhaseVerify, Argv: []string{"env", "ASL_CLI_TRACE=0", "just", "test-all"}},
		{Name: "asl-lint", Repo: ASLRepo, Phase: PhaseVerify, Argv: []string{"env", "ASL_CLI_TRACE=0", "just", "lint"}},
		{Name: "contract-sync", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "asl-contract-sync", "asl_root=" + aslRoot}},
		{Name: "paired-integration", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "asl-integration"}},
		{Name: "crash-restart", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "crash-restart"}},
		{Name: "amx-stress", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "reliability-stress"}},
		{Name: "asl-stress", Repo: ASLRepo, Phase: PhaseVerify, Argv: []string{"just", "reliability-stress"}},

		{Name: "asl-install", Repo: ASLRepo, Phase: PhaseInstall, Argv: []string{"just", "all"}},
		{Name: "asl-install-verify", Repo: ASLRepo, Phase: PhaseInstall, Argv: []string{"just", "verify-asl-install"}},
		{Name: "amx-install-restart", Repo: AgentMuxRepo, Phase: PhaseInstall, Argv: []string{"just", "install-restart-smoke"}},

		{Name: "cockpit-smoke", Repo: AgentMuxRepo, Phase: PhaseSmoke, Argv: []string{"just", "cockpit-smoke"}},
	}
}

// gate carries the resolved options through a run.
type gate struct {
	opts     Options
	run      CommandRunner
	now      func() time.Time
	progress io.Writer
	// published records that this run has already written its own record, so
	// the rewrite that adds the tag list is not mistaken for an overwrite of a
	// previous release's evidence.
	published bool
}

// Run executes the paired release gate.
//
// It returns the evidence record in every case where a record could be built at
// all, including failures: a failed gate's record is the most useful artifact it
// produces. The error distinguishes the two failure kinds -- ErrGateFailed for a
// pair that did not pass, anything else for an invocation the gate could not
// carry out.
func Run(ctx context.Context, opts Options) (*Evidence, error) {
	g, err := newGate(opts)
	if err != nil {
		return nil, err
	}
	return g.execute(ctx)
}

func newGate(opts Options) (*gate, error) {
	if opts.Run == nil {
		opts.Run = ExecRunner
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Namespace == "" {
		opts.Namespace = SigningNamespace
	}
	if opts.Progress == nil {
		opts.Progress = io.Discard
	}

	root, err := resolveRoot(opts.AgentMuxRoot, "Agent Mux")
	if err != nil {
		return nil, err
	}
	opts.AgentMuxRoot = root
	root, err = resolveRoot(opts.ASLRoot, "agent-session-loop")
	if err != nil {
		return nil, err
	}
	opts.ASLRoot = root

	// The ASL root is identified by the wrapper the gate is about to execute
	// rather than by the directory name, so a plausible-looking but wrong path
	// fails here instead of somewhere inside the environment phase.
	if _, err := os.Stat(filepath.Join(opts.ASLRoot, aslWrapperPath)); err != nil {
		return nil, fmt.Errorf("release: %s does not look like an agent-session-loop checkout (no %s)", opts.ASLRoot, aslWrapperPath)
	}

	if opts.SigningKey != "" {
		expanded, err := filepath.Abs(opts.SigningKey)
		if err != nil {
			return nil, fmt.Errorf("release: signing key path: %w", err)
		}
		if _, err := os.Stat(expanded); err != nil {
			return nil, fmt.Errorf("release: signing key %s is not readable: %w", expanded, err)
		}
		opts.SigningKey = expanded
	}

	if opts.EvidenceDir == "" {
		dir, err := DefaultEvidenceDir()
		if err != nil {
			return nil, err
		}
		opts.EvidenceDir = dir
	}
	dir, err := filepath.Abs(opts.EvidenceDir)
	if err != nil {
		return nil, fmt.Errorf("release: evidence directory: %w", err)
	}
	opts.EvidenceDir = dir

	return &gate{opts: opts, run: opts.Run, now: opts.Now, progress: opts.Progress}, nil
}

func resolveRoot(path, label string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("release: no %s checkout given", label)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("release: %s checkout %q: %w", label, path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("release: %s checkout %s is not readable: %w", label, abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("release: %s checkout %s is not a directory", label, abs)
	}
	return abs, nil
}

// DefaultEvidenceDir is where release records are stored when the config and
// the flag are both silent. It sits under the Agent Mux data directory beside
// the caches and handoffs, following the same layout every other package here
// builds by hand.
func DefaultEvidenceDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("release: locating home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "agent-mux", "release-evidence"), nil
}

// execute runs the whole procedure. It is written as a straight line on purpose:
// the order of these phases is the gate's meaning, and a reader should be able
// to confirm it without following indirection.
func (g *gate) execute(ctx context.Context) (*Evidence, error) {
	steps := Plan(g.opts.AgentMuxRoot, g.opts.ASLRoot)
	ev := &Evidence{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   g.now().UTC().Format(time.RFC3339),
		Result:        StatusFailed,
		Steps:         plannedResults(steps),
		Contract:      ContractEvidence{SetID: aslcontract.ContractSetID},
		Environment: Environment{
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			Go:        runtime.Version(),
			Harnesses: []HarnessVersion{},
		},
		Artifacts: []Artifact{},
		Signature: Signature{Namespace: g.opts.Namespace},
	}

	// Source revisions come first and are recorded even when they disqualify
	// the run, because "which tree refused to release" is the first thing an
	// operator needs.
	repos, err := g.readRepositories(ctx)
	ev.Repositories = repos
	if err != nil {
		return ev, err
	}

	manifestHash, err := sha256File(filepath.Join(g.opts.ASLRoot, aslManifestPath))
	if err != nil {
		return ev, fmt.Errorf("release: hashing the compatibility manifest: %w", err)
	}
	ev.Contract.ManifestSHA256 = manifestHash

	ev.PairID = PairID(repos[0].Revision, repos[1].Revision, ev.Contract.SetID)

	// A dirty tree is a gate failure rather than an invocation error: the gate
	// worked exactly as intended, and the record shows why it stopped.
	if dirty := dirtyRepos(repos); len(dirty) > 0 {
		g.logf("refusing: uncommitted changes in %s", strings.Join(dirty, ", "))
		if writeErr := g.finish(ctx, ev); writeErr != nil {
			return ev, writeErr
		}
		return ev, fmt.Errorf("%w: uncommitted changes in %s", ErrGateFailed, strings.Join(dirty, ", "))
	}

	failed := false
	for i, step := range steps {
		result, output := g.runStep(ctx, step)
		ev.Steps[i] = result
		if result.Status != StatusPassed {
			failed = true
			break
		}
		if err := g.absorb(step, output, ev); err != nil {
			ev.Steps[i].Status = StatusFailed
			ev.Steps[i].OutputTail = tail(output)
			g.logf("FAIL %s: %v", step.Name, err)
			failed = true
			break
		}
	}

	if !failed {
		ev.Result = StatusPassed
	}
	if err := g.finish(ctx, ev); err != nil {
		return ev, err
	}
	if failed {
		return ev, fmt.Errorf("%w: see %s", ErrGateFailed, ev.Path)
	}
	return ev, nil
}

// finish publishes the record and -- only for a passing run -- writes the
// annotated pair tags. Writing happens before signing and tagging so a failure
// in either still leaves the record on disk.
func (g *gate) finish(ctx context.Context, ev *Evidence) error {
	if ev.Result == StatusPassed && g.opts.Tag {
		if err := g.checkTagsAbsent(ctx, ev.PairID); err != nil {
			return err
		}
	}
	if err := g.publish(ctx, ev); err != nil {
		return err
	}
	if ev.Result == StatusPassed && g.opts.Tag {
		if err := g.writeTags(ctx, ev); err != nil {
			return err
		}
		// The tag list is part of the record, so the record is republished once
		// the tags exist. Signing a record that named tags which were never
		// written would be worse than not naming them at all.
		if err := g.publish(ctx, ev); err != nil {
			return err
		}
	}
	return nil
}

// publish declares the signature the record is about to carry, writes the
// record, and then produces that signature over the bytes just written.
//
// The declaration has to come first. `ssh-keygen -Y sign` signs a file, so the
// signature covers whatever the record said at write time. Filling the metadata
// in afterwards would leave the only durable statement about a signed release
// declaring itself unsigned -- with a detached signature that verifies against
// exactly those bytes, which is worse than an unsigned record because it reads
// as one while being provably a release.
//
// Nothing in the metadata depends on the signing run: the namespace, the key,
// and both paths are known before `ssh-keygen` is invoked. So declaring it up
// front costs nothing and is what makes a persisted record self-describing.
func (g *gate) publish(ctx context.Context, ev *Evidence) error {
	priorPath, priorSignature := ev.Path, ev.Signature
	ev.Path = EvidencePath(g.opts.EvidenceDir, ev.PairID)
	ev.Signature = g.declaredSignature(ev)
	if err := g.writeEvidence(ev); err != nil {
		// A write that did not happen leaves nothing to describe. The caller
		// still receives this record, and a run refused because the pair is
		// already recorded would otherwise report the previous release's
		// evidence file and signature as its own.
		ev.Path, ev.Signature = priorPath, priorSignature
		return err
	}
	if err := g.sign(ctx, ev); err != nil {
		// The record on disk now declares a signature that does not exist.
		// Restating it as unsigned is best effort -- the signing error is what
		// the operator acts on -- but leaving the overstatement behind would
		// hand the next reader a record claiming more than the run achieved.
		ev.Signature = Signature{Signed: false, Namespace: g.opts.Namespace}
		if rewriteErr := g.writeEvidence(ev); rewriteErr != nil {
			return errors.Join(err, rewriteErr)
		}
		return err
	}
	return nil
}

// declaredSignature is the signature metadata the record will carry, computed
// before the signature exists. Signing is on exactly when a key was named.
func (g *gate) declaredSignature(ev *Evidence) Signature {
	if g.opts.SigningKey == "" {
		return Signature{Signed: false, Namespace: g.opts.Namespace}
	}
	return Signature{
		Signed:    true,
		Namespace: g.opts.Namespace,
		Path:      SignaturePath(ev.Path),
		KeyPath:   g.opts.SigningKey,
	}
}

// absorb reads the output of the steps whose output the record needs. Every
// other step is judged by its exit status alone.
func (g *gate) absorb(step Step, output string, ev *Evidence) error {
	switch step.Name {
	case StepOSVersion:
		ev.Environment.OSVersion = firstLine(output)
	case StepKittyVersion:
		ev.Environment.Kitty = firstLine(output)
	case StepHarnessCompat:
		harnesses, err := parseHarnessCompat(output)
		if err != nil {
			return err
		}
		ev.Environment.Harnesses = harnesses
	case StepBuildAgentMux:
		artifacts, err := g.hashArtifacts()
		if err != nil {
			return err
		}
		ev.Artifacts = artifacts
	}
	return nil
}

// runStep executes one planned step, timing it through the injected clock.
func (g *gate) runStep(ctx context.Context, step Step) (StepResult, string) {
	dir := g.rootFor(step.Repo)
	g.logf("run  %s [%s] %s", step.Name, step.Repo, strings.Join(step.Argv, " "))
	started := g.now()
	output, code, err := g.run(ctx, dir, step.Argv)
	elapsed := g.now().Sub(started)

	result := StepResult{
		Name:       step.Name,
		Repo:       step.Repo,
		Phase:      step.Phase,
		Command:    step.Argv,
		Status:     StatusPassed,
		ExitCode:   code,
		DurationMS: elapsed.Milliseconds(),
	}
	switch {
	case err != nil:
		result.Status = StatusFailed
		result.ExitCode = -1
		result.OutputTail = tail(output + "\n" + err.Error())
	case code != 0:
		result.Status = StatusFailed
		result.OutputTail = tail(output)
	}
	if result.Status == StatusPassed {
		g.logf("ok   %s (%s)", step.Name, elapsed.Round(time.Millisecond))
	} else {
		g.logf("FAIL %s (exit %d)", step.Name, result.ExitCode)
	}
	return result, output
}

func (g *gate) rootFor(repo string) string {
	if repo == ASLRepo {
		return g.opts.ASLRoot
	}
	return g.opts.AgentMuxRoot
}

// readRepositories records the revision, description, and cleanliness of both
// checkouts. The order is fixed -- Agent Mux, then ASL -- because the pair ID
// folds the two revisions in that order.
func (g *gate) readRepositories(ctx context.Context) ([]RepoEvidence, error) {
	repos := []RepoEvidence{
		{Repo: AgentMuxRepo, Root: g.opts.AgentMuxRoot},
		{Repo: ASLRepo, Root: g.opts.ASLRoot},
	}
	for i := range repos {
		root := repos[i].Root
		rev, err := g.git(ctx, root, "rev-parse", "HEAD")
		if err != nil {
			return repos, fmt.Errorf("release: reading %s revision: %w", repos[i].Repo, err)
		}
		repos[i].Revision = firstLine(rev)

		describe, err := g.git(ctx, root, "describe", "--tags", "--always", "--dirty")
		if err != nil {
			// A repository with no tags at all is normal and must not stop a
			// release; the revision above is the identity that matters.
			describe = repos[i].Revision
		}
		repos[i].Describe = firstLine(describe)

		status, err := g.git(ctx, root, "status", "--porcelain")
		if err != nil {
			return repos, fmt.Errorf("release: reading %s status: %w", repos[i].Repo, err)
		}
		dirty := nonEmptyLines(status)
		repos[i].Clean = len(dirty) == 0
		repos[i].Dirty = dirty
	}
	return repos, nil
}

func (g *gate) git(ctx context.Context, dir string, args ...string) (string, error) {
	argv := append([]string{"git"}, args...)
	output, code, err := g.run(ctx, dir, argv)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("`%s` exited %d: %s", strings.Join(argv, " "), code, strings.TrimSpace(output))
	}
	return output, nil
}

// hashArtifacts records the identity of what is being released: the freshly
// built binary, the ASL wrapper that is about to be stowed, and a digest over
// the ASL package sources.
//
// The source-tree digest exists because ASL ships as Python. There is no
// compiled artifact whose hash pins the code that will run, and hashing only
// the wrapper would pin a twenty-line launcher while the program it launches
// changed underneath it.
func (g *gate) hashArtifacts() ([]Artifact, error) {
	artifacts := []Artifact{
		{Name: "amx", Repo: AgentMuxRepo, Path: "amx", Kind: "file"},
		{Name: "agent-session-loop", Repo: ASLRepo, Path: aslWrapperPath, Kind: "file"},
		{Name: "agent_session_loop", Repo: ASLRepo, Path: "agent_session_loop", Kind: "tree"},
	}
	for i := range artifacts {
		full := filepath.Join(g.rootFor(artifacts[i].Repo), artifacts[i].Path)
		var (
			digest string
			err    error
		)
		if artifacts[i].Kind == "tree" {
			digest, err = sha256Tree(full, ".py")
		} else {
			digest, err = sha256File(full)
		}
		if err != nil {
			return nil, fmt.Errorf("hashing %s: %w", artifacts[i].Name, err)
		}
		artifacts[i].SHA256 = digest
	}
	return artifacts, nil
}

func (g *gate) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(g.progress, format+"\n", args...)
}

// PairID is the deterministic identity of a released pair: the two source
// revisions and the contract set they agree on, in that fixed order.
//
// Nothing environmental goes in. Two operators on two machines releasing the
// same revisions must arrive at the same pair ID, or the ID cannot be used to
// correlate the two repositories' tags with the evidence record.
func PairID(agentMuxRevision, aslRevision, contractSetID string) string {
	sum := sha256.Sum256([]byte(agentMuxRevision + "\n" + aslRevision + "\n" + contractSetID + "\n"))
	return PairIDPrefix + hex.EncodeToString(sum[:])[:pairIDDigestLen]
}

func plannedResults(steps []Step) []StepResult {
	results := make([]StepResult, 0, len(steps))
	for _, step := range steps {
		results = append(results, StepResult{
			Name:    step.Name,
			Repo:    step.Repo,
			Phase:   step.Phase,
			Command: step.Argv,
			Status:  StatusNotRun,
		})
	}
	return results
}

func dirtyRepos(repos []RepoEvidence) []string {
	var names []string
	for _, repo := range repos {
		if !repo.Clean {
			names = append(names, repo.Repo)
		}
	}
	return names
}

// parseHarnessCompat reduces `asl harness-compat --json` to the rows the record
// keeps. The payload is trusted for shape but not for content: a harness the
// policy classifies as anything other than supported is reported as-is rather
// than rejected here, because the strict/non-strict decision belongs to the
// step's own exit status.
func parseHarnessCompat(output string) ([]HarnessVersion, error) {
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Harnesses     []struct {
			Harness string `json:"harness"`
			Version string `json:"version"`
			Status  string `json:"status"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, fmt.Errorf("parsing harness compatibility report: %w", err)
	}
	if len(payload.Harnesses) == 0 {
		return nil, errors.New("harness compatibility report named no harnesses")
	}
	versions := make([]HarnessVersion, 0, len(payload.Harnesses))
	for _, row := range payload.Harnesses {
		versions = append(versions, HarnessVersion{
			Harness: row.Harness,
			Version: row.Version,
			Status:  row.Status,
		})
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Harness < versions[j].Harness })
	return versions, nil
}

func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// sha256Tree digests every file under root whose name ends in suffix, in
// ascending path order, over the pre-image "<relative path>\n<file digest>\n".
//
// This is the same construction aslcontract uses for the corpus digest, and it
// is deliberate: paths participate, so a renamed file changes the digest, and
// the ordering is fixed, so two machines agree.
func sha256Tree(root, suffix string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no %s files under %s", suffix, root)
	}
	sort.Strings(paths)

	digest := sha256.New()
	for _, rel := range paths {
		fileSum, err := sha256File(filepath.Join(root, rel))
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(digest, "%s\n%s\n", filepath.ToSlash(rel), fileSum)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func firstLine(s string) string {
	trimmed := strings.TrimSpace(s)
	if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// tailBytes bounds a recorded failure tail. Large enough for a Go test failure
// with its stack, small enough that a record stays readable.
const tailBytes = 4000

func tail(output string) string {
	trimmed := strings.TrimSpace(output)
	if len(trimmed) <= tailBytes {
		return trimmed
	}
	return "...(truncated)...\n" + trimmed[len(trimmed)-tailBytes:]
}
