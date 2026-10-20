package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFinalEvidenceSigningFailureRollsBackPairTags covers the window that opens
// only after the transaction above has already succeeded.
//
// Both tags are written, and the run then has to make the release claim durable:
// a record naming those refs, and the signature over exactly those bytes. The
// signature is the half this test breaks. Two durable release tags are left
// standing while the only record for the pair is the provisional one written
// before them -- and the tags are what a reader of either repository alone sees.
//
// So the last publication is part of the transaction, not a postscript to it,
// and a failure anywhere inside it costs the refs. This test fails exactly one
// thing -- the signature over the tagged bytes -- and then asserts the five
// statements about that run all agree: the refs, the persisted record down to
// the result field a consumer reads on its own, the detached signature, the
// returned error, and what the operator was told while it happened. A run that
// returns an error and retracts both tags may not persist a record that says it
// passed.
func TestFinalEvidenceSigningFailureRollsBackPairTags(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	// A tag that predates the run, in both repositories. The rollback must
	// remove what this invocation created and nothing else.
	for _, root := range []string{f.agentMuxRoot, f.aslRoot} {
		run(t, root, "git", "tag", "-a", "v0.0.1", "-m", "an earlier release")
	}

	// Progress is where the operator reads a run as it happens: the release-gate
	// command wires it to its own stderr, so these lines are the CLI's output.
	var progress strings.Builder

	opts := f.options()
	opts.Tag = true
	opts.Progress = &progress
	var commands [][]string
	signings := 0
	opts.Run = func(ctx context.Context, dir string, argv []string) (string, int, error) {
		commands = append(commands, argv)
		// Only the first signature fails -- the one over the tagged bytes. A
		// tagging run signs nothing before its refs exist, so this is the first
		// signing invocation of the run, and the one over the final account
		// afterwards is produced by the real ssh-keygen. That isolates the
		// window the fix is about instead of disabling signing for the whole run.
		if isSSHKeygenSign(argv) {
			signings++
			if signings == 1 {
				return "sign_and_send_pubkey: signing failed for ED25519 key", 255, nil
			}
		}
		return f.exec(ctx, dir, argv)
	}

	ev, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("the gate reported success despite failing to sign the record that vouches for the tags")
	}
	if ev == nil {
		t.Fatal("no evidence was returned for a run that got as far as tagging")
	}
	// The returned error names what broke and what it cost, and it is neither a
	// gate result (every step passed) nor a partial state (the refs are gone).
	if errors.Is(err, ErrGateFailed) {
		t.Errorf("err = %v, want a publication failure rather than a gate result: every step passed", err)
	}
	if errors.Is(err, ErrPartialTagging) {
		t.Errorf("err = %v, want no partial state: both rollbacks succeeded", err)
	}
	for _, want := range []string{"signing evidence", "vouches for the pair tags", "no repository was left carrying the pair tag"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to name %q", err, want)
		}
	}

	// The refs are the claim, so they are checked directly. Only the tag that
	// predates the run survives, in both repositories.
	for _, root := range []string{f.agentMuxRoot, f.aslRoot} {
		tags := strings.Fields(run(t, root, "git", "tag", "-l"))
		if len(tags) != 1 || tags[0] != "v0.0.1" {
			t.Errorf("%s has tags %v, want only the pre-existing v0.0.1", root, tags)
		}
	}

	// Both refs really were created and then removed by object, not by name:
	// two annotated-tag writes and two compare-and-deletes.
	if creates := commandsMatching(commands, isGitTagCreate); len(creates) != 2 {
		t.Fatalf("annotated tag writes = %v, want one per repository", creates)
	}
	deletes := commandsMatching(commands, isGitUpdateRef)
	if len(deletes) != 2 {
		t.Fatalf("update-ref invocations = %v, want one rollback per repository", deletes)
	}
	for _, argv := range deletes {
		wantPrefix := []string{"git", "update-ref", "-d", "refs/tags/" + ev.PairID}
		if len(argv) != 5 || strings.Join(argv[:4], " ") != strings.Join(wantPrefix, " ") {
			t.Fatalf("rollback command = %v, want %v <oid>", argv, wantPrefix)
		}
		if oid := argv[4]; len(oid) < 40 || strings.ContainsAny(oid, " \t") {
			t.Errorf("rollback named %q as the expected object, want the created tag's object id", oid)
		}
	}

	// The persisted record is what a later reader acts on, so it is read back
	// rather than trusted from memory -- and it must agree with the refs.
	stored, loadErr := LoadEvidence(EvidencePath(f.evidenceDir, ev.PairID))
	if loadErr != nil {
		t.Fatalf("reading back the evidence record: %v", loadErr)
	}
	if len(ev.Tags) != 0 || len(stored.Tags) != 0 {
		t.Errorf("tags = %+v (persisted %+v), want none: neither repository is tagged", ev.Tags, stored.Tags)
	}
	// The result is the field a consumer reads without opening anything else,
	// so it has to carry the same outcome the error does. `passed` here would
	// hand a script a released pair that exists in neither repository; `partial`
	// would send an operator looking for a ref to clear that is not there.
	if stored.Result != StatusNotReleased || ev.Result != stored.Result {
		t.Errorf("result = %q (persisted %q), want %q: the tags this run wrote were taken back",
			ev.Result, stored.Result, StatusNotReleased)
	}
	if ev.Passed() || stored.Passed() {
		t.Error("a run that retracted both pair tags reads as a passed release")
	}
	failure := stored.TagFailure
	if failure == nil {
		t.Fatal("the persisted record says nothing about the tagging attempt it took back")
	}
	if !strings.Contains(failure.Error, "signing evidence") || !strings.Contains(failure.Error, "vouches for the pair tags") {
		t.Errorf("recorded cause = %q, want the unpublishable record named as the reason", failure.Error)
	}
	if len(failure.Stranded) != 0 || failure.RollbackError != "" {
		t.Errorf("stranded = %+v, rollback_error = %q, want neither for rollbacks that worked", failure.Stranded, failure.RollbackError)
	}
	// Reverse order: the repository tagged last is released first.
	wantRolledBack := []TagEvidence{{Repo: ASLRepo, Tag: ev.PairID}, {Repo: AgentMuxRepo, Tag: ev.PairID}}
	if len(failure.RolledBack) != 2 || failure.RolledBack[0] != wantRolledBack[0] || failure.RolledBack[1] != wantRolledBack[1] {
		t.Errorf("rolled_back = %+v, want %+v", failure.RolledBack, wantRolledBack)
	}

	// The detached signature state agrees too: the record declares a signature,
	// it is there, and it covers the final bytes rather than the pre-tag ones
	// the failed attempt superseded.
	if signings != 2 {
		t.Fatalf("ssh-keygen sign invocations = %d, want the failed signature over the tagged bytes and one over the final account", signings)
	}
	if stored.Signature != ev.Signature {
		t.Errorf("persisted signature %+v disagrees with the returned one %+v", stored.Signature, ev.Signature)
	}
	if !stored.Signature.Signed {
		t.Fatalf("persisted signature = %+v, want the final account signed: only the middle attempt failed", stored.Signature)
	}
	if _, statErr := os.Stat(stored.Signature.Path); statErr != nil {
		t.Fatalf("the record names a signature that is not there: %v", statErr)
	}
	verifySignature(t, VerifyCommand(f.publicKey, "release@test", stored.Path), stored.Path)

	// And the operator watching the run was told the same story the record
	// tells, in the order it happened.
	summary := progress.String()
	for _, want := range []string{
		"tagged " + AgentMuxRepo + " " + ev.PairID,
		"tagged " + ASLRepo + " " + ev.PairID,
		"rolled back " + ASLRepo + " " + ev.PairID,
		"rolled back " + AgentMuxRepo + " " + ev.PairID,
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("progress output does not report %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "STRANDED") {
		t.Errorf("progress output reports a stranded ref for a run that left none:\n%s", summary)
	}
}

