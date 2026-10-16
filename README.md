# Agent Mux

Manage Claude Code, GitHub Copilot CLI, and OpenAI Codex sessions from one
terminal dashboard or the macOS workspace. Agents run on a private tmux
server and keep running when you close the terminal or app. Kitty is an
optional backend.

Agent Mux groups sessions by project and profile, shows which agents need you,
searches past conversations and plans, and supports resume, fork, handoff,
and managed Git worktrees. It can also start and supervise prepared
`agent-session-loop` plans.

## First session

Requires Go 1.26+, [just](https://github.com/casey/just), tmux 3.5+ on `PATH`,
and an installed agent CLI. On macOS, native notifications additionally need
[alerter](https://github.com/vjeantet/alerter) 26.x+ when enabled. The optional
macOS app bundles tmux.

From this checkout:

```sh
just install
amx config edit
```

Installation puts `amx` in `~/.local/bin` and creates a starter configuration
at `~/.config/agent-mux/config.toml` if none exists. Ensure `~/.local/bin` is on
your `PATH`. In the configuration, set the launch destination:

```toml
[defaults]
launch_backend = "tmux"
```

Adapt a starter `[[profiles]]` entry to an agent you have installed and
authenticated, then point a `[[projects]]` entry at your checkout and that
profile. [Profile setup](docs/operations.md#harness-profiles) explains isolated
homes and authentication.

```sh
amx config validate
amx config hooks-sync
amx backend status --check
amx
```

Press `:` and choose **Open Launcher**, highlight your project, and press `Enter`.
The launcher shows the destination and effective profile. `Enter` on the live
session attaches; `F12` detaches back to the dashboard. Attached, `F1` shows
the shortcuts and `F2` opens the live overview.

An unset command-line backend still means Kitty for compatibility. Explicitly
setting `launch_backend = "tmux"` keeps launches consistent across terminals.
The app's Launch Project preference is separate and shown in its menu;
[launch destination rules](docs/usage.md#launch-destinations) explain precedence.
The default will change only after the [daily-driver trial](docs/daily-driver-trial.md).

## Everyday use

| Task | Entry point |
| --- | --- |
| Find sessions needing input | Dashboard, or `F4` while attached |
| See every live pane | `F2` or `amx overview` |
| Resume or fork a conversation | `s` for project history, `S` for all history |
| Read an action failure | `:` → **Show Action Feedback**; notices remain until dismissed |
| Work with plans and worktrees | `:` → **Search Plans** or **Manage Worktrees** |
| Check installation health | `amx doctor --strict` |

See [Usage](docs/usage.md) for navigation, filtering, continuity, worktrees,
and CLI examples.

## macOS workspace (optional)

With macOS 14+ and Xcode, after installing `amx`:

```sh
just mac-install
```

Agent Mux.app shows sessions in a native workspace with embedded terminals,
conversation history, checkout changes, and run controls. Closing the app
leaves agents running. See [the companion guide](docs/companion-app.md) for
setup and [macOS build instructions](macos/README.md) for development.

## Kitty (optional)

Use `just install-kitty` and follow [Kitty setup](docs/operations.md#kitty-optional).
Its bindings include `F2` for the live session overview and
`F4` for the next attention tab. Reload Kitty after changing bindings.

## Maintenance and reference

- [Updates, paired ASL installation, and profile setup](docs/operations.md)
- [Troubleshooting](docs/troubleshooting.md) and [daemon diagnostics](docs/daemon.md)
- [Harness hooks](docs/hooks.md) and [watchdog state](docs/watchdog.md)
- [ASL integration](docs/asl-integration.md) and [paired releases](docs/paired-releases.md)
- [Build and test](docs/testing.md)
- [Screen classifier health](docs/screen-classifier-health.md)

`just uninstall` stops the daemon and removes the binary; configuration and
session data remain. `just mac-uninstall` removes the app and leaves agents
running unless explicitly asked to end them.
