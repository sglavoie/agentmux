package worktrees

// This file is a test-only bridge, not production surface. `formatRow` is
// package-private because the root renders worktree rows through RenderList,
// never a row at a time. The row test still pins column widths and truncation
// directly, so the name it calls is re-provided here rather than exported from
// rows.go.

// FormatRow exposes formatRow to this package's tests.
var FormatRow = formatRow
