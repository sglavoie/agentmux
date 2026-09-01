package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

func TestGateRecordsSignedEvidenceForACleanPair(t *testing.T) {
	f := newFixture(t)
	log := filepath.Join(t.TempDir(), "recipes.log")
	t.Setenv("FAKE_JUST_LOG", log)

	opts := f.options()
	opts.Tag = true
	ev, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("gate failed: %v", err)
	}

	if !ev.Passed() {
		t.Fatalf("result = %q, want %q", ev.Result, StatusPassed)
	}
	for _, step := range ev.Steps {
		if step.Status != StatusPassed {
			t.Errorf("step %s = %s (exit %d): %s", step.Name, step.Status, step.ExitCode, step.OutputTail)
		}
		if step.DurationMS != 1000 {
			t.Errorf("step %s duration = %dms, want 1000ms from the injected clock", step.Name, step.DurationMS)
		}
	}
	if ev.GeneratedAt != "2026-08-26T12:00:00Z" {
		t.Errorf("generated_at = %q, want the injected clock's first reading", ev.GeneratedAt)
	}

	// Identity: the pair ID is a pure function of the two revisions and the
	// contract set, so the test can recompute it rather than accept whatever the
	// gate produced.
	wantPair := PairID(revisionOf(t, f.agentMuxRoot), revisionOf(t, f.aslRoot), aslcontract.ContractSetID)
	if ev.PairID != wantPair {
		t.Errorf("pair_id = %q, want %q", ev.PairID, wantPair)
	}
	if ev.Contract.SetID != aslcontract.ContractSetID {
		t.Errorf("contract set = %q, want the vendored constant %q", ev.Contract.SetID, aslcontract.ContractSetID)
	}
	if ev.Contract.ManifestSHA256 != sha256Of(t, filepath.Join(f.aslRoot, aslManifestPath)) {
		t.Errorf("manifest hash %q does not match the manifest on disk", ev.Contract.ManifestSHA256)
	}

	// Artifacts: three hashes, and the tree digest must ignore the non-source
	// sibling beside the package.
	if len(ev.Artifacts) != 3 {
		t.Fatalf("artifacts = %d, want 3: %+v", len(ev.Artifacts), ev.Artifacts)
	}
	for _, artifact := range ev.Artifacts {
		if len(artifact.SHA256) != 64 {
			t.Errorf("artifact %s has hash %q, want a sha256 hex digest", artifact.Name, artifact.SHA256)
		}
	}
	treeBefore := artifactHash(t, ev, "agent_session_loop")
	writeFile(t, filepath.Join(f.aslRoot, "agent_session_loop", "notes.txt"), "changed\n")
	if got, err := sha256Tree(filepath.Join(f.aslRoot, "agent_session_loop"), ".py"); err != nil || got != treeBefore {
		t.Errorf("source-tree digest changed for a non-source file: %q vs %q (err %v)", got, treeBefore, err)
	}

	// Environment: read from the probes, not assumed.
	if ev.Environment.OSVersion != fakeOSVersion {
		t.Errorf("os_version = %q, want %q", ev.Environment.OSVersion, fakeOSVersion)
	}
	if ev.Environment.Kitty != fakeKittyVersion {
		t.Errorf("kitty = %q, want %q", ev.Environment.Kitty, fakeKittyVersion)
	}
	wantHarnesses := []HarnessVersion{
		{Harness: "claude", Version: "2.1.231", Status: "supported"},
		{Harness: "codex", Version: "0.149.1", Status: "supported"},
		{Harness: "copilot", Version: "1.0.99", Status: "unknown"},
	}
	if len(ev.Environment.Harnesses) != len(wantHarnesses) {
		t.Fatalf("harnesses = %+v, want %+v", ev.Environment.Harnesses, wantHarnesses)
	}
	for i, want := range wantHarnesses {
		if ev.Environment.Harnesses[i] != want {
			t.Errorf("harness %d = %+v, want %+v (report rows are sorted by name)", i, ev.Environment.Harnesses[i], want)
		}
	}

	// The record on disk is the artifact, so it is read back rather than trusted
	// from the in-memory value.
	stored, err := LoadEvidence(EvidencePath(f.evidenceDir, ev.PairID))
	if err != nil {
		t.Fatalf("reading back the evidence record: %v", err)
	}
	if stored.PairID != ev.PairID || stored.Result != StatusPassed {
		t.Errorf("stored record disagrees with the returned one: %+v", stored)
	}

	// The signature is verified with the exact command the documentation gives
	// an operator, which is what keeps the namespace honest.
	verify := VerifyCommand(f.publicKey, "release@test", stored.Path)
	verifySignature(t, verify, stored.Path)

	if !stored.Signature.Signed || stored.Signature.Namespace != SigningNamespace {
		t.Errorf("signature = %+v, want a signature under %q", stored.Signature, SigningNamespace)
	}

	// Tags: written in both repositories, annotated, and carrying the identity a
	// reader needs to find the record.
	if len(stored.Tags) != 2 {
		t.Fatalf("tags = %+v, want one per repository", stored.Tags)
	}
	for _, root := range []string{f.agentMuxRoot, f.aslRoot} {
		message := run(t, root, "git", "tag", "-l", "--format=%(contents)", ev.PairID)
		if strings.TrimSpace(message) == "" {
			t.Fatalf("%s has no annotated tag %s", root, ev.PairID)
		}
		for _, want := range []string{
			"pair_id: " + ev.PairID,
			"manifest_sha256: " + ev.Contract.ManifestSHA256,
			"contract_set: " + aslcontract.ContractSetID,
		} {
			if !strings.Contains(message, want) {
				t.Errorf("%s tag message missing %q:\n%s", root, want, message)
			}
		}
	}

	// Nothing outside the fixture was touched: every recipe ran in one of the
	// two throwaway checkouts, and the record's own path is inside the
	// throwaway evidence directory.
	if !strings.HasPrefix(stored.Path, f.evidenceDir) {
		t.Errorf("evidence written to %s, outside the fixture's evidence directory", stored.Path)
	}
	recipes := strings.Fields(readFile(t, log))
	if len(recipes) == 0 {
		t.Fatal("the fake just recorded no recipes, so the run proved nothing")
	}
}

