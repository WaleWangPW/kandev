---
id: "05-reconcile-lifecycle"
title: "Port retained-anchor reconcile lifecycle"
status: pending
wave: 4
depends_on: ["01-profile-launch-binding", "04-central-admission"]
plan: "plan.md"
spec: "../../specs/tasks/archived-resource-safety/spec.md"
---

# Task 05: Port retained-anchor reconcile lifecycle

- **Acceptance:** Strict v2/v3 canonical admission, active-scope uniqueness, exact replay, participant-aware lifecycle gates, and generic-job isolation pass fresh/legacy/replay tests.
- **Acceptance:** Completion, unarchive, and interrupted same-attempt recovery are atomic under stable participant locks; stale generations never affect reactivated rows.
- **Acceptance:** Disabled routes/workers are zero-touch; enabled routes require real install-admin auth and return metadata only.
- **Verification:** `cd apps/backend && env -u KANDEV_TEST_POSTGRES_DSN go test ./internal/task/models ./internal/task/repository/sqlite ./internal/task/service ./internal/task/handlers ./internal/backendapp -run 'ArchivedResource|Reconcile|Unarchive|FeatureContract' -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go test -race ./internal/task/repository/sqlite ./internal/task/service ./internal/task/handlers -run 'ArchivedResource|Reconcile|Unarchive' -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go vet ./internal/task/... ./internal/backendapp`
- **Files likely touched:** task archived-resource models; repository interfaces and SQLite migrations/reconcile files; service reconcile/unarchive/worker files; handlers/routes; backend wiring; adjacent tests.
- **Dependencies:** Tasks 01 and 04; this is the mandatory merge point for the profile-binding and archived-resource safety chains.
- **Parallelism:** sequential because models, schema, service, and routes form one security boundary.
- **Inputs:** archived-resource-safety Data model/API/State machine; accepted v0.85 final snapshots as behavioral evidence only.

## Results

Pending.
