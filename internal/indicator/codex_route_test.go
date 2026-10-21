package indicator

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

func TestSharedCodexHooksRouteToConversationOwner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(runtimesession.EnvVar, testRuntimeSession)
	t.Setenv("KITTY_WINDOW_ID", "7")
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/wrong-kitty")
	const brainRuntime = "rs-abcdef0123456789abcdef0123456789"
	previousRead, previousDiscover := readCodexHookRuntimes, discoverHarnessProcessForDelivery
	t.Cleanup(func() {
		readCodexHookRuntimes, discoverHarnessProcessForDelivery = previousRead, previousDiscover
	})
	discoverHarnessProcessForDelivery = func(string) HarnessProcessDiscovery {
		return HarnessProcessDiscovery{Harness: "codex", PID: 999, StartToken: "shared", Trackable: true, SharedServer: true}
	}
	readCodexHookRuntimes = func() ([]runtimesession.Record, error) {
		return []runtimesession.Record{
			{ID: testRuntimeSession, State: runtimesession.StateLive, Conversation: &conversation.Identity{Harness: "codex", SessionID: "muse"}},
			{ID: brainRuntime, State: runtimesession.StateLive, Conversation: &conversation.Identity{Harness: "codex", SessionID: "brainnotes"},
				Process: &runtimesession.ProcessFingerprint{PID: 8166, StartToken: "Sat Oct 17 21:23:36 2026"}},
		}, nil
	}
	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "Stop"} {
		t.Run(event, func(t *testing.T) {
			client := &fakeDaemonClient{resp: protocol.Response{OK: true}}
			result := deliverFromStdinWithDeps(event, 0, "codex", "/tmp/amx", DeliveryOptions{},
				hookPayloadFile(t, `{"session_id":"brainnotes"}`), client)
			if !result.Successful() || client.calls != 1 {
				t.Fatalf("delivery = %+v, calls %d", result, client.calls)
			}
			req := client.reqs[0]
			if req.RuntimeSessionID != brainRuntime || req.WindowID != 0 || req.KittyListenOn != "" {
				t.Fatalf("hook used another client's inherited identity: %+v", req)
			}
			if req.HarnessProcess == nil || req.HarnessProcess.PID != 8166 {
				t.Fatalf("hook tracked the shared server instead of the terminal client: %+v", req.HarnessProcess)
			}
		})
	}
	// An explicit --runtime-session remains authoritative and does not need a
	// registry lookup. It is not an inherited shared-server environment value.
	readCodexHookRuntimes = func() ([]runtimesession.Record, error) {
		t.Fatal("explicit route consulted conversation inference")
		return nil, nil
	}
	client := &fakeDaemonClient{resp: protocol.Response{OK: true}}
	result := deliverFromStdinWithDeps("Stop", 0, "codex", "/tmp/amx", DeliveryOptions{RuntimeSessionID: brainRuntime},
		hookPayloadFile(t, `{"session_id":"brainnotes"}`), client)
	if !result.Successful() || client.reqs[0].RuntimeSessionID != brainRuntime {
		t.Fatalf("explicit runtime route = %+v", result)
	}
	// An isolated client (--no-daemon) owns its environment even for a fresh
	// conversation that does not have a registry binding yet.
	discoverHarnessProcessForDelivery = func(string) HarnessProcessDiscovery {
		return HarnessProcessDiscovery{Harness: "codex", PID: 8166, StartToken: "Sat Oct 17 21:23:36 2026", Trackable: true}
	}
	for _, conversation := range []string{"resumed-conversation", "fresh-after-new", "another-after-new", "resumed-conversation"} {
		for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "Stop"} {
			client = &fakeDaemonClient{resp: protocol.Response{OK: true}}
			result = deliverFromStdinWithDeps(event, 0, "codex", "/tmp/amx", DeliveryOptions{},
				hookPayloadFile(t, `{"session_id":"`+conversation+`"}`), client)
			if !result.Successful() || client.calls != 1 || client.reqs[0].RuntimeSessionID != testRuntimeSession {
				t.Fatalf("isolated client %s/%s = %+v", conversation, event, result)
			}
		}
	}
}

func TestSharedCodexUnresolvedOwnerNeverFallsBackToInheritedRuntime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(runtimesession.EnvVar, testRuntimeSession)
	previousRead, previousDiscover := readCodexHookRuntimes, discoverHarnessProcessForDelivery
	t.Cleanup(func() { readCodexHookRuntimes, discoverHarnessProcessForDelivery = previousRead, previousDiscover })
	discoverHarnessProcessForDelivery = func(string) HarnessProcessDiscovery {
		return HarnessProcessDiscovery{Harness: "codex", SharedServer: true}
	}
	match := runtimesession.Record{ID: testRuntimeSession, State: runtimesession.StateLive,
		Conversation: &conversation.Identity{Harness: "codex", SessionID: "brainnotes"}}
	ended := match
	ended.State = runtimesession.StateEnded
	otherHarness := match
	otherHarness.Conversation = &conversation.Identity{Harness: "claude", SessionID: "brainnotes"}
	for _, tc := range []struct {
		name, payload, reason string
		records               []runtimesession.Record
		err                   error
		retryable             bool
	}{
		{name: "missing identity", payload: `{}`, reason: "codex_conversation_missing"},
		{name: "unknown", payload: `{"session_id":"brainnotes"}`, reason: "codex_conversation_unbound"},
		{name: "ended", payload: `{"session_id":"brainnotes"}`, records: []runtimesession.Record{ended}, reason: "codex_conversation_unbound"},
		{name: "other harness", payload: `{"session_id":"brainnotes"}`, records: []runtimesession.Record{otherHarness}, reason: "codex_conversation_unbound"},
		{name: "ambiguous", payload: `{"session_id":"brainnotes"}`, records: []runtimesession.Record{match, match}, reason: "codex_conversation_ambiguous"},
		{name: "unreadable", payload: `{"session_id":"brainnotes"}`, err: errors.New("unreadable"), reason: protocol.OriginReasonRuntimeRegistryUnavailable, retryable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readCodexHookRuntimes = func() ([]runtimesession.Record, error) { return tc.records, tc.err }
			client := &fakeDaemonClient{resp: protocol.Response{OK: true}}
			result := deliverFromStdinWithDeps("Stop", 0, "codex", "/tmp/amx", DeliveryOptions{}, hookPayloadFile(t, tc.payload), client)
			if result.Successful() || result.Reason != tc.reason || result.Retryable != tc.retryable || client.calls != 0 {
				t.Fatalf("unresolved owner = %+v calls %d", result, client.calls)
			}
		})
	}
}

func TestDiscoverSharedCodexAppServer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		command string
		shared  bool
	}{
		{"/opt/bin/codex app-server --listen unix:// --managed-daemon", true},
		{"node /opt/bin/codex app-server --listen unix:// --managed-daemon", true},
		{"/opt/bin/codex app-server --listen stdio://", false},
		{"/opt/bin/codex resume conversation", false},
		{"/opt/bin/codex exec app-server", false},
		{"claude app-server", false},
	} {
		d := discoverHarnessProcess(10, "codex", fakeSnapshots(map[int]processSnapshot{
			10: {ParentPID: 1, CommandLine: tc.command, StartToken: "Sat Oct 17 21:23:36 2026"},
		}))
		if d.SharedServer != tc.shared {
			t.Fatalf("%q: shared server = %v", tc.command, d.SharedServer)
		}
	}
}
