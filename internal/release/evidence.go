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
	path, err := g.prepareEvidencePath(ev)
	if err != nil {
		return err
	}
	staging, err := stageEvidence(path, ev)
	if err != nil {
		return err
	}
	if err := os.Rename(staging, path); err != nil {
		_ = os.Remove(staging)
		return fmt.Errorf("release: publishing evidence: %w", err)
	}
	g.published = true
	g.logf("evidence %s", path)
	return nil
}

// writeSignedEvidence publishes a record together with the signature that covers
// it, ordered so that the only state a failure between the two can leave behind
// is one that claims nothing.
//
// This is how a release claim becomes durable. Ordinary publication writes the
// record and then signs what it wrote, which is right for every record that
// describes something already true -- a failed gate, a retracted attempt -- and
// wrong for the one record whose bytes are themselves the claim. Between that
// write and its signature the pair tags are backed by a durable
// `"result": "passed"` file with no signature beside it, and everything that
// could take that back -- restating the record, rolling the refs back, recording
// having done so -- needs the evidence medium the signature failure may itself
// have been a symptom of.
//
// So nothing here touches the published path until every step that can fail has
// already succeeded. The record is rendered into a sibling staging file, signed
// there -- `ssh-keygen -Y sign` writes its detached signature beside whatever
// file it is given -- and only then moved into place. What remains are two
// renames within one directory, and those two are not atomic together.
//
// So the order is what carries the guarantee, and the guarantee is directional
// rather than all-or-nothing. The signature is moved first and the record
// second. A failure or a crash between them leaves the previous record -- the
// provisional `"result": "tagging"` one -- standing beside a signature that does
// not cover it, which verifies nothing and claims nothing, and which the next
// attempt replaces. What cannot happen is the other half: a canonical
// `"result": "passed"` record is never visible without the signature covering it
// already beside it, which is the state this exists to prevent.
//
// Signing is unconditional: the only caller is a tagging run, and a tagging run
// without a signing key is refused before the gate reads anything. See
// ErrUnsignedTag.
//
// The metadata this fills in is unwound on every failure, because the record is
// still handed to the caller and rendered as the run's summary. A publication
// that did not happen must not leave the returned evidence naming a signature
// file that is not there. There are five ways to fail here and the unwinding is
// the same for all of them, so it is deferred rather than repeated -- the next
// early return added below inherits it instead of quietly not doing it.
func (g *gate) writeSignedEvidence(ctx context.Context, ev *Evidence) (err error) {
	priorPath, priorSignature := ev.Path, ev.Signature
	defer func() {
		if err != nil {
			ev.Path, ev.Signature = priorPath, priorSignature
		}
	}()

	path, err := g.prepareEvidencePath(ev)
	if err != nil {
		return err
	}
	ev.Signature = Signature{
		Signed:    true,
		Namespace: g.opts.Namespace,
		Path:      SignaturePath(path),
		KeyPath:   g.opts.SigningKey,
	}

	staging, err := stageEvidence(path, ev)
	if err != nil {
		return err
	}
	// Both removals are no-ops once the corresponding rename has landed.
	defer func() {
		_ = os.Remove(staging)
		_ = os.Remove(SignaturePath(staging))
	}()

	if err = g.signPath(ctx, staging); err != nil {
		return err
	}
	if err = os.Rename(SignaturePath(staging), SignaturePath(path)); err != nil {
		return fmt.Errorf("release: publishing the release signature: %w", err)
	}
	if err = os.Rename(staging, path); err != nil {
		return fmt.Errorf("release: publishing evidence: %w", err)
	}
	g.published = true
	g.logf("evidence %s", path)
	g.logf("signature %s", SignaturePath(path))
	return nil
}

// prepareEvidencePath resolves where this pair's record belongs, makes sure the
// directory exists, and refuses to displace evidence this run did not write.
//
// A record already on disk for this pair was produced by an earlier run and may
// carry a signature. Replacing it silently would destroy the only durable
// statement about a release that has possibly already shipped. The guard applies
// to the first write only: a tagging run replaces its own record to record what
// tagging did, and that is not an overwrite of somebody else's evidence.
func (g *gate) prepareEvidencePath(ev *Evidence) (string, error) {
	if err := os.MkdirAll(g.opts.EvidenceDir, 0o700); err != nil {
		return "", fmt.Errorf("release: creating evidence directory: %w", err)
	}
	path := EvidencePath(g.opts.EvidenceDir, ev.PairID)
	if !g.opts.Overwrite && !g.published {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("release: evidence for %s already exists at %s; move it aside or pass --force", ev.PairID, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("release: checking existing evidence: %w", err)
		}
	}
	ev.Path = path
	return path, nil
}

// stageEvidence renders the record beside where it will be published. The
// staging file is a sibling rather than a temporary elsewhere, because the
// publication is a rename and a rename cannot cross filesystems.
func stageEvidence(path string, ev *Evidence) (string, error) {
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return "", fmt.Errorf("release: encoding evidence: %w", err)
	}
	data = append(data, '\n')

	staging := path + ".staging"
	if err := os.WriteFile(staging, data, 0o600); err != nil {
		return "", fmt.Errorf("release: writing evidence: %w", err)
	}
	return staging, nil
}

