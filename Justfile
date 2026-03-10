# Show available recipes by default
[private]
default:
    @just --list

python_src    := "kittens/"
shell_scripts := "bin/cc-launch bin/cc-status bin/cc-toggle-star bin/cc-toggle-flag bin/cc-idle-watch bin/cc-audit bin/ccm lib/cc-common.sh lib/cc-common/*.sh lib/cc-title-icons.sh lib/install-hooks.sh lib/cc-audit/*.sh lib/ccm.sh lib/ccm/*.sh lib/sync-preambles.sh hooks/cc-activity-log"
rye           := home_directory() / ".rye/shims/rye"
shellcheck    := "/opt/homebrew/bin/shellcheck"
shfmt         := "/opt/homebrew/bin/shfmt"

prefix_bin   := home_directory() / ".local/bin"
prefix_lib   := home_directory() / ".local/lib/cc-manager"
prefix_kitty := home_directory() / ".config/kitty"
prefix_zfunc := home_directory() / ".zfunc"

root := justfile_directory()

# symlink pairs: destination:source
symlinks := prefix_bin / "ccm:bin/ccm" + "\n" + \
    prefix_bin / "cc-launch:bin/cc-launch" + "\n" + \
    prefix_bin / "cc-status:bin/cc-status" + "\n" + \
    prefix_bin / "cc-toggle-star:bin/cc-toggle-star" + "\n" + \
    prefix_bin / "cc-toggle-flag:bin/cc-toggle-flag" + "\n" + \
    prefix_bin / "cc-idle-watch:bin/cc-idle-watch" + "\n" + \
    prefix_bin / "cc-audit:bin/cc-audit" + "\n" + \
    prefix_bin / "cc-hook-indicator:hooks/cc-hook-indicator" + "\n" + \
    prefix_bin / "cc-activity-log:hooks/cc-activity-log" + "\n" + \
    prefix_lib / "cc-common.sh:lib/cc-common.sh" + "\n" + \
    prefix_lib / "cc-title-icons.sh:lib/cc-title-icons.sh" + "\n" + \
    prefix_lib / "ccm.sh:lib/ccm.sh" + "\n" + \
    prefix_lib / "ccm/utils.sh:lib/ccm/utils.sh" + "\n" + \
    prefix_lib / "ccm/config.sh:lib/ccm/config.sh" + "\n" + \
    prefix_lib / "ccm/notify.sh:lib/ccm/notify.sh" + "\n" + \
    prefix_lib / "ccm/hooks.sh:lib/ccm/hooks.sh" + "\n" + \
    prefix_lib / "ccm/logs.sh:lib/ccm/logs.sh" + "\n" + \
    prefix_lib / "ccm/help.sh:lib/ccm/help.sh" + "\n" + \
    prefix_lib / "ccm/main.sh:lib/ccm/main.sh" + "\n" + \
    prefix_lib / "palette.json:data/palette.json" + "\n" + \
    prefix_lib / "icons.json:data/icons.json" + "\n" + \
    prefix_lib / "toml_writer.py:lib/toml_writer.py" + "\n" + \
    prefix_lib / "toml_reader.py:lib/toml_reader.py" + "\n" + \
    prefix_lib / "toml-helper.py:lib/toml-helper.py" + "\n" + \
    prefix_kitty / "cc_dashboard:kittens/cc_dashboard" + "\n" + \
    prefix_kitty / "claude_dashboard.py:kittens/claude_dashboard.py" + "\n" + \
    prefix_kitty / "cc_session_picker.py:kittens/cc_session_picker.py" + "\n" + \
    prefix_kitty / "cc_quick_switch.py:kittens/cc_quick_switch.py" + "\n" + \
    prefix_kitty / "cc_keybindings.py:kittens/cc_keybindings.py" + "\n" + \
    prefix_kitty / "cc_color_picker.py:kittens/cc_color_picker.py" + "\n" + \
    prefix_kitty / "cc_tab_status.py:kittens/cc_tab_status.py" + "\n" + \
    prefix_kitty / "cc_session_info.py:kittens/cc_session_info.py" + "\n" + \
    prefix_kitty / "cc_session_notes.py:kittens/cc_session_notes.py" + "\n" + \
    prefix_kitty / "cc_reorder_tabs.py:kittens/cc_reorder_tabs.py" + "\n" + \
    prefix_zfunc / "_ccm:completions/_ccm" + "\n" + \
    prefix_zfunc / "_cc-launch:completions/_cc-launch" + "\n" + \
    prefix_zfunc / "_cc-status:completions/_cc-status" + "\n" + \
    prefix_kitty / "cc-manager.conf:cc-manager.conf"

