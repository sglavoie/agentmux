// The desktop release gate: what must pass before Agent Mux.app is released,
// kept apart from the paired gate because everything in it needs Xcode.
//
// The paired gate (gate.go) judges amx and agent-session-loop and must run on a
// machine that has neither Xcode nor a signing certificate. This one judges the
// app: it builds the Release bundle, checks what was built, and runs the hosted
// suite against the Debug and the Release app. It installs nothing, restarts
// nothing, and tags nothing, so it is safe to run on the machine it is typed
// on; the Xcode work goes wherever macos/scripts/remote-ui.sh sends it.
//
// It has one outcome the paired gate does not: pending. A Mac with no Xcode, a
// remote Mac that does not answer, or a notarized release asked of a keychain
// with no Developer ID is not a release that failed, and it is certainly not one
// that passed. The record says the gate could not judge, names why, lists every
// step as not run, and the command exits nonzero.

package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	// DesktopSchemaVersion versions the desktop evidence record on its own: it
	// is a different record from the pair's and changes for different reasons.
	DesktopSchemaVersion = 1

	// DesktopGateName is the record's "gate" field. A consumer handed a file
	// from the evidence directory reads it before anything else, so a desktop
	// record is never taken for a pair's.
	DesktopGateName = "desktop"

	// DesktopSigningNamespace is the `ssh-keygen -Y sign` namespace for desktop
	// evidence. It differs from SigningNamespace so that neither record's
	// signature verifies as the other's.
	DesktopSigningNamespace = "agentmux-desktop-release"

	// DesktopIDPrefix versions the record identity.
	DesktopIDPrefix = "amx-desktop-1."

	// StatusPending is a step status and a whole-run result of the desktop gate:
	// the infrastructure the step needs is not there, so nothing was judged.
	// Like every result other than `passed`, a reader that does not know it
	// reads it as not passed.
	StatusPending = "pending"

	// ExitInfrastructureMissing is the exit status by which a desktop gate step
	// says it had nothing to work with (sysexits' EX_TEMPFAIL).
	ExitInfrastructureMissing = 75

	// PhaseToolchain is the desktop gate's first phase. The other phases reuse
	// the paired gate's names.
	PhaseToolchain = "toolchain"

	// Desktop step names the gate reads output from.
	StepDesktopToolchain = "toolchain"
	StepDesktopBundle    = "bundle"
)

// ErrDesktopGateFailed reports that the desktop gate ran and the app did not pass.
var ErrDesktopGateFailed = errors.New("desktop release gate failed")

// ErrDesktopGatePending reports that the desktop gate could not judge the app
// because what it needs to build or test one is missing. It is an error, and
// the command exits nonzero on it: missing infrastructure never counts as
// success.
var ErrDesktopGatePending = errors.New("desktop release gate is pending: nothing was judged")

// DesktopOptions configures a desktop gate run.
type DesktopOptions struct {
	// AgentMuxRoot is the checkout whose app is being released.
	AgentMuxRoot string

	// Notarized asks for a distributable release: a Developer ID signature and
	// a stapled notarization ticket. Without it the gate accepts whatever
	// sign.sh produced and records what that was.
	Notarized bool

	// Rehearsal lets the gate run over a working tree with uncommitted
	// changes. The record is marked as a rehearsal and carries a digest of the
	// tree instead of standing for a revision; it is never a release record.
	Rehearsal bool

	// SigningKey, EvidenceDir, Overwrite, Run, Now, and Progress mean what they
	// mean in Options.
	SigningKey  string
	EvidenceDir string
	Overwrite   bool
	Run         CommandRunner
	Now         func() time.Time
	Progress    io.Writer
}

// Fact is one `key: value` line a desktop gate step reported about the
// toolchain or the bundle. A list rather than a map, so the record encodes the
// same way every time.
type Fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DesktopEnvironment records the machine the gate ran on and the one that ran
// Xcode, which under remote-ui.sh is another Mac.
type DesktopEnvironment struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	OSVersion string `json:"os_version"`
	Go        string `json:"go"`
	Toolchain []Fact `json:"toolchain"`
}

