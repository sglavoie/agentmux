# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

cc-manager-kitty is a Claude Code session manager for the Kitty terminal. It manages multiple Claude Code sessions as Kitty tabs, with support for multiple accounts (each with its own `CLAUDE_CONFIG_DIR`), color-coded tab bars, and an interactive curses-based dashboard. The tool relies on Kitty's remote control protocol (`kitty @`) and `jq` for JSON processing.

## Installation and testing

There is no build step or test suite. The project consists of shell scripts and a Python kitten. Install and uninstall via `just install` and `just uninstall`, which create/remove symlinks to `~/.local/bin/`, `~/.config/kitty/`, and `~/.zfunc/`. To verify changes manually, run `cc-launch --help` or `cc-status --help` after installing.

## Architecture

The codebase has a unified CLI entry point, a shared Bash library, a shared data layer, and three main components.

`data/` contains the shared JSON data files that serve as the single source of truth for the color palette and icon set. `data/palette.json` defines all 10 named colors with their active/inactive background hex values and text foreground RGB arrays. `data/icons.json` defines the star and state icons (bolt, stop, key, error) along with a `state_order` array for consistent regex construction. Both the Bash library (via `jq`) and the Python kitten code (via `json` stdlib) read from these files at runtime. The JSON files are symlinked into `~/.local/lib/cc-manager/` by `just install`.

`bin/ccm` is the unified CLI entry point that provides a single `ccm` command with subcommands. It delegates `ccm launch`, `ccm status`, and `ccm star` to the existing scripts via `exec`, and implements `ccm config` subcommands (edit, show, path, init, sync) for managing config files. It also provides `ccm dashboard` to launch the curses kitten and `ccm help` for discoverability.

`lib/cc-common.sh` is the shared Bash library sourced by both `cc-launch` and `cc-status`. It contains socket discovery, gum detection and styling, config file path resolution, tilde expansion, account loading (parallel indexed arrays), defaults loading, flag/project-window resolution, the 10-color palette, account-to-color mapping, and the `build_accounts_json`/`default_account_name` utilities. It has a source guard to prevent double-sourcing and does not call any functions at source time; callers invoke `load_accounts`, `load_defaults`, etc. explicitly.

`bin/cc-launch` is the primary Bash script (~650 lines). It handles session lifecycle: launching tabs from `sessions.toml`, adding/closing individual tabs, listing active sessions, saving current state back to config, and cleaning up stale tabs. It uses `kitty @ launch`, `kitty @ ls`, and `kitty @ set-tab-color` for all Kitty interactions, and optionally uses `gum` (charmbracelet/gum) for interactive TUI prompts. The dispatch is a case-match in `main()` routing `--all`, `--add`, `--close`, `--list`, `--save`, `--cleanup`, and `--sockets` to their respective `cmd_*` functions.

`bin/cc-status` is a smaller Bash script that queries `kitty @ ls` and prints a dynamically-sized table of active Claude sessions. It sources `lib/cc-common.sh` for account loading and utility functions.

`kittens/claude_dashboard.py` is a Python curses application that runs as a Kitty overlay kitten (via `kitten claude_dashboard.py`). It provides an interactive session list with navigation, close-confirmation, and a new-session creation form. It loads the color palette and icon set from the shared JSON data files in `data/`, implements account loading in Python, and calls `kitty @` commands via `subprocess.run`.

## Key conventions

Account detection uses Kitty user vars (`--var cc_account=NAME` set at launch time) as the primary method, with a fallback to parsing `CLAUDE_CONFIG_DIR` from process cmdlines. Both the shell scripts and the Python kitten implement this two-tier lookup. Additional user vars `cc_flags`, `cc_pw`, and `cc_env` store the per-session flags, project window override, and environment variables respectively for round-trip fidelity with save, restart, clone, and move operations.

Config files use TOML format. New installs use `ccm config init` to create example configs. The config directory defaults to `~/.config/cc-manager/` but can be overridden via `CC_MANAGER_CONFIG` and `CC_MANAGER_ACCOUNTS` environment variables.

`accounts.toml` uses `[[accounts]]` entries with `name`, `config_dir`, and `color` keys. `sessions.toml` uses `[[sessions]]` entries with required `title`, `directory`, `account` keys and optional `flags`, `project_window`, `env` keys. `defaults.toml` has a `[defaults]` section (with `claude_flags`, `project_window`, `sort_sessions`, `dashboard_refresh`) and a `[notifications]` section (with boolean `error`, `permission`, `stop`, `idle`, plus `idle_alert_minutes`, `min_secs`, and `ignore_patterns` as an array of strings).

