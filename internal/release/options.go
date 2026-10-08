// Gate inputs: the seams that make a release run testable -- the command
// runner, the clock -- and the operator-facing options beside them.

package release

import (
	"context"
	"io"
	"time"
)

// CommandRunner executes argv with dir as the working directory and returns the
// combined output together with the process exit status. A non-nil error means
// the command could not be run at all (missing executable, unusable directory);
// a command that ran and failed returns a non-zero code and a nil error, so the
// gate can record a real failure distinctly from a broken invocation.
type CommandRunner func(ctx context.Context, dir string, argv []string) (output string, exitCode int, err error)

// Options configures a gate run. Only the two repository roots are required;
// everything else has a defensible default, except signing, which is off unless
// a key is named.
type Options struct {
	// AgentMuxRoot and ASLRoot are the two source checkouts being released.
	AgentMuxRoot string
	ASLRoot      string

	// SigningKey is the SSH private key used for `ssh-keygen -Y sign`. When it
	// is empty the gate still runs and still writes evidence, but the record is
	// marked unsigned. An unsigned record is a rehearsal, not a release, and a
	// rehearsal may not tag: Tag without a key is refused outright.
	SigningKey string

	// EvidenceDir is where the record and its signature are written. Empty
	// means the default under the Agent Mux data directory.
	EvidenceDir string

	// Namespace overrides the signature namespace. Empty means SigningNamespace.
	// It exists so the isolated test can sign under a throwaway namespace, not
	// so releases can choose their own.
	Namespace string

	// Tag writes the annotated pair tag into both repositories after a passing
	// run. It is off by default: tagging is the one gate side effect that
	// changes history, and it should be asked for. It requires SigningKey --
	// see ErrUnsignedTag.
	Tag bool

	// Overwrite permits replacing an existing evidence record for the same pair
	// ID. Without it, a re-run of an already-recorded pair refuses rather than
	// destroying signed evidence.
	Overwrite bool

	// Run executes external commands. Empty means ExecRunner.
	Run CommandRunner

	// Now supplies the clock. Empty means time.Now. Durations are measured
	// through it, so an injected clock makes a recorded run reproducible.
	Now func() time.Time

	// Progress receives a human-readable line per step as the gate advances. A
	// gate run takes tens of minutes; silence for that long is indistinguishable
	// from a hang.
	Progress io.Writer
}
