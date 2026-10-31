# Download to first session

This milestone makes the packaged macOS app the first installation path in the
README. A missing config opens the workspace's existing guided setup before
the daemon is started. Invalid and unreadable configs remain errors. Setup's
summary returns app users to native launch controls and quotes the bundled
binary when a hook-sync retry is needed without `amx` on PATH.
Optional Git status and checkout review also bypass Apple's installer shim:
with no usable developer tools, status metadata stays empty and checkout
review reports Git as unavailable. An independently installed Git retains
PATH precedence.

Release assets now identify their architecture. `SHA256SUMS` covers the
actual uploaded ZIPs and evidence files, including the signatures; the paired
gate's unsigned-build hashes serve a different purpose. Release notes include
installation and update instructions.

## Automated verification

- `env -u AMX_RUNTIME_SESSION_ID just check`: passed, including the tagged
  suite, core race suites, both vet builds, and both lint builds.
- `just mac-test` and `just mac-test Release`, routed to the operator-selected
  MacBook Pro: each executed 1,024 tests, one skipped, zero failures. The
  changed native coverage checks missing/invalid/unreadable configuration,
  setup before daemon startup, and the return to Start Daemon afterwards.
- `TestPublishUploadsChecksumsOfSignedDownloads` runs the real publisher
  against local Git repositories and signed fixture evidence, with Apple and
  GitHub tooling replaced. It verifies both create and re-upload paths,
  architecture-specific filenames, and every checksum against the actual
  uploaded bytes after the fixture signature changes the CLI.
- The candidate was signed with Developer ID, accepted by Apple's notary
  service, stapled, and accepted by `spctl --assess --type execute`.

The candidate is a working-tree build based on `77ce1488`, not a published
release or a passing paired-release gate. See [candidate.json](candidate.json)
for its exact archive digest and product-source diff digest. The signed ZIP
is retained outside the repository; no binary is committed here.
Per-file product hashes also cover new source files absent from the tracked
diff. [check.log](check.log) and [publisher-fixture.log](publisher-fixture.log)
retain the final Go checks and the publisher fixture results. The refreshed
candidate changes the bundled Go CLI; native UI sources are unchanged since
the recorded Debug and Release runs. Their one skip is Dock badge verification,
which requires Accessibility permission on the test Mac.

## Packaged CLI acceptance

Run headlessly on macOS with Python 3.9+ as the acceptance driver:

```sh
python3 scripts/acceptance/packaged-app.py /path/to/release.zip \
  --apple-git-shim --workspace-setup --output /tmp/packaged-app.json

python3 scripts/acceptance/packaged-app.py /path/to/earlier-release.zip \
  --update /path/to/new-release.zip --output /tmp/packaged-update.json
```

The driver extracts the ZIP and checks the bundle signature. It uses a
disposable HOME, system-only PATH plus fixture commands, no separately
installed `amx` or tmux, and failing stand-ins for Git, Go, just, Homebrew,
Xcode, and Kitty. It runs guided setup, executes the hook commands that setup
installed, observes permission and completion states from the daemon, and
resumes a seeded conversation with its exact identity. Runtime checks require
the verified bundled tmux copied outside the app. The update case replaces
the bundle with a different app build, compares the server generation and
agent PID, and verifies that the surviving agent still accepts input. Cleanup
verifies that both the isolated server and daemon have stopped.

[macbook-headless.json](macbook-headless.json) records the final signed candidate
on the MacBook Pro with `--workspace-setup` and `--apple-git-shim`. All assertions
and cleanup passed; the report was retrieved when SSH connectivity returned.
[headless-update.json](headless-update.json) records
replacement of the earlier 0.8.1 bundle with the candidate. Both passed.
The harness is scripted and the transcript is seeded: this is package and
integration evidence, not a live model conversation. The test Macs have
developer tools installed; the acceptance environment disables them. It does
not claim a run on a physically pristine Mac without those tools installed.
With `--apple-git-shim`, the driver leaves `/usr/bin/git` selected and sets
`DEVELOPER_DIR` to an absent directory without changing the Mac's global
selection. It checks that review returns `review_unavailable` from the new
resolver and that session management still works. This exercises the real
system-Git selection path rather than substituting a failing Git fixture.

## Native first-run acceptance

The MacBook was logged in. The earlier claim that it needed unlocking was
incorrect: the shared-password VNC viewer had reached its own login screen,
and `/dev/console` ownership alone did not establish the operator session's
state. SSH System Events and own-window `screencapture` both worked using
existing permissions. No screen-sharing preference or privacy grant was changed.

[native-ui.json](native-ui.json) and the [native evidence](native/) record
observed launch, visible attention, terminal input, reopen, app replacement,
and native History resume. Reopen and replacement preserved the exact server
generation, runtime ID, agent PID and process start identity. After replacement,
the bundled CLI hash matches the final candidate, and input still reached the
retained agent. History then resumed the exact conversation into a new runtime.
The first transcript fixture omitted `entrypoint`, so its unknown classification
required Include Automated Sessions; the reusable fixture now emits the real
interactive `entrypoint: cli` marker.

Live setup exposed a missing producer of the workspace host marker: a test
that manually set the marker exercised only its consumer. `workspace-view setup`
now establishes and restores the marker itself. The corrected native summary
was inspected, and the actual command is covered by the packaged driver with
`--workspace-setup` as well as the Go command-boundary test.

The final candidate repeat completed after SSH connectivity returned. Setup had
already saved its disposable project before the interruptions. The restored
completed setup tab showed its closed-view placeholder; closing that view
returned to Launch Project. The native menu launched the fixture, permission
and completion hooks produced their expected states, and typed input reached
the agent. Reopening preserved its server generation, runtime ID, PID and start
identity, with working input. History showed the interactive transcript with
Include Automated Sessions off and resumed its exact ID into a new runtime.
The `native/final-*` records and captures retain this repeat; final cleanup
verified the app, daemon and backend stopped and its own preferences removed.

The combined runs cover:

1. Launch the candidate with no config or running daemon and observe the
   workspace open on Set Up First Project.
2. Complete setup in its embedded terminal, explicitly sync hooks, return,
   and start the daemon from the app.
3. Launch the project from the native menu, observe attention, and type a
   response in the terminal.
4. Quit and reopen the app; verify the same server generation and agent PID
   and working terminal input. End the fixture agent and resume from History.
5. Quit, replace the app bundle with a different build, reopen, and verify a
   running agent survives with its input and recorded identity intact.

As in the existing desktop drivers, an isolated UI copy may have its own
bundle identifier and a CLI shim restoring its disposable HOME after
Ghostty's `login(1)`. Record that instrumentation separately from verification
of the original signed archive. It must not modify the operator's own config,
profiles, sessions, or preferences.

## Publication

No GitHub release existed when work started. Download links in the README
fall back to source installation when no compatible asset is listed. A public
release still needs a committed source revision and both existing release
gates; the notarized working-tree candidate alone does not authorize or
replace those gates.
