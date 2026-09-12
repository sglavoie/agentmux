// The capture command's own tests: what one read returns, what it keeps, and
// what several of them do to each other.
//
// The client here is a real kitty.KittyClient rather than a stub of the command,
// because the two claims worth making are about the boundary — the reply always
// carries the request that caused it, and the lines it carries retain nothing of
// the scrollback they were cut from.
package commands

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// capturingKittyClient answers GetText from a per-window script and can be told
// to park a read until the test releases it.
//
// It is concurrency-safe on purpose: the scheduler above it is allowed to have
// four reads out at once, so a fake that raced under -race would report the
// test harness's bug as the subject's.
type capturingKittyClient struct {
	mu    sync.Mutex
	text  map[int]string
	err   map[int]error
	calls []int

	// gate, when non-nil, holds every read until the test closes the window's
	// channel, which is what lets replies be released in an arbitrary order.
	gate map[int]chan struct{}
}

func newCapturingKittyClient() *capturingKittyClient {
	return &capturingKittyClient{text: map[int]string{}, err: map[int]error{}}
}

func (c *capturingKittyClient) GetText(matchExpr, extent string) (string, error) {
	windowID := windowIDOf(matchExpr)

	c.mu.Lock()
	gate := c.gate[windowID]
	c.mu.Unlock()
	if gate != nil {
		<-gate
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, windowID)
	if extent != "all" {
		return "", fmt.Errorf("capture asked for extent %q, want the full scrollback", extent)
	}
	if err := c.err[windowID]; err != nil {
		return "", err
	}
	return c.text[windowID], nil
}

// windowIDOf reverses kitty.MatchWindowByID, so the fake answers the window the
// command actually asked for rather than one the test assumed it would.
func windowIDOf(matchExpr string) int {
	var id int
	if _, err := fmt.Sscanf(matchExpr, "id:%d", &id); err != nil {
		return -1
	}
	return id
}

func (c *capturingKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	panic("unexpected ListWindows")
}
func (c *capturingKittyClient) LaunchTab(kitty.LaunchOpts) error { panic("unexpected LaunchTab") }
func (c *capturingKittyClient) LaunchOverlay(kitty.LaunchOpts) error {
	panic("unexpected LaunchOverlay")
}
func (c *capturingKittyClient) CloseTab(string) error     { panic("unexpected CloseTab") }
func (c *capturingKittyClient) MoveTab(string, int) error { panic("unexpected MoveTab") }
func (c *capturingKittyClient) SetTabColor(string, string, string, string, string) error {
	panic("unexpected SetTabColor")
}
func (c *capturingKittyClient) SetTabTitle(string, string) error { panic("unexpected SetTabTitle") }
func (c *capturingKittyClient) SetUserVars(string, map[string]string) error {
	panic("unexpected SetUserVars")
}
func (c *capturingKittyClient) FocusTab(string) error    { panic("unexpected FocusTab") }
func (c *capturingKittyClient) FocusWindow(string) error { panic("unexpected FocusWindow") }
func (c *capturingKittyClient) LaunchOverlayPager(string) error {
	panic("unexpected LaunchOverlayPager")
}

// captureOnce runs one capture command and returns its message.
func captureOnce(t *testing.T, client kitty.KittyClient, req CaptureRequest) CaptureOutputMsg {
	t.Helper()
	msg, ok := CaptureOutput(client, req)().(CaptureOutputMsg)
	if !ok {
		t.Fatalf("the capture returned a %T, want a CaptureOutputMsg", msg)
	}
	return msg
}

// TestACaptureReturnsTheVisibleTailClippedToTheTile is the shape of the answer:
// the last Height lines, each at most Width cells, newest last.
func TestACaptureReturnsTheVisibleTailClippedToTheTile(t *testing.T) {
	client := newCapturingKittyClient()
	client.text[7] = "alpha\nbravo\ncharlie\ndelta-is-a-long-line\n"

	msg := captureOnce(t, client, CaptureRequest{WindowID: 7, Width: 8, Height: 2})

	want := []string{"charlie", "delta-i…"}
	if len(msg.Lines) != len(want) {
		t.Fatalf("the capture returned %q, want the last two lines %q", msg.Lines, want)
	}
	for i := range want {
		if msg.Lines[i] != want[i] {
			t.Fatalf("line %d is %q, want %q", i, msg.Lines[i], want[i])
		}
	}
	if msg.Err != nil {
		t.Fatalf("a successful capture reported %v", msg.Err)
	}
}

