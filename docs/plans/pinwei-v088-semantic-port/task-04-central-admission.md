---
id: "04-central-admission"
title: "Port central physical-delete admission"
status: pending
wave: 3
depends_on: ["03-environment-repo-generation-cas"]
plan: "plan.md"
spec: "../../specs/tasks/archived-resource-safety/spec.md"
---

# Task 04: Port central physical-delete admission

- **Acceptance:** All typed managed-root actions share canonical inventory, locks, and provisional leases; nil admission fails before dependent repository/Git/filesystem/runtime access.
- **Acceptance:** Unknown/future/malformed cleanup rows fail inventory closed, while known generic lifecycle rows and strict archived-resource anchors remain distinct.
- **Acceptance:** Both shipped flags are complete across backend/frontend contracts, disabled in all profiles, and no physical executor can succeed.
- **Acceptance:** A dependency-free static backend test reads the shipped frontend feature types/defaults and proves the exact two keys default false. When existing frontend dependencies are present, the focused Vitest contract also passes; when absent, verification records `FRONTEND_NOT_RUN_DEPENDENCIES_ABSENT` and does not install anything.
- **Verification:** `cd apps/backend && env -u KANDEV_TEST_POSTGRES_DSN go test ./internal/physicaldelete ./internal/worktree ./internal/common/config ./internal/runtimeflags ./internal/profiles ./internal/backendapp -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go test ./internal/backendapp -run FeatureContract -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go test -race ./internal/physicaldelete ./internal/worktree -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go vet ./internal/physicaldelete ./internal/worktree ./internal/backendapp`
- **Verification:** From the repository root: `if test -d apps/node_modules; then cd apps && pnpm --filter @kandev/web test -- lib/state/slices/features/features-contract.test.ts lib/state/slices/features/features-slice.test.ts app/actions/features.test.ts lib/api/domains/runtime-flags-api.test.ts; else echo FRONTEND_NOT_RUN_DEPENDENCIES_ABSENT; fi`. Dependency installation is forbidden.
- **Files likely touched:** new `internal/physicaldelete/*`; worktree admission seams; `profiles.yaml`; typed config/runtimeflags/profiles; backend boot wiring; frontend feature types/defaults; focused tests.
- **Dependencies:** Task 03.
- **Parallelism:** sequential because every later physical consumer depends on this contract.
- **Inputs:** archived-resource-safety What/Failure modes; ADR 0009; runtime feature-flag ADRs.

## Results

Pending.
