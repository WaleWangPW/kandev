import { describe, expect, it } from "vitest";
import type { TaskSwitcherItem } from "@/components/task/task-switcher";
import { applyGroup, applySort, applyView } from "@/lib/sidebar/apply-view";
import {
  aggregateSidebarTasks,
  type SidebarStepInfo,
  type WorkflowSnapshotMap,
} from "@/components/task/task-session-sidebar-aggregate";
import type { KanbanState } from "@/lib/state/slices";
import {
  getDestinationQueue,
  partitionWipTasks,
  type WipQueueTask,
} from "@/lib/kanban/wip-queue";

const WORKFLOW_ID = "55c97ece-2dd8-4d45-93a2-8104a7fd36a8";
const STEP_ID = "bc390dcb-21c4-451a-9309-249649514f50";
const TASK_ID = "d5a9f791-ca73-4fea-bc07-f3f988827709";

// Production fixture: single-step workflow ("运行中") carrying a single
// admitted IN_PROGRESS task. The user reported that the kanban "进行中"
// column and the sidebar's IN_PROGRESS group were both empty even though
// this task exists on the backend and reaches the snapshot.
//
// Investigation summary: three downstream consumers were validated against
// the fixture with these tests:
//   1. use-all-workflow-snapshots mapSnapshotTask drops the task via
//      stepIds.has(...) — covered end-to-end in
//      hooks/domains/kanban/use-all-workflow-snapshots.test.ts.
//   2. task-session-sidebar-aggregate applyActiveKanbanFallback drops the
//      task via activeStepIds.has(...) — covered below.
//   3. lib/kanban/wip-queue isDestinationQueued misclassifies the task as
//      queued because wipAdmitted is treated as falsy when not strictly
//      `true`. Covered below.
//
// All three passed for the production fixture. The actual gap was found
// one layer earlier: lib/ssr/mapper.ts (SSR boot payload) was the only
// path that failed to copy `wip_admitted`, `queued_for_step_id`, and
// `queued_at` onto kanban.tasks, so any task hydrated before its first
// `kanban.update` arrived had those fields as undefined. Downstream
// partition / count logic treats undefined as truthy in the
// `wipAdmitted !== true` guard, so a queued-overflow card could slip past
// the admitted bucket and an admitted card could fail the count. The fix
// is in lib/ssr/mapper.ts and is regression-locked by mapper.test.ts.

function makeKanbanTask(
  id: string,
  workflowStepId: string,
  state: "IN_PROGRESS",
): KanbanState["tasks"][number] {
  return {
    id,
    workflowId: WORKFLOW_ID,
    workflowStepId,
    title: id,
    position: 0,
    state,
    repositoryIds: [],
    wipAdmitted: true,
  } as unknown as KanbanState["tasks"][number];
}

function makeStep(id: string, position: number): SidebarStepInfo {
  return { id, title: `Step ${id}`, color: "bg-blue-500", position };
}

function makeSwitcher(
  id: string,
  state: "IN_PROGRESS",
  sessionState?: string,
): TaskSwitcherItem {
  return {
    id,
    title: id,
    state,
    sessionState: sessionState as TaskSwitcherItem["sessionState"],
  };
}

describe("queue in-progress visibility — production fixture", () => {
  it("aggregateSidebarTasks keeps the IN_PROGRESS task in the sidebar when the snapshot is fully loaded", () => {
    const snapshots: WorkflowSnapshotMap = {
      [WORKFLOW_ID]: {
        steps: [makeStep(STEP_ID, 0)],
        tasks: [makeKanbanTask(TASK_ID, STEP_ID, "IN_PROGRESS")],
      },
    };
    const result = aggregateSidebarTasks(snapshots, WORKFLOW_ID, [], []);
    const ids = result.allTasks.map((task) => task.id);
    expect(ids).toContain(TASK_ID);
  });

  it("aggregateSidebarTasks keeps the IN_PROGRESS task when activeTasks also reference it (active fallback)", () => {
    const snapshots: WorkflowSnapshotMap = {
      [WORKFLOW_ID]: {
        steps: [makeStep(STEP_ID, 0)],
        tasks: [makeKanbanTask(TASK_ID, STEP_ID, "IN_PROGRESS")],
      },
    };
    const activeTasks = [makeKanbanTask(TASK_ID, STEP_ID, "IN_PROGRESS")];
    const activeSteps = [makeStep(STEP_ID, 0)];
    const result = aggregateSidebarTasks(snapshots, WORKFLOW_ID, activeTasks, activeSteps);
    const ids = result.allTasks.map((task) => task.id);
    expect(ids).toContain(TASK_ID);
  });

  it("applyView (groupBy=state) routes the IN_PROGRESS task into the IN_PROGRESS sidebar group", () => {
    const result = applyGroup([makeSwitcher(TASK_ID, "IN_PROGRESS")], "state");
    const group = result.groups.find((g) => g.key === "IN_PROGRESS");
    expect(group).toBeDefined();
    expect(group?.tasks.map((t) => t.id)).toEqual([TASK_ID]);
  });

  // Candidate (3): wipAdmitted-true + queuedForStepId-empty (the production
  // fixture shape) is admitted, NOT queued. Currently passes thanks to the
  // `task.wipAdmitted !== true` guard, but the strict-`true` semantics also
  // accidentally admit undefined values; lock the intended contract here.
  it("getDestinationQueue leaves an admitted task with no queuedForStepId out of the queued list", () => {
    const task: WipQueueTask = {
      id: TASK_ID,
      workflowStepId: STEP_ID,
      wipAdmitted: true,
      queuedForStepId: null,
    };
    expect(getDestinationQueue([task], STEP_ID)).toEqual([]);
    const { admitted, queued } = partitionWipTasks([task], STEP_ID);
    expect(admitted.map((t) => t.id)).toEqual([TASK_ID]);
    expect(queued).toEqual([]);
  });

  it("applySort with view key=state surfaces the IN_PROGRESS task before backlog tasks", () => {
    const items: TaskSwitcherItem[] = [
      makeSwitcher("todo-1", "IN_PROGRESS", undefined),
      makeSwitcher(TASK_ID, "IN_PROGRESS", "RUNNING"),
    ];
    const sorted = applySort(
      items,
      { key: "state", direction: "asc" },
      [],
    );
    const order = sorted.map((t) => t.id);
    expect(order).toContain(TASK_ID);
  });
});