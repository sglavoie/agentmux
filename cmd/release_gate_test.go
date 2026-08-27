package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/release"
	"github.com/spf13/cobra"
)

// The gate's behaviour is tested in internal/release against a stand-in world.
// What is left to guard here is the surface: the command must stay hidden and
// registered, its flags are what `just paired-release-gate` types, and the
// recipes the gate leans on must keep the properties that make their results
// mean anything.

func TestReleaseGateIsRegisteredAndHidden(t *testing.T) {
	var found bool
	for _, command := range rootCmd.Commands() {
		if command.Name() == "release-gate" {
			found = true
			if !command.Hidden {
				t.Error("release-gate is visible in root help; it is an operator procedure with real side effects, not a user command")
			}
		}
	}
	if !found {
		t.Fatal("root command has no release-gate command")
	}

	// Every flag the justfile recipe types. A rename here silently turns the
	// documented invocation into an unknown-flag error.
	for _, name := range []string{"asl-root", "amx-root", "signing-key", "evidence-dir", "tag", "force", "json"} {
		if releaseGateCmd.Flags().Lookup(name) == nil {
			t.Errorf("release-gate does not declare --%s", name)
		}
	}
	if releaseGateCmd.Flag("asl-root").Annotations[cobraRequiredAnnotation] == nil {
		t.Error("--asl-root is not marked required; the gate must never guess which ASL checkout it is releasing")
	}
	if !releaseGateCmd.SilenceErrors || !releaseGateCmd.SilenceUsage {
		t.Error("release-gate must silence Cobra's own error and usage output; it maps outcomes to exit codes itself")
	}
}

// cobraRequiredAnnotation is Cobra's internal key for a required flag. It is
// spelled out rather than imported because Cobra does not export it.
const cobraRequiredAnnotation = "cobra_annotation_bash_completion_one_required_flag"

// TestReleaseGateJSONSurfaceIsVersioned records the deliberate exception:
// release-gate emits the evidence record directly rather than the shared
// schema-v1 envelope, for the same reason doctor and the debug report families
// do -- the record is the artifact, and wrapping it would put a second,
// unrelated version number in front of the one that governs it.
func TestReleaseGateJSONSurfaceIsVersioned(t *testing.T) {
	if releaseGateCmd.Flags().Lookup("json") == nil {
		t.Fatal("release-gate does not declare --json")
	}
	if release.SchemaVersion != 1 {
		t.Errorf("unexpected release evidence schema version %d", release.SchemaVersion)
	}
	if release.SigningNamespace != "agentmux-asl-release" {
		t.Errorf("signing namespace = %q; it is a verification contract and cannot drift", release.SigningNamespace)
	}
}

// justfileParameterizedRecipe extracts a recipe body by name, tolerating a
// parameter list after the name. The shared helper in justfile_deploy_test.go
// anchors on `name:`, which parameterized recipes do not match.
func justfileParameterizedRecipe(t *testing.T, justfile, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `[^:\n]*:.*$`)
	loc := re.FindStringIndex(justfile)
	if loc == nil {
		t.Fatalf("Justfile has no %q recipe", name)
	}
	rest := justfile[loc[1]:]
	for i, line := range strings.Split(rest, "\n") {
		if i == 0 {
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			return rest[:strings.Index(rest, line)]
		}
	}
	return rest
}