// DesktopEvidence is the desktop release record.
type DesktopEvidence struct {
	SchemaVersion int    `json:"schema_version"`
	Gate          string `json:"gate"`
	RecordID      string `json:"record_id"`
	GeneratedAt   string `json:"generated_at"`
	Result        string `json:"result"`
	// PendingReason says what was missing when Result is pending.
	PendingReason string `json:"pending_reason,omitempty"`
	// Rehearsal is true when the tree had uncommitted changes and the run was
	// allowed to proceed anyway. TreeSHA256 then identifies what was built.
	Rehearsal  bool         `json:"rehearsal"`
	Notarized  bool         `json:"notarized_required"`
	Repository RepoEvidence `json:"repository"`
	TreeSHA256 string       `json:"tree_sha256,omitempty"`

	Environment DesktopEnvironment `json:"environment"`
	Bundle      []Fact             `json:"bundle"`
	Steps       []StepResult       `json:"steps"`
	Signature   Signature          `json:"signature"`
	Path        string             `json:"path,omitempty"`
}

// Passed reports whether the app passed: every step ran and passed. A pending
// record and a failed one are both not passed. A rehearsal can pass; whether a
// passing rehearsal is acceptable is the reader's question, which is why the
// field sits beside the result.
func (e *DesktopEvidence) Passed() bool { return e.Result == StatusPassed }

// DesktopPlan returns the desktop gate's ordered steps.
//
// The toolchain is asked first, because it is the step that can say there is
// nothing to judge with, and saying so should cost seconds. The bundle is
// built and inspected before the suites run for the reason the paired gate
// builds before it verifies: the thing being released exists, and is known to
// be what it claims, before anything claims to have tested it. The Release
// suite goes last because it is the longest.
//
// Nothing here installs, restarts, or tags.
func DesktopPlan(notarized bool) []Step {
	gateScript := filepath.Join("macos", "scripts", "release-gate.sh")
	toolchain := []string{gateScript, "toolchain"}
	bundle := []string{gateScript, "bundle"}
	if notarized {
		toolchain = append(toolchain, "--notarized")
		bundle = append(bundle, "--notarized")
	}
	return []Step{
		{Name: StepOSVersion, Repo: AgentMuxRepo, Phase: PhaseEnvironment, Argv: []string{"sw_vers", "-productVersion"}},
		{Name: StepDesktopToolchain, Repo: AgentMuxRepo, Phase: PhaseToolchain, Argv: toolchain},

		{Name: "build-app", Repo: AgentMuxRepo, Phase: PhaseBuild, Argv: []string{"just", "mac-app", "Release"}},
		{Name: StepDesktopBundle, Repo: AgentMuxRepo, Phase: PhaseBuild, Argv: bundle},

		{Name: "hosted-tests-debug", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "mac-test"}},
		{Name: "hosted-tests-release", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "mac-test", "Release"}},
	}
}

// DesktopRecordID is the identity of a desktop record: the revision, and for a
// rehearsal the digest of the tree that stood on it.
func DesktopRecordID(revision, treeSHA256 string) string {
	sum := sha256.Sum256([]byte(revision + "\n" + treeSHA256 + "\n"))
	return DesktopIDPrefix + hex.EncodeToString(sum[:])[:pairIDDigestLen]
}

