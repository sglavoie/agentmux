package testutil

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitTableRow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
		want []string
	}{
		{"bordered", "| a | b | c |", []string{"a", "b", "c"}},
		{"borderless", "a | b", []string{"a", "b"}},
		{"surrounding space", "   |  a  |b|   ", []string{"a", "b"}},
		{"empty cells kept", "| a |  | c |", []string{"a", "", "c"}},
		{"pipe in a code span", "| `amx a | b` | c |", []string{"`amx a | b`", "c"}},
		{"pipe without spaces in a code span", "| `x|y` | z |", []string{"`x|y`", "z"}},
		{"double-backtick span holding a backtick", "| ``a ` | b`` | c |", []string{"``a ` | b``", "c"}},
		{"escaped pipe", `| a \| b | c |`, []string{"a | b", "c"}},
		{"escaped pipe in a code span", "| `a \\| b` | c |", []string{"`a | b`", "c"}},
		{"escaped pipe ends the row", `| a | b \|`, []string{"a", "b |"}},
		{"unclosed backtick is literal", "| it`s | b |", []string{"it`s", "b"}},
		{"pipe right after a closed span splits", "| `a`|b |", []string{"`a`", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SplitTableRow(tc.line); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SplitTableRow(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestIsDelimiterRow(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]bool{
		"| --- | --- |":       true,
		"|:---|---:|:-:|":     true,
		"| - |":               true,
		"| --- | x |":         false,
		"| :: |":              false,
		"| a | b |":           false,
		"| -- - |":            false,
		"| `---` |":           false,
		"| --- | --- | --- |": true,
	} {
		if got := IsDelimiterRow(SplitTableRow(line)); got != want {
			t.Errorf("IsDelimiterRow(%q) = %v, want %v", line, got, want)
		}
	}
	if IsDelimiterRow(nil) {
		t.Error("no cells is not a delimiter row")
	}
}

func TestMarkdownTable(t *testing.T) {
	t.Parallel()
	doc := strings.Join([]string{
		"# Doc",
		"",
		"| Other | Table |",
		"| --- | --- |",
		"| x | y |",
		"",
		"| ID | Command | Note |",
		"| --- | --- | --- |",
		"| W1 | `amx a | b` | first |",
		"| W2 | `amx c` | escaped \\| pipe |",
		"",
		"| ID | Command | Note |",
		"| --- | --- | --- |",
		"| W9 | later | table |",
	}, "\n")

	header, rows, err := MarkdownTable(doc, "| ID | Command |")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ID", "Command", "Note"}; !reflect.DeepEqual(header, want) {
		t.Fatalf("header = %q, want %q", header, want)
	}
	want := [][]string{
		{"W1", "`amx a | b`", "first"},
		{"W2", "`amx c`", "escaped | pipe"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %q, want %q (only the first matching table)", rows, want)
	}

	if _, _, err := MarkdownTable(doc, "| Missing |"); err == nil {
		t.Fatal("a table that is not there was found")
	}
	if _, _, err := MarkdownTable("| A | B |\n| a | b |\n", "| A |"); err == nil {
		t.Fatal("a header with no delimiter row was accepted")
	}
	_, rows, err = MarkdownTable("| A |\n| --- |\n", "| A |")
	if err != nil || len(rows) != 0 {
		t.Fatalf("an empty table = %q, %v; want no rows and no error", rows, err)
	}
}