// TestGateRunsPhasesInTheDocumentedOrder asserts the order the evidence claims,
// because the order is the gate's argument: verifying after installing, or
// smoking before restarting, would produce a record whose steps did not
// describe the state they ran against.
func TestGateRunsPhasesInTheDocumentedOrder(t *testing.T) {
	steps := Plan("/amx", "/asl")

	var phases []string
	for _, step := range steps {
		if len(phases) == 0 || phases[len(phases)-1] != step.Phase {
			phases = append(phases, step.Phase)
		}
	}
	want := []string{PhaseEnvironment, PhaseBuild, PhaseVerify, PhaseInstall, PhaseSmoke}
	if strings.Join(phases, ",") != strings.Join(want, ",") {
		t.Errorf("phase order = %v, want %v (each phase must be contiguous)", phases, want)
	}

	order := map[string]int{}
	for i, step := range steps {
		if _, duplicate := order[step.Name]; duplicate {
			t.Fatalf("step %q appears twice", step.Name)
		}
		order[step.Name] = i
	}
	// ASL installs before Agent Mux: the Agent Mux install restarts the daemon,
	// and a daemon restarted against a stale ASL says nothing about the pair.
	if order["asl-install"] > order["amx-install-restart"] {
		t.Error("Agent Mux installs before ASL; the documented order is ASL first")
	}
	if order["asl-install-verify"] < order["asl-install"] {
		t.Error("the installed-wrapper verification runs before ASL is installed")
	}
	if order["cockpit-smoke"] != len(steps)-1 {
		t.Error("the real-Kitty cockpit smoke must be last, against the installation just produced")
	}
	if order[StepBuildAgentMux] > order["amx-check"] {
		t.Error("the artifact must be built before anything claims to have tested it")
	}
}

