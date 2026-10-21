package agentloop

import "testing"

// Only ProcessExited may assert absence. Everything else that fails to confirm
// identity must land on a verdict the caller reads as indeterminate, because a
// wrongly retired lease duplicates work while a wrongly held one costs a cycle.
func TestAuthenticateProcessOnlyAssertsAbsenceWhenThePIDIsGone(t *testing.T) {
	t.Parallel()

	const published = "Fri Jul 10 13:05:00 2026"
	cases := []struct {
		name       string
		ref        ProcessRef
		pidRunning PIDChecker
		tokens     map[int]string
		probed     bool
		want       ProcessVerdict
	}{
		{
			name:       "matching birth",
			ref:        ProcessRef{PID: 42, StartToken: published},
			pidRunning: livePIDs,
			tokens:     map[int]string{42: published},
			probed:     true,
			want:       ProcessAlive,
		},
		{
			name:       "pid gone",
			ref:        ProcessRef{PID: 42, StartToken: published},
			pidRunning: deadPIDs,
			probed:     true,
			want:       ProcessExited,
		},
		{
			name:       "pid recycled",
			ref:        ProcessRef{PID: 42, StartToken: published},
			pidRunning: livePIDs,
			tokens:     map[int]string{42: "Sat Jul 11 08:00:00 2026"},
			probed:     true,
			want:       ProcessReplaced,
		},
		{
			name:       "probe unavailable",
			ref:        ProcessRef{PID: 42, StartToken: published},
			pidRunning: livePIDs,
			probed:     false,
			want:       ProcessUnverified,
		},
		{
			name: "probe missed a PID the liveness check still sees",
			ref:  ProcessRef{PID: 42, StartToken: published},
			// Two reads of the process table that disagree: neither is trusted
			// alone, and the recheck below still reports it running.
			pidRunning: livePIDs,
			probed:     true,
			want:       ProcessUnverified,
		},
		{
			name:       "no token published",
			ref:        ProcessRef{PID: 42},
			pidRunning: livePIDs,
			tokens:     map[int]string{42: published},
			probed:     true,
			want:       ProcessUnverified,
		},
		{
			name:       "no pid published",
			ref:        ProcessRef{StartToken: published},
			pidRunning: livePIDs,
			probed:     true,
			want:       ProcessUnverified,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := authenticateProcess(tc.ref, tc.pidRunning, tc.tokens, tc.probed)
			if got != tc.want {
				t.Fatalf("authenticateProcess = %q, want %q", got, tc.want)
			}
		})
	}
}

// A process that exits between the liveness check and the birth probe is the
// one case where a missing token does mean gone — but only because the cheap
// check is re-asked and agrees.
func TestAuthenticateProcessRechecksLivenessWhenTheProbeMissedThePID(t *testing.T) {
	t.Parallel()

	calls := 0
	pidRunning := func(int) bool {
		calls++
		return calls == 1
	}
	got := authenticateProcess(ProcessRef{PID: 42, StartToken: "Fri Jul 10 13:05:00 2026"}, pidRunning, map[int]string{}, true)
	if got != ProcessExited {
		t.Fatalf("authenticateProcess = %q, want %q", got, ProcessExited)
	}
	if calls != 2 {
		t.Fatalf("pidRunning calls = %d, want 2", calls)
	}
}

func TestCompareStartTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		published string
		observed  string
		want      ProcessVerdict
	}{
		{"identical", "Fri Jul 10 13:05:00 2026", "Fri Jul 10 13:05:00 2026", ProcessAlive},
		{"padded day of month", "Fri Jul  4 13:05:00 2026", "Fri Jul 4 13:05:00 2026", ProcessAlive},
		{"whole hour zone offset", "Fri Jul 10 13:05:00 2026", "Fri Jul 10 09:05:00 2026", ProcessAlive},
		// Nepal is +05:45; a daemon there reading a token written in UTC must
		// still recognize its own loop.
		{"three quarter hour zone offset", "Fri Jul 10 13:05:00 2026", "Fri Jul 10 18:50:00 2026", ProcessAlive},
		{"widest real offset", "Fri Jul 10 13:05:00 2026", "Sat Jul 11 03:05:00 2026", ProcessAlive},
		// A recycled PID born a minute later is not a zone rendering, and
		// accepting it would authenticate an unrelated process.
		{"one minute later", "Fri Jul 10 13:05:00 2026", "Fri Jul 10 13:06:00 2026", ProcessReplaced},
		{"seconds differ", "Fri Jul 10 13:05:00 2026", "Fri Jul 10 09:05:12 2026", ProcessReplaced},
		{"beyond any real offset", "Fri Jul 10 13:05:00 2026", "Sat Jul 11 13:05:00 2026", ProcessReplaced},
		{"unparsable observation", "Fri Jul 10 13:05:00 2026", "yesterday", ProcessUnverified},
		{"unparsable publication", "whenever", "Fri Jul 10 13:05:00 2026", ProcessUnverified},
		{"empty observation", "Fri Jul 10 13:05:00 2026", "   ", ProcessUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareStartTokens(tc.published, tc.observed); got != tc.want {
				t.Fatalf("compareStartTokens(%q, %q) = %q, want %q", tc.published, tc.observed, got, tc.want)
			}
		})
	}
}

// The batched probe is what keeps the ten-second cadence to one subprocess. It
// must survive being handed nothing to do.
func TestReadProcessBirthTokensHandlesAnEmptyBatch(t *testing.T) {
	t.Parallel()

	for _, pids := range [][]int{nil, {}, {0, -1}} {
		tokens, probed := readProcessBirthTokens(pids)
		if !probed {
			t.Fatalf("readProcessBirthTokens(%v) reported a failed probe", pids)
		}
		if len(tokens) != 0 {
			t.Fatalf("readProcessBirthTokens(%v) = %v, want no tokens", pids, tokens)
		}
	}
}
