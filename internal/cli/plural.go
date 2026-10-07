package cli

import "fmt"

// Count is any integer a command counts with; daemon counters are uint64.
type Count interface {
	~int | ~int64 | ~uint64
}

// CountOf is a count and the noun that agrees with it: "1 plan", "3 plans".
// Command output spells the plural out rather than hedging with "plan(s)",
// which reads as a template that was never filled in.
func CountOf[N Count](n N, one, many string) string {
	return fmt.Sprintf("%d %s", n, ByCount(n, one, many))
}

// ByCount picks the word that agrees with n, for the verbs and pronouns that
// follow a counted noun: "1 conversation has", "2 conversations have".
func ByCount[N Count](n N, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
