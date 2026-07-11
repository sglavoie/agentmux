package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// blockingProbeKittyClient wraps a fakeKittyClient but parks every GetText call
// until release is closed, modelling a slow or wedged Kitty screen read. It
// still serves ListWindows so the snapshot prime succeeds.
type blockingProbeKittyClient struct {
	*fakeKittyClient
	entered chan struct{}
	release chan struct{}
	text    string
}

func (b *blockingProbeKittyClient) GetText(matchExpr, extent string) (string, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return b.text, nil
}

// TestHandler_HookEventSlowProbeDoesNotDelayAck is the core ingress invariant:
// a context-limit screen read that blocks indefinitely must not delay the hook
// acknowledgement. The probe runs off the hook goroutine, so Handle returns
// immediately while GetText is still parked, and the error promotion lands only
// after the read completes.
func TestHandler_HookEventSlowProbeDoesNotDelayAck(t *testing.T) {
	h := newTestHandler()
	client := &blockingProbeKittyClient{
		fakeKittyClient: &fakeKittyClient{
			windows: hookEventWindows(palette.BuildTitle("api", "bolt", false), []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
			}}),
		},
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		text:    "Context limit reached - /compact or /clear to continue\n",
	}
	h.SetKittyClient(client)

	done := make(chan Response, 1)
	go func() {
		done <- h.Handle(Request{
			Type:        TypeHookEvent,
			WindowID:    42,
			Event:       "Notification",
			PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{NotificationType: "agent_idle"}),
		})
	}()

	// The ack must arrive while the probe's GetText is still blocked.
	select {
	case resp := <-done:
		if !resp.OK {
			t.Fatalf("hook response = %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hook acknowledgement blocked on a slow context-limit probe")
	}

	// The probe goroutine must have started its read (proving it was dispatched)
	// but not yet completed. Release it and confirm the error promotion lands.
	select {
	case <-client.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("context-limit probe was never dispatched")
	}
	close(client.release)

	deadline := time.Now().Add(2 * time.Second)
	for {
		h.stateMu.Lock()
		st := h.store[42]
		icon := ""
		if st != nil {
			icon = st.DesiredIcon
		}
		h.stateMu.Unlock()
		if icon == "error" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state icon = %q; want error after the async probe completed", icon)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
