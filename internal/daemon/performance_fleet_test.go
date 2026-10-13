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
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/gitstate"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// Fleet budgets: what the daemon's periodic work for tmux sessions costs with
// one, ten, and thirty agents, idle and under sustained output.
//
// The work is the same things at any size. A maintenance pass reconciles
// the registry with the private server every rendering.Interval, and the
// screen-probe loop reads the screen of each agent it has a question about,
// one pane after another, on the same interval. The watchdog judges every
// slot stale or not, and every profile silent or not, once a second, and
// starts a Git round over the sessions' directories when their answers have
// aged out; it needs no Kitty for a tmux session. What these tests measure is
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
//     however many agents there are, no more than tmux.MaxClientCommands
//     clients ever run at once, and the watchdog and its Git round start
//     none: a session's profile and directory are read once, with its label.
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
//
// The watchdog's share, measured 2026-10-13 on the same machine and tmux, in
// a daemon with no Kitty snapshot and every session active in a repository of
// its own; p95 over 30 ticks, and the slowest of 5 Git rounds with nothing
// cached:
//
//	fleet        watchdog tick   cold git round
//	 1 idle       0.009ms          16.2ms
//	 1 output     0.008ms          16.7ms
//	10 idle       0.013ms          51.6ms
//	10 output     0.014ms          54.9ms
//	30 idle       0.025ms         144.1ms
//	30 output     0.049ms         153.5ms
//
// A tick is daemon memory alone and starts no tmux client: a session's
// profile and directory were read with its label, by the first pass that saw
// it. A round is two git commands for each directory, four at a time, and is
// owed once every gitstate.DefaultTTL, off the watchdog.

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
	// fleetWatchdogBudget is the ceiling on one watchdog tick over the fleet:
	// the stale sweep of every slot, the hook-silence sweep of every profile,
	// and starting the Git round. It is daemon memory under stateMu, which
	// every hook acknowledgement waits on, so it is held to a small part of
	// the hook budget and not to the watchdog's own interval: less than one
	// subprocess costs, so a sweep that started one would not fit.
	fleetWatchdogBudget = 5 * time.Millisecond
	// fleetGitRoundBudget is the ceiling on one Git round over the fleet with
	// nothing cached: two git commands for each session's directory,
	// gitstate.DefaultWorkers at a time, off the watchdog and off stateMu. A
	// round is owed once per gitstate.DefaultTTL; one that outlasted the
	// watchdog's interval would hold the next tick's round back.
	fleetGitRoundBudget = watchdogInterval
	// fleetPasses and fleetSweeps are how many of each are timed, and
	// fleetWatchdogTicks and fleetGitRounds likewise.
	fleetPasses        = 30
	fleetSweeps        = 10
	fleetWatchdogTicks = 30
	fleetGitRounds     = 5
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
	// WatchdogP95Ms is one watchdog tick over the fleet, every slot active,
	// and ClientsPerWatchdog the tmux clients its ticks and Git rounds
	// started, which is none.
	WatchdogP95Ms      float64 `json:"watchdog_p95_ms"`
	ClientsPerWatchdog float64 `json:"clients_per_watchdog"`
	// GitRoundP50Ms and GitRoundMaxMs are a Git round over the directories
	// of the whole fleet with nothing cached.
	GitRoundP50Ms float64 `json:"git_round_p50_ms"`
	GitRoundMaxMs float64 `json:"git_round_max_ms"`

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
	// As Run installs it: a session's profile and directory are its pane's.
	h.SetRuntimeSessionLabeler(TmuxRuntimeSessionLabel)
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
	// what is paid once, which for a pass is reading each session's label
	// off its pane. It is dated an interval on so that it reconciles however
	// little time the launches took, and so adopts and labels the fleet.
	now := time.Now().Add(runtimeReconcileInterval)
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

	measureFleetWatchdog(t, h, agents, &m)

	health := runtimeHealthOf(t, h)
	if health.Sessions.Live != sessions || health.Reconcile.Failures != 0 || health.Capture.Failures != 0 {
		t.Errorf("health after the window: sessions = %+v reconcile = %+v capture = %+v", health.Sessions, health.Reconcile, health.Capture)
	}
	for _, agent := range agents {
		w.requireSameAgent(agent, "the measured window")
	}
	return m
}

