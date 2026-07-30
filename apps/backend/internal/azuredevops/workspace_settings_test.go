package azuredevops

import (
	"context"
	"testing"
)

func TestWorkspaceSettingsDefaultsAndActionOverrides(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	ctx := context.Background()
	if _, err := service.SetConfigForWorkspace(ctx, "ws-1", &SetConfigRequest{
		OrganizationURL: "https://dev.azure.com/acme", PAT: "pat",
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	defaults, err := service.GetWorkspaceSettings(ctx, "ws-1")
	if err != nil {
		t.Fatalf("get defaults: %v", err)
	}
	if len(defaults.WorkItemActions) == 0 || len(defaults.PullRequestActions) == 0 {
		t.Fatalf("expected built-in actions, got %+v", defaults)
	}

	custom := []ActionPreset{{Label: "Triage", Hint: "Sort the report", PromptTemplate: "Triage {{url}}"}}
	updated, err := service.UpdateWorkspaceSettings(ctx, &UpdateWorkspaceSettingsRequest{
		WorkspaceID:        "ws-1",
		WorkItemActions:    &custom,
		WorkItemActionsSet: true,
	})
	if err != nil {
		t.Fatalf("update actions: %v", err)
	}
	if len(updated.WorkItemActions) != 1 || updated.WorkItemActions[0].ID == "" {
		t.Fatalf("custom actions were not normalized: %+v", updated.WorkItemActions)
	}
	if len(updated.PullRequestActions) != len(DefaultPullRequestActionPresets()) {
		t.Fatalf("untouched PR actions should keep defaults: %+v", updated.PullRequestActions)
	}
}
