package overseer

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
)

// overseer policy and history keep the retained schema-v1 envelope and version
// their own capability contracts inside it, independently of each other and
// of the envelope. Both versions are audited here so changing either is an
// explicit decision, not a side effect.
func TestOverseerJSONCommandSurfaceIsVersioned(t *testing.T) {
	root := Command()
	for _, name := range []string{"policy", "history"} {
		command, _, err := root.Find([]string{name})
		if err != nil || command == root {
			t.Fatalf("overseer %s is not registered: %v", name, err)
		}
		for _, flag := range []string{"json", "contract-version"} {
			if command.Flags().Lookup(flag) == nil {
				t.Errorf("%s does not declare --%s", command.CommandPath(), flag)
			}
		}
	}
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
	}
	for _, contract := range []struct {
		name          string
		version, want int
	}{
		{contractName, contractVersion, 1},
		{historyContractName, historyContractVersion, 1},
	} {
		if contract.version != contract.want {
			t.Errorf("unexpected %s contract version %d; want %d", contract.name, contract.version, contract.want)
		}
	}
	if contractName != "overseer_policy" || historyContractName != "overseer_history" {
		t.Fatalf("contract names = %q, %q", contractName, historyContractName)
	}
}
