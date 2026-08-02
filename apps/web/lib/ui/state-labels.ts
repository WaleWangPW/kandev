import type { TaskState, TaskSessionState } from "@/lib/types/http";
import { translate } from "@/lib/i18n/locale";

const TASK_STATE_LABELS: Record<TaskState, string> = {
  CREATED: "Created",
  SCHEDULING: "Scheduling",
  TODO: "To do",
  IN_PROGRESS: "In progress",
  REVIEW: "Review",
  BLOCKED: "Blocked",
  WAITING_FOR_INPUT: "Waiting for input",
  COMPLETED: "Completed",
  FAILED: "Failed",
  CANCELLED: "Cancelled",
};

const TASK_SESSION_STATE_LABELS: Record<TaskSessionState, string> = {
  CREATED: "Created",
  IDLE: "Idle",
  STARTING: "Starting",
  RUNNING: "Running",
  WAITING_FOR_INPUT: "Waiting for input",
  COMPLETED: "Completed",
  FAILED: "Failed",
  CANCELLED: "Cancelled",
};

export function formatTaskStateLabel(state: TaskState | null | undefined): string {
  if (!state) return translate("Not started");
  return translate(TASK_STATE_LABELS[state] ?? state);
}

export function formatTaskSessionStateLabel(state: TaskSessionState | null | undefined): string {
  if (!state) return "";
  return translate(TASK_SESSION_STATE_LABELS[state] ?? state);
}
