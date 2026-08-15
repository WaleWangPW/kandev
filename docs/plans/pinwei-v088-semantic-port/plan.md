---
spec: docs/specs/tasks/archived-resource-safety/spec.md
related_specs:
  - docs/specs/no-silent-model-fallback/spec.md
  - docs/specs/tasks/runtime-cleanup.md
  - docs/specs/system-page/storage-maintenance.md
created: 2026-08-15
status: draft
---

# Implementation Plan: Pinwei v0.88 Semantic Port

## Overview

Port the accepted Pinwei safety contracts from the v0.85 lineage onto the clean
official `v0.88.0` commit `cab9eaf19d997bb4c8020dd263ddc60d5b035b64`.
The port preserves behavior, not old patches: v0.88 model/profile and
`task_environment_repos` ownership are authoritative, and removed
`task_session_worktrees` code is never restored. Each layer is a clean immutable
successor with targeted tests before the next layer begins.

Confirmed root causes:

- v0.88 preserves a missing configured model but still heals a user-modified
  mode, has no prepared-execution profile fingerprint, and may launch with a
  partial environment when secret resolution fails.
- Worktree cleanup still continues branch/store/cache mutation after physical
  removal failure.
- v0.88 has no generation-bound release contract for
  `task_environment_repos`; an old cleanup retry can overwrite a reactivated
  reference.
- The Pinwei central admission and archived-resource DB-only lifecycle are
  private v0.85 capabilities and are absent from upstream v0.88.

---

## Backend

### Profile launch binding

Amend `internal/agent/settings/controller/reconciler.go` so user-modified
non-empty mode/model values survive capability drift. Add a deterministic,
secret-free profile fingerprint in `internal/agent/runtime/lifecycle` and carry
it through lifecycle and orchestrator launch DTOs. Resolve the entire profile
environment atomically: secret store absence, missing references, or reveal
failure returns sanitized `BLOCKED_PROFILE_SECRET` with no partial environment.
Every ACP, passthrough, resume, recovery, startup, and executor launch verifies
the fingerprint and returns `BLOCKED_PROFILE_DRIFT` before process creation on
mismatch. Legacy rows without a fingerprint retain the compatibility path.

### Worktree failure atomicity

Change `internal/worktree/manager_cleanup.go` so `removeWorktreeDir` failure
returns immediately and preserves branch, store row, cache, path, and Git
registration. Batch cleanup tracks successful items and evicts only their
caches. This task does not claim rollback after a successful physical removal;
central admission remains the authority for broader DB/filesystem atomicity.

### Environment-repository generation CAS

Introduce a v0.88-native release snapshot and explicit Store method for
`task_environment_repos`. Bind all fourteen persisted columns. The
operation-bound identity snapshot is `id`, `task_environment_id`,
`repository_id`, `branch_slug`, `worktree_id`, `worktree_path`,
`worktree_branch`, and `created_at`; mutable CAS generation is `position`,
`error_message`, `status`, `updated_at`, `merged_at`, and `deleted_at`. SQLite
compares canonical instants and updates with raw
lexical tokens loaded in the same transaction. PostgreSQL uses typed timestamps
and row locks. Complete tombstones are zero-write no-ops; row replacement,
partial or reactivated tombstones, or drift in any individual column returns a
typed fail-closed error. The test matrix mutates every column independently.

### Central physical-delete admission

Port `internal/physicaldelete` as the only managed-root mutation authority.
Admission owns typed actions/resources, canonical path and Git common-dir
validation, complete authoritative inventory, lock ordering, and provisional
creation leases. `BeginProvisional` and `Execute` share one lock domain. No
managed-root physical executor is available in this release; missing admission
or executor denies before consumer repository, Git, filesystem, or runtime
mutation. Inventory decodes generic cleanup and strict versioned anchors from
the existing cleanup-job table and rejects unknown/future/malformed rows.

Wire the two restart-scoped flags through `profiles.yaml`, typed config,
`runtimeflags.Registry`, feature boot payload, and frontend feature defaults.
All shipped profiles remain false.

### Reconcile persistence and service

Port strict canonical v2/v3 snapshot models, fail-closed schema migration,
partial unique active-scope index verification, exact replay admission,
participant-aware lifecycle gates, and repository methods to the v0.88 schema.
Generic cleanup selectors and completion APIs exclude versioned jobs in every
state. Direct, startup, periodic, and retry paths use a shared completion helper,
same-attempt interrupted recovery, stable participant locks, and exact task,
association, runtime, branch, path, and generation validation.

Unarchive uses one sealed serializable repository transaction for exact task
generation CAS, generic cleanup cancellation, v2/v3 validation, and association
restore. Historical retained generations are strict active no-ops and never
expand current participant cleanup.

### Pending move, absent release, and environment retirement

