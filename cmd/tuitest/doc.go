// Package tuitest holds the hidden `tui-test` developer command, which drives
// the in-process TUI scenario harness in the tui package.
//
// The harness (tui's uitest_*.go files) and this command build only with the
// `tuitest` build tag, so a release amx links none of it; without the tag this
// file is all the package holds, and root registers the stand-in in
// cmd/harness_stub.go instead. Every recipe and test that runs scenarios
// passes the tag:
//
//	go run -tags tuitest . tui-test run docs/tui-testing/scenarios/*.json
//	go test -tags tuitest ./...
package tuitest
