// Pair tagging: the two-repository transaction that turns a passing gate into
// a released pair, and the accounting for the one failure it cannot prevent.
package release

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

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
