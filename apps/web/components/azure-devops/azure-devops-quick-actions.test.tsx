import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn() }));
vi.mock("@/lib/api/domains/azure-devops-api", () => ({
  getAzureDevOpsWorkspaceSettings: mocks.get,
  updateAzureDevOpsWorkspaceSettings: mocks.update,
}));
vi.mock("@/components/toast-provider", () => ({ useToast: () => ({ toast: vi.fn() }) }));

import { AzureDevOpsQuickActionsSection } from "./azure-devops-quick-actions";

beforeEach(() => {
  mocks.get.mockResolvedValue({
    workItemActions: [
      {
        id: "implement",
        label: "Implement",
        hint: "Build it",
        icon: "code",
        promptTemplate: "Implement {{url}}",
      },
    ],
    pullRequestActions: [
      {
        id: "review",
        label: "Review",
        hint: "Read it",
        icon: "eye",
        promptTemplate: "Review {{url}}",
      },
    ],
  });
});
afterEach(cleanup);

describe("AzureDevOpsQuickActionsSection", () => {
  it("renders editable work-item and pull-request actions from workspace settings", async () => {
    render(<AzureDevOpsQuickActionsSection workspaceId="workspace-1" />);
    await waitFor(() =>
      expect((screen.getByLabelText("Work item action label 1") as HTMLInputElement).value).toBe(
        "Implement",
      ),
    );
    expect((screen.getByLabelText("Pull request action label 1") as HTMLInputElement).value).toBe(
      "Review",
    );
  });
});
