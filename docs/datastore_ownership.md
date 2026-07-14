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

Corruption replacement adds a separate rebuild gate at
`agent-mux.db.rebuild`. Its exclusive descriptor survives the dashboard's
re-exec, while the private marker records the owner PID, whether prior daemon
liveness must be restored, and (after successful replacement) a durable pending
rebuild result. Gate publication and daemon startup serialize on the
stable descriptor lock `agent-mux.db.rebuild-startup.lock`. A daemon holds that
lock from before checking the marker until it has answered a health request on
its bound socket. Recovery holds the same lock while publishing the marker, then
checks health only after every earlier startup has either become reachable or
exited. Thus a daemon that wins is stopped before replacement, while a recovery
that wins makes the daemon exit before socket publication or SQLite open. The
daemon activity writer reacquires the same serialization immediately around its
deferred SQLite open so it cannot cross a gate published after daemon startup.
An actively locked marker rejects hook auto-start and explicit starts. An
unlocked ordinary marker left before replacement is crash residue and is
removed on the next check. Once replacement succeeds, the marker is durable:
loss of the advisory descriptor no longer authorizes daemon SQLite access, and
ordinary auto-start leaves the marker intact. The marker pathname is removed
only after the replacement's compatibility state is checkpointed durably. The
stable startup lock file is never removed; descriptor locking, not file
creation, defines ownership.

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
  lifecycle. Its deferred open crosses the daemon-startup serialization lock so
  an already-running worker cannot open SQLite after recovery publishes a gate.
- `tui.DataServices` owns one TUI-process pool. Unified search, workspace
  activity, workspace projection, transcript search, and plan search all reuse
  the injected snapshot and never open independent pools.

## Corruption recovery boundary

Automatic replacement begins only after TUI data-service initialization wraps
`SQLITE_CORRUPT`. A failed candidate is closed before the recovery command is
dispatched. `DataServices.writerMu` remains the TUI shutdown barrier around the
operation, so refresh work and `Close` cannot overlap destructive recovery.

The daemon recovery coordinator uses this order:

1. acquire the stable daemon-startup serialization lock, publish and lock the
   rebuild gate, then release serialization before inspecting daemon health;
   this waits out any earlier in-flight startup and rejects every later one;
2. record whether the daemon was live and, when it was, resolve its executable;
3. request shutdown and wait until the old socket inode is removed or replaced;
4. let daemon server drain finish, close the activity writer and its Store, and
   only then remove the old socket;
5. acquire the datastore's exclusive recovery lock;
6. recheck corruption against a private DB/WAL/SHM snapshot, quarantine the
   authoritative artifacts, create and validate the replacement, then
   downgrade the exclusive descriptor for the validated Store;
7. close the recovering TUI owner and re-exec the dashboard so no process-local
   SQLite WAL/shared-memory mapping can survive the same-path replacement;
8. transfer the one-shot recovery result and rebuild-completion responsibility
   to the fresh process through a consumed-once private handoff;
9. keep every daemon start blocked while the fresh TUI imports transcript/plan
   compatibility state and synchronizes workspaces through its process-owned
   Store;
10. run `PRAGMA wal_checkpoint(TRUNCATE)` so the rebuilt page count is durable
    in the main database before removing the rebuild marker; and
11. restart the daemon only when it was live before recovery.

If replacement itself fails, the coordinator restarts the daemon before
returning the failure because the replacement was never published. After a
successful replacement, rebuild completion is stricter: failed re-exec,
configuration loading, compatibility synchronization, checkpointing, or an
early fresh-TUI exit leaves the rebuild gate owned and the daemon stopped.
Only a fresh TUI initialization that completes the required truncating
checkpoint may consume completion ownership and restore prior daemon liveness.
Completion ownership remains pending until both gate finalization and any
required daemon restart succeed. An initialization retry rebuilds and
checkpoints its fresh candidate again; if gate removal already succeeded, the
process-owned completion resumes at executable resolution and daemon spawn
without trying to remove the now-absent marker or losing its restart intent.
If the re-exec fails or the re-execed process cannot load configuration, process
exit closes the advisory descriptor but leaves the durable marker and restart
intent behind. After correcting the launch or configuration problem, the
operator should run `amx` again. The fresh dashboard claims that unlocked
durable marker under daemon-startup serialization, performs ordinary
compatibility import and workspace synchronization, checkpoints, and only then
removes the marker and restores prior daemon liveness. Operators must not delete
`agent-mux.db.rebuild` merely because its recorded PID has exited; if a fresh
dashboard cannot claim or initialize it, preserve the database, recovery
quarantine, and marker for explicit repair rather than allowing daemon startup.

The private recheck matters because even read-only SQLite inspection can create
or rewrite shared-memory state. Recovery never mutates the authoritative
artifacts before their quarantine renames. The exclusive lock is held through
replacement validation and rollback, preventing any cooperating CLI, daemon,
or TUI pool from retaining a writable handle to the old inode.

The recovery command itself does not import compatibility data. It publishes a
healthy empty schema and releases its validation Store; the fresh process's
ordinary TUI initialization owns transcript/plan compatibility import,
workspace synchronization, search reconstruction, and the final WAL checkpoint
before releasing the rebuild gate. The re-exec boundary is load-bearing: closing Go-level
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