// RunDesktop executes the desktop release gate.
//
// Like Run it returns the record whenever one could be built. The error is
// ErrDesktopGateFailed for an app that did not pass, ErrDesktopGatePending
// when there was nothing to judge with, and anything else for an invocation the
// gate could not carry out.
func RunDesktop(ctx context.Context, opts DesktopOptions) (*DesktopEvidence, error) {
	root, err := resolveRoot(opts.AgentMuxRoot, "Agent Mux")
	if err != nil {
		return nil, err
	}
	if opts.SigningKey != "" {
		key, err := filepath.Abs(opts.SigningKey)
		if err != nil {
			return nil, fmt.Errorf("release: signing key path: %w", err)
		}
		if _, err := os.Stat(key); err != nil {
			return nil, fmt.Errorf("release: signing key %s is not readable: %w", key, err)
		}
		opts.SigningKey = key
	}
	if opts.EvidenceDir == "" {
		dir, err := DefaultEvidenceDir()
		if err != nil {
			return nil, err
		}
		opts.EvidenceDir = dir
	}
	evidenceDir, err := filepath.Abs(opts.EvidenceDir)
	if err != nil {
		return nil, fmt.Errorf("release: evidence directory: %w", err)
	}
	opts.EvidenceDir = evidenceDir

	// The paired gate's machinery does the running, timing, and signing. Its
	// options carry only what those need.
	g := &gate{
		opts: Options{
			AgentMuxRoot: root,
			SigningKey:   opts.SigningKey,
			EvidenceDir:  evidenceDir,
			Namespace:    DesktopSigningNamespace,
			Overwrite:    opts.Overwrite,
		},
		run:         opts.Run,
		now:         opts.Now,
		progress:    opts.Progress,
		pendingExit: ExitInfrastructureMissing,
	}
	if g.run == nil {
		g.run = ExecRunner
	}
	if g.now == nil {
		g.now = time.Now
	}
	if g.progress == nil {
		g.progress = io.Discard
	}
	return g.executeDesktop(ctx, opts)
}

func (g *gate) executeDesktop(ctx context.Context, opts DesktopOptions) (*DesktopEvidence, error) {
	steps := DesktopPlan(opts.Notarized)
	ev := &DesktopEvidence{
		SchemaVersion: DesktopSchemaVersion,
		Gate:          DesktopGateName,
		GeneratedAt:   g.now().UTC().Format(time.RFC3339),
		Result:        StatusFailed,
		Notarized:     opts.Notarized,
		Steps:         plannedResults(steps),
		Environment: DesktopEnvironment{
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			Go:        runtime.Version(),
			Toolchain: []Fact{},
		},
		Bundle:    []Fact{},
		Signature: Signature{Namespace: DesktopSigningNamespace},
	}

	repos, err := g.readRepositories(ctx, RepoEvidence{Repo: AgentMuxRepo, Root: g.opts.AgentMuxRoot})
	ev.Repository = repos[0]
	if err != nil {
		return ev, err
	}
	if !ev.Repository.Clean {
		digest, err := g.workingTreeDigest(ctx)
		if err != nil {
			return ev, err
		}
		ev.TreeSHA256 = digest
		ev.Rehearsal = opts.Rehearsal
	}
	ev.RecordID = DesktopRecordID(ev.Repository.Revision, ev.TreeSHA256)

	if !ev.Repository.Clean && !opts.Rehearsal {
		// No record is written for the refusal: it would occupy the record
		// the same tree's rehearsal is about to write.
		g.logf("refusing: uncommitted changes in %s", AgentMuxRepo)
		return ev, fmt.Errorf("%w: uncommitted changes; commit them, or pass --rehearsal for a record that is not a release", ErrDesktopGateFailed)
	}

	failed := false
	for i, step := range steps {
		result, output := g.runStep(ctx, step)
		if result.ExitCode == ExitInfrastructureMissing {
			result.Status = StatusPending
			ev.Steps[i] = result
			ev.Result = StatusPending
			ev.PendingReason = pendingReason(output)
			g.logf("PENDING %s: %s", step.Name, ev.PendingReason)
			if err := g.publishDesktop(ctx, ev); err != nil {
				return ev, err
			}
			return ev, fmt.Errorf("%w: %s", ErrDesktopGatePending, ev.PendingReason)
		}
		ev.Steps[i] = result
		if result.Status != StatusPassed {
			failed = true
			break
		}
		switch step.Name {
		case StepOSVersion:
			ev.Environment.OSVersion = firstLine(output)
		case StepDesktopToolchain:
			ev.Environment.Toolchain = parseFacts(output)
		case StepDesktopBundle:
			ev.Bundle = parseFacts(output)
		}
	}

	if !failed {
		ev.Result = StatusPassed
	}
	if err := g.publishDesktop(ctx, ev); err != nil {
		return ev, err
	}
	if failed {
		return ev, fmt.Errorf("%w: see %s", ErrDesktopGateFailed, ev.Path)
	}
	return ev, nil
}