// TestJustCrashRestartCannotPassWithoutRunning guards the failure mode a
// pattern-driven gate has by default: `go test -run` exits 0 when the pattern
// matches nothing, so a renamed scenario turns the gate into a green step that
// checked nothing.
func TestJustCrashRestartCannotPassWithoutRunning(t *testing.T) {
	justfile := readJustfile(t)
	recipe := justfileParameterizedRecipe(t, justfile, "crash-restart")

	if !strings.Contains(recipe, "no tests to run") {
		t.Error("crash-restart does not detect go test's empty-pattern message")
	}
	if !strings.Contains(recipe, "--- PASS") {
		t.Error("crash-restart does not count the scenarios that actually ran")
	}
	if !strings.Contains(recipe, "-race") {
		t.Error("crash-restart must run the fault and restart scenarios under the race detector")
	}
	if !strings.Contains(justfile, "crash_restart_scenarios :=") {
		t.Fatal("Justfile has no crash_restart_scenarios pattern")
	}
	// The pattern must name the restart-equivalence scenarios, not only the
	// fault injections: a crash gate that never replays a restart is a fault
	// gate with a misleading name.
	for _, want := range []string{"TestFault", "RestartEquivalence", "SurvivesRestart"} {
		if !strings.Contains(justfile, want) {
			t.Errorf("crash_restart_scenarios does not cover %q", want)
		}
	}
}

// TestJustCockpitSmokeFailsRatherThanSkips is the property that makes the
// smoke's result mean something. A smoke that passes because Kitty was absent
// is worse than no smoke: it reports a boundary as exercised that was never
// touched.
func TestJustCockpitSmokeFailsRatherThanSkips(t *testing.T) {
	recipe := justfileParameterizedRecipe(t, readJustfile(t), "cockpit-smoke")

	for _, want := range []string{
		"kitten @ ls",             // a real Kitty remote-control round trip
		"KITTY_WINDOW_ID",         // driven from a real window, not a headless shell
		"ready: true",             // the real daemon, not just a reachable socket
		"export-events",           // the daemon is reducing facts for that window
		"doctor --strict",         // invariants hold on the installed pair
		"config asl-sync --check", // the ASL pairing preflight, writing nothing
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("cockpit-smoke does not exercise %q:\n%s", want, recipe)
		}
	}
	for _, absent := range []string{"exit 0", "skip"} {
		if strings.Contains(recipe, absent) {
			t.Errorf("cockpit-smoke contains %q; it must fail rather than skip:\n%s", absent, recipe)
		}
	}
	// Read-only over the operator's live installation.
	for _, mutation := range []string{"session launch", "worktree remove", "config init", "daemon stop"} {
		if strings.Contains(recipe, mutation) {
			t.Errorf("cockpit-smoke runs %q; the smoke must observe, not mutate", mutation)
		}
	}
}

// TestJustPairedReleaseGatePassesTheCheckoutThrough covers the one thing the
// recipe itself owns: `just` hands `key=value` after a recipe name to the
// recipe positionally, so the prefixes have to be stripped or the gate is
// pointed at a directory named "asl_root=...".
func TestJustPairedReleaseGatePassesTheCheckoutThrough(t *testing.T) {
	recipe := justfileParameterizedRecipe(t, readJustfile(t), "paired-release-gate")

	for _, want := range []string{
		"${asl_root#asl_root=}",
		"${signing_key#signing_key=}",
		"release-gate",
		"--asl-root",
		"--signing-key",
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("paired-release-gate recipe missing %q:\n%s", want, recipe)
		}
	}
	// An omitted signing key must be loud. An unsigned record looks exactly
	// like a signed one to anyone who does not check.
	if !strings.Contains(recipe, "unsigned") {
		t.Errorf("paired-release-gate does not warn when no signing key is given:\n%s", recipe)
	}
	// A caller who passes only signing_key must not have it read as a checkout.
	if !strings.Contains(recipe, "signing_key=*") {
		t.Errorf("paired-release-gate does not reject a misplaced signing_key argument:\n%s", recipe)
	}
}

