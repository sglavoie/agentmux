package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExecRunner is the real CommandRunner: it runs argv in dir and returns the
// combined output. A command that ran and exited non-zero is not an error here
// -- that is a step result, and the gate records it as one. Only a command that
// could not be started at all returns an error.
func ExecRunner(ctx context.Context, dir string, argv []string) (string, int, error) {
	if len(argv) == 0 {
		return "", -1, errors.New("release: empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(output), exitErr.ExitCode(), nil
		}
		return string(output), -1, err
	}
	return string(output), 0, nil
}

// EvidencePath is where a pair's record lives inside an evidence directory.
// One file per pair ID: re-running the gate on the same revisions addresses the
// same record, which is what makes an accidental second run visible instead of
// producing two records that disagree.
func EvidencePath(evidenceDir, pairID string) string {
	return filepath.Join(evidenceDir, pairID+".json")
}

// SignaturePath is the detached signature beside a record. `ssh-keygen -Y sign`
// chooses this name itself; it is spelled out here so consumers do not have to
// know that.
func SignaturePath(evidencePath string) string { return evidencePath + ".sig" }

// writeEvidence renders the record and writes it atomically.
//
// The rendering is deterministic by construction: every collection in Evidence
// is an ordered slice rather than a map, so Go's encoder emits the same field
// order on every machine, and the only values that legitimately vary between two
// runs of the same revisions are the timestamp and the measured durations.
func (g *gate) writeEvidence(ev *Evidence) error {
	if err := os.MkdirAll(g.opts.EvidenceDir, 0o700); err != nil {
		return fmt.Errorf("release: creating evidence directory: %w", err)
	}
	path := EvidencePath(g.opts.EvidenceDir, ev.PairID)

	// A record already on disk for this pair was produced by an earlier run and
	// may carry a signature. Replacing it silently would destroy the only
	// durable statement about a release that has possibly already shipped. The
	// guard applies to the first write only: a tagging run rewrites its own
	// record in place to add the tag list, and that is not an overwrite of
	// somebody else's evidence.
	if !g.opts.Overwrite && !g.published {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("release: evidence for %s already exists at %s; move it aside or pass --force", ev.PairID, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("release: checking existing evidence: %w", err)
		}
	}
	ev.Path = path

	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return fmt.Errorf("release: encoding evidence: %w", err)
	}
	data = append(data, '\n')

	staging := path + ".staging"
	if err := os.WriteFile(staging, data, 0o600); err != nil {
		return fmt.Errorf("release: writing evidence: %w", err)
	}
	if err := os.Rename(staging, path); err != nil {
		_ = os.Remove(staging)
		return fmt.Errorf("release: publishing evidence: %w", err)
	}
	g.published = true
	g.logf("evidence %s", path)
	return nil
}

// retractSuperseded invalidates the record this run already published, once the
// run knows that record no longer describes what happened.
//
// It exists for one narrow state, and it is the last thing standing in it. The
// pre-tag record is on disk saying the run passed; both refs have since been
// taken back; and every attempt to publish that account has failed. Publication
// writes a sibling staging file and renames it, so an evidence directory that
// cannot be written to at all defeats it -- and what survives is a signed record
// reading `"result": "passed"`, with no tag_failure, for a release that exists
// in neither repository. That record is all a later reader has, and
// Evidence.Passed() returns true for it.
//
// So the retraction creates nothing. It rewrites the file this run already owns,
// in place, which needs write permission on that file rather than on the
// directory holding it -- exactly the permission the failed publication did not
// need and this one does not lack. Losing atomicity is the price and it is the
// right way round here: an interrupted rewrite leaves a truncated record that no
// longer parses, and unreadable evidence is not a release claim, while the
// bytes it replaces are.
//
// The signature declaration goes with it. The detached signature beside the
// record covers the superseded bytes, so a record that still declared itself
// signed would be pointing at a signature that cannot verify it -- and the
// signature file itself is discarded for the same reason sign removes a stale
// one before writing a new one.
func (g *gate) retractSuperseded(ev *Evidence) error {
	// Only a record this run wrote may be rewritten. Without this, a run refused
	// for being about to overwrite a previous release's evidence would answer
	// that refusal by overwriting it non-atomically instead.
	if !g.published {
		return nil
	}
	path := EvidencePath(g.opts.EvidenceDir, ev.PairID)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("release: checking the superseded evidence: %w", err)
	}

	ev.Path = path
	ev.Signature = Signature{Signed: false, Namespace: g.opts.Namespace}
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return fmt.Errorf("release: encoding the retracted evidence: %w", err)
	}
	data = append(data, '\n')
	if err := writeFileInPlace(path, data); err != nil {
		return fmt.Errorf("release: retracting the superseded evidence at %s: %w", path, err)
	}
	g.logf("retracted superseded evidence %s", path)
	if err := discardSignature(SignaturePath(path)); err != nil {
		return err
	}
	return nil
}

