# Show available recipes by default
[private]
default:
    @just --list

python_src    := "kittens/"
shell_scripts := "bin/cc-launch bin/cc-status bin/cc-toggle-star bin/cc-toggle-flag bin/cc-toggle-dim bin/cc-toggle-focus bin/cc-toggle-mute bin/cc-idle-watch bin/cc-audit bin/ccm lib/cc-common.sh lib/cc-common/*.sh lib/cc-title-icons.sh lib/install-hooks.sh lib/cc-audit/*.sh lib/ccm.sh lib/ccm/*.sh lib/sync-preambles.sh hooks/cc-activity-log hooks/cc-hook-indicator"
uv            := "uv"
shellcheck    := env("SHELLCHECK", "shellcheck")
shfmt         := env("SHFMT", "shfmt")

prefix_bin   := home_directory() / ".local/bin"
prefix_lib   := home_directory() / ".local/lib/cc-manager"
prefix_kitty := home_directory() / ".config/kitty"
prefix_zfunc := home_directory() / ".zfunc"
prefix_bash_comp := home_directory() / ".local/share/bash-completion/completions"

root := justfile_directory()

# symlink pairs: destination:source
symlinks := prefix_bin / "ccm:bin/ccm" + "\n" + \
    prefix_bin / "cc-launch:bin/cc-launch" + "\n" + \
    prefix_bin / "cc-status:bin/cc-status" + "\n" + \
    prefix_bin / "cc-toggle-star:bin/cc-toggle-star" + "\n" + \
    prefix_bin / "cc-toggle-flag:bin/cc-toggle-flag" + "\n" + \
    prefix_bin / "cc-toggle-dim:bin/cc-toggle-dim" + "\n" + \
    prefix_bin / "cc-toggle-focus:bin/cc-toggle-focus" + "\n" + \
    prefix_bin / "cc-toggle-mute:bin/cc-toggle-mute" + "\n" + \
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
    prefix_bash_comp / "ccm:completions/_ccm.bash" + "\n" + \
    prefix_kitty / "cc-manager.conf:cc-manager.conf"

# Validate all config files
validate:
    ccm config validate

# Verify installation symlinks and binaries
check-install:
    @ok=true; \
    for cmd in ccm cc-launch cc-status cc-hook-indicator; do \
        if command -v "$cmd" >/dev/null 2>&1; then \
            printf "  %-20s OK (%s)\n" "$cmd" "$(command -v "$cmd")"; \
        else \
            printf "  %-20s MISSING\n" "$cmd"; \
            ok=false; \
        fi; \
    done; \
    if [ "$ok" = false ]; then exit 1; fi

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
    if ! python3 -c "import sys; assert sys.version_info >= (3, 13), f'Python 3.13+ required, found {sys.version}'" 2>/dev/null; then \
        echo "Error: Python 3.13+ is required (found: $(python3 --version 2>/dev/null || echo 'none'))." >&2; \
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
    @mkdir -p "{{prefix_bin}}" "{{prefix_lib}}" "{{prefix_lib}}/ccm" "{{prefix_kitty}}" "{{prefix_zfunc}}" "{{prefix_bash_comp}}"
    @echo '{{symlinks}}' | while IFS=: read -r dest src; do \
        if [ -d "$dest" ] && [ ! -L "$dest" ]; then \
            echo "Warning: removing existing directory $dest (replacing with symlink)" >&2; \
            rm -rf "$dest"; \
        fi; \
        ln -sf "{{root}}/$src" "$dest"; \
    done
    @cp "{{root}}/VERSION" "{{prefix_lib}}/.version"
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
    @rm -rf "{{prefix_lib}}/ccm" 2>/dev/null || true
    @rm -rf "{{prefix_lib}}" 2>/dev/null || true
    @echo "Done."
    @echo ""
    @echo "Other artifacts you may want to remove:"
    @echo "  - Config directory: ~/.config/cc-manager/"
    @echo "  - Temp files:       ${TMPDIR:-/tmp}/ccm-*"
    @echo "  - State data:       ~/.local/state/cc-manager/"

# Run tests
test:
    python3 tests/test_toml_roundtrip.py
    python3 tests/test_toml_writer.py
    python3 tests/test_config_validator.py
    python3 tests/test_hooks_validator.py
    python3 tests/test_hooks_install.py
    python3 tests/test_data_files.py
    python3 tests/test_toml_helper.py
    python3 tests/test_dashboard_drawing.py
    python3 tests/test_override_resolution.py
    python3 tests/test_smoke.py

# Run linters (skips tools that are not installed)
lint:
    @if {{uv}} run ruff --version >/dev/null 2>&1; then \
        {{uv}} run ruff check {{python_src}}; \
    else \
        echo "Skipping ruff (not installed)"; \
    fi
    @if command -v {{shellcheck}} >/dev/null 2>&1; then \
        {{shellcheck}} {{shell_scripts}}; \
    else \
        echo "Skipping shellcheck (not installed)"; \
    fi

# Format code
format:
    {{uv}} run ruff format {{python_src}}
    {{shfmt}} -w {{shell_scripts}}

# Check formatting without modifying (skips tools that are not installed)
format-check:
    @if {{uv}} run ruff --version >/dev/null 2>&1; then \
        {{uv}} run ruff format --check {{python_src}}; \
    else \
        echo "Skipping ruff format check (not installed)"; \
    fi
    @if command -v {{shfmt}} >/dev/null 2>&1; then \
        {{shfmt}} -d {{shell_scripts}}; \
    else \
        echo "Skipping shfmt (not installed)"; \
    fi

# Run type checker (skips if mypy is not installed)
typecheck:
    @if {{uv}} run mypy --version >/dev/null 2>&1; then \
        {{uv}} run mypy {{python_src}}; \
    else \
        echo "Skipping mypy (not installed)"; \
    fi

# Verify VERSION file matches pyproject.toml
check-version:
    @python3 -c "import tomllib; v=tomllib.load(open('pyproject.toml','rb'))['project']['version']; f=open('VERSION').read().strip(); assert v==f, f'Version mismatch: pyproject.toml={v} VERSION={f}'; print(f'  Version {v} OK')"

# Bump version in both VERSION and pyproject.toml (e.g. just bump 0.3.0)
bump version:
    @echo "{{version}}" > VERSION
    @python3 -c "\
    import re, pathlib; \
    p = pathlib.Path('pyproject.toml'); \
    t = p.read_text(); \
    t = re.sub(r'(?m)^version = \".*\"', 'version = \"{{version}}\"', t, count=1); \
    p.write_text(t); \
    "
    @echo "Bumped to {{version}}"
    @just check-version

# Run all checks (test, lint, format-check, typecheck, keybinding audit, version)
check: test lint format-check typecheck check-keybindings check-version
