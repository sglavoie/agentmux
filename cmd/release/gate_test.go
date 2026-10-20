package release

import (
	"encoding/json"
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

// TestReleaseGateJSONSurfaceIsVersioned records the deliberate exception:
// release-gate emits the evidence record directly rather than the shared
// schema-v1 envelope, for the same reason doctor and the debug report families
// do -- the record is the artifact, and wrapping it would put a second,
// unrelated version number in front of the one that governs it.
func TestReleaseGateJSONSurfaceIsVersioned(t *testing.T) {
	if releaseGateCmd.Flags().Lookup("json") == nil {
		t.Fatal("release-gate does not declare --json")
	}
	if release.SchemaVersion != 2 {
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
	// The count it holds them to is the pattern's own, not a number written
	// down beside it that goes stale when a scenario is added.
	if !strings.Contains(recipe, "go test -list '{{ crash_restart_scenarios }}'") {
		t.Error("crash-restart does not derive its expected count from its -run pattern")
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
// smoke's result mean something. A smoke that passes because tmux was absent
// is worse than no smoke: it reports a boundary as exercised that was never
// touched.
func TestJustCockpitSmokeFailsRatherThanSkips(t *testing.T) {
	recipe := justfileRecipeClosure(t, readJustfile(t), "cockpit-smoke")

	for _, want := range []string{
		"{{ prefix_bin }}/amx",    // the installed amx, not a build
		"backend status --check",  // the tmux amx would start agents on is usable
		"new-session -d",          // a real tmux round trip
		`-S "$socket"`,            // on a socket of its own, never the private server's
		"ready: true",             // the real daemon, not just a reachable socket
		"export-events",           // the daemon answers for the sessions it holds
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
	// It runs from any terminal: nothing in it may ask for Kitty.
	for _, kitty := range []string{"kitty", "kitten", "KITTY_", "--current-window"} {
		if strings.Contains(recipe, kitty) {
			t.Errorf("cockpit-smoke names %q; it must run from any terminal with no Kitty:\n%s", kitty, recipe)
		}
	}
	// Read-only over the operator's live installation.
	for _, mutation := range []string{"session launch", "worktree remove", "config init", "daemon stop", "backend stop", "amx send", "kill-session"} {
		if strings.Contains(recipe, mutation) {
			t.Errorf("cockpit-smoke runs %q; the smoke must observe, not mutate", mutation)
		}
	}
	// Every tmux it runs names the smoke's own socket, so the server holding
	// the operator's agents is never the one started, listed, or killed.
	for _, line := range strings.Split(recipe, "\n") {
		if strings.Contains(line, `"$tmux_bin" -`) && !strings.Contains(line, `"$tmux_bin" -S "$socket"`) {
			t.Errorf("cockpit-smoke runs tmux outside its own socket: %s", strings.TrimSpace(line))
		}
	}
}

// TestJustKittySmokeIsOptInAndFailsRatherThanSkips keeps the Kitty smoke what
// it was while it was the cockpit smoke, and out of every gate: Kitty is an
// optional backend, so nothing an operator without it runs may depend on it.
func TestJustKittySmokeIsOptInAndFailsRatherThanSkips(t *testing.T) {
	justfile := readJustfile(t)
	recipe := justfileRecipeClosure(t, justfile, "kitty-smoke")

	for _, want := range []string{
		"kitten @ ls",                    // a real Kitty remote-control round trip
		"KITTY_WINDOW_ID",                // driven from a real window, not a headless shell
		"ready: true",                    // the real daemon, not just a reachable socket
		"export-events --current-window", // the daemon is reducing facts for that window
		"doctor --strict",                // invariants hold on the installed amx
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("kitty-smoke does not exercise %q:\n%s", want, recipe)
		}
	}
	for _, absent := range []string{"exit 0", "skip"} {
		if strings.Contains(recipe, absent) {
			t.Errorf("kitty-smoke contains %q; it must fail rather than skip:\n%s", absent, recipe)
		}
	}
	for _, mutation := range []string{"session launch", "worktree remove", "config init", "daemon stop"} {
		if strings.Contains(recipe, mutation) {
			t.Errorf("kitty-smoke runs %q; the smoke must observe, not mutate", mutation)
		}
	}

	for _, optIn := range []string{"kitty-smoke", "install-kitty"} {
		for _, gate := range []string{"check", "acceptance", "install", "install-restart-smoke", "update"} {
			header := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(gate) + `:.*$`).FindString(justfile)
			if header == "" {
				t.Fatalf("Justfile has no %q recipe", gate)
			}
			if strings.Contains(header, optIn) {
				t.Errorf("%s depends on %s; Kitty setup and its smoke are opt-in: %s", gate, optIn, header)
			}
		}
		for _, step := range release.Plan("/amx", "/asl") {
			if strings.Contains(strings.Join(step.Argv, " "), optIn) {
				t.Errorf("the paired gate's %s step runs %s", step.Name, optIn)
			}
		}
	}
}

// TestJustInstallCreatesNothingOfKittys is the install half of the same rule:
// the Kitty directory and snippet link are `install-kitty`'s, and `uninstall`
// still removes a link somebody made.
func TestJustInstallCreatesNothingOfKittys(t *testing.T) {
	justfile := readJustfile(t)

	install := justfileRecipe(t, justfile, "install")
	for _, kitty := range []string{"prefix_kitty", "agent-mux.conf"} {
		if strings.Contains(install, kitty) {
			t.Errorf("install touches %s; only install-kitty may:\n%s", kitty, install)
		}
	}
	link := `ln -sf "{{root}}/agent-mux.conf" "{{prefix_kitty}}/agent-mux.conf"`
	if got := justfileRecipe(t, justfile, "install-kitty"); !strings.Contains(got, link) {
		t.Errorf("install-kitty does not link the Kitty snippet:\n%s", got)
	}
	if got := justfileRecipe(t, justfile, "uninstall"); !strings.Contains(got, `rm -f "{{prefix_kitty}}/agent-mux.conf"`) {
		t.Errorf("uninstall no longer removes the Kitty snippet link:\n%s", got)
	}
}

// TestJustOverseerIntegrationFailsRatherThanSkips is the same property as the
// cockpit smoke's, for the gate that judges the installed *pair*. The suite it
// runs skips itself when there is no installed pair, because a checkout is a
// legitimate place to have none; this recipe is the caller that says there is
// supposed to be one, so the skip has to become a failure here or the step is a
// green report about two programs nobody checked against each other.
func TestJustOverseerIntegrationFailsRatherThanSkips(t *testing.T) {
	recipe := justfileRecipeClosure(t, readJustfile(t), "overseer-integration")

	for _, want := range []string{
		"AMX_ASL_OVERSEER_SMOKE=1",             // the suite's skip becomes a failure
		"AMX_INSTALLED_BINARY=",                // and it judges the installed binary
		"{{ prefix_bin }}/amx",                 // which is the installed one, not a build
		"tests.test_asl_overseer_paired_smoke", // the paired smoke suite itself
		`scripts/named-arg" asl_root`,          // `just` hands key=value positionally
		"command -v asl",                       // the ASL half is the one on PATH
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("overseer-integration does not exercise %q:\n%s", want, recipe)
		}
	}
	// Every failure path must be a nonzero exit. A recipe that shrugged at a
	// missing half would report a compatible pair it never assembled.
	for _, absent := range []string{"exit 0", "|| true", "skip"} {
		if strings.Contains(recipe, absent) {
			t.Errorf("overseer-integration contains %q; it must fail rather than skip:\n%s", absent, recipe)
		}
	}
}

// TestOverseerIntegrationSmokesBeforeTheCockpit pins the smoke phase's internal
// order. The overseer smoke needs only the two installed programs; the cockpit
// smoke needs a real tmux and the restarted daemon. Running the step that
// depends on the machine last is what lets an earlier failure be reported as a
// real regression rather than as the machine being wrong.
func TestOverseerIntegrationSmokesBeforeTheCockpit(t *testing.T) {
	steps := release.Plan("/amx", "/asl")

	overseer, cockpit := -1, -1
	for i, step := range steps {
		switch step.Name {
		case "overseer-integration":
			overseer = i
		case "cockpit-smoke":
			cockpit = i
		}
	}
	if overseer < 0 {
		t.Fatal("plan has no overseer-integration step")
	}
	if cockpit < 0 {
		t.Fatal("plan has no cockpit-smoke step")
	}
	if overseer > cockpit {
		t.Errorf("overseer-integration runs at %d, after cockpit-smoke at %d", overseer, cockpit)
	}
	if steps[overseer].Phase != release.PhaseSmoke {
		t.Errorf("overseer-integration is in phase %q, not %q", steps[overseer].Phase, release.PhaseSmoke)
	}
	// The checkout has to reach the recipe, or the gate smokes some other
	// pair -- or none, if nothing is installed where it looked.
	if got := strings.Join(steps[overseer].Argv, " "); !strings.Contains(got, "asl_root=/asl") {
		t.Errorf("overseer-integration argv = %q; it does not carry the ASL checkout", got)
	}
}

// TestJustPairedReleaseGatePassesTheCheckoutThrough covers the one thing the
// recipe itself owns: `just` hands `key=value` after a recipe name to the
// recipe positionally, so the prefixes have to be stripped or the gate is
// pointed at a directory named "asl_root=...".
func TestJustPairedReleaseGatePassesTheCheckoutThrough(t *testing.T) {
	recipe := justfileParameterizedRecipe(t, readJustfile(t), "paired-release-gate")

	for _, want := range []string{
		`"$named_arg" asl_root`,
		`"$named_arg" signing_key`,
		`"$named_arg" tag`,
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
// That run passes every step and really does write both pair tags, so its
// summary reads almost exactly like a release. Two things must differ: the word
// an operator scans for -- the tags were retracted because the record that
// vouches for them could not be signed, and a summary that still printed
// "tagged" would report a release that no longer exists in either repository --
// and the outcome on the first line, which is the only part a reader takes in
// before deciding whether to read the rest.
//
// Both output modes are rendered from the same record, so both are checked
// here. The human summary and the JSON a script consumes must not be able to
// disagree about whether the pair was released.
func TestReleaseGateSummaryDoesNotSayTaggedWhenTheTagsWereTakenBack(t *testing.T) {
	pairID := "amx-asl-1.0123456789abcdef"
	cause := "release: the record that vouches for the pair tags could not be published: " +
		"release: signing evidence exited 255: sign_and_send_pubkey: signing failed"
	ev := &release.Evidence{
		SchemaVersion: release.SchemaVersion,
		PairID:        pairID,
		// The gate itself passed and both tags were written; the result is what
		// the retraction left behind, and it is not `passed`.
		Result: release.StatusNotReleased,
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
			Error:      cause,
			RolledBack: []release.TagEvidence{{Repo: "asl", Tag: pairID}, {Repo: "agentmux", Tag: pairID}},
		},
	}
	if ev.Passed() {
		t.Fatal("the record the summary is rendered from reads as a passed release")
	}

	summary := renderReleaseGateResult(t, ev, false)

	for _, line := range strings.Split(summary, "\n") {
		if strings.Contains(line, "  tagged ") {
			t.Errorf("a run whose tags were rolled back reports them as written: %q", line)
		}
	}
	for _, want := range []string{
		"pair " + pairID + " (" + release.StatusNotReleased + ")",
		"tagging did not complete",
		"vouches for the pair tags",
		"rolled back asl " + pairID,
		"rolled back agentmux " + pairID,
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not contain %q:\n%s", want, summary)
		}
	}
	// The outcome line is what a reader scanning the top of a long summary
	// takes away, so it must not carry the word a release carries.
	if strings.Contains(summary, "("+release.StatusPassed+")") {
		t.Errorf("summary reports a run that released nothing as passed:\n%s", summary)
	}
	// Nothing was left behind, so the operator is asked to do nothing.
	for _, absent := range []string{"STRANDED", "clear it with"} {
		if strings.Contains(summary, absent) {
			t.Errorf("summary asks the operator to clean up after a completed rollback:\n%s", summary)
		}
	}

	// The JSON surface is the record itself, so what a script reads has to be
	// the same outcome the operator was shown and the same one the command
	// exited on: not released, no tags, both refs accounted for as taken back.
	var decoded release.Evidence
	document := renderReleaseGateResult(t, ev, true)
	if err := json.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatalf("decoding the JSON summary: %v\n%s", err, document)
	}
	if decoded.Result != release.StatusNotReleased || decoded.Passed() {
		t.Errorf("JSON result = %q, want %q", decoded.Result, release.StatusNotReleased)
	}
	if len(decoded.Tags) != 0 {
		t.Errorf("JSON tags = %+v, want none: neither repository carries the pair tag", decoded.Tags)
	}
	failure := decoded.TagFailure
	if failure == nil {
		t.Fatal("the JSON record says nothing about the tagging attempt it took back")
	}
	if failure.Error != cause {
		t.Errorf("JSON tag_failure.error = %q, want the returned error's cause %q", failure.Error, cause)
	}
	if len(failure.RolledBack) != 2 || len(failure.Stranded) != 0 {
		t.Errorf("JSON rolled_back = %+v, stranded = %+v, want both tags rolled back and none stranded",
			failure.RolledBack, failure.Stranded)
	}
}

// renderReleaseGateResult runs the command's own result writer in one of its two
// output modes. The mode is a package-level flag rather than a parameter, so it
// is set and restored here instead of in every caller.
func renderReleaseGateResult(t *testing.T, ev *release.Evidence, asJSON bool) string {
	t.Helper()
	previous := releaseGateJSON
	releaseGateJSON = asJSON
	t.Cleanup(func() { releaseGateJSON = previous })

	var out strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := writeReleaseGateResult(cmd, ev); err != nil {
		t.Fatalf("rendering the result: %v", err)
	}
	return out.String()
}

// TestReleaseGateSummaryOffersNoDeletionForAnUnidentifiedTag is the CLI half of
// `release.TestUnreadableCreatedTagDoesNotOfferNameOnlyDeletion`.
//
// The library refuses to put a name-only deletion in the record; this is the
// second place that refusal has to hold, because the summary is what an operator
// actually reads and pastes. A stranded tag whose object the run established
// prints a command to run, and one whose object it never established must print
// something an operator cannot mistake for that command -- so the two are checked
// side by side in a single failure, which is the arrangement that would tempt a
// renderer to treat them alike.
func TestReleaseGateSummaryOffersNoDeletionForAnUnidentifiedTag(t *testing.T) {
	pairID := "amx-asl-1.0123456789abcdef"
	ref := "refs/tags/" + pairID
	var out strings.Builder
	writeReleaseGateTagFailure(&out, &release.TagFailure{
		Error:         "release: reading back the tag just written to asl: exited 128",
		Stranded:      []release.TagEvidence{{Repo: "agentmux", Tag: pairID}, {Repo: "asl", Tag: pairID}},
		RollbackError: "release: the tag written to asl was never identified",
		Recovery:      []string{"git -C /src/agentmux update-ref -d " + ref + " abc123"},
		Unidentified: []release.UnidentifiedTag{{
			Repo:   "asl",
			Tag:    pairID,
			Reason: "release: the tag written to asl was never identified, so removing it could delete a ref this run did not create",
			Inspect: []string{
				"git -C /src/asl rev-parse " + ref,
				"git -C /src/asl cat-file -p " + ref,
			},
			Requirement: "confirm the object under " + ref + " in /src/asl is this run's annotated tag -- " +
				"its message reads `paired release " + pairID + "` -- then remove it by object with " +
				"`git -C /src/asl update-ref -d " + ref + " <the object id the inspection printed>`, never by name",
		}},
	})
	summary := out.String()

	// The identified tag still gets the pasteable compare-and-delete.
	for _, want := range []string{
		"STRANDED agentmux " + pairID,
		"clear it with: git -C /src/agentmux update-ref -d " + ref + " abc123",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not contain %q:\n%s", want, summary)
		}
	}
	// The unidentified one gets an inspection and a precondition, under wording
	// that does not read as something to run and be done with.
	for _, want := range []string{
		"UNIDENTIFIED asl " + pairID,
		"never identified",
		"inspect it with: git -C /src/asl rev-parse " + ref,
		"inspect it with: git -C /src/asl cat-file -p " + ref,
		"before removing it: ",
		"never by name",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not contain %q:\n%s", want, summary)
		}
	}
	// The unidentified tag is never offered under the wording that means
	// "pasteable, and then you are finished".
	for _, line := range strings.Split(summary, "\n") {
		if strings.Contains(line, "clear it with") && strings.Contains(line, "/src/asl") {
			t.Errorf("an unidentified tag is offered as a ready-to-run removal: %q", line)
		}
	}
	// And under no wording at all does a deletion by name appear.
	if strings.Contains(summary, "tag -d") {
		t.Errorf("the summary publishes a deletion by name:\n%s", summary)
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
