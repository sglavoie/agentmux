package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// Fleet budgets: what the daemon's periodic work for tmux sessions costs with
// one, ten, and thirty agents, idle and under sustained output.
//
// The work is the same two things at any size. A maintenance pass reconciles
// the registry with the private server every rendering.Interval, and the
// screen-probe loop reads the screen of each agent it has a question about,
// one pane after another, on the same interval. What these tests measure is
// how each grows with the fleet, against a real private tmux server running
// the production configuration and whatever tmux the runtime resolves (the
// reliability-stress recipe points it at the packaged runtime when one is
// built), with stand-in agents: `sleep` for an idle fleet, `yes` for one
// under output, which is every pane printing as fast as the server will take
// it and so the worst a real agent can do.
//
// Two kinds of assertion, and they are different in kind:
//
//   - The bounds are exact and hold at any load: a pass is one tmux client
//     however many agents there are, and no more than tmux.MaxClientCommands
//     clients ever run at once.
//   - The budgets are wall-clock ceilings with room over what was measured
//     (the table below), so they catch a regression in kind (a pass that
//     starts one client per agent, a read that scans the history) and not a
//     machine that was busy. Like the budgets in performance_test.go they run
//     alone, under `just reliability-stress`, with AMX_PERF_TESTS=1.
//
// Measured 2026-10-10 on a Mac Studio (Apple M2 Ultra, 24 cores, macOS
// 27.0.1) against the packaged tmux 3.7c; p95 over 30 passes and 10 sweeps,
// the server's resident memory at the end:
//
//	fleet        pass      read      sweep     4-wide round   tick      server
//	 1 idle       5.5ms     5.3ms      5.3ms     5.1ms         10.8ms     4 MiB
//	 1 output     6.0ms     6.0ms      6.0ms     6.2ms         12.0ms    29 MiB
//	10 idle       5.6ms     5.3ms     50.2ms    18.3ms         55.8ms     5 MiB
//	10 output     8.3ms     7.2ms     68.4ms    25.7ms         76.7ms   254 MiB
//	30 idle       6.8ms     5.4ms    153.7ms    48.6ms        160.5ms     5 MiB
//	30 output    14.6ms    10.4ms    293.4ms    86.4ms        308.0ms   703 MiB
//
// A pass is one tmux client at every size. A read is one client too, about
// 5ms of which is starting the process, so a sweep grows with the fleet and
// nothing else does. The server's memory is its panes' history: about 24 MiB
// for each pane that has printed its 50,000 lines.
//
// The recorded run is docs/acceptance/ghostty-desktop/session-18/fleet-budgets.log.

const (
	// fleetPassBudget is the ceiling on one maintenance pass (reconcile,
	// retire, label, checkpoint) at any measured fleet size.
	fleetPassBudget = 250 * time.Millisecond
	// fleetReadBudget is the ceiling on one screen read of one pane.
	fleetReadBudget = 150 * time.Millisecond
	// fleetTickBudget is the ceiling on everything one tick can ask for at
	// once: a maintenance pass plus a screen read of every agent in the
	// fleet. It is the interval itself, less a margin: work that did not fit
	// in a tick would fall behind forever.
	fleetTickBudget = rendering.Interval * 3 / 4
	// fleetServerMemoryPerSession is the ceiling on the tmux server's
	// resident memory per agent, over fleetServerMemoryBase for the server
	// itself. A pane's memory is its history, and the history is capped
	// (history-limit in the server configuration): a printing agent fills it
	// within a second and then stays there.
	fleetServerMemoryPerSession = 48.0
	fleetServerMemoryBase       = 32.0
	// fleetPasses and fleetSweeps are how many of each are timed.
	fleetPasses = 30
	fleetSweeps = 10
)

