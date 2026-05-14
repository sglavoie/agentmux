package embed

import _ "embed"

//go:embed example_config.toml
var ExampleConfig []byte
