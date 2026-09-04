package dashboard

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

const sortTestIdle = time.Minute

func sortedTabIDs(rows []session.RunningSession, activity map[int]time.Time) []int {
	SortRows(rows, activity, sortTestIdle)
	ids := make([]int, len(rows))
	for i := range rows {
		ids[i] = rows[i].TabID
	}
	return ids
}

func TestSortRowsOrdersUrgencyGroups(t *testing.T) {
	newest := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	rows := []session.RunningSession{
		{TabID: 1, State: "stop", PresentationChangedAt: newest},
		{TabID: 2, State: "bolt"},
		{TabID: 3, State: "key"},
	}
	if got, want := sortedTabIDs(rows, nil), []int{3, 2, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("group order = %v, want %v", got, want)
	}
}

func TestSortRowsOrdersEverySubtypeWithinItsGroup(t *testing.T) {
	stale := time.Now().Add(-time.Hour)
	tests := []struct {
		name     string
		rows     []session.RunningSession
		activity map[int]time.Time
		want     []int
	}{
		{
			name: "attention error then tied prompts then unseen stop",
			rows: []session.RunningSession{
				{TabID: 1, State: "stop", Unseen: true},
				{TabID: 2, State: "question"},
				{TabID: 3, State: "key"},
				{TabID: 4, State: "error"},
			},
			want: []int{4, 2, 3, 1},
		},
		{
			name: "active warning then tied explicit work then inferred activity",
			rows: []session.RunningSession{
				{TabID: 1, WindowID: 11},
				{TabID: 2, State: "compact"},
				{TabID: 3, State: "subagent"},
				{TabID: 4, State: "bolt"},
				{TabID: 5, State: "warning"},
			},
			want: []int{5, 2, 3, 4, 1},
		},
		{
			name: "idle has no subtype rank",
			rows: []session.RunningSession{
				{TabID: 1, WindowID: 11},
				{TabID: 2, State: "legacy"},
				{TabID: 3, State: "stop"},
			},
			activity: map[int]time.Time{11: stale},
			want:     []int{1, 2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sortedTabIDs(tt.rows, tt.activity); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tab order = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSortRowsUsesPresentationRecencyThenStableFallback(t *testing.T) {
	old := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	newest := old.Add(time.Hour)
	stale := time.Now().Add(-time.Hour)
	tests := []struct {
		name     string
		rows     []session.RunningSession
		activity map[int]time.Time
	}{
		{
			name: "attention",
			rows: []session.RunningSession{
				{TabID: 1, State: "key"},
				{TabID: 2, State: "question", PresentationChangedAt: newest},
				{TabID: 3, State: "key", PresentationChangedAt: old},
				{TabID: 4, State: "question", PresentationChangedAt: newest},
			},
		},
		{
			name: "active",
			rows: []session.RunningSession{
				{TabID: 1, State: "bolt"},
				{TabID: 2, State: "subagent", PresentationChangedAt: newest},
				{TabID: 3, State: "compact", PresentationChangedAt: old},
				{TabID: 4, State: "bolt", PresentationChangedAt: newest},
			},
		},
		{
			name: "idle",
			rows: []session.RunningSession{
				{TabID: 1, State: "stop"},
				{TabID: 2, State: "legacy", PresentationChangedAt: newest},
				{TabID: 3, WindowID: 13, PresentationChangedAt: old},
				{TabID: 4, State: "stop", PresentationChangedAt: newest},
			},
			activity: map[int]time.Time{13: stale},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := sortedTabIDs(tt.rows, tt.activity), []int{2, 4, 3, 1}; !reflect.DeepEqual(got, want) {
				t.Fatalf("recency order = %v, want %v", got, want)
			}
		})
	}
}

func TestSortRowsPreservesSortRunningPinAndConfiguredFallback(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 1, Account: "work", Base: "zeta", State: "key", Starred: true},
		{TabID: 2, Account: "work", Base: "beta", State: "question"},
		{TabID: 3, Account: "work", Base: "alpha", State: "key"},
	}
	rows = session.SortRunning(rows, "alpha")
	if got, want := sortedTabIDs(rows, nil), []int{1, 3, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback order = %v, want pinned row then configured alpha order %v", got, want)
	}
}

func TestSortRowsIsDeterministicAcrossRepeatedSorts(t *testing.T) {
	stamp := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	rows := []session.RunningSession{
		{TabID: 1, State: "question", PresentationChangedAt: stamp},
		{TabID: 2, State: "error"},
		{TabID: 3, State: "key", PresentationChangedAt: stamp},
		{TabID: 4, State: "warning"},
	}
	want := []int{2, 1, 3, 4}
	for i := 0; i < 20; i++ {
		if got := sortedTabIDs(rows, nil); !reflect.DeepEqual(got, want) {
			t.Fatalf("sort %d = %v, want %v", i, got, want)
		}
	}
}

func TestCompareRowPriorityIsTransitive(t *testing.T) {
	old := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	newest := old.Add(time.Hour)
	rows := []session.RunningSession{
		{State: "error", PresentationChangedAt: old},
		{State: "key", PresentationChangedAt: newest},
		{State: "question", PresentationChangedAt: old},
		{State: "stop", Unseen: true, PresentationChangedAt: newest},
		{State: "warning"},
		{State: "bolt", PresentationChangedAt: newest},
		{State: "compact", PresentationChangedAt: old},
		{State: ""},
		{State: "stop", PresentationChangedAt: newest},
	}

	for i := range rows {
		if got := compareRowPriority(rows[i], rows[i], nil, sortTestIdle); got != 0 {
			t.Fatalf("compare(row %d, itself) = %d, want 0", i, got)
		}
		for j := range rows {
			ij := compareRowPriority(rows[i], rows[j], nil, sortTestIdle)
			ji := compareRowPriority(rows[j], rows[i], nil, sortTestIdle)
			if ij != -ji {
				t.Fatalf("comparison is not antisymmetric for rows %d and %d: %d vs %d", i, j, ij, ji)
			}
			for k := range rows {
				jk := compareRowPriority(rows[j], rows[k], nil, sortTestIdle)
				if ij < 0 && jk < 0 && compareRowPriority(rows[i], rows[k], nil, sortTestIdle) >= 0 {
					t.Fatalf("comparison is not transitive for rows %d < %d < %d", i, j, k)
				}
			}
		}
	}
}
