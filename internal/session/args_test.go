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
