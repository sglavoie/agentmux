package ipc

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
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

		// The restart decision preceded the lock. An explicit restart may
		// have finished meanwhile, so recheck the socket before replacing it.
		if health := c.healthBefore(deadline); health.Alive && process.SocketOlderThanBinary(c.socketPath, amxBin) {
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
// is returned. Session-detail callers build the request through the dashboard
// command helper and call Send directly; ExportEvents is the typed helper
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
		RuntimeSlots:   resp.RuntimeSlots,
		KittySlots:     resp.KittySlots,
	}, nil
}

// ExportKittyWindowEvents resolves a Kitty window by its socket and raw number
// before exporting. A bare number can refer to a different Kitty instance.
func (c *Client) ExportKittyWindowEvents(windowID int, socket string) (protocol.EventsExport, error) {
	if windowID <= 0 || strings.TrimSpace(socket) == "" {
		return protocol.EventsExport{}, fmt.Errorf("kitty export requires a window ID and socket")
	}
	resp, err := c.sendWithDeadline(protocol.Request{
		Type: protocol.TypeEventsExport, WindowID: windowID, KittyListenOn: socket,
	}, exportDeadline)
	if err != nil {
		return protocol.EventsExport{}, err
	}
	if !resp.OK {
		return protocol.EventsExport{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	return protocol.EventsExport{
		EventsByWindow: resp.EventsByWindow,
		WindowMetadata: resp.WindowMetadata,
		RuntimeSlots:   resp.RuntimeSlots,
		KittySlots:     resp.KittySlots,
	}, nil
}

// ExportRuntimeSessionEvents is ExportEvents for the one slot a runtime
// session holds. The daemon resolves the slot, so this works for a session
// with no Kitty window; the answer's RuntimeSlots names the slot it used. A
// session the daemon holds no slot for is an error.
func (c *Client) ExportRuntimeSessionEvents(runtimeSessionID string) (protocol.EventsExport, error) {
	resp, err := c.sendWithDeadline(protocol.Request{Type: protocol.TypeEventsExport, RuntimeSessionID: runtimeSessionID}, exportDeadline)
	if err != nil {
		return protocol.EventsExport{}, err
	}
	if !resp.OK {
		return protocol.EventsExport{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	return protocol.EventsExport{
		EventsByWindow: resp.EventsByWindow,
		WindowMetadata: resp.WindowMetadata,
		RuntimeSlots:   resp.RuntimeSlots,
		KittySlots:     resp.KittySlots,
	}, nil
}

// StateQuery asks the daemon for its per-window states and its cached Kitty
// topology in one round trip, under the dedicated one-second budget rather
// than the 250ms hook-acknowledgement budget Send enforces.
//
// It is the dashboard refresh path: the response's KittySnapshot carries the
// window tree the daemon already listed, so a refresh does not run a
// `kitten @ ls` of its own. Only a ready snapshot supplies that tree, and a
// ready one does so even when the daemon has marked it stale; every other shape
// sends the caller to direct Kitty I/O as a fallback. A nil KittySnapshot means
// the daemon predates the block. A non-nil block with Ready false means a cold
// snapshot — a daemon that has not completed a listing yet — and the refresh
// falls back to direct Kitty I/O for that tick rather than waiting for the
// daemon to warm up. The per-window state from the same response is still used
// in both cases: a reachable daemon is authoritative about state even when its
// topology is unusable.
//
// The raw Response is returned rather than a projection because the state query
// has always been consumed field by field by its callers; adding a projection
// type here would only oblige every existing one to be rewritten.
func (c *Client) StateQuery() (protocol.Response, error) {
	resp, err := c.sendWithDeadline(protocol.Request{Type: protocol.TypeStateQuery}, stateQueryDeadline)
	if err != nil {
		return protocol.Response{}, err
	}
	if !resp.OK {
		return protocol.Response{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	return resp, nil
}
