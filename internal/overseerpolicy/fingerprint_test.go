package overseerpolicy

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func fingerprintOf(projects ...config.Session) string {
	return New(projects).Fingerprint()
}

func TestFingerprintIsSha256Hex(t *testing.T) {
	t.Parallel()

	got := fingerprintOf(config.Session{Title: "App", Directory: abs("repos", "app"), Profile: "work", ASLOverseer: boolPtr(true)})
	digest, ok := strings.CutPrefix(got, "sha256:")
	if !ok {
		t.Fatalf("Fingerprint() = %q, want a sha256: prefix", got)
	}
	if len(digest) != 64 {
		t.Fatalf("Fingerprint() digest = %q (%d chars), want 64 hex chars", digest, len(digest))
	}
	if strings.TrimLeft(digest, "0123456789abcdef") != "" {
		t.Fatalf("Fingerprint() digest = %q, want lowercase hex", digest)
	}
}

func TestFingerprintIsStableForTheSameDeclarations(t *testing.T) {
	t.Parallel()

	projects := []config.Session{
		{Title: "App", Directory: abs("repos", "app"), Profile: "work", ASLOverseer: boolPtr(true)},
		{Title: "Api", Directory: abs("repos", "api"), Profile: "work"},
	}
	first := New(projects).Fingerprint()
	second := New(projects).Fingerprint()
	if first != second {
		t.Fatalf("two evaluators over the same projects disagree: %q vs %q", first, second)
	}
}

// The fingerprint exists so a caller can tell that an earlier answer was
// decided against configuration that has since moved. Every input the decision
// actually depends on therefore has to move it.
func TestFingerprintChangesWithEveryDecidingField(t *testing.T) {
	t.Parallel()

	base := config.Session{Title: "App", Directory: abs("repos", "app"), Profile: "work", ASLOverseer: boolPtr(true)}
	baseline := fingerprintOf(base)

	declined := base
	declined.ASLOverseer = boolPtr(false)
	unenrolled := base
	unenrolled.ASLOverseer = nil
	moved := base
	moved.Directory = abs("repos", "other")
	renamed := base
	renamed.Title = "Application"
	reprofiled := base
	reprofiled.Profile = "personal"

	for name, project := range map[string]config.Session{
		"declined":   declined,
		"unenrolled": unenrolled,
		"moved":      moved,
		"renamed":    renamed,
		"reprofiled": reprofiled,
	} {
		if got := fingerprintOf(project); got == baseline {
			t.Errorf("%s left the fingerprint unchanged at %q", name, baseline)
		}
	}

	// "asked and declined" and "never asked" exclude alike but are not the same
	// configuration, so they must not share a fingerprint either.
	if fingerprintOf(declined) == fingerprintOf(unenrolled) {
		t.Error("declined and unenrolled share a fingerprint; the tri-state is being flattened")
	}
}

func TestFingerprintChangesWithDeclarationCountAndOrder(t *testing.T) {
	t.Parallel()

	app := config.Session{Title: "App", Directory: abs("repos", "app"), Profile: "work", ASLOverseer: boolPtr(true)}
	api := config.Session{Title: "Api", Directory: abs("repos", "api"), Profile: "work"}

	single := fingerprintOf(app)
	pair := fingerprintOf(app, api)
	if single == pair {
		t.Fatal("adding a declaration left the fingerprint unchanged")
	}
	// Configuration order is reported back in RepositoryDecision.Declarations,
	// so it is part of the answer and belongs in the fingerprint.
	if pair == fingerprintOf(api, app) {
		t.Fatal("reordering the declarations left the fingerprint unchanged")
	}
}

// A caller that holds an answer and acts on it later re-asks first and compares
// fingerprints, refusing when they differ. That guard is only reachable because
// the fingerprint covers every declaration rather than only the ones the plan
// named: configuration can move somewhere the plan never looks, and the caller
// still has to notice. The pairing is what matters — the fingerprint moves while
// the decision about the queried repository does not — so both halves are
// asserted together.
func TestFingerprintMovesWhenAnUnrelatedProjectIsWithdrawn(t *testing.T) {
	t.Parallel()

	app := abs("repos", "app")
	docs := config.Session{Title: "Docs", Directory: abs("repos", "docs"), Profile: "work", ASLOverseer: boolPtr(true)}
	withdrawn := docs
	withdrawn.ASLOverseer = boolPtr(false)
	declared := config.Session{Title: "App", Directory: app, Profile: "work", ASLOverseer: boolPtr(true)}

	before := fingerprintOf(declared, docs)
	after := fingerprintOf(declared, withdrawn)
	if before == after {
		t.Fatalf("withdrawing an unrelated project left the fingerprint at %q; a caller holding the earlier answer cannot tell configuration moved", before)
	}

	resolver := resolving(repoDir(app, app))
	assertAllowed(t, evaluate(t, []config.Session{declared, docs}, resolver, requestFor(app)))
	assertAllowed(t, evaluate(t, []config.Session{declared, withdrawn}, resolver, requestFor(app)))
}

// Naive concatenation would hash "a"+"b" and "ab"+"" to the same bytes. The
// length prefix is what keeps a title and a profile from trading characters
// across their boundary without moving the fingerprint.
func TestFingerprintSeparatesAdjacentFields(t *testing.T) {
	t.Parallel()

	directory := abs("repos", "app")
	split := fingerprintOf(config.Session{Title: "a", Profile: "b", Directory: directory})
	joined := fingerprintOf(config.Session{Title: "ab", Profile: "", Directory: directory})
	if split == joined {
		t.Fatalf("title/profile boundary is not encoded: both hash to %q", split)
	}
}

func TestFingerprintCoversOnlyTheAuthorizationInput(t *testing.T) {
	t.Parallel()

	base := config.Session{Title: "App", Directory: abs("repos", "app"), Profile: "work", ASLOverseer: boolPtr(true)}
	baseline := fingerprintOf(base)

	// Launch material is not authorization material: a project that changes
	// only how it starts a harness decides exactly the same way, and forcing
	// the caller to re-ask would be reporting a change that cannot exist.
	relaunched := base
	relaunched.Args = []string{"--model", "opus"}
	relaunched.Env = map[string]string{"FOO": "bar"}
	relaunched.Groups = []string{"backend"}
	if got := fingerprintOf(relaunched); got != baseline {
		t.Errorf("launch-only fields moved the fingerprint: %q vs %q", got, baseline)
	}

	// A declaration with no directory names no place on the filesystem and is
	// dropped at construction, so it cannot move the fingerprint either.
	if got := fingerprintOf(base, config.Session{Title: "Nowhere", Profile: "work", ASLOverseer: boolPtr(true)}); got != baseline {
		t.Errorf("a directoryless project moved the fingerprint: %q vs %q", got, baseline)
	}
}
