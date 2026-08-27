package release

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

// The paired release gate installs both programs, restarts the operator's real
// daemon, drives a real Kitty window, and writes annotated tags. None of that is
// acceptable in a test, and a gate whose logic is only exercised during a
// release is a gate that is discovered to be broken during a release.
//
// So the whole procedure runs here against a stand-in world: two throwaway Git
// repositories, an ephemeral SSH signing key, a fake `just`, a fake `kitty`, a
// fake `sw_vers`, and a fake ASL wrapper, all under t.TempDir() with the fakes
// staged ahead of everything else on PATH.
//
// What is real matters as much as what is faked. Git is real, so revision
// reading, cleanliness, and annotated tagging are genuinely exercised.
// `ssh-keygen` is real, so the signature this gate produces is verified with the
// same command the documentation tells an operator to run -- under the real
// release namespace, which is the part a mock would have been happy to get
// wrong. Hashing, JSON encoding, and the evidence file are real. Only the
// long-running commands whose behaviour this package does not own are replaced.

// fixture is a complete stand-in world for one gate run.
type fixture struct {
	agentMuxRoot string
	aslRoot      string
	evidenceDir  string
	fakeBin      string
	signingKey   string
	publicKey    string
}

const fakeKittyVersion = "kitty 0.44.1 created by Kovid Goyal"
const fakeOSVersion = "26.6"

// harnessCompatPayload is a canned `asl harness-compat --json` report. It is
// shaped like the real one, including a harness the policy does not fully
// endorse, so the evidence assertions cover a mixed report rather than only the
// all-green case.
const harnessCompatPayload = `{
  "schema_version": 1,
  "ok": true,
  "model_use": false,
  "counts": {"supported": 2, "unknown": 1, "incompatible": 0},
  "harnesses": [
    {"harness": "codex", "version": "0.149.1", "status": "supported"},
    {"harness": "copilot", "version": "1.0.99", "status": "unknown"},
    {"harness": "claude", "version": "2.1.231", "status": "supported"}
  ]
}`

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		agentMuxRoot: filepath.Join(base, "agentmux"),
		aslRoot:      filepath.Join(base, "scripts"),
		evidenceDir:  filepath.Join(base, "evidence"),
		fakeBin:      filepath.Join(base, "bin"),
	}
	mkdir(t, f.fakeBin)

	f.stageASLCheckout(t)
	f.stageAgentMuxCheckout(t)
	f.stageFakeCommands(t)
	f.stageSigningKey(t)

	// The fakes go first so they shadow any real `just`, `kitty`, or `sw_vers`
	// on the developer's machine; the rest of PATH is kept because `git` and
	// `ssh-keygen` are deliberately real.
	t.Setenv("PATH", f.fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f *fixture) stageASLCheckout(t *testing.T) {
	t.Helper()
	mkdir(t, filepath.Join(f.aslRoot, "agent_session_loop"))
	mkdir(t, filepath.Join(f.aslRoot, "bin", ".local", "bin"))
	mkdir(t, filepath.Join(f.aslRoot, "tests", "fixtures", "agent_mux_contract"))

	writeFile(t, filepath.Join(f.aslRoot, "agent_session_loop", "__init__.py"), "\n")
	writeFile(t, filepath.Join(f.aslRoot, "agent_session_loop", "cli.py"), "def main() -> int:\n    return 0\n")
	// A non-.py sibling that must not participate in the source-tree digest.
	writeFile(t, filepath.Join(f.aslRoot, "agent_session_loop", "notes.txt"), "not source\n")
	writeFile(t, filepath.Join(f.aslRoot, "tests", "fixtures", "agent_mux_contract", "manifest.json"), `{"current": "1"}`+"\n")

	writeScript(t, filepath.Join(f.aslRoot, "bin", ".local", "bin", "agent-session-loop"), `#!/bin/sh
case "$1" in
  harness-compat) cat <<'PAYLOAD'
`+harnessCompatPayload+`
PAYLOAD
  ;;
  *) echo "fake asl: unsupported command $1" >&2; exit 64 ;;
esac
`)
	initRepo(t, f.aslRoot)
}

