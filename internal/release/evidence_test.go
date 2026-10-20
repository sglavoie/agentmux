package release

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

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
	t.Parallel()

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
				f.setenv("FAKE_JUST_FAIL", tc.failRecipe)
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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	f := newFixture(t)
	log := filepath.Join(t.TempDir(), "recipes.log")
	f.setenv("FAKE_JUST_LOG", log)

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

func TestGateRefusesADirectoryThatIsNotAnASLCheckout(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	argv := VerifyCommand("/allowed", "me", "/evidence/pair.json")
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-n "+SigningNamespace) {
		t.Errorf("verify command %q does not name the release namespace", joined)
	}
	if !strings.Contains(joined, "-s /evidence/pair.json.sig") {
		t.Errorf("verify command %q does not point at the detached signature", joined)
	}
}

// TestEvidenceEncodesAsOrderedJSON guards the property the record's consumers
// depend on: every collection is a slice, so two encodings of the same run are
// byte-identical. A map anywhere in Evidence would encode in Go's randomized
// order and quietly make signed records unreproducible.
func TestEvidenceEncodesAsOrderedJSON(t *testing.T) {
	t.Parallel()

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

// The fixture runs the gate through its own CommandRunner, so ExecRunner's
// contract is held here: a command that ran and failed is a step result with
// its exit status, and only a command that could not start is an error.
func TestExecRunnerSeparatesAFailedCommandFromOneThatCouldNotStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	output, code, err := ExecRunner(context.Background(), dir, []string{"sh", "-c", "pwd -P; echo oops >&2; exit 3"})
	if err != nil || code != 3 || !strings.Contains(output, "oops") {
		t.Fatalf("failed command = (%q, %d, %v), want its output and exit 3", output, code, err)
	}
	if resolved, _ := filepath.EvalSymlinks(dir); !strings.Contains(output, resolved) {
		t.Fatalf("output %q does not show the command ran in %s", output, resolved)
	}
	if _, code, err := ExecRunner(context.Background(), dir, []string{filepath.Join(dir, "missing")}); err == nil || code != -1 {
		t.Fatalf("missing executable = (%d, %v), want -1 and an error", code, err)
	}
	if _, _, err := ExecRunner(context.Background(), dir, nil); err == nil {
		t.Fatal("empty argv was accepted")
	}
}
