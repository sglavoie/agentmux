package kitty

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// fakeKitty is a stateful `kitten @` stand-in for the shared backend contract.
// It models one OS window of tabs, each holding one window, and accepts only
// the argv shapes SubprocessClient issues and only `id:N` / `window_id:N`
// matches, so any other match expression reaching it fails the test the way
// an unexpected selector would misfire against real Kitty.
type fakeKitty struct {
	mu     sync.Mutex
	nextID int
	tabs   []*fakeTab
	active int // tab ID
	calls  [][]string
}

type fakeTab struct {
	id     int
	title  string
	window fakeWindow
}

type fakeWindow struct {
	id   int
	cwd  string
	argv []string
	vars map[string]string
	text string
}

// fakeEchoCommand is the argv[0] fakeKitty treats as "print argv[1]".
const fakeEchoCommand = "amx-contract-echo"

func newFakeKitty() *fakeKitty { return &fakeKitty{nextID: 1} }

var errNoMatch = errors.New("exit status 1")

func (f *fakeKitty) Run(name string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	if name != "kitten" || len(args) < 2 || args[0] != "@" {
		return nil, []byte("unexpected command"), fmt.Errorf("fake kitty: unexpected %s %v", name, args)
	}
	args = args[1:]
	if args[0] == "--to" {
		args = args[2:]
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "ls":
		return f.ls()
	case "launch":
		return nil, nil, f.launch(rest)
	case "close-tab":
		return f.withTab(rest, func(t *fakeTab, _ []string) error {
			f.tabs = slices.DeleteFunc(f.tabs, func(o *fakeTab) bool { return o == t })
			return nil
		})
	case "focus-tab":
		return f.withTab(rest, func(t *fakeTab, _ []string) error { f.active = t.id; return nil })
	case "focus-window":
		return f.withWindow(rest, func(t *fakeTab, _ []string) error { f.active = t.id; return nil })
	case "set-tab-title":
		return f.withTab(rest, func(t *fakeTab, pos []string) error {
			if len(pos) != 2 || pos[0] != "--" {
				return fmt.Errorf("fake kitty: set-tab-title positional %v", pos)
			}
			t.title = pos[1]
			return nil
		})
	case "set-tab-color":
		return f.withTab(rest, func(*fakeTab, []string) error { return nil })
	case "set-user-vars":
		return f.withWindow(rest, func(t *fakeTab, pos []string) error {
			for _, kv := range pos {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("fake kitty: bad var %q", kv)
				}
				if t.window.vars == nil {
					t.window.vars = map[string]string{}
				}
				t.window.vars[k] = v
			}
			return nil
		})
	case "get-text":
		var text string
		_, _, err := f.withWindow(rest, func(t *fakeTab, pos []string) error {
			for _, p := range pos {
				if p != "--extent=screen" && p != "--extent=all" {
					return fmt.Errorf("fake kitty: get-text arg %q", p)
				}
			}
			text = t.window.text
			return nil
		})
		return []byte(text), nil, err
	case "action":
		return nil, nil, f.action(rest)
	}
	return nil, []byte("unknown command"), fmt.Errorf("fake kitty: unknown command %q", cmd)
}

func (f *fakeKitty) ls() ([]byte, []byte, error) {
	tabs := make([]Tab, 0, len(f.tabs))
	for _, t := range f.tabs {
		varsJSON, err := json.Marshal(t.window.vars)
		if err != nil {
			return nil, nil, err
		}
		var uv UserVars
		if len(t.window.vars) > 0 {
			if err := json.Unmarshal(varsJSON, &uv); err != nil {
				return nil, nil, err
			}
		}
		title := ""
		if len(t.window.argv) > 0 {
			title = t.window.argv[0]
		}
		tabs = append(tabs, Tab{
			ID:       t.id,
			IsActive: t.id == f.active,
			Title:    t.title,
			Windows: []Window{{
				ID: t.window.id, IsActive: true, Title: title, CWD: t.window.cwd,
				Cmdline: t.window.argv, UserVars: uv,
			}},
		})
	}
	data, err := json.Marshal([]OSWindow{{ID: 1, IsActive: true, IsFocused: true, Tabs: tabs}})
	return data, nil, err
}

