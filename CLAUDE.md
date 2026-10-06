# CLAUDE.md

Guidance for coding agents working in Agent Mux.

## Project overview

Agent Mux is a Kitty-first, multi-harness session manager for Claude Code,
GitHub Copilot CLI, and OpenAI Codex. The `amx` binary owns:

- a live-session Bubble Tea dashboard with Past Sessions and Plans views;
- exact conversation continuity (focus live, resume closed, native or portable fork);
- isolated managed Git worktrees and deterministic cross-harness handoff packs;
- filesystem-backed transcript and plan caches with disposable search sidecars;
- an always-on local daemon that reduces hook events and exclusively renders Kitty state.

Kitty operations stay behind `internal/kitty` and session/action seams. Domain
services must not invoke `kitten @` directly.

## Build and acceptance

Requires Go 1.22+, Kitty, and `just`.

```bash
just build
just test
just acceptance   # gofmt, tests, core race suites, vet, every TUI scenario
just check        # acceptance plus golangci-lint
just install-restart-smoke
just crash-restart   # fault injection and restart equivalence, under -race
just cockpit-smoke   # real Kitty + installed pair; fails rather than skips
just overseer-integration asl_root=~/scripts   # installed amx + installed asl, isolated world
```

The native menu bar companion lives in `macos/` (Swift, XcodeGen, see
`macos/README.md`). Its recipes need Xcode and are deliberately outside
`check`, `acceptance`, and `paired-release-gate`, which
`TestMacAppRecipesStayOutOfTheGates` in `cmd/release` enforces:

```bash
just mac-app [Release]   # macos/build/Agent Mux.app; Release bundles `just build`'s amx
just mac-test            # hosted AgentMuxTests XCTest suite
just mac-install         # Release build, quit, ditto to /Applications, relaunch
```

Releases of the Agent Mux / agent-session-loop pair go through
`just paired-release-gate asl_root=… signing_key=…`, which runs both
repositories' gates, installs the pair (ASL first), restarts the real daemon,
drives a real Kitty smoke, and writes a signed evidence record. The gate lives
in `internal/release` and is tested in isolation; see
`docs/paired-releases.md`.

Use `amx doctor --strict`, `amx transcripts status`, and `amx --version` when
validating an installed artifact. The CLI and daemon build identities must
match.

## Architecture

### Durable and source data

These are the authoritative source artifacts and must not be rewritten as a
side effect of indexing:

- harness transcript files;
- saved plan directories;
- `config.toml`;
- handoff Markdown artifacts.

`transcripts.json`, `plans.json`, and their search sidecars are disposable
caches. Refresh, rebuild, and corruption quarantine may replace them, but the
source transcript and plan files remain untouched. Cache replacement uses
stable advisory lock sidecars plus atomic file renames.

The default cache paths are `~/.local/share/agent-mux/transcripts.json` and
`plans.json`, with sibling `transcripts.search/` and `plans.search/`
directories. `handoffs/`, `worktrees/`, and `launch_overrides.json` are
independent user assets, not cache internals. Successful handoffs and worktrees
survive launch; compensation may remove only the worktree created by the
failed request.

There is no SQLite runtime or migration path. Never add automatic deletion of
legacy database files. A one-off operator cleanup must stop the installed
daemon first and use the exact allowlist documented in README; it must preserve
caches, handoffs, worktrees, launch overrides, and ordinary daemon locks.

### Main packages

- `cmd`: the root of the CLI and nothing more — `rootCmd` itself, the artifact
  version it reports, `Execute`'s exit mapping, and the assembly that registers
  every command family's leaves under it. It owns no command behavior: every
  user-facing command lives in a child package, which is what lets it remain the
  only package in the `cmd` tree that any other `cmd` package may not import.
  Because assembly is all it does, "did every family reach the user" is the one
  regression it can hide, so `TestCommandFamiliesAreRegisteredUnderRoot` in
  `cmd/root_test.go` is the single root-owned registration assertion rather than
  one such file per family. Cross-family acceptance tests that name no command
  symbol (process lifecycle, diagnostics release, the ASL contract) also stay
  here.
- `cmd/daemon`: the `amx daemon` family — the always-on local daemon seen from
  the CLI side. It owns the daemon's lifecycle as a command (`start`, which
  blocks and is the process the detached launch re-execs, plus `stop` and
  `restart` over the lifecycle client seam), the `status` readiness/build/
  notification/ASL/companion lines, and `export-events`, the investigation snapshot with
  its window resolution, per-window trimming, and schema projection. `audit` is
  registered here but implemented in `cmd/debug`, whose invariant evaluator it
  shares. The family exports exactly one identifier, `Command`, so no daemon
  internal is re-exported through the CLI; everything it needs from the daemon
  it takes from `internal/daemon`, `internal/daemon/ipc`,
  `internal/daemon/eventlog`, and `internal/daemon/protocol`. A file here may
  name the imported `internal/daemon` package unqualified as `daemon`, because
  a package's own name is not in scope inside its files.
- `cmd/dashboard`: the interactive surfaces `amx` opens inside Kitty — the
  live-session dashboard, which is what a bare `amx` runs rather than a
  subcommand of its own, the live session overview behind `amx overview` and its
  F2 binding, and the color, harness-icon, and keybinding overlays.
  What makes them one family is the singleton rule: each asks Kitty whether its
  surface is already open and focuses that window instead of opening a second
  copy, and the dashboard additionally hands the running instance the tab the
  caller came from. The overview is where that rule is strictest: it hands a
  running instance nothing, because it shows every session at once and a repeat
  invocation must not disturb the selection or page the operator left, and it
  reports a Kitty listing or focus failure as an error instead of degrading to
  "nothing is open", because the degraded answer is a second full-screen surface
  polling Kitty beside the first. `amx open-dashboard` is the dashboard's
  out-of-terminal entry point, for a caller (the companion app, a script) that
  wants the dashboard brought forward rather than drawn where it runs: it
  applies the same singleton rule through the shared `focusDashboardIn`, and
  only when no dashboard is running launches one in a new tab, reporting
  `{window_id, tab_id, opened}`. It is strict the way the overview is — a failed
  listing or a failed focus is an error rather than a reason to open a second
  dashboard — and it validates the configuration before launching, because a
  tab that opens only to show a config error and close is worse than refusing.
