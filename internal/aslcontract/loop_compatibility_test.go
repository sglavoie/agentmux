package aslcontract

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

//go:embed compatibility/6f30193cd1744a98.json
var loopCompatibleManifest []byte

func TestReviewedLoopContractComponentsMatchVendoredCorpus(t *testing.T) {
	var peer Manifest
	if err := json.Unmarshal(loopCompatibleManifest, &peer); err != nil {
		t.Fatal(err)
	}
	if peer.CurrentContractSetID != LoopCompatibleContractSetID {
		t.Fatal("reviewed manifest does not name the accepted loop contract")
	}
	peerSet, err := peer.Current()
	if err != nil {
		t.Fatal(err)
	}
	local, err := ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	localSet, err := local.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range []string{ComponentProjection, ComponentLifecycleHook} {
		if !reflect.DeepEqual(peerSet.Components[component], localSet.Components[component]) {
			t.Errorf("%s component differs", component)
		}
	}
	for _, prefix := range []string{"schemas/projection-v1.json", "schemas/lifecycle-hook-v2.json", "fixtures/projection-v1/", "fixtures/lifecycle-hook-v2/"} {
		for name, digest := range peer.Files {
			if strings.HasPrefix(name, prefix) && local.Files[name] != digest {
				t.Errorf("reviewed loop component %s differs from the vendored file", name)
			}
		}
		for name := range local.Files {
			if strings.HasPrefix(name, prefix) && peer.Files[name] == "" {
				t.Errorf("reviewed manifest is missing %s", name)
			}
		}
	}
}

func TestLoopContractAllowlistRejectsUnknownPeers(t *testing.T) {
	for _, id := range []string{ContractSetID, LoopCompatibleContractSetID} {
		if !SupportsLoopContract(id) {
			t.Errorf("reviewed contract %s refused", id)
		}
	}
	for _, id := range []string{"", Prefix, "asl-amx-1.ffffffffffffffff", LoopCompatibleContractSetID + "x"} {
		if SupportsLoopContract(id) {
			t.Errorf("unknown contract %s accepted", id)
		}
	}
}