// TestFinalEvidenceWriteFailureRollsBackPairTags is the other half of the same
// window: the publication that would have made the release claim durable never
// reaches disk.
//
// Both refs exist and the only record for the pair is the provisional one this
// run wrote before tagging. The refs therefore go back, and the interesting
// question is what the file says while that is happening and afterwards. It is
// asked twice, because the compensation has two independent things that can
// fail and the answer has to hold when both do.
//
// The first subtest takes the evidence directory away, which is what ordinary
// publication needs: it writes a sibling staging file and renames it. The
// compensating account cannot be published either, so the record this run
// already owns is upgraded in place instead and ends up naming the refs it
// retracted.
//
// The second takes the record away too, and is the one the design has to be
// right for rather than merely recover from. Nothing can be written through the
// directory and nothing can be written to the file, so every path back to disk
// is closed and what outlives the process is exactly the bytes that were there
// before the first ref existed. Those bytes are the whole point: they say a
// tagging attempt was under way, they are not a release claim, no consumer
// reading `result` sees `passed`, and there is no signature beside them for
// anyone to verify. A compensation that only works when the medium it writes to
// recovers is not a guarantee; this asserts the guarantee that does not need one.
//
// Both cases assert every statement about the run agrees: both repositories'
// refs, the durable record and what Evidence.Passed() makes of it, the returned
// record, the signature on disk, the returned error, and what the operator was
// told while it happened.
func TestFinalEvidenceWriteFailureRollsBackPairTags(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("the injected write failures are filesystem permissions, which root ignores")
	}

	t.Run("the superseded record can still be rewritten in place", func(t *testing.T) {
		outcome := runUnpublishableTagging(t, false)
		ev, stored := outcome.ev, outcome.stored

		// The in-place rewrite landed, so the durable record is the account of
		// the retraction rather than the provisional bytes it replaced.
		if stored.Result != StatusNotReleased || ev.Result != stored.Result {
			t.Errorf("result = %q (persisted %q), want %q: the tags this run wrote were taken back",
				ev.Result, stored.Result, StatusNotReleased)
		}
		failure := stored.TagFailure
		if failure == nil {
			t.Fatal("the persisted record says nothing about the tagging attempt it took back")
		}
		if !strings.Contains(failure.Error, "writing evidence") || !strings.Contains(failure.Error, "vouches for the pair tags") {
			t.Errorf("recorded cause = %q, want the unpublishable record named as the reason", failure.Error)
		}
		if len(failure.Stranded) != 0 || failure.RollbackError != "" {
			t.Errorf("stranded = %+v, rollback_error = %q, want neither for rollbacks that worked", failure.Stranded, failure.RollbackError)
		}
		// Reverse order: the repository tagged last is released first.
		wantRolledBack := []TagEvidence{{Repo: ASLRepo, Tag: ev.PairID}, {Repo: AgentMuxRepo, Tag: ev.PairID}}
		if len(failure.RolledBack) != 2 || failure.RolledBack[0] != wantRolledBack[0] || failure.RolledBack[1] != wantRolledBack[1] {
			t.Errorf("rolled_back = %+v, want %+v", failure.RolledBack, wantRolledBack)
		}
		if !strings.Contains(outcome.progress, "retracted superseded evidence "+stored.Path) {
			t.Errorf("progress output does not report the in-place retraction:\n%s", outcome.progress)
		}
	})

	t.Run("the superseded record cannot be rewritten either", func(t *testing.T) {
		outcome := runUnpublishableTagging(t, true)
		ev, stored := outcome.ev, outcome.stored

		// Nothing could be written anywhere, so the durable record is the one
		// that was there before the first ref existed. It has to be safe on its
		// own terms, because on this path it is the only thing that is.
		if stored.Result != StatusTagging {
			t.Errorf("persisted result = %q, want %q: no write of any kind could land after tagging",
				stored.Result, StatusTagging)
		}
		if stored.TagFailure != nil {
			t.Errorf("tag_failure = %+v, want none: the provisional record predates the tags it never learned about", stored.TagFailure)
		}
		// The returned record still carries the full account. Only the file is
		// impoverished, and the command's own summary is rendered from this.
		if ev.TagFailure == nil || !strings.Contains(ev.TagFailure.Error, "writing evidence") {
			t.Errorf("returned tag_failure = %+v, want the publication failure named", ev.TagFailure)
		}
		// The operator is told the retraction could not be written, rather than
		// being left to infer it from a record that says less than the run knew.
		if !strings.Contains(outcome.err.Error(), "retracting the superseded evidence") {
			t.Errorf("err = %v, want the failed in-place retraction named too", outcome.err)
		}
		if strings.Contains(outcome.progress, "retracted superseded evidence") {
			t.Errorf("progress output claims a retraction that never reached disk:\n%s", outcome.progress)
		}
	})
}

