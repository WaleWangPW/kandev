package executor

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

// A child session that inherited its parent's environment must still launch
// when it is elected onto a different executor from the one that materialized
// the inherited environment. Rejecting the launch strands the child: it cannot
// attach to a parent environment that lives on one executor while being elected
// onto another. Instead the child detaches and materializes a fresh environment
// on its elected executor, leaving the parent's environment untouched.
func TestLaunchPreparedSession_DetachesInheritedEnvironmentOnExecutorMismatch(t *testing.T) {
	repo := newMockRepository()
	repo.tasks["task-parent"] = &models.Task{ID: "task-parent"}
	repo.taskEnvironments["env-parent"] = &models.TaskEnvironment{
		ID:           "env-parent",
		TaskID:       "task-parent",
		ExecutorType: string(models.ExecutorTypeLocal),
		Status:       models.TaskEnvironmentStatusReady,
	}
	repo.executors[models.ExecutorIDLocalDocker] = &models.Executor{
		ID:     models.ExecutorIDLocalDocker,
		Type:   models.ExecutorTypeLocalDocker,
		Status: models.ExecutorStatusActive,
	}
	session := &models.TaskSession{
		ID:                "session-child",
		TaskID:            "task-child",
		AgentProfileID:    "profile-123",
		TaskEnvironmentID: "env-parent",
		State:             models.TaskSessionStateCreated,
		StartedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	repo.sessions[session.ID] = session

	var launchedExecutorType string
	exec := newTestExecutor(t, &mockAgentManager{
		launchAgentFunc: func(_ context.Context, req *LaunchAgentRequest) (*LaunchAgentResponse, error) {
			launchedExecutorType = req.ExecutorType
			return &LaunchAgentResponse{AgentExecutionID: "exec-child", Status: v1.AgentStatusStarting}, nil
		},
	}, repo)

	_, err := exec.LaunchPreparedSession(context.Background(),
		&v1.Task{ID: session.TaskID, WorkspaceID: "ws-1"}, session.ID,
		LaunchOptions{AgentProfileID: session.AgentProfileID, ExecutorID: models.ExecutorIDLocalDocker, Prompt: "test"})
	if err != nil {
		t.Fatalf("LaunchPreparedSession() error = %v, want nil after detaching inherited environment", err)
	}
	if launchedExecutorType != string(models.ExecutorTypeLocalDocker) {
		t.Fatalf("launched executor type = %q, want %q", launchedExecutorType, models.ExecutorTypeLocalDocker)
	}
	if len(repo.createTaskEnvironmentCalls) != 1 {
		t.Fatalf("created %d task environments, want 1 fresh environment for the detached child", len(repo.createTaskEnvironmentCalls))
	}
	created := repo.createTaskEnvironmentCalls[0]
	if created.TaskID != session.TaskID {
		t.Fatalf("created environment TaskID = %q, want %q (child owns its detach), not the parent's %q", created.TaskID, session.TaskID, "task-parent")
	}
	if created.ExecutorType != string(models.ExecutorTypeLocalDocker) {
		t.Fatalf("created environment ExecutorType = %q, want %q", created.ExecutorType, models.ExecutorTypeLocalDocker)
	}
	if created.ID == "env-parent" {
		t.Fatalf("detached child re-bound to the parent environment %q", created.ID)
	}
}
