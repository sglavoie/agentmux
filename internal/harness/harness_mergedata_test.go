package harness

import (
	"strings"
	"testing"
)

// TestMergeDataNullJSONNoPanic verifies that MergeData does not panic when the
// existing hooks file contains a top-level JSON `null`. Unmarshaling `null`
// into a map sets the map to nil rather than erroring, so the subsequent
// `existingRaw["hooks"] = ...` assignment used to panic with "assignment to
// entry in nil map" and crash `amx config hooks-sync` on misconfigured files.
func TestMergeDataNullJSONNoPanic(t *testing.T) {
	for _, name := range []string{Claude, Codex, Copilot} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("MergeData(%q, null) panicked: %v", name, r)
				}
			}()
			contract := Lookup(name).HookContract()
			out, err := contract.MergeData([]byte("null"), nil, "/usr/bin/amx")
			if err != nil {
				t.Fatalf("MergeData(%q, null) err: %v", name, err)
			}
			if len(out) == 0 {
				t.Fatalf("MergeData(%q, null) returned empty output", name)
			}
		})
	}
}

func TestCopilotMergeDataMalformedJSONReturnsError(t *testing.T) {
	contract := Lookup(Copilot).HookContract()
	_, err := contract.MergeData([]byte(`{"hooks":`), nil, "/usr/bin/amx")
	if err == nil {
		t.Fatalf("MergeData(%q) returned nil error for malformed JSON", Copilot)
	}
	if !strings.Contains(err.Error(), "parsing agent-mux.json") {
		t.Fatalf("MergeData(%q) error = %v, want parsing agent-mux.json error", Copilot, err)
	}
}

// TestMergeDataHooksNullNoPanic verifies that MergeData handles `{"hooks": null}`
// without panicking. This is a separate code path from a top-level null because
// the existing object is non-nil; only the inner hooks field unmarshals to nil.
func TestMergeDataHooksNullNoPanic(t *testing.T) {
	for _, name := range []string{Claude, Codex} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("MergeData(%q, hooks:null) panicked: %v", name, r)
				}
			}()
			contract := Lookup(name).HookContract()
			out, err := contract.MergeData([]byte(`{"hooks":null}`), nil, "/usr/bin/amx")
			if err != nil {
				t.Fatalf("MergeData(%q, hooks:null) err: %v", name, err)
			}
			if len(out) == 0 {
				t.Fatalf("MergeData(%q, hooks:null) returned empty output", name)
			}
		})
	}
}
