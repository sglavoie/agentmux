package aslcontract

// LoopCompatibleContractSetID is the contract set this build implemented
// before it adopted asl-amx-1.85ec3041b9f03f47. That step added only bridge
// redirect and recovery methods; its lifecycle-hook and projection schemas AND
// fixtures are byte-identical to the vendored components. Its manifest is
// retained under compatibility and checked against those components by
// loop_compatibility_test.go, so state and hooks written by an ASL that still
// publishes it remain observable. This permits loop observation only; the
// bridge accepts ContractSetID alone.
const LoopCompatibleContractSetID = "asl-amx-1.6f30193cd1744a98"

// SupportsLoopContract reports whether the exact published identity has reviewed
// lifecycle-hook and projection compatibility. Unknown identities remain refused.
func SupportsLoopContract(id string) bool {
	return id == ContractSetID || id == LoopCompatibleContractSetID
}
