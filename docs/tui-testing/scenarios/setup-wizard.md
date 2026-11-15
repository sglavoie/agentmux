# First-project wizard: detected defaults and recent directories

The `setup-profile-*.json` and `setup-project-*.json` scenarios cover the
unchanged dashboard empty-state hints. The line-oriented wizard runs outside
Bubble Tea; its prompt scenarios are automated in
`cmd/config/setup_flags_test.go` and `cmd/config/setup_test.go`.

Run the focused scenarios with:

```sh
env -u AMX_RUNTIME_SESSION_ID -u TMUX -u TMUX_PANE \
  go test ./cmd/config -run 'TestSetup|TestWorkspaceSetup'
```

These tests use temporary homes and fake backend checks; they do not launch an
agent or touch the operator's hooks.

| Scenario | Expected prompts and outcome |
| --- | --- |
| No detected harness | Agent CLI defaults to `claude`; project directory defaults to the current directory |
| Only Codex default home exists | Agent CLI defaults to `codex`, even if its executable is absent |
| Several harnesses detected | Agent CLI retains the `claude` default |
| Recent existing project | A numbered directory list appears before `Project directory`; entering `1` uses the first path |
| Missing/deleted project | It is absent from detection; entering a missing path manually explains the error and allows another answer |
| No recent projects | Free-form `Project directory` prompt remains; no extra selection prompt consumes an answer |
| Partial setup flags | Supplied flags answer their fields; remaining fields still prompt |
| Both harness and directory flags | No prompts, including no hook prompt; hooks remain unsynced unless `--sync-hooks` is passed |
| Embedded workspace command | The same wizard works when invoked with a command that has no setup flags |
| Hook-sync failure | Config remains saved; summary provides a retry command; JSON reports `saved: true` and `hooks_status: "failed"` |

In a disposable interactive HOME, verify the numbered list remains readable
in both narrow and wide terminals and that the embedded wizard still ends with
“Press Enter to return to the workspace and refresh projects.” Authentication
is never inferred from a detected home, and setup never launches an agent.