// TestACaptureEchoesItsWholeRequest. The reply is what releases the scheduler's
// slot and what it judges the content by, so every field has to survive the round
// trip — a reply that lost its request ID would strand a slot forever.
func TestACaptureEchoesItsWholeRequest(t *testing.T) {
	client := newCapturingKittyClient()
	client.text[9] = "output\n"
	req := CaptureRequest{WindowID: 9, Generation: 12, RequestID: 34, Width: 20, Height: 3}

	if got := captureOnce(t, client, req).Request; got != req {
		t.Fatalf("the reply carried %+v, want the request %+v", got, req)
	}
}

// TestAFailedCaptureCarriesItsErrorAndNoLines. The failure is a fact about one
// window, so it comes back stamped like any other reply rather than as a dropped
// message the caller has to time out on.
func TestAFailedCaptureCarriesItsErrorAndNoLines(t *testing.T) {
	client := newCapturingKittyClient()
	client.err[3] = errors.New("kitten @ get-text: no matching window")

	msg := captureOnce(t, client, CaptureRequest{WindowID: 3, RequestID: 1, Width: 20, Height: 4})
	if msg.Err == nil {
		t.Fatal("a failed read reported no error")
	}
	if msg.Lines != nil {
		t.Fatalf("a failed read carried %q, want no lines", msg.Lines)
	}
	if msg.Request.RequestID != 1 {
		t.Fatalf("the failed reply carried request %d, want 1", msg.Request.RequestID)
	}
}

// TestAnEmptyTerminalIsAnEmptySuccess is the distinction the loading state
// depends on: a window with nothing in it answers with no error and no lines,
// which the caller stores as a landed read rather than leaving as a pending one.
func TestAnEmptyTerminalIsAnEmptySuccess(t *testing.T) {
	client := newCapturingKittyClient()
	client.text[5] = "\n\n"

	msg := captureOnce(t, client, CaptureRequest{WindowID: 5, Width: 20, Height: 4})
	if msg.Err != nil || len(msg.Lines) != 0 {
		t.Fatalf("an empty terminal returned %q / %v, want no lines and no error", msg.Lines, msg.Err)
	}
}

// TestWindowZeroIsAnsweredWithoutAskingKitty. A row with no live window still
// holds a slot against its reply, so the command answers immediately rather than
// running a subprocess against a window that cannot exist.
func TestWindowZeroIsAnsweredWithoutAskingKitty(t *testing.T) {
	client := newCapturingKittyClient()

	msg := captureOnce(t, client, CaptureRequest{WindowID: 0, RequestID: 8, Width: 20, Height: 4})
	if msg.Err != nil || msg.Lines != nil {
		t.Fatalf("window zero returned %q / %v, want an empty success", msg.Lines, msg.Err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("window zero produced %d Kitty calls, want none", len(client.calls))
	}
}

// TestAClippedCaptureRetainsNothingOfTheScrollback is the memory contract, and it
// is asserted against the string headers because there is no other way to see it:
// a tail slice of strings.Split's output LOOKS like nine short lines and keeps a
// megabyte of history alive behind them.
func TestAClippedCaptureRetainsNothingOfTheScrollback(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "line %04d of a long scrollback\n", i)
	}
	scrollback := b.String()

	lines := ClipCapture(scrollback, 40, 9)
	if len(lines) != 9 {
		t.Fatalf("the clip returned %d lines, want 9", len(lines))
	}
	for i, line := range lines {
		if aliases(line, scrollback) {
			t.Fatalf("line %d points into the scrollback's own memory; the whole capture is retained behind it", i)
		}
	}
}

