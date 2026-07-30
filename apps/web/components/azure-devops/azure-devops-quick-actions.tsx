"use client";

import { useCallback, useEffect, useState } from "react";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { Button } from "@kandev/ui/button";
import { Card, CardContent } from "@kandev/ui/card";
import { Input } from "@kandev/ui/input";
import { Textarea } from "@kandev/ui/textarea";
import { SettingsSection } from "@/components/settings/settings-section";
import { useToast } from "@/components/toast-provider";
import {
  getAzureDevOpsWorkspaceSettings,
  updateAzureDevOpsWorkspaceSettings,
} from "@/lib/api/domains/azure-devops-api";
import type { AzureDevOpsActionPreset } from "@/lib/types/azure-devops";

function newAction(): AzureDevOpsActionPreset {
  return {
    id: `preset_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 7)}`,
    label: "New action",
    hint: "",
    icon: "sparkle",
    promptTemplate: "",
  };
}

function ActionList({
  kind,
  actions,
  onChange,
}: {
  kind: "Work item" | "Pull request";
  actions: AzureDevOpsActionPreset[];
  onChange: (actions: AzureDevOpsActionPreset[]) => void;
}) {
  const patch = (index: number, values: Partial<AzureDevOpsActionPreset>) =>
    onChange(
      actions.map((action, current) => (current === index ? { ...action, ...values } : action)),
    );
  return (
    <div className="space-y-3">
      {actions.map((action, index) => (
        <div key={action.id} className="space-y-2 rounded-md border p-3">
          <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
            <Input
              value={action.label}
              aria-label={`${kind} action label ${index + 1}`}
              onChange={(event) => patch(index, { label: event.target.value })}
            />
            <Input
              value={action.hint}
              aria-label={`${kind} action hint ${index + 1}`}
              placeholder="Short hint"
              onChange={(event) => patch(index, { hint: event.target.value })}
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="h-11 w-11 cursor-pointer text-destructive sm:h-8 sm:w-8"
              aria-label={`Remove ${kind.toLowerCase()} action ${index + 1}`}
              onClick={() => onChange(actions.filter((_, current) => current !== index))}
            >
              <IconTrash className="h-4 w-4" />
            </Button>
          </div>
          <Textarea
            value={action.promptTemplate}
            aria-label={`${kind} action prompt ${index + 1}`}
            placeholder="Prompt using {{url}} and {{title}}"
            className="min-h-24 font-mono text-xs"
            onChange={(event) => patch(index, { promptTemplate: event.target.value })}
          />
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        className="h-11 w-full cursor-pointer sm:w-auto"
        onClick={() => onChange([...actions, newAction()])}
      >
        <IconPlus className="h-4 w-4" /> Add {kind.toLowerCase()} action
      </Button>
    </div>
  );
}

export function AzureDevOpsQuickActionsSection({ workspaceId }: { workspaceId: string }) {
  const { toast } = useToast();
  const [workItemActions, setWorkItemActions] = useState<AzureDevOpsActionPreset[]>([]);
  const [pullRequestActions, setPullRequestActions] = useState<AzureDevOpsActionPreset[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let current = true;
    void getAzureDevOpsWorkspaceSettings(workspaceId)
      .then((settings) => {
        if (!current) return;
        setWorkItemActions(settings.workItemActions);
        setPullRequestActions(settings.pullRequestActions);
      })
      .catch(() => undefined)
      .finally(() => current && setLoading(false));
    return () => {
      current = false;
    };
  }, [workspaceId]);

  const save = useCallback(async () => {
    setSaving(true);
    try {
      const settings = await updateAzureDevOpsWorkspaceSettings(workspaceId, {
        workItemActions,
        pullRequestActions,
      });
      setWorkItemActions(settings.workItemActions);
      setPullRequestActions(settings.pullRequestActions);
      toast({ description: "Azure DevOps quick actions saved", variant: "success" });
    } catch (error) {
      toast({
        description:
          error instanceof Error ? error.message : "Failed to save Azure DevOps quick actions",
        variant: "error",
      });
    } finally {
      setSaving(false);
    }
  }, [pullRequestActions, toast, workItemActions, workspaceId]);

  return (
    <SettingsSection
      title="Quick actions"
      description="Task prompts available from Azure DevOps work items and pull requests."
    >
      <Card>
        <CardContent className="space-y-6 pt-6">
          <div className="space-y-3">
            <h3 className="text-sm font-medium">Work items</h3>
            <ActionList kind="Work item" actions={workItemActions} onChange={setWorkItemActions} />
          </div>
          <div className="space-y-3">
            <h3 className="text-sm font-medium">Pull requests</h3>
            <ActionList
              kind="Pull request"
              actions={pullRequestActions}
              onChange={setPullRequestActions}
            />
          </div>
          <Button
            type="button"
            className="h-11 w-full cursor-pointer sm:w-auto"
            disabled={loading || saving}
            onClick={() => void save()}
          >
            {saving ? "Saving..." : "Save quick actions"}
          </Button>
        </CardContent>
      </Card>
    </SettingsSection>
  );
}
