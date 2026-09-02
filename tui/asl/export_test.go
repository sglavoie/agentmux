package asl

// This file is a test-only bridge, not production surface. `errorCode` is
// package-private because nothing outside tui/asl needs to recover a bridge
// refusal code: the root reads refusals through RefusalText and StaleTarget.
// The refusal test still asserts the code recovery directly, so the name it
// calls is re-provided here rather than exported from refusal.go.

// ErrorCode exposes errorCode to this package's tests.
var ErrorCode = errorCode
