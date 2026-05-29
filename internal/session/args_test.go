package session

import (
	"reflect"
	"testing"
)

func TestShellQuoteArgs_RoundTripsThroughParseFlags(t *testing.T) {
	args := []string{
		"--prompt",
		"hello world",
		"--literal=$HOME",
		"semi;colon",
		"O'Brien",
		"",
	}

	serialized := shellQuoteArgs(args)
	got := parseFlags(serialized)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("parseFlags(shellQuoteArgs(args)) = %#v, want %#v (serialized %q)", got, args, serialized)
	}
}

func TestSplitShellArgs_DoubleQuotedBackslashes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "ordinary backslashes are preserved",
			raw:  `cmd "C:\path\name" "\q"`,
			want: []string{"cmd", `C:\path\name`, `\q`},
		},
		{
			name: "shell escapes inside double quotes are consumed",
			raw:  "cmd \"a\\\"b\" \"c\\\\d\" \"cost\\$5\"",
			want: []string{"cmd", `a"b`, `c\d`, "cost$5"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splitShellArgs(tc.raw)
			if err != nil {
				t.Fatalf("splitShellArgs(%q): %v", tc.raw, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("splitShellArgs(%q) = %#v, want %#v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestSplitShellArgs_BackslashNewlineContinuesLine(t *testing.T) {
	raw := "cmd foo\\\nbar \"a\\\nb\" \\\n next"
	want := []string{"cmd", "foobar", "ab", "next"}

	got, err := splitShellArgs(raw)
	if err != nil {
		t.Fatalf("splitShellArgs(%q): %v", raw, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitShellArgs(%q) = %#v, want %#v", raw, got, want)
	}
}
