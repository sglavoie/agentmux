package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// slowHandler answers one request type only after a fixed delay, standing in
// for an ASL bridge call that legitimately outlives the default handshake.
type slowHandler struct {
	*testHandler
	slowType protocol.MessageType
	delay    time.Duration
}

func (h *slowHandler) Handle(req protocol.Request) protocol.Response {
	if req.Type == h.slowType {
		time.Sleep(h.delay)
	}
	return h.testHandler.Handle(req)
}

// TestServer_SlowASLActionReplyIsWritten is the regression for the uniform 1s
// connection deadline: an asl_action the bridge takes 3s to settle must still
// reach the client, because a lost reply turns a settled mutation into an
// unknown one.
func TestServer_SlowASLActionReplyIsWritten(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-srvslow-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "d.sock")

	handler := &slowHandler{testHandler: newTestHandler(), slowType: protocol.TypeASLAction, delay: 3 * time.Second}
	server, err := NewServer(socketPath, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(server.Close)
	go func() { _ = server.Serve(context.Background()) }()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(asl.ActionDeadline)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	payload, err := json.Marshal(protocol.Request{Type: protocol.TypeASLAction, ASL: &protocol.ASLRequest{Method: "reply"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("reply was not written after a 3s asl_action: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if !resp.OK {
		t.Fatalf("reply OK = false, error %q", resp.Error)
	}
}

func TestDeadlineFor(t *testing.T) {
	cases := []struct {
		typ  protocol.MessageType
		want time.Duration
	}{
		{protocol.TypeASLAction, asl.ActionDeadline},
		{protocol.TypeASLWorkbench, asl.ActionDeadline},
		{protocol.TypeASLQuestion, asl.ReadDeadline},
		{protocol.TypeASLSnapshot, asl.ReadDeadline},
		{protocol.TypeFocusWindow, FocusWindowDeadline},
		{protocol.TypeNotificationTest, NotificationTestDeadline},
		{protocol.TypeHookEvent, connDeadline},
		{protocol.TypeStateUpdate, connDeadline},
		{protocol.TypeHealth, connDeadline},
		{protocol.MessageType("unknown"), connDeadline},
	}
	for _, tc := range cases {
		if got := deadlineFor(tc.typ); got != tc.want {
			t.Errorf("deadlineFor(%q) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

// TestDeadlineFor_DefaultFollowsConnDeadline keeps the default entry tied to the
// handshake deadline, so the idle-connection override tests rely on also governs
// every request type the table does not name.
func TestDeadlineFor_DefaultFollowsConnDeadline(t *testing.T) {
	prev := connDeadline
	connDeadline = 50 * time.Millisecond
	t.Cleanup(func() { connDeadline = prev })
	if got := deadlineFor(protocol.TypeHealth); got != connDeadline {
		t.Fatalf("deadlineFor(health) = %v, want %v", got, connDeadline)
	}
	if got := deadlineFor(protocol.TypeASLAction); got != asl.ActionDeadline {
		t.Fatalf("deadlineFor(asl_action) = %v, want %v", got, asl.ActionDeadline)
	}
}
