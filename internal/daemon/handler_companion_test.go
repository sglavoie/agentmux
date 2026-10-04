package daemon

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// recordingFocusClient records every Kitty call focus_window makes, in order,
// with the socket it was dialed through. onList runs inside ListWindows so a
// test can observe what the handler holds while Kitty is being read.
type recordingFocusClient struct {
	mu       sync.Mutex
	live     []kitty.OSWindow
	calls    []string
	focusErr error
	onList   func()
}

func (c *recordingFocusClient) record(call string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}

func (c *recordingFocusClient) ListWindows(socket string) ([]kitty.OSWindow, error) {
	c.record("list " + socket)
	if c.onList != nil {
		c.onList()
	}
	return c.live, nil
}

func (c *recordingFocusClient) FocusTab(socket, matchExpr string) error {
	c.record("tab " + socket + " " + matchExpr)
	return c.focusErr
}

func (c *recordingFocusClient) FocusWindow(socket, matchExpr string) error {
	c.record("window " + socket + " " + matchExpr)
	return nil
}

func (c *recordingFocusClient) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func focusRequest(socket string, windowID int) protocol.Request {
	return protocol.Request{Type: protocol.TypeFocusWindow, FocusWindow: &protocol.FocusWindowRequest{KittySocket: socket, WindowID: windowID}}
}

const (
	focusHookSocket     = "unix:/tmp/kitty-hook"
	focusListenSocket   = "unix:/tmp/kitty-listen"
	focusSnapshotSocket = "unix:/tmp/kitty-snapshot"
)

// newFocusTestHandler knows exactly one socket from each source the allowlist
// draws on: a per-window hook socket, the effective listen address, and the
// socket the published snapshot was read through.
func newFocusTestHandler(client *recordingFocusClient) *Handler {
	h := newTestHandler()
	h.SetFocusClient(client)
	h.stateMu.Lock()
	h.kittySockets[7] = focusHookSocket
	h.lastKittyListenOn = focusListenSocket
	h.stateMu.Unlock()
	h.snapshots.Publish(probes.Publication{Windows: companionWindows(3), Socket: focusSnapshotSocket, CapturedAt: time.Now()})
	return h
}

func TestFocusWindowFocusesTabThenWindowOnEveryKnownSocket(t *testing.T) {
	for _, socket := range []string{focusHookSocket, focusListenSocket, focusSnapshotSocket} {
		t.Run(socket, func(t *testing.T) {
			client := &recordingFocusClient{live: companionWindows(3)}
			h := newFocusTestHandler(client)

			resp := h.Handle(focusRequest(socket, 2))
			if !resp.OK {
				t.Fatalf("focus_window refused: %q", resp.Error)
			}
			want := protocol.FocusWindowResult{WindowID: 2, TabID: 102, OSWindowID: 1}
			if resp.Focused == nil || *resp.Focused != want {
				t.Fatalf("Focused = %+v; want %+v", resp.Focused, want)
			}
			calls := client.snapshot()
			wantCalls := []string{
				"list " + socket,
				"tab " + socket + " " + kitty.MatchTabByWindowID(2),
				"window " + socket + " " + kitty.MatchWindowByID(2),
			}
			if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
				t.Fatalf("Kitty calls = %q; want list, then tab, then window: %q", calls, wantCalls)
			}
		})
	}
}

// TestFocusWindowRejectsUnknownSocketWithoutDialing is the security boundary:
// an address the daemon did not learn itself is never dialed.
func TestFocusWindowRejectsUnknownSocketWithoutDialing(t *testing.T) {
	for _, socket := range []string{"unix:/tmp/attacker", "", "tcp:127.0.0.1:1"} {
		client := &recordingFocusClient{live: companionWindows(3)}
		h := newFocusTestHandler(client)

		resp := h.Handle(focusRequest(socket, 2))
		if resp.OK || resp.Error != protocol.FocusErrorUnknownSocket {
			t.Fatalf("socket %q: response = %+v; want %q", socket, resp, protocol.FocusErrorUnknownSocket)
		}
		if calls := client.snapshot(); len(calls) != 0 {
			t.Fatalf("socket %q: Kitty was called %q; want no call at all", socket, calls)
		}
	}
}

