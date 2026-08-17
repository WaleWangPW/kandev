---
id: "08-migration-verification"
title: "Verify migrations and adversarial safety matrix"
status: completed
wave: 7
depends_on: ["01-profile-launch-binding", "07-consumer-integration"]
plan: "plan.md"
spec: "../../specs/tasks/archived-resource-safety/spec.md"
---

# Task 08: Verify migrations and adversarial safety matrix

- **Acceptance:** SQLite fresh, v0.88 legacy, half-migrated, replay, rollback, malformed-index, and existing retained-anchor preservation matrices pass without physical effects.
- **Acceptance:** All targeted ordinary/race/vet checks and the full backend compile/test gate have classified results; PostgreSQL is honestly PASS or `NOT_RUN`, never inferred.
- **Acceptance:** The dependency-free backend/frontend feature-contract test passes. Focused frontend tests pass when the existing dependency tree is present, or the receipt explicitly says `FRONTEND_NOT_RUN_DEPENDENCIES_ABSENT`; dependencies are never installed for this task.
- **Acceptance:** Final diff is formatted, contains no unexpected paths, and leaves the source worktree clean after commit.
- **Verification:** `cd apps/backend && env -u KANDEV_TEST_POSTGRES_DSN go test ./... -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go test ./internal/backendapp -run FeatureContract -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go test -race ./internal/physicaldelete ./internal/worktree ./internal/task/repository/sqlite ./internal/task/service ./internal/task/handlers ./internal/agent/runtime/lifecycle ./internal/orchestrator/executor -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go vet ./... && test -z "$(gofmt -l internal)" && git -C ../.. diff --check`
- **Verification:** From the repository root: `if test -d apps/node_modules; then cd apps && pnpm --filter @kandev/web test -- lib/state/slices/features/features-contract.test.ts lib/state/slices/features/features-slice.test.ts app/actions/features.test.ts lib/api/domains/runtime-flags-api.test.ts; else echo FRONTEND_NOT_RUN_DEPENDENCIES_ABSENT; fi`; record either PASS or the explicit `NOT_RUN` receipt. Dependency installation is forbidden.
- **Files likely touched:** tests and migration fixtures already owned by Tasks 01-07; plan/task result sections.
- **Dependencies:** Tasks 01 and 07; source acceptance cannot bypass either chain.
- **Parallelism:** sequential final source acceptance.
- **Inputs:** all spec scenarios and task results.

## Results

2026-08-17 local verification receipt (no runtime, database, or physical
action):

- SQLite cleanup-schema adversarial matrix: PASS. New fixtures cover a v0.88
  legacy table, a half-migrated table, idempotent replay, malformed
  active-scope index rejection, and byte-preserving replay of an existing
  retained v2 anchor. The existing task-environment release-generation test
  also verifies that environment deletion preserves the authoritative row.
- Managed-root regression matrix: PASS. Worktree preparer rollback fixtures
  now use a test-only authorized admission wrapper; production remains wired
  to the sealed executor. macOS temporary-directory aliases are canonicalized
  only after root/path validation, while a symlinked workspace root is still
  rejected.
- Targeted ordinary tests: PASS for physicaldelete, worktree, task SQLite
  repository, task service, task handlers, lifecycle, executor, and
  backendapp. The backendapp route harness now matches production's
  resource-cleanup/unarchive composition.
- Targeted race suite: PASS for physicaldelete, worktree, task SQLite
  repository, task service, task handlers, lifecycle, and executor.
- Full backend compile gate (`go test ./... -run '^$'`): PASS. Full vet:
  PASS. `git diff --check`: PASS. Every changed Go file is gofmt-clean; the
  repository-wide `gofmt -l internal` gate still reports four unchanged
  inherited files (`orchestrator/queue_purge_status_test.go`, task SQLite
  `base.go`, and two task status-summary projector files), so that global
  formatting receipt is classified FAIL rather than PASS.
- Full ordinary backend suite (`go test ./... -count=1`): classified FAIL in
  two inherited host-fixture tests outside this task's source scope:
  `internal/agentctl/server/api` expects a bundled VS Code remote CLI below
  the Go test binary, and `internal/agentctl/server/config` assumes a login
  shell preserves the generated GitHub CLI shim ahead of the host `gh`.
  Both reproduce in isolation on this host. They are not counted as PASS and
  need a dedicated agentctl fixture/environment repair.
- PostgreSQL: NOT_RUN. `KANDEV_TEST_POSTGRES_DSN` was explicitly unset; no
  PostgreSQL instance was contacted.
- Frontend focused tests: `FRONTEND_NOT_RUN_DEPENDENCIES_ABSENT`; no
  dependencies were installed. The dependency-free backend feature-contract
  test passed.

2026-08-17 successor verification receipt (no runtime, database, or physical
action):

- Integration launch fixtures now provide the same valid, durable profile
  fingerprint shape as production resolvers. `go test` and `go test -race`
  for `internal/integration` both PASS; this preserves the profile-drift
  fail-closed contract rather than weakening it for tests.
- Full backend compile (`go test ./... -run '^$'`) and `go vet ./...`: PASS.
  Targeted ordinary and race suites for physicaldelete, worktree, task SQLite
  repository, task service, task handlers, lifecycle, executor, and backendapp:
  PASS. `git diff --check`: PASS.
- Full ordinary backend results remain classified rather than treated as PASS:
  the agentctl API and config fixture failures reproduce from the Task07 parent;
  an intermittent process/repoclone pair passes in isolated reruns. No failure
  is attributed to this Task08 successor. PostgreSQL remains NOT_RUN because
  `KANDEV_TEST_POSTGRES_DSN` was explicitly unset.
- The existing workspace package-manager wrapper requested a dependency purge,
  which is prohibited. No install or purge was performed. The already-present
  Web Vitest binary ran the four required feature-contract files directly:
  4 files / 12 tests PASS.