func (f *fixture) stageAgentMuxCheckout(t *testing.T) {
	t.Helper()
	mkdir(t, f.agentMuxRoot)
	writeFile(t, filepath.Join(f.agentMuxRoot, "main.go"), "package main\n\nfunc main() {}\n")
	initRepo(t, f.agentMuxRoot)
}

// stageFakeCommands writes the stand-ins for the external tools the gate drives
// but does not own. The fake `just` records every recipe it was asked for, which
// is how a test asserts that a phase really did stop rather than merely that the
// record says it did.
func (f *fixture) stageFakeCommands(t *testing.T) {
	t.Helper()
	writeScript(t, filepath.Join(f.fakeBin, "sw_vers"), "#!/bin/sh\necho "+fakeOSVersion+"\n")
	writeScript(t, filepath.Join(f.fakeBin, "kitty"), "#!/bin/sh\necho '"+fakeKittyVersion+"'\n")
	writeScript(t, filepath.Join(f.fakeBin, "just"), `#!/bin/sh
recipe="$1"
if [ -n "${FAKE_JUST_LOG:-}" ]; then
    echo "$recipe" >> "$FAKE_JUST_LOG"
fi
if [ "$recipe" = "build" ]; then
    # The gate hashes this immediately after the build step, so the fake has to
    # actually produce an artifact.
    printf 'fake amx binary\n' > amx
fi
if [ -n "${FAKE_JUST_FAIL:-}" ] && [ "$recipe" = "$FAKE_JUST_FAIL" ]; then
    echo "fake just: $recipe failed on purpose"
    echo "fake just: diagnostic tail" >&2
    exit 1
fi
echo "fake just: $recipe ok"
`)
}

// stageSigningKey mints an ephemeral, passphrase-free ed25519 key plus the
// allowed-signers file a verifier needs. It never touches ~/.ssh.
func (f *fixture) stageSigningKey(t *testing.T) {
	t.Helper()
	f.signingKey = filepath.Join(t.TempDir(), "release_key")
	run(t, "", "ssh-keygen", "-t", "ed25519", "-N", "", "-C", "amx-release-test", "-f", f.signingKey)

	pub, err := os.ReadFile(f.signingKey + ".pub")
	if err != nil {
		t.Fatalf("read generated public key: %v", err)
	}
	f.publicKey = filepath.Join(filepath.Dir(f.signingKey), "allowed_signers")
	writeFile(t, f.publicKey, "release@test "+strings.TrimSpace(string(pub))+"\n")
}

func (f *fixture) options() Options {
	return Options{
		AgentMuxRoot: f.agentMuxRoot,
		ASLRoot:      f.aslRoot,
		SigningKey:   f.signingKey,
		EvidenceDir:  f.evidenceDir,
		Now:          steppingClock(),
	}
}

// steppingClock advances one second per reading, so every step in a recorded run
// lasts exactly 1000ms. A real clock would make the durations untestable; a
// frozen one would make a duration of zero indistinguishable from a duration
// that was never measured.
func steppingClock() func() time.Time {
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	ticks := 0
	return func() time.Time {
		now := base.Add(time.Duration(ticks) * time.Second)
		ticks++
		return now
	}
}

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

