package layout

import (
	"strings"
	"testing"
)

func TestQueryLineKeepsTheEndOfALongQuery(t *testing.T) {
	line := QueryLine("/", "a-very-long-query-ending-here█", 16)
	if got := StyledWidth(line); got != 16 {
		t.Fatalf("width = %d, want 16: %q", got, line)
	}
	if !strings.HasPrefix(line, "/…") || !strings.HasSuffix(line, "ending-here█") {
		t.Fatalf("line = %q, want the prompt, an ellipsis, and the end of the query", line)
	}
	if got := QueryLine("/", "short", 16); got != "/short" {
		t.Fatalf("short query = %q, want it whole", got)
	}
	if got := QueryLine("/", "anything", 0); got != "/anything" {
		t.Fatalf("no width yet = %q, want it whole", got)
	}
}
