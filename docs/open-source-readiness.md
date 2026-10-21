# Open-source readiness

Initial review: 2026-10-21, based on revision `3bb5637a` and the repository's
GitHub settings. This is a publication checklist, not a release certification.

## Before making the repository public

- [x] Choose a license and add a root `LICENSE`: MIT, selected by the
  maintainer. The desktop build includes it; existing third-party notices remain.
- [ ] Review redistribution of the ASL contract corpus under
  `internal/aslcontract/`, including its source repository's licensing.
- [ ] Review the committed acceptance evidence for publication. There are 128
  PNG screenshots and 49 text files containing the maintainer's home path.
  Other records include signing identities and local machine details. These
  are privacy review items, not automatically credentials.
- [ ] Decide whether to publish the existing history or a clean export after
  that review. Deleting a file in a new commit does not remove earlier copies.
  Do not rewrite the existing repository as part of routine cleanup.
- [ ] Enable and run GitHub CI. The `CI` workflow was `disabled_manually` at
  review time; successful Dependency Graph runs do not validate the project.
- [x] Make the cross-repository CI job usable by forks. The private comparison
  now runs only on trusted upstream events, excluding Dependabot. All
  contributors still run the ordinary suite and its offline contract checks.
- [x] Explicitly install tmux in the macOS full CI lane, so integration tests
  do not depend on whether the runner image happens to include it.
- [ ] Enable and verify a private vulnerability reporting channel. Its GitHub
  API endpoint returned 404 during review; availability is unconfirmed.
- [ ] Add a short repository description and topics on GitHub.
- [ ] Review the final diff and explicitly change repository visibility.

## Prepared locally

- [x] Contributor guide with actual build and test recipes.
- [x] Security reporting guidance, including a fallback when GitHub private
  reporting is unavailable.
- [x] Bug, feature, and pull request templates.
- [x] README source checkout instructions and links to contribution guidance.
- [x] Local validation: `actionlint` on the workflow, Go contract/release tests,
  and a native app build that includes `Contents/Resources/LICENSE`.
- [x] Initial Gitleaks history scan: 1,261 commits, 16 findings. Inspection
  identified two explicit diagnostic canaries, twelve synthetic fixture action
  tokens, and two fixture digests. No confirmed credential was found by that
  scan. This does not cover the contents of screenshots or prove the absence
  of secrets. Raw redacted scanner reports remain outside the repository.
- [x] Local OCR pass over all 128 currently tracked PNG images, with no OCR
  errors. Pattern checks found no recognized credential-shaped strings,
  email addresses, private-network addresses, or `/Users/` paths. Five images
  yielded no text. This is a screening pass, not a complete visual review,
  and does not cover images deleted from older commits. OCR output remains
  outside the repository.

## Desktop demo

The [local demo tooling](../scripts/demo/README.md) prepares three isolated
sessions and includes a compiled window recorder. Its headless smoke check
verified the three initial states, an approval transition, and shutdown of
both the demo daemon and tmux server. The verified 46-second recording ran
on the maintainer's MacBook Pro over SSH. All nine foreground/view checks
passed, all three managed hook files were in sync, and the approval changed
the documentation session from waiting to completed. The remote demo app,
sessions, and daemon were stopped afterward. The driving checkout has a
local UI block.

Scene frames were visually reviewed; OCR checked all 230 source frames with
no recognized personal-path, account, or credential-pattern findings. The
animation, video, and still are in `docs/media/`, with the GIF embedded in
the README. Raw captures and scan reports remain outside the repository.

- [x] Ask which physical Mac to use before recording. The maintainer selected
  the MacBook Pro over SSH. Recording must never run in GitHub Actions.
- [x] Run the actual desktop app with an isolated HOME, daemon, private tmux
  server, preferences domain, and synthetic project data.
- [x] Record a short walkthrough: grouped sessions → switch to another agent
  → session needing attention → dashboard/overview → checkout changes.
- [x] Use deterministic stand-in agent output and label it as demo data;
  verify the app interactions and state changes rather than presenting a
  designed mockup as a recording.
- [x] Capture only the demo app, inspect the frames for private information,
  and generate a compact animated README preview plus a readable still image.
- [x] Prepare automation and reproduction instructions alongside the media.
- [x] Embed the verified recording near the top of the README with useful alt
  text and a description of the workflow shown.
- [ ] Review and commit the prepared changes before publication.

Existing automation starts in
[`session-15/driver/README.md`](acceptance/ghostty-desktop/session-15/driver/README.md).
It is an acceptance driver with machine-specific assumptions; adapt its
isolation and input mechanisms for a public demo instead of running it as-is.

## Binary distribution, after source publication

No GitHub releases were listed during review. Source publication can precede
downloadable binaries.

- [ ] Review third-party notices for the compiled Go CLI as well as the
  already-bundled Ghostty/tmux notices; include required notices in artifacts.
- [ ] Choose the supported desktop architectures and verify clean builds.
- [ ] Complete the separate [desktop release gate](desktop-releases.md).
- [ ] Sign with Developer ID and notarize before advertising a broadly
  installable macOS download. Existing trial documentation records ad-hoc
  signing, with notarization unobserved.
- [ ] Publish versioned artifacts with checksums, release notes, requirements,
  and installation instructions.
- [ ] Complete the [daily-driver trial](daily-driver-trial.md) before changing
  the existing unset-backend compatibility behavior.
