package physicaldelete

import "context"

// filesystemExecutor is deliberately empty. There is no filesystem mutation
// implementation in this successor, so quarantine, restore, purge, parent
// removal, recursive removal, and provisional rollback all deny here.
type filesystemExecutor struct{}

func (filesystemExecutor) execute(_ context.Context, request Request, receipt Receipt) (Receipt, error) {
	receipt.Executor = ExecutorFilesystem
	receipt.Action = request.Action
	receipt.Mutated = false
	receipt.Reason = DenialExecutorUnavailable
	return receipt, ErrExecutorUnavailable
}
