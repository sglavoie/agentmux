package screenstate

import "embed"

// The default pattern files sit beside the adapters they describe, named
// patterns_<harness>.toml so the file a reader wants is next to claude.go and
// codex.go rather than one directory away. loadEmbedded derives the harness
// name from the part after the prefix.
//
//go:embed patterns_*.toml
var defaultPatternFiles embed.FS
