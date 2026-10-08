// Publishing a gate run's verdict: the ordering that keeps a record from ever
// claiming more than the run has actually achieved.

package release

import (
	"context"
	"errors"
	"fmt"
)

// finish publishes the record and -- only for a passing run -- writes the
// annotated pair tags. Writing happens before signing and tagging so a failure
// in either still leaves the record on disk.
//
// Tagging is reachable only when a signing key was named: newGate refuses the
// combination before the gate runs at all, so the tags this writes always
// accompany a record that is being signed.
//
// A run that is not tagging is done in one publication: nothing follows that
// could contradict the record, so `passed` is the truth at the moment it is
// written.
//
// A tagging run is not. Its verdict is provisional until both refs exist and
// the record that names them has been made durable, so the record it publishes
// first says exactly that -- StatusTagging, unsigned -- and the release claim is
// written once, afterwards, in the single step that also produces the signature
// over it. See writeSignedEvidence.
//
// The order matters more than it looks. Between the two publications the pair is
// in the one state this gate cannot make atomic: refs written into two
// independent repositories with a record on disk that predates them. What that
// durable record says is therefore the whole question, because every recovery
// from here -- rolling the refs back, publishing the account of having done so --
// can itself fail on the same medium that just failed. It says a tagging attempt
// is under way, which is true no matter which of the following steps breaks, and
// it carries no signature that could be replayed as a release approval.
func (g *gate) finish(ctx context.Context, ev *Evidence) error {
	tagging := ev.Result == StatusPassed && g.opts.Tag
	if !tagging {
		return g.publish(ctx, ev)
	}
	if err := g.checkTagsAbsent(ctx, ev.PairID); err != nil {
		return err
	}
	if err := g.publishProvisional(ctx, ev); err != nil {
		// Nothing was tagged and nothing durable claims otherwise, but the
		// record is still returned and rendered as the run's summary, so it may
		// not keep the provisional result: StatusTagging describes a run that
		// got as far as writing refs, and this one never reached the first one.
		ev.Result = StatusNotReleased
		return err
	}

	created, tagErr := g.writeTags(ctx, ev)
	if tagErr != nil {
		// writeTags has already undone what it could and recorded what it could
		// not, and downgraded the result out of StatusTagging accordingly.
		// Publishing is what makes that account outlive this process.
		if err := g.publishRetraction(ctx, ev); err != nil {
			return errors.Join(tagErr, err)
		}
		return tagErr
	}

	// Both refs exist, so the run has done everything a release requires and the
	// record may finally say so. This is the only place StatusPassed becomes
	// durable for a tagging run.
	ev.Result = StatusPassed
	if err := g.writeSignedEvidence(ctx, ev); err != nil {
		return g.abandonUnvouchedTags(ctx, ev, created, err)
	}
	return nil
}

// publishProvisional writes the record that stands while the pair tags are being
// written, and makes sure nothing beside it says more than it does.
//
// The record itself is downgraded to StatusTagging first, which is what makes
// declaredSignature withhold the signature: signing this would produce a
// verifiable statement about a release that has not happened, and the whole
// point of the provisional record is that it makes no claim to be one.
//
// The stale signature is a separate hazard. A `--force` run replaces a previous
// release's record, and that release's detached signature is still sitting in
// the directory covering bytes this write has just superseded. Removing it here
// costs nothing and closes the only window in which a record for this pair could
// be found next to a signature it does not describe.
func (g *gate) publishProvisional(ctx context.Context, ev *Evidence) error {
	ev.Result = StatusTagging
	if err := g.publish(ctx, ev); err != nil {
		return err
	}
	return discardSignature(SignaturePath(ev.Path))
}

// abandonUnvouchedTags removes pair tags whose own evidence could not be
// finalized, and then records having done so.
//
// The failure this compensates for is narrow and entirely durable. Both refs
// exist; the record that describes them does not. Whatever broke -- the write,
// the signature, or either of the two renames that publish them -- the canonical
// path still holds the provisional pre-tag record, saying a tagging attempt is
// under way and satisfying Passed() no more than it did before. If the signature
// rename landed and the record rename did not, that signature is sitting beside
// it, but it covers bytes that were never published and so verifies nothing that
// is there. A reader of either repository alone sees a release; a reader of the
// record sees an attempt that never finished.
//
// So the refs are retracted, using the same compare-and-delete transaction a
// failed second tag write uses, and for the same reason: only what this run
// created may be deleted. A ref that cannot be retracted becomes the recorded,
// recoverable partial state.
//
// The account is then published in turn, which cannot be assumed to work -- the
// thing that just failed is publication. It is attempted anyway, and when it
// fails the provisional record is upgraded in place instead, so the file says
// what happened rather than only that something was attempted. See
// publishRetraction. Both errors are returned together rather than the second
// hiding the first. None of that is load-bearing for the release claim: the
// refs are gone, and the record that outlives this process never said the pair
// passed.
func (g *gate) abandonUnvouchedTags(ctx context.Context, ev *Evidence, created []createdTag, cause error) error {
	abandonErr := g.abandonTags(ctx, ev, created,
		fmt.Errorf("release: the record that vouches for the pair tags could not be published: %w", cause))
	if err := g.publishRetraction(ctx, ev); err != nil {
		return errors.Join(abandonErr, err)
	}
	return abandonErr
}

// publishRetraction publishes a record that has just been downgraded out of
// `passed`, and makes sure the downgrade is durable even when publishing it is
// not.
//
// Every caller is compensating for a tagging attempt that did not complete, so
// the record in memory now says `not-released` or `partial` while the one on
// disk is the provisional pre-tag one, which says only that a tagging attempt
// was under way. That is already safe -- it is not a release claim and carries
// no signature -- but it is not the truth either: it names no refs, no cause,
// and nothing an operator can act on. Ordinary publication is how it is
// replaced. When that fails -- and on the path this exists for, a failed
// publication is exactly what is being compensated for -- the superseded record
// is upgraded in place instead. See gate.retractSuperseded.
//
// The publication error is still what the caller reports. A retraction is not a
// success: it means the account of what happened could only be written by giving
// up the guarantees publication provides, and the operator is owed the reason
// the ordinary write failed.
func (g *gate) publishRetraction(ctx context.Context, ev *Evidence) error {
	publishErr := g.publish(ctx, ev)
	if publishErr == nil {
		return nil
	}
	if retractErr := g.retractSuperseded(ev); retractErr != nil {
		return errors.Join(publishErr, retractErr)
	}
	return publishErr
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
// before the signature exists. Signing is on exactly when a key was named and
// the record is making a statement worth signing.
//
// A provisional record is not. Its whole purpose is to occupy the evidence path
// while a tagging attempt is in flight without asserting anything about it, and
// a signature over those bytes would be a verifiable artifact for a release that
// has not happened -- one that survives on disk precisely when the run cannot
// come back to replace it.
func (g *gate) declaredSignature(ev *Evidence) Signature {
	if g.opts.SigningKey == "" || ev.Result == StatusTagging {
		return Signature{Signed: false, Namespace: g.opts.Namespace}
	}
	return Signature{
		Signed:    true,
		Namespace: g.opts.Namespace,
		Path:      SignaturePath(ev.Path),
		KeyPath:   g.opts.SigningKey,
	}
}
