package harness

var registry = map[string]Impl{
	Claude:  claudeHarness{},
	Copilot: copilotHarness{},
	Codex:   codexHarness{},
}

// Lookup returns the Impl for name, defaulting to Claude for unknown/empty values.
func Lookup(name string) Impl {
	if impl, ok := registry[Normalize(name)]; ok {
		return impl
	}
	return registry[Claude]
}

// Register adds or replaces a harness implementation. Intended for tests.
func Register(impl Impl) {
	registry[Normalize(impl.Name())] = impl
}
