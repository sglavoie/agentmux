package commands

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

type recordingCapturer struct {
	calls []backend.Ref
	ext   []backend.Extent
	text  string
	err   error
}

func (c *recordingCapturer) Capture(ref backend.Ref, extent backend.Extent) (string, error) {
	c.calls = append(c.calls, ref)
	c.ext = append(c.ext, extent)
	return c.text, c.err
}

func screenOnce(t *testing.T, sources CaptureSources, req CaptureRequest) CaptureOutputMsg {
	t.Helper()
	msg, ok := CaptureScreen(sources, req)().(CaptureOutputMsg)
	if !ok {
		t.Fatalf("the capture returned a %T, want a CaptureOutputMsg", msg)
	}
	return msg
}

// A Kitty target is read exactly as CaptureOutput reads a window.
func TestCaptureScreenReadsAKittyTargetThroughKitty(t *testing.T) {
	client := newCapturingKittyClient()
	client.text[7] = "alpha\nbravo\n"
	tmux := &recordingCapturer{text: "never"}
	sources := CaptureSources{Kitty: client, Panes: map[runtimesession.BackendKind]backend.Capturer{
		runtimesession.BackendTmux: tmux,
	}}

	req := CaptureRequest{Target: session.KittyPaneTarget(7), WindowID: 7, Generation: 3, RequestID: 5, Width: 20, Height: 4}
	msg := screenOnce(t, sources, req)
	if msg.Err != nil || len(msg.Lines) != 2 || msg.Lines[1] != "bravo" || msg.Request != req {
		t.Fatalf("got %+v", msg)
	}
	if len(tmux.calls) != 0 {
		t.Fatal("a Kitty target reached the tmux capturer")
	}

	// A request that names only a window ID still works.
	legacy := CaptureRequest{WindowID: 7, RequestID: 6, Width: 20, Height: 4}
	if msg := screenOnce(t, sources, legacy); msg.Err != nil || len(msg.Lines) != 2 || msg.Request != legacy {
		t.Fatalf("legacy request got %+v", msg)
	}
}

// Any other backend is read through its capturer, clipped the same way.
func TestCaptureScreenReadsAnotherBackendThroughItsCapturer(t *testing.T) {
	client := newCapturingKittyClient()
	tmux := &recordingCapturer{text: "alpha\nbravo\ncharlie\ndelta-is-a-long-line\n"}
	sources := CaptureSources{Kitty: client, Panes: map[runtimesession.BackendKind]backend.Capturer{
		runtimesession.BackendTmux: tmux,
	}}
	target := session.PaneTarget{Backend: runtimesession.BackendTmux, Pane: backend.PaneID("%3")}
	req := CaptureRequest{Target: target, Generation: 2, RequestID: 9, Width: 8, Height: 2}

	msg := screenOnce(t, sources, req)
	if msg.Err != nil || msg.Request != req {
		t.Fatalf("got %+v", msg)
	}
	if want := []string{"charlie", "delta-i…"}; len(msg.Lines) != 2 || msg.Lines[0] != want[0] || msg.Lines[1] != want[1] {
		t.Fatalf("lines %q, want %q", msg.Lines, want)
	}
	if len(tmux.calls) != 1 || tmux.calls[0] != backend.PaneID("%3") || tmux.ext[0] != backend.ExtentAll {
		t.Fatalf("capturer calls %v %v", tmux.calls, tmux.ext)
	}
	if len(client.calls) != 0 {
		t.Fatalf("a tmux target reached Kitty: %v", client.calls)
	}
}

// A read that cannot be taken always replies, with an error and no lines.
func TestCaptureScreenFailuresReplyWithoutContent(t *testing.T) {
	target := session.PaneTarget{Backend: runtimesession.BackendTmux, Pane: backend.PaneID("%3")}
	req := CaptureRequest{Target: target, RequestID: 4, Width: 20, Height: 4}

	noSource := screenOnce(t, CaptureSources{Kitty: newCapturingKittyClient()}, req)
	if !errors.Is(noSource.Err, ErrCaptureUnavailable) || noSource.Lines != nil || noSource.Request != req {
		t.Fatalf("no source: %+v", noSource)
	}

	gone := errors.New("no server running")
	failing := &recordingCapturer{text: "text returned beside an error", err: gone}
	failed := screenOnce(t, CaptureSources{Panes: map[runtimesession.BackendKind]backend.Capturer{
		runtimesession.BackendTmux: failing,
	}}, req)
	if !errors.Is(failed.Err, gone) || failed.Lines != nil || failed.Request != req {
		t.Fatalf("failing capturer: %+v", failed)
	}

	kittyReq := CaptureRequest{Target: session.KittyPaneTarget(7), WindowID: 7, RequestID: 8, Width: 20, Height: 4}
	noKitty := screenOnce(t, CaptureSources{}, kittyReq)
	if !errors.Is(noKitty.Err, ErrCaptureUnavailable) || noKitty.Lines != nil || noKitty.Request != kittyReq {
		t.Fatalf("no Kitty client: %+v", noKitty)
	}
}
