package cmd

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon"
)

var invariantNow = time.Unix(1_700_000_000, 0)

func TestSourceTreeHasNoRetiredPlatformStacks(t *testing.T) {
	root := ".."
	retiredPackages := []string{
		"activity",
		"compatimport",
		"datastore",
		"datastorerecovery",
		"search",
		"workspace",
	}
	for _, name := range retiredPackages {
		directory := filepath.Join("internal", name)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(directory))); err == nil {
			t.Errorf("retired platform directory still exists: %s", directory)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", directory, err)
		}
	}

	retiredImports := make(map[string]bool, len(retiredPackages))
	for _, name := range retiredPackages {
		retiredImports["github.com/sglavoie/agentmux/internal/"+name] = true
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			for retiredImport := range retiredImports {
				if importPath == retiredImport || strings.HasPrefix(importPath, retiredImport+"/") {
					t.Errorf("%s imports retired platform package %s", path, importPath)
				}
			}
		}

		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && strings.Contains(value, "agent-mux.db") {
				t.Errorf("%s contains a retired database pathname", path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func healthyTestHealth(build buildinfo.Info) daemon.Health {
	b := build
	return daemon.Health{Alive: true, Ready: true, Build: &b, Metrics: &daemon.MetricsSnapshot{}}
}

func findingsByCheck(findings []doctorFinding, check string) []doctorFinding {
	out := make([]doctorFinding, 0)
	for _, f := range findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func TestInvariantASLActiveWorkMustBeVisible(t *testing.T) {
	aslFact := func(phase string) daemon.SourceFactSnapshot {
		return daemon.SourceFactSnapshot{
			Kind: daemon.SourceASL, Key: "run-1", RunID: "run-1", Phase: phase, LeaseLive: phase != "finished",
		}
	}
	cases := []struct {
		name    string
		phase   string
		desired string
		want    bool
	}{
		{"turn showing empty fails", daemon.ASLPhaseTurn, "", true},
		{"verification showing stop fails", daemon.ASLPhaseVerification, "stop", true},
		{"turn showing bolt passes", daemon.ASLPhaseTurn, "bolt", false},
		{"between showing empty passes", daemon.ASLPhaseBetween, "", false},
		{"paused showing empty passes", daemon.ASLPhasePaused, "", false},
		// A waiting run is blocked on a human, not executing: the active-work
		// invariant deliberately says nothing about what it renders.
		{"waiting showing empty passes", daemon.ASLPhaseWaiting, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{
				7: {
					Renderer: daemon.WindowDebugRenderer{DesiredIcon: tc.desired, StateSet: true},
					Shadow: daemon.WindowDebugShadow{
						EffectiveIcon: tc.desired, EffectiveGen: 1,
						Facts: []daemon.SourceFactSnapshot{aslFact(tc.phase)},
					},
				},
			}}
			findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
			got := findingsByCheck(findings, checkASLActiveState)
			if (len(got) > 0) != tc.want {
				t.Fatalf("asl_active_state findings = %+v; want failure=%t", got, tc.want)
			}
		})
	}
}

func TestInvariantLiveSubagentNeedsRobotOrHigherAttention(t *testing.T) {
	subagentFact := daemon.SourceFactSnapshot{
		Kind: daemon.SourceSubagent, Key: "agent-1", Action: "subagent", Icon: "subagent",
	}
	legacyBucket := daemon.SourceFactSnapshot{
		Kind: daemon.SourceLegacy, Key: "win:7:agents", Agents: 2, Action: "subagent", Icon: "subagent",
	}
	cases := []struct {
		name    string
		fact    daemon.SourceFactSnapshot
		desired string
		want    bool
	}{
		{"identified agent behind bolt fails", subagentFact, "bolt", true},
		{"legacy multiset behind empty fails", legacyBucket, "", true},
		{"agent behind robot passes", subagentFact, "subagent", false},
		{"agent behind key passes (higher attention)", subagentFact, "key", false},
		{"agent behind error passes", subagentFact, "error", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{
				7: {
					Renderer: daemon.WindowDebugRenderer{DesiredIcon: tc.desired, StateSet: true},
					Shadow: daemon.WindowDebugShadow{
						EffectiveIcon: tc.desired, EffectiveGen: 1,
						Facts: []daemon.SourceFactSnapshot{tc.fact},
					},
				},
			}}
			findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
			got := findingsByCheck(findings, checkSubagentVisibility)
			if (len(got) > 0) != tc.want {
				t.Fatalf("subagent_visibility findings = %+v; want failure=%t", got, tc.want)
			}
		})
	}
}

