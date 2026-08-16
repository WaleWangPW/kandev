package physicaldelete

import "context"

// releaseExecutor is the sealed absence-only admission executor. A request
// routed here has already proven that the targeted path and Git worktree
// registration are absent from the writer-DB inventory, that no resource
// protects it, and that the root policy does not shield it. The executor
// therefore performs no filesystem, Git, or database mutation: it returns a
// receipt with Mutated=false so the caller knows the release was admitted but
// produced zero effect.
type releaseExecutor struct{}

func (releaseExecutor) execute(_ context.Context, request Request, receipt Receipt) (Receipt, error) {
	receipt.Executor = ExecutorNone
	receipt.Action = request.Action
	receipt.Mutated = false
	return receipt, nil
}
