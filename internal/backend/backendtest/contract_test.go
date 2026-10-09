package backendtest

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// TestContractAcceptsCoreOnlyBackend runs the contract against an in-memory
// backend that performs only the core operations, which is the only way the
// suite's unsupported-capability branches run before a second real backend
// exists.
func TestContractAcceptsCoreOnlyBackend(t *testing.T) {
	Run(t, func(t *testing.T) Harness {
		return Harness{
			Backend:   &coreOnly{},
			Directory: t.TempDir(),
			EchoArgv:  func(marker string) []string { return []string{"echo", marker} },
		}
	})
}

type memPane struct {
	group, pane string
	title, cwd  string
	vars        map[string]string
	text        string
}

type coreOnly struct {
	mu    sync.Mutex
	next  int
	panes []*memPane
}

func (b *coreOnly) Kind() runtimesession.BackendKind { return runtimesession.BackendTmux }

func (b *coreOnly) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		backend.OpDiscover: true, backend.OpLaunch: true, backend.OpClose: true,
		backend.OpCapture: true, backend.OpMetadata: true,
	}
}

func (b *coreOnly) Discover() ([]backend.Pane, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]backend.Pane, 0, len(b.panes))
	for _, p := range b.panes {
		out = append(out, backend.Pane{
			Ref: backend.PaneID(p.pane), Group: backend.GroupID(p.group),
			GroupTitle: p.title, CWD: p.cwd, Vars: maps.Clone(p.vars),
		})
	}
	return out, nil
}

func (b *coreOnly) Launch(spec backend.LaunchSpec) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	p := &memPane{
		group: "s" + strconv.Itoa(b.next), pane: "%" + strconv.Itoa(b.next),
		title: spec.Title, cwd: spec.Directory, vars: maps.Clone(spec.Vars),
	}
	if len(spec.Argv) == 2 {
		p.text = spec.Argv[1]
	}
	b.panes = append(b.panes, p)
	return nil
}

func (b *coreOnly) find(ref backend.Ref) (int, error) {
	if err := ref.Validate(); err != nil {
		return -1, err
	}
	i := slices.IndexFunc(b.panes, func(p *memPane) bool {
		return (ref.Scope == backend.ScopeGroup && p.group == ref.ID) || (ref.Scope == backend.ScopePane && p.pane == ref.ID)
	})
	if i < 0 {
		return -1, fmt.Errorf("no target %s", ref)
	}
	return i, nil
}

func (b *coreOnly) Close(ref backend.Ref) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	i, err := b.find(ref)
	if err != nil {
		return err
	}
	b.panes = slices.Delete(b.panes, i, i+1)
	return nil
}

func (b *coreOnly) Capture(ref backend.Ref, _ backend.Extent) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ref.Validate(); err == nil && ref.Scope != backend.ScopePane {
		return "", &backend.InvalidRefError{Ref: ref, Reason: "capture needs a pane"}
	}
	i, err := b.find(ref)
	if err != nil {
		return "", err
	}
	return b.panes[i].text, nil
}

func (b *coreOnly) SetMetadata(ref backend.Ref, vars map[string]string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ref.Validate(); err == nil && ref.Scope != backend.ScopePane {
		return &backend.InvalidRefError{Ref: ref, Reason: "metadata needs a pane"}
	}
	i, err := b.find(ref)
	if err != nil {
		return err
	}
	if b.panes[i].vars == nil {
		b.panes[i].vars = map[string]string{}
	}
	maps.Copy(b.panes[i].vars, vars)
	return nil
}

func (b *coreOnly) unsupported(op backend.Operation) error {
	return &backend.UnsupportedError{Kind: b.Kind(), Operation: op}
}

func (b *coreOnly) Focus(backend.Ref) error            { return b.unsupported(backend.OpFocus) }
func (b *coreOnly) SetTitle(backend.Ref, string) error { return b.unsupported(backend.OpTitle) }
func (b *coreOnly) SetColors(backend.Ref, backend.Colors) error {
	return b.unsupported(backend.OpColors)
}
func (b *coreOnly) Move(backend.Ref, int) error { return b.unsupported(backend.OpReorder) }
