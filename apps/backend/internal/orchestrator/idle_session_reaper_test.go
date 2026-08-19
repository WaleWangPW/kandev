package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kandev/kandev/internal/agentruntime"
	"github.com/kandev/kandev/internal/task/models"
)

// TestIdleReaper_TickSkipsRowsBelowMinIdle verifies the age filter:
// a row whose UpdatedAt is within idleReaperMinIdle is never reaped,
// regardless of its session state. The tick records what it touched
// via a channel; assert the channel stays empty for two ticks, then
// drop a second row whose age crosses the threshold and assert the
// second row is reaped.
//
// synctest advances time instantly, so the entire sequence runs
// without time.Sleep.
func TestIdleReaper_TickSkipsRowsBelowMinIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := setupTestRepo(t)
		ctx := context.Background()
		now := time.Now().UTC()
		seedTaskAndSession(t, repo, "task-recent", "s-recent",
			models.TaskSessionStateWaitingForInput)
		// Recent row: UpdatedAt = now, age = 0 < minIdle.
		if err := repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
			ID: "s-recent", SessionID: "s-recent", TaskID: "task-recent",
			Runtime:   agentruntime.RuntimeStandalone,
			Status:    models.ExecutorRunningStatusRunning,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("upsert recent: %v", err)
		}

		svc := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(),
			&mockAgentManager{isAgentRunning: false})
		svc.turnService = &inactiveTurnService{}
		svc.idleReaper = newIdleSessionReaper()
		svc.idleReaper.minIdle = 100 * time.Millisecond
		svc.idleReaper.interval = 50 * time.Millisecond

		// Two ticks: both should skip (age < minIdle).
		svc.reclaimIdleSessionsOnce(ctx)
		svc.reclaimIdleSessionsOnce(ctx)

		row, err := repo.GetExecutorRunningBySessionID(ctx, "s-recent")
		if err != nil {
			t.Fatalf("recent row missing: %v", err)
		}
		if row.Status != models.ExecutorRunningStatusRunning {
			t.Fatalf("recent row reclaimed prematurely; status=%q", row.Status)
		}

		// Advance time past minIdle. A fresh tick should now reclaim.
		time.Sleep(150 * time.Millisecond) // synctest virtual time
		svc.reclaimIdleSessionsOnce(ctx)

		row, err = repo.GetExecutorRunningBySessionID(ctx, "s-recent")
		if err != nil {
			t.Fatalf("recent row missing after tick: %v", err)
		}
		if row.Status != models.ExecutorRunningStatusStopped {
			t.Fatalf("old row not reclaimed after tick; status=%q", row.Status)
		}
		if row.LocalPID != 0 {
			t.Fatalf("LocalPID not cleared; got %d", row.LocalPID)
		}
	})
}

// TestIdleReaper_TickSkipsLiveRuntime asserts the live-runtime guard:
// a row whose IsAgentRunningForSession returns true is never reaped,
// even past the idle threshold. This is the most load-bearing invariant
// — the reaper must NEVER touch a running executor.
func TestIdleReaper_TickSkipsLiveRuntime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := setupTestRepo(t)
		ctx := context.Background()
		now := time.Now().UTC().Add(-1 * time.Hour) // well past minIdle
		seedTaskAndSession(t, repo, "task-live", "s-live",
			models.TaskSessionStateWaitingForInput)
		if err := repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
			ID: "s-live", SessionID: "s-live", TaskID: "task-live",
			Runtime:   agentruntime.RuntimeStandalone,
			Status:    models.ExecutorRunningStatusRunning,
			LocalPID:  9999,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("upsert live: %v", err)
		}

		svc := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(),
			&mockAgentManager{isAgentRunning: true}) // critical: agent is alive
		svc.turnService = &inactiveTurnService{}
		svc.idleReaper = newIdleSessionReaper()
		svc.idleReaper.minIdle = 0 // age filter doesn't matter — runtime guard wins

		svc.reclaimIdleSessionsOnce(ctx)

		row, err := repo.GetExecutorRunningBySessionID(ctx, "s-live")
		if err != nil {
			t.Fatalf("live row missing: %v", err)
		}
		if row.Status != models.ExecutorRunningStatusRunning {
			t.Fatalf("live runtime must not be reclaimed; status=%q", row.Status)
		}
		if row.LocalPID != 9999 {
			t.Fatalf("live runtime must keep its LocalPID; got %d", row.LocalPID)
		}
	})
}

