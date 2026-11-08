# Menu bar companion

`Agent Mux.app` is an optional native macOS menu bar app for Agent Mux. It
shows the live managed sessions with attention counts in the menu bar, shows
a session from a menu row or a global shortcut, shows the daemon's
notifications as native banners, and answers ASL supervisor questions in a
native panel. Sessions run on the tmux backend and open in the app's own
[workspace window](companion-app/workspace.md#workspace), which is also where Launch Project starts a
new one. The daemon stays the source of truth: the app subscribes to the daemon's companion
stream and runs the `amx` CLI for actions. Nothing in Agent Mux depends on it,
and quitting it returns every behavior to what it was without it; agents on
tmux keep running and `amx attach` reaches them from any terminal.

This page is the operator's guide. How the app is built, signed, and laid out,
and how each feature works inside, is in [`macos/README.md`](../macos/README.md).

## Guides

- [Installation and permissions](companion-app/install.md)
- [Workspace and terminals](companion-app/workspace.md)
- [Native workflow views](companion-app/workflows.md)
- [Shortcuts, notifications, and troubleshooting](companion-app/operations.md)

The detailed sections are in the topic guides below. Existing section links
continue to point to their new locations.

## Install

See [Install](companion-app/install.md#install).

### Building from source

See [Building from source](companion-app/install.md#building-from-source).

### Updating

See [Updating](companion-app/install.md#updating).

### Removing

See [Removing](companion-app/install.md#removing).

## Permissions

See [Permissions](companion-app/install.md#permissions).

## Workspace

See [Workspace](companion-app/workspace.md#workspace).

### Dashboard and utility views

See [Dashboard and utility views](companion-app/workspace.md#dashboard-and-utility-views).

### Keys, history, and the mouse in a terminal

See [Keys, history, and the mouse in a terminal](companion-app/workspace.md#keys-history-and-the-mouse-in-a-terminal).

## Native workflow views

See [Native workflow views](companion-app/workflows.md#native-workflow-views).

### Returning to work

See [Returning to work](companion-app/workflows.md#returning-to-work).

### Command palette and launch

See [Command palette and launch](companion-app/workflows.md#command-palette-and-launch).

### Waiting

See [Waiting](companion-app/workflows.md#waiting).

### Continue with another agent

See [Continue with another agent](companion-app/workflows.md#continue-with-another-agent).

### Conversation History

See [Conversation History](companion-app/workflows.md#conversation-history).

### Checkout Changes

See [Checkout Changes](companion-app/workflows.md#checkout-changes).

### Project Briefing

See [Project Briefing](companion-app/workflows.md#project-briefing).

### Conversation recaps

See [Conversation recaps](companion-app/workflows.md#conversation-recaps).

### Since Last Looked

See [Since Last Looked](companion-app/workflows.md#since-last-looked).

### ASL Runs and Run a Plan

See [ASL Runs and Run a Plan](companion-app/workflows.md#asl-runs-and-run-a-plan).

### Reading, status, and feedback

See [Reading, status, and feedback](companion-app/workflows.md#reading-status-and-feedback).

### Panes and restoration

See [Panes and restoration](companion-app/workflows.md#panes-and-restoration).

## Review-window shortcuts

See [Review-window shortcuts](companion-app/operations.md#review-window-shortcuts).

## Global shortcuts

See [Global shortcuts](companion-app/operations.md#global-shortcuts).

## Notifications

See [Notifications](companion-app/operations.md#notifications).

## Checking the pairing

See [Checking the pairing](companion-app/operations.md#checking-the-pairing).

## Troubleshooting

See [Troubleshooting](companion-app/operations.md#troubleshooting).

## Known limits

See [Known limits](companion-app/operations.md#known-limits).

### Waiting controls and update times

See [Waiting controls and update times](companion-app/operations.md#waiting-controls-and-update-times).

### Resolved action feedback

See [Resolved action feedback](companion-app/operations.md#resolved-action-feedback).

### Accounted-for sessions and notes

See [Accounted-for sessions and notes](companion-app/operations.md#accounted-for-sessions-and-notes).