// unpublishableTagging is what one run of the scenario above left behind.
type unpublishableTagging struct {
	f        *fixture
	ev       *Evidence
	err      error
	stored   *Evidence
	progress string
}

// runUnpublishableTagging drives a tagging run whose final publication cannot
// land, and asserts everything the two cases have in common.
//
// sealRecord decides how far the injected failure reaches: the evidence
// directory is always taken away the moment both refs exist, and the record
// already in it is taken away too when the caller asks for the case where
// in-place retraction is impossible as well. Both are filesystem permissions
// rather than a stubbed writer, so the failure the gate meets is the one a real
// read-only medium produces.
//
// The shared assertions are the invariant the finding is about: whatever else
// happened, no ref survives, and the record that outlives the process is neither
// readable as a release nor backed by a signature that verifies.
func runUnpublishableTagging(t *testing.T, sealRecord bool) unpublishableTagging {
	t.Helper()
	f := newFixture(t)

	// Progress is the operator's view of the run: release-gate wires it to its
	// own stderr, so these lines are CLI output.
	var progress strings.Builder

	opts := f.options()
	opts.Tag = true
	opts.Progress = &progress
	sealed := false
	opts.Run = func(ctx context.Context, dir string, argv []string) (string, int, error) {
		output, code, err := f.exec(ctx, dir, argv)
		// The moment both refs exist and the only record for the pair is the
		// provisional one: close the medium. Nothing else in the run writes there.
		if dir == f.aslRoot && isGitTagCreate(argv) && err == nil && code == 0 && !sealed {
			sealed = true
			sealEvidence(t, f.evidenceDir, sealRecord)
		}
		return output, code, err
	}
	t.Cleanup(func() { unsealEvidence(f.evidenceDir) })

	ev, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("the gate reported success despite failing to publish the record that names the tags")
	}
	if ev == nil {
		t.Fatal("no evidence was returned for a run that got as far as tagging")
	}
	if !strings.Contains(err.Error(), "writing evidence") {
		t.Errorf("err = %v, want the write failure named", err)
	}
	if !strings.Contains(err.Error(), "no repository was left carrying the pair tag") {
		t.Errorf("err = %v, want the retraction reported to the operator", err)
	}
	if errors.Is(err, ErrPartialTagging) {
		t.Errorf("err = %v, want no partial state: both rollbacks succeeded", err)
	}

	// The claim is retracted even though no account of retracting it could be
	// published.
	for _, root := range []string{f.agentMuxRoot, f.aslRoot} {
		if tags := strings.Fields(run(t, root, "git", "tag", "-l")); len(tags) != 0 {
			t.Errorf("%s has tags %v after an unpublishable tagging attempt, want none", root, tags)
		}
	}
	if len(ev.Tags) != 0 {
		t.Errorf("tags = %+v, want none", ev.Tags)
	}
	if ev.TagFailure == nil || len(ev.TagFailure.RolledBack) != 2 {
		t.Errorf("tag_failure = %+v, want both tags recorded as rolled back", ev.TagFailure)
	}
	// The record the caller is handed carries the retraction, so it carries the
	// outcome too -- including for the summary the command prints, which is
	// rendered from this and not from the file.
	if ev.Result != StatusNotReleased || ev.Passed() {
		t.Errorf("result = %q, want %q: both tags were taken back", ev.Result, StatusNotReleased)
	}

	unsealEvidence(f.evidenceDir)

	// The file is the only thing that outlives the process, so it is read back
	// rather than trusted from memory.
	stored, loadErr := LoadEvidence(EvidencePath(f.evidenceDir, ev.PairID))
	if loadErr != nil {
		t.Fatalf("reading back the evidence record: %v", loadErr)
	}
	if len(stored.Tags) != 0 {
		t.Errorf("persisted tags = %+v, want none: no tag survives this run", stored.Tags)
	}
	// The single field a consumer reads without opening anything else may not
	// report a release that neither repository carries.
	if stored.Result == StatusPassed || stored.Passed() {
		t.Errorf("persisted result = %q reads as a passed release for a run that released nothing", stored.Result)
	}

	// The signature state agrees with the bytes. Whatever record survived, it
	// declares itself unsigned -- and nothing beside it may still verify,
	// because what a signature here would vouch for is a release.
	if stored.Signature != ev.Signature {
		t.Errorf("persisted signature %+v disagrees with the returned one %+v", stored.Signature, ev.Signature)
	}
	if stored.Signature.Signed {
		t.Errorf("persisted signature = %+v, want unsigned: no release was made", stored.Signature)
	}
	requireNoValidSignature(t, VerifyCommand(f.publicKey, "release@test", stored.Path), stored.Path)

	// And the operator watching the run was told the same story, in the order it
	// happened: both tags written, both taken back, nothing left behind.
	summary := progress.String()
	for _, want := range []string{
		"tagged " + AgentMuxRepo + " " + ev.PairID,
		"tagged " + ASLRepo + " " + ev.PairID,
		"rolled back " + ASLRepo + " " + ev.PairID,
		"rolled back " + AgentMuxRepo + " " + ev.PairID,
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("progress output does not report %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "STRANDED") {
		t.Errorf("progress output reports a stranded ref for a run that left none:\n%s", summary)
	}

	return unpublishableTagging{f: f, ev: ev, err: err, stored: stored, progress: summary}
}

// sealEvidence makes the evidence directory unwritable, and optionally every
// file already in it, so that neither publication nor in-place rewriting can
// land. The files are sealed first: once the directory is read-only the mode of
// what it contains can still be changed, but doing it in this order keeps the
// window in which the directory is closed and a file is not from existing at all.
func sealEvidence(t *testing.T, dir string, records bool) {
	t.Helper()
	if records {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Errorf("listing the evidence directory to seal it: %v", err)
			return
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if err := os.Chmod(filepath.Join(dir, entry.Name()), 0o400); err != nil {
				t.Errorf("sealing %s: %v", entry.Name(), err)
			}
		}
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Errorf("sealing the evidence directory: %v", err)
	}
}

// unsealEvidence restores what sealEvidence took away, so the record can be read
// back and the temporary directory can be removed. It is best effort: it runs
// both as cleanup and inline, and the failure it would report is one the
// assertions that follow report better.
func unsealEvidence(dir string) {
	_ = os.Chmod(dir, 0o700)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			_ = os.Chmod(filepath.Join(dir, entry.Name()), 0o600)
		}
	}
}
