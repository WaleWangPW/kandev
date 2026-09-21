---
id: "06-terminal-resume-rollback"
title: "Fail stale active sessions after resume relaunch errors"
status: done
wave: 5
depends_on:
  - "04-resume-lease-boundary"
plan: "plan.md"
requirements:
  - REQ-AGENTS-AGENT-RESUME-RUNTIME-RECOVERY-001
acceptance_criteria:
  - AC-AGENTS-AGENT-RESUME-RUNTIME-RECOVERY-001.5
system_design:
  - ../../specs/agents/system-design/agent-resume-runtime-recovery.md
---

# Task 06: Fail stale active sessions after resume relaunch errors

## Scope

- Convert a prior `RUNNING` or `STARTING` state to `FAILED` when relaunch fails
  and the session still owns the guarded `STARTING` transition.
- Preserve non-active prior states and concurrent terminal transitions.
- Keep credential-snapshot rollback and ceiling-reservation release unchanged.
- Add focused mapping and real resume-path regression coverage.

## Acceptance

A failed relaunch never advertises `RUNNING` or `STARTING` without a recovered
agent process. The recorded launch error remains visible, and a later explicit
resume can use the existing terminal-state cleanup path. Non-active prior states
and concurrent terminal winners retain their existing behavior.

## Verification

```bash
cd apps/backend && go test ./internal/orchestrator/executor \
  -run 'TestTerminalRollbackState|TestResumeSession_RedirectsRunningToFailedWhenRelaunchFails' \
  -count=1
cd apps/backend && go test ./internal/orchestrator/executor -count=1
python3 scripts/lint-spec-files.py --all
```

## Results

The rollback target now derives from the state observed before resume. Prior
active states become `FAILED`; other states are restored unchanged. Focused and
full executor package tests pass, and the specification package records the
same invariant.
