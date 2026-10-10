package tmux

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Every read and every change of the private server is one short-lived tmux
// client process. A daemon reconciling, an overview capturing nine panes, and
// a dashboard listing sessions each start their own, and nothing related one
// caller's to another's: thirty agents could mean thirty clients at once
// against a server that answers them one at a time anyway. So the clients of
// one process are admitted through a gate. What it bounds is how many
// subprocesses this program has outstanding, which is what the machine pays
// for; the server's own work is unchanged, because it never ran two commands
// at once.
//
// An attached terminal (`AttachCommand`) is not a command and does not pass
// the gate: it lives for as long as someone is looking.

// MaxClientCommands is how many tmux client processes one program runs at
// once. Four matches the overview's capture ceiling, so a full round of
// captures is admitted together and everything else queues behind it.
const MaxClientCommands = 4

// ErrClientQueueTimeout reports a command that was never started: every slot
// stayed taken for the whole of the command's own timeout.
var ErrClientQueueTimeout = errors.New("tmux: timed out waiting for a client slot")

// ClientStats is what the gate has admitted since the program started.
type ClientStats struct {
	// Limit is MaxClientCommands.
	Limit int
	// Commands counts client processes started; Failures those that did not
	// succeed (a server that is not running is one), and Timeouts those that
	// ran or queued past their timeout.
	Commands uint64
	Failures uint64
	Timeouts uint64
	// InFlight is how many run now; MaxInFlight the most that ever did, which
	// never exceeds Limit.
	InFlight    int
	MaxInFlight int
	// Queued counts commands that had to wait for a slot, and MaxQueue the
	// longest any of them waited.
	Queued   uint64
	MaxQueue time.Duration
	// LastDuration and MaxDuration are of the client process itself, queueing
	// excluded.
	LastDuration time.Duration
	MaxDuration  time.Duration
}

type clientGate struct {
	slots chan struct{}

	mu    sync.Mutex
	stats ClientStats
}

func newClientGate(limit int) *clientGate {
	return &clientGate{slots: make(chan struct{}, limit), stats: ClientStats{Limit: limit}}
}

// clients is the gate every Server of this process shares: the bound is on
// the program, whichever server a command is for.
var clients = newClientGate(MaxClientCommands)

// Stats reports this program's tmux client activity.
func Stats() ClientStats { return clients.snapshot() }

func (g *clientGate) snapshot() ClientStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}

// acquire takes a slot, waiting for one until ctx ends. The returned release
// must be called exactly once, with how the command ended.
func (g *clientGate) acquire(ctx context.Context) (release func(failed, timedOut bool), err error) {
	queuedAt := time.Now()
	select {
	case g.slots <- struct{}{}:
	default:
		select {
		case g.slots <- struct{}{}:
		case <-ctx.Done():
			g.mu.Lock()
			g.stats.Queued++
			g.stats.Timeouts++
			g.stats.Failures++
			g.stats.MaxQueue = max(g.stats.MaxQueue, time.Since(queuedAt))
			g.mu.Unlock()
			return nil, ErrClientQueueTimeout
		}
		waited := time.Since(queuedAt)
		g.mu.Lock()
		g.stats.Queued++
		g.stats.MaxQueue = max(g.stats.MaxQueue, waited)
		g.mu.Unlock()
	}
	started := time.Now()
	g.mu.Lock()
	g.stats.Commands++
	g.stats.InFlight++
	g.stats.MaxInFlight = max(g.stats.MaxInFlight, g.stats.InFlight)
	g.mu.Unlock()
	return func(failed, timedOut bool) {
		elapsed := time.Since(started)
		g.mu.Lock()
		g.stats.InFlight--
		g.stats.LastDuration = elapsed
		g.stats.MaxDuration = max(g.stats.MaxDuration, elapsed)
		if failed {
			g.stats.Failures++
		}
		if timedOut {
			g.stats.Timeouts++
		}
		g.mu.Unlock()
		<-g.slots
	}, nil
}

// FailureClass names what kind of failure err is, in a fixed vocabulary that
// is safe to publish: it never carries a path, an argument, or anything a
// pane printed. Empty for nil.
func FailureClass(err error) string {
	var command *CommandError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrServerNotRunning):
		return "server_not_running"
	case errors.Is(err, ErrClientQueueTimeout), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case IsNoRuntime(err):
		return "tmux_unavailable"
	case errors.As(err, &command):
		return "command_failed"
	default:
		return "unreadable"
	}
}
