import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({
  teams: vi.fn(),
  boards: vi.fn(),
  snapshot: vi.fn(),
  update: vi.fn(),
}));

vi.mock("@/lib/api/domains/azure-devops-api", () => ({
  listAzureDevOpsTeams: apiMocks.teams,
  listAzureDevOpsBoards: apiMocks.boards,
  getAzureDevOpsBoardSnapshot: apiMocks.snapshot,
  updateAzureDevOpsBoardWorkItem: apiMocks.update,
}));

import { useAzureDevOpsBoard } from "./use-azure-devops-board";

const boardSnapshot = {
  board: {
    columns: [
      { id: "todo", name: "To Do" },
      { id: "done", name: "Done" },
    ],
  },
  items: [
    { id: 1, revision: 1, title: "First", columnId: "todo", columnDone: false },
    { id: 2, revision: 1, title: "Second", columnId: "todo", columnDone: false },
  ],
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((next, fail) => {
    resolve = next;
    reject = fail;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  vi.clearAllMocks();
  apiMocks.boards.mockResolvedValue({ boards: [] });
  apiMocks.snapshot.mockResolvedValue({ board: { columns: [] }, items: [] });
});
afterEach(cleanup);

describe("useAzureDevOpsBoard", () => {
  it("restores valid remembered team and board IDs", async () => {
    apiMocks.teams.mockResolvedValue({
      teams: [
        { id: "team-a", name: "Team A" },
        { id: "team-b", name: "Team B" },
      ],
    });
    apiMocks.boards.mockResolvedValue({
      boards: [
        { id: "board-a", name: "Board A" },
        { id: "board-b", name: "Board B" },
      ],
    });

    const { result } = renderHook(() =>
      useAzureDevOpsBoard("workspace-a", "project-1", {
        teamId: "team-b",
        boardId: "board-b",
      }),
    );

    await waitFor(() => expect(result.current.boardId).toBe("board-b"));
    expect(result.current.teamId).toBe("team-b");
  });

  it("ignores stale team discovery after the workspace changes", async () => {
    const stale = deferred<{ teams: Array<{ id: string; name: string }> }>();
    apiMocks.teams
      .mockReturnValueOnce(stale.promise)
      .mockResolvedValueOnce({ teams: [{ id: "new", name: "New team" }] });

    const { result, rerender } = renderHook(
      ({ workspaceId }) => useAzureDevOpsBoard(workspaceId, "project-1"),
      { initialProps: { workspaceId: "workspace-a" } },
    );
    rerender({ workspaceId: "workspace-b" });
    await waitFor(() => expect(result.current.teamId).toBe("new"));

    await act(async () => stale.resolve({ teams: [{ id: "old", name: "Old team" }] }));
    expect(result.current.teamId).toBe("new");
  });

  it("restores only a rejected optimistic update and keeps its error visible", async () => {
    apiMocks.teams.mockResolvedValue({ teams: [{ id: "team-1", name: "Team" }] });
    apiMocks.boards.mockResolvedValue({ boards: [{ id: "board-1", name: "Board" }] });
    const rejectedUpdate = deferred<never>();
    apiMocks.snapshot.mockResolvedValue(boardSnapshot);
    apiMocks.update
      .mockReturnValueOnce(rejectedUpdate.promise)
      .mockResolvedValueOnce({ ...boardSnapshot.items[1], columnId: "done", revision: 2 });

    const { result } = renderHook(() => useAzureDevOpsBoard("workspace-a", "project-1"));
    await waitFor(() => expect(result.current.snapshot?.items).toHaveLength(2));

    const first = result.current.moveItem(result.current.snapshot!.items[0], "done");
    await waitFor(() => expect(result.current.snapshot?.items[0].columnId).toBe("done"));
    await expect(
      result.current.moveItem(result.current.snapshot!.items[1], "done"),
    ).resolves.toBeUndefined();
    rejectedUpdate.reject(new Error("update failed"));
    await expect(first).rejects.toThrow("update failed");

    await waitFor(() => {
      expect(result.current.snapshot?.items[0].columnId).toBe("todo");
      expect(result.current.snapshot?.items[1].columnId).toBe("done");
      expect(result.current.error).toBe("update failed");
    });
  });
});
