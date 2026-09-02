package asl

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// A refusal the TUI does not recognize falls through to the generic "outcome is
// unknown" sentence, which is correct for a genuinely unknown outcome and wrong
// for a code the bridge already explains. Auditing against the shared vocabulary
// makes a bridge that grows a code fail here rather than silently degrade.
func TestASLRefusalTextCoversEveryDeclaredErrorCode(t *testing.T) {
	generic := RefusalText(errors.New("some-code-that-does-not-exist: detail"))

	for _, code := range aslbridge.ErrorCodes {
		if staleCodes[code] == errorCodes[code] {
			t.Errorf("%s: must be classified as exactly one of stale or error", code)
			continue
		}
		err := errors.New(code + ": drop the v1 column before the backfill lands")
		if got := ErrorCode(err); got != code {
			t.Errorf("%s: recovered code = %q", code, got)
		}
		text := RefusalText(err)
		if strings.Contains(text, "backfill") {
			t.Errorf("%s: refusal text repeats the bridge message: %q", code, text)
		}
		if staleCodes[code] {
			if !StaleTarget(err) {
				t.Errorf("%s: stale code is not treated as a moved target", code)
			}
			continue
		}
		if text == generic {
			t.Errorf("%s: falls through to the generic unknown-outcome sentence", code)
		}
	}

	declared := make(map[string]bool, len(aslbridge.ErrorCodes))
	for _, code := range aslbridge.ErrorCodes {
		declared[code] = true
	}
	for _, mapped := range []map[string]bool{staleCodes, errorCodes} {
		for code := range mapped {
			if !declared[code] {
				t.Errorf("%s: mapped here but absent from aslbridge.ErrorCodes", code)
			}
		}
	}
}