func TestInvariantRepeatedActiveAfterTerminalSuppressions(t *testing.T) {
	event := func(at int64) daemon.SessionEvent {
		return daemon.SessionEvent{At: at, Suppressed: true, SuppressionReason: "active_after_terminal"}
	}
	recent := invariantNow.Add(-time.Minute).Unix()
	stale := invariantNow.Add(-2 * activeAfterTerminalGracePeriod).Unix()
	cases := []struct {
		name   string
		events []daemon.SessionEvent
		want   bool
	}{
		{"three recent fail", []daemon.SessionEvent{event(recent), event(recent), event(recent)}, true},
		{"two recent pass", []daemon.SessionEvent{event(recent), event(recent)}, false},
		{"three outside grace pass", []daemon.SessionEvent{event(stale), event(stale), event(stale)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := daemon.EventsExport{
				EventsByWindow: map[int][]daemon.SessionEvent{7: tc.events},
				WindowMetadata: map[int]daemon.WindowDebugMetadata{7: {}},
			}
			findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
			got := findingsByCheck(findings, checkActiveAfterTerminal)
			if (len(got) > 0) != tc.want {
				t.Fatalf("active_after_terminal findings = %+v; want failure=%t", got, tc.want)
			}
		})
	}
}

// TestInvariantStopHoldStuckWarnsPastTwiceTheTimeout pins the one regression
// mode the confirmed-idle gate introduces: a hold that outlives its own hard
// deadline leaves the tab active for a stop that never paints. It is a warning
// because the tab is still showing the state its harness last reported, so the
// healthy verdict must not move.
func TestInvariantStopHoldStuckWarnsPastTwiceTheTimeout(t *testing.T) {
	const timeout = 15 * time.Second
	hold := func(armedAgo time.Duration, class string) *daemon.PendingTerminalStop {
		armedAt := invariantNow.Add(-armedAgo)
		return &daemon.PendingTerminalStop{
			ArmedAt:    armedAt.Unix(),
			QuietUntil: armedAt.Add(6 * time.Second).Unix(),
			Deadline:   armedAt.Add(timeout).Unix(),
			Class:      class,
		}
	}
	cases := []struct {
		name string
		hold *daemon.PendingTerminalStop
		want bool
	}{
		{"no hold passes", nil, false},
		{"inside the deadline passes", hold(10*time.Second, daemon.StopHoldClassTerminal), false},
		{"past the deadline but inside twice the timeout passes", hold(20*time.Second, daemon.StopHoldClassTerminal), false},
		{"exactly twice the timeout passes", hold(2*timeout, daemon.StopHoldClassTerminal), false},
		{"past twice the timeout warns", hold(2*timeout+time.Second, daemon.StopHoldClassTerminal), true},
		{"reset class is never stuck", hold(time.Hour, daemon.StopHoldClassReset), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{
				7: {
					Renderer: daemon.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true},
					Shadow: daemon.WindowDebugShadow{
						EffectiveIcon: "bolt", EffectiveGen: 1, PendingTerminalStop: tc.hold,
					},
				},
			}}
			findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
			got := findingsByCheck(findings, checkStopHoldStuck)
			if (len(got) > 0) != tc.want {
				t.Fatalf("stop_hold_stuck findings = %+v; want finding=%t", got, tc.want)
			}
			if tc.want && (got[0].Severity != severityWarning || got[0].WindowID != 7) {
				t.Fatalf("stop_hold_stuck finding = %+v; want a warning for window 7", got[0])
			}
			if n := countFindings(findings, severityFailure); n != 0 {
				t.Fatalf("failure findings = %d; a stuck hold must not affect the healthy verdict (%+v)", n, findings)
			}
		})
	}
}

