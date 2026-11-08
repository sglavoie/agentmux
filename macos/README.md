# Agent Mux.app

A native macOS menu bar companion for Agent Mux. It is an accessory app (no
Dock icon) that talks to the always-on amx daemon over its Unix socket and runs
the `amx` CLI for actions. The app embeds Ghostty (`AgentMux/Ghostty/`, see
[Embedded terminals](docs/runtime.md#embedded-terminals)) and shows each session's terminal
in its [workspace window](docs/workspace.md#workspace), beside a sidebar of every live session.

The app shows the live managed sessions, with attention counts in the menu bar
and a badge on its Dock icon whenever the icon is visible. The badge shows the
total sessions requiring attention and clears when none are waiting or the
daemon feed is disconnected. A click focuses the session in the workspace.
Two global shortcuts reach the dashboard and the sessions
waiting on you from any app. The daemon's notifications arrive as native banners
while the app runs, and ASL supervisor questions are answered in a native panel.

Dock badges require **System Settings → Notifications → Agent Mux → Badge
application icon** to be enabled. An upgrade from a build that only requested
banner and sound permissions may leave this switch off.

## Guides

- [Building and distributing Agent Mux.app](docs/building.md)
- [App layout, terminals, and runtime](docs/runtime.md)
- [Workspace implementation](docs/workspace.md)
- [Notifications and answer panels](docs/notifications.md)
- [Shortcuts and preferences](docs/preferences.md)
- [Tests, logs, and shared conversations](docs/testing.md)

The detailed sections are in the topic guides below. Existing section links
continue to point to their new locations.

## Requirements

See [Requirements](docs/building.md#requirements).

## Recipes

See [Recipes](docs/building.md#recipes).

### GhosttyKit

See [GhosttyKit](docs/building.md#ghosttykit).

### The tmux runtime

See [The tmux runtime](docs/building.md#the-tmux-runtime).

### Signing

See [Signing](docs/building.md#signing).

### Other environment

See [Other environment](docs/building.md#other-environment).

### Install, update, uninstall

See [Install, update, uninstall](docs/building.md#install-update-uninstall).

### Icons

See [Icons](docs/building.md#icons).

### Notarizing a release build

See [Notarizing a release build](docs/building.md#notarizing-a-release-build).

## Layout

See [Layout](docs/runtime.md#layout).

## Embedded terminals

See [Embedded terminals](docs/runtime.md#embedded-terminals).

## Workspace

See [Workspace](docs/workspace.md#workspace).

### Decision inbox, conversation history, and changes

See [Decision inbox, conversation history, and changes](docs/workspace.md#decision-inbox-conversation-history-and-changes).

### Native ASL Runs and plan launch

See [Native ASL Runs and plan launch](docs/workspace.md#native-asl-runs-and-plan-launch).

### Dashboard and utility views

See [Dashboard and utility views](docs/workspace.md#dashboard-and-utility-views).

## Which amx the app runs

See [Which amx the app runs](docs/runtime.md#which-amx-the-app-runs).

## How the app runs amx

See [How the app runs amx](docs/runtime.md#how-the-app-runs-amx).

## The live feed

See [The live feed](docs/runtime.md#the-live-feed).

## The daemon line

See [The daemon line](docs/runtime.md#the-daemon-line).

## Focus

See [Focus](docs/runtime.md#focus).

## Menu actions

See [Menu actions](docs/runtime.md#menu-actions).

## Notifications

See [Notifications](docs/notifications.md#notifications).

## ASL answer panel

See [ASL answer panel](docs/notifications.md#asl-answer-panel).

## Prompt answer panel

See [Prompt answer panel](docs/notifications.md#prompt-answer-panel).

## Workspace shortcuts

See [Workspace shortcuts](docs/preferences.md#workspace-shortcuts).

## Global shortcuts

See [Global shortcuts](docs/preferences.md#global-shortcuts).

## Preferences

See [Preferences](docs/preferences.md#preferences).

## Single instance and tests

See [Single instance and tests](docs/testing.md#single-instance-and-tests).

### Native agent handoff

See [Native agent handoff](docs/testing.md#native-agent-handoff).

### Project Briefing

See [Project Briefing](docs/testing.md#project-briefing).

### Running the UI on another Mac

See [Running the UI on another Mac](docs/testing.md#running-the-ui-on-another-mac).

## Logs

See [Logs](docs/testing.md#logs).

### Recorded activity

See [Recorded activity](docs/testing.md#recorded-activity).

## Shared conversation threads

See [Shared conversation threads](docs/testing.md#shared-conversation-threads).