- `cmd/project`: the `amx project` family — the command surface over configured
  projects, the launch templates `[[projects]]` declares. `list` reports each
  as `{profile, title, harness, directory, groups}`, and `launch` starts a new
  session from one through `actions.Launch` in `actions.DefaultRegistry()`, the
  same definition the dashboard's launcher runs, so the two cannot disagree
  about what launching a project means. A project is identified by its profile
  and title (an exact title wins; failing one, a title matching exactly one
  project once leading emoji/symbols and case are set aside selects it, so
  `amx` reaches `🐱amx`); a title shared by several projects without
  `--profile`, and a selector matching nothing, are refused with ordered
  `profile/title` candidates rather than resolved by a guess. A launch reports
  the new tab's identity only when exactly one new tab carrying the title
  appeared. Neither leaf rewrites `config.toml`.
- `cmd/config`: the `amx config` family — the config file's lifecycle (init,
  edit, show, path, validate, including the `$EDITOR` field splitter and the
  project-directory audit) and `asl-sync`, the ordered preflight-migrate-write-
  restart-verify pairing with agent-session-loop and its structured error codes.
  It registers `cmd/hooks`' two leaves under itself.
- `cmd/hooks`: harness hook files and hook delivery — the `hook-indicator`
  entry point harness hooks call, with its fail-open legacy path and its strict
  75/2 exit mapping, and the `hooks-sync`/`hooks-status` maintenance leaves the
  config family surfaces.
- `cmd/debug`: the daemon-state diagnostic surfaces and the vocabulary they
  share — `amx debug` (status, tail, probe, explain, capture, bundle, replay,
  and `subscribe`, which opens a companion stream exactly as an app does and
  prints each frame, one schema-v1 record per line under `--json`),
  `amx doctor` with its stable finding identifiers and versioned report, the
  invariant evaluator both run, and `amx daemon audit`, which is a doctor
  surface wearing a daemon subcommand name. It also owns the export projections
  (`ExportWindowIDs`, `ExportLiveMetadata`, `ASLStatusLine`) that doctor and the
  daemon command family must agree on, so a doctor cannot report drift that
  `daemon export-events` does not see.
- `cmd/session`: the command surfaces that act on a conversation or on the Kitty
  tab carrying one — the same domain seen from two distances. The `amx session`
  family works on conversation identity (list, search, show, continue,
  fork, fork-worktree, handoff) and always resolves an exact `(harness, session_id)`
  before it acts; `search` runs the same `transcript.Search` Past Sessions
  does, and it and `list` share one set of selector flags;
  the root-level tab controls work on the live instance instead
  (`focus-prev-managed`, `focus-next-managed`, `focus-next`, `mute-toggle`,
  `resort`). Both halves reduce to "which session, and what happens to it".
  `mute-toggle --json` reports `{window_id, muted, changed}` for the companion
  app; a mute whose title write failed is partial durable success. Like
  `cmd/daemon`, a file here may name the imported `internal/session` package
  unqualified as `session`.
- `cmd/worktree`: the `amx worktree` family — listing managed Agent Mux Git
  worktrees, showing one, reopening it as a live session, opening a shell or the
  configured editor in it, and the bounded destructive leaves (`remove` a
  clean inactive worktree, `forget` stale metadata, and `prune`, which selects
  every worktree `remove` would accept and removes each through `remove`
  itself, previewing unless `--yes`). The bounded creation and
  cleanup themselves stay in `internal/worktree`; this is the command surface
  over them, and it may likewise name that import unqualified as `worktree`.
- `cmd/transcripts`: the `amx transcripts` family — the maintenance surface for
  the disposable transcript metadata cache and its search sidecars. Its three
  leaves report what the store holds (`status`), migrate legacy search text and
  drop orphan sidecars (`compact`), and reparse every discoverable source
  transcript from the configured profiles (`rebuild`). The source transcript
  files themselves are never rewritten.
- `cmd/notifications`: the `amx notifications` family — the operator-facing
  surface that sends a real notification through the backend a real one would
  take (the connected companion app via the daemon's `notification_test`, else
  Alerter from the CLI process) and reports which backend confirmed delivery, so a silent daemon can be told
  apart from a muted operating system, and that sets or clears the running
  daemon's in-memory snooze (`snooze 1h|until HH:MM|indefinite|off [--json]`).
  Snooze never auto-starts a daemon, since a snooze held by a daemon that was
  not running would change nothing; `test` sends from the CLI process rather
  than through the daemon, so it asks the daemon for the snooze first and
  reports a snoozed test as suppressed instead of sending past it.
- `cmd/overseer`: the `amx overseer` family — the authorization surface the ASL
  overseer asks before it reads project or conversation content, prepares a
  mutation, or dispatches an action. `policy` decides nothing: it reads the
  operator's current configuration, hands the plan's repository set to
  `internal/overseerpolicy`, and reports what that package answered. Three
  properties are the surface, not incidental to it. Configuration is read on
  every question, with no cached evaluator and no daemon in the path, so
  withdrawing an opt-in takes effect on the next question rather than the next
  restart. An answer is evidence for one question — it carries the configuration
  fingerprint it was decided against and a digest of the request, and no token,
  expiry, or grant — so there is nothing in it to hold and present later as
  permission. And a refusal exits nonzero in both output modes, so a caller that
  branches on the process status alone fails closed. `history` is the one leaf in
  the family that returns conversation content, and it asks that same question
  twice: once for the plan before anything is discovered, and once per
  conversation against the working directory its identity metadata names, which
  must both lie inside an authorized repository and pass the policy evaluation on
  its own. The per-conversation question is decided against configuration
  reloaded at that moment and re-decides the plan's whole repository set, not the
  conversation's directory alone, because authorization is per plan and a walk of
  a corpus takes long enough for an opt-in to be withdrawn during it. A
  withdrawal partway through stops the request: no further conversation is
  opened, and the evidence gathered before it is dropped rather than returned
  beside a refusal, since the client contract is binary and a request half-served
  under a permission that no longer holds is not half-authorized. Configuration
  that becomes unreadable mid-request stops it the same way, as an error rather
  than a decision. It reaches the corpus only through `transcript.History` and never
  through the transcript index or its search sidecars — a refresh has already
  read every message body into a sidecar, so a cache lookup would be a way to
  serve an excluded conversation without asking about it. Its `withheld` entries
  name the harness, the session, and the reason and nothing that locates them,
  because the path, the directory, and the project are what an exclusion is
  about. The two payloads version independently of each other and of the
  envelope: `overseer_policy` and `overseer_history`, each with its own digest
  domain.
