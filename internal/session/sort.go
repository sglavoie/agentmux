package session

import (
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// SortKey computes the sort key for a tab with the given account, base title,
// and starred flag. mode is "alpha" or "account".
//
// Pinned (starred) tabs sort before non-pinned via a "0:" vs "1:" prefix.
// Alpha mode sorts by full base title; account mode groups by account name first.
func SortKey(account, baseTitle string, starred bool, mode string) string {
	pin := "1"
	if starred {
		pin = "0"
	}
	proj := projectName(baseTitle)
	switch mode {
	case "account":
		return pin + ":" + strings.ToLower(account) + ":" + strings.ToLower(proj)
	default: // "alpha"
		return pin + ":" + strings.ToLower(baseTitle)
	}
}

// ComputeInsertLocation determines where a new tab should be inserted to
// maintain sorted order. New tabs are never pinned (starred=false).
//
// Returns ("first", 0) when the new tab sorts before all existing ones, or
// ("after", tabID) where tabID is the tab the new tab should follow.
func ComputeInsertLocation(client kitty.KittyClient, mode, account, baseTitle string) (string, int, error) {
	running, err := List(client)
	if err != nil {
		return "", 0, err
	}

	newKey := SortKey(account, baseTitle, false, mode)

	type entry struct {
		key   string
		tabID int
	}
	var entries []entry
	for _, s := range running {
		k := SortKey(s.Account, s.Base, s.Starred, mode)
		entries = append(entries, entry{key: k, tabID: s.TabID})
	}

	// Sort existing entries by key to find insertion point.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].key < entries[j].key
	})

	prevID := 0
	for _, e := range entries {
		if e.key >= newKey {
			break
		}
		prevID = e.tabID
	}

	if prevID == 0 {
		return "first", 0, nil
	}
	return "after", prevID, nil
}

// SortRunning returns a copy of sessions sorted by the given mode.
func SortRunning(sessions []RunningSession, mode string) []RunningSession {
	sorted := make([]RunningSession, len(sessions))
	copy(sorted, sessions)
	sort.SliceStable(sorted, func(i, j int) bool {
		ki := SortKey(sorted[i].Account, sorted[i].Base, sorted[i].Starred, mode)
		kj := SortKey(sorted[j].Account, sorted[j].Base, sorted[j].Starred, mode)
		return ki < kj
	})
	return sorted
}

// projectName strips the leading "X:" account prefix from a base title.
// e.g., "W:api" → "api", "myproject" → "myproject"
func projectName(base string) string {
	if len(base) >= 2 && base[1] == ':' && base[0] >= 'A' && base[0] <= 'Z' {
		return base[2:]
	}
	return base
}
