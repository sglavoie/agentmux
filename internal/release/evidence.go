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

// sign produces the detached SSH signature over the record's bytes.
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
	if g.opts.SigningKey == "" {
		ev.Signature = Signature{Signed: false, Namespace: g.opts.Namespace}
		g.logf("evidence is unsigned (no signing key given)")
		return nil
	}
	sigPath := SignaturePath(ev.Path)
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
	ev.Signature = Signature{
		Signed:    true,
		Namespace: g.opts.Namespace,
		Path:      sigPath,
		KeyPath:   g.opts.SigningKey,
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

// writeTags records the pair in both repositories with an annotated tag.
//
// Both tags carry the same name and the same message, so either repository
// alone answers "what was this released with": the peer revision, the contract
// set, the manifest hash the corpus was verified against, and the evidence file
// that proves the gate ran.
func (g *gate) writeTags(ctx context.Context, ev *Evidence) error {
	message := tagMessage(ev)
	for _, repo := range []string{AgentMuxRepo, ASLRepo} {
		root := g.rootFor(repo)
		argv := []string{"git", "tag", "-a", ev.PairID, "-m", message}
		output, code, err := g.run(ctx, root, argv)
		if err != nil {
			return fmt.Errorf("release: tagging %s: %w", repo, err)
		}
		if code != 0 {
			return fmt.Errorf("release: tagging %s exited %d: %s", repo, code, strings.TrimSpace(output))
		}
		ev.Tags = append(ev.Tags, TagEvidence{Repo: repo, Tag: ev.PairID})
		g.logf("tagged %s %s", repo, ev.PairID)
	}
	return nil
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
