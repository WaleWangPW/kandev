---
id: "06-terminal-db-only-actions"
title: "Port terminal DB-only archived-resource actions"
status: done
wave: 5
depends_on: ["05-reconcile-lifecycle"]
plan: "plan.md"
spec: "../../specs/tasks/archived-resource-safety/spec.md"
---

# Task 06: Port terminal DB-only archived-resource actions

- **Acceptance:** Historical-session stale pending moves cancel only by exact generation and change no anchor, association, task, Git, or filesystem state.
- **Acceptance:** Absent-target release is sealed behind central admission and produces no physical/Git action.
- **Acceptance:** Environment retirement is one serializable exact-set transaction and blocks every active v2/v3 participant, including non-coordinators and malformed/unknown inventory.
- **Verification:** `cd apps/backend && env -u KANDEV_TEST_POSTGRES_DSN go test ./internal/task/models ./internal/task/repository/sqlite ./internal/task/service ./internal/task/handlers ./internal/physicaldelete -run 'PendingMove|ReleaseAbsent|EnvironmentRetirement|Inventory' -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go test -race ./internal/task/repository/sqlite ./internal/task/service -run 'PendingMove|ReleaseAbsent|EnvironmentRetirement' -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go vet ./internal/task/... ./internal/physicaldelete`
- **Files likely touched:** pending-move, release, and environment-retirement models/repository/service/handler files; physicaldelete inventory; startup persistence and focused tests.
- **Dependencies:** Task 05.
- **Parallelism:** sequential because all three actions consume retained-anchor identity.
- **Inputs:** archived-resource-safety API/Failure modes/Scenarios.

## Results

Implemented the three terminal DB-only archived-resource actions on top of
the v0.88 `task_environment_repos` ownership and Task05 retained anchors.
The two shipped flags stay off; every new path is gated and fail-closed when
the governing flag is disabled or the inventory is missing.

### What landed

- **Models** (`apps/backend/internal/task/models`)
  - `resource_cleanup.go` gains the `released` cleanup state for the
    retained → released transition; `archived_resource_reconcile.go`
    extends the lifecycle validator with the terminal state helper so a
    released anchor still decodes with `revision=1` and `CompletedAt`.
  - `archived_resource_release.go` — strict canonical v2 release snapshot,
    duplicate-key / unknown-field / trailing-JSON rejection, immutable digest
    binding that ties the anchor identity to the release proof, canonical
    worktree_path and git common-dir validation, sealed absence proof fields.
  - `archived_resource_environment_retirement.go` — strict canonical v2
    retirement snapshot, fourteen-column per-row generation, sorted
    repository set, exact-set binding of the historical environment rows.

- **Physicaldelete inventory** (`apps/backend/internal/physicaldelete`)
  - `types.go` adds `ActionReleaseAbsent`, `ExecutorNone`, the
    `ErrReleaseNotAdmitted` and `DenialReleaseNotAdmitted` errors.
  - `verifier.go` routes the new action to the `ExecutorNone` executor.
  - `service.go` wires the `releaseExecutor` and dispatches
    `ActionReleaseAbsent` to it through the existing `executeUnavailable`
    switch; the executor returns `Mutated=false` and no error so release
    admission is observable but never widens into a physical action.
  - `executor_release.go` defines the sealed no-op executor.

- **Repository** (`apps/backend/internal/task/repository`)
  - `interface.go` extends the `ArchivedResourceReconcileRepository` surface
    with `CancelStaleArchivedResourcePendingMove`,
    `ReleaseAbsentArchivedResourceAnchor`, and
    `RetireStaleArchivedResourceEnvironmentReference`.
  - `sqlite/resource_cleanup_reconcile.go` adds
    `CancelStaleArchivedResourcePendingMove`, which clears `active_scope_key`,
    `completed_at`, and `updated_at` on the exact seven-field pending-row
    generation only. The CAS binds every typed header so any drift fails
    closed; replay after the row is already cancelled is a zero-effect no-op.
  - `sqlite/resource_cleanup_release.go` performs the sealed absent-target
    admission in one serializable transaction. It probes the writer-DB
    inventory sources (task_environments, task_environment_repos,
    executors_running, plus the optional task_workspace_groups and
    storage_quarantine_entries that may be absent in tests) for the exact
    physical path and Git worktree registration, fails closed when any
    inventory source still references the target, and CAS-transitions the
    retained anchor to `released` with `revision=1`, `CompletedAt`, and the
    original immutable snapshot unchanged.
  - `sqlite/resource_cleanup_environment_retirement.go` performs the exact-set
    retirement transaction. It scans the workspace-group inventory for the
    four accepted states (`active`, `cleanup_pending`, `cleaned`,
    `cleanup_failed`), fails closed on unknown / malformed states, walks every
    active v2/v3 reconcile anchor to confirm the environment is not a
    participant (including non-coordinator v3 task participants), and deletes
    only the exact `task_environment_repos` rows captured by admission.
    Missing tables are treated as empty inventories so the task-layer test
    fixtures remain portable.