func TestInvariantOrderBookOwnerMismatchBurstIsStructured(t *testing.T) {
	recent := invariantNow.Add(-time.Minute).Unix()
	events := make([]daemon.SessionEvent, 0, 19)
	events = append(events, daemon.SessionEvent{
		At: recent, Event: "UserPromptSubmit", Action: "active",
		Suppressed: true, SuppressionReason: "owner_mismatch",
	})
	for i := 0; i < 18; i++ {
		events = append(events, daemon.SessionEvent{
			At: recent, Event: "PreToolUse", Action: "active",
			Suppressed: true, SuppressionReason: "owner_mismatch",
		})
	}
	snapshot := daemon.EventsExport{
		EventsByWindow: map[int][]daemon.SessionEvent{7: events},
		WindowMetadata: map[int]daemon.WindowDebugMetadata{7: {
			Durable: daemon.WindowDebugDurable{SessionTitle: "OrderBook"},
		}},
	}

	findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
	prompt := findingsByCheck(findings, checkOwnerMismatchPrompt)
	if len(prompt) != 1 || prompt[0].Severity != severityFailure || prompt[0].WindowID != 7 ||
		!strings.Contains(prompt[0].Message, "1 prompt event") || !strings.Contains(prompt[0].Message, "15m0s") {
		t.Fatalf("owner_mismatch_prompt findings = %+v; want one 15-minute failure for window 7", prompt)
	}
	burst := findingsByCheck(findings, checkOwnerMismatchBurst)
	if len(burst) != 1 || burst[0].Severity != severityWarning || burst[0].WindowID != 7 ||
		!strings.Contains(burst[0].Message, "18 non-prompt event") || !strings.Contains(burst[0].Message, "15m0s") {
		t.Fatalf("owner_mismatch_burst findings = %+v; want one 15-minute warning for window 7", burst)
	}
}

func TestInvariantOwnerMismatchWindowAndRoutingBoundaries(t *testing.T) {
	mismatch := func(at int64, event string) daemon.SessionEvent {
		return daemon.SessionEvent{
			At: at, Event: event, Suppressed: true, SuppressionReason: "owner_mismatch",
		}
	}
	cutoff := invariantNow.Add(-doctorSuppressionWindow).Unix()
	recent := invariantNow.Add(-time.Minute).Unix()
	stale := cutoff - 1
	cases := []struct {
		name       string
		events     []daemon.SessionEvent
		wantPrompt bool
		wantBurst  bool
	}{
		{"one recent Claude prompt fails", []daemon.SessionEvent{mismatch(recent, "UserPromptSubmit")}, true, false},
		{"one recent Copilot prompt fails", []daemon.SessionEvent{mismatch(recent, "userPromptSubmitted")}, true, false},
		{"prompt exactly at cutoff fails", []daemon.SessionEvent{mismatch(cutoff, "UserPromptSubmit")}, true, false},
		{"prompt before cutoff passes", []daemon.SessionEvent{mismatch(stale, "UserPromptSubmit")}, false, false},
		{"two recent non-prompts pass", []daemon.SessionEvent{mismatch(recent, "PreToolUse"), mismatch(recent, "PostToolUse")}, false, false},
		{"three recent non-prompts warn", []daemon.SessionEvent{mismatch(recent, "PreToolUse"), mismatch(recent, "PostToolUse"), mismatch(recent, "Stop")}, false, true},
		{"three stale non-prompts pass", []daemon.SessionEvent{mismatch(stale, "PreToolUse"), mismatch(stale, "PostToolUse"), mismatch(stale, "Stop")}, false, false},
		{"other suppression reason passes", []daemon.SessionEvent{{At: recent, Event: "UserPromptSubmit", Suppressed: true, SuppressionReason: "guard_blocked"}}, false, false},
		{"correct owner route passes", []daemon.SessionEvent{{At: recent, Event: "UserPromptSubmit", RouteDecision: "owner", OriginWindowID: 91}}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := daemon.EventsExport{
				EventsByWindow: map[int][]daemon.SessionEvent{7: tc.events},
				WindowMetadata: map[int]daemon.WindowDebugMetadata{7: {}},
			}
			findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
			if got := len(findingsByCheck(findings, checkOwnerMismatchPrompt)) > 0; got != tc.wantPrompt {
				t.Fatalf("owner_mismatch_prompt=%t (%+v); want %t", got, findings, tc.wantPrompt)
			}
			if got := len(findingsByCheck(findings, checkOwnerMismatchBurst)) > 0; got != tc.wantBurst {
				t.Fatalf("owner_mismatch_burst=%t (%+v); want %t", got, findings, tc.wantBurst)
			}
		})
	}
}

