---
id: "23-provider-presets"
title: "Azure provider presets"
status: pending
wave: 11
depends_on: []
plan: "plan.md"
spec: "../../specs/azure-devops-integration/spec.md"
---

# Task 23: Azure Provider Presets

## Acceptance

- Workspace settings return current built-in work-item/PR default queries and
  quick actions when overrides are null, and preserve explicit customized lists.
- PATCH uses omitted-vs-null semantics per preset family; reset removes the
  override so future built-in improvements apply.
- Preset normalization rejects invalid kinds/filters, drops empty action
  labels, assigns missing IDs, and never stores credentials or result data.

## Verification

- `go test ./internal/azuredevops -run 'Test(WorkspaceSettings|DefaultQueryPresets|ActionPresets)'` from `apps/backend`.
- `pnpm --filter @kandev/web test -- --run components/azure-devops/azure-devops-presets.test.ts` from `apps`.

## Files Likely Touched

- `apps/backend/internal/azuredevops/models.go`
- `apps/backend/internal/azuredevops/store.go`
- `apps/backend/internal/azuredevops/workspace_settings.go`
- `apps/backend/internal/azuredevops/workspace_settings_test.go`
- `apps/backend/internal/azuredevops/controller.go`
- `apps/backend/internal/azuredevops/controller_test.go`
- `apps/web/lib/types/azure-devops.ts`
- `apps/web/lib/api/domains/azure-devops-api.ts`
- `apps/web/components/azure-devops/azure-devops-presets.ts`
- `apps/web/components/azure-devops/azure-devops-action-presets.ts`

## Dependencies

None.

## Parallelism

Sequential. Query/action preset families share one workspace settings patch.

## Inputs

- Spec: query/action preset data and reset scenarios.
- GitHub `workspace_settings_service.go`, `action_presets_service.go`, and
  frontend default-query/action preset resolvers.
- ADR 0030 workspace-scoped integration settings.

## Risks

- Preserve existing Azure saved views while adding patchable settings; do not
  make config replacement erase unrelated preset fields.

## Output Contract

Report default/override semantics, RED/GREEN commands, files changed, migration
behavior, risks, and update task/plan status.
