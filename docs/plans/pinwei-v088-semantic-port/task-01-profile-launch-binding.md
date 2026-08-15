---
id: "01-profile-launch-binding"
title: "Preserve explicit profile launch bindings"
status: pending
wave: 1
depends_on: []
plan: "plan.md"
spec: "../../specs/no-silent-model-fallback/spec.md"
---

# Task 01: Preserve explicit profile launch bindings

- **Acceptance:** User-modified non-empty model/mode values survive catalog drift; system-managed empty fields still seed normally.
- **Acceptance:** Prepared executions carry a secret-free deterministic fingerprint; every launch path blocks drift before process creation while legacy empty fingerprints remain compatible.
- **Acceptance:** Secret-store or reveal failure returns sanitized `BLOCKED_PROFILE_SECRET`, supplies no partial environment, and starts no process.
- **Verification:** `cd apps/backend && env -u KANDEV_TEST_POSTGRES_DSN go test ./internal/agent/settings/controller ./internal/agent/runtime/lifecycle ./internal/orchestrator/executor ./internal/backendapp && env -u KANDEV_TEST_POSTGRES_DSN go test -race ./internal/agent/runtime/lifecycle ./internal/orchestrator/executor && env -u KANDEV_TEST_POSTGRES_DSN go vet ./internal/agent/settings/controller ./internal/agent/runtime/lifecycle ./internal/orchestrator/executor ./internal/backendapp`
- **Files likely touched:** `internal/agent/settings/controller/reconciler.go`; `internal/agent/runtime/lifecycle/profile_env.go`; new `profile_fingerprint.go`; lifecycle resolver/types/manager launch/startup/execution/passthrough files; `internal/backendapp/adapters.go`; `internal/orchestrator/executor/executor*.go`; adjacent tests.
- **Dependencies:** None.
- **Parallelism:** sequential; Task 02 is disjoint and may be delegated only by explicit user instruction.
- **Inputs:** no-silent-model-fallback persistent binding amendment; plan Profile launch binding section.

## Results

Pending.