// publishDesktop writes the record and, when a key was given, signs it. Every
// desktop record describes something already true when it is written, so the
// ordinary order is right: the record, then the signature over those bytes.
func (g *gate) publishDesktop(ctx context.Context, ev *DesktopEvidence) error {
	if err := os.MkdirAll(g.opts.EvidenceDir, 0o700); err != nil {
		return fmt.Errorf("release: creating evidence directory: %w", err)
	}
	path := EvidencePath(g.opts.EvidenceDir, ev.RecordID)
	if !g.opts.Overwrite {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("release: evidence for %s already exists at %s; move it aside or pass --force", ev.RecordID, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("release: checking existing evidence: %w", err)
		}
	}
	ev.Path = path
	if g.opts.SigningKey != "" {
		ev.Signature = Signature{
			Signed:    true,
			Namespace: DesktopSigningNamespace,
			Path:      SignaturePath(path),
			KeyPath:   g.opts.SigningKey,
		}
	}

	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return fmt.Errorf("release: encoding evidence: %w", err)
	}
	staging := path + ".staging"
	if err := os.WriteFile(staging, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("release: writing evidence: %w", err)
	}
	if err := os.Rename(staging, path); err != nil {
		_ = os.Remove(staging)
		return fmt.Errorf("release: publishing evidence: %w", err)
	}
	g.logf("evidence %s", path)
	if !ev.Signature.Signed {
		// A signature left by an earlier run of the same record would sit
		// beside bytes it does not cover.
		if err := discardSignature(SignaturePath(path)); err != nil {
			return err
		}
		g.logf("evidence is unsigned (no signing key given)")
		return nil
	}
	if err := g.signPath(ctx, path); err != nil {
		return err
	}
	g.logf("signature %s", ev.Signature.Path)
	return nil
}

// workingTreeDigest identifies a tree with uncommitted changes: every file Git
// tracks or would track, by path and content. Ignored files (build products,
// the vendored GhosttyKit) are left out, as they are from a commit.
func (g *gate) workingTreeDigest(ctx context.Context) (string, error) {
	listing, err := g.git(ctx, g.opts.AgentMuxRoot, "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return "", fmt.Errorf("release: listing the working tree: %w", err)
	}
	paths := nonEmptyLines(listing)
	sort.Strings(paths)
	tree := sha256.New()
	for _, path := range paths {
		digest, err := sha256File(filepath.Join(g.opts.AgentMuxRoot, path))
		if errors.Is(err, os.ErrNotExist) {
			// Deleted in the working tree and still in the index.
			digest = "deleted"
		} else if err != nil {
			return "", fmt.Errorf("release: hashing %s: %w", path, err)
		}
		_, _ = fmt.Fprintf(tree, "%s\x00%s\n", path, digest)
	}
	return hex.EncodeToString(tree.Sum(nil)), nil
}

// parseFacts reads the `key: value` lines a release-gate.sh step printed.
// Anything else in the output is ignored.
func parseFacts(output string) []Fact {
	facts := []Fact{}
	for _, line := range strings.Split(output, "\n") {
		name, value, ok := strings.Cut(line, ": ")
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			continue
		}
		facts = append(facts, Fact{Name: name, Value: strings.TrimSpace(value)})
	}
	return facts
}

// pendingReason is the step's own account of what was missing: its last
// `pending:` line, or failing that its last line.
func pendingReason(output string) string {
	lines := nonEmptyLines(output)
	for i := len(lines) - 1; i >= 0; i-- {
		if reason, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "pending: "); ok {
			return reason
		}
	}
	if len(lines) > 0 {
		return strings.TrimSpace(lines[len(lines)-1])
	}
	return "a step reported missing infrastructure and did not say what"
}

// LoadDesktopEvidence reads a desktop record back, refusing one of another
// gate or another schema.
func LoadDesktopEvidence(path string) (*DesktopEvidence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ev DesktopEvidence
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("release: parsing evidence %s: %w", path, err)
	}
	if ev.Gate != DesktopGateName {
		return nil, fmt.Errorf("release: evidence %s is not a desktop record (gate %q)", path, ev.Gate)
	}
	if ev.SchemaVersion != DesktopSchemaVersion {
		return nil, fmt.Errorf("release: evidence %s has schema version %d, expected %d", path, ev.SchemaVersion, DesktopSchemaVersion)
	}
	return &ev, nil
}
