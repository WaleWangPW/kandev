import { describe, expect, it } from "vitest";
import type { TaskSession } from "@/lib/types/http";
import { recoveredSessionFromResponse } from "./chat-input-container";

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
