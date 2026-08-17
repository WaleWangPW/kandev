package service

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

type recordingArchivedCascadeCanceller struct {
	expected []models.ArchiveTaskCleanupCancellationExpectation
}

func (*recordingArchivedCascadeCanceller) CleanupTaskResources(context.Context, string, bool) {}

func (c *recordingArchivedCascadeCanceller) CancelRetryableArchivedCascadeCleanup(
	_ context.Context,
	expected []models.ArchiveTaskCleanupCancellationExpectation,
) (int, error) {
	c.expected = append([]models.ArchiveTaskCleanupCancellationExpectation(nil), expected...)
	return len(expected), nil
}

func TestCancelArchivedCascadeCleanupLeavesTasksArchived(t *testing.T) {
	tasks := newFakeTaskRepo()
	tasks.addTask("root", "", "ws-1")
	tasks.addTask("child", "root", "ws-1")
	archivedAt := time.Date(2026, 8, 17, 1, 2, 3, 0, time.UTC)
	tasks.mu.Lock()
	for _, id := range []string{"root", "child"} {
		tasks.tasks[id].ArchivedAt = &archivedAt
		tasks.tasks[id].ArchivedByCascadeID = "cascade-cancel"
	}
	tasks.mu.Unlock()

	canceller := &recordingArchivedCascadeCanceller{}
	svc := NewHandoffService(newCascadeRepo(tasks), nil, nil, nil, nil, nil)
	svc.SetTaskResourceCleaner(canceller)
	outcome, err := svc.CancelArchivedCascadeCleanup(context.Background(), "root")
	if err != nil {
		t.Fatalf("CancelArchivedCascadeCleanup: %v", err)
	}
	if outcome.CancelledJobs != 2 || len(outcome.TaskIDs) != 2 || len(canceller.expected) != 2 {
		t.Fatalf("outcome=%#v expectations=%#v", outcome, canceller.expected)
	}
	for _, expected := range canceller.expected {
		if !expected.ArchivedAt.Equal(archivedAt) || expected.CascadeID != "cascade-cancel" {
			t.Fatalf("cancellation expectation not generation-bound: %#v", expected)
		}
	}
	for _, id := range []string{"root", "child"} {
		task, err := svc.tasks.GetTask(context.Background(), id)
		if err != nil || task.ArchivedAt == nil || task.ArchivedByCascadeID != "cascade-cancel" {
			t.Fatalf("task %s was changed: %#v, %v", id, task, err)
		}
	}
}
