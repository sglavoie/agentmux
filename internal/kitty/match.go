package kitty

import (
	"fmt"
	"regexp"
)

// MatchTabByID returns a match expression that selects a tab by numeric ID.
func MatchTabByID(id int) string {
	return fmt.Sprintf("id:%d", id)
}

// MatchTabByTitle returns a match expression that selects tabs by title.
//
// Per Kitty's match syntax, text fields like `title` interpret the query as a
// regular expression, so callers must escape regex metacharacters themselves
// if they need an exact-match lookup. The expression is intentionally not
// anchored so partial matches work for callers that want substring semantics.
func MatchTabByTitle(title string) string {
	return fmt.Sprintf("title:%s", title)
}

// MatchWindowByVar returns a match expression selecting windows with a specific user var value.
//
// Kitty interprets both NAME and VALUE in `var:NAME=VALUE` as Python regexes
// applied unanchored. Callers passing literal variable/account/profile names
// expect exact equality, so both sides are escaped and anchored to prevent
// bleed (e.g. account "work" matching "workshop", or variable "cc_account"
// matching "old_cc_account").
func MatchWindowByVar(varName, value string) string {
	return fmt.Sprintf("var:^%s$=^%s$", regexp.QuoteMeta(varName), regexp.QuoteMeta(value))
}

// MatchAccountWindows returns a match expression for all windows belonging to an account.
func MatchAccountWindows(account string) string {
	return MatchWindowByVar("cc_account", account)
}

// MatchWindowByID returns a match expression that selects a window by numeric ID.
func MatchWindowByID(id int) string {
	return fmt.Sprintf("id:%d", id)
}

// MatchTabByWindowID returns a match expression for the tab containing the given window ID.
func MatchTabByWindowID(id int) string {
	return fmt.Sprintf("window_id:%d", id)
}
