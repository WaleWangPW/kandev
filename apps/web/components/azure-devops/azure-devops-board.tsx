"use client";

import { useMemo, useState } from "react";
import {
  IconChevronLeft,
  IconChevronRight,
  IconExternalLink,
  IconGripVertical,
  IconList,
} from "@tabler/icons-react";
import { Alert, AlertDescription } from "@kandev/ui/alert";
import { Badge } from "@kandev/ui/badge";
import { Button } from "@kandev/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@kandev/ui/card";
import { Drawer, DrawerContent, DrawerHeader, DrawerTitle } from "@kandev/ui/drawer";
import { Input } from "@kandev/ui/input";
import { Label } from "@kandev/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@kandev/ui/select";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@kandev/ui/sheet";
import { Textarea } from "@kandev/ui/textarea";
import {
  type AzureDevOpsBoardEditorValues,
  useAzureDevOpsBoard,
} from "@/hooks/domains/azure-devops/use-azure-devops-board";
import { useResponsiveBreakpoint } from "@/hooks/use-responsive-breakpoint";
import type {
  AzureDevOpsBoard,
  AzureDevOpsBoardWorkItem,
  AzureDevOpsProject,
} from "@/lib/types/azure-devops";
import { groupAzureDevOpsBoardItems } from "./azure-devops-board-view";

type Props = {
  workspaceId?: string;
  projectId: string;
  projects: AzureDevOpsProject[];
  onProjectChange: (projectId: string) => void;
};

type EditorValues = { title: string; assignedTo: string; tags: string; columnId: string };

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
      role="button"
      tabIndex={0}
      onDragStart={onDragStart}
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpen();
        }
      }}
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

function BoardEditorForm({
  item,
  board,
  onSave,
  onClose,
}: {
  item: AzureDevOpsBoardWorkItem;
  board: AzureDevOpsBoard;
  onSave: (item: AzureDevOpsBoardWorkItem, values: AzureDevOpsBoardEditorValues) => Promise<void>;
  onClose: () => void;
}) {
  const [title, setTitle] = useState(item.title);
  const [assignedTo, setAssignedTo] = useState(item.assignedTo ?? "");
  const [tags, setTags] = useState(item.tags?.join(", ") ?? "");
  const [columnId, setColumnId] = useState(item.columnId);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const save = async () => {
    setSaving(true);
    setSaveError(null);
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
      onClose();
    } catch (cause) {
      setSaveError(cause instanceof Error ? cause.message : "Unable to save work item");
    } finally {
      setSaving(false);
    }
  };
  return (
    <div className="space-y-4 p-4">
      {saveError && (
        <Alert variant="destructive">
          <AlertDescription>{saveError}</AlertDescription>
        </Alert>
      )}
      <BoardEditorFields
        board={board}
        values={{ title, assignedTo, tags, columnId }}
        onChange={(next) => {
          if (next.title !== undefined) setTitle(next.title);
          if (next.assignedTo !== undefined) setAssignedTo(next.assignedTo);
          if (next.tags !== undefined) setTags(next.tags);
          if (next.columnId !== undefined) setColumnId(next.columnId);
        }}
      />
      <Button
        type="button"
        className="w-full cursor-pointer"
        disabled={saving || !title.trim()}
        onClick={save}
      >
        {saving ? "Saving…" : "Save changes"}
      </Button>
      {item.webUrl && (
        <Button asChild variant="ghost" className="w-full cursor-pointer">
          <a href={item.webUrl} target="_blank" rel="noreferrer">
            Open in Azure DevOps <IconExternalLink className="ml-2 h-4 w-4" />
          </a>
        </Button>
      )}
    </div>
  );
}