// TestFocusWindowEmptyListenAddressIsNotAWildcard guards the allowlist's
// zero values: with no listen address and no snapshot, an empty socket must
// not match them.
func TestFocusWindowEmptyListenAddressIsNotAWildcard(t *testing.T) {
	client := &recordingFocusClient{live: companionWindows(3)}
	h := newTestHandler()
	h.SetFocusClient(client)

	resp := h.Handle(focusRequest("", 2))
	if resp.Error != protocol.FocusErrorUnknownSocket || len(client.snapshot()) != 0 {
		t.Fatalf("response = %+v, calls = %q; want unknown_socket and no call", resp, client.snapshot())
	}
}

func TestFocusWindowStaleTargetWritesNothing(t *testing.T) {
	client := &recordingFocusClient{live: companionWindows(3)}
	h := newFocusTestHandler(client)

	resp := h.Handle(focusRequest(focusHookSocket, 99))
	if resp.OK || resp.Error != protocol.FocusErrorStaleTarget {
		t.Fatalf("response = %+v; want %q", resp, protocol.FocusErrorStaleTarget)
	}
	if calls := client.snapshot(); len(calls) != 1 || calls[0] != "list "+focusHookSocket {
		t.Fatalf("Kitty calls = %q; want the verifying list only", calls)
	}
}

func TestFocusWindowReportsFocusFailure(t *testing.T) {
	client := &recordingFocusClient{live: companionWindows(3), focusErr: errors.New("kitty refused")}
	h := newFocusTestHandler(client)

	resp := h.Handle(focusRequest(focusHookSocket, 2))
	if resp.OK || !strings.Contains(resp.Error, "kitty refused") {
		t.Fatalf("response = %+v; want a focus failure naming the Kitty error", resp)
	}
	// The window step is still attempted after a failed tab step.
	if calls := client.snapshot(); len(calls) != 3 {
		t.Fatalf("Kitty calls = %q; want list, tab, and window", calls)
	}
}

func TestFocusWindowRejectsMalformedRequests(t *testing.T) {
	client := &recordingFocusClient{live: companionWindows(3)}
	h := newFocusTestHandler(client)
	for name, req := range map[string]protocol.Request{
		"no block":        {Type: protocol.TypeFocusWindow},
		"zero window":     focusRequest(focusHookSocket, 0),
		"negative window": focusRequest(focusHookSocket, -3),
	} {
		if resp := h.Handle(req); resp.OK || resp.Error == protocol.FocusErrorStaleTarget || resp.Error == protocol.FocusErrorUnknownSocket {
			t.Errorf("%s: response = %+v; want a plain request error", name, resp)
		}
	}
	if calls := client.snapshot(); len(calls) != 0 {
		t.Fatalf("Kitty calls = %q; want none for malformed requests", calls)
	}

	bare := newTestHandler()
	if resp := bare.Handle(focusRequest(focusHookSocket, 2)); resp.OK {
		t.Fatalf("a handler without a focus client answered OK: %+v", resp)
	}
}

// TestFocusWindowDialsKittyOffStateMu proves the Kitty subprocesses run with
// stateMu released: every hook acknowledgement waits on it.
func TestFocusWindowDialsKittyOffStateMu(t *testing.T) {
	client := &recordingFocusClient{live: companionWindows(3)}
	h := newFocusTestHandler(client)
	held := true
	client.onList = func() {
		if h.stateMu.TryLock() {
			held = false
			h.stateMu.Unlock()
		}
	}
	if resp := h.Handle(focusRequest(focusHookSocket, 2)); !resp.OK {
		t.Fatalf("focus_window refused: %q", resp.Error)
	}
	if held {
		t.Fatal("stateMu was held while Kitty was listed")
	}
}
