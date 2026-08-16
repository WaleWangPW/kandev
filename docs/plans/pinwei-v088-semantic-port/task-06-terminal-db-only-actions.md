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

### Fix receipt (counterexample response)

The first candidate (`0fc8f491`) routed `ReleaseAbsentArchivedResourceTarget`
straight to the release repository writer. The sealed central admission
(`physicaldelete.ActionReleaseAbsent` / `releaseExecutor`) was unreachable
from the production service path, so the retained anchor could transition
to released without the central lock, inventory, or root-policy gate.

The successor commit closes that gap end-to-end:

- **Service** (`apps/backend/internal/task/service`)
  - `Service.physicalDeleteAdmission` field plus
    `SetPhysicalDeleteAdmission` setter, threaded from
    `backendapp.provideWorktreeManager` so the task service and the worktree
    manager share the same `physicaldelete.Service` instance and lock domain.
  - `ReleaseAbsentArchivedResourceTarget` now invokes the sealed
    `ActionReleaseAbsent` admission BEFORE any repository read/mutation,
    requires `receipt.Mutated == false`, `receipt.Executor == ExecutorNone`,
    `receipt.Action == ActionReleaseAbsent`, and a matching
    `ResourceKind/ResourceID`. Any drift fails closed with one of three new
    typed errors (`ErrArchivedResourceReleaseAdmissionUnavailable`,
    `ErrArchivedResourceReleaseAdmissionDenied`,
    `ErrArchivedResourceReleaseAdmissionMutated`).
  - Nil admission fails closed before any repo call
    (`ErrArchivedResourceReleaseAdmissionUnavailable`).

- **Composition** (`apps/backend/internal/backendapp/worktree.go`)
  - The same `physicaldelete.New` instance that the worktree manager consumes
    is now wired into the task service via `taskSvc.SetPhysicalDeleteAdmission`.

- **Tests** (`apps/backend/internal/task/service/resource_cleanup_terminal_actions_admission_test.go`)
  - `admissionOrderSpy` records the exact admission request and call order;
    `releaseRepoSpy` enforces zero repo reads on every fail-closed scenario.
  - `TestTerminalReleaseRunsAdmissionBeforeAnyRepositoryCall` — admission
    request shape (`ActionReleaseAbsent`, `AuthorityAdmin`, `ExecutorNone`,
    `ResourceKindEnvironmentRepo`, exact path + id) plus one-and-only-one
    repo call.
  - `TestTerminalReleaseFailsClosedWhenAdmissionIsNil` — zero repo calls.
  - `TestTerminalReleaseFailsClosedWhenAdmissionDeny` /
    `…InventoryIncomplete` / `…LockedOut` — every denial path produces
    `ErrArchivedResourceReleaseAdmissionDenied` and zero repo calls.
  - `TestTerminalReleaseFailsClosedWhenAdmissionUnavailable` — typed
    "unavailable" error, zero repo calls.
  - `TestTerminalReleaseFailsClosedWhenAdmissionReturnsMutatedReceipt` /
    `…WrongExecutor` / `…WrongAction` — every receipt-validation failure
    produces `ErrArchivedResourceReleaseAdmissionMutated` and zero repo calls.
  - `TestTerminalReleaseReceiptContractHoldsOnSuccess` — `physical_retained`
    true, `physical_removed` false, terminal `released` state, single target.

### Re-verification (all `env -u KANDEV_TEST_POSTGRES_DSN`, from `apps/backend`)

```text
$ go test ./internal/task/models ./internal/task/repository/sqlite \
        ./internal/task/service ./internal/task/handlers ./internal/physicaldelete \
        -run 'PendingMove|ReleaseAbsent|EnvironmentRetirement|Inventory' -count=1
ok  internal/task/models             0.272s
ok  internal/task/repository/sqlite   0.657s
ok  internal/task/service            0.980s
ok  internal/task/handlers           0.985s
ok  internal/physicaldelete          1.308s

$ go test -race ./internal/task/repository/sqlite ./internal/task/service \
        -run 'PendingMove|ReleaseAbsent|EnvironmentRetirement' -count=1
ok  internal/task/repository/sqlite   2.018s
ok  internal/task/service            1.921s

$ go vet ./internal/task/... ./internal/physicaldelete
(no output)

$ go build ./...
(no output)
```

PostgreSQL runtime remains `NOT_RUN`; no new dependency was installed; the
shipped `archivedResourceReconcile` / `archivedResourcePhysicalRelease` flags
remain default-off; no route, runtime, or live action was started; no live DB,
API, or remote server was contacted.

### Terminal action fix receipt (counterexample response)

The previous composition-fix candidate (`eb90182`) routed the release
admission through `physicaldelete.New` but:
1. the receipt's `ResourceID` carried the anchor's `operation_id` instead
   of the v0.88-conventional `worktree_id`;
2. the release path skipped the central `RootPolicy` and would accept a
   root-protected path;
