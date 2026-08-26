package agentloop

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Process birth authentication.
//
// A PID is not an identity: the kernel recycles it, so a run that crashed can
// leave behind a projection naming a PID some unrelated process now owns.
// agent-session-loop pairs every PID it publishes with a start token — the
// birth time `ps` reports, which is fixed for the life of a process — and
// AgentMux re-derives that token before it will call a run present.

// ProcessVerdict is the outcome of authenticating one published process
// reference.
type ProcessVerdict string

const (
	// ProcessAlive means the PID is running and its birth matches the token
	// ASL published, so this is the same process ASL was talking about.
	ProcessAlive ProcessVerdict = "alive"
	// ProcessExited means no process holds the PID. This is the one verdict
	// that asserts absence.
	ProcessExited ProcessVerdict = "exited"
	// ProcessReplaced means a process holds the PID but was born at a
	// different time, so the PID has been recycled.
	ProcessReplaced ProcessVerdict = "replaced"
	// ProcessUnverified means birth could not be established at all.
	ProcessUnverified ProcessVerdict = "unverified"
)

// ProcessBirthLookup resolves birth tokens for a batch of PIDs. It returns the
// tokens it could read and whether the probe itself worked; a probe that could
// not run at all is distinct from one that ran and found the PIDs gone.
type ProcessBirthLookup func(pids []int) (tokens map[int]string, probed bool)

// birthProbeTimeout bounds the `ps` call. Reconciliation runs on the daemon's
// maintenance tick, so a probe that hangs would stall every plan's evidence,
// not just the one it was reading.
const birthProbeTimeout = 5 * time.Second

// ctimeLayout is `ps -o lstart=` output with runs of whitespace collapsed:
// ASL pads the day of month, so normalization has to happen before parsing.
const ctimeLayout = "Mon Jan 2 15:04:05 2006"

// maxZoneSkew is the widest real UTC offset, and zoneOffsetStep the coarsest
// granularity every real one shares. Together they bound how far apart two
// renderings of the same birth can legitimately be — see compareStartTokens.
// The step matters: without it any process born a whole minute after the one
// ASL published would authenticate as that process.
const (
	maxZoneSkew    = 14 * time.Hour
	zoneOffsetStep = 15 * time.Minute
)

// processBirthTokens is the production probe, replaceable in tests.
var processBirthTokens ProcessBirthLookup = readProcessBirthTokens

// readProcessBirthTokens asks `ps` for the birth time of each PID in one call.
//
// The environment is deliberately inherited rather than pinned to C/UTC.
// agent-session-loop runs its own probe with an inherited environment, and
// tokens are compared as text first, so matching its invocation is what makes
// the common case a byte-for-byte match. Pinning the locale here would make
// every healthy run differ from its published token on any host whose system
// zone is not UTC.
func readProcessBirthTokens(pids []int) (map[int]string, bool) {
	if len(pids) == 0 {
		return nil, true
	}
	list := make([]string, 0, len(pids))
	for _, pid := range pids {
		if pid > 0 {
			list = append(list, strconv.Itoa(pid))
		}
	}
	if len(list) == 0 {
		return nil, true
	}

	ctx, cancel := context.WithTimeout(context.Background(), birthProbeTimeout)
	defer cancel()
	// #nosec G204 -- every argument is a decimal integer this function formatted.
	output, err := exec.CommandContext(ctx, "ps", "-o", "pid=", "-o", "lstart=", "-p", strings.Join(list, ",")).Output()
	if err != nil && len(output) == 0 {
		// `ps` exits nonzero when none of the PIDs matched, which is a real
		// answer, but it is indistinguishable here from a spawn failure. Only
		// an empty result is treated as "the probe did not run".
		return nil, false
	}

	tokens := make(map[int]string, len(list))
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, convErr := strconv.Atoi(fields[0])
		if convErr != nil || pid <= 0 {
			continue
		}
		tokens[pid] = strings.Join(fields[1:], " ")
	}
	return tokens, true
}

// authenticateProcess decides whether ref still names the process ASL saw.
//
// Only ProcessExited asserts absence. Every other failure to confirm — a
// recycled PID, an unreadable probe, an unparsable token — resolves to a
// verdict the caller must treat as indeterminate, because the cost of wrongly
// declaring a healthy run gone (a retired lease and the duplicate mutations
// that follow) is far higher than the cost of holding a lease one cycle
// longer.
func authenticateProcess(ref ProcessRef, pidRunning PIDChecker, tokens map[int]string, probed bool) ProcessVerdict {
	if ref.PID <= 0 || ref.StartToken == "" {
		return ProcessUnverified
	}
	if !pidRunning(ref.PID) {
		return ProcessExited
	}

	observed, found := tokens[ref.PID]
	if !found {
		if !probed {
			return ProcessUnverified
		}
		// The probe ran and did not see the PID, yet the liveness check did.
		// The process exited between the two reads, so re-ask the cheap check
		// rather than trust either observation alone.
		if !pidRunning(ref.PID) {
			return ProcessExited
		}
		return ProcessUnverified
	}
	return compareStartTokens(ref.StartToken, observed)
}

// compareStartTokens matches a published token against a freshly observed one.
//
// Text equality is the normal path. When it fails, the difference is far more
// likely to be a rendering difference than a recycled PID: `ps` formats the
// birth time in whatever zone its caller inherited, and the daemon and the
// loop need not have inherited the same one. A difference that looks exactly
// like a real UTC offset — a multiple of fifteen minutes, no wider than
// fourteen hours — is therefore read as the same instant rendered differently.
// Anything else — a different seconds-of-minute, a sub-minute skew from a
// stepped clock, an offset no zone could produce — refuses to confirm.
func compareStartTokens(published, observed string) ProcessVerdict {
	published = strings.Join(strings.Fields(published), " ")
	observed = strings.Join(strings.Fields(observed), " ")
	if published == "" || observed == "" {
		return ProcessUnverified
	}
	if strings.EqualFold(published, observed) {
		return ProcessAlive
	}

	publishedAt, err := time.Parse(ctimeLayout, published)
	if err != nil {
		return ProcessUnverified
	}
	observedAt, err := time.Parse(ctimeLayout, observed)
	if err != nil {
		return ProcessUnverified
	}

	delta := publishedAt.Sub(observedAt)
	if delta < 0 {
		delta = -delta
	}
	if delta%zoneOffsetStep == 0 && delta <= maxZoneSkew {
		return ProcessAlive
	}
	return ProcessReplaced
}