- `cmd/release` and `cmd/tuitest`: the two hidden operator/developer commands.
  `release-gate` drives the paired release procedure in `internal/release`;
  `tui-test` drives the in-process TUI scenario harness in `tui`.
- `internal/cli`: the shared bootstrap every command family runs before it does
  its own job — how a failure becomes a process exit status (the `ExitError`
  quiet-exit contract and the nonzero status vocabulary), how a command answers
  a machine caller (the common schema-v1 envelope, the structured error payload,
  and its candidate/parameter help shapes) and a person (the display-width
  aligned table and the terminal check that keeps plain output tab-separated
  for pipes), how a command assembles the runtime
  it acts on (the validated configuration, the Kitty client and the window the
  CLI was invoked from, the daemon export-client seam, the harness transcript
  readers it discovers sources with, and the context its work runs under), and
  how the process
  coordinates with itself (the `--debug`/`AMX_DEBUG` verbosity policy with the
  logger it installs, and locating the running `amx` binary that hook commands
  write and daemon start/restart re-execs). Command-specific flag parsing,
  command construction, and command-specific output stay in the command
  packages: this is not a helpers bag, and an identifier belongs here only when
  more than one command family needs it or when it is the vocabulary that makes
  such a contract nameable. It must never import a `cmd` package, and it reaches
  the daemon through `internal/daemon/ipc` only — the transport, auto-start, and
  detached-spawn policies themselves stay in `internal/daemon/ipc` and
  `internal/daemon/process`.
- `internal/actions`: shared typed action registry used by CLI and TUI.
- `internal/conversation`: canonical `(harness, session_id)` identity.
- `internal/session`: Kitty-backed lifecycle, exact live-owner resolution, and
  harness invocation construction.
- `internal/harness`: adapter-owned fresh/resume/fork argv and capability metadata.
- `internal/transcript`, `internal/plans`: filesystem discovery, metadata caches,
  source-backed search, pruning, and corruption recovery. `internal/transcript`
  additionally holds the metadata admission path (`Candidate`, `Authorizer`,
  `MetadataDiscoverer`, `History`), which is a second and narrower walk of the
  same corpus: it reads conversation identity through per-harness structs that
  have no field for message content, authorizes each conversation against the
  single working directory it names, and only then opens it. The ordinary
  discovery path cannot serve as that boundary — `Discover` streams every message
  body to build the snippet, the count, and the search text, and `Refresh` writes
  that text into a sidecar, so by the time a refresh has decided anything about a
  transcript it has already read all of it. `History` never touches `Index`,
  `LoadIndex`, or a search sidecar, and its signature is the proof. Zero named
  directories and more than one are both refusals, because an unidentifiable
  conversation cannot be shown to be outside an exclusion and a mixed-project one
  could only be sorted out by reading the content the decision gates.
- `internal/cachefile`, `internal/searchstore`: shared atomic cache primitives
  and disposable search-sidecar storage.
- `internal/searchquery`: what a typed search query means — whitespace-split
  words ANDed together, double-quoted phrases kept whole, matched against
  already-lowercased text. The transcript and plan searches count with it, and
  the transcript and plan viewers step between lines holding any of its terms,
  so a result and the matches inside it cannot disagree about the query. It
  also picks the line a result is previewed by (`MatchLine`), entering a long
  line just before a deep match, so both searches show a match the same way.
- `internal/handoff`: bounded deterministic pack creation, private artifacts,
  and cross-harness launch workflow.
- `internal/worktree`: bounded managed Git worktree creation and cleanup.
- `internal/gitstate`: what the checkout in a directory says about itself — a
  branch name and a dirty bit, read through an injectable runner with a
  per-command timeout and held in a bounded TTL cache keyed by absolute
  directory. It decides nothing and reports no errors: a non-repository, a
  missing `git`, a timeout, and a detached HEAD all come back as an empty
  branch, because this state decorates a dashboard row and a row that grew an
  error message would be worse than one that said nothing. The boundary against
  `internal/worktree` is the thing to keep straight, since both produce a branch
  and a dirty bit. `worktree` is AUTHORITATIVE and EXPENSIVE — scoped to the
  managed worktree root, canonicalizing symlinks, checking containment,
  requiring Git registration, and spending four or more git calls plus
  user-visible warnings per entry, because its answers gate destructive
  actions. `gitstate` is CHEAP, UNSCOPED, and ADVISORY: any directory, two
  commands, no warnings. Use `worktree` to decide something and `gitstate` to
  paint a column, never the reverse. Ahead/behind is deliberately out of scope
  and documented as a decision in the package doc, not left as an omission.
  What the daemon does with the result — which directories are worth probing
  and how a result reaches a window without holding `stateMu` across a
  subprocess — stays in `internal/daemon` (`git_state.go`), which drives the
  refresh from the watchdog loop.
- `internal/overseerpolicy`: whether the ASL overseer is authorized to act on
  the repositories of one plan. It reads the same configured projects
  `internal/projectresolve` reads, and the boundary between them is the one to
  keep straight: `projectresolve` answers "what should this row be called" and
  is allowed to pick the most specific match and fall back to a basename,
  because a mislabelled row is cosmetic; `overseerpolicy` answers "may we act
  here" and has no degraded form. It examines every declaration that applies to
  a directory rather than the most specific one, so a nested opt-in cannot carve
  an exception out of an enclosing exclusion; it measures applicability both
  physically and by position inside the repository family, so an exclusion on a
  main checkout reaches the linked worktree that would otherwise bypass it while
  monorepo siblings stay independent; and it refuses on an unresolvable path or
  an unresolved Git identity rather than treating either as "no project here".
  Authorization is per plan, not per directory: every repository a plan
  participates in must be permitted, so one exclusion denies the whole request.
  It decides only — it reads no project or conversation content, and it never
  writes the `asl_overseer` key, which stays an operator edit.