// TestSignedUntaggedEvidencePersistsSignedMetadata covers the run that produces
// no second write: signing without tags.
//
// The record is the release artifact, and the copy on disk is the only one that
// outlives the process. A run that signed but persisted `"signed": false` would
// leave a record that reads as a rehearsal while a detached signature verifies
// against exactly those bytes -- so the persisted metadata is checked against
// the returned result, and the signature is checked against the final bytes
// rather than against whatever the record said at some earlier moment.
//
// Both outcomes are exercised. A failed gate is signed too (the signature
// attests provenance, `result` attests approval), and it is the outcome an
// operator is most likely to be reading a record for.
func TestSignedUntaggedEvidencePersistsSignedMetadata(t *testing.T) {
	cases := []struct {
		name       string
		failRecipe string
		wantResult string
	}{
		{name: "passing", wantResult: StatusPassed},
		{name: "failing", failRecipe: "test-all", wantResult: StatusFailed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.failRecipe != "" {
				t.Setenv("FAKE_JUST_FAIL", tc.failRecipe)
			}

			opts := f.options()
			opts.Tag = false
			ev, err := Run(context.Background(), opts)
			if tc.wantResult == StatusPassed {
				if err != nil {
					t.Fatalf("gate failed: %v", err)
				}
			} else if !errors.Is(err, ErrGateFailed) {
				t.Fatalf("err = %v, want ErrGateFailed", err)
			}
			if ev.Result != tc.wantResult {
				t.Fatalf("result = %q, want %q", ev.Result, tc.wantResult)
			}
			if len(ev.Tags) != 0 {
				t.Fatalf("tags = %+v, want none: this test covers the untagged run", ev.Tags)
			}

			stored, err := LoadEvidence(EvidencePath(f.evidenceDir, ev.PairID))
			if err != nil {
				t.Fatalf("reading back the evidence record: %v", err)
			}
			if stored.Result != tc.wantResult {
				t.Errorf("stored result = %q, want %q", stored.Result, tc.wantResult)
			}

			// The persisted metadata is the claim a later reader acts on, so it
			// must say what the returned result says -- not a pre-signature
			// version of it.
			if stored.Signature != ev.Signature {
				t.Errorf("persisted signature %+v disagrees with the returned one %+v", stored.Signature, ev.Signature)
			}
			want := Signature{
				Signed:    true,
				Namespace: SigningNamespace,
				Path:      SignaturePath(stored.Path),
				KeyPath:   f.signingKey,
			}
			if stored.Signature != want {
				t.Errorf("persisted signature = %+v, want %+v", stored.Signature, want)
			}
			if _, statErr := os.Stat(stored.Signature.Path); statErr != nil {
				t.Errorf("the record names a signature that is not there: %v", statErr)
			}

			// And the signature has to cover the bytes that carry that metadata,
			// not an earlier revision of the record.
			verifySignature(t, VerifyCommand(f.publicKey, "release@test", stored.Path), stored.Path)
		})
	}
}

// TestSigningFailureLeavesNoOverstatedRecord is the other side of declaring the
// signature before it exists: when `ssh-keygen` fails, the record must not be
// left claiming a signature that was never produced.
func TestSigningFailureLeavesNoOverstatedRecord(t *testing.T) {
	f := newFixture(t)
	// A signing key that exists (newGate insists on that) but that ssh-keygen
	// cannot sign with.
	broken := filepath.Join(t.TempDir(), "broken_key")
	writeFile(t, broken, "not a private key\n")

	opts := f.options()
	opts.SigningKey = broken
	ev, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("the gate reported success with an unusable signing key")
	}
	if !strings.Contains(err.Error(), "signing evidence") {
		t.Errorf("err = %v, want the signing failure named", err)
	}

	stored, loadErr := LoadEvidence(EvidencePath(f.evidenceDir, ev.PairID))
	if loadErr != nil {
		t.Fatalf("reading back the evidence record: %v", loadErr)
	}
	if stored.Signature.Signed {
		t.Errorf("persisted signature = %+v, want the record restated as unsigned", stored.Signature)
	}
	if stored.Signature.Namespace != SigningNamespace {
		t.Errorf("namespace = %q, want %q even when unsigned", stored.Signature.Namespace, SigningNamespace)
	}
	if _, statErr := os.Stat(SignaturePath(stored.Path)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a signature file exists beside a record that could not be signed: %v", statErr)
	}
}

