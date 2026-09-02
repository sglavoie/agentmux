package indicator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
)

// psCommandTimeout bounds each `ps` invocation used during process-tree
// discovery. The indicator runs synchronously inside every hook invocation, so
// a wedged or paused `ps` must fail open rather than hang the hook script.
// Exposed as a var so tests can shrink it without sleeping.
var psCommandTimeout = 2 * time.Second

var runPSProcessSnapshot = func(ctx context.Context, pid int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "ppid=", "-o", "lstart=", "-o", "command=")
	cmd.Env = stableProcessLocaleEnv()
	return cmd.Output()
}

// HarnessProcessDiscovery is the non-sensitive result of walking the hook's
// process ancestry. Command lines are inspected transiently but deliberately do
// not survive this boundary.
type HarnessProcessDiscovery struct {
	Harness    string
	PID        int
	StartToken string
	PipeMode   bool
	Trackable  bool
}

// Identity returns a fresh transport value only when the discovery contains
// the complete process fingerprint required for lifecycle tracking.
func (d HarnessProcessDiscovery) Identity() *protocol.HarnessProcessIdentity {
	if !d.Trackable {
		return nil
	}
	return &protocol.HarnessProcessIdentity{
		Harness:    d.Harness,
		PID:        d.PID,
		StartToken: d.StartToken,
	}
}

type processSnapshot struct {
	ParentPID   int
	StartToken  string
	CommandLine string
}

type processSnapshotFunc func(int) (processSnapshot, bool)

// DiscoverHarnessProcess finds the nearest ancestor whose executable matches
// the declared producer harness. An unknown or missing producer retains the
// legacy any-ancestor pipe-mode signal but intentionally omits identity: an
// unrelated outer harness must never acquire ownership of the event.
func DiscoverHarnessProcess(producerHarness string) HarnessProcessDiscovery {
	producer := harness.NormalizeKnown(producerHarness)
	if producer == "" {
		compatibility := discoverHarnessProcess(os.Getpid(), "", psProcessSnapshot)
		return HarnessProcessDiscovery{PipeMode: compatibility.PipeMode}
	}
	return discoverHarnessProcess(os.Getpid(), producer, psProcessSnapshot)
}

// IsPipeMode reports whether the current process tree contains a non-interactive
// harness invocation such as `claude -p`, `copilot --prompt`, or `codex exec`.
// It remains the compatibility wrapper for callers that do not declare a
// producer; production hook delivery uses DiscoverHarnessProcess instead.
func IsPipeMode() bool {
	return discoverHarnessProcess(os.Getpid(), "", psProcessSnapshot).PipeMode
}

// discoverHarnessProcess is the process-discovery test seam. The snapshot
// function supplies one bounded observation per ancestor in production and
// deterministic process trees in tests.
func discoverHarnessProcess(pid int, producerHarness string, snapshot processSnapshotFunc) HarnessProcessDiscovery {
	producer := harness.NormalizeKnown(producerHarness)
	declaredProducer := strings.TrimSpace(producerHarness) != ""
	if declaredProducer && producer == "" {
		return HarnessProcessDiscovery{}
	}

	visited := make(map[int]bool)
	var nearest HarnessProcessDiscovery
	for pid > 1 {
		if visited[pid] {
			break
		}
		visited[pid] = true

		observed, ok := snapshot(pid)
		if !ok {
			break
		}

		if declaredProducer {
			matchedHarness, pipeMode, matched := classifyHarnessInvocation(observed.CommandLine, producer, false)
			if matched {
				startToken := normalizeStartToken(observed.StartToken)
				return HarnessProcessDiscovery{
					Harness: matchedHarness, PID: pid, StartToken: startToken,
					PipeMode: pipeMode, Trackable: startToken != "",
				}
			}
		} else {
			// Compatibility mode keeps walking past an interactive inner harness
			// so an outer pipe invocation is still detected, matching IsPipeMode's
			// historical any-ancestor behavior.
			if matchedHarness, pipeMode, matched := classifyHarnessInvocation(observed.CommandLine, "", true); matched {
				startToken := normalizeStartToken(observed.StartToken)
				return HarnessProcessDiscovery{
					Harness: matchedHarness, PID: pid, StartToken: startToken,
					PipeMode: pipeMode, Trackable: startToken != "",
				}
			}
			if nearest.Harness == "" {
				if matchedHarness, pipeMode, matched := classifyHarnessInvocation(observed.CommandLine, "", false); matched {
					startToken := normalizeStartToken(observed.StartToken)
					nearest = HarnessProcessDiscovery{
						Harness: matchedHarness, PID: pid, StartToken: startToken,
						PipeMode: pipeMode, Trackable: startToken != "",
					}
				}
			}
		}

		parent := observed.ParentPID
		if parent <= 0 || parent == pid {
			break
		}
		pid = parent
	}
	return nearest
}

// walkTree is retained for source compatibility with older package tests. New
// production discovery uses one processSnapshot per ancestor instead of
// separate command-line and parent-PID probes.
func walkTree(pid int, cmdline func(int) string, ppid func(int) int) bool {
	snapshot := func(pid int) (processSnapshot, bool) {
		return processSnapshot{ParentPID: ppid(pid), CommandLine: cmdline(pid)}, true
	}
	return discoverHarnessProcess(pid, "", snapshot).PipeMode
}