- `internal/daemon`: the orchestration root — daemon construction and lifecycle,
  the sweep/probe/render schedules, reconciliation coordination, and the public
  entry points `Run`, `NewOperationalLogger`, and `NewHandler`/`Handler`. It owns
  the one thing none of its children may: the `Handler` itself, and with it
  `stateMu`, every reducer mutation taken under it, the single renderer writer,
  and every Kitty and IPC call the daemon makes. Its children hold vocabulary,
  pure projections, and caller-locked trackers; the root decides what an
  observation *does* to a window. It imports all twelve daemon subpackages and
  none of them imports it back, which is what keeps the graph acyclic. It also
  owns the companion hub (`companion_hub.go`), which follows the same off-lock
  rule as `git_state.go`: publish paths ring `companionDirty`, a non-blocking
  send on a one-slot channel that is safe under `stateMu`; the hub debounces it
  at 100ms (with a 5s safety tick for clock-driven facts), takes one short copy
  under `stateMu` through `copyStateLocked` — the same copy the state query
  publishes — and projects, hashes, and fans out with no daemon lock held,
  skipping a publish whose digest is unchanged. The `Handler` implements
  `ipc.SubscriptionHandler`: a subscribe registers the sink, replays the latest
  snapshot, and is acknowledged with a 15s heartbeat the hub then sends, and
  health reports the subscribers as they identified themselves. The
  companion's `focus_window` request (`handler_companion.go`) is the one
  companion request that dials Kitty: it accepts only a socket the daemon
  learned itself (a `kittySockets` value, `lastKittyListenOn`, or the snapshot's
  socket), copies that allowlist under `stateMu`, and runs the list, verify,
  tab, and window steps through `notification.FocusKittyWindow` — the same
  helper click-to-focus uses — with the lock released. Its
  `notification_snooze` request sets, clears, or reads the notifier's
  in-memory snooze and rings `companionDirty` on every change; a timed snooze
  that simply runs out reaches subscribers on the 5s safety tick. The hub is
  also the notifier's `AppBackend` (`companion_backend.go`): the role goes to
  the NEWEST subscriber that claimed `notification_backend` with
  `notifications_authorized` and passes to the newest remaining claimant when
  it leaves; a delivery stays bound to the subscriber it was sent to and ends
  when that subscriber does. Its `backendMu` is a leaf lock — safe under
  `stateMu` and `hub.mu`, holding nothing but a send into a buffer that cannot
  fill — and frames are written after it is released. The
  `notification_disposition` request settles a delivery idempotently: a
  repeated `delivered` is ignored, the first terminal disposition wins, a
  terminal one other than `failed` implies delivery, and an unknown
  notification ID is answered ok. `notification_test` offers one test notice to
  the app and never falls back; the CLI tests Alerter itself when no app
  confirmed one.
- `internal/daemon/protocol`: the daemon wire contract — request/response model,
  command names, durable and exported envelopes, and the serialization
  vocabularies. It holds shapes and pure projections only, and must never import
  the daemon root.
- `internal/daemon/state`: the side-effect-free fact reducer and the durable
  per-window state — source facts, effective-state reduction, retention, and the
  amx_state user-var schema with its version migrations. Like protocol it must
  never import the daemon root, and it performs no Kitty, IPC, or notification
  I/O of its own.
- `internal/daemon/notification`: notification settings, the Alerter backend
  and its capability probe, the delivery decision pipeline (gates, permission
  debounce, cooldown), the global snooze (`snooze.go`: in memory only, never
  written to config, gated right after quiet hours and re-checked when a
  debounced permission fires), click-to-focus, and the disposition vocabulary.
  It also owns the decision half of the companion app backend
  (`app_backend.go`): the `AppBackend` seam (`Ready`, `Deliver`, `Retract`) and
  the `AppNotice` a notification frame carries. Backend selection is per
  dispatch, inside `runDispatch` and after every gate, so the app path
  suppresses exactly what the Alerter path does; `backend_unavailable` means
  neither Alerter nor a ready app exists. A ready app is offered the
  notification first and has 3s to report `delivered`; anything short of that
  is withdrawn and recorded as `app_fallback`, and the Alerter path runs as if
  no app existed. After `delivered` the dispatch waits for the interaction up
  to the ordinary dispatch timeout: a click runs the same `focusForClick` an
  Alerter click does, and `action_mute`, `action_snooze`, and `action_answer`
  are recorded the way `replied` is — the app carries them out, the daemon only
  names them. A notice still showing is retracted when a newer notification
  for its window passes every gate (never by one that is suppressed, which
  would leave the operator with nothing), when `CancelPending` or
  `CancelPendingDisposition` says the window moved on, and when the window
  closes. Dispatch and focus dispositions name the backend they came from.
  Like protocol and state it must never import the daemon root; the daemon
  drives it through the `Notifier` seams rather than reaching into its state.
- `internal/daemon/rendering`: the daemon's status presentation — the icon the
  effective state paints, the tab-title text that carries it, the tab's title
  owner, the latest-generation render queue, the presentation cadence, and the
  bounded render-generation traces exported as the renderer's display model. It
  is domain-specific, not a formatting utility package: it must never import the
  daemon root, and it performs no Kitty, IPC, or journal I/O — the daemon root
  keeps the render loops and every Kitty write.
- `internal/daemon/events`: what one hook delivery *is*, before anything decides
  what to do about it — the agent-session-loop wire vocabulary and its
  compatibility gate, payload normalization into the identity-stamped
  `protocol.NormalizedEvent`, the state-update request built from it, per-event
  routing evidence and its outcome/confidence classification, and the durable
  session-event record. Like protocol, state, and rendering it must never import
  the daemon root, and it performs no Kitty, IPC, or journal I/O. Routing
  *decisions* and transport dispatch stay in the daemon root: this package
  supplies the vocabulary and the evidence, the root chooses and acts.
- `internal/daemon/activity`: whether a window is working or resting, and the
  evidence that settles the question — the legacy action guard, the
  stabilization-sample and screen-decline trackers with their per-(harness,tier)
  sample policy, the Claude title and Codex prompt classifiers, the per-sweep
  screen-text cache, authoritative-idle naming and probe eligibility, foreground
  harness classification, and the stale/hook-silence thresholds. The trackers are
  deliberately CALLER-LOCKED and zero-value ready: the daemon mutates a streak in
  the same critical section as the reducer state it is about to settle, so an
  internal mutex would hide an atomicity requirement rather than satisfy it. Like
  protocol, state, rendering, and events it must never import the daemon root,
  and it performs no Kitty, IPC, or journal I/O — deciding what an idle
  observation DOES to a window stays in the root, which owns the reducer, the
  renderer, and the notifier.