// fleetMeasurement is one fleet's numbers, as logged and as recorded.
type fleetMeasurement struct {
	Sessions int    `json:"sessions"`
	Workload string `json:"workload"`
	Tmux     string `json:"tmux"`

	LaunchMs float64 `json:"launch_ms"`

	PassP50Ms float64 `json:"pass_p50_ms"`
	PassP95Ms float64 `json:"pass_p95_ms"`
	PassMaxMs float64 `json:"pass_max_ms"`
	// ClientsPerPass is tmux client processes started per maintenance pass.
	ClientsPerPass float64 `json:"clients_per_pass"`

	ReadP50Ms float64 `json:"read_p50_ms"`
	ReadP95Ms float64 `json:"read_p95_ms"`
	ReadMaxMs float64 `json:"read_max_ms"`
	// SweepP95Ms is a read of every pane, one after another, as the probe
	// loop makes them.
	SweepP95Ms float64 `json:"sweep_p95_ms"`
	// RoundP95Ms is a read of every pane asked for at once, as an overview
	// asks, admitted MaxClientCommands at a time.
	RoundP95Ms float64 `json:"round_p95_ms"`
	// TickP95Ms is PassP95Ms + SweepP95Ms: the most one tick asks for.
	TickP95Ms float64 `json:"tick_p95_ms"`

	MaxClientsAtOnce int    `json:"max_clients_at_once"`
	ClientLimit      int    `json:"client_limit"`
	ClientTimeouts   uint64 `json:"client_timeouts"`

	// ServerCPUPercent is the tmux server's CPU over the measured window,
	// and ServerRSSMiB its resident memory at the end of it.
	ServerCPUPercent float64 `json:"server_cpu_percent"`
	ServerRSSMiB     float64 `json:"server_rss_mib"`
	// ClientCPUMs is the CPU the tmux clients of the window used, in total.
	ClientCPUMs float64 `json:"client_cpu_ms"`
	WindowMs    float64 `json:"window_ms"`
}

func msOf(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func percentile(durations []time.Duration, p float64) time.Duration {
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted))*p+0.999999) - 1
	return sorted[min(max(index, 0), len(sorted)-1)]
}

// processCPU reads a process's accumulated CPU time and resident size.
func processCPU(t *testing.T, pid int) (time.Duration, float64) {
	t.Helper()
	out, err := exec.Command("ps", "-o", "cputime=,rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps %d: %v", pid, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		t.Fatalf("ps %d printed %q", pid, out)
	}
	// [[dd-]hh:]mm:ss.cc
	var total time.Duration
	clock := fields[0]
	if days, rest, ok := strings.Cut(clock, "-"); ok {
		d, _ := strconv.Atoi(days)
		total += time.Duration(d) * 24 * time.Hour
		clock = rest
	}
	parts := strings.Split(clock, ":")
	units := []time.Duration{time.Second, time.Minute, time.Hour}
	for i := range parts {
		value, err := strconv.ParseFloat(parts[len(parts)-1-i], 64)
		if err != nil || i >= len(units) {
			t.Fatalf("ps %d cputime %q", pid, fields[0])
		}
		total += time.Duration(value * float64(units[i]))
	}
	rss, _ := strconv.ParseFloat(fields[1], 64)
	return total, rss / 1024
}