// TestJustPairedReleaseGateRefusesTagWithoutSigningKey is the recipe half of the
// invariant `release.TestTaggingRequiresSigningKey` pins in the library: the
// pair tags are the durable claim that a release happened, so a rehearsal must
// not be able to write them. The gate refuses the combination itself; the recipe
// refuses it first, before the Go toolchain is even started, because this is the
// one operator mistake whose cost is a tag that cannot be un-pushed.
func TestJustPairedReleaseGateRefusesTagWithoutSigningKey(t *testing.T) {
	recipe := justfileParameterizedRecipe(t, readJustfile(t), "paired-release-gate")

	// The tag decision has to be made before the signing decision, or the
	// refusal cannot see that tagging was asked for.
	if !strings.Contains(recipe, "tag_requested") {
		t.Errorf("paired-release-gate does not decide tagging before it decides signing:\n%s", recipe)
	}
	if !strings.Contains(recipe, "tag=yes requires signing_key") {
		t.Errorf("paired-release-gate does not refuse tag=yes without a signing key:\n%s", recipe)
	}
	// `just` fills the parameters positionally, so `asl_root=… tag=yes` lands
	// "tag=yes" in the signing_key slot. Left alone that is the worst outcome
	// available: the tag request is silently dropped and the run is refused for
	// an unreadable key instead.
	if !strings.Contains(recipe, "asl_root=*|tag=*") {
		t.Errorf("paired-release-gate does not reject a tag= argument in the signing_key slot:\n%s", recipe)
	}
	// The refusal must precede the invocation, not annotate it.
	refusal := strings.Index(recipe, "tag=yes requires signing_key")
	invocation := strings.Index(recipe, "go run .")
	if invocation < 0 {
		t.Fatalf("paired-release-gate no longer invokes the gate:\n%s", recipe)
	}
	if refusal > invocation {
		t.Errorf("paired-release-gate refuses an unsigned tagging run only after invoking the gate:\n%s", recipe)
	}
	// And the library must agree, so the recipe is a courtesy rather than the
	// only thing standing between a rehearsal and a release tag.
	if !strings.Contains(release.ErrUnsignedTag.Error(), "signing key") {
		t.Errorf("release.ErrUnsignedTag = %v; the recipe mirrors a refusal that no longer names the prerequisite", release.ErrUnsignedTag)
	}
}

// TestReleaseGateSurfacesAStrandedPairTag guards the half of "durably record and
// surface" that the evidence file cannot cover.
//
// A stranded tag is the one gate outcome that leaves state outside the evidence
// directory, in a repository the operator will go on using. The record names it,
// but a record nobody opens is not a surface, so the summary has to name it too
// -- and it has to carry the command rather than tell the reader to go find it.
func TestReleaseGateSurfacesAStrandedPairTag(t *testing.T) {
	var out strings.Builder
	writeReleaseGateTagFailure(&out, &release.TagFailure{
		Error:         "release: tagging asl exited 128: fatal: cannot lock ref",
		Stranded:      []release.TagEvidence{{Repo: "agentmux", Tag: "amx-asl-1.0123456789abcdef"}},
		RollbackError: "release: removing tag amx-asl-1.0123456789abcdef from agentmux exited 1",
		Recovery:      []string{"git -C /src/agentmux update-ref -d refs/tags/amx-asl-1.0123456789abcdef abc123"},
	})
	summary := out.String()

	for _, want := range []string{
		"STRANDED agentmux amx-asl-1.0123456789abcdef",
		"this pair is not released",
		"cannot lock ref",
		"git -C /src/agentmux update-ref -d refs/tags/amx-asl-1.0123456789abcdef abc123",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not contain %q:\n%s", want, summary)
		}
	}
	// "tagged" is what a released pair prints. A half-tagged one must not borrow
	// the word an operator scans for.
	for _, line := range strings.Split(summary, "\n") {
		if strings.Contains(line, "  tagged ") {
			t.Errorf("a stranded tag is reported with the released-pair wording: %q", line)
		}
	}

	// A completed rollback says so and offers nothing to run, because there is
	// nothing left to clear.
	out.Reset()
	writeReleaseGateTagFailure(&out, &release.TagFailure{
		Error:      "release: tagging asl exited 128",
		RolledBack: []release.TagEvidence{{Repo: "agentmux", Tag: "amx-asl-1.0123456789abcdef"}},
	})
	rolled := out.String()
	if !strings.Contains(rolled, "rolled back agentmux") {
		t.Errorf("a rolled-back attempt is not reported:\n%s", rolled)
	}
	if strings.Contains(rolled, "STRANDED") || strings.Contains(rolled, "clear it with") {
		t.Errorf("a rolled-back attempt asks the operator to clean up after it:\n%s", rolled)
	}

	// And a run that tagged cleanly prints none of this.
	out.Reset()
	writeReleaseGateTagFailure(&out, nil)
	if out.Len() != 0 {
		t.Errorf("a successful run reports a tagging failure:\n%s", out.String())
	}
}

