package layout

// This file is a test-only bridge, not production surface. `composeOverlay` is
// package-private because every caller outside tui/layout centers first and so
// goes through CenterOverlay or TopCenterOverlay. The overlay test
// still drives the raw splice at explicit coordinates, so the name it calls is
// re-provided here rather than exported from overlay.go.

// ComposeOverlay exposes composeOverlay to this package's tests.
var ComposeOverlay = composeOverlay