function BoardEditorFields({
  board,
  values,
  onChange,
}: {
  board: AzureDevOpsBoard;
  values: EditorValues;
  onChange: (next: Partial<EditorValues>) => void;
}) {
  return (
    <>
      <div className="space-y-1.5">
        <Label htmlFor="azure-board-title">Title</Label>
        <Input
          id="azure-board-title"
          value={values.title}
          onChange={(event) => onChange({ title: event.target.value })}
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="azure-board-assignee">Assignee</Label>
        <Input
          id="azure-board-assignee"
          value={values.assignedTo}
          onChange={(event) => onChange({ assignedTo: event.target.value })}
          placeholder="Unassigned"
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="azure-board-tags">Tags</Label>
        <Textarea
          id="azure-board-tags"
          value={values.tags}
          onChange={(event) => onChange({ tags: event.target.value })}
          placeholder="tag-one, tag-two"
        />
      </div>
      <div className="space-y-1.5">
        <Label>Column</Label>
        <Select value={values.columnId} onValueChange={(columnId) => onChange({ columnId })}>
          <SelectTrigger data-testid="azure-board-column-select">
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
    </>
  );
}

function BoardEditor({
  item,
  board,
  onOpenChange,
  onSave,
}: {
  item: AzureDevOpsBoardWorkItem | null;
  board: AzureDevOpsBoard;
  onOpenChange: (open: boolean) => void;
  onSave: (item: AzureDevOpsBoardWorkItem, values: AzureDevOpsBoardEditorValues) => Promise<void>;
}) {
  const { isMobile } = useResponsiveBreakpoint();
  if (!item) return null;
  const title = `Edit work item #${item.id}`;
  const form = (
    <BoardEditorForm
      key={`${item.id}-${item.revision}`}
      item={item}
      board={board}
      onSave={onSave}
      onClose={() => onOpenChange(false)}
    />
  );
  if (isMobile)
    return (
      <Drawer open onOpenChange={onOpenChange}>
        <DrawerContent className="max-h-[min(44rem,calc(100dvh-16px-env(safe-area-inset-bottom,0px)))]">
          <DrawerHeader>
            <DrawerTitle>{title}</DrawerTitle>
          </DrawerHeader>
          <div className="min-h-0 overflow-y-auto pb-[env(safe-area-inset-bottom,0px)]">{form}</div>
        </DrawerContent>
      </Drawer>
    );
  return (
    <Sheet open onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>{title}</SheetTitle>
        </SheetHeader>
        {form}
      </SheetContent>
    </Sheet>
  );
}