- `internal/daemon/probes`: the daemon's external-evidence layer — how it looks
  at the world outside its own hook stream, and how it decides that a look is
  still valid. Two kinds of evidence live here: the OS process table (the
  `ps` PID/lstart harness-liveness inspector, its bounded batches, and its
  cross-platform absent-process fallback) and the Kitty window tree (the
  immutable snapshot cache that keeps `ListWindows` off the hook path, the
  derived per-tab and per-pane freshness epochs, and the producer/source/target
  probe binding with its receipt and epoch validation). Like protocol, state,
  rendering, events, and activity it must never import the daemon root, and it
  performs no Kitty, IPC, or journal I/O of its own — it interprets a captured
  tree rather than fetching one. Deciding what an observation MEANS for a window
  stays in the root, which owns the reducer, the renderer, the notifier, and
  every harness probe adapter. Process launch and control belong to
  `internal/daemon/process`, not here.
- `internal/daemon/process`: the daemon's own life as an operating-system
  process — how a new daemon is launched (`amx daemon start` in a fresh session
  with the platform detach attributes), how it is cut loose from the terminal
  that launched it (stdio redirected into the private bootstrap log, and the
  `AMX_DAEMON_DETACHED`/`AMX_DAEMON_LAUNCH_ID`/`AMX_DAEMON_BOOTSTRAP_LOG` launch
  handshake the child reads back), how exactly one daemon is allowed to own a
  socket (the flock single-instance lock and the client-side auto-start lock),
  and when a running daemon has been superseded by a newer `amx` binary. It is
  the counterpart of `internal/daemon/probes`: probes observes processes the
  daemon did not start, this package starts and owns the one it did. Like
  protocol, state, rendering, events, activity, and probes it must never import
  the daemon root, and it holds no IPC transport and no process-table
  inspection — the retry/deadline path around a spawn stays with the client, and
  reconciling an observed harness exit into window state stays in the root with
  the reducer.
- `internal/daemon/asl`: what the daemon knows about an agent-session-loop run,
  and how it reads it. Two directions of evidence meet here. Outbound, the
  optional bridge subprocess seam, its capability block, and the four
  snapshot/question/action/workbench request handlers, plus the client-side
  decoding of their replies. Inbound, the durable per-run state ASL publishes:
  the projection phase a discovered run is in, the supervisor mailbox markers
  that say a human is being waited on and the closure markers that say they are
  not, the per-run monotonic generation/sequence position that decides whether a
  read is stale, the lease-evidence rule that keeps one unreadable plan from
  retiring a healthy run's lease, and the reconciliation and hook-delivery
  counters that make a silent pairing legible. Like protocol, state, rendering,
  events, activity, probes, and process it must never import the daemon root,
  and it performs no Kitty, IPC, or journal I/O. It does not own what an ASL
  observation *does* to a window: seeding a lease, retiring one, raising the
  question icon, and notifying about it are reducer mutations under stateMu and
  stay in the root. Generic hook decoding likewise stays in
  `internal/daemon/events`, which normalizes an ASL lifecycle event alongside
  every other hook.
- `internal/daemon/eventlog`: the daemon's record of what happened — the
  bounded per-window ring of accepted, suppressed, and daemon-synthesized
  rows, the global monotonic receipt cursor that totally orders them and the
  reference/request lookups that read a row back by it, the durable per-window
  counters and the deep-copied read projections served to session detail and
  event export, the render-completion and probe-trace correlation stamped back
  onto the rows that caused them, and the `daemon.decision` journal record —
  with its `replay.*` and `route.*` evidence — that outlives the ring and that
  `internal/debugger` decodes offline. The boundary is the row, not the
  decision: like protocol, state, rendering, events, activity, probes, process,
  and asl it must never import the daemon root, and it performs no Kitty, IPC,
  or journal I/O — it builds the record the root appends, and every reducer,
  renderer, and notifier mutation under stateMu stays in the root. It
  deliberately does not absorb the tail, cursor, and replay services in
  `internal/debugger`, which read immutable diagnostics artifacts with no
  daemon running.
- `internal/daemon/ipc`: how a request reaches the daemon and how an answer
  gets back — the Unix-socket server with its owner-only 0600 bind and
  drain-on-close lifecycle, the newline-delimited JSON framing with its request
  and response size ceilings and bad-payload throttling, the bounded
  single-consumer ingress queue that serializes application and degrades inline
  under saturation, the request-ID dedupe cache that makes a retried delivery
  at-most-once, the transport's own request-outcome and queue-latency counters,
  and the whole client side: the socket path, the send deadlines, daemon
  auto-start and restart, the typed health, event-export, and ASL calls, and
  the subscribe client (`subscription_client.go`), which reads frames until a
  bye, EOF, or cancellation of its context. A
  connection carries exactly one request/response pair and there is no
  multiplexing, so a second answer means a second connection — except
  `subscribe`, whose ack opens a stream of server-to-client frames on the same
  connection (`subscription.go`). That stream is one-directional, bounded, and
  coalesced, and never carries a second request: client lines after the
  subscribe are read only to see EOF. A `StreamSink` keeps the latest snapshot
  in a single slot that the writer drains before a 64-frame queue, so a newer
  snapshot replaces an unwritten one; a full queue or a frame write exceeding
  its 2s deadline ends the subscriber as a slow consumer, with a best-effort
  `bye`; a frame over `protocol.CompanionFrameMaxBytes` is dropped and counted;
  and a ninth concurrent subscriber is refused with `subscriber_limit`. `Close`
  ends every live stream with `bye{shutdown}` before it waits on connections,
  because a stream never ends on its own and would otherwise hold the drain
  forever. Streams are offered through the optional `SubscriptionHandler`
  interface; a handler that does not implement it answers subscribe as an
  unknown request type. It sees its application only as the three-method
  `RequestHandler` seam, which is what keeps it — like protocol, state, rendering, events,
  activity, probes, process, asl, and eventlog — from ever importing the daemon
  root. It dispatches a request to the application and routes it onto the
  serialized or the inline path; naming the concrete behavior a request type
  means — and every reducer, renderer, and notifier mutation under stateMu —
  stays in the root, which implements `RequestHandler`.
