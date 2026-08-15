---
status: draft
created: 2026-08-15
owner: cfl
---

# Archived Resource Safety

## Why

Archived tasks and interrupted cleanup leave durable runtime, environment, Git,
and filesystem references. Operators need Kandev to reconcile obsolete database
ownership without losing local work, while every physical deletion path remains
provably fail-closed until a separately authorized executor exists.

## What

- Every managed-root remove, rename, quarantine, restore, purge, worktree remove,
  recreate, and fallback deletion passes through one central admission service.
- Missing admission, incomplete inventory, unknown resource state, path drift,
  generation drift, concurrent ownership growth, or an unavailable executor
  denies the action before Git, filesystem, runtime, or database mutation.
- This release keeps managed-root physical execution unavailable. Successful
  reconcile operations are DB-only and report `physical_retained=true` and
  `physical_removed=false`.
- The existing `task_resource_cleanup_jobs` table is the sole durable ledger.
  Reconcile, retained, blocked, release, and environment-retirement state do not
  create a second cleanup ledger.
- A retained anchor preserves the immutable snapshot, digest, operation identity,
  managed root, resource identity, association generations, branch OIDs, and
  physical-path metadata after task/session/workspace cleanup.
- Single-target v2 and complete shared-target v3 reconcile atomically tombstone
  only the exact active association generation captured by admission. A stale
  job never applies to a reactivated or later generation.
- Interrupted running v2/v3 jobs recover the same claimed attempt under the full
  participant lock set. Deterministic zero-effect conflicts may become blocked;
  transport, commit, mixed-state, or readback uncertainty remains
  `OUTCOME_UNKNOWN` and is never presented as retry-safe.
- Unarchive, reconcile, lifecycle creation, storage maintenance, Office GC,
  Git worktree management, task/session/workspace cleanup, and startup recovery
  share the same ownership and lock contracts.
- A pristine stale pending move may be cancelled only by exact generation-bound,
  canonical DB-only request. The retained anchor and all non-target rows remain
  byte-equivalent.
- A retained target whose physical path and Git registration are already absent
  may transition to released only through the sealed absent-target admission.
  Release does not delete files or branches.
- A stopped or failed task environment whose exact historical repository
  reference is no longer needed may be retired DB-only after all task,
  participant, active-cleanup, runtime, association, and retained-anchor guards
  pass in one serializable transaction.
- All shipped profiles keep `archivedResourceReconcile` and
  `archivedResourcePhysicalRelease` disabled by default. Physical release cannot
  be enabled implicitly by reconcile or migration.

## Data model

### `task_resource_cleanup_jobs`

The existing table remains authoritative. Generic lifecycle jobs use
`snapshot_version=0` and known generic triggers. Archived-resource anchors use
strict versioned canonical JSON, non-empty digests, deterministic operation IDs,
and an active scope key. Unknown triggers, versions, states, malformed headers,
or partial lifecycle combinations fail inventory validation.

Version 2 represents one task-owned target. Version 3 represents one physical
target and the complete sorted participant task/association/branch sets.
Retained and blocked anchors keep their non-null scope and immutable raw snapshot.

### `task_environment_repos`

Version 0.88 environment repository rows are the authoritative physical
worktree references. Cleanup release binds all fourteen persisted columns. The
operation-bound identity snapshot is `id`, `task_environment_id`, `repository_id`,
`branch_slug`, `worktree_id`, `worktree_path`, `worktree_branch`, and
`created_at`. The mutable CAS generation is `position`, `error_message`,
`status`, `updated_at`, `merged_at`, and `deleted_at`. Complete tombstone replay
is a zero-write no-op. Row replacement, partial tombstones, reactivation, or
drift in any one of the fourteen columns fails closed. Tests mutate each column
independently so a generic claim such as "all generation fields" cannot hide a
missing predicate.

These rows are permanent durable control-plane tombstones in this port. Their
capacity grows with the number of distinct physical references ever admitted;
purge, compaction, and environment-ID reuse are deliberately out of scope. Any
future retention policy requires a separate design that preserves the complete
generation CAS and proves that no retained cleanup can still address the row.

The removed `task_session_worktrees` schema is historical input only and is not
reintroduced, dual-read, or dual-written.

## API surface

All routes require a real authenticated install administrator and are absent
when their governing restart-scoped flag is disabled. Bodies are canonical,
bounded JSON with duplicate keys, unknown fields, trailing values, and
non-canonical timestamps rejected.

- `POST /api/v1/tasks/:id/resource-cleanup/reconcile`: one exact v2 target.
- `POST /api/v1/system/resource-cleanup/reconcile-group`: one exact v3 shared
  target with the complete participant and association sets.
- `POST /api/v1/system/resource-cleanup/cancel-stale-pending-move`: one exact
  stale pending move associated with a retained anchor.
