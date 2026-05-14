# Show available recipes by default
[private]
default:
    @just --list

prefix_bin := home_directory() / ".local/bin"
prefix_kitty := home_directory() / ".config/kitty"
root := justfile_directory()

# Build the ccm binary
build:
    go build -o ccm .

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

# Install binary and config
install: build
    @mkdir -p "{{prefix_bin}}"
    rm -f "{{prefix_bin}}/ccm"
    cp ccm "{{prefix_bin}}/ccm"
    @mkdir -p "{{prefix_kitty}}"
    ln -sf "{{root}}/cc-manager.conf" "{{prefix_kitty}}/cc-manager.conf"
    @echo "Installed {{prefix_bin}}/ccm"
    @echo "Symlinked {{prefix_kitty}}/cc-manager.conf"

# Remove binary and config
uninstall:
    rm -f "{{prefix_bin}}/ccm"
    rm -f "{{prefix_kitty}}/cc-manager.conf"
    @echo "Removed {{prefix_bin}}/ccm"
    @echo "Removed {{prefix_kitty}}/cc-manager.conf"

# Bump version in cmd/root.go (e.g. just bump 0.4.0)
bump version:
    @sed -i '' 's/const version = ".*"/const version = "{{version}}"/' cmd/root.go
    @grep 'const version' cmd/root.go

# Remove stale symlinks left by the old Bash/Python installation
clean-legacy:
    @echo "Removing stale shell scripts from ~/.local/bin/..."
    rm -f "{{prefix_bin}}/cc-launch" "{{prefix_bin}}/cc-status"
    rm -f "{{prefix_bin}}/cc-toggle-star" "{{prefix_bin}}/cc-toggle-flag"
    rm -f "{{prefix_bin}}/cc-toggle-dim" "{{prefix_bin}}/cc-toggle-focus"
    rm -f "{{prefix_bin}}/cc-toggle-mute"
    rm -f "{{prefix_bin}}/cc-idle-watch" "{{prefix_bin}}/cc-audit"
    rm -f "{{prefix_bin}}/cc-hook-indicator" "{{prefix_bin}}/cc-activity-log"
    @echo "Removing stale Python kittens from ~/.config/kitty/..."
    rm -f "{{prefix_kitty}}"/cc_*.py
    rm -f "{{prefix_kitty}}/claude_dashboard.py"
    rm -rf "{{prefix_kitty}}/cc_dashboard"
    @echo "Removing stale library directory..."
    rm -rf {{home_directory()}}/.local/lib/cc-manager
    @echo "Removing stale completions..."
    rm -f {{home_directory()}}/.zfunc/_ccm {{home_directory()}}/.zfunc/_cc-launch {{home_directory()}}/.zfunc/_cc-status
    rm -f {{home_directory()}}/.local/share/bash-completion/completions/cc-launch
    rm -f {{home_directory()}}/.local/share/bash-completion/completions/cc-status
    rm -f {{home_directory()}}/.local/share/bash-completion/completions/ccm
    @echo "Legacy cleanup complete."
