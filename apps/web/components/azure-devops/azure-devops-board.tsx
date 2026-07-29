"use client";

/* eslint-disable max-lines, max-lines-per-function */

import { useEffect, useMemo, useState } from "react";
import { IconExternalLink, IconGripVertical } from "@tabler/icons-react";
import { Alert, AlertDescription } from "@kandev/ui/alert";
import { Badge } from "@kandev/ui/badge";
import { Button } from "@kandev/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@kandev/ui/card";
import { Input } from "@kandev/ui/input";
import { Label } from "@kandev/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@kandev/ui/select";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@kandev/ui/sheet";
import { Textarea } from "@kandev/ui/textarea";
import { useResponsiveBreakpoint } from "@/hooks/use-responsive-breakpoint";
import {
  getAzureDevOpsBoardSnapshot,
  listAzureDevOpsBoards,
  listAzureDevOpsTeams,
  updateAzureDevOpsBoardWorkItem,
} from "@/lib/api/domains/azure-devops-api";
import type {
  AzureDevOpsBoard,
  AzureDevOpsBoardReference,
  AzureDevOpsBoardSnapshot,
  AzureDevOpsBoardWorkItem,
  AzureDevOpsProject,
  AzureDevOpsTeam,
} from "@/lib/types/azure-devops";
import { groupAzureDevOpsBoardItems } from "./azure-devops-board-view";

type Props = {
  workspaceId?: string;
  projectId: string;
  projects: AzureDevOpsProject[];
  onProjectChange: (projectId: string) => void;
};

function BoardCard({
  item,
  onOpen,
  onDragStart,
}: {
  item: AzureDevOpsBoardWorkItem;
  onOpen: () => void;
  onDragStart: () => void;
}) {
  return (
    <Card
      draggable
      onDragStart={onDragStart}
      onClick={onOpen}
      className="cursor-pointer gap-2 py-3 transition hover:border-primary/50"
      data-testid={`azure-board-card-${item.id}`}
    >
      <CardHeader className="px-3 py-0">
        <CardTitle className="flex items-start gap-2 text-sm font-medium">
          <IconGripVertical className="mt-0.5 hidden h-4 w-4 shrink-0 text-muted-foreground md:block" />
          <span className="line-clamp-2">{item.title || "Untitled work item"}</span>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-2 px-3 text-xs text-muted-foreground">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="outline">#{item.id}</Badge>
          {item.type && <Badge variant="secondary">{item.type}</Badge>}
          {item.assignedTo && <span className="truncate">{item.assignedTo}</span>}
        </div>
        {!!item.tags?.length && <div className="truncate">{item.tags.join(" · ")}</div>}
      </CardContent>
    </Card>
  );
}