- `internal/daemon/companion`: what a companion — the native menu bar app, or
  any other long-lived subscriber — is shown. `Project` is one pure function
  from values the caller already copied (the per-window state the state query
  publishes, the per-window Kitty sockets, the cached window tree and the
  socket it was read through, the notifier's posture, and the ASL supervisor
  targets and pending questions) to the versioned
  `protocol.CompanionSnapshot`, and `Digest` is that snapshot's content
  identity. It reads no clock of its own, performs no Kitty, IPC, filesystem,
  or journal I/O, and — like protocol, state, rendering, events, activity,
  probes, process, asl, eventlog, and ipc — must never import the daemon root,
  so the hub can build a snapshot outside `stateMu` from a copy taken under it;
  its own tests assert the import list. Three rules are the package rather than
  details of it. A daemon state is joined to a tree window only when both are
  provably about the same Kitty instance — window IDs mean something only
  inside the instance that issued them — and a state recorded against another
  instance is published as its own tree-less session rather than decorating a
  window that merely shares its number; when the daemon also read that other
  instance's tree (the closed-window GC lists each instance a stored window
  belongs to), the session takes its tab and title from it. The attention bit is
  `protocol.AttentionStates` over the daemon's icon (falling back to the
  title's) or Kitty's own flag, the same set `amx focus-next` cycles on, so the
  menu bar and the CLI cannot disagree about which session is waiting. And the
  snapshot carries timestamps, never ages, so projecting an unchanged world
  twice is byte-identical and the hub can skip a publish; truncation at
  `CompanionSessionCap`, and a measured byte budget under the 1 MiB frame cap,
  drop idle and working rows before any row a human is waited on. ASL waiting
  entries name a run and its latest question and never carry a question body
  or an action token.
- `internal/diagnostics`: private fail-open causal journals, operational and
  native logs, temporary raw captures, and bounded diagnostic storage.
- `internal/debugger`: offline-first status, query, explanation, bundle, and
  replay services shared by debug, doctor, and hook-status surfaces.
- `internal/release`: the paired ASL/Agent Mux release gate — ordered step plan,
  artifact hashing, deterministic signed evidence records, and annotated pair
  tags. Every external command goes through an injectable runner and time comes
  from an injectable clock, which is what keeps the gate testable without
  touching the live installation.
- `tui`: the Bubble Tea program itself — the `Model`, the mode dispatcher, every
  key handler, the update loop that consumes whatever an effect reports back,
  the shared theme, one projection seam file per leaf
  (`tui/dashboard_view.go`, `tui/history_view.go`, `tui/launch_view.go`,
  `tui/plans_view.go`, `tui/worktrees_view.go`, `tui/asl_view.go`, and
  `tui/command_palette.go`, which carries the palette's controller and its
  projection seam in one file), and the deterministic in-process uitest harness
  that `cmd/tuitest/tuitest.go` drives. Command and message production is
  divided rather than owned outright. The effects the dashboard starts away from
  the update loop — the live-topology refresh, daemon queries, harness
  lifecycle, clipboard, directory and editor launches, health probes,
  agent-session-loop discovery — live in `tui/commands`
  (`tui/commands/session.go` and its siblings) together with the shared result
  messages they report back with (`tui/commands/message.go`). The root keeps the
  command and message pairs that close over its own model state: the transcript
  and plan cache loads and debounced searches, worktree loads and mutations, the
  ASL snapshot/question/action round trips, the preview and toast tickers, and
  launch progress. Visibility is the line to read: every message type in
  `tui/commands` is exported because it crosses back into the root's update
  loop, and every message type the root declares is unexported because it never
  leaves the loop that raised it. It is the only package in the TUI tree that
  names `Model`: the nine leaves below take explicit `*Input` values and return
  strings, so vocabulary and pure rendering move out while
  what-a-keypress-*does* stays in. The uitest files cannot become a child
  package because `cmd` reaches them through the root. The whole TUI tree — root
  included — depends on `internal/daemon/ipc`, `internal/daemon/process`, and
  `internal/daemon/protocol` only; `internal/daemon` itself is not in its
  transitive imports at all.
- `tui/layout`: terminal geometry, and nothing else — the visible-window slice
  of a flat list, wrap/clamp width math, grapheme-aware display width,
  truncation and padding, and the overlay compositor that splices a popup into
  a base view. It also holds the model-independent row and detail primitives a
  selectable list is built from: greedy word wrapping with a rune-safe hard
  break, the label-plus-shortcut row that concatenates to exactly its budget,
  the optionally labelled band rule, and the fixed-height detail block that
  marks what it had to clip. They live here rather than beside either caller
  because `tui/commands` and `tui/sessionactions` both draw with them and the
  dependency runs one way between those two; the test is that none of them
  names a model, a session, or an action. It is the one leaf every other leaf
  imports and the only package in the repository's TUI tree with no in-repo
  dependency whatsoever.
- `tui/dashboard`: the live-session list — the filter DSL with its removable
  chips and completion menu, row/title/activity formatting, group borders and
  the state gloss, cursor restoration across a rebuilt list, pagination, the
  session inspector (its fixed metadata block, its five output-content states,
  its family-card form over a summary, and the pure output viewport that holds a
  snapshot while scrolled back), and profile/harness color assignment (which
  `history`, `launch`, and the root all read).
- `tui/history`: the two histories that exist here — the per-session event
  detail overlay with its phase timeline, and Past Sessions transcript history
  (search rows, the transcript viewer, the full-message overlay, the delete
  prompt). There is no archive/restore flow.
- `tui/launch`: the launcher list and its preview, filter/profile-cycle/pin
  controls, the profile-override overlay, persisted launcher preferences, the
  `ConfigPickerPurpose` vocabulary, and launch batch targets and progress
  wording. The root keeps the `tea.Cmd` builders that actually launch.
- `tui/plans`: the Plans browser — listing and sort order, search result rows,
  and the viewer's wrapped rows with their source-line mapping.
- `tui/worktrees`: managed-worktree rows — state classification and badges,
  compact age, source labels, filter, sort, and durable mutation summaries.
- `tui/asl`: the ASL Runs surface — run rows and their sort, exact and fallback
  session binding, the fixed refusal sentences mapped from bridge codes, the
  bounded operation-receipt history, and the response form's state with its
  `String`/`GoString` redaction contract, which is load-bearing because the
  fields are exported.
