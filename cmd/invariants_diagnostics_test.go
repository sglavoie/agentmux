package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestInvariantUnknownProducerFactWarnings(t *testing.T) {
	cases := []struct {
		name string
		fact protocol.SourceFactSnapshot
		want bool
	}{
		{"legacy window icon warns", protocol.SourceFactSnapshot{Kind: protocol.SourceLegacy, Key: "win:7", Icon: "bolt"}, true},
		{"legacy alternate window key warns", protocol.SourceFactSnapshot{Kind: protocol.SourceLegacy, Key: "window:7", Icon: "stop"}, true},
		{"legacy window lease warns", protocol.SourceFactSnapshot{Kind: protocol.SourceLegacy, Key: "win:7", LeaseLive: true}, true},
		{"acknowledged legacy window passes", protocol.SourceFactSnapshot{Kind: protocol.SourceLegacy, Key: "win:7", Icon: "bolt", Acked: true}, false},
		{"inert legacy window passes", protocol.SourceFactSnapshot{Kind: protocol.SourceLegacy, Key: "win:7"}, false},
		{"legacy subagent bucket passes", protocol.SourceFactSnapshot{Kind: protocol.SourceLegacy, Key: "session:win:7", Icon: "subagent"}, false},
		{"known producer passes", protocol.SourceFactSnapshot{Kind: protocol.SourceHarness, Key: "claude:s1", Icon: "bolt"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := protocol.EventsExport{WindowMetadata: map[int]protocol.WindowDebugMetadata{7: {
				Shadow: protocol.WindowDebugShadow{Facts: []protocol.SourceFactSnapshot{tc.fact}},
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
	valid := protocol.SourceFactSnapshot{
		Kind: protocol.SourceHarness, Key: "claude:s1", Icon: "bolt",
		HarnessProcessHarness: "claude", HarnessProcessPID: 4242,
		HarnessProcessStartToken: "Thu Jul 16 10:20:30 2026",
	}
	cases := []struct {
		name string
		fact protocol.SourceFactSnapshot
		want bool
	}{
		{"complete identity passes", valid, false},
		{"absent identity passes", protocol.SourceFactSnapshot{Kind: protocol.SourceHarness, Key: "claude:s1"}, false},
		{"partial identity fails", protocol.SourceFactSnapshot{Kind: protocol.SourceHarness, Key: "claude:s1", HarnessProcessPID: 4242}, true},
		{"producer mismatch fails", protocol.SourceFactSnapshot{
			Kind: protocol.SourceHarness, Key: "claude:s1", HarnessProcessHarness: "codex", HarnessProcessPID: 4242,
			HarnessProcessStartToken: "Thu Jul 16 10:20:30 2026",
		}, true},
		{"ASL ownership fails", protocol.SourceFactSnapshot{
			Kind: protocol.SourceASL, Key: "run-1", HarnessProcessHarness: "claude", HarnessProcessPID: 4242,
			HarnessProcessStartToken: "Thu Jul 16 10:20:30 2026",
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := protocol.EventsExport{WindowMetadata: map[int]protocol.WindowDebugMetadata{7: {
				Shadow: protocol.WindowDebugShadow{Facts: []protocol.SourceFactSnapshot{tc.fact}},
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
	facts := func(n int) []protocol.SourceFactSnapshot {
		out := make([]protocol.SourceFactSnapshot, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, protocol.SourceFactSnapshot{
				Kind: protocol.SourceHarness, Key: "session-" + strconv.Itoa(i),
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
			snapshot := protocol.EventsExport{WindowMetadata: map[int]protocol.WindowDebugMetadata{7: {
				Shadow: protocol.WindowDebugShadow{Facts: facts(tc.count)},
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
	facts := make([]protocol.SourceFactSnapshot, 0, 108)
	for i := 0; i < 48; i++ {
		facts = append(facts, protocol.SourceFactSnapshot{Kind: protocol.SourceHarness, Key: "acked-" + strconv.Itoa(i), Acked: true})
	}
	for i := 0; i < 20; i++ {
		facts = append(facts,
			protocol.SourceFactSnapshot{Kind: protocol.SourceHarness, Key: "live-" + strconv.Itoa(i), Icon: "bolt"},
			protocol.SourceFactSnapshot{Kind: protocol.SourceHarness, Key: "terminal-" + strconv.Itoa(i), Acked: true, Terminated: true, Icon: "stop"},
			protocol.SourceFactSnapshot{Kind: protocol.SourceASL, Key: "lease-" + strconv.Itoa(i), Acked: true, LeaseLive: true},
		)
	}
	snapshot := protocol.EventsExport{WindowMetadata: map[int]protocol.WindowDebugMetadata{7: {
		Shadow: protocol.WindowDebugShadow{Facts: facts},
	}}}
	findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
	if got := findingsByCheck(findings, checkAcknowledgedPressure); len(got) != 0 {
		t.Fatalf("acknowledged_fact_pressure = %+v; unacknowledged facts, terminal history, and live ASL leases must be excluded", got)
	}
}

func TestInvariantShadowCurrentMismatch(t *testing.T) {
	harnessFact := protocol.SourceFactSnapshot{Kind: protocol.SourceHarness, Key: "s", Action: "active", Icon: "bolt"}
	aslFact := protocol.SourceFactSnapshot{Kind: protocol.SourceASL, Key: "run", Phase: daemon.ASLPhaseBetween, LeaseLive: true}
	cases := []struct {
		name string
		meta protocol.WindowDebugMetadata
		want bool
	}{
		{
			"icon disagreement fails",
			protocol.WindowDebugMetadata{
				Renderer: protocol.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true},
				Shadow:   protocol.WindowDebugShadow{EffectiveIcon: "", EffectiveGen: 3, Facts: []protocol.SourceFactSnapshot{harnessFact}},
			},
			true,
		},
		{
			"agreement passes",
			protocol.WindowDebugMetadata{
				Renderer: protocol.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true},
				Shadow:   protocol.WindowDebugShadow{EffectiveIcon: "bolt", EffectiveGen: 3, Facts: []protocol.SourceFactSnapshot{harnessFact}},
			},
			false,
		},
		{
			"no shadow knowledge passes even with a scalar icon",
			protocol.WindowDebugMetadata{
				Renderer: protocol.WindowDebugRenderer{DesiredIcon: "stop", StateSet: true},
			},
			false,
		},
		{
			"loop disagreement with an ASL fact fails",
			protocol.WindowDebugMetadata{
				Renderer: protocol.WindowDebugRenderer{DesiredIcon: "", StateSet: true, Looping: false, LoopingSet: true},
				Shadow: protocol.WindowDebugShadow{
					EffectiveIcon: "", EffectiveLooping: true, EffectiveGen: 2,
					Facts: []protocol.SourceFactSnapshot{aslFact},
				},
			},
			true,
		},
		{
			"loop disagreement without ASL knowledge passes (discovery owns 🔁)",
			protocol.WindowDebugMetadata{
				Renderer: protocol.WindowDebugRenderer{DesiredIcon: "", StateSet: true, Looping: true, LoopingSet: true},
				Shadow: protocol.WindowDebugShadow{
					EffectiveIcon: "", EffectiveLooping: false, EffectiveGen: 2,
					Facts: []protocol.SourceFactSnapshot{harnessFact},
				},
			},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := protocol.EventsExport{WindowMetadata: map[int]protocol.WindowDebugMetadata{7: tc.meta}}
			findings := evaluateDaemonInvariants(snapshot, healthyTestHealth(doctorTestBuild), doctorTestBuild, nil, invariantNow)
			got := findingsByCheck(findings, checkShadowCurrentMismatch)
			if (len(got) > 0) != tc.want {
				t.Fatalf("shadow_current_mismatch findings = %+v; want failure=%t", got, tc.want)
			}
		})
	}
}

func TestInvariantRenderGenerationDriftAndQueueSaturation(t *testing.T) {
	snapshot := protocol.EventsExport{WindowMetadata: map[int]protocol.WindowDebugMetadata{
		7: {Renderer: protocol.WindowDebugRenderer{DirtyAgeSeconds: 5, QueuedGen: 9, RenderedGen: 7, FailedGen: 9}},
		8: {Renderer: protocol.WindowDebugRenderer{DirtyAgeSeconds: 4}},
	}}
	health := healthyTestHealth(doctorTestBuild)
	health.Metrics = &protocol.MetricsSnapshot{QueueFull: 2}
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
	over := make([]string, protocol.MaxRenderCausalIDs+1)
	for i := range over {
		over[i] = fmt.Sprintf("trace-%d", i)
	}
	snapshot := protocol.EventsExport{WindowMetadata: map[int]protocol.WindowDebugMetadata{
		7: {Renderer: protocol.WindowDebugRenderer{Generations: []protocol.RenderGenerationTrace{{Generation: 9, TraceIDs: over}}}},
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
			findings := evaluateDaemonInvariants(protocol.EventsExport{}, tc.health, doctorTestBuild, nil, invariantNow)
			got := findingsByCheck(findings, checkBuildMismatch)
			if (len(got) > 0) != tc.want {
				t.Fatalf("build_mismatch findings = %+v; want failure=%t", got, tc.want)
			}
		})
	}
}

func TestInvariantTombstonesAndAckedFactsAreInformational(t *testing.T) {
	snapshot := protocol.EventsExport{WindowMetadata: map[int]protocol.WindowDebugMetadata{
		// A tombstoned window is exempt from failure checks even when its
		// retained facts would otherwise violate the ASL invariant.
		7: {
			Watchdog: protocol.WindowDebugWatchdog{ClosedAt: invariantNow.Add(-time.Hour).Unix()},
			Renderer: protocol.WindowDebugRenderer{DesiredIcon: "", StateSet: true},
			Shadow: protocol.WindowDebugShadow{EffectiveGen: 1, Facts: []protocol.SourceFactSnapshot{{
				Kind: protocol.SourceASL, Key: "run", Phase: daemon.ASLPhaseTurn, LeaseLive: true,
			}}},
		},
		8: {
			Renderer: protocol.WindowDebugRenderer{DesiredIcon: "", StateSet: true},
			Shadow: protocol.WindowDebugShadow{EffectiveGen: 2, Facts: []protocol.SourceFactSnapshot{{
				Kind: protocol.SourceHarness, Key: "s", Action: "stop", Icon: "stop", Acked: true,
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
	snapshot := protocol.EventsExport{WindowMetadata: map[int]protocol.WindowDebugMetadata{
		7: {Durable: protocol.WindowDebugDurable{Harness: "claude"}},
	}}
	liveByID := map[int]protocol.WindowLiveMetadata{
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