func TestGateRefusesToOverwriteAnExistingRecord(t *testing.T) {
	f := newFixture(t)

	first, err := Run(context.Background(), f.options())
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	stamp := modTime(t, first.Path)

	refused, err := Run(context.Background(), f.options())
	if err == nil {
		t.Fatal("a second run silently replaced the first record")
	}
	// The refused run wrote nothing, so its record must not name the first
	// release's evidence file or claim its signature.
	if refused.Path != "" || refused.Signature.Signed {
		t.Errorf("refused run reports evidence %q and signature %+v it did not write", refused.Path, refused.Signature)
	}
	if errors.Is(err, ErrGateFailed) {
		t.Errorf("err = %v, want an invocation refusal rather than a gate failure", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("err = %v, want the refusal to name the escape hatch", err)
	}
	if modTime(t, first.Path) != stamp {
		t.Error("the existing record was modified by the refused run")
	}

	forced := f.options()
	forced.Overwrite = true
	if _, err := Run(context.Background(), forced); err != nil {
		t.Fatalf("--force run failed: %v", err)
	}
}

func TestGateRefusesToRetagAReleasedPair(t *testing.T) {
	f := newFixture(t)
	opts := f.options()
	opts.Tag = true
	first, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}

	again := f.options()
	again.Tag = true
	again.Overwrite = true
	_, err = Run(context.Background(), again)
	if err == nil {
		t.Fatal("a released pair was re-tagged")
	}
	if errors.Is(err, ErrGateFailed) {
		t.Errorf("err = %v, want an invocation refusal rather than a gate failure", err)
	}
	if !strings.Contains(err.Error(), first.PairID) {
		t.Errorf("err = %v, want the existing tag named", err)
	}

	// The refusal happens before anything is written, so the first release's
	// record and signature are untouched.
	for _, root := range []string{f.agentMuxRoot, f.aslRoot} {
		tags := strings.Fields(run(t, root, "git", "tag", "-l"))
		if len(tags) != 1 {
			t.Errorf("%s has tags %v, want exactly the first release's", root, tags)
		}
	}
	verifySignature(t, VerifyCommand(f.publicKey, "release@test", first.Path), first.Path)
}

func TestGateLeavesAnUnsignedRecordWhenNoKeyIsGiven(t *testing.T) {
	f := newFixture(t)
	opts := f.options()
	opts.SigningKey = ""

	ev, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("gate failed: %v", err)
	}
	if ev.Signature.Signed {
		t.Error("a record was reported as signed with no signing key")
	}
	if ev.Signature.Namespace != SigningNamespace {
		t.Errorf("namespace = %q, want %q even when unsigned", ev.Signature.Namespace, SigningNamespace)
	}
	if _, statErr := os.Stat(SignaturePath(ev.Path)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a signature file exists beside an unsigned record: %v", statErr)
	}
}