// retractSuperseded replaces the record this run already published, once the run
// knows that record no longer describes what happened.
//
// It exists for one narrow state. Both refs have since been taken back, every
// attempt to publish that account has failed, and what is on disk is the
// provisional pre-tag record. Publication writes a sibling staging file and
// renames it, so an evidence directory that cannot be written to at all defeats
// it. What survives is therefore a record saying a tagging attempt was under way
// -- which is not a release claim, does not satisfy Evidence.Passed(), and
// carries no signature anyone could replay as one -- but which also names no
// refs, no cause, and nothing an operator can act on.
//
// So the retraction creates nothing. It rewrites the file this run already owns,
// in place, which needs write permission on that file rather than on the
// directory holding it -- exactly the permission the failed publication did not
// need and this one may not lack. Losing atomicity is the price and it is the
// right way round here: an interrupted rewrite leaves a truncated record that no
// longer parses, and unreadable evidence is not a release claim either.
//
// This is an improvement on the durable state and never the thing that makes it
// safe. Nothing above depends on it succeeding, which is the point: the medium
// it writes to is the one that just failed, and a recovery that has to assume
// its own precondition is not a recovery. When it cannot land, the provisional
// record stands and says less than the truth rather than more.
//
// The signature declaration goes with it. Any detached signature beside the
// record covers bytes that are gone, so a record that still declared itself
// signed would be pointing at a signature that cannot verify it -- and the
// signature file itself is discarded for the same reason sign removes a stale
// one before writing a new one. That discard is attempted even when the rewrite
// fails: the two are independent hazards, and skipping the second because the
// first broke is how a stale signature outlives the bytes it covered.
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
	rewriteErr := g.rewriteInPlace(path, ev)
	return errors.Join(rewriteErr, discardSignature(SignaturePath(path)))
}

// rewriteInPlace renders the record over the file it already occupies.
func (g *gate) rewriteInPlace(path string, ev *Evidence) error {
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return fmt.Errorf("release: encoding the retracted evidence: %w", err)
	}
	data = append(data, '\n')
	if err := writeFileInPlace(path, data); err != nil {
		return fmt.Errorf("release: retracting the superseded evidence at %s: %w", path, err)
	}
	g.logf("retracted superseded evidence %s", path)
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
// Not every record declares one, and the two reasons are worth telling apart in
// the log. A run given no signing key produces unsigned evidence throughout and
// the operator chose that. A tagging run's provisional record is unsigned
// because it is not a release claim yet, and the signature arrives later with
// the record that is -- so reporting the first reason there would describe a
// misconfiguration that does not exist. See gate.declaredSignature.
func (g *gate) sign(ctx context.Context, ev *Evidence) error {
	if !ev.Signature.Signed {
		if g.opts.SigningKey == "" {
			g.logf("evidence is unsigned (no signing key given)")
		} else {
			g.logf("evidence is unsigned (the pair is not tagged yet)")
		}
		return nil
	}
	if err := g.signPath(ctx, ev.Path); err != nil {
		return err
	}
	g.logf("signature %s", ev.Signature.Path)
	return nil
}

// signPath runs `ssh-keygen -Y sign` over one file and confirms the detached
// signature landed beside it. It signs whatever path it is given, which is what
// lets writeSignedEvidence sign a staging file rather than the published record.
//
// `ssh-keygen -Y sign` writes <file>.sig next to the file and overwrites nothing
// else, so any existing signature is removed first: a stale signature left
// beside a rewritten record is the one outcome worse than an unsigned one.
//
// The namespace is not decoration. A signature made under
// "agentmux-asl-release" verifies only when the verifier names that same
// namespace, so a release key that is also used to sign commits or Git objects
// cannot have one of those signatures replayed as a release approval.
//
// Success is not taken on the exit status alone. The signature is the artifact
// the caller is about to publish or rename, so its absence has to be an error
// here rather than a missing file discovered by whoever verifies the release.
func (g *gate) signPath(ctx context.Context, path string) error {
	sigPath := SignaturePath(path)
	if err := os.Remove(sigPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("release: clearing previous signature: %w", err)
	}
	argv := []string{
		"ssh-keygen", "-Y", "sign",
		"-f", g.opts.SigningKey,
		"-n", g.opts.Namespace,
		path,
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
	return nil
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
	if ev.SchemaVersion < OldestSchemaVersion || ev.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("release: evidence %s has schema version %d, expected %d", path, ev.SchemaVersion, SchemaVersion)
	}
	// The evidence directory also holds desktop records, which share the
	// schema number and the result vocabulary and describe no pair.
	if !strings.HasPrefix(ev.PairID, PairIDPrefix) {
		return nil, fmt.Errorf("release: evidence %s is not a paired release record", path)
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
