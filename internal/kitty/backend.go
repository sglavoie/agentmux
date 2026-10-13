package kitty

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Backend adapts a KittyClient to backend.Backend. It is the only place a
// backend.Ref becomes a Kitty match expression: a group is a tab and a pane is
// a window, both addressed by Kitty's decimal ID. Refs are validated before
// any `kitten @` call so a caller can never smuggle a match expression (such
// as "1 or title:x") through an ID.
type Backend struct {
	client KittyClient
}

var _ backend.Backend = (*Backend)(nil)

// NewBackend wraps client. The adapter holds no state of its own, so wrapping
// at each call site is cheap.
func NewBackend(client KittyClient) *Backend {
	return &Backend{client: client}
}

// GroupRef is the backend reference for a Kitty tab ID.
func GroupRef(tabID int) backend.Ref { return backend.GroupID(strconv.Itoa(tabID)) }

// PaneRef is the backend reference for a Kitty window ID.
func PaneRef(windowID int) backend.Ref { return backend.PaneID(strconv.Itoa(windowID)) }

// Kind reports the Kitty backend kind.
func (b *Backend) Kind() runtimesession.BackendKind { return runtimesession.BackendKitty }

// Capabilities reports that Kitty performs every backend operation.
func (b *Backend) Capabilities() backend.Capabilities {
	caps := make(backend.Capabilities, len(backend.Operations))
	for _, op := range backend.Operations {
		caps[op] = true
	}
	return caps
}

// Discover flattens `kitten @ ls` into one entry per window.
func (b *Backend) Discover() ([]backend.Pane, error) {
	osWindows, err := b.client.ListWindows()
	if err != nil {
		return nil, err
	}
	var panes []backend.Pane
	for _, osw := range osWindows {
		for _, tab := range osw.Tabs {
			group := GroupRef(tab.ID)
			for _, win := range tab.Windows {
				pane := backend.Pane{
					Ref:         PaneRef(win.ID),
					Group:       group,
					GroupTitle:  tab.Title,
					Title:       win.Title,
					CWD:         win.CWD,
					Active:      win.IsActive,
					GroupActive: tab.IsActive,
				}
				vars, err := userVarMap(win.UserVars)
				if err != nil {
					return nil, err
				}
				pane.Vars = vars
				for _, fp := range win.ForegroundProcesses {
					pane.Foreground = append(pane.Foreground, backend.Process{
						PID:  fp.PID,
						Argv: append([]string(nil), fp.Cmdline...),
						CWD:  fp.CWD,
					})
				}
				panes = append(panes, pane)
			}
		}
	}
	return panes, nil
}

// Launch opens a new tab running spec.Argv.
func (b *Backend) Launch(spec backend.LaunchSpec) error {
	location, err := kittyLocation(spec.Placement)
	if err != nil {
		return err
	}
	return b.client.LaunchTab(LaunchOpts{
		Title:     spec.Title,
		Directory: spec.Directory,
		Location:  location,
		Env:       spec.Env,
		UserVars:  spec.Vars,
		Cmdline:   spec.Argv,
	})
}

// Close closes the tab a group ref names or a pane ref belongs to.
func (b *Backend) Close(ref backend.Ref) error {
	match, err := tabMatch(ref)
	if err != nil {
		return err
	}
	return b.client.CloseTab(match)
}

// Capture reads text from a window.
func (b *Backend) Capture(ref backend.Ref, extent backend.Extent) (string, error) {
	id, err := paneID(ref)
	if err != nil {
		return "", err
	}
	switch extent {
	case backend.ExtentScreen, backend.ExtentAll:
	default:
		return "", fmt.Errorf("kitty: unknown capture extent %q", extent)
	}
	return b.client.GetText(MatchWindowByID(id), string(extent))
}