The TOML infrastructure consists of `lib/toml_reader.py` (uses stdlib `tomllib`), `lib/toml_writer.py` (pure-Python serializer, no third-party deps), and `lib/toml-helper.py` (CLI bridge for Bash scripts, outputting TSV). These are symlinked to `~/.local/lib/cc-manager/` by `just install`.

Override resolution follows a simple model: if a session's optional key is omitted, the global default applies; if it is `"-"`, the feature is disabled; any other value replaces the global default entirely with no merging. Note that the `--save` command reconstructs `sessions.toml` from live Kitty tab state and cannot recover per-session flag or project_window overrides. The env key is round-tripped via the `cc_env` Kitty user var.

The color palette has exactly 10 named colors (green, blue, red, orange, purple, teal, pink, yellow, gray, cyan), each with active/inactive background hex values and text foreground RGB values. The palette and icon set are defined once in `data/palette.json` and `data/icons.json` respectively, and read at runtime by both the Bash library (via `jq`) and the Python kitten code (via `json` stdlib). To change a color or icon, edit the corresponding JSON file — no synchronization across files is needed.

Tab title convention uses an uppercase initial of the account name followed by a colon (e.g., `W:project` for work, `P:dotfiles` for personal). The `--cleanup` command relies on this pattern to identify managed tabs. The star icon (`★`) functions as a pin toggle, bound to F2: pressing F2 stars the tab and moves it to the front of the tab bar, where pinned tabs are sorted alphabetically among themselves. Pressing F2 again unstars the tab and returns it to its natural position. Pinning works regardless of whether `sort_sessions=alpha` is enabled — pinned tabs always reorder to the front.

Kitty socket discovery follows a three-step precedence: explicit `--socket` argument, then `KITTY_LISTEN_ON` environment variable, then the most recent `/tmp/kitty-*` socket file.

Shell scripts target Bash 3 compatibility (macOS default), using parallel indexed arrays instead of associative arrays for account storage.

`cc-hook-indicator` automatically detects `claude -p` (pipe mode) invocations by walking the process tree. When pipe mode is detected, macOS notifications are suppressed, but icon changes and activity tracking still function normally.

Hooks are managed through `~/.config/cc-manager/hooks.json`, which has two top-level keys: `hooks` (the global defaults defining which `cc-hook-indicator` commands run for each of the 6 Claude Code hook events) and `account_overrides` (per-account customizations). The `account_overrides` object is keyed by account name and supports `disabled_events` (an array of event names to suppress the built-in `cc-hook-indicator` entry for that account) and `extra_commands` (a dict mapping event names to arrays of additional hook entries appended after the built-in ones). A file without `account_overrides` behaves identically to one with an empty `account_overrides` object, preserving backward compatibility.

The `ccm hooks` CLI subcommand provides two operations: `ccm hooks sync [ACCOUNT]` runs `install-hooks.sh` to merge the computed effective hooks into each account's `settings.json` (or a single account when specified), and `ccm hooks status` prints a table showing whether each account's installed hooks are `in_sync`, `drifted`, or `missing` relative to the current `hooks.json` configuration. The dashboard provides an interactive hooks manager accessible via the `H` key from the session list, where users can navigate accounts, toggle events on/off per account, add or remove custom hook commands, and trigger sync operations with visual drift feedback.

## Kitty documentation reference

The `docs/` directory contains concise reference documents extracted from the official Kitty documentation at https://sw.kovidgoyal.net/kitty/. Consult these when working on Kitty-specific functionality:

- `docs/kitty_remote_control.md` covers the `kitten @` remote control protocol, including how to enable it, socket discovery, the `--to` flag, and summaries of all commands used by this project (`ls`, `launch`, `set-tab-color`, `close-tab`, `set-tab-title`, `focus-tab`).
- `docs/kitty_launch.md` details the launch command's options for creating tabs, setting environment variables, user variables (`--var`), working directories, focus control, and process lifecycle.
- `docs/kitty_kittens.md` explains how to write custom Kitty kittens in Python, including the `main()` / `handle_result()` entry points, the `boss` object, remote control from within kittens, and how `claude_dashboard.py` diverges from the standard pattern.
- `docs/kitty_tab_bar.md` covers tab bar configuration in `kitty.conf` (position, style, title templates, colors) and per-tab color overrides via `kitten @ set-tab-color`, which is how cc-manager applies account-specific colors.
- `docs/kitty_match_syntax.md` documents the `--match` expression syntax used across all `kitten @` commands, including available fields, query types, Boolean operators, and how cc-manager uses `var:cc_account` matching.
