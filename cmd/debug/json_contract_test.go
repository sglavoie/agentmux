package debug

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/spf13/cobra"
)

// Debug commands intentionally emit command-specific reports directly rather
// than wrapping them in the retained session command envelope. Audit every
// JSON-producing leaf and every report-family schema together so either kind
// of surface growth requires an explicit contract decision.
func TestDebugJSONCommandSurfaceIsVersioned(t *testing.T) {
	commands := []*cobra.Command{
		debugStatusCmd, debugTailCmd, debugProbeCmd, debugExplainCmd,
		debugCaptureStartCmd, debugCaptureStopCmd, debugCaptureStatusCmd,
		debugBundleCmd, debugReplayCmd, debugSubscribeCmd,
	}
	for _, command := range commands {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}

	// Additive fields carried by existing schema versions (no bump required):
	// status/tail v2 health gained `earliest_observed_at`, the shared
	// `journal_snapshot` object gained `promoted_spool_files_read`, the
	// status v2 hooks report gained `delivery_failures` {count, latest_at}
	// beside the stable `hook_delivery_failures` warning finding, and the
	// bundle v2 `doctor.json` member gained the optional `daemon.asl`
	// capability block, which a daemon without the integration omits entirely.
	//
	// Not in this ledger: `daemon.screen_patterns` on doctor v3. It is an
	// additive field, but it arrived alongside a new failure-severity finding
	// that bumped the doctor report version anyway, so it belongs to that
	// version's history rather than here. This ledger records fields that
	// shipped WITHOUT a bump; adding versioned ones would make it a changelog.
	schemas := []struct {
		name          string
		version, want int
	}{
		{name: "status", version: debugger.StatusSchemaVersion, want: 2},
		{name: "tail", version: debugger.TailSchemaVersion, want: 2},
		{name: "probe", version: debugger.ProbeSchemaVersion, want: 1},
		{name: "explain", version: debugger.ExplainSchemaVersion, want: 2},
		{name: "capture", version: debugger.CaptureSchemaVersion, want: 1},
		{name: "bundle", version: debugger.BundleSchemaVersion, want: 2},
		{name: "replay", version: debugger.ReplaySchemaVersion, want: 1},
		{name: "subscribe", version: subscribeSchemaVersion, want: 1},
	}
	for _, schema := range schemas {
		if schema.version != schema.want {
			t.Errorf("unexpected debug %s JSON schema version %d; want %d", schema.name, schema.version, schema.want)
		}
	}
}

// `amx doctor` gets its own audit rather than joining one of the families
// above, because it is not part of any of them: it is a single top-level leaf
// whose payload is a versioned diagnostic report, not the shared schema-v1
// envelope, and whose report deliberately carries health fields at the top
// level instead of under `data`. So none of the three envelope assertions
// applies to it and the debug-family assertion is about a different version
// namespace entirely.
//
// What is worth auditing here is what doctor's consumers actually branch on:
// the report version, and the stable finding identifiers. A finding identifier
// is the contract — `amx doctor --strict` exits nonzero on any
// failure-severity finding, so a consumer that treats an unrecognized failure
// identifier as fatal is behaving correctly, which is exactly why adding one
// bumps the report version.
//
// Not audited here: `release-gate`, which also declares --json; its evidence
// record is versioned by internal/release. `daemon audit` has its own audit
// below.
func TestDoctorJSONCommandSurfaceIsVersioned(t *testing.T) {
	commands := []*cobra.Command{doctorCmd}
	for _, command := range commands {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
		// --strict is half the contract: it is what turns a failure-severity
		// finding into a process exit status a script can branch on.
		if command.Flags().Lookup("strict") == nil {
			t.Errorf("%s does not declare --strict", command.CommandPath())
		}
	}

	// v5 is the companion bump: `companion_app` (info) and
	// `companion_build_mismatch` (warning) are new identifiers carried with
	// the additive `daemon.companion` block. Neither changes --strict.
	//
	// v4 is the Kitty-snapshot bump: `kitty_snapshot_stale` is a new WARNING
	// identifier carried alongside the additive `daemon.kitty_snapshot` block.
	// It is the first bump not driven by a failure identifier — a consumer that
	// has never seen it cannot tell a fresh snapshot from a daemon too old to
	// report one, which is exactly the distinction the block exists to make.
	//
	// v3 is the screen-classifier bump: `screenstate_persistent_decline` is a
	// new failure identifier. `screenstate_override_invalid` shipped in the
	// same work at warning severity and required no bump of its own.
	//
	// v7 is the backend bump: `backend_tmux_unavailable` and
	// `backend_server_unreadable` can be failures where launches go to tmux.
	if doctorReportSchemaVersion != 7 {
		t.Fatalf("unexpected doctor JSON schema version %d; want 7", doctorReportSchemaVersion)
	}

	// Every stable finding identifier a doctor report can carry. Adding one is
	// a contract decision; a duplicate would make two unrelated conditions
	// indistinguishable to a consumer that branches on `check`.
	checks := []string{
		checkASLActiveState, checkSubagentVisibility, checkActiveAfterTerminal,
		checkShadowCurrentMismatch, checkQueueSaturation,
		checkRenderGenerationDrift, checkRenderCausalBound, checkBuildMismatch,
		checkHarnessDrift, checkClosedTombstone, checkAcknowledgedState,
		checkOwnerMismatchPrompt, checkOwnerMismatchBurst,
		checkUnknownProducerFact, checkAcknowledgedPressure,
		checkMalformedProcessIdentity, checkStopHoldStuck,
		checkScreenPersistentDecline, checkScreenPatternOverrideInvalid,
		checkKittySnapshotStale, checkCompanionApp, checkCompanionBuildMismatch,
		checkRuntimeReconcileStale, checkRuntimeReconcileFailing,
		checkScreenCaptureFailing, checkTmuxClientTimeouts,
		checkBackendPrefix + "tmux_unavailable", checkBackendPrefix + "server_unreadable",
		checkBackendPrefix + "server_binary_unknown", checkBackendPrefix + "server_keeps_previous_tmux",
	}
	seen := make(map[string]bool, len(checks))
	for _, check := range checks {
		if check == "" {
			t.Error("an empty doctor finding identifier is not a contract")
		}
		if seen[check] {
			t.Errorf("duplicate doctor finding identifier %q", check)
		}
		seen[check] = true
	}
}

