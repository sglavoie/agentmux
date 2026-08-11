package indicator

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

type handlerDaemonClient struct {
	handler *daemon.Handler
	calls   int
}

func (c *handlerDaemonClient) SendWithAutoStart(req daemon.Request, _ string) (daemon.Response, error) {
	c.calls++
	return c.handler.Handle(req), nil
}

func exactSizedHookPayload(t *testing.T, size int, fields map[string]any) string {
	t.Helper()
	payload := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		payload[key] = value
	}
	payload["bulk_transcript"] = ""
	base, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	padding := size - len(base)
	if padding < 0 {
		t.Fatalf("requested payload size %d is smaller than semantic base %d", size, len(base))
	}
	payload["bulk_transcript"] = strings.Repeat("x", padding)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != size {
		t.Fatalf("exact payload size = %d, want %d", len(encoded), size)
	}
	return string(encoded)
}

func hookPayloadFile(t *testing.T, payload string) *os.File {
	t.Helper()
	stdin, err := os.CreateTemp(t.TempDir(), "hook-payload-*.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	if _, err := stdin.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	return stdin
}

func deliverOutsideKitty(t *testing.T) (DeliveryResult, []diagnostics.TraceEnvelope, string) {
	t.Helper()
	t.Setenv("KITTY_LISTEN_ON", "")

	const payload = `{"source_event_id":"source-1"}`
	stdin, err := os.CreateTemp(t.TempDir(), "hook-payload-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = stdin.Close()
	})

	result := Deliver("Stop", 42, "claude", "/tmp/amx", DeliveryOptions{})
	if result.Status != DeliveryPermanentFailure || result.Reason != "outside_kitty" ||
		len(result.TraceID) != 36 || result.RequestID != result.TraceID {
		t.Fatalf("result = %+v", result)
	}

	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	records, err := journal.Read(context.Background(), diagnostics.Selector{RequestID: result.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	return result, records, payload
}
