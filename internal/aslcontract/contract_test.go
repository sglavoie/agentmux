package aslcontract

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// This file is the offline half of the vendoring gate: it proves the copy is
// internally consistent and still derives the ID this build claims. The other
// half — that the copy matches the ASL that is actually installed — is
// `just asl-integration`, which compares a live hello against ContractSetID.
// Neither check alone is sufficient: a wholesale re-copy of a newer corpus
// passes here and fails there, and a locally edited fixture fails here.

func TestVendoredFilesMatchTheManifestDigests(t *testing.T) {
	manifest, err := ReadManifest()
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	digests, err := FileDigests()
	if err != nil {
		t.Fatalf("digest corpus: %v", err)
	}

	for name, want := range manifest.Files {
		got, ok := digests[name]
		if !ok {
			t.Errorf("manifest pins %s but the vendored corpus has no such file", name)
			continue
		}
		if got != want {
			t.Errorf("%s: digest = %s, manifest pins %s", name, got, want)
		}
	}
	for name := range digests {
		if _, ok := manifest.Files[name]; !ok {
			t.Errorf("vendored corpus holds %s, which the manifest does not pin", name)
		}
	}
}

func TestVendoredCorpusDerivesThePinnedContractSetID(t *testing.T) {
	derived, err := DerivedContractSetID()
	if err != nil {
		t.Fatalf("derive contract set id: %v", err)
	}
	// A vendored byte changed on this side moves this ID and nothing else, which
	// is exactly the failure a hand-edited fixture must produce: the corpus is
	// ASL's to change, and Agent Mux's copy of it is not a place to fix things.
	if derived != ContractSetID {
		t.Errorf("vendored corpus derives %s, this build pins %s", derived, ContractSetID)
	}

	manifest, err := ReadManifest()
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.CurrentContractSetID != ContractSetID {
		t.Errorf("manifest names %s as current, this build pins %s",
			manifest.CurrentContractSetID, ContractSetID)
	}
	current, err := manifest.Current()
	if err != nil {
		t.Fatalf("current contract set: %v", err)
	}
	digest, err := CorpusDigest()
	if err != nil {
		t.Fatalf("corpus digest: %v", err)
	}
	if current.CorpusDigest != digest {
		t.Errorf("manifest corpus digest = %s, vendored bytes digest to %s",
			current.CorpusDigest, digest)
	}
	if current.Status != "current" {
		t.Errorf("current set status = %q, want %q", current.Status, "current")
	}
}

func TestTheManifestIsExcludedFromTheIdentityItCarries(t *testing.T) {
	names, err := CorpusFiles()
	if err != nil {
		t.Fatalf("list corpus: %v", err)
	}
	for _, name := range names {
		if name == "manifest.json" {
			t.Fatal("manifest.json is inside the digest it carries")
		}
	}
	// The README is prose about the contract, not the contract, so correcting a
	// sentence must not move the ID. It is excluded by being the only non-JSON
	// file, which this asserts rather than assumes.
	if _, err := fs.Stat(FS(), "README.md"); err != nil {
		t.Fatalf("vendored corpus carries no README: %v", err)
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".json") {
			t.Errorf("%s is inside the digest but is not a canonical JSON document", name)
		}
	}
}

func TestTheCurrentSetPinsEveryComponentSchemaVersion(t *testing.T) {
	manifest, err := ReadManifest()
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	current, err := manifest.Current()
	if err != nil {
		t.Fatalf("current contract set: %v", err)
	}
	// The hello carries one contract-set ID rather than a version per component,
	// which is only sufficient because the set behind the ID pins each of them.
	want := map[string]int{
		ComponentBridgeProtocol: 2,
		ComponentLifecycleHook:  2,
		ComponentProjection:     1,
	}
	if len(current.Components) != len(want) {
		t.Fatalf("contract set declares %d components, want %d", len(current.Components), len(want))
	}
	for name, version := range want {
		component, ok := current.Components[name]
		if !ok {
			t.Errorf("contract set declares no %s component", name)
			continue
		}
		if component.SchemaVersion != version {
			t.Errorf("%s schema version = %d, want %d", name, component.SchemaVersion, version)
		}
		if component.SchemaPath == "" || component.Digest == "" {
			t.Errorf("%s names no schema path or digest", name)
		}
		if manifest.Files[component.SchemaPath] != component.Digest {
			t.Errorf("%s schema digest %s does not match the file digest %s",
				name, component.Digest, manifest.Files[component.SchemaPath])
		}
	}
}

func TestEveryFixtureDeclaresTheRootItExercises(t *testing.T) {
	components := map[string][]string{
		FixturesBridgeProtocol: nil,
		FixturesLifecycleHook:  nil,
		FixturesProjection:     nil,
	}
	for component := range components {
		for _, validity := range []string{"valid", "invalid"} {
			fixtures, err := ReadFixtures(component, validity)
			if err != nil {
				t.Fatalf("read %s/%s: %v", component, validity, err)
			}
			if len(fixtures) == 0 {
				t.Errorf("%s/%s holds no fixtures", component, validity)
			}
			for _, fixture := range fixtures {
				if fixture.Case == "" || fixture.Root == "" || len(fixture.Document) == 0 {
					t.Errorf("%s: case/root/document incomplete", fixture.Path)
				}
				if validity == "invalid" && (fixture.Reason == "" || fixture.Pointer == "") {
					t.Errorf("%s: an invalid fixture must name its reason and pointer", fixture.Path)
				}
			}
		}
	}
}

// Every fixture that spells a contract-set ID spells the placeholder, because a
// fixture cannot carry the digest of its own bytes. A real ID appearing in one
// would mean the corpus had been edited to agree with a build rather than the
// other way round.
func TestFixturesCarryThePlaceholderIdentityOnly(t *testing.T) {
	names, err := CorpusFiles()
	if err != nil {
		t.Fatalf("list corpus: %v", err)
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "fixtures/") {
			continue
		}
		raw, err := fs.ReadFile(FS(), name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var fixture Fixture
		if err := json.Unmarshal(raw, &fixture); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		for _, id := range contractSetIDsIn(t, fixture.Document) {
			if id != PlaceholderContractSetID {
				t.Errorf("%s carries contract set %q, want the placeholder %q",
					name, id, PlaceholderContractSetID)
			}
		}
	}
}

// contractSetIDsIn collects every contract_set_id member at any depth.
func contractSetIDsIn(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	var found []string
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == "contract_set_id" {
					found = append(found, fmt.Sprint(child))
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return found
}
