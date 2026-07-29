"use client";

import { useCallback, useEffect, useState } from "react";
import {
  getAzureDevOpsBoardSnapshot,
  listAzureDevOpsBoards,
  listAzureDevOpsTeams,
  updateAzureDevOpsBoardWorkItem,
} from "@/lib/api/domains/azure-devops-api";
import type {
  AzureDevOpsBoardSnapshot,
  AzureDevOpsBoardWorkItem,
  AzureDevOpsBoardWorkItemUpdate,
  AzureDevOpsBoardReference,
  AzureDevOpsTeam,
} from "@/lib/types/azure-devops";

type BoardWorkItemChanges = Omit<AzureDevOpsBoardWorkItemUpdate, "revision">;

export type AzureDevOpsBoardEditorValues = Pick<
  BoardWorkItemChanges,
  "title" | "assignedTo" | "tags" | "columnId"
>;

function errorMessage(cause: unknown, fallback: string): string {
  return cause instanceof Error ? cause.message : fallback;
}

function replaceBoardItem(
  snapshot: AzureDevOpsBoardSnapshot | null,
  id: number,
  item: AzureDevOpsBoardWorkItem,
): AzureDevOpsBoardSnapshot | null {
  if (!snapshot) return snapshot;
  return {
    ...snapshot,
    items: snapshot.items.map((candidate) => (candidate.id === id ? item : candidate)),
  };
}

function useBoardDiscovery(workspaceId: string | undefined, projectId: string) {
  const [teams, setTeams] = useState<AzureDevOpsTeam[]>([]);
  const [boards, setBoards] = useState<AzureDevOpsBoardReference[]>([]);
  const [teamId, setTeamId] = useState("");
  const [boardId, setBoardId] = useState("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!workspaceId || !projectId) {
      setTeams([]);
      setTeamId("");
      return;
    }
    let cancelled = false;
    setBoards([]);
    setTeamId("");
    setBoardId("");
    setError(null);
    listAzureDevOpsTeams(workspaceId, projectId)
      .then((result) => {
        if (!cancelled) {
          setTeams(result.teams);
          setTeamId(result.teams[0]?.id ?? "");
        }
      })
      .catch((cause) => !cancelled && setError(errorMessage(cause, "Unable to load teams")));
    return () => {
      cancelled = true;
    };
  }, [projectId, workspaceId]);

  useEffect(() => {
    if (!workspaceId || !projectId || !teamId) {
      setBoards([]);
      setBoardId("");
      return;
    }
    let cancelled = false;
    setBoardId("");
    listAzureDevOpsBoards(workspaceId, projectId, teamId)
      .then((result) => {
        if (!cancelled) {
          setBoards(result.boards);
          setBoardId(result.boards[0]?.id ?? "");
        }
      })
      .catch((cause) => !cancelled && setError(errorMessage(cause, "Unable to load boards")));
    return () => {
      cancelled = true;
    };
  }, [projectId, teamId, workspaceId]);

  return { teams, boards, teamId, setTeamId, boardId, setBoardId, error };
}

export function useAzureDevOpsBoard(workspaceId: string | undefined, projectId: string) {
  const discovery = useBoardDiscovery(workspaceId, projectId);
  const { teamId, boardId } = discovery;
  const [snapshot, setSnapshot] = useState<AzureDevOpsBoardSnapshot | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reloadSnapshot = useCallback(async () => {
    if (!workspaceId || !projectId || !teamId || !boardId) return;
    setLoading(true);
    setError(null);
    try {
      const next = await getAzureDevOpsBoardSnapshot(workspaceId, projectId, teamId, boardId);
      setSnapshot(next);
    } catch (cause) {
      setError(errorMessage(cause, "Unable to load board"));
    } finally {
      setLoading(false);
    }
  }, [boardId, projectId, teamId, workspaceId]);

  useEffect(() => {
    let cancelled = false;
    if (!workspaceId || !projectId || !teamId || !boardId) return;
    setLoading(true);
    setError(null);
    getAzureDevOpsBoardSnapshot(workspaceId, projectId, teamId, boardId)
      .then((next) => !cancelled && setSnapshot(next))
      .catch((cause) => !cancelled && setError(errorMessage(cause, "Unable to load board")))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [boardId, projectId, teamId, workspaceId]);

  const updateItem = useCallback(
    async (item: AzureDevOpsBoardWorkItem, values: BoardWorkItemChanges) => {
      if (!workspaceId || !teamId || !boardId) return;
      const previous = snapshot;
      const optimistic = { ...item, ...values };
      setSnapshot((current) => replaceBoardItem(current, item.id, optimistic));
      try {
        const updated = await updateAzureDevOpsBoardWorkItem(
          workspaceId,
          projectId,
          teamId,
          boardId,
          item.id,
          { revision: item.revision, ...values },
        );
        setSnapshot((current) => replaceBoardItem(current, item.id, updated));
      } catch (cause) {
        setSnapshot(previous);
        setError(errorMessage(cause, "Unable to update work item"));
        void reloadSnapshot();
        throw cause;
      }
    },
    [boardId, projectId, reloadSnapshot, snapshot, teamId, workspaceId],
  );

  const moveItem = useCallback(
    async (item: AzureDevOpsBoardWorkItem, columnId: string) => {
      if (item.columnId !== columnId) await updateItem(item, { columnId });
    },
    [updateItem],
  );

  const saveItem = useCallback(
    (item: AzureDevOpsBoardWorkItem, values: AzureDevOpsBoardEditorValues) =>
      updateItem(item, values),
    [updateItem],
  );

  return {
    teams: discovery.teams,
    boards: discovery.boards,
    teamId,
    setTeamId: discovery.setTeamId,
    boardId,
    setBoardId: discovery.setBoardId,
    snapshot,
    loading,
    error: discovery.error ?? error,
    moveItem,
    saveItem,
  };
}