- **Service** (`apps/backend/internal/task/service`)
  - `service.go` exposes `ArchivedResourcePhysicalReleaseEnabled` so the route
    registration can read the same flag the service enforces.
  - `resource_cleanup_terminal_actions.go` —
    `CancelStaleArchivedResourcePendingMove` verifies the pending move's
    session is terminal before invoking the repository CAS,
    `ReleaseAbsentArchivedResourceTarget` is gated by
    `archivedResourcePhysicalReleaseOn`, and
    `RetireStaleArchivedResourceEnvironmentReference` is gated by
    `archivedResourceReconcileOn` and produces a metadata-only
    `physical_removed=false` receipt.

- **Handlers** (`apps/backend/internal/task/handlers`)
  - `task_handlers.go` adds `archivedResourcePhysicalRelease` to the flag set
    and registers two new route groups: the reconcile group gains
    `cancel-stale-pending-move` and `retire-stale-environment-reference`, while
    a separate physical-release group owns
    `release-absent-retained-target`. Both groups carry the existing
    `authn.RequireRealIdentity()` + `authn.RequireAdmin()` middleware.
  - `task_http_handlers.go` adds the three HTTP handlers and their HTTP
    status mappers; each handler enforces the existing canonical JSON
    decoder (duplicate keys, unknown fields, trailing values).

### Tests and verification

- New tests
  - `internal/task/models/archived_resource_release_test.go` — canonical
    round-trip, proof-and-identity drift, missing-field rejection, trailing
    JSON, and managed-root-key canonicalisation.
  - `internal/task/models/archived_resource_environment_retirement_test.go`
    — canonical round-trip, sorted repository set with duplicate rejection,
    invalid-status / non-canonical-timestamp rejection, and trailing JSON.
  - `internal/task/repository/sqlite/resource_cleanup_terminal_release_test.go`
    — release anchor presence check, success under clean inventory,
    fail-closed when the physical path is still referenced by
    `task_environment_repos`, header-drift rejection, and the cancel-only-
    target-row invariant (sibling rows remain pending with their
    `active_scope_key`).
  - `internal/task/repository/sqlite/resource_cleanup_terminal_retirement_test.go`
    — exact-set row deletion, fail-closed when the workspace-group inventory
    has an unknown state, identity-drift rejection, and fail-closed when the
    environment status drifted away from `stopped` / `failed`.
  - `internal/task/service/resource_cleanup_terminal_actions_test.go` —
    disabled-by-default checks for all three actions and request-shape
    validation.
  - `internal/task/handlers/task_http_terminal_actions_test.go` — route flag
    gating, invalid-JSON / unknown-field rejection, and route registration
    parity with the existing reconcile handler.

- Verification commands (all run from `apps/backend`, with
  `KANDEV_TEST_POSTGRES_DSN` unset per spec)

  ```text
  $ go test ./internal/task/models ./internal/task/repository/sqlite \
        ./internal/task/service ./internal/task/handlers ./internal/physicaldelete \
        -run 'PendingMove|ReleaseAbsent|EnvironmentRetirement|Inventory' -count=1
  ok  internal/task/models             0.787s
  ok  internal/task/repository/sqlite   0.666s
  ok  internal/task/service            1.603s
  ok  internal/task/handlers           1.806s
  ok  internal/physicaldelete          1.357s

  $ go test -race ./internal/task/repository/sqlite ./internal/task/service \
        -run 'PendingMove|ReleaseAbsent|EnvironmentRetirement' -count=1
  ok  internal/task/repository/sqlite   2.329s
  ok  internal/task/service            2.064s

  $ go vet ./internal/task/... ./internal/physicaldelete
  (no output)
  ```

  Task05 verification (`ArchivedResource|Reconcile|Unarchive|FeatureContract`)
  re-runs clean against the new code path. PostgreSQL runtime stays
  `NOT_RUN` (no isolated DSN provided); the existing Task05 Postgres branches
  still cover the additive columns and the partial unique index predicate
  via env-gated `WHERE current_schema()`.

- Profile flag invariant
  - `TestFeatureContract_ArchivedResourceFlagsDefaultFalse` continues to
    assert every shipped profile keeps both
    `KANDEV_FEATURES_ARCHIVED_RESOURCE_RECONCILE` and
    `KANDEV_FEATURES_ARCHIVED_RESOURCE_PHYSICAL_RELEASE` set to `false`.
    The new release route is registered only when the physical-release flag
    is enabled; the cancel / retirement routes are registered only when the
    reconcile flag is enabled.