// measureFleetWatchdog times the watchdog over the fleet's slots, in a daemon
// that has no Kitty snapshot: every session is made active, so each tick
// advances a slot's and a fact's idle time for all of them, its profile is
// judged for silence, and its directory is a repository the Git round reads.
// It runs after the measured window, whose client CPU is tmux's alone.
func measureFleetWatchdog(t *testing.T, h *Handler, agents []faultAgent, m *fleetMeasurement) {
	t.Helper()
	if _, _, ready := h.cachedSnapshot(); ready {
		t.Fatal("the fleet's daemon has a Kitty snapshot; the watchdog is measured without one")
	}
	h.SetStaleThresholds(map[string]time.Duration{"active": time.Hour, "subagent": time.Hour, "compact": time.Hour}, nil)
	h.SetHookSilenceThresholds(time.Hour, nil)
	base := time.Now()
	directories := make(map[int]string, len(agents))
	for _, agent := range agents {
		slot := h.runtimeSessionSlot(string(agent.id))
		h.stateMu.Lock()
		st, tracked := h.store[slot]
		label := h.runtimeLabels[string(agent.id)]
		if tracked {
			st.LastAction, st.LastAccount, st.Activity = "active", label.profile, base
			h.lastSeenEventAt[strings.ToLower(label.profile)] = base
		}
		h.stateMu.Unlock()
		directory, ok := gitstate.Key(label.directory)
		if !tracked || !isRuntimeSyntheticSlot(slot) || label.profile == "" || !ok {
			t.Fatalf("%s: slot %d tracked=%v label=%+v; the watchdog has nothing of it to sweep", agent.id, slot, tracked, label)
		}
		seedReducerFact(h, slot, base, protocol.ReducerEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "main"},
			Action: "active", Seq: 1, At: base.Unix(),
		})
		// A branch has a name once it has a commit.
		for _, args := range [][]string{
			{"init", "--quiet", "--initial-branch", "fleet"},
			{"-c", "user.name=fleet", "-c", "user.email=fleet@example.invalid", "-c", "commit.gpgsign=false",
				"commit", "--quiet", "--allow-empty", "--no-verify", "-m", "fleet"},
		} {
			if out, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v in %s: %v: %s", args, directory, err, out)
			}
		}
		directories[slot] = directory
	}

	before := tmux.Stats()
	// The round a tick starts is held back, so a tick is timed without one
	// running beside it; the rounds are timed on their own below.
	var started []func()
	h.probeRunner = func(fn func()) { started = append(started, fn) }
	h.SetGitProbe(true, 0, 0)
	ticks := make([]time.Duration, fleetWatchdogTicks)
	now := base
	for i := range ticks {
		now = now.Add(watchdogInterval)
		tickStarted := time.Now()
		h.watchdogTick(now, watchdogInterval)
		ticks[i] = time.Since(tickStarted)
	}
	if len(started) != 1 {
		t.Fatalf("%d watchdog ticks started %d Git rounds; one is in flight at a time", fleetWatchdogTicks, len(started))
	}
	for slot := range directories {
		if got := effectiveState(h, slot).Icon; got != "bolt" {
			t.Fatalf("slot %d icon = %q after %d ticks; the measured sweep was to leave an active session active", slot, got, fleetWatchdogTicks)
		}
	}

	rounds := make([]time.Duration, fleetGitRounds)
	for i := range rounds {
		// A new cache has no answers, so every directory is probed.
		h.SetGitProbe(true, 0, 0)
		roundStarted := time.Now()
		h.runGitStateRefresh(nil)
		rounds[i] = time.Since(roundStarted)
	}
	for slot, directory := range directories {
		if got := windowGitState(t, h, slot); got.GitDirectory != directory || got.GitBranch != "fleet" {
			t.Fatalf("slot %d git state = %q %q; want branch fleet in %s", slot, got.GitDirectory, got.GitBranch, directory)
		}
	}
	h.probeRunner = nil
	h.SetGitProbe(false, 0, 0)

	m.WatchdogP95Ms = msOf(percentile(ticks, 0.95))
	m.GitRoundP50Ms, m.GitRoundMaxMs = msOf(percentile(rounds, 0.5)), msOf(percentile(rounds, 1))
	m.ClientsPerWatchdog = float64(tmux.Stats().Commands-before.Commands) / fleetWatchdogTicks
}

