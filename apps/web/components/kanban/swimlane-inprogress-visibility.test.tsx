import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { StateProvider } from "@/components/state-provider";
import type { Task } from "@/components/kanban-card";
import type { WorkflowStep } from "@/components/kanban-column";
import { SwimlaneGraphContent } from "./swimlane-graph-content";
import { i18n } from "@/lib/i18n";

afterEach(() => {
  cleanup();
});

const STEP_ID = "bc390dcb-21c4-451a-9309-249649514f50";
const TASK_ID = "d5a9f791-ca73-4fea-bc07-f3f988827709";

const STEPS: WorkflowStep[] = [
  { id: STEP_ID, title: "运行中", color: "bg-blue-500" },
];

function makeInProgressTask(): Task {
  return {
    id: TASK_ID,
    title: "Total control",
    workflowStepId: STEP_ID,
    workflowId: "55c97ece-2dd8-4d45-93a2-8104a7fd36a8",
    state: "IN_PROGRESS",
    position: 0,
    wipAdmitted: true,
  } as Task;
}

describe("SwimlaneGraphContent — IN_PROGRESS task visibility (regression for kanban '进行中列为空')", () => {
  it("renders the admitted IN_PROGRESS task as a chip in the only step column", () => {
    const { container } = render(
      <StateProvider>
        <SwimlaneGraphContent
          workflowId="55c97ece-2dd8-4d45-93a2-8104a7fd36a8"
          steps={STEPS}
          tasks={[makeInProgressTask()]}
          onPreviewTask={() => undefined}
        />
      </StateProvider>,
    );
    // The task title is rendered as the chip button text content. The
    // production fixture has title="Total control" + step id=bc390dcb; if
    // the chip is mounted the column should contain that text.
    const html = container.innerHTML;
    if (!html.includes("Total control")) {
      throw new Error(
        `IN_PROGRESS task chip missing from swimlane render; html=${html.slice(0, 500)}`,
      );
    }
    // The step title must also surface so the column header is identifiable.
    expect(html).toContain("运行中");
  });
});