func TestInvariantUnknownProducerFactWarnings(t *testing.T) {
	cases := []struct {
		name string
		fact daemon.SourceFactSnapshot
		want bool
	}{
		{"legacy window icon warns", daemon.SourceFactSnapshot{Kind: daemon.SourceLegacy, Key: "win:7", Icon: "bolt"}, true},
		{"legacy alternate window key warns", daemon.SourceFactSnapshot{Kind: daemon.SourceLegacy, Key: "window:7", Icon: "stop"}, true},
		{"legacy window lease warns", daemon.SourceFactSnapshot{Kind: daemon.SourceLegacy, Key: "win:7", LeaseLive: true}, true},
		{"acknowledged legacy window passes", daemon.SourceFactSnapshot{Kind: daemon.SourceLegacy, Key: "win:7", Icon: "bolt", Acked: true}, false},
		{"inert legacy window passes", daemon.SourceFactSnapshot{Kind: daemon.SourceLegacy, Key: "win:7"}, false},
		{"legacy subagent bucket passes", daemon.SourceFactSnapshot{Kind: daemon.SourceLegacy, Key: "session:win:7", Icon: "subagent"}, false},
		{"known producer passes", daemon.SourceFactSnapshot{Kind: daemon.SourceHarness, Key: "claude:s1", Icon: "bolt"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{7: {
				Shadow: daemon.WindowDebugShadow{Facts: []daemon.SourceFactSnapshot{tc.fact}},
			}}}
			findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
			got := findingsByCheck(findings, checkUnknownProducerFact)
			if (len(got) > 0) != tc.want {
				t.Fatalf("unknown_producer_fact findings = %+v; want warning=%t", got, tc.want)
			}
			if len(got) > 0 && got[0].Severity != severityWarning {
				t.Fatalf("unknown_producer_fact severity = %q; want warning", got[0].Severity)
			}
		})
	}
}