// `amx daemon audit --json` emits its own versioned report, like doctor, and
// shares doctor's finding identifiers; what a consumer branches on is the
// report version, the healthy verdict, and the strict-style exit status.
func TestDaemonAuditJSONCommandSurfaceIsVersioned(t *testing.T) {
	if daemonAuditCmd.Flags().Lookup("json") == nil {
		t.Errorf("%s does not declare --json", daemonAuditCmd.CommandPath())
	}
	if auditReportSchemaVersion != 1 {
		t.Fatalf("unexpected daemon audit JSON schema version %d; want 1", auditReportSchemaVersion)
	}
	encoded, err := json.Marshal(auditReport{SchemaVersion: auditReportSchemaVersion, Findings: []doctorFinding{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"schema_version":1`, `"started_at"`, `"finished_at"`, `"samples"`, `"findings":[]`, `"healthy"`} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("daemon audit report %s lacks %s", encoded, field)
		}
	}
}

// A debug leaf whose report could not be produced has no report version to
// speak of, so under --json it fails with the common error envelope, and
// never with a bare stderr line a script cannot parse.
func TestDebugJSONFailuresUseTheErrorEnvelope(t *testing.T) {
	resetDebugTestState(t)
	oldCaptureJSON, oldBundleJSON, oldBundleMode := debugCaptureJSON, debugBundleJSON, debugBundleMode
	oldStopOut, oldBundleOut := debugCaptureStopCmd.OutOrStdout(), debugBundleCmd.OutOrStdout()
	t.Cleanup(func() {
		debugCaptureJSON, debugBundleJSON, debugBundleMode = oldCaptureJSON, oldBundleJSON, oldBundleMode
		debugCaptureStopCmd.SetOut(oldStopOut)
		debugBundleCmd.SetOut(oldBundleOut)
	})
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	debugPaths = func() (diagnostics.Paths, error) { return paths, nil }

	cases := []struct {
		name     string
		command  *cobra.Command
		args     []string
		prepare  func()
		wantCode string
		wantText string
	}{
		{name: "capture stop with nothing to stop", command: debugCaptureStopCmd,
			prepare: func() { debugCaptureJSON = true }, wantCode: "no_capture", wantText: "no capture"},
		{name: "tail with a negative --last", command: debugTailCmd,
			prepare: func() { debugTailJSON, debugTailLimit = true, -1 }, wantCode: "invalid_arguments", wantText: "--last"},
		{name: "tail with a bad time bound", command: debugTailCmd,
			prepare: func() { debugTailJSON, debugSelectorFlags.after = true, "yesterday" }, wantCode: "invalid_arguments", wantText: "--after"},
		{name: "explain with an out-of-range limit", command: debugExplainCmd,
			prepare: func() { debugExplainJSON, debugExplainMaxNodes = true, 0 }, wantCode: "invalid_arguments", wantText: "--max-nodes"},
		{name: "probe with a selector it ignores", command: debugProbeCmd,
			prepare: func() { debugProbeJSON, debugSelectorFlags.profile = true, "work" }, wantCode: "invalid_arguments", wantText: "--profile"},
		{name: "bundle with an unknown mode", command: debugBundleCmd,
			prepare: func() { debugBundleJSON, debugBundleMode = true, "bogus" }, wantCode: "invalid_arguments", wantText: "--mode"},
		{name: "replay of a missing input", command: debugReplayCmd, args: []string{filepath.Join(t.TempDir(), "missing")},
			prepare: func() { debugReplayJSON = true }, wantCode: "not_found", wantText: "missing"},
		{name: "replay with no input", command: debugReplayCmd,
			prepare: func() { debugReplayJSON = true }, wantCode: "missing_parameter", wantText: "JOURNAL_OR_BUNDLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			debugSelectorFlags = debugSelectorFlagValues{}
			debugTailJSON, debugExplainJSON, debugProbeJSON, debugReplayJSON = false, false, false, false
			debugCaptureJSON, debugBundleJSON = false, false
			debugTailLimit, debugExplainMaxNodes = 50, debugger.DefaultExplainMaxNodes
			debugBundleMode = string(debugger.BundleModeShare)
			tc.prepare()
			var out bytes.Buffer
			tc.command.SetOut(&out)
			err := tc.command.RunE(tc.command, tc.args)
			var exit *cli.ExitError
			if !errors.As(err, &exit) || !exit.Quiet {
				t.Fatalf("err = %#v, want a quiet exit after the envelope", err)
			}
			var envelope cli.JSONErrorEnvelope
			if decodeErr := json.Unmarshal(out.Bytes(), &envelope); decodeErr != nil {
				t.Fatalf("decode %q: %v", out.String(), decodeErr)
			}
			if envelope.SchemaVersion != cli.JSONSchemaVersion || envelope.Error.Code != tc.wantCode ||
				!strings.Contains(envelope.Error.Message, tc.wantText) {
				t.Fatalf("envelope = %+v, want code %q mentioning %q", envelope, tc.wantCode, tc.wantText)
			}
		})
	}
}

// Probe and capture start refuse the record selectors they cannot honour, and
// their help lists only the ones they take.
func TestDebugProbeAndCaptureRefuseSelectorsTheyIgnore(t *testing.T) {
	resetDebugTestState(t)
	ignored := map[string]func(*debugSelectorFlagValues){
		"profile":      func(f *debugSelectorFlagValues) { f.profile = "work" },
		"session":      func(f *debugSelectorFlagValues) { f.sessionID = "s-1" },
		"asl-run":      func(f *debugSelectorFlagValues) { f.aslRunID = "run-1" },
		"event":        func(f *debugSelectorFlagValues) { f.event = "Stop" },
		"source-event": func(f *debugSelectorFlagValues) { f.sourceEventID = "src-1" },
		"outcome":      func(f *debugSelectorFlagValues) { f.outcomes = []string{"error"} },
		"stage":        func(f *debugSelectorFlagValues) { f.stages = []string{"hook.delivery"} },
		"after":        func(f *debugSelectorFlagValues) { f.after = "30m" },
		"before":       func(f *debugSelectorFlagValues) { f.before = "30m" },
	}
	for name, set := range ignored {
		debugSelectorFlags = debugSelectorFlagValues{windowID: 7}
		set(&debugSelectorFlags)
		probed := false
		debugProbeClient = func() debugger.ProbeClient { probed = true; return debugTestProbe{} }
		err := debugProbeCmd.RunE(debugProbeCmd, nil)
		if err == nil || !strings.Contains(err.Error(), "--"+name) || probed {
			t.Errorf("probe with --%s: err = %v, probed = %t", name, err, probed)
		}
	}

	root := &cobra.Command{Use: "amx"}
	root.AddCommand(debugCmd)
	t.Cleanup(func() { root.RemoveCommand(debugCmd) })
	for leaf, want := range map[*cobra.Command][]string{debugProbeCmd: debugProbeSelectors, debugCaptureStartCmd: debugCaptureSelectors} {
		usage := inheritedFlagUsages(leaf, true)
		for _, flag := range []string{"harness", "profile", "window", "trace", "request", "stage", "after"} {
			listed := strings.Contains(usage, "--"+flag+" ")
			if listed != slices.Contains(want, flag) {
				t.Errorf("%s help lists --%s = %t:\n%s", leaf.CommandPath(), flag, listed, usage)
			}
		}
	}
}
