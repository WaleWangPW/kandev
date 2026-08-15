---
id: "02-worktree-failure-atomicity"
title: "Preserve worktrees when physical removal fails"
status: pending
wave: 1
depends_on: []
plan: "plan.md"
spec: "../../specs/tasks/archived-resource-safety/spec.md"
---

# Task 02: Preserve worktrees when physical removal fails

- **Acceptance:** A physical worktree removal error causes zero subsequent branch, store, cache, path, or Git-registration mutation.
- **Acceptance:** Batch cleanup evicts cache only for successful items and returns the first failure without hiding later results.
- **Verification:** `cd apps/backend && env -u KANDEV_TEST_POSTGRES_DSN go test ./internal/worktree -run 'Cleanup|Remove|SharedReference' -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go test -race ./internal/worktree -run 'Cleanup|Remove|SharedReference' -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go vet ./internal/worktree`
- **Files likely touched:** `internal/worktree/manager_cleanup.go`; new `internal/worktree/manager_cleanup_error_test.go`; existing cleanup fixture tests only when needed.
- **Dependencies:** None.
- **Parallelism:** parallel-safe with Task 01; exact files are disjoint and no shared schema/config is touched.
- **Inputs:** archived-resource-safety Failure modes; plan Worktree failure atomicity section.

## Results

Pending.
