package screenstate

import "embed"

//go:embed patterns/*.toml
var defaultPatternFiles embed.FS
