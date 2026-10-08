package probes

// Harness process liveness inspection. The hook client is the only component
// that ever needs command text: it uses it transiently to discover the owning
// harness and pipe mode. Once the daemon has a validated PID/start-token
// fingerprint, lifecycle checks deliberately ask ps for only PID and lstart.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

const (
	// Keep one lifecycle pass within the one-second polling cadence in the
	// ordinary case while leaving enough time for a temporarily slow ps.
	harnessProcessInspectionTimeout = 750 * time.Millisecond
	// Bound argv growth even when a restored daemon has many tracked sessions.
	// Sixty-four decimal PIDs remain comfortably below every supported
	// platform's command-line limit.
	harnessProcessInspectionBatchSize = 64
)

// HarnessProcessInspection is the result of comparing a tracked process
// fingerprint with one successful PID/lstart snapshot. Unknown is fail-open:
// the caller must retain every fact and retry on a later lifecycle pass.
type HarnessProcessInspection string

const (
	HarnessProcessUnknown           HarnessProcessInspection = "unknown"
	HarnessProcessAliveSameInstance HarnessProcessInspection = "alive_same_instance"
	HarnessProcessMissing           HarnessProcessInspection = "missing"
	HarnessProcessReplacedInstance  HarnessProcessInspection = "replaced_instance"
)

// HarnessProcessInspector is injectable so lifecycle tests can hold an
// inspection in flight without invoking the platform process table. An
// implementation must honor ctx cancellation. Missing map entries are treated
// as HarnessProcessUnknown.
type HarnessProcessInspector interface {
	InspectHarnessProcesses(context.Context, []protocol.HarnessProcessIdentity) map[protocol.HarnessProcessIdentity]HarnessProcessInspection
}

type psHarnessProcessInspector struct {
	timeout   time.Duration
	batchSize int
	runBatch  func(context.Context, []int) ([]byte, error)
}

// NewPSHarnessProcessInspector returns the production PID/lstart inspector.
func NewPSHarnessProcessInspector() HarnessProcessInspector {
	return &psHarnessProcessInspector{
		timeout:   harnessProcessInspectionTimeout,
		batchSize: harnessProcessInspectionBatchSize,
		runBatch:  runPSHarnessProcessBatch,
	}
}

// InspectHarnessProcesses deduplicates identities and PIDs, splits the query
// into bounded batches, and compares each successful PID snapshot with every
// identity that names it. One failed batch becomes unknown only for that
// batch; successful batches in the same pass remain usable.
func (p *psHarnessProcessInspector) InspectHarnessProcesses(
	ctx context.Context,
	identities []protocol.HarnessProcessIdentity,
) map[protocol.HarnessProcessIdentity]HarnessProcessInspection {
	results := make(map[protocol.HarnessProcessIdentity]HarnessProcessInspection, len(identities))
	byPID := make(map[int][]protocol.HarnessProcessIdentity)
	for _, raw := range identities {
		identity, ok := protocol.CanonicalHarnessProcessIdentity(raw)
		if !ok {
			results[raw] = HarnessProcessUnknown
			continue
		}
		if _, seen := results[identity]; seen {
			continue
		}
		results[identity] = HarnessProcessUnknown
		byPID[identity.PID] = append(byPID[identity.PID], identity)
	}

	pids := make([]int, 0, len(byPID))
	for pid := range byPID {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	batchSize := p.batchSize
	if batchSize < 1 {
		batchSize = harnessProcessInspectionBatchSize
	}
	for start := 0; start < len(pids); start += batchSize {
		end := start + batchSize
		if end > len(pids) {
			end = len(pids)
		}
		batch := pids[start:end]
		batchCtx := ctx
		cancel := func() {}
		if p.timeout > 0 {
			batchCtx, cancel = context.WithTimeout(ctx, p.timeout)
		}
		output, err := p.runBatch(batchCtx, batch)
		cancel()
		if err != nil {
			continue
		}
		observed, err := parsePSHarnessProcessBatch(output, batch)
		if err != nil {
			continue
		}
		for _, pid := range batch {
			startToken, alive := observed[pid]
			for _, identity := range byPID[pid] {
				switch {
				case !alive:
					results[identity] = HarnessProcessMissing
				case startToken == identity.StartToken:
					results[identity] = HarnessProcessAliveSameInstance
				default:
					results[identity] = HarnessProcessReplacedInstance
				}
			}
		}
	}
	return results
}

func runPSHarnessProcessBatch(ctx context.Context, pids []int) ([]byte, error) {
	values := make([]string, len(pids))
	for i, pid := range pids {
		values[i] = strconv.Itoa(pid)
	}
	cmd := exec.CommandContext(ctx, "ps", "-p", strings.Join(values, ","), "-o", "pid=", "-o", "lstart=")
	cmd.Env = stableDaemonProcessLocaleEnv()
	output, err := cmd.Output()
	if err == nil {
		return output, nil
	}
	// BSD and procps ps both use exit status 1 when no selected PID exists.
	// An empty snapshot is therefore confirmed absence, not an inspector
	// failure. Any other command failure remains unknown/fail-open.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 &&
		len(strings.TrimSpace(string(output))) == 0 && len(strings.TrimSpace(string(exitErr.Stderr))) == 0 {
		return output, nil
	}
	return output, err
}

// parsePSHarnessProcessBatch returns PID -> normalized five-field C-locale,
// UTC lstart token. A malformed or unexpected row invalidates the entire batch
// so partial parsing can never turn ambiguous process-table output into an exit.
func parsePSHarnessProcessBatch(output []byte, requested []int) (map[int]string, error) {
	wanted := make(map[int]bool, len(requested))
	for _, pid := range requested {
		wanted[pid] = true
	}
	observed := make(map[int]string, len(requested))
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		// POSIX lstart is exactly five whitespace-delimited fields under the C
		// locale and pinned UTC timezone. PID is the only preceding field;
		// command text is not queried.
		if len(fields) != 6 {
			return nil, fmt.Errorf("malformed ps lifecycle row")
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || !wanted[pid] {
			return nil, fmt.Errorf("unexpected ps lifecycle pid")
		}
		token := strings.Join(fields[1:6], " ")
		if token == "" {
			return nil, fmt.Errorf("empty ps lifecycle start token")
		}
		if previous, duplicate := observed[pid]; duplicate && previous != token {
			return nil, fmt.Errorf("conflicting ps lifecycle rows")
		}
		observed[pid] = token
	}
	return observed, nil
}

func stableDaemonProcessLocaleEnv() []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "LC_ALL=") || strings.HasPrefix(entry, "LANG=") ||
			strings.HasPrefix(entry, "TZ=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "LC_ALL=C", "LANG=C", "TZ=UTC")
}
