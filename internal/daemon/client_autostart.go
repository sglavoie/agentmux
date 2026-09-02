package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/process"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

type observedRequestSender struct {
	client   *Client
	req      protocol.Request
	deadline time.Time
	observer DeliveryObserver
	attempt  int
}

func (s *observedRequestSender) send() (protocol.Response, error) {
	s.attempt++
	req := s.req
	req.Attempt = s.attempt
	started := time.Now()
	resp, err := s.client.sendBefore(req, s.deadline)
	event := DeliveryStageEvent{
		Stage:     DeliveryStageIPCAttempt,
		Attempt:   s.attempt,
		Outcome:   "success",
		Reason:    "acknowledged",
		LatencyMS: float64(time.Since(started)) / float64(time.Millisecond),
	}
	if err != nil {
		event.Outcome = "error"
		event.Reason = err.Error()
	}
	if s.observer != nil {
		s.observer(event)
	}
	return resp, err
}

func observeDeliveryStage(observer DeliveryObserver, stage, outcome, reason string, started time.Time) {
	if observer == nil {
		return
	}
	observer(DeliveryStageEvent{
		Stage:     stage,
		Outcome:   outcome,
		Reason:    reason,
		LatencyMS: float64(time.Since(started)) / float64(time.Millisecond),
	})
}

func (c *Client) sendWithRetriesObserved(sender *observedRequestSender, deadline time.Time) (protocol.Response, error) {
	var resp protocol.Response
	err := errors.New("daemon did not start in time")
	for time.Until(deadline) > 0 {
		delay := autoStartRetryDelay
		if remaining := time.Until(deadline); remaining < delay {
			delay = remaining
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		resp, err = sender.send()
		if err == nil {
			return resp, nil
		}
	}
	return protocol.Response{}, err
}

// SendWithAutoStart sends req to the daemon, auto-starting it if not running.
// amxBin is the path to the amx binary used for auto-start.
func (c *Client) SendWithAutoStart(req protocol.Request, amxBin string) (protocol.Response, error) {
	return c.SendWithAutoStartObserved(req, amxBin, nil)
}

// SendWithAutoStartObserved is SendWithAutoStart with additive transport-stage
// observation. The observer runs synchronously after each attempt or autostart
// action; callers must keep it bounded and fail-open.
func (c *Client) SendWithAutoStartObserved(req protocol.Request, amxBin string, observer DeliveryObserver) (protocol.Response, error) {
	deadline := time.Now().Add(autoStartDeadline)
	sender := &observedRequestSender{client: c, req: req, deadline: deadline, observer: observer}
	restarted := false
	var autoStartLock *os.File
	defer func() {
		if autoStartLock != nil {
			process.ReleaseAutoStartLock(autoStartLock)
		}
	}()

	if process.SocketOlderThanBinary(c.socketPath, amxBin) {
		lock, lockErr := process.AcquireAutoStartLock(c.socketPath)
		if lockErr != nil {
			if !errors.Is(lockErr, process.ErrAutoStartInProgress) {
				return protocol.Response{}, fmt.Errorf("daemon auto-start lock failed: %w", lockErr)
			}
			observeDeliveryStage(observer, DeliveryStageDaemonAutostart, "waiting", "another_client_starting", time.Now())
			resp, err := sender.send()
			if err == nil {
				return resp, nil
			}
			resp, retryErr := c.sendWithRetriesObserved(sender, deadline)
			if retryErr == nil {
				return resp, nil
			}
			return protocol.Response{}, fmt.Errorf("daemon did not start in time: %w", retryErr)
		}
		autoStartLock = lock

		if health := c.healthBefore(deadline); health.Alive {
			slog.Debug("daemon client: restarting stale daemon for newer amx binary",
				"socket_path", c.socketPath,
				"amx_bin", amxBin,
				"daemon_pid", health.PID,
			)
			started := time.Now()
			if err := c.restartDetachedBefore(amxBin, true, deadline); err != nil {
				observeDeliveryStage(observer, DeliveryStageDaemonAutostart, "error", err.Error(), started)
				return protocol.Response{}, fmt.Errorf("daemon auto-restart failed: %w", err)
			}
			observeDeliveryStage(observer, DeliveryStageDaemonAutostart, "success", "stale_daemon_restarted", started)
			restarted = true
		}
	}

	resp, err := sender.send()
	if err == nil {
		return resp, nil
	}

	// If we just restarted the daemon, restartDetached has already spawned the
	// replacement process — it just hasn't bound the socket yet. Spawning again
	// here would create a second daemon process whose NewServer races with the
	// first by removing the socket file out from under it, potentially leaving
	// behind a ghost daemon listening on no filesystem entry. Skip the
	// spawnDetached and fall straight into the retry loop instead.
	if !restarted {
		if autoStartLock == nil {
			lock, lockErr := process.AcquireAutoStartLock(c.socketPath)
			if lockErr != nil {
				if !errors.Is(lockErr, process.ErrAutoStartInProgress) {
					return protocol.Response{}, fmt.Errorf("daemon auto-start lock failed: %w", lockErr)
				}
			} else {
				autoStartLock = lock
			}
		}
		if autoStartLock != nil {
			// Another process may have completed startup between our failed Send
			// and acquiring the lock. Recheck before spawning.
			resp, err = sender.send()
			if err == nil {
				return resp, nil
			}
			started := time.Now()
			if startErr := spawnDetached(amxBin); startErr != nil {
				observeDeliveryStage(observer, DeliveryStageDaemonAutostart, "error", startErr.Error(), started)
				return protocol.Response{}, fmt.Errorf("daemon auto-start failed: %w", startErr)
			}
			observeDeliveryStage(observer, DeliveryStageDaemonAutostart, "success", "daemon_spawned", started)
		}
	}

	// Retry only for the remainder of the hook's auto-start budget.
	resp, err = c.sendWithRetriesObserved(sender, deadline)
	if err == nil {
		return resp, nil
	}
	return protocol.Response{}, fmt.Errorf("daemon did not start in time: %w", err)
}

// ExportEvents returns the per-window hook-event ring buffers plus durable
// metadata from the running daemon. When windowID is non-zero, the daemon
// filters the snapshot to that single window; otherwise every exported window
// is returned. Existing TypeSessionDetail callers build the request inline and
// call Send directly (see tui/model_modes.go); ExportEvents is the typed helper
// for the richer CLI export so the command does not have to know about the
// message-type constant or response-shape details.
func (c *Client) ExportEvents(windowID int) (protocol.EventsExport, error) {
	resp, err := c.sendWithDeadline(protocol.Request{Type: protocol.TypeEventsExport, WindowID: windowID}, exportDeadline)
	if err != nil {
		return protocol.EventsExport{}, err
	}
	if !resp.OK {
		return protocol.EventsExport{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	return protocol.EventsExport{
		EventsByWindow: resp.EventsByWindow,
		WindowMetadata: resp.WindowMetadata,
	}, nil
}
