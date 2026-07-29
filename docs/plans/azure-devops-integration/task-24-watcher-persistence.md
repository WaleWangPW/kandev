---
id: "24-watcher-persistence"
title: "Azure watcher persistence"
status: pending
wave: 12
depends_on: ["22-task-work-item-links", "23-provider-presets"]
plan: "plan.md"
spec: "../../specs/azure-devops-integration/spec.md"
---

# Task 24: Azure Watcher Persistence

## Acceptance

- Work-item and pull-request watcher stores implement workspace-scoped CRUD,
  enabled listing, generation reservations, task attachment, release, errors,
  reset preview/reset, and cleanup-policy deletion.
- Controllers require workspace authorization for list/create and authorize the
  watch's owner before ID-based mutations without leaking another workspace's
  existence.
- Poll interval defaults to 300 seconds, clamps below 60 seconds, and
  `max_inflight_tasks` uses omitted/zero/value semantics shared by other watches.

## Verification

- `go test ./internal/azuredevops -run 'Test(WorkItem|PullRequest)Watch(Store|Controller|Reset|Authorization)'` from `apps/backend`.

## Files Likely Touched

- `apps/backend/internal/azuredevops/watch_models.go`
- `apps/backend/internal/azuredevops/store_watches.go`
- `apps/backend/internal/azuredevops/store_watches_test.go`
- `apps/backend/internal/azuredevops/service_watch_reset.go`
- `apps/backend/internal/azuredevops/service_watch_reset_test.go`
- `apps/backend/internal/azuredevops/controller_watches.go`
- `apps/backend/internal/azuredevops/controller_watch_reset.go`
- `apps/backend/internal/azuredevops/controller_test.go`
- `apps/backend/internal/azuredevops/lifecycle.go`

## Dependencies

Tasks 22-23.

## Parallelism

Sequential. Both watcher kinds share schema, reset, cleanup, and authorization.

## Inputs

- Spec: Azure watches data model/API/state guarantees.
- GitLab watch models/store/reset as the closest two-kind provider pattern.
- `internal/watchreset` shared cleanup helper.

## Risks

- Generation must be included in every reservation write/update condition so a
  reset cannot let an old dispatch attach a task to new ownership.

## Output Contract

Report schema and state transitions, RED/GREEN commands, files changed,
authorization evidence, risks, and update task/plan status.
