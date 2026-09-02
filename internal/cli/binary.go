package cli

import (
	"fmt"
	"os"
)

// binaryName is the bare command used when the process cannot locate its own
// executable. It resolves through PATH, which is enough for the two callers
// that tolerate the failure: writing a hook command line, and re-execing the
// daemon.
const binaryName = "amx"

// AmxBinary reports the path of the running amx binary. Commands need it
// because they write it into harness hook configuration and hand it to the
// daemon start and restart paths, so the daemon that comes back up is the same
// build as the CLI that asked for it.
//
// On failure it returns binaryName alongside the reason: callers that must fail
// loudly return the error, and callers that are deliberately fail-open (the
// hook indicator, `amx daemon restart`) report or ignore it and use the
// fallback, exactly as they did when each resolved os.Executable itself.
func AmxBinary() (string, error) {
	bin, err := os.Executable()
	if err != nil {
		return binaryName, fmt.Errorf("resolving amx binary: %w", err)
	}
	return bin, nil
}