func childCPU() time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &usage); err != nil {
		return 0
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// measureFleet launches sessions stand-in agents and times the daemon's
// periodic work over them.
func measureFleet(t *testing.T, sessions int, workload string) fleetMeasurement {
	t.Helper()
	w := newRuntimeFaultWorld(t)
	if workload == "output" {
		// Every launch runs `$SHELL -lic <harness command>`; this shell is the
		// agent, and it never stops printing.
		shell := filepath.Join(t.TempDir(), "printing-agent")
		line := strings.Repeat("agent output ", 6)
		if err := os.WriteFile(shell, []byte("#!/bin/sh\nexec yes '"+line+"'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SHELL", shell)
	}
	h, _ := w.startDaemon(time.Now())
	h.SetScreenObserver(w.tmux.Backend)
	m := fleetMeasurement{Sessions: sessions, Workload: workload, ClientLimit: tmux.MaxClientCommands}
	if out, err := exec.Command(w.tmux.Server.Binary(), "-V").Output(); err == nil {
		m.Tmux = strings.TrimSpace(string(out))
	}

	launching := time.Now()
	agents := make([]faultAgent, sessions)
	for i := range agents {
		agents[i] = w.launch(fmt.Sprintf("fleet-%02d", i))
	}
	m.LaunchMs = msOf(time.Since(launching))

	survey := w.survey()
	serverPID, err := strconv.Atoi(strings.SplitN(survey.Instance.Generation, "-", 2)[0])
	if err != nil {
		t.Fatalf("server generation %q names no PID", survey.Instance.Generation)
	}
	// One pass and one sweep outside the window: the first of each pays for
	// what is paid once.
	now := time.Now()
	h.maintainRuntimeSessions(now)
	for _, agent := range agents {
		if _, err := h.screenTextCacheFor().Observe(agent.launched.Pane); err != nil {
			t.Fatalf("warm-up read: %v", err)
		}
	}
	if workload == "output" {
		// Let every pane fill its screen and start scrolling.
		time.Sleep(time.Second)
	}

	windowStart := time.Now()
	serverCPUBefore, _ := processCPU(t, serverPID)
	clientCPUBefore := childCPU()
	before := tmux.Stats()

	passes := make([]time.Duration, fleetPasses)
	for i := range passes {
		now = now.Add(runtimeReconcileInterval)
		started := time.Now()
		h.maintainRuntimeSessions(now)
		passes[i] = time.Since(started)
	}
	afterPasses := tmux.Stats()
	m.ClientsPerPass = float64(afterPasses.Commands-before.Commands) / fleetPasses

	var reads, sweeps, rounds []time.Duration
	for sweep := 0; sweep < fleetSweeps; sweep++ {
		// As the probe loop reads: one cache for the tick, one goroutine.
		screens := h.screenTextCacheFor()
		sweepStarted := time.Now()
		for _, agent := range agents {
			started := time.Now()
			screen, err := screens.Observe(agent.launched.Pane)
			reads = append(reads, time.Since(started))
			if err != nil {
				t.Fatalf("read of %s: %v", agent.id, err)
			}
			if workload == "output" && !strings.Contains(screen.Text, "agent output") {
				t.Fatalf("read of %s shows no output: %q", agent.id, screen.Text)
			}
		}
		sweeps = append(sweeps, time.Since(sweepStarted))

		// As an overview reads: every pane asked for at once.
		roundStarted := time.Now()
		var wg sync.WaitGroup
		failed := make(chan error, len(agents))
		for _, agent := range agents {
			wg.Add(1)
			go func(pane backend.Ref) {
				defer wg.Done()
				if _, err := w.tmux.Backend.Observe(pane); err != nil {
					failed <- err
				}
			}(agent.launched.Pane)
		}
		wg.Wait()
		close(failed)
		for err := range failed {
			t.Fatalf("concurrent read: %v", err)
		}
		rounds = append(rounds, time.Since(roundStarted))
	}

	window := time.Since(windowStart)
	serverCPUAfter, rss := processCPU(t, serverPID)
	after := tmux.Stats()

	m.PassP50Ms, m.PassP95Ms, m.PassMaxMs = msOf(percentile(passes, 0.5)), msOf(percentile(passes, 0.95)), msOf(percentile(passes, 1))
	m.ReadP50Ms, m.ReadP95Ms, m.ReadMaxMs = msOf(percentile(reads, 0.5)), msOf(percentile(reads, 0.95)), msOf(percentile(reads, 1))
	m.SweepP95Ms = msOf(percentile(sweeps, 0.95))
	m.RoundP95Ms = msOf(percentile(rounds, 0.95))
	m.TickP95Ms = m.PassP95Ms + m.SweepP95Ms
	m.MaxClientsAtOnce = after.MaxInFlight
	m.ClientTimeouts = after.Timeouts - before.Timeouts
	m.WindowMs = msOf(window)
	m.ServerCPUPercent = 100 * float64(serverCPUAfter-serverCPUBefore) / float64(window)
	m.ServerRSSMiB = rss
	m.ClientCPUMs = msOf(childCPU() - clientCPUBefore)

	health := runtimeHealthOf(t, h)
	if health.Sessions.Live != sessions || health.Reconcile.Failures != 0 || health.Capture.Failures != 0 {
		t.Errorf("health after the window: sessions = %+v reconcile = %+v capture = %+v", health.Sessions, health.Reconcile, health.Capture)
	}
	for _, agent := range agents {
		w.requireSameAgent(agent, "the measured window")
	}
	return m
}

func TestPerformance_FleetBudgets(t *testing.T) {
	requirePerformanceOptIn(t)

	var recorded []fleetMeasurement
	for _, sessions := range []int{1, 10, 30} {
		for _, workload := range []string{"idle", "output"} {
			t.Run(fmt.Sprintf("%d-sessions-%s", sessions, workload), func(t *testing.T) {
				m := measureFleet(t, sessions, workload)
				recorded = append(recorded, m)
				t.Logf("%s: launch %.0fms; pass p50 %.1fms p95 %.1fms max %.1fms (%.2f tmux clients per pass); read p50 %.1fms p95 %.1fms max %.1fms; sweep of %d p95 %.1fms; %d-wide round p95 %.1fms; tick p95 %.1fms of %s; at most %d clients at once; server %.1f%% CPU, %.1f MiB; clients %.0fms CPU over %.0fms",
					m.Tmux, m.LaunchMs, m.PassP50Ms, m.PassP95Ms, m.PassMaxMs, m.ClientsPerPass,
					m.ReadP50Ms, m.ReadP95Ms, m.ReadMaxMs, sessions, m.SweepP95Ms, m.ClientLimit, m.RoundP95Ms,
					m.TickP95Ms, rendering.Interval, m.MaxClientsAtOnce, m.ServerCPUPercent, m.ServerRSSMiB, m.ClientCPUMs, m.WindowMs)

				// Bounds: exact, whatever the load.
				if m.ClientsPerPass > 1 {
					t.Errorf("a maintenance pass over %d sessions started %.2f tmux clients; it is one survey per server", sessions, m.ClientsPerPass)
				}
				if m.MaxClientsAtOnce > m.ClientLimit {
					t.Errorf("%d tmux clients ran at once; the limit is %d", m.MaxClientsAtOnce, m.ClientLimit)
				}
				if m.ClientTimeouts != 0 {
					t.Errorf("%d tmux clients timed out", m.ClientTimeouts)
				}
				if limit := fleetServerMemoryBase + fleetServerMemoryPerSession*float64(sessions); m.ServerRSSMiB > limit {
					t.Errorf("tmux server holds %.0f MiB for %d sessions; budget is %.0f MiB", m.ServerRSSMiB, sessions, limit)
				}
				// Budgets: ceilings over what was measured.
				if got := time.Duration(m.PassP95Ms * float64(time.Millisecond)); got > fleetPassBudget {
					t.Errorf("maintenance pass p95 = %s; budget is %s", got, fleetPassBudget)
				}
				if got := time.Duration(m.ReadP95Ms * float64(time.Millisecond)); got > fleetReadBudget {
					t.Errorf("screen read p95 = %s; budget is %s", got, fleetReadBudget)
				}
				if got := time.Duration(m.TickP95Ms * float64(time.Millisecond)); got > fleetTickBudget {
					t.Errorf("one tick's work (a pass and a read of all %d panes) p95 = %s; budget is %s", sessions, got, fleetTickBudget)
				}
			})
		}
	}

	// AMX_PERF_RECORD names a file the measurements are written to, which is
	// how a run becomes a record.
	if path := os.Getenv("AMX_PERF_RECORD"); path != "" && !t.Failed() {
		data, err := json.MarshalIndent(map[string]any{
			"measured_at": time.Now().UTC().Format(time.RFC3339),
			"budgets_ms": map[string]float64{
				"pass_p95": msOf(fleetPassBudget), "read_p95": msOf(fleetReadBudget), "tick_p95": msOf(fleetTickBudget),
			},
			"budget_server_mib_per_session": fleetServerMemoryPerSession, "budget_server_mib_base": fleetServerMemoryBase,
			"bounds":      map[string]any{"tmux_clients_per_pass": 1, "tmux_clients_at_once": tmux.MaxClientCommands},
			"interval_ms": msOf(rendering.Interval), "passes": fleetPasses, "sweeps": fleetSweeps,
			"fleets": recorded,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatalf("recording measurements: %v", err)
		}
	}
}
