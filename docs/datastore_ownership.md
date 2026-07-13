# Datastore ownership

Every command and workflow has one owner for each opened datastore pool. Code
that receives a `*datastore.Store` borrows it and must not close it.

The TUI follows the same rule at process scope. `DataServices` opens one pool,
initializes transcript, plan, workspace, activity, and unified-search services
in a fixed order, and closes the pool only after the Bubble Tea context is
cancelled. Its index facades and repositories are borrowers. Transcript and
plan refreshes share one writer gate; routine workspace projection is read-only
and reuses the immutable catalog produced by explicit synchronization.

Every `Store` also owns a descriptor-backed shared advisory lock at
`agent-mux.db.recovery.lock` for exactly the store lifetime. `Store.Close`
closes the SQLite pool before releasing that lock and joins independent close
errors. The stable lock file is not operator data and remains in place between
opens; descriptor locking, not lock-file creation or deletion, defines
ownership.

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

## Corruption recovery boundary

Automatic replacement begins only after TUI data-service initialization wraps
`SQLITE_CORRUPT`. A failed candidate is closed before the recovery command is
dispatched. `DataServices.writerMu` remains the TUI shutdown barrier around the
operation, so refresh work and `Close` cannot overlap destructive recovery.

The daemon recovery coordinator uses this order when the daemon is live:

1. resolve the currently running executable;
2. request shutdown and wait until the old socket inode is removed or replaced;
3. let daemon server drain finish, close the activity writer and its Store, and
   only then remove the old socket;
4. acquire the datastore's exclusive recovery lock;
5. recheck corruption against a private DB/WAL/SHM snapshot, quarantine the
   authoritative artifacts, create and validate the replacement, then
   downgrade the exclusive descriptor for the validated Store;
6. close the recovering TUI owner and re-exec the dashboard so no process-local
   SQLite WAL/shared-memory mapping can survive the same-path replacement;
7. transfer the one-shot recovery result and any pending daemon restart to the
   fresh process through a consumed-once private handoff;
8. keep the daemon stopped while the fresh TUI imports transcript/plan
   compatibility state and synchronizes workspaces through its process-owned
   Store; and
9. restart the previously live daemon after the rebuild attempt finishes.

If replacement itself fails, the coordinator restarts the daemon before
returning the failure. If re-exec fails, the recovering process invokes its
transferred restart fallback. If the fresh TUI exits before rebuilding,
`DataServices.Close` owns the same deferred restart responsibility.

The private recheck matters because even read-only SQLite inspection can create
or rewrite shared-memory state. Recovery never mutates the authoritative
artifacts before their quarantine renames. The exclusive lock is held through
replacement validation and rollback, preventing any cooperating CLI, daemon,
or TUI pool from retaining a writable handle to the old inode.

The recovery command itself does not import compatibility data. It publishes a
healthy empty schema and releases its validation Store; the fresh process's
ordinary TUI initialization owns transcript/plan compatibility import,
workspace synchronization, and search reconstruction before releasing the
deferred daemon restart. The re-exec boundary is load-bearing: closing Go-level
Store values alone does not guarantee that the SQLite implementation has
dropped process-scoped WAL state for a pathname whose inode was replaced.

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
- No command performs a full integrity scan on ordinary startup. Destructive
  automatic recovery is limited to the failed TUI initialization path and a
  wrapped SQLite primary result code 11; transient failures retain bounded
  retry behavior without acquiring the exclusive recovery lock.