// TestTaggingRequiresSigningKey is the invariant that keeps a rehearsal from
// manufacturing release provenance.
//
// The pair tag is what either repository alone answers "was this released" with,
// and the evidence signature is the only thing that makes that answer checkable.
// An unsigned run that could still tag would write exactly the same two refs as
// a real release, backed by a record marked `"signed": false` -- so the refusal
// has to come before the gate does anything at all, not after a twenty-minute
// procedure that has already installed both programs.
func TestTaggingRequiresSigningKey(t *testing.T) {
	f := newFixture(t)
	log := filepath.Join(t.TempDir(), "recipes.log")
	t.Setenv("FAKE_JUST_LOG", log)

	opts := f.options()
	opts.SigningKey = ""
	opts.Tag = true

	ev, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("the gate accepted a tagging run with no signing key")
	}
	if !errors.Is(err, ErrUnsignedTag) {
		t.Errorf("err = %v, want ErrUnsignedTag", err)
	}
	if errors.Is(err, ErrGateFailed) {
		t.Errorf("err = %v, want an invocation refusal rather than a gate failure", err)
	}
	if !strings.Contains(err.Error(), "signing key") {
		t.Errorf("err = %v, want the refusal to name what is missing", err)
	}
	// No record could honestly be written for a run that never ran, and the
	// caller must not be handed a half-built one to print.
	if ev != nil {
		t.Errorf("refused run returned evidence %+v", ev)
	}

	// The three things the refusal has to precede: the steps, the evidence, and
	// either repository's refs.
	if executed, readErr := os.ReadFile(log); readErr == nil {
		t.Errorf("the refused run executed recipes:\n%s", executed)
	}
	if entries, statErr := os.ReadDir(f.evidenceDir); statErr == nil && len(entries) > 0 {
		t.Errorf("the refused run wrote %d evidence file(s)", len(entries))
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("reading the evidence directory: %v", statErr)
	}
	for _, root := range []string{f.agentMuxRoot, f.aslRoot} {
		if tags := strings.Fields(run(t, root, "git", "tag", "-l")); len(tags) != 0 {
			t.Errorf("%s carries tags %v after a refused run", root, tags)
		}
	}

	// The rehearsal itself stays available: it is only the tags that a run
	// without a key may not produce.
	rehearsal := f.options()
	rehearsal.SigningKey = ""
	rehearsal.Tag = false
	unsigned, err := Run(context.Background(), rehearsal)
	if err != nil {
		t.Fatalf("unsigned untagged rehearsal failed: %v", err)
	}
	if unsigned.Signature.Signed {
		t.Error("a rehearsal reported itself as signed")
	}
	if len(unsigned.Tags) != 0 {
		t.Errorf("a rehearsal wrote tags %+v", unsigned.Tags)
	}
	if _, statErr := os.Stat(unsigned.Path); statErr != nil {
		t.Errorf("the rehearsal left no evidence record: %v", statErr)
	}
}