# Audit keybinding documentation completeness
check-keybindings:
    @python scripts/check_keybindings.py

# Check required dependencies
check-deps:
    @ok=true; \
    if ! command -v jq >/dev/null 2>&1; then \
        echo "Error: jq is required but not found." >&2; \
        ok=false; \
    fi; \
    if ! python3 -c "import tomllib" 2>/dev/null; then \
        echo "Error: Python 3.11+ is required (for tomllib)." >&2; \
        ok=false; \
    fi; \
    if ! command -v kitty >/dev/null 2>&1; then \
        echo "Error: kitty is required but not found." >&2; \
        ok=false; \
    fi; \
    if [ "$ok" = false ]; then \
        exit 1; \
    fi

# Install symlinks and hooks
install: check-deps check-keybindings
    @find . -name "*.py[co]" -or -name "__pycache__" -exec rm -rf -- {} + 2>/dev/null; true
    @mkdir -p "{{prefix_bin}}" "{{prefix_lib}}" "{{prefix_lib}}/ccm" "{{prefix_kitty}}" "{{prefix_zfunc}}"
    @echo '{{symlinks}}' | while IFS=: read -r dest src; do \
        if [ -d "$dest" ] && [ ! -L "$dest" ]; then \
            echo "Warning: removing existing directory $dest (replacing with symlink)" >&2; \
            rm -rf "$dest"; \
        fi; \
        ln -sf "{{root}}/$src" "$dest"; \
    done
    @echo "Symlinks created."
    @echo ""
    @echo "If you haven't already, add this line to your kitty.conf:"
    @echo "  include cc-manager.conf"
    @echo ""
    @echo "Then reload Kitty config with ctrl+shift+f5."
    @echo ""
    @just sync-preambles
    @just install-hooks

# Sync config documentation preambles
sync-preambles:
    @echo "Syncing config documentation..."
    @bash "{{root}}/lib/sync-preambles.sh" "{{root}}"

# Install Claude Code hooks
install-hooks:
    @echo "Installing Claude Code hooks..."
    @bash "{{root}}/lib/install-hooks.sh" "{{root}}/config/hooks.example.json"
    @echo "Hooks installation complete."

# Remove all symlinks and hooks
uninstall:
    @echo "Removing Claude Code hooks from account settings..."
    -@bash "{{root}}/lib/install-hooks.sh" --remove
    @echo '{{symlinks}}' | while IFS=: read -r dest src; do \
        if [ -L "$dest" ] && [ "$(readlink "$dest")" = "{{root}}/$src" ]; then \
            rm "$dest"; \
            echo "Removed $dest"; \
        fi; \
    done
    @echo "Done."
    @echo ""
    @echo "Other artifacts you may want to remove:"
    @echo "  - Config directory: ~/.config/cc-manager/"
    @echo "  - Temp files:       /tmp/ccm-*"
    @echo "  - Activity data:    ~/.local/state/cc-manager/activity/"

# Run tests
test:
    python3 tests/test_toml_roundtrip.py

# Run linters
lint:
    {{rye}} run ruff check {{python_src}}
    {{shellcheck}} {{shell_scripts}}

# Format code
format:
    {{rye}} run ruff format {{python_src}}
    {{shfmt}} -w {{shell_scripts}}

# Check formatting without modifying
format-check:
    {{rye}} run ruff format --check {{python_src}}
    {{shfmt}} -d {{shell_scripts}}

# Run type checker
typecheck:
    {{rye}} run mypy {{python_src}}

# Run all checks (test, lint, format-check, typecheck, keybinding audit)
check: test lint format-check typecheck check-keybindings
