package aslbridge

// This file decides what a failure is allowed to say publicly. The daemon log
// may render everything, including whatever the child printed on stderr; the
// capability block may not, because bridge-authored text can quote a plan.

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxReasonChars = 200

// privateError carries child-authored diagnostics alongside a daemon-authored
// cause. Only the daemon log ever renders Error(); sanitizeReason unwraps to
// the public half, so a bridge is free to print whatever it likes on stderr
// without that text reaching the published capability block.
type privateError struct {
	public error
	detail string
}

func (e *privateError) Error() string {
	return e.public.Error() + " (stderr: " + e.detail + ")"
}

func (e *privateError) Unwrap() error { return e.public }

func handshakeFailure(err error, tail *stderrTail) error {
	public := fmt.Errorf("asl bridge: handshake failed: %w", err)
	if line := tail.lastLine(); line != "" {
		return &privateError{public: public, detail: line}
	}
	return public
}

// sanitizeReason renders a failure for the published capability block. A
// bridge-reported refusal collapses to its stable code because its message is
// ASL-authored text that could quote a plan; everything else is daemon-authored
// and is only bounded and flattened.
func sanitizeReason(err error) string {
	var private *privateError
	if errors.As(err, &private) {
		err = private.public
	}
	switch {
	case err == nil:
		return ""
	case errors.Is(err, io.EOF), errors.Is(err, errConnClosed):
		return "bridge exited"
	}
	var protocolErr *Error
	if errors.As(err, &protocolErr) {
		return "bridge refused: " + protocolErr.Code
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	if len(msg) > maxReasonChars {
		msg = msg[:maxReasonChars-1] + "…"
	}
	return msg
}
