---
id: "08-migration-verification"
title: "Verify migrations and adversarial safety matrix"
status: pending
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

Pending.
