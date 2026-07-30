"use client";

import { useEffect, useState } from "react";
import { getAzureDevOpsWorkspaceSettings } from "@/lib/api/domains/azure-devops-api";
import type { AzureDevOpsActionPreset } from "@/lib/types/azure-devops";

export function useAzureDevOpsWorkspaceActions(workspaceId?: string) {
  const [workItemActions, setWorkItemActions] = useState<AzureDevOpsActionPreset[]>([]);
  const [pullRequestActions, setPullRequestActions] = useState<AzureDevOpsActionPreset[]>([]);

  useEffect(() => {
    if (!workspaceId) return;
    void getAzureDevOpsWorkspaceSettings(workspaceId)
      .then((settings) => {
        setWorkItemActions(settings.workItemActions);
        setPullRequestActions(settings.pullRequestActions);
      })
      .catch(() => {
        setWorkItemActions([]);
        setPullRequestActions([]);
      });
  }, [workspaceId]);

  return { workItemActions, pullRequestActions };
}
