---
id: "27-work-item-detail-ui"
title: "Responsive work-item detail"
status: pending
wave: 13
depends_on:
  [
    "19-browse-preferences",
    "20-work-item-detail",
    "21-work-item-mutations",
    "22-task-work-item-links",
    "23-provider-presets",
  ]
plan: "plan.md"
spec: "../../specs/azure-devops-integration/spec.md"
---

# Task 27: Responsive Work-Item Detail

## Acceptance

- Board cards and work-item rows open shared read-only detail showing core
  fields, sanitized description, planning values, discussion paging/retry,
  linked tasks, Azure link, and a visible quick-action Task menu.
- Only Assign to me, Unassign, and board column/split controls mutate the work
  item; quick actions open task creation with provider context and persist the
  resulting work-item association.
- Desktop uses a modal `Dialog`; phone uses one safe-area-aware full-height
  `Drawer` with fixed context/actions, one internal scroll owner, 44px controls,
  and no document horizontal overflow.

## Verification

- `pnpm --filter @kandev/web test -- --run hooks/domains/azure-devops/use-azure-devops-work-item-detail.test.tsx components/azure-devops/azure-devops-work-item-detail.test.tsx components/azure-devops/azure-devops-task-launcher.test.tsx` from `apps`.
- `pnpm --filter @kandev/web typecheck` from `apps`.
- `pnpm --filter @kandev/web lint` from `apps`.

## Files Likely Touched

- `apps/web/lib/types/azure-devops.ts`
- `apps/web/lib/api/domains/azure-devops-api.ts`
- `apps/web/hooks/domains/azure-devops/use-azure-devops-work-item-detail.ts`
- `apps/web/hooks/domains/azure-devops/use-azure-devops-work-item-detail.test.tsx`
- `apps/web/hooks/domains/azure-devops/use-azure-devops-board.ts`
- `apps/web/components/azure-devops/azure-devops-board.tsx`
- `apps/web/components/azure-devops/azure-devops-results.tsx`
- `apps/web/components/azure-devops/azure-devops-work-item-detail.tsx`
- `apps/web/components/azure-devops/azure-devops-task-actions.tsx`
- `apps/web/components/azure-devops/azure-devops-task-launcher.tsx`
- `apps/web/app/azure-devops/azure-devops-page-client.tsx`

## Dependencies

Tasks 19-23.

## Parallelism

Sequential. Detail, board state, task launch, and association cache share one
frontend interaction model.

## Inputs

- Spec: work-item detail/mutation/quick-action scenarios.
- Mobile exemplar: `task-layout.tsx` and
  `session-task-switcher-sheet.tsx` for dedicated full-height detail and
  safe-area-aware drawer geometry.
- Desktop exemplar: existing Kandev detail dialogs; use `@kandev/ui/dialog`.
- Required skills during implementation: `/tdd`, `/mobile-parity`, `/e2e`.

## Mobile Design Contract

- Desktop outcome: inspect one item without losing board context, take the
  allowed mutations, or launch a task.
- Phone entry point: tapping the visible card body opens detail directly from
  the focused column; no intermediate menu.
- Hierarchy: fixed title/type/status header, scrollable description/planning/
  discussion body, then persistent explicit Task and assignment/move actions.
- Surface rationale: detail is dense primary content, so a full-height Drawer
  is preferable to an inset temporary picker; temporary column/action choices
  may use the shared responsive menu treatment.
- Shared hooks own data, paging, mutations, linked tasks, and launch payloads;
  only desktop/mobile composition differs.
- Use `100dvh`, one `min-h-0 overflow-y-auto` body, bottom safe-area clearance,
  focus return, and at least 44px touch targets.

## Risks

- Sanitize Azure HTML before rendering and do not let comment failures close or
  replace already loaded core detail.

## Output Contract

Report responsive compositions, RED/GREEN commands, rendered desktop/mobile
inspection, files changed, risks, and update task/plan status.