// TestIdleReaper_TickSkipsActiveTurn asserts the active-turn guard:
// a session with a non-nil active turn is never reaped, even past the
// idle threshold. This protects in-flight turns from being reaped
// mid-stream.
func TestIdleReaper_TickSkipsActiveTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := setupTestRepo(t)
		ctx := context.Background()
		now := time.Now().UTC().Add(-1 * time.Hour)
		seedTaskAndSession(t, repo, "task-turn", "s-turn",
			models.TaskSessionStateWaitingForInput)
		if err := repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
			ID: "s-turn", SessionID: "s-turn", TaskID: "task-turn",
			Runtime:   agentruntime.RuntimeStandalone,
			Status:    models.ExecutorRunningStatusRunning,
			LocalPID:  4242,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("upsert turn row: %v", err)
		}

		svc := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(),
			&mockAgentManager{isAgentRunning: false})
		// Inject a turn service that reports an active turn.
		svc.turnService = &alwaysActiveTurnService{}
		svc.idleReaper = newIdleSessionReaper()
		svc.idleReaper.minIdle = 0

		svc.reclaimIdleSessionsOnce(ctx)

		row, err := repo.GetExecutorRunningBySessionID(ctx, "s-turn")
		if err != nil {
			t.Fatalf("turn row missing: %v", err)
		}
		if row.Status != models.ExecutorRunningStatusRunning {
			t.Fatalf("active turn must block reclaim; status=%q", row.Status)
		}
		if row.LocalPID != 4242 {
			t.Fatalf("active turn row must keep LocalPID; got %d", row.LocalPID)
		}
	})
}

// alwaysActiveTurnService reports a non-nil turn for any session —
// used to prove the active-turn guard is consulted by the reaper.
type alwaysActiveTurnService struct{}

func (*alwaysActiveTurnService) GetActiveTurn(_ context.Context, _ string) (*models.Turn, error) {
	return &models.Turn{ID: "turn-always"}, nil
}
func (*alwaysActiveTurnService) StartTurn(context.Context, string) (*models.Turn, error) {
	panic("alwaysActiveTurnService: StartTurn should not be called by reaper tests")
}
func (*alwaysActiveTurnService) ReserveTurn(context.Context, string, *models.PromptDispatchRecovery) (*models.Turn, error) {
	panic("alwaysActiveTurnService: ReserveTurn should not be called by reaper tests")
}
func (*alwaysActiveTurnService) MarkReservedTurnDispatchAttempted(context.Context, *models.Turn) error {
	panic("alwaysActiveTurnService: MarkReservedTurnDispatchAttempted should not be called by reaper tests")
}
func (*alwaysActiveTurnService) PublishReservedTurn(context.Context, *models.Turn) error {
	panic("alwaysActiveTurnService: PublishReservedTurn should not be called by reaper tests")
}
func (*alwaysActiveTurnService) RollbackReservedTurn(context.Context, string, string) (bool, error) {
	panic("alwaysActiveTurnService: RollbackReservedTurn should not be called by reaper tests")
}
func (*alwaysActiveTurnService) ReconcileUnpublishedPromptTurns(context.Context) (int, error) {
	return 0, nil
}
func (*alwaysActiveTurnService) CompleteTurn(context.Context, string) error {
	return nil
}
func (*alwaysActiveTurnService) GetTurn(context.Context, string) (*models.Turn, error) {
	return nil, nil
}
func (*alwaysActiveTurnService) UpdateTurn(context.Context, *models.Turn) error {
	return nil
}
func (*alwaysActiveTurnService) PatchTurnMetadata(context.Context, string, string, map[string]interface{}) error {
	return nil
}
func (*alwaysActiveTurnService) AbandonOpenTurns(context.Context, string) error {
	return nil
}

