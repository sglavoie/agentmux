package cmd

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon"
)

// TestDaemonGuideSchemaV10FactKeys pins the schema-v10 export-events example
// in docs/daemon.md to the actual wire shape of
// daemon.SourceFactSnapshot. WindowDebugShadow.Facts serializes that type
// directly, so shadow.facts[] entries can only use its compact JSON tags
// (k/y/i/pm/...), never descriptive spellings like kind/key/icon. The prior
// round-trip test in this file exercised the Go type but never guarded the raw
// key spelling in the documentation snippet, letting the example drift to keys
// that amx daemon export-events cannot emit.
func TestDaemonGuideSchemaV10FactKeys(t *testing.T) {
	data, err := os.ReadFile("../docs/daemon.md")
	if err != nil {
		t.Fatalf("ReadFile(../docs/daemon.md): %v", err)
	}

	guide := string(data)
	facts := extractSchemaV10ExampleFacts(t, guide)
	if len(facts) == 0 {
		t.Fatalf("daemon guide schema-v10 example has no shadow.facts entries")
	}

	valid := sourceFactSnapshotWireTags()
	for i, fact := range facts {
		for key := range fact {
			if !valid[key] {
				t.Fatalf("daemon guide schema-v10 facts[%d] uses key %q, which is not a SourceFactSnapshot wire tag (valid: %v)", i, key, sortedKeys(valid))
			}
		}
	}

	// The example must show the compact ownership keys and a pipe-mode fact so
	// operators see the real wire spelling and the presence-encoded pm flag.
	var sawPipeMode, sawOrigin, sawAckedAt, sawProcess bool
	for _, fact := range facts {
		for _, need := range []string{"k", "y", "i"} {
			if _, ok := fact[need]; !ok {
				t.Fatalf("daemon guide schema-v10 fact object missing compact key %q: %v", need, fact)
			}
		}
		var snap daemon.SourceFactSnapshot
		blob, err := json.Marshal(fact)
		if err != nil {
			t.Fatalf("re-marshal example fact: %v", err)
		}
		if err := json.Unmarshal(blob, &snap); err != nil {
			t.Fatalf("decode example fact into SourceFactSnapshot: %v", err)
		}
		if raw, ok := fact["pm"]; ok {
			var pm bool
			if err := json.Unmarshal(raw, &pm); err != nil {
				t.Fatalf("decode pm value %s: %v", raw, err)
			}
			if pm != snap.PipeMode {
				t.Fatalf("example pm=%v does not round-trip through SourceFactSnapshot (%v)", pm, snap.PipeMode)
			}
			if pm {
				sawPipeMode = true
			}
		}
		if snap.OriginWindowID > 0 {
			sawOrigin = true
		}
		if snap.Acked && snap.AckedAt > 0 {
			sawAckedAt = true
		}
		processFields := 0
		for _, key := range []string{"ph", "pp", "pt"} {
			if _, ok := fact[key]; ok {
				processFields++
			}
		}
		if processFields != 0 && processFields != 3 {
			t.Fatalf("daemon guide schema-v10 fact has partial process identity: %v", fact)
		}
		if processFields == 3 && snap.HarnessProcessHarness != "" && snap.HarnessProcessPID > 1 && snap.HarnessProcessStartToken != "" {
			sawProcess = true
		}
	}
	if !sawPipeMode {
		t.Fatalf("daemon guide schema-v10 example must include a pipe-mode fact with pm:true")
	}
	if !sawOrigin || !sawAckedAt {
		t.Fatalf("daemon guide schema-v10 example must include ow origin and an acknowledged aa timestamp")
	}
	if !sawProcess {
		t.Fatalf("daemon guide schema-v10 example must include a complete ph/pp/pt process identity")
	}
	for _, want := range []string{`--schema 8`, `--schema 9`, `"reducer_decision": "applied"`, `"generations"`} {
		if !strings.Contains(guide, want) {
			t.Errorf("daemon guide schema-v10 guidance missing %q", want)
		}
	}
}

func TestCrossHarnessDiagnosticDocumentation(t *testing.T) {
	var guides strings.Builder
	for _, path := range []string{"../docs/hooks.md", "../docs/daemon.md", "../docs/operations.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		guides.Write(data)
	}
	guide := guides.String()
	for _, want := range []string{
		"--harness=<claude|codex|copilot>",
		"`amx_state` schema v4",
		"`owner_mismatch_prompt`",
		"`owner_mismatch_burst`",
		"`unknown_producer_fact`",
		"`acknowledged_fact_pressure`",
		`"route_decision": "owner"`,
		`"origin_window_id": 91`,
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("daemon guide missing cross-harness diagnostic contract %q", want)
		}
	}

	acceptanceData, err := os.ReadFile("../docs/daemon_state_acceptance.md")
	if err != nil {
		t.Fatalf("ReadFile(../docs/daemon_state_acceptance.md): %v", err)
	}
	acceptance := string(acceptanceData)
	for _, want := range []string{
		"## Cross-harness route diagnostics (session 6)",
		"19-event synthetic trace",
		"`owner_mismatch_prompt`",
		"`owner_mismatch_burst`",
		"`unknown_producer_fact`",
		"`acknowledged_fact_pressure`",
	} {
		if !strings.Contains(acceptance, want) {
			t.Errorf("acceptance documentation missing %q", want)
		}
	}
}

// extractSchemaV10ExampleFacts returns the shadow.facts array from the schema-v10
// export example decoded as a list of raw JSON objects.
func extractSchemaV10ExampleFacts(t *testing.T, guide string) []map[string]json.RawMessage {
	t.Helper()

	marker := "\"schema_version\": 11"
	start := strings.Index(guide, marker)
	if start < 0 {
		t.Fatalf("daemon guide missing schema_version 11 example")
	}
	rest := guide[start:]
	idx := strings.Index(rest, "\"facts\"")
	if idx < 0 {
		t.Fatalf("schema-v10 example missing a facts array")
	}
	rest = rest[idx:]
	open := strings.IndexByte(rest, '[')
	if open < 0 {
		t.Fatalf("facts array has no opening bracket")
	}
	depth := 0
	end := -1
	for i := open; i < len(rest); i++ {
		switch rest[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		t.Fatalf("facts array has no closing bracket")
	}

	var facts []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rest[open:end+1]), &facts); err != nil {
		t.Fatalf("decode facts array %q: %v", rest[open:end+1], err)
	}
	return facts
}

// sourceFactSnapshotWireTags returns the set of JSON tag names emitted by
// daemon.SourceFactSnapshot.
func sourceFactSnapshotWireTags() map[string]bool {
	tags := map[string]bool{}
	rt := reflect.TypeOf(daemon.SourceFactSnapshot{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name != "" {
			tags[name] = true
		}
	}
	return tags
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
