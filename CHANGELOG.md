# Changelog

All notable changes to this project will be documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [0.2.0] - 2026-03-11

### Added

- Unified `ccm` CLI entry point with subcommands
- Interactive curses dashboard (`ccm dashboard`) with session list, navigation,
  close-confirmation, and new-session form
- Session groups (`group` key) for organizing related sessions, with group sort
  mode, group filtering, and group-level launch/close actions
- Batch operations: clone, color override, config-dir override, and group
  actions for marked sessions
- Session notes overlay for per-tab free-text annotations
- Session info overlay and tab status overlay
- Activity log browser with search and cross-log global search
- Preferences editor with persistent UI state
- Hooks manager (`ccm hooks sync/status`) with per-account overrides
- Idle notification watcher (`ccm idle-watch`)
- Quick-switch fuzzy finder (`ccm switch`)
- Upstream feature compatibility checker (`ccm audit`)
- Config validation (`ccm config validate`)
- JSON output for `cc-status --json`
- Dry-run mode for `cc-launch`
- Progress indicators and dependency checks
- Tab sorting after lifecycle operations (restart, clone, move)
- Star/pin toggle (F2) and flag marker toggle
- Auto-generated keybinding overlay from footer hints
- Zsh completions for `ccm`, `cc-launch`, and `cc-status`
- Kitty session file example for startup

### Fixed

- Hardened error handling, input validation, and operational safety across shell
  scripts
- Improved error reporting and diagnostics
- Escape key delay reduced across all curses kittens
- Detail panel hidden when overlay menus are active
- Config dir override revert to account default

### Changed

- Migrated from Makefile to Justfile
- Shared color palette and icon set moved to JSON data files (`data/`)

## [0.1.0] - 2026-02-21

### Added

- Initial release with `cc-launch` and `cc-status` scripts
- Multi-account support via `accounts.toml` with color-coded tabs
- Session config via `sessions.toml` with per-session flag overrides
- Global defaults via `defaults.toml`
- TOML config infrastructure (reader, writer, CLI bridge)
- Kitty remote control integration for tab management
- Socket auto-discovery with multi-instance support
- Shared Bash library (`cc-common.sh`)
