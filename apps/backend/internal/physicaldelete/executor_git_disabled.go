package physicaldelete

import "context"

// gitExecutor is deliberately empty. Registered-worktree removal and branch
// deletion never reach Git, prune, force, or a broader fallback in this
// successor.
type gitExecutor struct{}

func (gitExecutor) execute(_ context.Context, request Request, receipt Receipt) (Receipt, error) {
	receipt.Executor = ExecutorGit
	receipt.Action = request.Action
	receipt.Mutated = false
	receipt.Reason = DenialExecutorUnavailable
	return receipt, ErrExecutorUnavailable
}
