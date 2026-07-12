# Datastore ownership

Every command and workflow has one owner for each opened datastore pool. Code
that receives a `*datastore.Store` borrows it and must not close it.

The TUI follows the same rule at process scope. `DataServices` opens one pool,
initializes transcript, plan, workspace, activity, and unified-search services
in a fixed order, and closes the pool only after the Bubble Tea context is
cancelled. Its index facades and repositories are borrowers. Transcript and
plan refreshes share one writer gate; routine workspace projection is read-only
and reuses the immutable catalog produced by explicit synchronization.

## Index facades

- `transcript.LoadSQLiteIndex` and `plans.LoadSQLiteIndex` are compatibility
  wrappers. Each opens one store and transfers ownership to the returned
  facade; the caller closes the facade.
- `LoadSQLiteIndexWithStore` and `EnableSQLiteIndexWithMode` borrow their
  supplied store. Closing any number of borrowed facades does not close the
  shared pool.
- `SyncSkip` attaches existing SQLite state without compatibility ingestion.
  Status paths use it. `SyncIfChanged` performs incremental synchronization for
  normal commands. `SyncForce` is reserved for explicit compact/rebuild work.

Incremental synchronization fingerprints each rollback entry from normalized
metadata and sidecar size/mtime. A warm run opens no sidecar body and starts no
write transaction. Changed entries are refreshed atomically with their import
ledger rows. Removing a rollback entry never deletes the durable SQLite row.

## Production opens

- CLI commands in `cmd/index.go`, `cmd/transcripts.go`,
  `cmd/project_commands.go`, and `cmd/search.go` open once at command entry and
  defer that store's close. Every repository and index used by the command
  borrows that store.
- `cmd/doctor.go` intentionally performs one bounded inspection open and closes
  it immediately.
- `internal/handoff.Workflow` and `internal/worktree.Workflow` intentionally
  open one short-lived store per explicit workflow execution, then close it.
- The daemon activity writer owns its worker-lifetime store through the writer
  lifecycle.
- `tui.DataServices` owns one TUI-process pool. Unified search, workspace
  activity, workspace projection, transcript search, and plan search all reuse
  the injected snapshot and never open independent pools.

## Synchronization and read-only boundaries

- Datastore opening serializes the full pending migration sequence. Independent
  daemon, TUI, and CLI processes may open a fresh or older supported database
  concurrently without racing migration inspection against migration writes.
- A binary refuses a database whose schema is newer than it understands. Agent
  Mux never attempts an automatic downgrade.
- `workspace.Service.Synchronize` is the configuration/Git discovery write
  boundary. It persists identities, current memberships, and search projection
  data, then returns an immutable catalog.
- `workspace.ProjectSnapshot` and routine TUI polling project from that catalog.
  They may read activity, conversations, and linked plans but perform no
  datastore writes.
- `amx index status`, `amx transcripts status`, and ordinary `amx doctor` use
  read-only inspection once the schema is current. They do not run compatibility
  synchronization or repair missing search documents. `amx doctor --strict`
  deliberately adds explicit FTS verification.
