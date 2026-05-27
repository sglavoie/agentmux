package harness

import (
	"testing"
)

func TestRegister(t *testing.T) {
	const fakeName = "fake-harness"

	t.Cleanup(func() { delete(registry, fakeName) })

	if got := Lookup(fakeName).Name(); got != "claude" {
		t.Fatalf("pre-register: expected claude default, got %q", got)
	}

	Register(fakeHarness{name: fakeName})

	got := Lookup(fakeName)
	if got.Name() != fakeName {
		t.Errorf("post-register: Lookup(%q).Name() = %q, want %q", fakeName, got.Name(), fakeName)
	}
	if got.Executable() != "fake" {
		t.Errorf("post-register: Executable() = %q, want %q", got.Executable(), "fake")
	}
}

func TestRegisterNormalizesName(t *testing.T) {
	const rawName = "MixedCase-Harness"
	const normalized = "mixedcase-harness"

	t.Cleanup(func() {
		delete(registry, rawName)
		delete(registry, normalized)
	})

	Register(fakeHarness{name: rawName})

	if got := Lookup(rawName); got.Name() != rawName {
		t.Errorf("Lookup(%q).Name() = %q, want %q", rawName, got.Name(), rawName)
	}
	if got := Lookup(normalized); got.Name() != rawName {
		t.Errorf("Lookup(%q).Name() = %q, want %q", normalized, got.Name(), rawName)
	}
	if got := Lookup("MIXEDCASE-HARNESS"); got.Name() != rawName {
		t.Errorf("Lookup(upper).Name() = %q, want %q", got.Name(), rawName)
	}
	if _, ok := registry[rawName]; ok {
		t.Errorf("registry should not contain non-normalized key %q", rawName)
	}
	if _, ok := registry[normalized]; !ok {
		t.Errorf("registry missing normalized key %q", normalized)
	}
}

func TestValidRecognizesRegisteredHarness(t *testing.T) {
	const name = "synthetic-harness"

	t.Cleanup(func() { delete(registry, name) })

	if Valid(name) {
		t.Fatalf("pre-register: Valid(%q) = true, want false", name)
	}

	Register(fakeHarness{name: name})

	if !Valid(name) {
		t.Errorf("post-register: Valid(%q) = false, want true", name)
	}
	if !Valid("SYNTHETIC-HARNESS") {
		t.Errorf("post-register: Valid uppercase should be true")
	}
}

func TestValidBuiltins(t *testing.T) {
	for _, name := range []string{Claude, Copilot, Codex} {
		if !Valid(name) {
			t.Errorf("Valid(%q) = false, want true", name)
		}
	}
	if Valid("nope") {
		t.Errorf("Valid(\"nope\") = true, want false")
	}
}
