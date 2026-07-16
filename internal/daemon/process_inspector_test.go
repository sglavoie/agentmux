package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPSHarnessProcessInspectorClassifiesAndDeduplicates(t *testing.T) {
	first := HarnessProcessIdentity{Harness: "claude", PID: 101, StartToken: "Thu Jul 16 10:20:30 2026"}
	replaced := HarnessProcessIdentity{Harness: "codex", PID: 102, StartToken: "Thu Jul 16 10:21:30 2026"}
	missing := HarnessProcessIdentity{Harness: "copilot", PID: 103, StartToken: "Thu Jul 16 10:22:30 2026"}
	inspector := &psHarnessProcessInspector{
		timeout: time.Second, batchSize: 64,
		runBatch: func(_ context.Context, pids []int) ([]byte, error) {
			if want := []int{101, 102, 103}; !reflect.DeepEqual(pids, want) {
				t.Fatalf("pids = %v, want %v", pids, want)
			}
			return []byte("101 Thu Jul 16 10:20:30 2026\n102 Thu Jul 16 11:21:30 2026\n"), nil
		},
	}

	got := inspector.InspectHarnessProcesses(context.Background(), []HarnessProcessIdentity{first, replaced, missing, first})
	if got[first] != HarnessProcessAliveSameInstance || got[replaced] != HarnessProcessReplacedInstance || got[missing] != HarnessProcessMissing {
		t.Fatalf("inspection = %+v", got)
	}
}

func TestPSHarnessProcessInspectorChunksAndIsolatesFailedBatch(t *testing.T) {
	identities := make([]HarnessProcessIdentity, 0, 5)
	for pid := 6; pid >= 2; pid-- {
		identities = append(identities, HarnessProcessIdentity{
			Harness: "claude", PID: pid, StartToken: fmt.Sprintf("Thu Jul 16 10:20:0%d 2026", pid),
		})
	}
	var mu sync.Mutex
	var batches [][]int
	inspector := &psHarnessProcessInspector{
		timeout: time.Second, batchSize: 2,
		runBatch: func(_ context.Context, pids []int) ([]byte, error) {
			mu.Lock()
			batches = append(batches, append([]int(nil), pids...))
			mu.Unlock()
			if pids[0] == 4 {
				return nil, errors.New("injected ps failure")
			}
			out := ""
			for _, pid := range pids {
				out += fmt.Sprintf("%d Thu Jul 16 10:20:0%d 2026\n", pid, pid)
			}
			return []byte(out), nil
		},
	}

	got := inspector.InspectHarnessProcesses(context.Background(), identities)
	if want := [][]int{{2, 3}, {4, 5}, {6}}; !reflect.DeepEqual(batches, want) {
		t.Fatalf("batches = %v, want %v", batches, want)
	}
	for _, identity := range identities {
		want := HarnessProcessAliveSameInstance
		if identity.PID == 4 || identity.PID == 5 {
			want = HarnessProcessUnknown
		}
		if got[identity] != want {
			t.Errorf("pid %d status = %q, want %q", identity.PID, got[identity], want)
		}
	}
}

func TestPSHarnessProcessInspectorTimeoutAndMalformedOutputAreUnknown(t *testing.T) {
	identity := HarnessProcessIdentity{Harness: "claude", PID: 101, StartToken: "Thu Jul 16 10:20:30 2026"}
	for _, tc := range []struct {
		name     string
		timeout  time.Duration
		runBatch func(context.Context, []int) ([]byte, error)
	}{
		{
			name: "timeout", timeout: 10 * time.Millisecond,
			runBatch: func(ctx context.Context, _ []int) ([]byte, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
		{
			name: "malformed", timeout: time.Second,
			runBatch: func(context.Context, []int) ([]byte, error) {
				return []byte("101 Thu Jul 16\n"), nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspector := &psHarnessProcessInspector{timeout: tc.timeout, batchSize: 64, runBatch: tc.runBatch}
			if got := inspector.InspectHarnessProcesses(context.Background(), []HarnessProcessIdentity{identity})[identity]; got != HarnessProcessUnknown {
				t.Fatalf("status = %q, want unknown", got)
			}
		})
	}
}

func TestPSHarnessProcessInspectorPinsTimezoneAcrossCaptureAndInspection(t *testing.T) {
	pid := os.Getpid()
	t.Setenv("TZ", "Asia/Tokyo")
	output, err := runPSHarnessProcessBatch(context.Background(), []int{pid})
	if err != nil {
		t.Fatalf("capture ps snapshot: %v", err)
	}
	observed, err := parsePSHarnessProcessBatch(output, []int{pid})
	if err != nil {
		t.Fatalf("parse capture ps snapshot: %v", err)
	}
	identity := HarnessProcessIdentity{Harness: "claude", PID: pid, StartToken: observed[pid]}

	t.Setenv("TZ", "America/Los_Angeles")
	inspector := newPSHarnessProcessInspector()
	if got := inspector.InspectHarnessProcesses(context.Background(), []HarnessProcessIdentity{identity})[identity]; got != HarnessProcessAliveSameInstance {
		t.Fatalf("inspection across ambient TZ divergence = %q, want alive_same_instance", got)
	}

	joined := "\n" + strings.Join(stableDaemonProcessLocaleEnv(), "\n") + "\n"
	if strings.Count(joined, "\nTZ=UTC\n") != 1 || strings.Contains(joined, "TZ=America/Los_Angeles") {
		t.Fatalf("stable daemon environment did not replace ambient TZ: %q", joined)
	}
}

func TestParsePSHarnessProcessBatchRejectsUnexpectedRows(t *testing.T) {
	got, err := parsePSHarnessProcessBatch([]byte("  42 Thu  Jul  16  10:20:30  2026\n"), []int{42, 43})
	if err != nil || got[42] != "Thu Jul 16 10:20:30 2026" {
		t.Fatalf("parse = %+v err=%v", got, err)
	}
	if _, present := got[43]; present {
		t.Fatal("missing pid appeared in parsed snapshot")
	}

	malformed := [][]byte{
		[]byte("42 Thu Jul 16 2026\n"),
		[]byte("not-a-pid Thu Jul 16 10:20:30 2026\n"),
		[]byte("99 Thu Jul 16 10:20:30 2026\n"),
	}
	for _, output := range malformed {
		if _, err := parsePSHarnessProcessBatch(output, []int{42}); err == nil {
			t.Errorf("parsePSHarnessProcessBatch(%q) succeeded", output)
		}
	}
}

func TestDeduplicatedLifecycleIdentitiesStableOrder(t *testing.T) {
	identities := []HarnessProcessIdentity{
		{Harness: "codex", PID: 3, StartToken: "c"},
		{Harness: "claude", PID: 2, StartToken: "b"},
		{Harness: "claude", PID: 1, StartToken: "a"},
	}
	candidates := make([]processLifecycleCandidate, 0, 4)
	for _, identity := range append(identities, identities[0]) {
		candidates = append(candidates, processLifecycleCandidate{identity: identity})
	}
	got := deduplicatedLifecycleIdentities(candidates)
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].Harness != identities[j].Harness {
			return identities[i].Harness < identities[j].Harness
		}
		return identities[i].PID < identities[j].PID
	})
	if !reflect.DeepEqual(got, identities) {
		t.Fatalf("identities = %+v, want %+v", got, identities)
	}
}