// writeFileInPlace truncates an existing file and writes over it. It is the
// deliberate opposite of the staging-and-rename writeEvidence performs: no entry
// is created or removed in the containing directory, so it works where that
// cannot.
func writeFileInPlace(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// discardSignature takes away a detached signature that no longer covers the
// record beside it.
//
// Removing it is the honest outcome and is tried first. Removal edits the
// directory, though, and a retraction is reached precisely when the directory
// could not be edited, so the fallback truncates the signature in place: an
// empty file verifies nothing, which is the only property being asked for here.
func discardSignature(sigPath string) error {
	if err := os.Remove(sigPath); err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := os.Truncate(sigPath, 0); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("release: discarding the superseded signature at %s: %w", sigPath, err)
	}
	return nil
}

// sign produces the detached SSH signature over the record's bytes, fulfilling
// the signature the record already declares. It is the second half of publish:
// the metadata is written first so that the bytes being signed describe the
// signature that covers them.
//
// `ssh-keygen -Y sign` writes <file>.sig next to the file and refuses to
// overwrite nothing else, so the signature is removed first: a stale signature
// left beside a rewritten record is the one outcome worse than an unsigned one.
//
// The namespace is not decoration. A signature made under
// "agentmux-asl-release" verifies only when the verifier names that same
// namespace, so a release key that is also used to sign commits or Git objects
// cannot have one of those signatures replayed as a release approval.
func (g *gate) sign(ctx context.Context, ev *Evidence) error {
	if !ev.Signature.Signed {
		g.logf("evidence is unsigned (no signing key given)")
		return nil
	}
	sigPath := ev.Signature.Path
	if err := os.Remove(sigPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("release: clearing previous signature: %w", err)
	}
	argv := []string{
		"ssh-keygen", "-Y", "sign",
		"-f", g.opts.SigningKey,
		"-n", g.opts.Namespace,
		ev.Path,
	}
	output, code, err := g.run(ctx, g.opts.EvidenceDir, argv)
	if err != nil {
		return fmt.Errorf("release: signing evidence: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("release: signing evidence exited %d: %s", code, strings.TrimSpace(output))
	}
	if _, err := os.Stat(sigPath); err != nil {
		return fmt.Errorf("release: ssh-keygen reported success but wrote no signature at %s", sigPath)
	}
	g.logf("signature %s", sigPath)
	return nil
}

// checkTagsAbsent refuses before anything is written when either repository
// already carries this pair's tag.
//
// It runs before the record is written rather than after, because the
// alternative is a passing gate that writes and signs evidence and then fails on
// the tag -- leaving a signed record for a pair whose tags say something else.
func (g *gate) checkTagsAbsent(ctx context.Context, pairID string) error {
	for _, repo := range []string{AgentMuxRepo, ASLRepo} {
		root := g.rootFor(repo)
		_, code, err := g.run(ctx, root, []string{"git", "rev-parse", "--verify", "--quiet", "refs/tags/" + pairID})
		if err != nil {
			return fmt.Errorf("release: checking for tag %s in %s: %w", pairID, repo, err)
		}
		if code == 0 {
			return fmt.Errorf("release: %s already carries tag %s; a released pair is not re-tagged", repo, pairID)
		}
	}
	return nil
}

// createdTag is a ref this invocation wrote, remembered precisely enough to be
// taken back. The object ID is the whole point: a rollback that deletes by name
// removes whatever now answers to that name, while one that deletes by object
// removes only what this run produced.
type createdTag struct {
	repo string
	root string
	name string
	// oid is the tag object the creation produced, or empty when it could not
	// be read back. An unidentified tag is never deleted.
	oid string
}

// writeTags records the pair in both repositories with an annotated tag.
//
// Both tags carry the same name and the same message, so either repository
// alone answers "what was this released with": the peer revision, the contract
// set, the manifest hash the corpus was verified against, and the evidence file
// that proves the gate ran.
//
// The two writes are one transaction. Git has no cross-repository commit, so it
// is built here: every tag this run creates is remembered with the object it
// produced, and a failure on the second write takes the first one back before
// returning. A pair tagged in one repository only is worse than an untagged
// pair -- it is a release claim that the peer contradicts, standing next to
// evidence written before either tag existed.
//
// Only when the rollback itself cannot complete is a ref left behind, and then
// it is recorded on the evidence record as an explicitly partial, explicitly
// recoverable state. See abandonTags.
//
// The refs it created are returned so the caller can take them back too. Both
// tags existing is not yet a release: the record that vouches for them is
// published afterwards, and a failure there leaves exactly the state this
// transaction exists to prevent. See gate.abandonUnvouchedTags. The returned
// slice is meaningful only alongside a nil error -- a failed attempt has
// already been abandoned and accounted for here.
func (g *gate) writeTags(ctx context.Context, ev *Evidence) ([]createdTag, error) {
	message := tagMessage(ev)
	var created []createdTag
	for _, repo := range []string{AgentMuxRepo, ASLRepo} {
		tag, err := g.createTag(ctx, repo, g.rootFor(repo), ev.PairID, message)
		if tag != nil {
			created = append(created, *tag)
		}
		if err != nil {
			return nil, g.abandonTags(ctx, ev, created, err)
		}
	}
	// The record gains the tag list only once both refs exist. Naming a tag the
	// peer does not have is the exact confusion this transaction exists to
	// prevent, and a half-filled list would put it in the record too.
	for _, tag := range created {
		ev.Tags = append(ev.Tags, TagEvidence{Repo: tag.repo, Tag: tag.name})
	}
	return created, nil
}

// createTag writes one annotated tag and reads back the object it produced.
//
// The read-back is not a paranoid double check. It is what makes the tag
// removable later under a compare-and-delete, so a tag that cannot be identified
// is a tag that cannot be taken back safely -- which makes a failed read-back a
// failed creation. It is still returned, because the ref exists and the caller
// has to account for it rather than forget it.
func (g *gate) createTag(ctx context.Context, repo, root, name, message string) (*createdTag, error) {
	output, code, err := g.run(ctx, root, []string{"git", "tag", "-a", name, "-m", message})
	if err != nil {
		return nil, fmt.Errorf("release: tagging %s: %w", repo, err)
	}
	if code != 0 {
		return nil, fmt.Errorf("release: tagging %s exited %d: %s", repo, code, strings.TrimSpace(output))
	}
	oid, err := g.git(ctx, root, "rev-parse", "refs/tags/"+name)
	if err != nil {
		return &createdTag{repo: repo, root: root, name: name},
			fmt.Errorf("release: reading back the tag just written to %s: %w", repo, err)
	}
	g.logf("tagged %s %s", repo, name)
	return &createdTag{repo: repo, root: root, name: name, oid: firstLine(oid)}, nil
}

// abandonTags undoes what this run wrote and records whatever it could not undo.
//
// The two outcomes are kept distinct because the operator's next action differs.
// A fully rolled-back attempt is an untagged pair: fix the cause and re-run. A
// stranded ref has to be removed first -- the gate refuses to re-tag a pair that
// already carries its tag -- so the record names it and carries the exact command
// that clears it.
//
// Neither of them is a release, so neither leaves the result at `passed`. The
// distinction survives in the result itself -- StatusPartial when a ref is
// still out there, StatusNotReleased when nothing is -- and in the RolledBack
// and Stranded lists that say which refs went which way. A run that returns an
// error, records a tagging failure, and still persists `"result": "passed"`
// would be three statements about one outcome that do not agree, and the field
// a result-only consumer reads would be the one that is wrong.
func (g *gate) abandonTags(ctx context.Context, ev *Evidence, created []createdTag, cause error) error {
	failure := &TagFailure{Error: cause.Error()}
	var rollbackErrs []error
	// Reverse order: the repository tagged last is released first, so the
	// undo mirrors the do.
	for i := len(created) - 1; i >= 0; i-- {
		tag := created[i]
		record := TagEvidence{Repo: tag.repo, Tag: tag.name}
		if err := g.removeTag(ctx, tag); err != nil {
			failure.Stranded = append(failure.Stranded, record)
			rollbackErrs = append(rollbackErrs, err)
			g.logf("STRANDED %s %s: %v", tag.repo, tag.name, err)
			// A ref whose object this run never established gets an inspection
			// rather than a command. See UnidentifiedTag: the only deletion the
			// gate could spell out for it is one by name, and that is the one
			// deletion it must never publish.
			if tag.oid == "" {
				unidentified := unidentifiedTag(ev, tag, err)
				failure.Unidentified = append(failure.Unidentified, unidentified)
				g.logf("UNIDENTIFIED %s %s: no deletion command is offered; inspect with: %s",
					tag.repo, tag.name, strings.Join(unidentified.Inspect, "; "))
				continue
			}
			failure.Recovery = append(failure.Recovery, recoveryCommand(tag))
			continue
		}
		failure.RolledBack = append(failure.RolledBack, record)
		g.logf("rolled back %s %s", tag.repo, tag.name)
	}

	ev.Tags = nil
	ev.TagFailure = failure
	if len(failure.Stranded) == 0 {
		ev.Result = StatusNotReleased
		return fmt.Errorf("%w; no repository was left carrying the pair tag", cause)
	}
	failure.RollbackError = errors.Join(rollbackErrs...).Error()
	ev.Result = StatusPartial
	return fmt.Errorf("%w: %w; %s still carries %s and must be cleared before another attempt: %s",
		ErrPartialTagging, cause, strandedRepos(failure.Stranded), ev.PairID,
		recoveryGuidance(failure))
}

// removeTag deletes a ref this run created, and only while it is still exactly
// what this run created.
//
// `git update-ref -d <ref> <oid>` is a compare-and-delete: it refuses when the
// ref does not currently point at <oid>. That is the entire safety argument for
// the rollback. `git tag -d` would remove whatever now sits under the name,
// which for a name the gate verified absent minutes ago is almost always the tag
// it wrote -- but "almost always" is not a property a tool that deletes refs
// should rely on when the alternative costs one argument.
func (g *gate) removeTag(ctx context.Context, tag createdTag) error {
	if tag.oid == "" {
		return fmt.Errorf("release: the tag written to %s was never identified, so removing it could delete a ref this run did not create", tag.repo)
	}
	argv := []string{"git", "update-ref", "-d", "refs/tags/" + tag.name, tag.oid}
	output, code, err := g.run(ctx, tag.root, argv)
	if err != nil {
		return fmt.Errorf("release: removing tag %s from %s: %w", tag.name, tag.repo, err)
	}
	if code != 0 {
		return fmt.Errorf("release: removing tag %s from %s exited %d: %s", tag.name, tag.repo, code, strings.TrimSpace(output))
	}
	return nil
}

// recoveryCommand is what an operator runs by hand to clear a stranded tag. It
// goes into the record and not only into the error, because the process that
// printed the error is gone by the time anyone reads the record -- and the
// record is what a later reader has to be able to act on.
//
// It is the same compare-and-delete removeTag uses, and for the same reason: an
// instruction that outlives the run has more time to go stale than the run did,
// so binding the deletion to the object matters more here, not less. A tag with
// no object has no recovery command at all -- see unidentifiedTag.
func recoveryCommand(tag createdTag) string {
	return fmt.Sprintf("git -C %s update-ref -d refs/tags/%s %s", tag.root, tag.name, tag.oid)
}

// unidentifiedTag is the account of a stranded ref this run cannot name an
// object for.
//
// The gate could reach for `git rev-parse` again here and delete whatever came
// back. It deliberately does not: a second read by name is the same read that
// already failed, and trusting it would make the compare-and-delete a formality
// -- comparing the ref against a value derived from the ref. Identity that would
// actually be trustworthy has to come from the object's own contents, and the
// only reader who can judge that is a person looking at the annotation this run
// wrote. So the record asks for that judgement instead of faking it.
//
// What it publishes is therefore two read-only commands and a requirement. The
// requirement names the deletion's shape but not a runnable instance of it,
// because the object id belongs in it and the gate does not have one.
func unidentifiedTag(ev *Evidence, tag createdTag, cause error) UnidentifiedTag {
	ref := "refs/tags/" + tag.name
	return UnidentifiedTag{
		Repo:   tag.repo,
		Tag:    tag.name,
		Reason: cause.Error(),
		Inspect: []string{
			fmt.Sprintf("git -C %s rev-parse %s", tag.root, ref),
			fmt.Sprintf("git -C %s cat-file -p %s", tag.root, ref),
		},
		Requirement: fmt.Sprintf(
			"confirm the object under %s in %s is this run's annotated tag -- its message reads `paired release %s` and names evidence %s -- then remove it by object with `git -C %s update-ref -d %s <the object id the inspection printed>`, never by name",
			ref, tag.root, ev.PairID, filepath.Base(ev.Path), tag.root, ref),
	}
}

// recoveryGuidance is what the returned error tells an operator to do about
// every ref this run could not take back.
//
// The two kinds do not compose into one list of commands, so they are not
// presented as one: an identified tag contributes the command that clears it,
// and an unidentified one contributes what has to be established before anything
// is deleted. Flattening the second into the first is how a name-only deletion
// would get published in the first place.
func recoveryGuidance(failure *TagFailure) string {
	parts := make([]string, 0, len(failure.Recovery)+len(failure.Unidentified))
	parts = append(parts, failure.Recovery...)
	for _, tag := range failure.Unidentified {
		parts = append(parts, fmt.Sprintf("in %s no deletion command can be offered -- %s; %s",
			tag.Repo, tag.Reason, tag.Requirement))
	}
	return strings.Join(parts, "; ")
}

func strandedRepos(stranded []TagEvidence) string {
	names := make([]string, 0, len(stranded))
	for _, tag := range stranded {
		names = append(names, tag.Repo)
	}
	return strings.Join(names, ", ")
}

// tagMessage is the annotated tag body. It is plain "key: value" lines so it
// stays readable in `git show` and parseable by anything that needs to.
func tagMessage(ev *Evidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "paired release %s\n\n", ev.PairID)
	fmt.Fprintf(&b, "pair_id: %s\n", ev.PairID)
	fmt.Fprintf(&b, "manifest_sha256: %s\n", ev.Contract.ManifestSHA256)
	fmt.Fprintf(&b, "contract_set: %s\n", ev.Contract.SetID)
	for _, repo := range ev.Repositories {
		fmt.Fprintf(&b, "%s_revision: %s\n", repo.Repo, repo.Revision)
	}
	for _, artifact := range ev.Artifacts {
		fmt.Fprintf(&b, "%s_sha256: %s\n", artifact.Name, artifact.SHA256)
	}
	fmt.Fprintf(&b, "evidence: %s\n", filepath.Base(ev.Path))
	return b.String()
}

// LoadEvidence reads a record back. Consumers verifying a release read the file
// through this rather than reaching into the JSON, so a schema version they do
// not understand is refused in one place.
func LoadEvidence(path string) (*Evidence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ev Evidence
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("release: parsing evidence %s: %w", path, err)
	}
	if ev.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("release: evidence %s has schema version %d, expected %d", path, ev.SchemaVersion, SchemaVersion)
	}
	return &ev, nil
}

// VerifyCommand is the exact `ssh-keygen -Y verify` invocation that checks a
// release record, given an allowed-signers file and the principal to accept.
//
// It is built here rather than only written in the documentation so the command
// an operator is told to run comes from the same place as the namespace the gate
// signs with. The two cannot drift.
func VerifyCommand(allowedSigners, principal, evidencePath string) []string {
	return []string{
		"ssh-keygen", "-Y", "verify",
		"-f", allowedSigners,
		"-I", principal,
		"-n", SigningNamespace,
		"-s", SignaturePath(evidencePath),
	}
}