// TestSecondRepositoryTagFailureLeavesNoPairTag is the pair-tag transaction.
//
// The two tags are written one after the other into two independent
// repositories, so the second write is the one that can fail with the first
// already durable. What that used to leave behind was the worst possible state:
// Agent Mux carrying a release tag, ASL carrying nothing, and an evidence record
// written before either tag existed that therefore names neither. Anyone reading
// the tagged repository alone would have read a release.
//
// So the failure has to take the first tag back, and the refs plus the persisted
// record have to agree afterwards.
func TestSecondRepositoryTagFailureLeavesNoPairTag(t *testing.T) {
	f := newFixture(t)

	// An unrelated tag that predates the run, in both repositories. The rollback
	// must not touch it: it deletes what this invocation created, not what it
	// finds under a name.
	for _, root := range []string{f.agentMuxRoot, f.aslRoot} {
		run(t, root, "git", "tag", "-a", "v0.0.1", "-m", "an earlier release")
	}

	opts := f.options()
	opts.Tag = true
	var commands [][]string
	opts.Run = func(ctx context.Context, dir string, argv []string) (string, int, error) {
		commands = append(commands, argv)
		if dir == f.aslRoot && isGitTagCreate(argv) {
			return "fatal: injected second-repository tag failure", 128, nil
		}
		return ExecRunner(ctx, dir, argv)
	}

	ev, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("the gate reported success despite failing to tag the second repository")
	}
	if errors.Is(err, ErrGateFailed) {
		t.Errorf("err = %v, want a tagging failure rather than a gate result: every step passed", err)
	}
	if errors.Is(err, ErrPartialTagging) {
		t.Errorf("err = %v, want no partial state: the rollback succeeded", err)
	}
	if !strings.Contains(err.Error(), "injected second-repository tag failure") {
		t.Errorf("err = %v, want the underlying failure named", err)
	}
	if ev == nil {
		t.Fatal("no evidence was returned for a run that got as far as tagging")
	}

	// The refs are the claim, so they are checked first and directly.
	for _, root := range []string{f.agentMuxRoot, f.aslRoot} {
		tags := strings.Fields(run(t, root, "git", "tag", "-l"))
		if len(tags) != 1 || tags[0] != "v0.0.1" {
			t.Errorf("%s has tags %v, want only the pre-existing v0.0.1", root, tags)
		}
	}

	// The rollback is a compare-and-delete against the object the run created,
	// not a delete by name.
	deletes := commandsMatching(commands, func(argv []string) bool {
		return len(argv) > 1 && argv[0] == "git" && argv[1] == "update-ref"
	})
	if len(deletes) != 1 {
		t.Fatalf("update-ref invocations = %v, want exactly one rollback", deletes)
	}
	wantPrefix := []string{"git", "update-ref", "-d", "refs/tags/" + ev.PairID}
	if len(deletes[0]) != 5 || strings.Join(deletes[0][:4], " ") != strings.Join(wantPrefix, " ") {
		t.Fatalf("rollback command = %v, want %v <oid>", deletes[0], wantPrefix)
	}
	if oid := deletes[0][4]; len(oid) < 40 || strings.ContainsAny(oid, " \t") {
		t.Errorf("rollback named %q as the expected object, want the created tag's object id", oid)
	}

	// The record and the refs have to say the same thing, in memory and on disk.
	if len(ev.Tags) != 0 {
		t.Errorf("tags = %+v, want none: neither repository is tagged", ev.Tags)
	}
	if ev.Result != StatusPassed {
		t.Errorf("result = %q, want %q: the gate itself passed and the pair is simply untagged", ev.Result, StatusPassed)
	}
	stored, loadErr := LoadEvidence(EvidencePath(f.evidenceDir, ev.PairID))
	if loadErr != nil {
		t.Fatalf("reading back the evidence record: %v", loadErr)
	}
	if len(stored.Tags) != 0 {
		t.Errorf("persisted tags = %+v, want none: the record must not name a tag that is not there", stored.Tags)
	}
	if stored.Result != StatusPassed {
		t.Errorf("persisted result = %q, want %q", stored.Result, StatusPassed)
	}
	failure := stored.TagFailure
	if failure == nil {
		t.Fatal("the persisted record says nothing about the tagging attempt that failed")
	}
	if !strings.Contains(failure.Error, "injected second-repository tag failure") {
		t.Errorf("recorded cause = %q, want the underlying failure", failure.Error)
	}
	if len(failure.Stranded) != 0 {
		t.Errorf("stranded = %+v, want none", failure.Stranded)
	}
	if failure.RollbackError != "" {
		t.Errorf("rollback_error = %q, want empty for a rollback that worked", failure.RollbackError)
	}
	want := []TagEvidence{{Repo: AgentMuxRepo, Tag: ev.PairID}}
	if len(failure.RolledBack) != 1 || failure.RolledBack[0] != want[0] {
		t.Errorf("rolled_back = %+v, want %+v", failure.RolledBack, want)
	}

	// And the record on disk is the final one: the signature covers the bytes
	// that carry this account, not the pre-tagging revision of them.
	verifySignature(t, VerifyCommand(f.publicKey, "release@test", stored.Path), stored.Path)
}

