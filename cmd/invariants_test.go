package cmd

import (
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