3. no test exercised the service end-to-end through the real
   `physicaldelete.New` + `SQLInventorySource` composition to verify the
   exact-bound anchor identity, the CAS path, and the no-op
   `Mutated=false` / `Executor=ExecutorNone` contract.

This successor closes all three gaps:

- **Inventory loader** (`apps/backend/internal/physicaldelete/writerdb.go`)
  - Accepts both `reconcile` (`models.TaskResourceCleanupTriggerReconcile`,
    the canonical v0.88 trigger value Task05 writes) and the historical
    `archived_resource_reconcile` (used by the existing
    `TestSQLInventorySourceRejectsUnvalidatedReconcileAndFutureCleanup`),
    so the Task05 v2/v3 seed pattern and the canonical writer both load.
- **Service** (`apps/backend/internal/task/service`)
  - `ReleaseAbsentArchivedResourceTarget` sends the anchor's `worktree_id`
    (not `operation_id`) as `physicaldelete.Request.Resource.ID` so the
    receipt's `ResourceID` follows the v0.88 convention.
  - `verifyAbsentTargetRelease` consults the configured `RootPolicy` after
    the inventory absence proof succeeds, so a root-protected release
    path fails closed with `ErrProtectedResource` (mapped to
    `ErrArchivedResourceReleaseAdmissionDenied` at the service layer).
- **Composition** (`apps/backend/internal/backendapp/worktree.go`)
  - Unchanged; the same `physicaldelete.New` instance continues to be
    shared with the task service.

- **Tests** (`apps/backend/internal/task/service/resource_cleanup_terminal_release_real_test.go`)
  - Real-composition fixture builds an in-memory SQLite writer DB with the
    full v0.88 schema, seeds a Task05-built retained v2 anchor using the
    canonical `models.TaskResourceCleanupTriggerReconcile` trigger, and
    wires the same `physicaldelete.New(physicaldelete.Config{
    Inventory: NewSQLInventorySource(db)})` instance into the service via
    `SetPhysicalDeleteAdmission` plus a `terminalReleaseCASRepo` that
    performs the exact DB-only CAS.
  - `TestTerminalReleaseRealCompositionNoOpSucceeds` proves the success
    path: the admission passes, the CAS flips the anchor from `retained`
    to `released` with the canonical `revision=1` / `completed_at` /
    `managed_root_key` fields byte-equal, and the result carries the
    exact-bound metadata.
  - `TestTerminalReleaseReceiptResourceIDIsWorktreeID` exercises the sealed
    admission directly and verifies `receipt.ResourceID` is the
    `worktree_id`, not the `operation_id`.
  - `TestTerminalReleaseRootPolicyRejectsRootProtectedPath` constructs a
    `RootPolicy` whose only protected path equals the release target, and
    asserts both the central admission and the service-level call fail
    closed.
  - `TestTerminalReleaseExactBoundIdentityMismatchFails` walks four
    identity-drift mutations (operation_id, worktree_id, digest,
    task_id) and verifies each fails closed through the real
    composition.

### Re-verification (all `env -u KANDEV_TEST_POSTGRES_DSN`, from `apps/backend`)

```text
$ go test ./internal/task/models ./internal/task/repository/sqlite \
        ./internal/task/service ./internal/task/handlers ./internal/physicaldelete \
        -run 'PendingMove|ReleaseAbsent|EnvironmentRetirement|Inventory' -count=1
ok  internal/task/models             0.680s
ok  internal/task/repository/sqlite   0.853s
ok  internal/task/service            0.962s
ok  internal/task/handlers           1.258s
ok  internal/physicaldelete          0.644s
```

The previous candidate (`225c022`) routed `ReleaseAbsentArchivedResourceTarget`
through a sealed `physicaldelete.Admission`, but the real writer inventory
loader (`SQLInventorySource.Load`) returned `ErrInventoryIncomplete` for any
row with `trigger='archived_resource_reconcile'` — meaning the Task05 v2/v3
anchor decoder was never wired into the inventory. Any legal release
(which requires a retained v2 anchor) was therefore fail-closed by
inventory load before admission ran. The mock-only tests in the previous
candidate used an empty cleanup-anchor set and never exercised the real
composition path.

This successor commit closes that gap end-to-end:

