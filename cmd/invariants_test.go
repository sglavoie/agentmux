package cmd

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon"
)

var invariantNow = time.Unix(1_700_000_000, 0)

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