func TestInvariantMalformedProcessIdentityUsesPersistedFactsOnly(t *testing.T) {
	valid := daemon.SourceFactSnapshot{
		Kind: daemon.SourceHarness, Key: "claude:s1", Icon: "bolt",
		HarnessProcessHarness: "claude", HarnessProcessPID: 4242,
		HarnessProcessStartToken: "Thu Jul 16 10:20:30 2026",
	}
	cases := []struct {
		name string
		fact daemon.SourceFactSnapshot
		want bool
	}{
		{"complete identity passes", valid, false},
		{"absent identity passes", daemon.SourceFactSnapshot{Kind: daemon.SourceHarness, Key: "claude:s1"}, false},
		{"partial identity fails", daemon.SourceFactSnapshot{Kind: daemon.SourceHarness, Key: "claude:s1", HarnessProcessPID: 4242}, true},
		{"producer mismatch fails", daemon.SourceFactSnapshot{
			Kind: daemon.SourceHarness, Key: "claude:s1", HarnessProcessHarness: "codex", HarnessProcessPID: 4242,
			HarnessProcessStartToken: "Thu Jul 16 10:20:30 2026",
		}, true},
		{"ASL ownership fails", daemon.SourceFactSnapshot{
			Kind: daemon.SourceASL, Key: "run-1", HarnessProcessHarness: "claude", HarnessProcessPID: 4242,
			HarnessProcessStartToken: "Thu Jul 16 10:20:30 2026",
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{7: {
				Shadow: daemon.WindowDebugShadow{Facts: []daemon.SourceFactSnapshot{tc.fact}},
			}}}
			findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
			got := findingsByCheck(findings, checkMalformedProcessIdentity)
			if (len(got) > 0) != tc.want {
				t.Fatalf("malformed_process_identity findings = %+v; want failure=%t", got, tc.want)
			}
			if len(got) > 0 && got[0].Severity != severityFailure {
				t.Fatalf("malformed_process_identity severity = %q; want failure", got[0].Severity)
			}
		})
	}
}

func TestInvariantAcknowledgedFactPressureBoundaries(t *testing.T) {
	facts := func(n int) []daemon.SourceFactSnapshot {
		out := make([]daemon.SourceFactSnapshot, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, daemon.SourceFactSnapshot{
				Kind: daemon.SourceHarness, Key: "session-" + strconv.Itoa(i),
				Action: "active", Icon: "bolt", Acked: true, AckedAt: invariantNow.Unix(),
			})
		}
		return out
	}
	cases := []struct {
		name     string
		count    int
		severity string
	}{
		{"48 is informational only", 48, ""},
		{"49 warns", 49, severityWarning},
		{"64 warns", 64, severityWarning},
		{"65 fails", 65, severityFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{7: {
				Shadow: daemon.WindowDebugShadow{Facts: facts(tc.count)},
			}}}
			findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
			pressure := findingsByCheck(findings, checkAcknowledgedPressure)
			if tc.severity == "" {
				if len(pressure) != 0 {
					t.Fatalf("acknowledged_fact_pressure = %+v; want none", pressure)
				}
				return
			}
			if len(pressure) != 1 || pressure[0].Severity != tc.severity ||
				!strings.Contains(pressure[0].Message, strconv.Itoa(tc.count)+" evictable") {
				t.Fatalf("acknowledged_fact_pressure = %+v; want one %s finding for %d", pressure, tc.severity, tc.count)
			}
		})
	}
}

func TestInvariantAcknowledgedFactPressureExclusions(t *testing.T) {
	facts := make([]daemon.SourceFactSnapshot, 0, 108)
	for i := 0; i < 48; i++ {
		facts = append(facts, daemon.SourceFactSnapshot{Kind: daemon.SourceHarness, Key: "acked-" + strconv.Itoa(i), Acked: true})
	}
	for i := 0; i < 20; i++ {
		facts = append(facts,
			daemon.SourceFactSnapshot{Kind: daemon.SourceHarness, Key: "live-" + strconv.Itoa(i), Icon: "bolt"},
			daemon.SourceFactSnapshot{Kind: daemon.SourceHarness, Key: "terminal-" + strconv.Itoa(i), Acked: true, Terminated: true, Icon: "stop"},
			daemon.SourceFactSnapshot{Kind: daemon.SourceASL, Key: "lease-" + strconv.Itoa(i), Acked: true, LeaseLive: true},
		)
	}
	snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{7: {
		Shadow: daemon.WindowDebugShadow{Facts: facts},
	}}}
	findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
	if got := findingsByCheck(findings, checkAcknowledgedPressure); len(got) != 0 {
		t.Fatalf("acknowledged_fact_pressure = %+v; unacknowledged facts, terminal history, and live ASL leases must be excluded", got)
	}
}

