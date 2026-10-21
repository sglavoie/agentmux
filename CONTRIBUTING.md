# Contributing to Agent Mux

Bug reports, documentation improvements, and focused pull requests are welcome.
For a substantial feature or architecture change, open an issue first so we can
agree on its scope.

## Develop locally

Clone the repository and install the tools listed in [the README](README.md).
Use the Go version declared in `go.mod` or newer. From the repository root:

```sh
just build
just test
```

`just test` includes the `tuitest` build tag; plain `go test ./...` omits the
dashboard scenario harness. Before submitting a code change, run `just check`
(requires `golangci-lint`). See [testing](docs/testing.md) for focused checks
and the separate integration gates.

For native desktop changes, follow [the macOS build guide](macos/README.md)
and run `just mac-test`. The desktop suite requires Xcode and launches the app.
It can run on another Mac using `AMX_MAC_TEST_HOST`. Set
`AMX_UI_TEST_REMOTE_ONLY=1` if UI must never run on your development Mac.

The ASL integration is optional for normal development. Its cross-repository
checks require a separate agent-session-loop checkout; describe any checks
you could not run in your pull request.

## Submit a change

- Keep each pull request focused and explain the problem and resulting behavior.
- Include relevant test results and screenshots for visible desktop changes.
- Update user documentation when commands, configuration, or behavior change.
- Preserve upstream license notices when modifying borrowed code.
- Use synthetic projects and conversations in fixtures and screenshots. Remove
  credentials, private prompts, personal paths, and account information from
  reports before sharing them.

Use Conventional Commit titles such as `fix(macos): Preserve terminal focus`.
Architecture and implementation guidance lives in [CLAUDE.md](CLAUDE.md).

Please keep discussions respectful, concrete, and focused on the work. See
[SECURITY.md](SECURITY.md) for suspected vulnerabilities.
