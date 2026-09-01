package release

import (
	"context"
	"errors"
	"strings"
	"testing"
)

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
	// Every step passed, and the pair was still not released: the result says
	// so rather than leaving the tagging failure to a field a result-only
	// consumer never reads. Nothing was left behind, so it is not `partial`.
	if ev.Result != StatusNotReleased {
		t.Errorf("result = %q, want %q: the run was asked to tag a pair and tagged neither repository", ev.Result, StatusNotReleased)
	}
	if ev.Passed() {
		t.Error("a tagging attempt that was taken back in full reads as a released pair")
	}
	stored, loadErr := LoadEvidence(EvidencePath(f.evidenceDir, ev.PairID))
	if loadErr != nil {
		t.Fatalf("reading back the evidence record: %v", loadErr)
	}
	if len(stored.Tags) != 0 {
		t.Errorf("persisted tags = %+v, want none: the record must not name a tag that is not there", stored.Tags)
	}
	if stored.Result != StatusNotReleased || stored.Passed() {
		t.Errorf("persisted result = %q, want %q", stored.Result, StatusNotReleased)
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

// TestUnreadableCreatedTagDoesNotOfferNameOnlyDeletion covers the one leftover
// ref the gate knows the least about.
//
// A tag creation has two halves: `git tag -a` writes the ref, and `git rev-parse`
// reads back the object it produced. When the second half fails the ref is real
// and nameless -- the run knows a tag exists under that name and does not know
// that the object under it is the one it wrote. Automated rollback already
// refuses such a tag, because every removal the gate performs is a
// compare-and-delete and there is nothing to compare against. The recovery
// instruction did not: it fell back to `git tag -d <name>`, which deletes
// whatever now answers to the name. Published in a record that outlives the run,
// that is an instruction to delete a ref this invocation may never have created.
//
// So the requirement here is a negative one, and it is asserted as a negative:
// nothing the run publishes -- the persisted record, the operator's stream, or
// the returned error -- may contain a deletion by name, and running everything it
// does publish must leave a replacement ref exactly where it was.
func TestUnreadableCreatedTagDoesNotOfferNameOnlyDeletion(t *testing.T) {
	f := newFixture(t)

	// A tag that predates the run, in both repositories. Nothing this run
	// publishes may endanger it either.
	for _, root := range []string{f.agentMuxRoot, f.aslRoot} {
		run(t, root, "git", "tag", "-a", "v0.0.1", "-m", "an earlier release")
	}

	// Progress is the operator's view of a run in flight: release-gate wires it
	// to its own stderr, so these lines are CLI output.
	var progress strings.Builder

	opts := f.options()
	opts.Tag = true
	opts.Progress = &progress
	var commands [][]string
	opts.Run = func(ctx context.Context, dir string, argv []string) (string, int, error) {
		commands = append(commands, argv)
		// Only the second repository's read-back fails. Its `git tag -a` really
		// runs, so the ref genuinely exists and genuinely has no known object --
		// which is the whole state under test. Agent Mux's tag is identified
		// normally, so the run also has an identified ref to take back and the
		// two kinds are told apart on one record.
		if dir == f.aslRoot && isGitTagReadBack(argv) {
			return "fatal: injected tag read-back failure", 128, nil
		}
		return ExecRunner(ctx, dir, argv)
	}

	ev, err := Run(context.Background(), opts)
	if !errors.Is(err, ErrPartialTagging) {
		t.Fatalf("err = %v, want ErrPartialTagging: a ref survives that a human must clear", err)
	}
	if ev == nil {
		t.Fatal("no evidence was returned for a run that got as far as tagging")
	}

	// The refs are the state the record has to describe. Agent Mux's identified
	// tag went back; ASL's unidentified one did not.
	if tags := strings.Fields(run(t, f.agentMuxRoot, "git", "tag", "-l")); len(tags) != 1 || tags[0] != "v0.0.1" {
		t.Errorf("agentmux tags = %v, want only the pre-existing v0.0.1: its tag was identified and removed", tags)
	}
	aslTags := strings.Fields(run(t, f.aslRoot, "git", "tag", "-l"))
	if len(aslTags) != 2 {
		t.Fatalf("asl tags = %v, want the pre-existing v0.0.1 and the stranded pair tag", aslTags)
	}

	// The run itself never attempted a name-only deletion either: one
	// compare-and-delete for the identified tag, and no `git tag -d` at all.
	if deletes := commandsMatching(commands, isGitUpdateRef); len(deletes) != 1 {
		t.Errorf("update-ref invocations = %v, want exactly the identified tag's rollback", deletes)
	}
	if byName := commandsMatching(commands, isGitTagDelete); len(byName) != 0 {
		t.Errorf("the run executed %v, want no deletion by name", byName)
	}

	stored, loadErr := LoadEvidence(EvidencePath(f.evidenceDir, ev.PairID))
	if loadErr != nil {
		t.Fatalf("reading back the evidence record: %v", loadErr)
	}
	if stored.Result != StatusPartial || stored.Passed() {
		t.Errorf("persisted result = %q, want %q: a pair tagged on one side is not released", stored.Result, StatusPartial)
	}
	if len(stored.Tags) != 0 {
		t.Errorf("persisted tags = %+v, want none", stored.Tags)
	}
	failure := stored.TagFailure
	if failure == nil {
		t.Fatal("the persisted record does not describe the ref it left behind")
	}
	wantStranded := TagEvidence{Repo: ASLRepo, Tag: ev.PairID}
	if len(failure.Stranded) != 1 || failure.Stranded[0] != wantStranded {
		t.Fatalf("stranded = %+v, want %+v", failure.Stranded, wantStranded)
	}
	wantRolledBack := TagEvidence{Repo: AgentMuxRepo, Tag: ev.PairID}
	if len(failure.RolledBack) != 1 || failure.RolledBack[0] != wantRolledBack {
		t.Errorf("rolled_back = %+v, want %+v", failure.RolledBack, wantRolledBack)
	}
	// The executable-recovery field is where a name-only deletion used to land.
	// For a ref with no known object it must be empty rather than helpful.
	if len(failure.Recovery) != 0 {
		t.Errorf("recovery = %v, want no executable deletion: this run cannot name the object it created", failure.Recovery)
	}
	if len(failure.Unidentified) != 1 {
		t.Fatalf("unidentified = %+v, want the stranded ref accounted for", failure.Unidentified)
	}
	unidentified := failure.Unidentified[0]
	if unidentified.Repo != ASLRepo || unidentified.Tag != ev.PairID {
		t.Errorf("unidentified = %+v, want %s %s", unidentified, ASLRepo, ev.PairID)
	}
	if !strings.Contains(unidentified.Reason, "never identified") {
		t.Errorf("reason = %q, want it to say the object was never established", unidentified.Reason)
	}
	if !strings.Contains(unidentified.Requirement, "never by name") ||
		!strings.Contains(unidentified.Requirement, ev.PairID) {
		t.Errorf("requirement = %q, want the confirmation this run cannot make and the pair it is about", unidentified.Requirement)
	}
	verifySignature(t, VerifyCommand(f.publicKey, "release@test", stored.Path), stored.Path)

	// Nothing anywhere in the durable record, the operator's stream, or the
	// returned error is a deletion by name. The record is checked as raw bytes
	// rather than field by field, so a future field cannot reintroduce one
	// somewhere this test does not look.
	for _, published := range []struct {
		what string
		text string
	}{
		{"the persisted record", readFile(t, stored.Path)},
		{"the progress the command printed", progress.String()},
		{"the returned error", err.Error()},
	} {
		if strings.Contains(published.text, "tag -d") {
			t.Errorf("%s offers a deletion by name:\n%s", published.what, published.text)
		}
	}

	// The operator is told the ref is there and told it is not clearable
	// automatically -- silence would be its own failure.
	summary := progress.String()
	for _, want := range []string{
		"tagged " + AgentMuxRepo + " " + ev.PairID,
		"rolled back " + AgentMuxRepo + " " + ev.PairID,
		"STRANDED " + ASLRepo + " " + ev.PairID,
		"UNIDENTIFIED " + ASLRepo + " " + ev.PairID,
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("progress output does not report %q:\n%s", want, summary)
		}
	}

	// Now the property the old fallback violated. Someone else puts a different
	// object under the same name -- which is what `git tag -d` would have
	// deleted -- and then everything the record publishes is executed verbatim.
	run(t, f.aslRoot, "git", "tag", "-f", "-a", ev.PairID, "-m", "a replacement tag this run did not write")
	replacement := strings.TrimSpace(run(t, f.aslRoot, "git", "rev-parse", "refs/tags/"+ev.PairID))
	if replacement == "" {
		t.Fatal("the replacement tag has no object")
	}

	executable := append([]string{}, failure.Recovery...)
	executable = append(executable, unidentified.Inspect...)
	if len(executable) != len(unidentified.Inspect) || len(unidentified.Inspect) == 0 {
		t.Fatalf("published commands = %v, want only the read-only inspection", executable)
	}
	for _, command := range executable {
		run(t, "", strings.Fields(command)...)
	}
	if after := strings.TrimSpace(run(t, f.aslRoot, "git", "rev-parse", "refs/tags/"+ev.PairID)); after != replacement {
		t.Errorf("refs/tags/%s is %q after running everything the record publishes, want the untouched replacement %q",
			ev.PairID, after, replacement)
	}
}