func (f *fakeKitty) launch(args []string) error {
	t := &fakeTab{}
	location := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--type=tab", a == "--no-response":
		case a == "--":
			t.window.argv = slices.Clone(args[i+1:])
			i = len(args)
		case strings.HasPrefix(a, "--tab-title="):
			t.title = strings.TrimPrefix(a, "--tab-title=")
		case strings.HasPrefix(a, "--cwd="):
			t.window.cwd = strings.TrimPrefix(a, "--cwd=")
		case strings.HasPrefix(a, "--location="):
			location = strings.TrimPrefix(a, "--location=")
		case strings.HasPrefix(a, "--env="):
		case strings.HasPrefix(a, "--var="):
			k, v, _ := strings.Cut(strings.TrimPrefix(a, "--var="), "=")
			if t.window.vars == nil {
				t.window.vars = map[string]string{}
			}
			t.window.vars[k] = v
		default:
			return fmt.Errorf("fake kitty: launch arg %q", a)
		}
	}
	t.id = f.nextID
	t.window.id = f.nextID + 1000
	f.nextID++
	if len(t.window.argv) == 2 && t.window.argv[0] == fakeEchoCommand {
		t.window.text = t.window.argv[1] + "\n"
	}
	at := len(f.tabs)
	cur := f.activeIndex()
	switch location {
	case "first":
		at = 0
	case "before":
		if cur >= 0 {
			at = cur
		}
	case "after":
		if cur >= 0 {
			at = cur + 1
		}
	}
	f.tabs = slices.Insert(f.tabs, at, t)
	f.active = t.id
	return nil
}

func (f *fakeKitty) action(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("fake kitty: action %v", args)
	}
	cur := f.activeIndex()
	if cur < 0 {
		return errNoMatch
	}
	n := len(f.tabs)
	switch args[0] {
	case "move_tab_forward":
		next := (cur + 1) % n
		f.tabs[cur], f.tabs[next] = f.tabs[next], f.tabs[cur]
	case "move_tab_backward":
		prev := (cur - 1 + n) % n
		f.tabs[cur], f.tabs[prev] = f.tabs[prev], f.tabs[cur]
	default:
		return fmt.Errorf("fake kitty: action %q", args[0])
	}
	return nil
}

func (f *fakeKitty) activeIndex() int {
	return slices.IndexFunc(f.tabs, func(t *fakeTab) bool { return t.id == f.active })
}

// withTab resolves a tab-selecting --match (id:N is a tab ID, window_id:N the
// tab holding that window) and passes the remaining positional args.
func (f *fakeKitty) withTab(args []string, fn func(*fakeTab, []string) error) ([]byte, []byte, error) {
	match, pos, err := splitMatch(args)
	if err != nil {
		return nil, nil, err
	}
	kind, id, err := parseMatch(match)
	if err != nil {
		return nil, []byte(err.Error()), err
	}
	for _, t := range f.tabs {
		if (kind == "id" && t.id == id) || (kind == "window_id" && t.window.id == id) {
			return nil, nil, fn(t, pos)
		}
	}
	return nil, []byte("No matching tabs for expression: " + match), errNoMatch
}

// withWindow resolves a window-selecting --match (id:N is a window ID).
func (f *fakeKitty) withWindow(args []string, fn func(*fakeTab, []string) error) ([]byte, []byte, error) {
	match, pos, err := splitMatch(args)
	if err != nil {
		return nil, nil, err
	}
	kind, id, err := parseMatch(match)
	if err != nil || kind != "id" {
		return nil, []byte("bad window match " + match), fmt.Errorf("fake kitty: window match %q", match)
	}
	for _, t := range f.tabs {
		if t.window.id == id {
			return nil, nil, fn(t, pos)
		}
	}
	return nil, []byte("No matching windows for expression: " + match), errNoMatch
}

func splitMatch(args []string) (string, []string, error) {
	var match string
	var pos []string
	for i, a := range args {
		switch {
		case a == "--no-response":
		case strings.HasPrefix(a, "--match="):
			match = strings.TrimPrefix(a, "--match=")
		case a == "--":
			pos = append(pos, args[i:]...)
			return match, pos, nil
		default:
			pos = append(pos, a)
		}
	}
	if match == "" {
		return "", nil, errors.New("fake kitty: missing --match")
	}
	return match, pos, nil
}

func parseMatch(match string) (string, int, error) {
	kind, raw, ok := strings.Cut(match, ":")
	if !ok || (kind != "id" && kind != "window_id") {
		return "", 0, fmt.Errorf("fake kitty: unsupported match %q", match)
	}
	id, err := strconv.Atoi(raw)
	if err != nil || strconv.Itoa(id) != raw {
		return "", 0, fmt.Errorf("fake kitty: unsupported match %q", match)
	}
	return kind, id, nil
}
