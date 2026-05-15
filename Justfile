# Show available recipes by default
[private]
default:
    @just --list

prefix_bin := home_directory() / ".local/bin"
prefix_kitty := home_directory() / ".config/kitty"
prefix_config := home_directory() / ".config/agent-mux"
root := justfile_directory()

# Build the amx binary
build:
    go build -o amx .

# Run all tests
test:
    go test ./...

# Run go vet
vet:
    go vet ./...

# Run linter
lint:
    golangci-lint run

# Run full check suite (test + vet + lint)
check: test vet lint

# Install binary, Kitty config, and a starter app config if missing
install: build
    @mkdir -p "{{prefix_bin}}"
    rm -f "{{prefix_bin}}/amx"
    cp amx "{{prefix_bin}}/amx"
    @mkdir -p "{{prefix_kitty}}"
    ln -sf "{{root}}/agent-mux.conf" "{{prefix_kitty}}/agent-mux.conf"
    @if [ ! -f "{{prefix_config}}/config.toml" ]; then "{{prefix_bin}}/amx" config init >/dev/null; echo "Seeded {{prefix_config}}/config.toml"; fi
    @echo "Installed {{prefix_bin}}/amx"
    @echo "Symlinked {{prefix_kitty}}/agent-mux.conf"

# Remove binary and config
uninstall:
    rm -f "{{prefix_bin}}/amx"
    rm -f "{{prefix_kitty}}/agent-mux.conf"
    @echo "Removed {{prefix_bin}}/amx"
    @echo "Removed {{prefix_kitty}}/agent-mux.conf"

# Bump version in cmd/root.go (e.g. just bump 0.4.0)
bump version:
    @sed -i '' 's/const version = ".*"/const version = "{{version}}"/' cmd/root.go
    @grep 'const version' cmd/root.go