function BoardEditor({
  item,
  board,
  open,
  onOpenChange,
  onSave,
}: {
  item: AzureDevOpsBoardWorkItem | null;
  board: AzureDevOpsBoard;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: (
    item: AzureDevOpsBoardWorkItem,
    values: { title: string; assignedTo: string; tags: string[]; columnId: string },
  ) => Promise<void>;
}) {
  const [title, setTitle] = useState("");
  const [assignedTo, setAssignedTo] = useState("");
  const [tags, setTags] = useState("");
  const [columnId, setColumnId] = useState("");
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    if (!item) return;
    setTitle(item.title);
    setAssignedTo(item.assignedTo ?? "");
    setTags(item.tags?.join(", ") ?? "");
    setColumnId(item.columnId);
  }, [item]);
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>Edit work item {item ? `#${item.id}` : ""}</SheetTitle>
        </SheetHeader>
        {item && (
          <div className="space-y-4 p-4">
            <div className="space-y-1.5">
              <Label htmlFor="azure-board-title">Title</Label>
              <Input
                id="azure-board-title"
                value={title}
                onChange={(event) => setTitle(event.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="azure-board-assignee">Assignee</Label>
              <Input
                id="azure-board-assignee"
                value={assignedTo}
                onChange={(event) => setAssignedTo(event.target.value)}
                placeholder="Unassigned"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="azure-board-tags">Tags</Label>
              <Textarea
                id="azure-board-tags"
                value={tags}
                onChange={(event) => setTags(event.target.value)}
                placeholder="tag-one, tag-two"
              />
            </div>
            <div className="space-y-1.5">
              <Label>Column</Label>
              <Select value={columnId} onValueChange={setColumnId}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {board.columns.map((column) => (
                    <SelectItem key={column.id} value={column.id}>
                      {column.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button
              type="button"
              className="w-full"
              disabled={saving || !title.trim()}
              onClick={async () => {
                setSaving(true);
                try {
                  await onSave(item, {
                    title: title.trim(),
                    assignedTo: assignedTo.trim(),
                    tags: tags
                      .split(",")
                      .map((tag) => tag.trim())
                      .filter(Boolean),
                    columnId,
                  });
                  onOpenChange(false);
                } finally {
                  setSaving(false);
                }
              }}
            >
              {saving ? "Saving…" : "Save changes"}
            </Button>
            {item.webUrl && (
              <Button asChild variant="ghost" className="w-full">
                <a href={item.webUrl} target="_blank" rel="noreferrer">
                  Open in Azure DevOps <IconExternalLink className="ml-2 h-4 w-4" />
                </a>
              </Button>
            )}
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}

export function AzureDevOpsBoard({ workspaceId, projectId, projects, onProjectChange }: Props) {
  const { isMobile: phone } = useResponsiveBreakpoint();
  const [teams, setTeams] = useState<AzureDevOpsTeam[]>([]);
  const [boards, setBoards] = useState<AzureDevOpsBoardReference[]>([]);
  const [teamId, setTeamId] = useState("");
  const [boardId, setBoardId] = useState("");
  const [snapshot, setSnapshot] = useState<AzureDevOpsBoardSnapshot | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedColumn, setSelectedColumn] = useState("");
  const [editing, setEditing] = useState<AzureDevOpsBoardWorkItem | null>(null);
  const [dragged, setDragged] = useState<AzureDevOpsBoardWorkItem | null>(null);

  useEffect(() => {
    if (!workspaceId || !projectId) return;
    setTeamId("");
    setBoardId("");
    setSnapshot(null);
    void listAzureDevOpsTeams(workspaceId, projectId)
      .then((result) => {
        setTeams(result.teams);
        setTeamId(result.teams[0]?.id ?? "");
      })
      .catch((cause) => setError(cause instanceof Error ? cause.message : "Unable to load teams"));
  }, [workspaceId, projectId]);
  useEffect(() => {
    if (!workspaceId || !projectId || !teamId) return;
    setBoardId("");
    setSnapshot(null);
    void listAzureDevOpsBoards(workspaceId, projectId, teamId)
      .then((result) => {
        setBoards(result.boards);
        setBoardId(result.boards[0]?.id ?? "");
      })
      .catch((cause) => setError(cause instanceof Error ? cause.message : "Unable to load boards"));
  }, [workspaceId, projectId, teamId]);
  useEffect(() => {
    if (!workspaceId || !projectId || !teamId || !boardId) return;
    setLoading(true);
    setError(null);
    void getAzureDevOpsBoardSnapshot(workspaceId, projectId, teamId, boardId)
      .then((result) => {
        setSnapshot(result);
        setSelectedColumn(result.board.columns[0]?.id ?? "");
      })
      .catch((cause) => setError(cause instanceof Error ? cause.message : "Unable to load board"))
      .finally(() => setLoading(false));
  }, [workspaceId, projectId, teamId, boardId]);

  const groups = useMemo<Map<string, AzureDevOpsBoardWorkItem[]>>(
    () => (snapshot ? groupAzureDevOpsBoardItems(snapshot.board, snapshot.items) : new Map()),
    [snapshot],
  );
  const moveItem = async (item: AzureDevOpsBoardWorkItem, columnId: string) => {
    if (!workspaceId || !snapshot || item.columnId === columnId) return;
    const previous = snapshot;
    setSnapshot({
      ...snapshot,
      items: snapshot.items.map((candidate) =>
        candidate.id === item.id ? { ...candidate, columnId } : candidate,
      ),
    });
    try {
      const updated = await updateAzureDevOpsBoardWorkItem(
        workspaceId,
        projectId,
        teamId,
        boardId,
        item.id,
        { revision: item.revision, columnId },
      );
      setSnapshot((current) =>
        current
          ? {
              ...current,
              items: current.items.map((candidate) =>
                candidate.id === item.id ? updated : candidate,
              ),
            }
          : current,
      );
    } catch (cause) {
      setSnapshot(previous);
      setError(cause instanceof Error ? cause.message : "Unable to update work item");
    }
  };
  const saveItem = async (
    item: AzureDevOpsBoardWorkItem,
    values: { title: string; assignedTo: string; tags: string[]; columnId: string },
  ) => {
    if (!workspaceId) return;
    const updated = await updateAzureDevOpsBoardWorkItem(
      workspaceId,
      projectId,
      teamId,
      boardId,
      item.id,
      { revision: item.revision, ...values },
    );
    setSnapshot((current) =>
      current
        ? {
            ...current,
            items: current.items.map((candidate) =>
              candidate.id === item.id ? updated : candidate,
            ),
          }
        : current,
    );
  };

  if (!projectId)
    return (
      <div className="p-6 text-sm text-muted-foreground">Select a project to view its board.</div>
    );
  return (
    <section
      className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden p-4"
      data-testid="azure-devops-board"
    >
      <div className="flex flex-wrap gap-2">
        <Select value={projectId} onValueChange={onProjectChange}>
          <SelectTrigger className="w-52">
            <SelectValue placeholder="Project" />
          </SelectTrigger>
          <SelectContent>
            {projects.map((project) => (
              <SelectItem key={project.id} value={project.id}>
                {project.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={teamId} onValueChange={setTeamId} disabled={!teams.length}>
          <SelectTrigger className="w-52">
            <SelectValue placeholder="Team" />
          </SelectTrigger>
          <SelectContent>
            {teams.map((team) => (
              <SelectItem key={team.id} value={team.id}>
                {team.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={boardId} onValueChange={setBoardId} disabled={!boards.length}>
          <SelectTrigger className="w-52">
            <SelectValue placeholder="Board" />
          </SelectTrigger>
          <SelectContent>
            {boards.map((board) => (
              <SelectItem key={board.id} value={board.id}>
                {board.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {loading && <div className="p-6 text-sm text-muted-foreground">Loading board…</div>}
      {!loading && snapshot && (
        <>
          {phone && (
            <div className="flex gap-1 overflow-x-auto pb-1">
              {snapshot.board.columns.map((column) => (
                <Button
                  key={column.id}
                  type="button"
                  size="sm"
                  variant={selectedColumn === column.id ? "default" : "outline"}
                  onClick={() => setSelectedColumn(column.id)}
                >
                  {column.name}{" "}
                  <span className="ml-1 opacity-70">{groups.get(column.id)?.length ?? 0}</span>
                </Button>
              ))}
            </div>
          )}
          <div
            className={
              phone
                ? "min-h-0 flex-1 overflow-y-auto"
                : "grid min-h-0 flex-1 auto-cols-fr grid-flow-col gap-3 overflow-x-auto"
            }
          >
            {snapshot.board.columns
              .filter((column) => !phone || column.id === selectedColumn)
              .map((column) => (
                <div
                  key={column.id}
                  className="flex min-w-72 flex-col gap-2 rounded-lg bg-muted/40 p-2"
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={() => {
                    if (dragged) void moveItem(dragged, column.id);
                    setDragged(null);
                  }}
                >
                  <div className="flex items-center justify-between px-1 text-sm font-semibold">
                    <span>{column.name}</span>
                    <Badge variant="secondary">{groups.get(column.id)?.length ?? 0}</Badge>
                  </div>
                  <div className="space-y-2">
                    {(groups.get(column.id) ?? []).map((item) => (
                      <BoardCard
                        key={item.id}
                        item={item}
                        onOpen={() => setEditing(item)}
                        onDragStart={() => setDragged(item)}
                      />
                    ))}
                  </div>
                </div>
              ))}
          </div>
          <BoardEditor
            item={editing}
            board={snapshot.board}
            open={!!editing}
            onOpenChange={(open) => !open && setEditing(null)}
            onSave={saveItem}
          />
        </>
      )}
    </section>
  );
}