- `tui/commands`: command and key-action definitions, parameterized over the
  root model with generics (`Command[M]`, `Action[M]`) so the leaf never names
  `Model` yet the palette can still ask a live model whether a command is
  visible and available — availability is re-derived at render time, not frozen
  when the palette opens. It also owns the palette's matching, ordering, layout
  and painting, and the effect layer the root half of the division above hands
  off to: the model-free command builders that run work outside the update loop
  and the exported messages those effects come back as. What a command *does* to
  the model stays in the root, which supplies every catalog entry's closure. It
  additionally owns the dashboard refresh command cycle and the package-level
  seams root tests swap (`Registry`, `DaemonRequest`, `StateQuery`,
  `SpawnDaemonDetached`, `CopyToClipboard`, `ActiveLoopDiscover`). Its live
  topology is daemon-first: a refresh takes the window tree from the daemon's
  published snapshot whenever that snapshot is present, ready, and attributed to
  the same Kitty instance the dashboard runs inside — including when the daemon
  has marked it stale — and lists Kitty itself only when the daemon is
  unreachable, rejects the query, has never completed a list, predates the
  snapshot block, or supplies a tree whose instance identity is missing on
  either side or known to differ. That same instance proof gates the daemon's
  per-window state, activity, and watchdog maps, which are keyed by bare window
  IDs that name a window only inside the instance they came from: a refresh that
  cannot prove the instance renders the state and loop markers parsed from live
  Kitty titles instead, exactly as it does with no daemon at all. A stale tree is
  still rendered but drives no Kitty write, because the window IDs in it may
  already be gone. It also owns the read-only retrieval boundary:
  `RefreshOverview` is `Refresh` minus its write budget, and the two share the
  unexported `readLiveSessions` so the shape of the topology cannot drift between
  them. The distinction is not an optimization. `Refresh` is allowed to spend a
  bounded number of Kitty writes reconciling what it found — including consuming
  the one-time `amx_dashboard_select` request — and only the dashboard may do
  that, because that request names a selection exactly once and a second surface
  reading it would swallow it. `RefreshOverview` therefore performs no Kitty
  write of any kind, and the guarantee is tested from both sides: the overview's
  recording client records a violation for every write method, and a dedicated
  test asserts that an overview retrieval leaves a pending selection request
  intact for the dashboard to consume afterwards.