- `POST /api/v1/system/resource-cleanup/release-absent-retained-target`: one
  exact retained target whose path and Git worktree registration are absent.
- `POST /api/v1/system/resource-cleanup/retire-stale-environment-reference`:
  one exact stopped/failed environment and repository reference.

Success responses expose only operation identity, terminal state, bounded
counts, and physical-retention booleans. They never echo paths, snapshots,
secrets, task text, or association bodies.

## State machine

- Generic cleanup: `prepared -> pending -> running -> retry_wait -> running`,
  ending in `succeeded`, `failed`, or exact pristine `cancelled`.
- Reconcile anchor: `prepared -> pending -> running`, optionally through
  `retry_wait`, ending in `retained` or `blocked`.
- Physical release: `retained -> released` only after exact absent-target
  admission; no intermediate state authorizes a physical action.
- Environment retirement removes only the exact historical environment rows;
  it does not change the retained anchor state.

Generic selectors, reset, claim, completion, and cancellation never select or
mutate versioned archived-resource jobs. Versioned paths never reinterpret an
unknown generic trigger.

## Permissions

- Real install administrators may invoke enabled metadata-only routes.
- Synthetic identities, members, foreign-workspace administrators, agents,
  WebSocket callers, MCP tools, Office actions, and unauthenticated callers have
  no archived-resource mutation surface.
- Internal workers use the same feature gates, participant inventory, lock set,
  and repository CAS as direct routes.

## Failure modes

- Unknown or malformed inventory, repository errors, runtime liveness unknown,
  active executors, active sessions, archive-generation drift, hidden
  associations, participant growth, branch drift, or path identity drift cause
  zero mutation.
- Filesystem or Git removal failure preserves database state, branch state,
  registration, path, and cache. Batch cleanup evicts cache only for successful
  items.
- Missing physical paths may be retained as durable metadata; absence is not
  permission to delete, reconstruct, or prune anything.
- SQLite compares canonical instants but uses same-transaction raw lexical time
  tokens for exact CAS. PostgreSQL uses typed timestamps under row locks.
- A commit or post-write readback whose outcome cannot be proven returns
  `OUTCOME_UNKNOWN`. Automatic retry is forbidden until exact readback proves
  zero effect.
- PostgreSQL runtime behavior remains `NOT_RUN` unless an explicitly isolated
  test DSN is supplied. Ambient or live PostgreSQL is never used for validation.

## Persistence guarantees

Retained, blocked, and released anchors, their immutable snapshots, digests,
scope keys, attempts, revisions, and completion timestamps survive task,
session, workspace, and backend lifecycle changes. Disabled feature flags cause
zero archived-resource repository activity. Startup recovery touches only exact
interrupted jobs when enabled and never increases an already claimed attempt.

## Scenarios

- **GIVEN** central admission is unavailable, **WHEN** any managed-root creation,
  cleanup, release, or fallback path is requested, **THEN** it fails before any
  repository, Git, filesystem, or runtime mutation required by that path.
- **GIVEN** two tasks share one target, **WHEN** v3 reconcile omits either owner
  or association, **THEN** it returns conflict with zero mutation.
- **GIVEN** an association is tombstoned, reactivated, and later tombstoned as a
  new generation, **WHEN** an old retained job replays or unarchives, **THEN** it
  cannot modify the new generation.
- **GIVEN** a running job was interrupted before completion, **WHEN** enabled
  startup recovery runs, **THEN** it completes the same attempt or preserves an
  explicit blocked/unknown outcome without a second claim.
- **GIVEN** worktree removal fails, **WHEN** cleanup runs, **THEN** its path, Git
  registration, branch OID, database reference, and cache remain unchanged.
- **GIVEN** a complete tombstone, **WHEN** identical cleanup retries, **THEN** no
  persisted timestamp or field changes.
- **GIVEN** a retained anchor with one exact stale pending move, **WHEN** the
  cancellation route succeeds, **THEN** only that row disappears and the anchor,
  session, association, task generation, Git, and filesystem state are unchanged.
- **GIVEN** an absent retained target, **WHEN** sealed release succeeds, **THEN**
  the anchor becomes released and no physical or Git action occurs.
- **GIVEN** an environment is a non-coordinator participant in any active v3
  cleanup, **WHEN** environment retirement is requested, **THEN** retirement is
  rejected with zero mutation.
- **GIVEN** both archived-resource flags are disabled, **WHEN** routes, startup,
  periodic workers, and generic cleanup execute, **THEN** versioned anchors are
  byte-equivalent before and after.

## Out of scope

- Enabling any physical deletion executor or setting
  `archivedResourcePhysicalRelease=true` in shipped profiles.
- Deleting or cleaning dirty worktrees, branches, task records, queue messages,
  retained anchors, or user files.
- Reintroducing v0.85 session-worktree persistence or a second ledger.
- Installing dependencies, using live/ambient PostgreSQL, or treating an
  offline test as a production result.
