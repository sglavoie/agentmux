package testutil

import (
	"fmt"
	"strings"
)

// SplitTableRow splits one Markdown table row into its trimmed cells. The
// border pipes are optional. A pipe splits cells only outside a code span and
// only when it is not escaped: "`a | b`" and "a \| b" are each one cell, and
// an escaped pipe reads as a plain "|" in the cell. A backtick run with no
// closing run of the same length is literal text, as in CommonMark, so it
// opens no span.
func SplitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(line); {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line) && line[i+1] == '|':
			cell.WriteByte('|')
			i += 2
		case c == '`':
			run := backtickRun(line, i)
			end := closingRun(line, i+run, run)
			if end < 0 {
				cell.WriteString(line[i : i+run])
				i += run
				continue
			}
			// Inside the span only \| means anything; it still reads as "|".
			cell.WriteString(strings.ReplaceAll(line[i:end+run], `\|`, "|"))
			i = end + run
		case c == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
			i++
		default:
			cell.WriteByte(c)
			i++
		}
	}
	cells = append(cells, strings.TrimSpace(cell.String()))
	// The border pipes leave an empty first and last field.
	if strings.HasPrefix(line, "|") && len(cells) > 0 {
		cells = cells[1:]
	}
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) && len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells
}

func backtickRun(s string, at int) int {
	n := 0
	for at+n < len(s) && s[at+n] == '`' {
		n++
	}
	return n
}

// closingRun returns where the first backtick run of exactly length n starts
// at or after from, or -1.
func closingRun(s string, from, n int) int {
	for i := from; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		run := backtickRun(s, i)
		if run == n {
			return i
		}
		i += run
	}
	return -1
}

// IsDelimiterRow reports whether cells are a table's header delimiter row,
// such as "| --- | :---: |".
func IsDelimiterRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		trimmed := strings.Trim(cell, ":")
		if len(trimmed) < 1 || strings.Trim(trimmed, "-") != "" {
			return false
		}
	}
	return true
}

// MarkdownTable finds the first table in doc whose header line starts with
// headerPrefix and returns its header and body rows as cells. The body ends
// at the first line that is not a table row. It is an error when no such
// table exists or its header is not followed by a delimiter row.
func MarkdownTable(doc, headerPrefix string) (header []string, rows [][]string, err error) {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), headerPrefix) {
			continue
		}
		if i+1 >= len(lines) || !IsDelimiterRow(SplitTableRow(lines[i+1])) {
			return nil, nil, fmt.Errorf("table %q has no delimiter row under its header", headerPrefix)
		}
		header = SplitTableRow(line)
		for _, body := range lines[i+2:] {
			if !strings.HasPrefix(strings.TrimSpace(body), "|") {
				break
			}
			rows = append(rows, SplitTableRow(body))
		}
		return header, rows, nil
	}
	return nil, nil, fmt.Errorf("no table with header %q", headerPrefix)
}