func TestInvariantShadowCurrentMismatch(t *testing.T) {
	harnessFact := daemon.SourceFactSnapshot{Kind: daemon.SourceHarness, Key: "s", Action: "active", Icon: "bolt"}
	aslFact := daemon.SourceFactSnapshot{Kind: daemon.SourceASL, Key: "run", Phase: daemon.ASLPhaseBetween, LeaseLive: true}
	cases := []struct {
		name string
		meta daemon.WindowDebugMetadata
		want bool
	}{
		{
			"icon disagreement fails",
			daemon.WindowDebugMetadata{
				Renderer: daemon.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true},
				Shadow:   daemon.WindowDebugShadow{EffectiveIcon: "", EffectiveGen: 3, Facts: []daemon.SourceFactSnapshot{harnessFact}},
			},
			true,
		},
		{
			"agreement passes",
			daemon.WindowDebugMetadata{
				Renderer: daemon.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true},
				Shadow:   daemon.WindowDebugShadow{EffectiveIcon: "bolt", EffectiveGen: 3, Facts: []daemon.SourceFactSnapshot{harnessFact}},
			},
			false,
		},
		{
			"no shadow knowledge passes even with a scalar icon",
			daemon.WindowDebugMetadata{
				Renderer: daemon.WindowDebugRenderer{DesiredIcon: "stop", StateSet: true},
			},
			false,
		},
		{
			"loop disagreement with an ASL fact fails",
			daemon.WindowDebugMetadata{
				Renderer: daemon.WindowDebugRenderer{DesiredIcon: "", StateSet: true, Looping: false, LoopingSet: true},
				Shadow: daemon.WindowDebugShadow{
					EffectiveIcon: "", EffectiveLooping: true, EffectiveGen: 2,
					Facts: []daemon.SourceFactSnapshot{aslFact},
				},
			},
			true,
		},
		{
			"loop disagreement without ASL knowledge passes (discovery owns 🔁)",
			daemon.WindowDebugMetadata{
				Renderer: daemon.WindowDebugRenderer{DesiredIcon: "", StateSet: true, Looping: true, LoopingSet: true},
				Shadow: daemon.WindowDebugShadow{
					EffectiveIcon: "", EffectiveLooping: false, EffectiveGen: 2,
					Facts: []daemon.SourceFactSnapshot{harnessFact},
				},
			},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{7: tc.meta}}
			findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
			got := findingsByCheck(findings, checkShadowCurrentMismatch)
			if (len(got) > 0) != tc.want {
				t.Fatalf("shadow_current_mismatch findings = %+v; want failure=%t", got, tc.want)
			}
		})
	}
}

func TestInvariantRenderGenerationDriftAndQueueSaturation(t *testing.T) {
	snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{
		7: {Renderer: daemon.WindowDebugRenderer{DirtyAgeSeconds: 5, QueuedGen: 9, RenderedGen: 7, FailedGen: 9}},
		8: {Renderer: daemon.WindowDebugRenderer{DirtyAgeSeconds: 4}},
	}}
	health := healthyTestHealth(doctorTestBuild)
	health.Metrics = &daemon.MetricsSnapshot{QueueFull: 2}
	findings := evaluateDaemonInvariants(snapshot, health, doctorTestBuild, nil, invariantNow)

	drift := findingsByCheck(findings, checkRenderGenerationDrift)
	if len(drift) != 1 || drift[0].WindowID != 7 {
		t.Fatalf("render_generation_drift findings = %+v; want exactly window 7", drift)
	}
	if got := findingsByCheck(findings, checkQueueSaturation); len(got) != 1 {
		t.Fatalf("queue_saturation findings = %+v; want one", got)
	}
}

