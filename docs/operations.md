# Configuration, testing, and troubleshooting

This is the operator index. Each area below has its own guide.

| Guide | Covers |
| --- | --- |
| [Build and test](testing.md) | `just` gates, the three non-hermetic gates, CI lanes, the live Kitty worktree checklist, the TUI layout regression harness |
| [agent-session-loop rollout](asl-integration.md) | install order, `[defaults.asl]` modes, `amx config asl-sync`, the six doctor pairing dimensions, run-presence cutover, deferred gaps |
| [Paired releases](paired-releases.md) | `just paired-release-gate`, phase order, the signed evidence record, verification, tags and their rollback, maintenance installation, harness compatibility policy |
| [Screen classifier health](screen-classifier-health.md) | pattern-set sources in `amx doctor`, `screenstate_override_invalid` and `screenstate_persistent_decline`, the decline-signal drill |
| [Troubleshooting](troubleshooting.md) | symptom-first recovery for icons, hooks, routing, the daemon, the ASL cockpit, and debug bundles |

## Config commands

| Command | Description |
| --- | --- |
| `amx config init` | create the example config |
| `amx config edit` | open the config in `$EDITOR` |
| `amx config show` | print the parsed config |
| `amx config path` | print the config path |
| `amx config validate` | validate the config; warns about unknown and deprecated keys and about project directories that are missing or not directories. `--strict` fails on such a directory instead. `--json` reports the same as data, with a nonzero exit only under `--strict` |
| `amx config hooks-sync` | sync hooks into all configured profiles |
| `amx config hooks-status` | show hook sync status per profile |