// TestUnremovablePairTagIsRecordedAsRecoverablePartialState covers the case the
// transaction cannot make disappear: the second tag fails and the first one
// cannot be deleted either.
//
// Nothing here can restore the pair, so the requirement changes shape. The
// leftover ref must be impossible to read as a release, and the record must
// carry enough to clear it after the process that printed the error is gone --
// which the test proves by running the recorded recovery command and watching
// the ref go away.
func TestUnremovablePairTagIsRecordedAsRecoverablePartialState(t *testing.T) {
	f := newFixture(t)

	opts := f.options()
	opts.Tag = true
	opts.Run = func(ctx context.Context, dir string, argv []string) (string, int, error) {
		if dir == f.aslRoot && isGitTagCreate(argv) {
			return "fatal: injected second-repository tag failure", 128, nil
		}
		if len(argv) > 1 && argv[0] == "git" && argv[1] == "update-ref" {
			return "fatal: injected rollback failure", 128, nil
		}
		return ExecRunner(ctx, dir, argv)
	}

	ev, err := Run(context.Background(), opts)
	if !errors.Is(err, ErrPartialTagging) {
		t.Fatalf("err = %v, want ErrPartialTagging", err)
	}
	if !strings.Contains(err.Error(), AgentMuxRepo) || !strings.Contains(err.Error(), ev.PairID) {
		t.Errorf("err = %v, want the stranded repository and tag named", err)
	}

	// The ref really is still there -- this is the state the record has to
	// describe, not a hypothetical one.
	if tags := strings.Fields(run(t, f.agentMuxRoot, "git", "tag", "-l")); len(tags) != 1 || tags[0] != ev.PairID {
		t.Fatalf("agentmux tags = %v, want the stranded pair tag", tags)
	}
	if tags := strings.Fields(run(t, f.aslRoot, "git", "tag", "-l")); len(tags) != 0 {
		t.Fatalf("asl tags = %v, want none", tags)
	}

	stored, loadErr := LoadEvidence(EvidencePath(f.evidenceDir, ev.PairID))
	if loadErr != nil {
		t.Fatalf("reading back the evidence record: %v", loadErr)
	}
	// Unmistakable: not passed, and not carrying a tag list.
	if stored.Result != StatusPartial {
		t.Errorf("persisted result = %q, want %q", stored.Result, StatusPartial)
	}
	if stored.Passed() {
		t.Error("a half-tagged pair reads as a passed release")
	}
	if len(stored.Tags) != 0 {
		t.Errorf("persisted tags = %+v, want none: a pair tagged on one side is not a tagged pair", stored.Tags)
	}
	failure := stored.TagFailure
	if failure == nil {
		t.Fatal("the persisted record does not describe the partial state it left behind")
	}
	want := TagEvidence{Repo: AgentMuxRepo, Tag: ev.PairID}
	if len(failure.Stranded) != 1 || failure.Stranded[0] != want {
		t.Fatalf("stranded = %+v, want %+v", failure.Stranded, want)
	}
	if !strings.Contains(failure.RollbackError, "injected rollback failure") {
		t.Errorf("rollback_error = %q, want the removal failure named", failure.RollbackError)
	}
	if len(failure.Recovery) != 1 {
		t.Fatalf("recovery = %v, want one command per stranded tag", failure.Recovery)
	}
	verifySignature(t, VerifyCommand(f.publicKey, "release@test", stored.Path), stored.Path)

	// The recorded command is the recovery, so it is run rather than pattern
	// matched. A recovery instruction that does not work is worse than none.
	run(t, "", strings.Fields(failure.Recovery[0])...)
	if tags := strings.Fields(run(t, f.agentMuxRoot, "git", "tag", "-l")); len(tags) != 0 {
		t.Errorf("agentmux tags = %v after the recorded recovery, want none", tags)
	}
}

// isGitTagCreate identifies the annotated-tag write, and only that: the gate
// also runs `git rev-parse refs/tags/...` and `git tag -l` against the same
// repositories, and an injected failure that caught those would be testing
// something else.
func isGitTagCreate(argv []string) bool {
	return len(argv) > 2 && argv[0] == "git" && argv[1] == "tag" && argv[2] == "-a"
}

func commandsMatching(commands [][]string, pred func([]string) bool) [][]string {
	var matched [][]string
	for _, argv := range commands {
		if pred(argv) {
			matched = append(matched, argv)
		}
	}
	return matched
}

func TestGateRefusesADirectoryThatIsNotAnASLCheckout(t *testing.T) {
	f := newFixture(t)
	opts := f.options()
	opts.ASLRoot = f.agentMuxRoot // a real directory, and the wrong one

	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("the gate accepted a directory with no ASL wrapper")
	} else if !strings.Contains(err.Error(), "agent-session-loop checkout") {
		t.Errorf("err = %v, want the failure to say what was missing", err)
	}
}