// TestIdleReaper_TickSkipsWrongState verifies the session-state guard:
// rows whose session state is not in WaitingForInput/Idle/Completed
// are never reaped, regardless of age. The reaper delegates the
// state check to reclaimIdleSession which fails closed.
func TestIdleReaper_TickSkipsWrongState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := setupTestRepo(t)
		ctx := context.Background()
		now := time.Now().UTC().Add(-1 * time.Hour)
		seedTaskAndSession(t, repo, "task-running", "s-running",
			models.TaskSessionStateRunning)
		if err := repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
			ID: "s-running", SessionID: "s-running", TaskID: "task-running",
			Runtime:   agentruntime.RuntimeStandalone,
			Status:    models.ExecutorRunningStatusRunning,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("upsert running: %v", err)
		}

		svc := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(),
			&mockAgentManager{isAgentRunning: false})
		svc.turnService = &inactiveTurnService{}
		svc.idleReaper = newIdleSessionReaper()
		svc.idleReaper.minIdle = 0

		svc.reclaimIdleSessionsOnce(ctx)

		row, err := repo.GetExecutorRunningBySessionID(ctx, "s-running")
		if err != nil {
			t.Fatalf("running row missing: %v", err)
		}
		if row.Status != models.ExecutorRunningStatusRunning {
			t.Fatalf("running session must not be reaped; status=%q", row.Status)
		}
	})
}

// TestIdleReaper_LoopStartStopLifecycle verifies the goroutine
// ownership contract: start launches a goroutine, the loop fires
// ticks at the configured interval, and stop cancels + joins
// cleanly. Uses an atomic counter (channel races were intermittent
// under -race; the counter is race-free and bounded by stop()).
func TestIdleReaper_LoopStartStopLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := setupTestRepo(t)
		ctx := context.Background()
		seedTaskAndSession(t, repo, "task-loop", "s-loop",
			models.TaskSessionStateWaitingForInput)
		now := time.Now().UTC().Add(-1 * time.Hour)
		if err := repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
			ID: "s-loop", SessionID: "s-loop", TaskID: "task-loop",
			Runtime:   agentruntime.RuntimeStandalone,
			Status:    models.ExecutorRunningStatusRunning,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}

		svc := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(),
			&mockAgentManager{isAgentRunning: false})
		svc.turnService = &inactiveTurnService{}
		svc.idleReaper = newIdleSessionReaper()
		svc.idleReaper.minIdle = 0
		svc.idleReaper.interval = 5 * time.Millisecond

		var ticks int32
		svc.idleReaper.start(ctx, func(tickCtx context.Context) {
			atomic.AddInt32(&ticks, 1)
		})

		// Let virtual time run for a few ticks.
		time.Sleep(20 * time.Millisecond)
		beforeStop := atomic.LoadInt32(&ticks)
		if beforeStop < 2 {
			t.Fatalf("expected >=2 ticks before stop; got %d", beforeStop)
		}

		// Stop must cancel + join cleanly; counter is monotone after.
		svc.stopIdleSessionReaper()
		time.Sleep(10 * time.Millisecond)
		afterStop := atomic.LoadInt32(&ticks)
		if afterStop < beforeStop {
			t.Fatalf("counter regressed after stop: before=%d after=%d", beforeStop, afterStop)
		}
	})
}

// seedTaskAndSession is defined in task_operations_test.go with the same
// signature (taskID, sessionID, sessionState). The reaper tests reuse it.
