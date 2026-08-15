---
id: "06-terminal-db-only-actions"
title: "Port terminal DB-only archived-resource actions"
status: pending
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

Pending.
