// This file is the dashboard binding vocabulary: what it means for a key
// pressed on the dashboard to run a catalog command, and what a caller may ask
// about that fact without knowing the model.
//
// A Binding is deliberately not a second command registry. It names an existing
// ID and adds only what a key needs that a command does not: which key strings
// dispatch it, how the binding is spelled in the palette and in help, the short
// label the footer draws it under, and whether the key consults the command's
// gates before running it. Everything a binding can be asked — is it available,
// is it hidden, what does it do — is answered by the command it names.

package commands

// Gate says whether a dashboard key consults its command's palette gates before
// running it.
//
// The two values are the whole vocabulary of "direct key differs from palette",
// which is otherwise an unwritten property spread across a key handler. A
// binding that needs neither value is not expressible, which is the point: a
// third behavior has to be declared here before a key can have it.
type Gate int

const (
	// GateChecked refuses the keypress exactly as the palette refuses the
	// command: hidden or unavailable means nothing happens. Every binding is
	// this unless it has a stated reason not to be.
	GateChecked Gate = iota
	// GateUnchecked hands the keypress straight to the command body, which is
	// the authority on its own refusal and reports it to the operator. It is for
	// a command whose refusal carries feedback the palette replaces with a
	// dimmed row — dropping the keypress would drop the explanation with it.
	GateUnchecked
)

// Binding declares one dashboard key and the command it runs.
type Binding struct {
	// Keys are every key string that dispatches Command from the dashboard.
	// The first is canonical: it is what the footer draws and what a caller
	// asking "which key is this command on" gets back.
	Keys []string
	// Display is how the palette and generated help spell the binding. It
	// differs from the canonical key only when several keys run one command
	// and naming just one of them would hide the others.
	Display string
	// Hint is the short label the footer and generated help draw the binding
	// under. An empty Hint means the footer never draws it; the command is
	// still reachable by key and from the palette.
	Hint string
	// Command is the stable catalog identity this key runs. It is an ID rather
	// than a command value so the binding table can be declared without the
	// model the catalog is generic over.
	Command ID
	// Gate is how the key treats a refusal. See Gate.
	Gate Gate
}

// Key is the canonical key string for the binding.
func (b Binding) Key() string {
	if len(b.Keys) == 0 {
		return ""
	}
	return b.Keys[0]
}

// Binds reports whether key dispatches this binding.
func (b Binding) Binds(key string) bool {
	for _, candidate := range b.Keys {
		if candidate == key {
			return true
		}
	}
	return false
}

// BindingSet is an ordered dashboard binding table. Order is dispatch
// precedence: when two bindings claim one key, the earlier one is offered the
// keypress first and the later one only sees it if the earlier command is
// hidden or unavailable.
type BindingSet []Binding

// For returns every binding key dispatches, in precedence order.
func (s BindingSet) For(key string) []Binding {
	var out []Binding
	for _, b := range s {
		if b.Binds(key) {
			out = append(out, b)
		}
	}
	return out
}

// Of returns the binding that runs id. A command with no dashboard key returns
// ok=false; a command claimed by several bindings returns the first, which is
// the one whose key the command is reached by in practice.
func (s BindingSet) Of(id ID) (Binding, bool) {
	for _, b := range s {
		if b.Command == id {
			return b, true
		}
	}
	return Binding{}, false
}

// Display is how id's binding is spelled for the palette and generated help,
// or "" when no dashboard key runs it.
func (s BindingSet) Display(id ID) string {
	if b, ok := s.Of(id); ok {
		return b.Display
	}
	return ""
}

// Key is the canonical dashboard key for id, or "" when no key runs it.
func (s BindingSet) Key(id ID) string {
	if b, ok := s.Of(id); ok {
		return b.Key()
	}
	return ""
}

// Hint is the footer/help label declared for id, or "" when the footer does
// not draw it.
func (s BindingSet) Hint(id ID) string {
	if b, ok := s.Of(id); ok {
		return b.Hint
	}
	return ""
}
