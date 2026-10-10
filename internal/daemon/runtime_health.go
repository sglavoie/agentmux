package daemon

import (
	"fmt"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// What the daemon does for runtime sessions happens where nobody is looking:
// a pass every two seconds against servers that outlive it, and screen reads
// on a loop of their own. This file is how that work is seen. It keeps a
// tally of the passes and the reads, publishes it through health as
// protocol.RuntimeHealth, and writes one journal record for each edge worth
// finding later: a pass that starts failing, the pass that recovers, and a
// pass that changed something.
//
// Nothing recorded here comes from a pane or a launch. A failure is named by
// its class, a session by its opaque runtime-session ID, and everything else
// is a count or a duration, so the tally can be exported as it stands.

// runtimeReconcileStage is the journal stage of reconciliation edges.
const runtimeReconcileStage = "daemon.runtime_reconcile"

// runtimeJournal writes one journal record; nil when the journal is disabled.
type runtimeJournal func(stage, outcome, reason string, evidence map[string]string)

// runtimeSessionLister is the registry view the session counts are read from.
type runtimeSessionLister interface {
	Live() ([]runtimesession.Record, error)
}

// runtimeHealthTally is the handler's record of its runtime-session work. It
// has its own lock because its writers (the maintenance and screen-probe
// loops) and its reader (a health request) share nothing else.
type runtimeHealthTally struct {
	mu sync.Mutex

	passes              uint64
	failures            uint64
	consecutiveFailures uint64
	lastPassAt          time.Time
	lastPass            time.Duration
	maxPass             time.Duration
	failureClass        string
	ended               uint64
	released            uint64
	detached            uint64

	sessions protocol.RuntimeSessionHealth

	reads                   uint64
	readFailures            uint64
	consecutiveReadFailures uint64
	lastReadOKAt            time.Time
	lastRead                time.Duration
	maxRead                 time.Duration
}

// SetRuntimeJournal installs the journal writer for reconciliation edges. It
// is called once at startup, before any pass runs.
func (h *Handler) SetRuntimeJournal(record func(stage, outcome, reason string, evidence map[string]string)) {
	h.runtimeJournal = record
}

// recordRuntimeReconcile tallies one finished pass and journals its edge. It
// returns how many passes in a row have now failed.
func (h *Handler) recordRuntimeReconcile(finished time.Time, elapsed time.Duration, report tmux.Report, err error) uint64 {
	class := tmux.FailureClass(err)
	sessions, sessionsErr := h.countRuntimeSessions()
	if sessionsErr != nil {
		class = "registry_unreadable"
	}

	t := &h.runtimeHealth
	t.mu.Lock()
	wasFailing := t.consecutiveFailures > 0
	t.passes++
	t.lastPassAt = finished
	t.lastPass = elapsed
	t.maxPass = max(t.maxPass, elapsed)
	t.failureClass = class
	if class != "" {
		t.failures++
		t.consecutiveFailures++
	} else {
		t.consecutiveFailures = 0
	}
	t.ended += uint64(len(report.Ended))
	t.released += uint64(len(report.Released))
	t.detached += uint64(len(report.Detached))
	t.sessions = sessions
	failing := t.consecutiveFailures
	t.mu.Unlock()

	if h.runtimeJournal == nil {
		return failing
	}
	evidence := map[string]string{
		"duration_ms":       fmt.Sprintf("%.1f", float64(elapsed)/float64(time.Millisecond)),
		"live_sessions":     fmt.Sprint(sessions.Live),
		"attached_sessions": fmt.Sprint(sessions.Attached),
		"servers":           fmt.Sprint(sessions.Servers),
	}
	// A failing streak writes its first pass and its recovery, never the
	// passes in between: a server unreadable for an hour is two records.
	switch {
	case class != "" && !wasFailing:
		evidence["failure_class"] = class
		h.runtimeJournal(runtimeReconcileStage, "error", "reconcile_failed", evidence)
	case class == "" && wasFailing:
		h.runtimeJournal(runtimeReconcileStage, "success", "reconcile_recovered", evidence)
	}
	if report.Empty() {
		return failing
	}
	evidence["ended"] = fmt.Sprint(len(report.Ended))
	evidence["released"] = fmt.Sprint(len(report.Released))
	evidence["detached"] = fmt.Sprint(len(report.Detached))
	evidence["consecutive_failures"] = fmt.Sprint(failing)
	for i, ended := range report.Ended {
		if i == 4 {
			break
		}
		evidence[fmt.Sprintf("ended_%d", i)] = string(ended.ID) + " " + string(ended.Outcome)
	}
	h.runtimeJournal(runtimeReconcileStage, "success", "sessions_changed", evidence)
	return failing
}

// countRuntimeSessions reads the live sessions the registry lists. A registry
// that cannot be read is reported as not known rather than as empty.
func (h *Handler) countRuntimeSessions() (protocol.RuntimeSessionHealth, error) {
	lister, ok := h.runtimeDirectory.(runtimeSessionLister)
	if !ok {
		return protocol.RuntimeSessionHealth{}, nil
	}
	live, err := lister.Live()
	if err != nil {
		return protocol.RuntimeSessionHealth{}, err
	}
	counts := protocol.RuntimeSessionHealth{Known: true, Live: len(live)}
	servers := map[string]bool{}
	for _, record := range live {
		if record.Target != nil && record.Target.Instance.Kind == runtimesession.BackendTmux {
			counts.Tmux++
			servers[record.Target.Instance.Address] = true
		}
		if record.Attachment != nil {
			counts.Attached++
		} else {
			counts.Unattached++
		}
	}
	counts.Servers = len(servers)
	return counts, nil
}

// recordScreenRead tallies one screen read the probes made.
func (h *Handler) recordScreenRead(elapsed time.Duration, err error) {
	t := &h.runtimeHealth
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reads++
	t.lastRead = elapsed
	t.maxRead = max(t.maxRead, elapsed)
	if err != nil {
		t.readFailures++
		t.consecutiveReadFailures++
		return
	}
	t.consecutiveReadFailures = 0
	t.lastReadOKAt = time.Now()
}

// runtimeHealthSnapshot is the tally as health publishes it.
func (h *Handler) runtimeHealthSnapshot(now time.Time) *protocol.RuntimeHealth {
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	age := func(at time.Time) *float64 {
		if at.IsZero() {
			return nil
		}
		seconds := max(now.Sub(at).Seconds(), 0)
		return &seconds
	}
	t := &h.runtimeHealth
	t.mu.Lock()
	health := &protocol.RuntimeHealth{
		Reconcile: protocol.RuntimeReconcileHealth{
			Enabled:             h.runtimeReconciler != nil,
			IntervalSeconds:     runtimeReconcileWait(t.consecutiveFailures).Seconds(),
			Passes:              t.passes,
			Failures:            t.failures,
			ConsecutiveFailures: t.consecutiveFailures,
			AgeSeconds:          age(t.lastPassAt),
			LastDurationMs:      ms(t.lastPass),
			MaxDurationMs:       ms(t.maxPass),
			FailureClass:        t.failureClass,
			Ended:               t.ended,
			Released:            t.released,
			Detached:            t.detached,
		},
		Sessions: t.sessions,
		Capture: protocol.ScreenCaptureHealth{
			Reads:               t.reads,
			Failures:            t.readFailures,
			ConsecutiveFailures: t.consecutiveReadFailures,
			AgeSeconds:          age(t.lastReadOKAt),
			LastDurationMs:      ms(t.lastRead),
			MaxDurationMs:       ms(t.maxRead),
		},
	}
	t.mu.Unlock()
	clients := tmux.Stats()
	health.TmuxClient = protocol.TmuxClientHealth{
		Limit:         clients.Limit,
		Commands:      clients.Commands,
		Failures:      clients.Failures,
		Timeouts:      clients.Timeouts,
		InFlight:      clients.InFlight,
		MaxInFlight:   clients.MaxInFlight,
		Queued:        clients.Queued,
		MaxQueueMs:    ms(clients.MaxQueue),
		MaxDurationMs: ms(clients.MaxDuration),
	}
	return health
}
