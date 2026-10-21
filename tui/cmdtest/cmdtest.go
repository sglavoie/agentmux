// Package cmdtest reads the shape of Bubble Tea commands for tests and for the
// tag-gated scenario harness in tui: whether a message is a batch or sequence,
// and whether a command is one of Bubble Tea's timers.
//
// Nothing in a release build imports it. It is a package of its own so the
// dashboard's untagged unit tests and the `tuitest`-tagged harness agree on what
// counts as a batch or a timer without either keeping a private copy.
package cmdtest

import (
	"reflect"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Flatten reports whether msg is a slice-shaped batch/sequence message and, if
// so, returns the component commands it carries. It reads the slice by
// reflection so callers never depend on Bubble Tea's unexported batch and
// sequence message types.
func Flatten(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice {
		return nil, false
	}
	out := make([]tea.Cmd, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		if c, ok := v.Index(i).Interface().(tea.Cmd); ok && c != nil {
			out = append(out, c)
		}
	}
	return out, true
}

// IsTimer reports whether cmd was built by tea.Tick or tea.Every, which a
// test or the harness skips rather than waits on.
func IsTimer(cmd tea.Cmd) bool {
	fn := runtime.FuncForPC(reflect.ValueOf(cmd).Pointer())
	if fn == nil {
		return false
	}
	name := fn.Name()
	return strings.Contains(name, "bubbletea/v2.Tick") || strings.Contains(name, "bubbletea/v2.Every")
}
