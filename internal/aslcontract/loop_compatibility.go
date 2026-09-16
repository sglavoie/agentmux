package aslcontract

// LoopCompatibleContractSetID adds bridge redirect/recovery methods, but its
// lifecycle-hook and projection schemas AND fixtures are byte-identical to our
// vendored components. The publisher's manifest is retained under compatibility
// and checked against those components by loop_compatibility_test.go.
// This permits loop observation only; bridge negotiation remains separate.
const LoopCompatibleContractSetID = "asl-amx-1.85ec3041b9f03f47"

// SupportsLoopContract reports whether the exact published identity has reviewed
// lifecycle-hook and projection compatibility. Unknown identities remain refused.
func SupportsLoopContract(id string) bool {
	return id == ContractSetID || id == LoopCompatibleContractSetID
}
