// Package release implements the paired agent-session-loop / Agent Mux release
// gate: the ordered step plan that must pass, step by step, before the two
// repositories are tagged with annotated pair tags as a compatible pair.
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
// in cmd/release points the whole procedure at throwaway repositories, a fake Kitty,
// an ephemeral signing key, and a temporary evidence directory, so the gate
// logic stays continuously exercised without touching the operator's real
// installation.
package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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

// StatusPartial is a whole-run result and never a step status: the gate passed,
// pair tagging then broke midway, and the ref it had already written could not
// be taken back. One repository is left carrying a tag the other does not have.
//
// It is a distinct value rather than a flag beside `"result": "passed"` because
// every consumer that only knows the old vocabulary still reads it as "not
// passed", which is the property that matters. A half-tagged pair must never be
// mistaken for a released one, including by a reader written before this state
// existed.
const StatusPartial = "partial"

// StatusNotReleased is the other whole-run result a tagging attempt can end on:
// every step passed, the tags were asked for, something after them failed, and
// the compensation took every ref back. Nothing survives for anyone to clear.
//
// It is not `passed`, because a run asked to release a pair and released
// nothing did not do what it was asked; the record has to qualify the outcome,
// and a result that says `passed` beside a `tag_failure` block is two statements
// that disagree. It is not `failed` either, because that is the gate's own
// verdict on the pair and every planned step here passed -- a reader would go
// looking for the step that broke and find none. And it is not `partial`,
// because that value's entire purpose is to say a ref was left behind: an
// operator who reads it goes and clears one, and here there is nothing to clear.
//
// Like StatusPartial it is safe for a consumer written before it existed, which
// reads any value other than `passed` as not passed.
const StatusNotReleased = "not-released"

// StatusTagging is the result a tagging run's record carries while the release
// it describes is still being assembled. It is written before the first ref
// exists and replaced by the finished account, so it is the value that survives
// a tagging run that was interrupted or that could never publish its outcome.
//
// It exists because the alternative is a record that overstates itself for a
// window the gate cannot shorten to nothing. A tagging run's record used to be
// published as `passed` before either tag was written, on the reasoning that
// every planned step really had passed -- and then rewritten afterwards to say
// what tagging did. Everything between those two writes was a durable, signed
// `"result": "passed"` file for a release that did not exist yet and might
// never. Recovering from that depends on rewriting the record later, which
// depends on the evidence medium still being writable, which is exactly the
// thing that fails on the paths where the guarantee is needed.
//
// So the claim is not made until it is true. The pre-tag record says a tagging
// attempt is under way and declares itself unsigned; nothing about it is
// admissible as a release, and no signature is produced that could later be
// replayed as one. `passed` is written once -- together with the signature over
// it -- only after both refs exist. See gate.finish and gate.writeSignedEvidence.
//
// Like StatusPartial and StatusNotReleased it is safe for a consumer written
// before it existed, which reads any value other than `passed` as not passed.
const StatusTagging = "tagging"

// ErrGateFailed reports that the gate ran correctly and the pair did not pass.
// It is distinct from an invocation error (a missing repository root, an
// unreadable signing key, a tag that already exists) because the two call for
// different operator responses and different process exit codes.
var ErrGateFailed = errors.New("paired release gate failed")

// ErrUnsignedTag reports a tagging run that named no signing key.
//
// The pair tag is the durable claim that a release happened, and the evidence
// record's signature is the only thing that makes that claim checkable. A run
// without a key writes a record marked unsigned -- a rehearsal -- and a
// rehearsal that could still write the tags would produce release provenance no
// one can verify, indistinguishable in either repository from the real thing.
// So the two are bound together here rather than left to operator discipline.
var ErrUnsignedTag = errors.New("release: tagging requires a signing key: an unsigned run is a rehearsal and never writes release tags")

// ErrPartialTagging reports the one outcome the pair-tag transaction cannot
// prevent: a tag was created in one repository, the peer's creation failed, and
// the first tag could not be removed either.
//
// It is a sentinel because it is the only gate outcome that leaves durable state
// behind for a human to clear. Everything else either happened or did not; this
// one demands an action before the pair can be attempted again, and the evidence
// record carries the exact command.
var ErrPartialTagging = errors.New("release: pair tagging left a repository tagged")

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
// produces. The error says which kind of failure it was -- ErrGateFailed for a
// pair that did not pass, ErrPartialTagging for a pair whose tagging left a ref
// behind that a human must clear, anything else for an invocation the gate could
// not carry out.
func Run(ctx context.Context, opts Options) (*Evidence, error) {
	g, err := newGate(opts)
	if err != nil {
		return nil, err
	}
	return g.execute(ctx)
}

func newGate(opts Options) (*gate, error) {
	// This is the first thing checked, ahead of resolving roots and ahead of
	// every default, because it is the only refusal that has to happen before
	// the gate has read anything at all: a run that cannot legitimately tag
	// must not run the steps, write a record, or read either repository's refs
	// on its way to discovering that.
	if opts.Tag && strings.TrimSpace(opts.SigningKey) == "" {
		return nil, ErrUnsignedTag
	}

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
