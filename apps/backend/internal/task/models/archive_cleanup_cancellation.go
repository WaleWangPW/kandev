package models

import "time"

// ArchiveTaskCleanupCancellationExpectation binds an operator cancellation to
// the exact archive generation that created a cascade cleanup intent. It keeps
// a later unarchive/rearchive from being mistaken for the original operation.
type ArchiveTaskCleanupCancellationExpectation struct {
	TaskID      string
	ArchivedAt  time.Time
	CascadeID   string
	OperationID string
}