function MobileColumnNavigator({
  columns,
  selectedColumn,
  counts,
  onChange,
}: {
  columns: AzureDevOpsBoard["columns"];
  selectedColumn: string;
  counts: Map<string, AzureDevOpsBoardWorkItem[]>;
  onChange: (columnId: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const index = Math.max(
    0,
    columns.findIndex((column) => column.id === selectedColumn),
  );
  const column = columns[index];
  const select = (columnId: string) => {
    onChange(columnId);
    setOpen(false);
  };
  return (
    <div className="flex min-h-11 items-center gap-2">
      <Button
        type="button"
        variant="outline"
        size="icon"
        className="h-11 w-11 cursor-pointer"
        aria-label="Previous board column"
        disabled={index === 0}
        onClick={() => onChange(columns[index - 1].id)}
      >
        <IconChevronLeft className="h-4 w-4" />
      </Button>
      <Button
        type="button"
        variant="outline"
        className="min-h-11 flex-1 cursor-pointer justify-between"
        data-testid="azure-board-column-picker"
        onClick={() => setOpen(true)}
      >
        <span>{column?.name ?? "Board column"}</span>
        <span>{counts.get(column?.id ?? "")?.length ?? 0}</span>
        <IconList className="h-4 w-4" />
      </Button>
      <Button
        type="button"
        variant="outline"
        size="icon"
        className="h-11 w-11 cursor-pointer"
        aria-label="Next board column"
        disabled={index >= columns.length - 1}
        onClick={() => onChange(columns[index + 1].id)}
      >
        <IconChevronRight className="h-4 w-4" />
      </Button>
      <Drawer open={open} onOpenChange={setOpen}>
        <DrawerContent className="max-h-[min(32rem,calc(100dvh-16px-env(safe-area-inset-bottom,0px)))]">
          <DrawerHeader>
            <DrawerTitle>Board columns</DrawerTitle>
          </DrawerHeader>
          <div className="min-h-0 overflow-y-auto p-4 pb-[env(safe-area-inset-bottom,0px)]">
            {columns.map((candidate) => (
              <Button
                key={candidate.id}
                type="button"
                variant={candidate.id === selectedColumn ? "default" : "outline"}
                className="mb-2 min-h-11 w-full cursor-pointer justify-between"
                onClick={() => select(candidate.id)}
              >
                <span>{candidate.name}</span>
                <Badge variant="secondary">{counts.get(candidate.id)?.length ?? 0}</Badge>
              </Button>
            ))}
          </div>
        </DrawerContent>
      </Drawer>
    </div>
  );
}

function BoardSelectors({
  projectId,
  projects,
  onProjectChange,
  teamId,
  teams,
  onTeamChange,
  boardId,
  boards,
  onBoardChange,
}: {
  projectId: string;
  projects: AzureDevOpsProject[];
  onProjectChange: (value: string) => void;
  teamId: string;
  teams: Array<{ id: string; name: string }>;
  onTeamChange: (value: string) => void;
  boardId: string;
  boards: Array<{ id: string; name: string }>;
  onBoardChange: (value: string) => void;
}) {
  return (
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
      <Select value={teamId} onValueChange={onTeamChange} disabled={!teams.length}>
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
      <Select value={boardId} onValueChange={onBoardChange} disabled={!boards.length}>
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
  );
}

export function AzureDevOpsBoard({ workspaceId, projectId, projects, onProjectChange }: Props) {
  const { isMobile } = useResponsiveBreakpoint();
  const board = useAzureDevOpsBoard(workspaceId, projectId);
  const [selectedColumn, setSelectedColumn] = useState("");
  const [editing, setEditing] = useState<AzureDevOpsBoardWorkItem | null>(null);
  const [dragged, setDragged] = useState<AzureDevOpsBoardWorkItem | null>(null);
  const groups = useMemo(
    () =>
      board.snapshot
        ? groupAzureDevOpsBoardItems(board.snapshot.board, board.snapshot.items)
        : new Map<string, AzureDevOpsBoardWorkItem[]>(),
    [board.snapshot],
  );
  const columns = board.snapshot?.board.columns ?? [];
  const effectiveColumn = selectedColumn || columns[0]?.id || "";
  if (!projectId)
    return (
      <div className="p-6 text-sm text-muted-foreground">Select a project to view its board.</div>
    );
  return (
    <section
      className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden p-4"
      data-testid="azure-devops-board"
    >
      <BoardSelectors
        projectId={projectId}
        projects={projects}
        onProjectChange={onProjectChange}
        teamId={board.teamId}
        teams={board.teams}
        onTeamChange={board.setTeamId}
        boardId={board.boardId}
        boards={board.boards}
        onBoardChange={board.setBoardId}
      />
      {board.error && (
        <Alert variant="destructive">
          <AlertDescription>{board.error}</AlertDescription>
        </Alert>
      )}
      {board.loading && <div className="p-6 text-sm text-muted-foreground">Loading board…</div>}
      {!board.loading && board.snapshot && (
        <>
          {isMobile && (
            <MobileColumnNavigator
              columns={columns}
              selectedColumn={effectiveColumn}
              counts={groups}
              onChange={setSelectedColumn}
            />
          )}
          <div
            className={
              isMobile
                ? "min-h-0 flex-1 overflow-y-auto"
                : "grid min-h-0 flex-1 auto-cols-fr grid-flow-col gap-3 overflow-x-auto"
            }
          >
            {columns
              .filter((column) => !isMobile || column.id === effectiveColumn)
              .map((column) => (
                <div
                  key={column.id}
                  data-testid={`azure-board-column-${column.id}`}
                  className="flex min-w-72 flex-col gap-2 rounded-lg bg-muted/40 p-2"
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={() => {
                    if (dragged) void board.moveItem(dragged, column.id);
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
            board={board.snapshot.board}
            onOpenChange={(open) => !open && setEditing(null)}
            onSave={board.saveItem}
          />
        </>
      )}
    </section>
  );
}
