// Package backendtest is the shared contract every backend.Backend must pass.
// An adapter's own test package calls Run with a factory that builds a fresh,
// isolated backend per subtest: a scripted fake for Kitty, a private server
// for tmux. The contract speaks only through the backend interface, so a
// behavior that passes here is one domain services can rely on regardless of
// which terminal hosts the agent.
package backendtest

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
)

// Harness is one isolated backend under test.
type Harness struct {
	Backend backend.Backend
	// Directory is an existing directory launches may use as their CWD.
	Directory string
	// EchoArgv returns an argv that writes marker to the terminal and then
	// keeps running until its group is closed.
	EchoArgv func(marker string) []string
	// Timeout bounds every eventual observation. Zero means two seconds.
	Timeout time.Duration
}

// launchVar is the metadata key the contract tags launches with. It is one of
// the keys AgentMux defines, so backends that report only those keys (Kitty)
// still return it.
const launchVar = "amx_launch_id"

// metadataVar is the key the metadata cases write and clear.
const metadataVar = "amx_project"

var markerSeq atomic.Int64

// Run executes the contract. newHarness is called once per subtest.
func Run(t *testing.T, newHarness func(t *testing.T) Harness) {
	t.Helper()
	cases := []struct {
		name string
		fn   func(*testing.T, *contract)
	}{
		{"capabilities", testCapabilities},
		{"launch_is_discoverable", testLaunchDiscoverable},
		{"capture_reads_pane_output", testCapture},
		{"metadata_round_trips", testMetadata},
		{"presentation_follows_capabilities", testPresentation},
		{"close_ends_only_the_target", testClose},
		{"invalid_refs_are_refused_before_acting", testInvalidRefs},
		{"discover_returns_independent_copies", testDiscoverCopies},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if h.Backend == nil || h.EchoArgv == nil || h.Directory == "" {
				t.Fatal("backendtest: harness must set Backend, Directory, and EchoArgv")
			}
			if h.Timeout == 0 {
				h.Timeout = 2 * time.Second
			}
			tc.fn(t, &contract{Harness: h})
		})
	}
}

type contract struct {
	Harness
}

type launched struct {
	id     string
	marker string
	title  string
	pane   backend.Pane
}

