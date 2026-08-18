import { describe, expect, it } from "vitest";
import type { TaskSession } from "@/lib/types/http";
import {
  mayApplyRecoveredSession,
  recoveredSessionFromResponse,
} from "./chat-input-container";

const staleSession = {
  id: "session-1",
  task_id: "task-1",
  state: "WAITING_FOR_INPUT",
  error_message: "old failure",
  started_at: "2026-08-17T00:00:00Z",
  updated_at: "2026-08-17T00:00:00Z",
} as TaskSession;

describe("recoveredSessionFromResponse", () => {
  it("clears a stale recovery error only for the matching authoritative response", () => {
    expect(
      recoveredSessionFromResponse(
        staleSession,
        { success: true, task_id: "task-1", session_id: "session-1", state: "WAITING_FOR_INPUT" },
        "task-1",
        "session-1",
      ),
    ).toMatchObject({ state: "WAITING_FOR_INPUT", error_message: "" });

    expect(
      recoveredSessionFromResponse(
        staleSession,
        {
          success: true,
          task_id: "task-other",
          session_id: "session-1",
          state: "WAITING_FOR_INPUT",
        },
        "task-1",
        "session-1",
      ),
    ).toBeNull();
  });
});

describe("mayApplyRecoveredSession", () => {
  it("returns true when the session is still in a recoverable state", () => {
    expect(
      mayApplyRecoveredSession(
        staleSession,
        { success: true, task_id: "task-1", session_id: "session-1", state: "WAITING_FOR_INPUT" },
        "task-1",
        "session-1",
      ),
    ).toBe(true);
  });

  it("refuses to apply a recovery reply when the in-memory session has been cancelled", () => {
    // `setTaskSession` covers the client state. A cancellation event
    // arriving on the WS would push the session out of the recoverable
    // bucket; the recovery reply's `state: WAITING_FOR_INPUT` would
    // otherwise overwrite that cancellation silently.
    const cancelled = { ...staleSession, state: "CANCELLED" } as TaskSession;
    expect(
      mayApplyRecoveredSession(
        cancelled,
        { success: true, task_id: "task-1", session_id: "session-1", state: "WAITING_FOR_INPUT" },
        "task-1",
        "session-1",
      ),
    ).toBe(false);
  });

  it("refuses to apply a recovery reply when the session has failed mid-flight", () => {
    const failed = { ...staleSession, state: "FAILED" } as TaskSession;
    expect(
      mayApplyRecoveredSession(
        failed,
        { success: true, task_id: "task-1", session_id: "session-1", state: "WAITING_FOR_INPUT" },
        "task-1",
        "session-1",
      ),
    ).toBe(false);
  });

  it("refuses to apply a recovery reply when the response is missing", () => {
    expect(mayApplyRecoveredSession(staleSession, null, "task-1", "session-1")).toBe(false);
  });

  it("refuses to apply a recovery reply when the session has been removed", () => {
    expect(
      mayApplyRecoveredSession(
        undefined,
        { success: true, task_id: "task-1", session_id: "session-1", state: "WAITING_FOR_INPUT" },
        "task-1",
        "session-1",
      ),
    ).toBe(false);
  });
});
