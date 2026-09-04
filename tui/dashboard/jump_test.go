package dashboard

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

func TestFindNextMatching(t *testing.T) {
	mkRows := func(states ...string) []session.RunningSession {
		rows := make([]session.RunningSession, len(states))
		for i, s := range states {
			rows[i].State = s
		}
		return rows
	}

	match := func(s string) bool { return defaultAttentionStates[s] }

	cases := []struct {
		name string
		rows []session.RunningSession
		from int
		dir  int
		want int
	}{
		{
			name: "empty",
			rows: nil,
			from: 0,
			dir:  +1,
			want: -1,
		},
		{
			name: "no match",
			rows: mkRows("", "bolt", "stop"),
			from: 0,
			dir:  +1,
			want: -1,
		},
		{
			name: "single match forward",
			rows: mkRows("", "key", ""),
			from: 0,
			dir:  +1,
			want: 1,
		},
		{
			name: "single match backward",
			rows: mkRows("", "key", ""),
			from: 2,
			dir:  -1,
			want: 1,
		},
		{
			name: "cursor on match advances past",
			rows: mkRows("key", "", "error"),
			from: 0,
			dir:  +1,
			want: 2,
		},
		{
			name: "wrap forward",
			rows: mkRows("error", "", ""),
			from: 2,
			dir:  +1,
			want: 0,
		},
		{
			name: "wrap backward",
			rows: mkRows("", "", "warning"),
			from: 0,
			dir:  -1,
			want: 2,
		},
		{
			name: "only match is current row wraps to itself",
			rows: mkRows("", "key", ""),
			from: 1,
			dir:  +1,
			want: 1,
		},
		{
			name: "invalid dir zero",
			rows: mkRows("key"),
			from: 0,
			dir:  0,
			want: -1,
		},
		{
			name: "from out of range positive",
			rows: mkRows("", "key"),
			from: 99,
			dir:  +1,
			want: 1,
		},
		{
			name: "from out of range negative",
			rows: mkRows("key", ""),
			from: -5,
			dir:  +1,
			want: 0,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := FindNextMatching(BuildItems(tc.rows), tc.from, tc.dir, match)
			if got != tc.want {
				t.Errorf("FindNextMatching(from=%d, dir=%d) = %d, want %d", tc.from, tc.dir, got, tc.want)
			}
		})
	}
}
