package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/orchestrator/watcher"
	"github.com/kandev/kandev/internal/task/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

// TestHandleAgentCompleted_SubtaskWithoutRequestsInputCollapsesToCompleted
// pins the symptom reported in the v0.88 total-control fix plan:
// "child in a terminal state without requests_input must NOT write
// WAITING_FOR_INPUT". handleAgentCompleted's existing line 1238 writes
// WAITING unconditionally for non-transitioned terminal receipts.
// setSessionWaitingForInputIfRequested (this commit's guard) refuses
// the WAITING write for child tasks and instead collapses the session
// to COMPLETED, so a child agent's clean exit does not leave the
// session in a stuck RUNNING/RUNNING-equivalent state.
//
// The seed task is a subtask (ParentID="parent") and the agent's last
// message has requests_input=false; the orchestrator must therefore
// finish the session to COMPLETED, not WAITING.
func TestHandleAgentCompleted_SubtaskWithoutRequestsInputCollapsesToCompleted(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	now := time.Now().UTC()

	seedSession(t, repo, "child-task", "s-child", "")
	if err := repo.UpdateTask(ctx, &models.Task{
		ID: "child-task", WorkspaceID: "ws1", Title: "child",
		State: v1.TaskStateInProgress, ParentID: "parent",
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set child-task ParentID: %v", err)
	}
	seedExecutorRunning(t, repo, "s-child", "child-task", "exec-child")

	// No messages: requests_input must be false, so the guard refuses
	// the WAITING write and collapses the session to COMPLETED.

	taskRepo := newMockTaskRepo()
	agentMgr := &mockAgentManager{repoForExecutionLookup: repo}
	svc := createTestServiceWithScheduler(repo, newMockStepGetter(), taskRepo, agentMgr)

	svc.handleAgentCompleted(ctx, watcherAgentCompletedData("child-task", "s-child", "exec-child"))
	waitForStopCall(t, agentMgr)

	updated, err := repo.GetTaskSession(ctx, "s-child")
	if err != nil {
		t.Fatalf("load session after subtask terminal: %v", err)
	}
	if updated.State != models.TaskSessionStateCompleted {
		t.Fatalf("subtask terminal without requests_input must collapse to COMPLETED, got %q", updated.State)
	}
}

// TestHandleAgentCompleted_SubtaskWithRequestsInputStillWritesWaiting
// pins the positive half of the guard: a child task whose agent
// actually asked the user for input (clarification request,
// requests_input=true) MUST still flip the session to WAITING so the
// chat UI surfaces the prompt — the guard is supposed to suppress
// false-positive WAITING writes, not legitimate clarification
// surfaces.
func TestHandleAgentCompleted_SubtaskWithRequestsInputStillWritesWaiting(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	now := time.Now().UTC()

	seedSession(t, repo, "child-clarify", "s-child-cl", "")
	if err := repo.UpdateTask(ctx, &models.Task{
		ID: "child-clarify", WorkspaceID: "ws1", Title: "child clarify",
		State: v1.TaskStateInProgress, ParentID: "parent",
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set child-clarify ParentID: %v", err)
	}
	seedExecutorRunning(t, repo, "s-child-cl", "child-clarify", "exec-child-cl")

	// Agent clarification request — the only path that should still
	// surface WAITING for a subtask session. The message row requires a
	// turn_id FK; seed a turn first so CreateMessage succeeds.
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{
		ID: "turn-child-cl", TaskID: "child-clarify", TaskSessionID: "s-child-cl",
		StartedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, repo.CreateMessage(ctx, &models.Message{
		ID: "m-clarify", TaskSessionID: "s-child-cl", TaskID: "child-clarify",
		TurnID:     "turn-child-cl",
		AuthorType: models.MessageAuthorAgent, Content: "I need X",
		Type: models.MessageTypeClarificationRequest, RequestsInput: true,
		CreatedAt: now, UpdatedAt: now,
	}))

	taskRepo := newMockTaskRepo()
	agentMgr := &mockAgentManager{repoForExecutionLookup: repo}
	svc := createTestServiceWithScheduler(repo, newMockStepGetter(), taskRepo, agentMgr)

	svc.handleAgentCompleted(ctx, watcherAgentCompletedData("child-clarify", "s-child-cl", "exec-child-cl"))
	waitForStopCall(t, agentMgr)

	updated, err := repo.GetTaskSession(ctx, "s-child-cl")
	if err != nil {
		t.Fatalf("load session after subtask clarification: %v", err)
	}
	if updated.State != models.TaskSessionStateWaitingForInput {
		t.Fatalf("subtask with requests_input=true must still flip to WAITING, got %q", updated.State)
	}
}

// TestHandleAgentCompleted_SiblingSessionWithoutRequestsInputStillWritesWaiting
// pins the negative half of the parent-side guard. A root task with
// multiple sessions has each session finish independently: the finishing
// session must still flip to WAITING (its siblings can keep working),
// even though its last message did not request input. The guard
// therefore matches subtasks only; root tasks keep the original
// affordance.
func TestHandleAgentCompleted_SiblingSessionWithoutRequestsInputStillWritesWaiting(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	now := time.Now().UTC()

	// Root task (no ParentID) with two sessions: one finishing, one
	// still running. The finishing session's agent exit has no
	// requests_input — the guard must NOT collapse it, so the multi-
	// session task can carry a per-session WAITING signal.
	seedSession(t, repo, "t1", "s-finishing", "")
	seedExecutorRunning(t, repo, "s-finishing", "t1", "exec-finishing")
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "s-running", TaskID: "t1",
		State:     models.TaskSessionStateRunning,
		StartedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	}))

	taskRepo := newMockTaskRepo()
	agentMgr := &mockAgentManager{repoForExecutionLookup: repo}
	svc := createTestServiceWithScheduler(repo, newMockStepGetter(), taskRepo, agentMgr)

	svc.handleAgentCompleted(ctx, watcherAgentCompletedData("t1", "s-finishing", "exec-finishing"))
	waitForStopCall(t, agentMgr)

	updated, err := repo.GetTaskSession(ctx, "s-finishing")
	if err != nil {
		t.Fatalf("load finishing session: %v", err)
	}
	if updated.State != models.TaskSessionStateWaitingForInput {
		t.Fatalf("sibling session on root task must still flip to WAITING, got %q", updated.State)
	}
}

func watcherAgentCompletedData(taskID, sessionID, execID string) watcher.AgentEventData {
	return watcher.AgentEventData{
		TaskID:           taskID,
		SessionID:        sessionID,
		AgentExecutionID: execID,
	}
}