Port only the accepted final pending-move behavior, including historical
terminal sessions and exact seven-field pending-row generation. Port the sealed
absent-target release and stopped/failed environment retirement routes.
Environment retirement scans every active versioned anchor and treats a v3 task
participant as protected even when it is not the coordinator. It recognizes
only the explicit workspace-group inventory states accepted by the fixed v0.85
successor; all unknown resource kinds/states remain globally fail-closed.

### Consumer integration

Replace every managed-root physical mutation entry with central admission:
worktree cleanup/recreate, task/session/workspace lifecycle, Office GC, system
storage/quarantine, Git worktree removal, agentctl cleanup, database reset, and
fallback removal paths. Ordinary user workspace-file edit/delete APIs remain
outside managed-root physical cleanup. No WebSocket, MCP, or Office mutation
route is added for archived-resource operations.

---

## Frontend

No bespoke archived-resource action UI is added. Extend only the typed feature
contract/defaults required by the generic Feature Toggles surface. Frontend
validation runs only when dependencies already exist; this plan does not
authorize installing them.

---

## Tests

- **Profile preservation and fingerprint:** table-driven reconciler, lifecycle,
  passthrough, startup, resume, executor, and secret-failure tests in the
  affected packages. Assert zero process/env on block and sanitized outputs.
- **Worktree removal failure:** real Git worktree fixture proves path,
  registration, branch OID, store row, and cache remain unchanged.
- **Environment-repo release:** SQLite replay/partial/reactivation/offset/1us
  drift and channel-barrier concurrency; env-gated PostgreSQL typed CAS.
- **Admission:** action/resource/path/common-dir/lock/provisional matrices and
  nil-admission zero-touch tests for every creation and deletion consumer.
- **Persistence:** SQLite fresh/legacy/half-migrated/replay and exact partial
  index verification. PostgreSQL DDL/query behavior remains env-gated.
- **Reconcile:** canonical JSON adversarial matrix, 0/128/129 association bounds,
  exact replay/conflict, v2/v3 atomic completion, same-attempt recovery,
  participant lock convergence, unarchive generations, and disabled zero-touch.
- **Pending/release/retirement:** exact generation, stale/historical, participant
  protection, outcome-unknown, and non-target byte-equivalence tests.
- **Integration:** handlers with real auth middleware, assembled routes off/on,
  generic cleanup regression, storage/Office/task/worktree consumers, full
  backend compile, focused race, vet, gofmt, and diff checks.

No test reads ambient `KANDEV_TEST_POSTGRES_DSN`. Commands explicitly unset it;
PostgreSQL runtime is reported `NOT_RUN` unless a separately authorized isolated
DSN exists.

---

## Verification Results

Pending. Each task records exact commands and results before its status changes
to done.

---

## Implementation Waves And Parallel Candidates

The primary execution order is sequential because later layers depend on exact
contracts and schemas from earlier layers.

Wave 1:

- [ ] [Task 01: profile launch binding](task-01-profile-launch-binding.md)
- [ ] [Task 02: worktree failure atomicity](task-02-worktree-failure-atomicity.md)

Task 02 is a parallel candidate only because its files are disjoint from Task
01; user authorization is still required before delegation.

Wave 2:

- [ ] [Task 03: environment-repository generation CAS](task-03-environment-repo-generation-cas.md)

Wave 3:

- [ ] [Task 04: central physical-delete admission](task-04-central-admission.md)

Wave 4:

- [ ] [Task 05: reconcile persistence and lifecycle](task-05-reconcile-lifecycle.md)

Wave 5:

- [ ] [Task 06: pending move, release, and retirement](task-06-terminal-db-only-actions.md)

Wave 6:

- [ ] [Task 07: managed-root consumer integration](task-07-consumer-integration.md)

Wave 7:

- [ ] [Task 08: migration and adversarial verification](task-08-migration-verification.md)

Wave 8:

- [ ] [Task 09: fixed-object and private overlay delivery](task-09-overlay-delivery.md)

Wave 9:

- [ ] [Task 10: offline staging and controlled runtime readback](task-10-runtime-handoff.md)

## Risks

- v0.88 normalized environment ownership is not source-compatible with the old
  session-worktree implementation; copying old SQL is a safety failure.
- Full PostgreSQL runtime behavior remains unknown without an isolated DSN and
  must not be inferred from SQLite.
- The installed v0.85 database contains durable retained anchors. Upgrade
  migrations must prove byte-equivalent preservation before any runtime canary.
- Two pre-existing upstream worktrees and the documented Pinwei dirty/HOLD set
  remain out of scope and must not be read, reset, cleaned, or retired.
- Physical execution remains deliberately unavailable. This plan does not make
  disk reclamation or dirty-worktree cleanup a completion criterion.
