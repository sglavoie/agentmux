// Package testutil holds helpers shared by tests across packages. Nothing in
// a shipped code path imports it.
//
// Eventually is the one polling loop for tests that wait on real time: a
// real tmux server, a real socket, a goroutine the test does not own. A test
// whose waiting is all goroutines and timers belongs in a testing/synctest
// bubble instead, where it waits on fake time and needs no poll at all.
//
// MarkdownTable and SplitTableRow are the one reading of a Markdown table for
// the tests that hold documentation to the code.
package testutil