func TestInvariantRenderCausalIDsAreBounded(t *testing.T) {
	over := make([]string, daemon.MaxRenderCausalIDs+1)
	for i := range over {
		over[i] = fmt.Sprintf("trace-%d", i)
	}
	snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{
		7: {Renderer: daemon.WindowDebugRenderer{Generations: []daemon.RenderGenerationTrace{{Generation: 9, TraceIDs: over}}}},
	}}
	findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
	got := findingsByCheck(findings, checkRenderCausalBound)
	if len(got) != 1 || got[0].WindowID != 7 {
		t.Fatalf("render_causal_bound findings = %+v; want exactly window 7", got)
	}
}

func TestInvariantBuildMismatch(t *testing.T) {
	other := doctorTestBuild
	other.BinaryHash = "hash-b"
	cases := []struct {
		name   string
		health daemon.Health
		want   bool
	}{
		{"same artifact passes", healthyTestHealth(doctorTestBuild), false},
		{"different hash fails", healthyTestHealth(other), true},
		{"missing daemon build identity fails", daemon.Health{Alive: true, Ready: true}, true},
		{"dead daemon skips the check", daemon.Health{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := evaluateDaemonInvariants(daemon.EventsExport{}, tc.health, doctorTestBuild, nil, invariantNow)
			got := findingsByCheck(findings, checkBuildMismatch)
			if (len(got) > 0) != tc.want {
				t.Fatalf("build_mismatch findings = %+v; want failure=%t", got, tc.want)
			}
		})
	}
}

func TestInvariantTombstonesAndAckedFactsAreInformational(t *testing.T) {
	snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{
		// A tombstoned window is exempt from failure checks even when its
		// retained facts would otherwise violate the ASL invariant.
		7: {
			Watchdog: daemon.WindowDebugWatchdog{ClosedAt: invariantNow.Add(-time.Hour).Unix()},
			Renderer: daemon.WindowDebugRenderer{DesiredIcon: "", StateSet: true},
			Shadow: daemon.WindowDebugShadow{EffectiveGen: 1, Facts: []daemon.SourceFactSnapshot{{
				Kind: daemon.SourceASL, Key: "run", Phase: daemon.ASLPhaseTurn, LeaseLive: true,
			}}},
		},
		8: {
			Renderer: daemon.WindowDebugRenderer{DesiredIcon: "", StateSet: true},
			Shadow: daemon.WindowDebugShadow{EffectiveGen: 2, Facts: []daemon.SourceFactSnapshot{{
				Kind: daemon.SourceHarness, Key: "s", Action: "stop", Icon: "stop", Acked: true,
			}}},
		},
	}}
	findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)

	if got := countFindings(findings, severityFailure); got != 0 {
		t.Fatalf("failures = %d (%+v); want 0", got, findings)
	}
	if got := findingsByCheck(findings, checkClosedTombstone); len(got) != 1 || got[0].Severity != severityInfo {
		t.Fatalf("closed_tombstone findings = %+v; want one info", got)
	}
	if got := findingsByCheck(findings, checkAcknowledgedState); len(got) != 1 || got[0].Severity != severityInfo {
		t.Fatalf("acknowledged_state findings = %+v; want one info", got)
	}
}

func TestInvariantHarnessDriftIsAWarning(t *testing.T) {
	snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{
		7: {Durable: daemon.WindowDebugDurable{Harness: "claude"}},
	}}
	liveByID := map[int]daemon.WindowLiveMetadata{
		7: {Found: true, Harness: "codex"},
	}
	findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, liveByID, invariantNow)
	got := findingsByCheck(findings, checkHarnessDrift)
	if len(got) != 1 || got[0].Severity != severityWarning {
		t.Fatalf("harness_drift findings = %+v; want one warning", got)
	}
	if countFindings(findings, severityFailure) != 0 {
		t.Fatalf("harness drift must not be a failure: %+v", findings)
	}
}