// Observe reads a window's visible screen and its title. Kitty's get-text
// reports neither the window size nor the screen mode, and a Kitty window
// closes with its program, so those fields stay zero; the instance is named
// by kind only because the client does not expose which socket it talks to.
// The title comes from the window tree, read before the screen so the text is
// the later of the two.
func (b *Backend) Observe(ref backend.Ref) (backend.Screen, error) {
	id, err := paneID(ref)
	if err != nil {
		return backend.Screen{}, err
	}
	osWindows, err := b.client.ListWindows()
	if err != nil {
		return backend.Screen{}, err
	}
	_, _, win, found := FindWindowLocationByID(osWindows, id)
	if !found {
		return backend.Screen{}, fmt.Errorf("kitty: window %d not found", id)
	}
	text, err := b.Capture(ref, backend.ExtentScreen)
	if err != nil {
		return backend.Screen{}, err
	}
	return backend.Screen{
		Instance:   runtimesession.BackendInstance{Kind: runtimesession.BackendKitty},
		Pane:       ref,
		Text:       text,
		Title:      win.Title,
		CapturedAt: time.Now(),
	}, nil
}

// SetMetadata writes Kitty user vars onto a window.
func (b *Backend) SetMetadata(ref backend.Ref, vars map[string]string) error {
	id, err := paneID(ref)
	if err != nil {
		return err
	}
	return b.client.SetUserVars(MatchWindowByID(id), vars)
}

// Focus focuses a tab (group ref) or a window (pane ref).
func (b *Backend) Focus(ref backend.Ref) error {
	id, err := refID(ref)
	if err != nil {
		return err
	}
	if ref.Scope == backend.ScopePane {
		return b.client.FocusWindow(MatchWindowByID(id))
	}
	return b.client.FocusTab(MatchTabByID(id))
}

// SetTitle sets the title of the tab a ref names or contains.
func (b *Backend) SetTitle(ref backend.Ref, title string) error {
	match, err := tabMatch(ref)
	if err != nil {
		return err
	}
	return b.client.SetTabTitle(match, title)
}

// SetColors sets tab colors; backend.ColorReset maps to Kitty's NONE.
func (b *Backend) SetColors(ref backend.Ref, colors backend.Colors) error {
	match, err := tabMatch(ref)
	if err != nil {
		return err
	}
	return b.client.SetTabColor(match,
		kittyColor(colors.ActiveBg), kittyColor(colors.InactiveBg),
		kittyColor(colors.ActiveFg), kittyColor(colors.InactiveFg))
}

// Move slides a tab within its OS window.
func (b *Backend) Move(ref backend.Ref, delta int) error {
	match, err := tabMatch(ref)
	if err != nil {
		return err
	}
	return b.client.MoveTab(match, delta)
}

// tabMatch selects the tab a ref names (id:N) or the tab containing the
// window a pane ref names (window_id:N).
func tabMatch(ref backend.Ref) (string, error) {
	id, err := refID(ref)
	if err != nil {
		return "", err
	}
	if ref.Scope == backend.ScopePane {
		return MatchTabByWindowID(id), nil
	}
	return MatchTabByID(id), nil
}

func paneID(ref backend.Ref) (int, error) {
	id, err := refID(ref)
	if err != nil {
		return 0, err
	}
	if ref.Scope != backend.ScopePane {
		return 0, &backend.InvalidRefError{Ref: ref, Reason: "kitty: operation needs a window (pane) reference"}
	}
	return id, nil
}

// refID accepts only canonical positive decimal IDs, the form Kitty itself
// reports, so the reference round-trips exactly through Discover.
func refID(ref backend.Ref) (int, error) {
	if err := ref.Validate(); err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(ref.ID)
	if err != nil || id <= 0 || strconv.Itoa(id) != ref.ID {
		return 0, &backend.InvalidRefError{Ref: ref, Reason: "kitty: id must be a positive decimal integer"}
	}
	return id, nil
}

func kittyLocation(p backend.Placement) (string, error) {
	switch p {
	case backend.PlacementDefault, backend.PlacementFirst, backend.PlacementBefore, backend.PlacementAfter:
		return string(p), nil
	default:
		return "", fmt.Errorf("kitty: unknown placement %q", p)
	}
}

func kittyColor(c string) string {
	if c == backend.ColorReset {
		return TabColorDefault
	}
	return c
}

// userVarMap renders the user vars AgentMux defines as a key map, dropping
// empty values. Kitty's listing is decoded into the typed UserVars, so vars
// outside that set are not reported; MarshalJSON is the authority on which
// keys exist and how presence-only keys (amx_locked) spell.
func userVarMap(u UserVars) (map[string]string, error) {
	data, err := json.Marshal(u)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var vars map[string]string
	for k, v := range raw {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if vars == nil {
			vars = make(map[string]string)
		}
		vars[k] = s
	}
	return vars, nil
}