func TestGateRefusesADirtyWorkingTree(t *testing.T) {
	f := newFixture(t)
	log := filepath.Join(t.TempDir(), "recipes.log")
	t.Setenv("FAKE_JUST_LOG", log)
	writeFile(t, filepath.Join(f.aslRoot, "agent_session_loop", "scratch.py"), "# uncommitted\n")

	opts := f.options()
	opts.Tag = true
	ev, err := Run(context.Background(), opts)
	if !errors.Is(err, ErrGateFailed) {
		t.Fatalf("err = %v, want ErrGateFailed", err)
	}
	if ev.Result != StatusFailed {
		t.Errorf("result = %q, want %q", ev.Result, StatusFailed)
	}

	// A release built from an unrecorded working tree is unreproducible, so
	// nothing at all may run first.
	for _, step := range ev.Steps {
		if step.Status != StatusNotRun {
			t.Errorf("step %s ran (%s) despite a dirty tree", step.Name, step.Status)
		}
	}
	if _, statErr := os.Stat(log); statErr == nil {
		t.Error("the fake just was invoked despite a dirty tree")
	}

	var dirty *RepoEvidence
	for i := range ev.Repositories {
		if !ev.Repositories[i].Clean {
			dirty = &ev.Repositories[i]
		}
	}
	if dirty == nil {
		t.Fatal("no repository was recorded as dirty")
	}
	if dirty.Repo != ASLRepo {
		t.Errorf("dirty repo = %q, want %q", dirty.Repo, ASLRepo)
	}
	if len(dirty.Dirty) == 0 || !strings.Contains(strings.Join(dirty.Dirty, "\n"), "scratch.py") {
		t.Errorf("dirty paths = %v, want the uncommitted file named", dirty.Dirty)
	}

	// The refusal is still evidence, and it is still on disk.
	if _, statErr := os.Stat(EvidencePath(f.evidenceDir, ev.PairID)); statErr != nil {
		t.Errorf("no record was written for the refusal: %v", statErr)
	}
	if len(ev.Tags) != 0 {
		t.Errorf("tags = %+v, want none for a failed gate", ev.Tags)
	}
	for _, root := range []string{f.agentMuxRoot, f.aslRoot} {
		if tags := strings.TrimSpace(run(t, root, "git", "tag", "-l")); tags != "" {
			t.Errorf("%s was tagged despite the failure: %q", root, tags)
		}
	}
}

func TestGateStopsAtTheFirstFailingStep(t *testing.T) {
	f := newFixture(t)
	t.Setenv("FAKE_JUST_FAIL", "test-all")

	opts := f.options()
	opts.Tag = true
	ev, err := Run(context.Background(), opts)
	if !errors.Is(err, ErrGateFailed) {
		t.Fatalf("err = %v, want ErrGateFailed", err)
	}

	byName := map[string]StepResult{}
	for _, step := range ev.Steps {
		byName[step.Name] = step
	}
	failed := byName["asl-tests"]
	if failed.Status != StatusFailed {
		t.Fatalf("asl-tests = %+v, want a recorded failure", failed)
	}
	if failed.ExitCode != 1 {
		t.Errorf("asl-tests exit code = %d, want 1", failed.ExitCode)
	}
	// A failing step without its output is not diagnosable, and a passing step
	// with its output makes two identical runs differ for no reader's benefit.
	if !strings.Contains(failed.OutputTail, "failed on purpose") {
		t.Errorf("asl-tests output tail = %q, want the command's output", failed.OutputTail)
	}
	if tail := byName["amx-check"].OutputTail; tail != "" {
		t.Errorf("passing step recorded an output tail: %q", tail)
	}

	// Everything after the failure is recorded as not-run rather than omitted, so
	// a truncated gate cannot read as a complete one.
	for _, name := range []string{"asl-lint", "paired-integration", "asl-install", "amx-install-restart", "cockpit-smoke"} {
		if got := byName[name].Status; got != StatusNotRun {
			t.Errorf("step %s = %s, want %s after an earlier failure", name, got, StatusNotRun)
		}
	}
	if got := byName["amx-check"].Status; got != StatusPassed {
		t.Errorf("step amx-check = %s, want %s", got, StatusPassed)
	}

	if len(ev.Tags) != 0 {
		t.Errorf("tags = %+v, want none for a failed gate", ev.Tags)
	}
	// A failed record is still signed: the signature attests that this gate
	// produced this record, and `result` is what says whether it passed.
	if !ev.Signature.Signed {
		t.Error("a failed record was left unsigned")
	}
	verifySignature(t, VerifyCommand(f.publicKey, "release@test", ev.Path), ev.Path)
}
