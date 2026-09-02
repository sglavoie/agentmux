package protocol

import (
	"runtime"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

const (
	// Darwin ps rejects larger selectors with "process id too large". Linux
	// permits a wider PID namespace on 64-bit systems.
	maxDarwinHarnessProcessPID = 99_999
	maxLinuxHarnessProcessPID  = 4 * 1024 * 1024
)

// ValidatedHarnessProcessIdentity is the single trust boundary for
// hook-supplied process ownership. The start token remains opaque; validation
// only canonicalizes whitespace, bounds it, and requires an exact supported
// producer match plus a usable PID.
func ValidatedHarnessProcessIdentity(producerHarness string, identity *HarnessProcessIdentity) *HarnessProcessIdentity {
	if identity == nil {
		return nil
	}
	producer := harness.NormalizeKnown(producerHarness)
	identityHarness := harness.NormalizeKnown(identity.Harness)
	startToken := strings.Join(strings.Fields(identity.StartToken), " ")
	if producer == "" || identityHarness == "" || identityHarness != producer ||
		identity.PID <= 1 || identity.PID > maxHarnessProcessPID() ||
		startToken == "" || len(startToken) > 128 {
		return nil
	}
	return &HarnessProcessIdentity{Harness: identityHarness, PID: identity.PID, StartToken: startToken}
}

func maxHarnessProcessPID() int {
	if runtime.GOOS == "linux" {
		return maxLinuxHarnessProcessPID
	}
	return maxDarwinHarnessProcessPID
}

func CanonicalHarnessProcessIdentity(identity HarnessProcessIdentity) (HarnessProcessIdentity, bool) {
	validated := ValidatedHarnessProcessIdentity(identity.Harness, &identity)
	if validated == nil {
		return HarnessProcessIdentity{}, false
	}
	return *validated, true
}

// FactProcessIdentity returns the canonical process identity a fact produced by
// source may retain. Transport normalization already validates production
// events; this second boundary keeps direct/replay callers from attaching
// malformed, producer-mismatched, or forbidden ownership.
func FactProcessIdentity(source SourceID, process HarnessProcessIdentity) HarnessProcessIdentity {
	switch source.Kind {
	case SourceHarness, SourceLegacy, SourceSubagent:
	default:
		return HarnessProcessIdentity{}
	}

	identity, ok := CanonicalHarnessProcessIdentity(process)
	if !ok {
		return HarnessProcessIdentity{}
	}
	if source.Kind == SourceHarness {
		prefix := identity.Harness + ":"
		if len(source.Key) <= len(prefix) || source.Key[:len(prefix)] != prefix {
			return HarnessProcessIdentity{}
		}
	}
	return identity
}