func looksLikePipeMode(cmdline string) bool {
	_, pipeMode, matched := classifyHarnessInvocation(cmdline, "", true)
	return matched && pipeMode
}

// classifyHarnessInvocation finds an exact harness executable in cmdline. Only
// argv[0], or the script immediately launched by a supported runtime, may name
// the harness: arguments such as "--harness claude" and "/tmp/claude" must not
// make a hook client, shell wrapper, or unrelated intermediary own the event.
// When producer is non-empty only that harness may match. pipeOnly skips
// interactive invocations so compatibility discovery can continue looking for
// an outer non-interactive harness.
func classifyHarnessInvocation(cmdline, producer string, pipeOnly bool) (string, bool, bool) {
	fields := commandFields(cmdline)
	index := harnessExecutableIndex(fields)
	if index < 0 {
		return "", false, false
	}
	name := harness.NormalizeKnown(commandBase(fields[index]))
	if producer != "" && name != producer {
		return "", false, false
	}
	pipeMode := harness.InvocationIsPipeMode(name, fields[index+1:])
	if pipeOnly && !pipeMode {
		return "", false, false
	}
	return name, pipeMode, true
}

func harnessExecutableIndex(fields []string) int {
	if len(fields) == 0 {
		return -1
	}
	if harness.NormalizeKnown(commandBase(fields[0])) != "" {
		return 0
	}
	if len(fields) > 1 && isHarnessScriptRuntime(commandBase(fields[0])) &&
		harness.NormalizeKnown(commandBase(fields[1])) != "" {
		return 1
	}
	return -1
}

func isHarnessScriptRuntime(name string) bool {
	switch strings.ToLower(name) {
	case "node", "nodejs":
		return true
	default:
		return false
	}
}

// commandFields splits the transient ps command text without breaking quoted
// executable paths. Quotes only open quoting at a field boundary: ps renders
// argv as raw text, so quotes within a field are literal argument bytes. It
// intentionally does not interpret escapes or expand any shell syntax;
// classification needs only argv-like field boundaries.
func commandFields(cmdline string) []string {
	var fields []string
	var field strings.Builder
	var quote rune
	inField := false
	flush := func() {
		if !inField {
			return
		}
		fields = append(fields, field.String())
		field.Reset()
		inField = false
	}
	for _, r := range cmdline {
		if quote != 0 {
			inField = true
			if r == quote {
				quote = 0
			} else {
				field.WriteRune(r)
			}
			continue
		}
		switch {
		case (r == '\'' || r == '"') && !inField:
			quote = r
			inField = true
		case unicode.IsSpace(r):
			flush()
		default:
			inField = true
			field.WriteRune(r)
		}
	}
	flush()
	return fields
}

func commandBase(field string) string {
	field = strings.Trim(strings.TrimSpace(field), `"'`)
	base := filepath.Base(field)
	if i := strings.LastIndex(base, `\`); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".exe")
}

func normalizeStartToken(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// looksLikeClaudePipe is kept as a compatibility alias for older tests while
// the implementation supports Claude, Copilot, and Codex non-interactive runs.
func looksLikeClaudePipe(cmdline string) bool {
	return looksLikePipeMode(cmdline)
}

// psProcessSnapshot obtains parent PID, start token, and command line in one
// timeout-bounded ps call. LC_ALL/LANG pin lstart's opaque textual token to a
// stable locale and timezone. Only the PID and normalized start token escape
// discovery.
func psProcessSnapshot(pid int) (processSnapshot, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), psCommandTimeout)
	defer cancel()
	out, err := runPSProcessSnapshot(ctx, pid)
	if err != nil {
		return processSnapshot{}, false
	}
	return parseProcessSnapshot(string(out))
}

func parseProcessSnapshot(output string) (processSnapshot, bool) {
	fields := strings.Fields(output)
	// POSIX ps lstart is five whitespace-delimited fields in the C locale:
	// weekday, month, day, time, year. The remaining fields are command text.
	if len(fields) < 7 {
		return processSnapshot{}, false
	}
	parent, err := strconv.Atoi(fields[0])
	if err != nil {
		return processSnapshot{}, false
	}
	commandLine := strings.Join(fields[6:], " ")
	if commandLine == "" {
		return processSnapshot{}, false
	}
	return processSnapshot{
		ParentPID: parent, StartToken: normalizeStartToken(strings.Join(fields[1:6], " ")), CommandLine: commandLine,
	}, true
}

func stableProcessLocaleEnv() []string {
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

// psCommandLine and psParentPID remain compatibility test helpers. Production
// ancestry discovery calls psProcessSnapshot once per PID.
func psCommandLine(pid int) string {
	snapshot, ok := psProcessSnapshot(pid)
	if !ok {
		return ""
	}
	return snapshot.CommandLine
}

func psParentPID(pid int) int {
	snapshot, ok := psProcessSnapshot(pid)
	if !ok {
		return -1
	}
	return snapshot.ParentPID
}
