package azuredevops

import (
	"context"
	"reflect"
	"testing"
)

func TestDefaultQueryPresetsMatchAzureBrowseShortcuts(t *testing.T) {
	workItemIDs := make([]string, 0, len(DefaultWorkItemQueryPresets()))
	for _, preset := range DefaultWorkItemQueryPresets() {
		workItemIDs = append(workItemIDs, preset.ID)
	}
	pullRequestIDs := make([]string, 0, len(DefaultPullRequestQueryPresets()))
	for _, preset := range DefaultPullRequestQueryPresets() {
		pullRequestIDs = append(pullRequestIDs, preset.ID)
	}

	if want := []string{"recent", "assigned", "active", "created"}; !reflect.DeepEqual(workItemIDs, want) {
		t.Fatalf("work-item preset IDs = %v, want %v", workItemIDs, want)
	}
	if want := []string{"review-requested", "active", "completed", "created"}; !reflect.DeepEqual(pullRequestIDs, want) {
		t.Fatalf("pull-request preset IDs = %v, want %v", pullRequestIDs, want)
	}
}

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