func TestPairIDFoldsBothRevisionsInAFixedOrder(t *testing.T) {
	amx, asl := "a1b2c3", "d4e5f6"
	id := PairID(amx, asl, aslcontract.ContractSetID)

	if !strings.HasPrefix(id, PairIDPrefix) {
		t.Errorf("pair id %q lacks the %q prefix", id, PairIDPrefix)
	}
	if len(id) != len(PairIDPrefix)+pairIDDigestLen {
		t.Errorf("pair id %q has an unexpected width", id)
	}
	if PairID(amx, asl, aslcontract.ContractSetID) != id {
		t.Error("pair id is not deterministic for identical inputs")
	}
	// Order participates: releasing revision X of Agent Mux against Y of ASL is
	// a different pair from the reverse, and the ID must say so.
	if PairID(asl, amx, aslcontract.ContractSetID) == id {
		t.Error("swapping the revisions produced the same pair id")
	}
	if PairID(amx, asl, "asl-amx-1.0000000000000000") == id {
		t.Error("changing the contract set produced the same pair id")
	}
}

func TestVerifyCommandNamesTheReleaseNamespace(t *testing.T) {
	argv := VerifyCommand("/allowed", "me", "/evidence/pair.json")
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-n "+SigningNamespace) {
		t.Errorf("verify command %q does not name the release namespace", joined)
	}
	if !strings.Contains(joined, "-s /evidence/pair.json.sig") {
		t.Errorf("verify command %q does not point at the detached signature", joined)
	}
}

// --- helpers -------------------------------------------------------------

func verifySignature(t *testing.T, argv []string, evidencePath string) {
	t.Helper()
	data, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("read evidence for verification: %v", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`%s` failed: %v\n%s", strings.Join(argv, " "), err, out)
	}
}

func artifactHash(t *testing.T, ev *Evidence, name string) string {
	t.Helper()
	for _, artifact := range ev.Artifacts {
		if artifact.Name == name {
			return artifact.SHA256
		}
	}
	t.Fatalf("no artifact named %q in %+v", name, ev.Artifacts)
	return ""
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	sum, err := sha256File(path)
	if err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return sum
}

func revisionOf(t *testing.T, root string) string {
	t.Helper()
	return strings.TrimSpace(run(t, root, "git", "rev-parse", "HEAD"))
}

func modTime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.ModTime()
}

// initRepo makes a real Git repository with one commit. The identity and
// signing settings are set locally so the test never depends on -- or is broken
// by -- the developer's global Git configuration.
func initRepo(t *testing.T, root string) {
	t.Helper()
	run(t, root, "git", "init", "--initial-branch=main")
	run(t, root, "git", "config", "user.email", "release@test")
	run(t, root, "git", "config", "user.name", "Release Test")
	run(t, root, "git", "config", "commit.gpgsign", "false")
	run(t, root, "git", "config", "tag.gpgsign", "false")
	run(t, root, "git", "add", "-A")
	run(t, root, "git", "commit", "-m", "fixture")
	// The gate must ignore the artifact it builds; leaving it untracked would
	// make every run dirty.
	writeFile(t, filepath.Join(root, ".gitignore"), "amx\n")
	run(t, root, "git", "add", "-A")
	run(t, root, "git", "commit", "-m", "ignore build output")
}

func run(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`%s` in %s failed: %v\n%s", strings.Join(argv, " "), dir, err, out)
	}
	return string(out)
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeScript(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write script %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestEvidenceEncodesAsOrderedJSON guards the property the record's consumers
// depend on: every collection is a slice, so two encodings of the same run are
// byte-identical. A map anywhere in Evidence would encode in Go's randomized
// order and quietly make signed records unreproducible.
func TestEvidenceEncodesAsOrderedJSON(t *testing.T) {
	ev := &Evidence{
		SchemaVersion: SchemaVersion,
		PairID:        "amx-asl-1.0000000000000000",
		Result:        StatusPassed,
		Repositories:  []RepoEvidence{{Repo: AgentMuxRepo}, {Repo: ASLRepo}},
		Artifacts:     []Artifact{{Name: "amx"}},
		Environment:   Environment{Harnesses: []HarnessVersion{{Harness: "codex"}}},
		Steps:         []StepResult{{Name: "amx-check"}},
	}
	first, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := json.MarshalIndent(ev, "", "  ")
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("evidence encoding is not stable across encodes:\n%s\n---\n%s", first, again)
		}
	}
}