func TestPerformance_FleetBudgets(t *testing.T) {
	requirePerformanceOptIn(t)

	var recorded []fleetMeasurement
	for _, sessions := range []int{1, 10, 30} {
		for _, workload := range []string{"idle", "output"} {
			t.Run(fmt.Sprintf("%d-sessions-%s", sessions, workload), func(t *testing.T) {
				m := measureFleet(t, sessions, workload)
				recorded = append(recorded, m)
				t.Logf("%s: launch %.0fms; pass p50 %.1fms p95 %.1fms max %.1fms (%.2f tmux clients per pass); read p50 %.1fms p95 %.1fms max %.1fms; sweep of %d p95 %.1fms; %d-wide round p95 %.1fms; tick p95 %.1fms of %s; at most %d clients at once; server %.1f%% CPU, %.1f MiB; clients %.0fms CPU over %.0fms; watchdog tick p95 %.3fms (%.2f tmux clients); cold git round p50 %.1fms max %.1fms",
					m.Tmux, m.LaunchMs, m.PassP50Ms, m.PassP95Ms, m.PassMaxMs, m.ClientsPerPass,
					m.ReadP50Ms, m.ReadP95Ms, m.ReadMaxMs, sessions, m.SweepP95Ms, m.ClientLimit, m.RoundP95Ms,
					m.TickP95Ms, rendering.Interval, m.MaxClientsAtOnce, m.ServerCPUPercent, m.ServerRSSMiB, m.ClientCPUMs, m.WindowMs,
					m.WatchdogP95Ms, m.ClientsPerWatchdog, m.GitRoundP50Ms, m.GitRoundMaxMs)

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
				if m.ClientsPerWatchdog != 0 {
					t.Errorf("the watchdog over %d sessions started %.2f tmux clients a tick; it reads daemon memory alone", sessions, m.ClientsPerWatchdog)
				}
				// Budgets: ceilings over what was measured.
				if got := time.Duration(m.PassP95Ms * float64(time.Millisecond)); got > fleetPassBudget {
					t.Errorf("maintenance pass p95 = %s; budget is %s", got, fleetPassBudget)
				}
				if got := time.Duration(m.ReadP95Ms * float64(time.Millisecond)); got > fleetReadBudget {
					t.Errorf("screen read p95 = %s; budget is %s", got, fleetReadBudget)
				}
				if got := time.Duration(m.WatchdogP95Ms * float64(time.Millisecond)); got > fleetWatchdogBudget {
					t.Errorf("watchdog tick p95 = %s; budget is %s", got, fleetWatchdogBudget)
				}
				if got := time.Duration(m.GitRoundMaxMs * float64(time.Millisecond)); got > fleetGitRoundBudget {
					t.Errorf("cold Git round over %d directories took %s; budget is %s", sessions, got, fleetGitRoundBudget)
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
				"watchdog_p95": msOf(fleetWatchdogBudget), "git_round_max": msOf(fleetGitRoundBudget),
			},
			"budget_server_mib_per_session": fleetServerMemoryPerSession, "budget_server_mib_base": fleetServerMemoryBase,
			"bounds":      map[string]any{"tmux_clients_per_pass": 1, "tmux_clients_at_once": tmux.MaxClientCommands, "tmux_clients_per_watchdog": 0},
			"interval_ms": msOf(rendering.Interval), "passes": fleetPasses, "sweeps": fleetSweeps,
			"watchdog_ticks": fleetWatchdogTicks, "git_rounds": fleetGitRounds,
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