// TestAClippedCaptureIsBoundedByTheTile. Width is counted in display cells and
// height in lines, and both are ceilings rather than targets.
func TestAClippedCaptureIsBoundedByTheTile(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(strings.Repeat("x", 500) + "\n")
	}

	for _, size := range [][2]int{{1, 1}, {10, 3}, {44, 9}, {200, 40}} {
		lines := ClipCapture(b.String(), size[0], size[1])
		if len(lines) > size[1] {
			t.Fatalf("%dx%d returned %d lines, want at most %d", size[0], size[1], len(lines), size[1])
		}
		for _, line := range lines {
			if len([]rune(line)) > size[0] {
				t.Fatalf("%dx%d returned a %d-cell line, want at most %d", size[0], size[1], len([]rune(line)), size[0])
			}
		}
	}
}

// TestADegenerateTileCapturesNothing. A tile with no room for output is not a
// tile with one line of it.
func TestADegenerateTileCapturesNothing(t *testing.T) {
	for _, size := range [][2]int{{0, 5}, {5, 0}, {-1, 5}, {5, -1}, {0, 0}} {
		if got := ClipCapture("alpha\nbravo\n", size[0], size[1]); got != nil {
			t.Fatalf("%dx%d captured %q, want nothing", size[0], size[1], got)
		}
	}
}

// TestConcurrentCapturesAreIndependent releases four parked reads in the reverse
// of the order they were issued.
//
// The property is that a reply belongs to its own request and to nothing else:
// the scheduler above this treats a reply as the release of one slot and the
// content of one tile, and a command that closed over shared state would make the
// order replies happen to arrive change which tile got which text.
func TestConcurrentCapturesAreIndependent(t *testing.T) {
	client := newCapturingKittyClient()
	client.gate = map[int]chan struct{}{}
	windows := []int{11, 22, 33, 44}
	for i, windowID := range windows {
		client.text[windowID] = fmt.Sprintf("window %d\n", windowID)
		client.gate[windowID] = make(chan struct{})
		if i == 2 {
			client.err[windowID] = errors.New("boom")
		}
	}

	replies := make(chan CaptureOutputMsg, len(windows))
	for i, windowID := range windows {
		req := CaptureRequest{
			WindowID: windowID, Generation: i + 1, RequestID: 100 + i, Width: 40, Height: 4,
		}
		go func() {
			msg, _ := CaptureOutput(client, req)().(CaptureOutputMsg)
			replies <- msg
		}()
	}

	// Released newest first, so an implementation that answered in issue order
	// would deadlock rather than pass.
	for i := len(windows) - 1; i >= 0; i-- {
		close(client.gate[windows[i]])
	}

	seen := map[int]CaptureOutputMsg{}
	for range windows {
		msg := <-replies
		seen[msg.Request.WindowID] = msg
	}
	for i, windowID := range windows {
		msg, ok := seen[windowID]
		if !ok {
			t.Fatalf("window %d never answered", windowID)
		}
		if msg.Request.RequestID != 100+i || msg.Request.Generation != i+1 {
			t.Fatalf("window %d answered request %+v, want request %d/generation %d",
				windowID, msg.Request, 100+i, i+1)
		}
		if windowID == 33 {
			if msg.Err == nil {
				t.Fatalf("window %d was told to fail and did not", windowID)
			}
			continue
		}
		if msg.Err != nil {
			t.Fatalf("window %d failed with %v; only window 33 was told to", windowID, msg.Err)
		}
		if len(msg.Lines) != 1 || msg.Lines[0] != fmt.Sprintf("window %d", windowID) {
			t.Fatalf("window %d returned %q, want its own text", windowID, msg.Lines)
		}
	}
}

// aliases reports whether s points into container's backing array, which is what
// a substring of it does and what a copy of it does not.
func aliases(s, container string) bool {
	if len(s) == 0 || len(container) == 0 {
		return false
	}
	start := uintptr(unsafe.Pointer(unsafe.StringData(container)))
	at := uintptr(unsafe.Pointer(unsafe.StringData(s)))
	return at >= start && at < start+uintptr(len(container))
}