// launch starts one agent and waits until discovery reports it.
func (c *contract) launch(t *testing.T, extra map[string]string) launched {
	t.Helper()
	n := markerSeq.Add(1)
	l := launched{
		id:     fmt.Sprintf("contract-%d-%d", time.Now().UnixNano(), n),
		marker: fmt.Sprintf("amx-contract-marker-%d", n),
		title:  fmt.Sprintf("contract %d", n),
	}
	vars := map[string]string{launchVar: l.id}
	maps.Copy(vars, extra)
	err := c.Backend.Launch(backend.LaunchSpec{
		Title:     l.title,
		Directory: c.Directory,
		Vars:      vars,
		Argv:      c.EchoArgv(l.marker),
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	l.pane = c.waitPane(t, l.id, func(backend.Pane) bool { return true })
	return l
}

// waitPane polls Discover until the pane tagged id satisfies ok.
func (c *contract) waitPane(t *testing.T, id string, ok func(backend.Pane) bool) backend.Pane {
	t.Helper()
	var last []backend.Pane
	var lastErr error
	deadline := time.Now().Add(c.Timeout)
	for {
		last, lastErr = c.Backend.Discover()
		if lastErr == nil {
			if p, found := paneByLaunch(last, id); found && ok(p) {
				return p
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never reached the expected state; last discover err=%v panes=%+v", id, lastErr, last)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitGone polls Discover until no pane is tagged id.
func (c *contract) waitGone(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(c.Timeout)
	for {
		panes, err := c.Backend.Discover()
		if err == nil {
			if _, found := paneByLaunch(panes, id); !found {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s still discovered after close (err=%v)", id, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func paneByLaunch(panes []backend.Pane, id string) (backend.Pane, bool) {
	for _, p := range panes {
		if p.Vars[launchVar] == id {
			return p, true
		}
	}
	return backend.Pane{}, false
}

func testCapabilities(t *testing.T, c *contract) {
	if c.Backend.Kind() == "" {
		t.Fatal("Kind is empty")
	}
	caps := c.Backend.Capabilities()
	for _, op := range []backend.Operation{
		backend.OpDiscover, backend.OpLaunch, backend.OpClose, backend.OpCapture, backend.OpMetadata,
	} {
		if !caps.Supports(op) {
			t.Errorf("core operation %s must be supported", op)
		}
	}
	known := make(map[backend.Operation]bool, len(backend.Operations))
	for _, op := range backend.Operations {
		known[op] = true
	}
	// Input is the one operation outside that list: a backend has it exactly
	// when it implements Typist.
	known[backend.OpInput] = true
	for op := range caps {
		if !known[op] {
			t.Errorf("capability %q is not a declared backend.Operation", op)
		}
	}
	if _, types := c.Backend.(backend.Typist); types != caps.Supports(backend.OpInput) {
		t.Errorf("implements Typist = %v, but reports input support = %v", types, caps.Supports(backend.OpInput))
	}
}

func testLaunchDiscoverable(t *testing.T, c *contract) {
	l := c.launch(t, nil)
	p := l.pane
	if p.Ref.Scope != backend.ScopePane || p.Ref.ID == "" {
		t.Errorf("pane ref = %+v, want a pane-scoped ID", p.Ref)
	}
	if p.Group.Scope != backend.ScopeGroup || p.Group.ID == "" {
		t.Errorf("group ref = %+v, want a group-scoped ID", p.Group)
	}
	if err := p.Ref.Validate(); err != nil {
		t.Errorf("discovered pane ref invalid: %v", err)
	}
	if p.GroupTitle != l.title {
		t.Errorf("GroupTitle = %q, want %q", p.GroupTitle, l.title)
	}
	if !samePath(p.CWD, c.Directory) {
		t.Errorf("CWD = %q, want %q", p.CWD, c.Directory)
	}
	panes, err := c.Backend.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !backend.GroupPresent(panes, p.Group) {
		t.Errorf("GroupPresent(%v) = false after launch", p.Group)
	}
	if got, ok := backend.FindPane(panes, p.Ref); !ok || got.Vars[launchVar] != l.id {
		t.Errorf("FindPane(%v) = %+v, %v", p.Ref, got, ok)
	}
}

func testCapture(t *testing.T, c *contract) {
	l := c.launch(t, nil)
	deadline := time.Now().Add(c.Timeout)
	for {
		text, err := c.Backend.Capture(l.pane.Ref, backend.ExtentScreen)
		if err == nil && strings.Contains(text, l.marker) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Capture(screen) never contained %q; last text=%q err=%v", l.marker, text, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	text, err := c.Backend.Capture(l.pane.Ref, backend.ExtentAll)
	if err != nil {
		t.Fatalf("Capture(all): %v", err)
	}
	if !strings.Contains(text, l.marker) {
		t.Errorf("Capture(all) = %q, want it to contain %q", text, l.marker)
	}
	if _, err := c.Backend.Capture(l.pane.Group, backend.ExtentScreen); !errors.Is(err, backend.ErrInvalidRef) {
		t.Errorf("Capture(group ref) err = %v, want ErrInvalidRef", err)
	}

	// A backend that also observes reports the same screen, for that pane and
	// its own kind, with a capture instant.
	observer, ok := c.Backend.(backend.Observer)
	if !ok {
		return
	}
	before := time.Now()
	screen, err := observer.Observe(l.pane.Ref)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !strings.Contains(screen.Text, l.marker) {
		t.Errorf("Observe text = %q, want it to contain %q", screen.Text, l.marker)
	}
	if screen.Pane != l.pane.Ref || screen.Instance.Kind != c.Backend.Kind() || screen.Exited {
		t.Errorf("Observe = pane %v kind %q exited %v, want %v %q false",
			screen.Pane, screen.Instance.Kind, screen.Exited, l.pane.Ref, c.Backend.Kind())
	}
	if screen.CapturedAt.Before(before) || screen.CapturedAt.After(time.Now()) {
		t.Errorf("Observe CapturedAt = %v, outside the read", screen.CapturedAt)
	}
	// The title read with the screen is the one discovery reports for the pane.
	if discovered := c.waitPane(t, l.id, func(backend.Pane) bool { return true }); screen.Title != discovered.Title {
		t.Errorf("Observe title = %q, Discover reports %q", screen.Title, discovered.Title)
	}
	if _, err := observer.Observe(l.pane.Group); !errors.Is(err, backend.ErrInvalidRef) {
		t.Errorf("Observe(group ref) err = %v, want ErrInvalidRef", err)
	}
}

func testMetadata(t *testing.T, c *contract) {
	l := c.launch(t, map[string]string{metadataVar: "before"})
	if got := l.pane.Vars[metadataVar]; got != "before" {
		t.Fatalf("launch var %s = %q, want %q", metadataVar, got, "before")
	}
	if err := c.Backend.SetMetadata(l.pane.Ref, map[string]string{metadataVar: "after"}); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	c.waitPane(t, l.id, func(p backend.Pane) bool { return p.Vars[metadataVar] == "after" })
	if err := c.Backend.SetMetadata(l.pane.Ref, nil); err != nil {
		t.Fatalf("SetMetadata(empty): %v", err)
	}
	if err := c.Backend.SetMetadata(l.pane.Ref, map[string]string{metadataVar: ""}); err != nil {
		t.Fatalf("SetMetadata(clear): %v", err)
	}
	p := c.waitPane(t, l.id, func(p backend.Pane) bool { return p.Vars[metadataVar] == "" })
	if p.Vars[launchVar] != l.id {
		t.Errorf("clearing %s disturbed %s: %+v", metadataVar, launchVar, p.Vars)
	}
	if err := c.Backend.SetMetadata(l.pane.Group, map[string]string{metadataVar: "x"}); !errors.Is(err, backend.ErrInvalidRef) {
		t.Errorf("SetMetadata(group ref) err = %v, want ErrInvalidRef", err)
	}
}

func testPresentation(t *testing.T, c *contract) {
	l := c.launch(t, nil)
	caps := c.Backend.Capabilities()
	group := l.pane.Group
	check := func(op backend.Operation, err error) {
		t.Helper()
		if caps.Supports(op) {
			if err != nil {
				t.Errorf("%s: %v", op, err)
			}
			return
		}
		if !errors.Is(err, backend.ErrUnsupported) {
			t.Errorf("%s unsupported but err = %v, want ErrUnsupported", op, err)
		}
	}
	check(backend.OpFocus, c.Backend.Focus(group))
	check(backend.OpFocus, c.Backend.Focus(l.pane.Ref))
	check(backend.OpColors, c.Backend.SetColors(group, backend.Colors{InactiveBg: "#112233", ActiveBg: backend.ColorReset}))
	check(backend.OpReorder, c.Backend.Move(group, 0))

	retitled := l.title + " renamed"
	check(backend.OpTitle, c.Backend.SetTitle(group, retitled))
	if caps.Supports(backend.OpTitle) {
		c.waitPane(t, l.id, func(p backend.Pane) bool { return p.GroupTitle == retitled })
		viaPane := l.title + " via pane"
		if err := c.Backend.SetTitle(l.pane.Ref, viaPane); err != nil {
			t.Fatalf("SetTitle(pane ref): %v", err)
		}
		c.waitPane(t, l.id, func(p backend.Pane) bool { return p.GroupTitle == viaPane })
	}

	if caps.Supports(backend.OpReorder) {
		other := c.launch(t, nil)
		if err := c.Backend.Move(other.pane.Group, -1); err != nil {
			t.Errorf("Move(-1): %v", err)
		}
		// Reordering must not change which panes exist or what they carry.
		c.waitPane(t, l.id, func(p backend.Pane) bool { return p.Group == group })
		c.waitPane(t, other.id, func(p backend.Pane) bool { return p.Group == other.pane.Group })
	}
}

func testClose(t *testing.T, c *contract) {
	first := c.launch(t, nil)
	second := c.launch(t, nil)
	third := c.launch(t, nil)
	if err := c.Backend.Close(first.pane.Group); err != nil {
		t.Fatalf("Close(group): %v", err)
	}
	c.waitGone(t, first.id)
	c.waitPane(t, second.id, func(backend.Pane) bool { return true })

	if err := c.Backend.Close(second.pane.Ref); err != nil {
		t.Fatalf("Close(pane): %v", err)
	}
	c.waitGone(t, second.id)
	c.waitPane(t, third.id, func(backend.Pane) bool { return true })

	panes, err := c.Backend.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if backend.GroupPresent(panes, first.pane.Group) {
		t.Errorf("closed group %v still present", first.pane.Group)
	}
	if err := c.Backend.Close(first.pane.Group); err == nil {
		t.Error("closing an already-closed group succeeded; want an error")
	}
	if err := c.Backend.SetMetadata(second.pane.Ref, map[string]string{metadataVar: "x"}); err == nil {
		t.Error("SetMetadata on a closed pane succeeded; want an error")
	}
	c.waitPane(t, third.id, func(backend.Pane) bool { return true })
}

func testInvalidRefs(t *testing.T, c *contract) {
	l := c.launch(t, nil)
	before, err := c.Backend.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	refs := []backend.Ref{
		{},
		{Scope: "window", ID: l.pane.Ref.ID},
		{Scope: backend.ScopePane, ID: ""},
		{Scope: backend.ScopeGroup, ID: "   "},
	}
	for _, ref := range refs {
		ops := map[string]error{
			"Close":       c.Backend.Close(ref),
			"SetMetadata": c.Backend.SetMetadata(ref, map[string]string{metadataVar: "x"}),
			"Focus":       c.Backend.Focus(ref),
			"SetTitle":    c.Backend.SetTitle(ref, "x"),
			"SetColors":   c.Backend.SetColors(ref, backend.Colors{InactiveBg: "#000000"}),
			"Move":        c.Backend.Move(ref, 1),
		}
		_, ops["Capture"] = c.Backend.Capture(ref, backend.ExtentScreen)
		for name, err := range ops {
			if !errors.Is(err, backend.ErrInvalidRef) && !errors.Is(err, backend.ErrUnsupported) {
				t.Errorf("%s(%+v) err = %v, want ErrInvalidRef", name, ref, err)
			}
		}
	}
	after, err := c.Backend.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("invalid refs changed topology: %d panes before, %d after", len(before), len(after))
	}
	if p, ok := paneByLaunch(after, l.id); !ok || p.Vars[metadataVar] != "" || p.GroupTitle != l.title {
		t.Errorf("invalid refs disturbed the launched pane: %+v", p)
	}
}

func testDiscoverCopies(t *testing.T, c *contract) {
	l := c.launch(t, map[string]string{metadataVar: "kept"})
	panes, err := c.Backend.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for i := range panes {
		if panes[i].Vars != nil {
			panes[i].Vars[metadataVar] = "mutated"
		}
		panes[i].GroupTitle = "mutated"
	}
	p := c.waitPane(t, l.id, func(backend.Pane) bool { return true })
	if p.Vars[metadataVar] != "kept" || p.GroupTitle != l.title {
		t.Errorf("mutating a Discover result leaked into the next one: %+v", p)
	}
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