- `tui/sessionactions`: what a session action IS, declared once for the two
  dashboard entry points that offer it — the per-row actions overlay and the
  command palette. It holds the stable `commands.ID` identity, both surfaces'
  wordings side by side (the overlay's label and one-line description, the
  palette's title, description, and aliases) rather than merged into one, the
  mnemonic, the menu band, and the availability rule that decides whether the
  action may run against a row. Keeping the two wordings in one declaration is
  the point: the surfaces are allowed to read differently, but they must not be
  allowed to disagree about which actions exist or when each one is permitted,
  which is what two tables of rules had let them do. It also owns the captured
  `Target` — a row snapshot, from which tab and window identity are read rather
  than stored beside it, so an action cannot be retargeted by a topology refresh
  between selection and dispatch — and the `Result` contract carrying action
  identity, request identity, and an outcome that keeps a refusal distinct from a
  failure. Two presentation answers live here for the same reason the wordings
  do. The menu's initial selection is declared rather than left as index zero,
  so opening the menu and pressing enter does what pressing enter on the row
  did before there was a menu. And the destructive confirmation's rendering
  (`prompt.go`) is here rather than in the root, because the prompt is the
  wording an action already decided: the host passes the `ConfirmPrompt` the
  controller produced and a subject, and gets back a box. Its consequence
  sentence is WRAPPED, never truncated — half a sentence about what saying yes
  destroys is a different warning rather than a shorter one, and the operator
  answers y or n to whichever one they read. It names no host model and imports
  neither `tui` nor any surface above it, so the dependency direction stays
  `tui` → `tui/sessionactions` →
  `tui/commands` → `tui/dashboard`. What an action DOES stays in the root, which
  supplies the runner and the executor for each identity; a definition the root
  has no implementation for is dropped rather than rendered, and the root's
  `TestOverlayOffersEveryMenuDefinition` and
  `TestPaletteOffersEverySessionDefinition` are what report it.
- `tui/overview`: the live session overview — the standalone Bubble Tea program
  `amx overview` and `F2` open, showing every managed session at once as a grid
  of live pane previews. Like `tui/customization` it is a sibling of the root
  rather than one of its leaves: `cmd/dashboard` runs it directly and root `tui`
  does not import it. It reaches the world only through an unexported `deps`
  struct of function values — the topology read, the pane capture and its own
  cadence, the interval tick, the daemon-held compact preference, the focus
  call, the session-action dispatch, the close-recovery relaunch, and the
  clock — which is what lets the package-level acceptance sequences drive the
  real `Update`/`View` loop against a frozen clock, scripted snapshots, and a
  recording Kitty client with no sleeps anywhere. Three properties are the
  package rather than details of it. Its reads are BOUNDED, FAIR, and
  RECOVERABLE — at most four captures in flight and one per window, queued FIFO
  and coalesced, a request ID releasing the slot so a lost reply cannot leak one,
  and a generation gating what may be painted so a late reply cannot repaint a
  grid that has since been repaged or resized. Its retrieval is READ-ONLY: it
  takes topology through `commands.RefreshOverview` and never `commands.Refresh`,
  so it cannot consume the dashboard's pending selection request, and startup,
  every refresh, every navigation key, the `?` help overlay, and browsing the `a`
  actions menu write nothing to Kitty at all — `c` is no exception either, since
  it saves only the daemon's in-memory compact preference. Every Kitty MUTATION
  is therefore one the operator explicitly asked for: the focus call `Enter`, a
  digit, and the menu's Open entry share, and the twelve session actions the
  menu dispatches under the same declarations, availability rules, and
  `confirm_destructive` prompts the dashboard reads from `tui/sessionactions`.
  The feedback row is where those land, and it names every refusal and every
  failure — including for the actions that announce nothing on the way out,
  because a grid that silently declined to do what was asked is the one outcome
  an operator cannot see. And a close made here is recoverable here — `u`
  relaunches a CONFIRMED close for five seconds, from a recovery stack local to
  this program, and the tab the overview is itself running in is never offered,
  because closing it takes the grid and the offer with it. Its compact
  three-line tile header (`dashboard.CompactMetaLines`) is deliberately a strict
  subset of the inspector's six facts — branch, directory, and the
  supervision/lock/mute line stay in the inspector, because a tile that grew
  them would be an inspector rendered nine times rather than an overview.
- `tui/customization`: the standalone color, icon, and keybinding overlays, each
  its own Bubble Tea program. It is a sibling of the root rather than one of its
  leaves: `cmd` launches it directly and root `tui` does not import it.

### Identity and safety rules

- Conversation identity is `(harness, session_id)`; live instance identity is
  the freshly validated Kitty window/title owner.
- Configured projects are launch templates identified by profile and title;
  continuity actions use exact transcript identities.
- Ambiguous selectors return ordered candidates and never guess.
- Handoff/worktree mutations require explicit confirmation where
  exposed by the command/action contract.
- Hook acknowledgement remains prompt-free and does not perform cache refreshes.

## Configuration

Default path: `~/.config/agent-mux/config.toml` (override with `AMX_CONFIG`).
Canonical sections are `[[profiles]]` and `[[projects]]`; `[[accounts]]` and
`[[sessions]]` remain deprecated aliases for compatibility.

```toml
[defaults]
sort = "alpha"
refresh_interval = 2
guard_duration = 1
plans_directory = "~/.local/share/agent-plans"
git_probe = true   # per-directory branch/dirty probe; false stops running git

[defaults.handoff]
max_bytes = 65536
recent_messages = 12

[[profiles]]
name = "codex-work"
harness = "codex"
home_dir = "~/.codex-work"
color = "purple"

[[projects]]
title = "API"
directory = "~/projects/api"
profile = "codex-work"
```

Profile names are launch identities. Harness-specific args belong on a profile
or project, not in shared defaults. The full commented template is
`embed/example_config.toml`.

## Command and JSON conventions

Retained continuity commands live under `session`. Successful `--json` output uses
`{"schema_version":1,"data":...}`. Failures use the common structured error
envelope; partial durable success returns data with a nonzero quiet exit.
`doctor --json` is a versioned diagnostic report with the same top-level schema
version but intentionally includes health fields directly. The `debug` report
families likewise emit command-specific reports directly rather than using the
common `data` wrapper: `status`, `tail`, `explain`, and `bundle` use schema v2,
while `probe`, `capture`, and `replay` remain on schema v1. `debug tail
--follow --json` emits one schema-v2 record wrapper per JSON line. The hidden
`release-gate --json` likewise emits its evidence record directly: the record is
the release artifact and carries its own `schema_version`, so wrapping it would
put a second, unrelated version number in front of the one that governs it.
`overseer policy --json` and `overseer history --json` go the other way: they
keep the common `data` wrapper and version their own capability contract
*inside* it, in a `contract` block naming the contract, its version, and the
access categories it accepts. The authorization surface has to be able to move
without moving a version number that governs commands with nothing to do with
authorization, and it is likewise independent of the ASL bridge protocol. The
two contracts (`overseer_policy` and `overseer_history`) version independently of
each other for the same reason. Their refusals are decisions rather than errors,
so a refusal returns the data envelope with a nonzero quiet exit; only a
malformed request or unreadable configuration uses the error envelope.

When adding a command:

1. provide dependency seams for filesystem, daemon, and Kitty work;
2. preserve text and JSON exit-code consistency;
3. use `missingParameter`, `commandFailure`, and candidate-bearing ambiguity errors;
4. add the command to the JSON surface audit when it supports `--json`.

## Testing and diagnostics

TUI scenarios are isolated from the operator's config, data, daemon, and Kitty.
Add narrow and wide scenarios for layout changes and run:

```bash
go run . tui-test run docs/tui-testing/scenarios/*.json
```

The acceptance map is in `docs/acceptance.md`. Operational troubleshooting
starts with:

```bash
amx doctor --strict
amx transcripts status
amx daemon export-events --current-window --last 50
```

Release acceptance must keep explicit coverage for warm Past Sessions,
partial transcript/plan failures, CLI and TUI continuity, handoff/worktree
retention and compensation, and the absence of retired database artifacts from
TUI, daemon, doctor, and session-command paths. Every JSON file in
`docs/tui-testing/scenarios/` is both discovered by `just tui-scenarios` and
executed in-package; keep the documented scenario inventory exact.

`amx doctor` must retain the existing daemon/reducer/renderer invariants when
new checks are added. Stable finding identifiers are JSON consumer contracts.

## Adding a harness

1. Implement `harness.Impl` and invocation capabilities in `internal/harness`.
2. Register the adapter and hook contract.
3. Add transcript reader fixtures and config coverage.
4. Extend fresh/resume/fork cross-harness tests, explicitly representing
   unsupported native fork capability.
5. Add the screen adapter in `internal/screenstate`, capture-first. This step is
   not optional for a harness whose transcript cannot report that a pane is
   waiting on a human — a permission prompt is not a transcript event, so on
   Copilot the screen is the only evidence such a pane exists at all.

   Take the captures *before* writing a single pattern, with
   `kitten @ get-text --extent=screen` against a live pane of the harness, and
   commit them byte-for-byte under `internal/screenstate/testdata/`. They are
   never hand-edited and never adapted from another harness's file. Cover at
   minimum a working screen, a finished turn, a bare composer, a blocking
   prompt, an interrupted turn, and a composer holding unsent input, plus a
   `_t0`/`_t1` pair each for working and idle so the Layer B fingerprint claim
   is tested rather than assumed. Record the harness build in the test file's
   header comment: the version identifies which capture must be re-taken when a
   release moves the copy.

   Do not borrow patterns from an existing set. All three provisional Copilot
   clauses, written by analogy to Claude and Codex, were wrong in kind rather
   than mis-tuned — the composer had no prompt indicator at all, the status line
   had neither the assumed ellipsis nor the assumed timer, and the interrupt
   hint was spelled without its preposition. Each would have failed silently.
   Expect the discriminator to be a *shape* rather than a glyph or a word:
   Copilot's `●` is simultaneously a spinner frame and the bullet it prefixes
   ordinary output with, so only the footer's column geometry separates a live
   turn from finished prose. Write down the accepted cost of every such choice
   in the TOML, and check that it falls on the fail-closed side — a withheld
   reading, never a granted one.

   Then assert every committed capture. Nothing in this package discovers
   fixtures: a file dropped into `testdata/` that no test names is read by zero
   tests and passes the suite silently, which is the one failure mode the
   capture rule exists to prevent. Give each capture a row stating its state,
   tier, and veto set, and add a named test for each trap a capture was taken to
   pose — including a guard that the capture still *contains* that trap, so a
   later re-capture cannot quietly turn the assertion vacuous.

   Finally, list every clause that is a sole veto source in the adapter's
   `Requirements` so an override that drops it is refused at install time rather
   than degrading to "no match" on a live pane.
6. Run `just check`.

Version is the constant in `cmd/root.go`; update it with `just bump VERSION`.
