package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// dialedSocket returns the value passed to `kitten @ --to <socket>` in the
// most recent recorded call, or "" plus false when the client dialed without a
// socket (no `--to` flag). The most-recent call is what the assertion targets:
// SetKittyClient primes the snapshot cache with one initial ListWindows through
// the daemon's own socket, so the client-under-test's dial is the later one.
func dialedSocket(runner *kitty.MockRunner) (string, bool) {
	for i := len(runner.Calls) - 1; i >= 0; i-- {
		args := runner.Calls[i].Args
		for j, a := range args {
			if a == "--to" && j+1 < len(args) {
				return args[j+1], true
			}
		}
	}
	return "", false
}

func assertDialedSocket(t *testing.T, runner *kitty.MockRunner, want string) {
	t.Helper()
	got, ok := dialedSocket(runner)
	if !ok {
		t.Fatalf("no --to argument found in kitten calls: %+v", runner.Calls)
	}
	if got != want {
		t.Fatalf("dialed socket = %q, want %q", got, want)
	}
}

func TestNewDaemonKittyClient_NormalizesFDEnvironment(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "fd:34")
	t.Setenv("KITTY_PID", "4242")
	runner := kitty.NewMockRunner(map[string][]byte{})

	client := newDaemonKittyClient(runner)
	// Parse error is expected (empty canned response); we only assert the
	// endpoint the daemon-constructed client dials.
	_, _ = client.ListWindows()

	assertDialedSocket(t, runner, "unix:/tmp/kitty-4242")
}

func TestNewDaemonKittyClient_NoSocketOutsideKitty(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "")
	t.Setenv("KITTY_PID", "")
	runner := kitty.NewMockRunner(map[string][]byte{})

	client := newDaemonKittyClient(runner)
	_, _ = client.ListWindows()

	if got, ok := dialedSocket(runner); ok {
		t.Fatalf("dialed socket = %q, want none outside Kitty", got)
	}
}

func TestHookKittyClient_ScopesToRequestSocket(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	runner := kitty.NewMockRunner(map[string][]byte{})
	h.SetKittyClient(kitty.NewClientWithSocket(runner, "unix:/tmp/kitty-daemon"))

	_, _ = h.hookKittyClient(protocol.Request{KittyListenOn: "unix:/tmp/kitty-request"}).ListWindows()

	assertDialedSocket(t, runner, "unix:/tmp/kitty-request")
}

func TestHookKittyClient_FallsBackForUnusableRequestSocket(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		socket string
	}{
		{name: "fd descriptor", socket: "fd:34"},
		{name: "empty", socket: ""},
		{name: "unknown scheme", socket: "/tmp/kitty-guess"},
		{name: "prefix-only unix", socket: "unix:"},
		{name: "prefix-only tcp", socket: "tcp:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			runner := kitty.NewMockRunner(map[string][]byte{})
			h.SetKittyClient(kitty.NewClientWithSocket(runner, "unix:/tmp/kitty-daemon"))

			_, _ = h.hookKittyClient(protocol.Request{KittyListenOn: tc.socket}).ListWindows()

			assertDialedSocket(t, runner, "unix:/tmp/kitty-daemon")
		})
	}
}

func TestRememberKittySocket_RejectsUnusableAndPreservesStable(t *testing.T) {
	t.Parallel()

	h := newTestHandler()

	h.stateMu.Lock()
	h.ensureWindowStateLocked(42)
	h.rememberKittySocketLocked(42, "unix:/tmp/kitty-stable")
	// Neither an fd: descriptor, an empty value, nor a prefix-only "unix:"/"tcp:"
	// endpoint may replace the stable one.
	h.rememberKittySocketLocked(42, "fd:34")
	h.rememberKittySocketLocked(42, "")
	h.rememberKittySocketLocked(42, "   ")
	h.rememberKittySocketLocked(42, "unix:")
	h.rememberKittySocketLocked(42, "tcp:")
	gotWindow := h.kittySockets[42]
	gotFallback := h.lastKittyListenOn
	gotMeta := h.windowMetadata[42].Renderer.KittyListenOn
	h.stateMu.Unlock()

	if gotWindow != "unix:/tmp/kitty-stable" {
		t.Fatalf("per-window socket = %q, want unix:/tmp/kitty-stable", gotWindow)
	}
	if gotFallback != "unix:/tmp/kitty-stable" {
		t.Fatalf("fallback socket = %q, want unix:/tmp/kitty-stable", gotFallback)
	}
	if gotMeta != "unix:/tmp/kitty-stable" {
		t.Fatalf("renderer metadata socket = %q, want unix:/tmp/kitty-stable (never fd:)", gotMeta)
	}
}

func TestIsReusableKittySocket(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"unix:/tmp/kitty-1": true,
		"tcp:localhost:900": true,
		" unix:/tmp/k ":     true,
		"fd:34":             false,
		"":                  false,
		"   ":               false,
		"/tmp/kitty-guess":  false,
		"unix:":             false,
		"tcp:":              false,
	}
	for socket, want := range cases {
		if got := isReusableKittySocket(socket); got != want {
			t.Errorf("isReusableKittySocket(%q) = %v, want %v", socket, got, want)
		}
	}
}