// TestReleaseGateSummaryDoesNotSayTaggedWhenTheTagsWereTakenBack is the CLI half
// of `release.TestFinalEvidenceSigningFailureRollsBackPairTags`.
//
// That run passes every step and really does write both pair tags, so the
// summary carries `(passed)` and reads almost exactly like a release. The one
// thing that must differ is the word an operator scans for: the tags were
// retracted because the record that vouches for them could not be signed, and a
// summary that still printed "tagged" would report a release that no longer
// exists in either repository.
func TestReleaseGateSummaryDoesNotSayTaggedWhenTheTagsWereTakenBack(t *testing.T) {
	pairID := "amx-asl-1.0123456789abcdef"
	ev := &release.Evidence{
		SchemaVersion: release.SchemaVersion,
		PairID:        pairID,
		Result:        release.StatusPassed,
		Repositories: []release.RepoEvidence{
			{Repo: "agentmux", Revision: "aaaaaaaaaaaaaaaaaaaa", Clean: true},
			{Repo: "asl", Revision: "bbbbbbbbbbbbbbbbbbbb", Clean: true},
		},
		Steps: []release.StepResult{{Name: "cockpit-smoke", Status: release.StatusPassed, DurationMS: 1000}},
		Path:  "/evidence/" + pairID + ".json",
		Signature: release.Signature{
			Signed:    true,
			Namespace: release.SigningNamespace,
			Path:      release.SignaturePath("/evidence/" + pairID + ".json"),
		},
		TagFailure: &release.TagFailure{
			Error: "release: the record that vouches for the pair tags could not be published: " +
				"release: signing evidence exited 255: sign_and_send_pubkey: signing failed",
			RolledBack: []release.TagEvidence{{Repo: "asl", Tag: pairID}, {Repo: "agentmux", Tag: pairID}},
		},
	}

	var out strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := writeReleaseGateResult(cmd, ev); err != nil {
		t.Fatalf("rendering the summary: %v", err)
	}
	summary := out.String()

	for _, line := range strings.Split(summary, "\n") {
		if strings.Contains(line, "  tagged ") {
			t.Errorf("a run whose tags were rolled back reports them as written: %q", line)
		}
	}
	for _, want := range []string{
		"tagging did not complete",
		"vouches for the pair tags",
		"rolled back asl " + pairID,
		"rolled back agentmux " + pairID,
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not contain %q:\n%s", want, summary)
		}
	}
	// Nothing was left behind, so the operator is asked to do nothing.
	for _, absent := range []string{"STRANDED", "clear it with"} {
		if strings.Contains(summary, absent) {
			t.Errorf("summary asks the operator to clean up after a completed rollback:\n%s", summary)
		}
	}
}

// TestReleaseEvidenceDefaultsUnderTheDataDirectory keeps release records beside
// the caches, handoffs, and worktrees rather than in a location invented for
// this feature.
func TestReleaseEvidenceDefaultsUnderTheDataDirectory(t *testing.T) {
	dir, err := release.DefaultEvidenceDir()
	if err != nil {
		t.Fatalf("default evidence dir: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	want := filepath.Join(home, ".local", "share", "agent-mux", "release-evidence")
	if dir != want {
		t.Errorf("default evidence dir = %q, want %q", dir, want)
	}
}
