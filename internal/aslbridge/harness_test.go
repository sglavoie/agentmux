package aslbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBridge is an in-process stand-in for `asl bridge serve --stdio`. Every
// supervision behaviour the daemon needs — handshake failure, contamination,
// oversize frames, restart backoff, leak-free shutdown — is reachable through
// it without spawning a process.
type fakeBridge struct {
	t *testing.T

	// script runs on its own goroutine with the child's stdout writer. It
	// returns when the child should stop writing.
	script func(*fakeBridge)

	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter

	requests chan map[string]any

	mu        sync.Mutex
	exitOnce  sync.Once
	exited    chan struct{}
	scriptEnd chan struct{}
	readerEnd chan struct{}
}

func newFakeBridge(t *testing.T, script func(*fakeBridge)) *fakeBridge {
	t.Helper()
	sr, sw := io.Pipe()
	or, ow := io.Pipe()
	return &fakeBridge{
		t:            t,
		script:       script,
		stdinReader:  sr,
		stdinWriter:  sw,
		stdoutReader: or,
		stdoutWriter: ow,
		requests:     make(chan map[string]any, 64),
		exited:       make(chan struct{}),
		scriptEnd:    make(chan struct{}),
		readerEnd:    make(chan struct{}),
	}
}

func (f *fakeBridge) child() *Child {
	go func() {
		defer close(f.readerEnd)
		scanner := bufio.NewScanner(f.stdinReader)
		scanner.Buffer(make([]byte, 0, 64<<10), MaxFrameBytes*2)
		for scanner.Scan() {
			var req map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
				continue
			}
			select {
			case f.requests <- req:
			default:
			}
		}
		// Stdin close is the daemon's end-of-stream signal.
		f.exit()
	}()
	go func() {
		defer close(f.scriptEnd)
		if f.script != nil {
			f.script(f)
		}
	}()
	return &Child{
		Path:      "/fake/asl",
		Stdin:     f.stdinWriter,
		Stdout:    f.stdoutReader,
		Stderr:    strings.NewReader(""),
		Wait:      f.wait,
		Terminate: f.terminate,
	}
}

func (f *fakeBridge) launcher() Launcher {
	return func(context.Context) (*Child, error) { return f.child(), nil }
}

// writeLine emits one raw stdout line. Tests use it for contamination and
// oversize cases; well-formed frames go through writeFrame.
func (f *fakeBridge) writeLine(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, _ = io.WriteString(f.stdoutWriter, s+"\n")
}

func (f *fakeBridge) writeFrame(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		f.t.Fatalf("marshalling fake frame: %v", err)
	}
	f.writeLine(string(data))
}

func (f *fakeBridge) hello(capabilities ...string) {
	if len(capabilities) == 0 {
		capabilities = []string{MethodRunsSnapshot, MethodQuestionGet, MethodQuestionReply, MethodWorkbenchEnsure}
	}
	f.writeFrame(map[string]any{
		"type":                  "hello",
		"protocol":              ProtocolVersion,
		"asl_version":           "9.9.9",
		"capabilities":          capabilities,
		"max_frame_bytes":       MaxFrameBytes,
		"poll_interval_seconds": 1.0,
	})
}

func (f *fakeBridge) respond(id string, result any) {
	f.writeFrame(map[string]any{
		"type": "response", "protocol": ProtocolVersion, "id": id, "ok": true, "result": result,
	})
}

func (f *fakeBridge) refuse(id string, e Error) {
	f.writeFrame(map[string]any{
		"type": "response", "protocol": ProtocolVersion, "id": id, "ok": false, "error": e,
	})
}

func (f *fakeBridge) emit(seq uint64, name string, data any) {
	f.writeFrame(map[string]any{
		"type": "event", "protocol": ProtocolVersion, "seq": seq, "event": name, "data": data,
	})
}

// nextRequestOf returns the next request for the given method, failing the test
// if none arrives promptly.
func (f *fakeBridge) nextRequestOf(method string) map[string]any {
	f.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case req := <-f.requests:
			if req["method"] == method {
				return req
			}
		case <-deadline:
			f.t.Fatalf("timed out waiting for a %s request", method)
			return nil
		}
	}
}

// exit ends the session the way a crashing or finishing child would: stdout
// reaches EOF and Wait returns.
func (f *fakeBridge) exit() {
	f.exitOnce.Do(func() {
		_ = f.stdoutWriter.Close()
		close(f.exited)
	})
}

func (f *fakeBridge) wait() error {
	<-f.exited
	return nil
}

func (f *fakeBridge) terminate() error {
	f.exit()
	return nil
}

// emptySnapshot is the minimal well-formed runs.snapshot result.
func emptySnapshot() map[string]any {
	return map[string]any{"protocol": ProtocolVersion, "rows": []any{}, "row_count": 0, "digest": "d0"}
}

// serveSnapshots answers every runs.snapshot request until the daemon closes
// stdin. It is the "healthy bridge" script.
func serveSnapshots(f *fakeBridge) {
	f.hello()
	for {
		select {
		case req := <-f.requests:
			if req["method"] == MethodRunsSnapshot {
				f.respond(req["id"].(string), emptySnapshot())
			}
		case <-f.exited:
			return
		}
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitFor polls until cond holds or the test fails.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