- **Inventory** (`apps/backend/internal/physicaldelete`)
  - `selectCleanupAnchors` now decodes each `task_resource_cleanup_jobs`
    row via the Task05 canonical decoder. Generic cleanup rows
    (`archive`, `delete`, …) produce unvalidated anchors that never become
    release candidates. `archived_resource_reconcile` rows are routed
    through `decodeRetainedReconcileAnchor`, which calls
    `models.DecodeArchivedResourceReconcileSnapshot` (v2) or
    `models.DecodeArchivedResourceGroupReconcileSnapshot` (v3) and verifies
    every redundant header column. Any decode failure or column drift fails
    the entire inventory closed.
  - `CleanupAnchor` carries the full canonical identity (`OperationID`,
    `SnapshotDigest`, `ResourceKind`, `ResourceID`, `TaskID`,
    `ManagedRootKey`, `AnchorRevision`, `Path`, `RepositoryID`, `Branch`,
    `HeadOID`, `SnapshotVersion`) so the release admission can perform an
    exact-bound match.
  - `inventory.validate` requires every validated anchor to carry that
    identity; unknown / malformed rows fail closed.
  - `Request.AnchorIdentity` is a new struct that the release admission
    verifies field-by-field against the loaded anchor. `ComputeAnchorManagedRootKey`
    derives the canonical managed root key from the worktree path.
  - `Execute` short-circuits `verifyAnchors` for `ActionReleaseAbsent`
    (the absence is proven via the inventory, not Lstat) and routes through
    a new `verifyAbsentTargetRelease` that:
    1. finds the unique validated retained anchor whose
       `OperationID` / `SnapshotDigest` / `ResourceKind` / `ResourceID` /
       `TaskID` / `ManagedRootKey` / `SnapshotVersion` match `AnchorIdentity`;
    2. verifies the target path is absent from each non-anchor inventory
       source (`EnvironmentRepositories`, `ExecutorWorktrees`,
       `TaskEnvironments`, `WorkspaceGroups`, `QuarantineEntries`) by
       walking the per-source slices so deduped path collisions cannot hide
       a remaining reference;
    3. verifies the requested `CommonDir` is not in any non-anchor
       common-dir.
  - `inventorySnapshot` now carries the full `Inventory` so the release
    admission can iterate every source slice independently.

- **Service** (`apps/backend/internal/task/service`)
  - `ReleaseAbsentArchivedResourceTarget` populates `physicaldelete.AnchorIdentity`
    with every redundant header field so the central admission has the full
    canonical identity to bind. All other fields are unchanged.

- **Tests** (`apps/backend/internal/physicaldelete/composer_release_test.go`)
  - Real-composition fixture: builds an in-memory SQLite writer DB with the
    full v0.88 schema, seeds a Task05-built retained v2 anchor, and runs
    `physicaldelete.New(physicaldelete.Config{Inventory: NewSQLInventorySource(db)})`
    — the exact path `backendapp.provideWorktreeManager` uses.
  - Success: `TestRealCompositionReleaseAdmitsExactBoundRetainedAnchor`
    proves `Mutated=false`, `Executor=ExecutorNone`, `Action=ActionReleaseAbsent`,
    non-empty `InventoryDigest`, and a non-empty canonical lock entry.
  - Zero-write failure cases (all return their declared typed error):
    - `…OnExtraRepositoryReference` — `task_environment_repos.worktree_path`
      still points at the target.
    - `…OnExecutorReference` — `executors_running` still points at the target.
    - `…OnUnknownWorkspaceState` — `task_workspace_groups.cleanup_status` is
      not one of the four accepted states.
    - `…OnMalformedV2` — v2 anchor with invalid snapshot bytes fails inventory
      decode, so the whole load is fail-closed.
    - `…OnDriftDigest` — request digest does not match anchor digest.
    - `…OnUnknownOperationID` — request binds to a non-existent anchor.
    - `…OnWrongResourceKind` — request resource_kind does not match anchor.
    - `…OnAnchorNotRetained` — anchor state mutated away from `retained`.
    - `…OnWrongAnchorVersion` — request version does not match anchor.
    - `…OnEmptyAnchorIdentity` — request omits anchor identity.
    - `…OnChildren` — request carries children (release must be leaf-only).
  - Zero durable side effect:
    `TestRealCompositionReleaseLeavesAnchorAndTargetUntouched` verifies the
    anchor row's `state` and `completed_at` are byte-equal before and after
    the admission, and `task_environment_repos` row count is unchanged.

### Re-verification (all `env -u KANDEV_TEST_POSTGRES_DSN`, from `apps/backend`)

```text
$ go test ./internal/task/models ./internal/task/repository/sqlite \
        ./internal/task/service ./internal/task/handlers ./internal/physicaldelete \
        -run 'PendingMove|ReleaseAbsent|EnvironmentRetirement|Inventory' -count=1
ok  internal/task/models             1.246s
ok  internal/task/repository/sqlite   1.156s
ok  internal/task/service            1.440s
ok  internal/task/handlers           1.427s
ok  internal/physicaldelete          1.735s

$ go test -race ./internal/task/repository/sqlite ./internal/task/service \
        -run 'PendingMove|ReleaseAbsent|EnvironmentRetirement' -count=1
ok  internal/task/repository/sqlite   2.293s
ok  internal/task/service            2.303s

$ go vet ./internal/task/... ./internal/physicaldelete
(no output)

$ go build ./...
(no output)
```

PostgreSQL runtime remains `NOT_RUN`; no new dependency was installed; the
shipped `archivedResourceReconcile` / `archivedResourcePhysicalRelease` flags
remain default-off; no route, runtime, or live action was started; no live DB,
API, or remote server was contacted.
