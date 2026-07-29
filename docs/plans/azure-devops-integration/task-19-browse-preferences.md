---
id: "19-browse-preferences"
title: "Portable Azure browse preferences"
status: pending
wave: 11
depends_on: []
plan: "plan.md"
spec: "../../specs/azure-devops-integration/spec.md"
---

# Task 19: Portable Azure Browse Preferences

## Acceptance

- Backend user settings expose and patch
  `azure_devops_browse_preferences`, keyed by workspace, without browser-storage
  hydration or persistence.
- Azure browse restores mode, scope, project/team/board/column, and work-item/PR
  filters only after settings load; rapid changes use queued writes and stale
  responses cannot overwrite the latest state.
- Missing/inaccessible remembered provider IDs fall back through the existing
  valid-child defaults while preserving other valid preferences.

## Verification

- `go test ./internal/user/... -run AzureDevOpsBrowsePreferences` from `apps/backend`.
- `pnpm --filter @kandev/web test -- --run lib/ssr/user-settings.test.ts hooks/domains/azure-devops/use-azure-devops-preferences.test.tsx hooks/domains/azure-devops/use-azure-devops-board.test.tsx` from `apps`.

## Files Likely Touched

- `apps/backend/internal/user/models/models.go`
- `apps/backend/internal/user/dto/dto.go`
- `apps/backend/internal/user/service/service.go`
- `apps/backend/internal/user/store/sqlite.go`
- `apps/backend/internal/user/service/service_test.go`
- `apps/backend/internal/user/store/sqlite_test.go`
- `apps/web/lib/types/http-user-settings.ts`
- `apps/web/lib/state/slices/settings/types.ts`
- `apps/web/lib/ssr/user-settings.ts`
- `apps/web/hooks/domains/azure-devops/use-azure-devops-preferences.ts`
- `apps/web/hooks/domains/azure-devops/use-azure-devops-preferences.test.tsx`
- `apps/web/hooks/domains/azure-devops/use-azure-devops-board.ts`
- `apps/web/app/azure-devops/azure-devops-page-client.tsx`

## Dependencies

None.

## Parallelism

Sequential. It spans the shared user-settings contract and Azure page state.

## Inputs

- Spec: Azure browse preferences data model and restore scenario.
- ADR 0041 backend-owned portable user settings.
- `apps/web/lib/user-settings-sync.ts` queued update pattern.

## Risks

- Preference hydration must not trigger default searches against a transient
  pre-hydration selection or persist a fallback before discovery settles.

## Output Contract

Report setting shape, fallback rules, RED/GREEN commands, files changed,
browser-storage audit, risks, and update task/plan status.
