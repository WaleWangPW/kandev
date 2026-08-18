package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/worktree"
)

type typedErrorPreparer struct {
	err error
}

func (p *typedErrorPreparer) Name() string { return "typed-error" }

func (p *typedErrorPreparer) Prepare(
	_ context.Context,
	_ *EnvPrepareRequest,
	_ PrepareProgressCallback,
) (*EnvPrepareResult, error) {
	return &EnvPrepareResult{
		Success:      false,
		ErrorMessage: p.err.Error(),
	}, p.err
}

func newLaunchErrorTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, _ := createTestManagerWithTracking()
	cleanupManagerStopCh(t, mgr)
	return mgr
}

func TestLaunchApplyPrepareResultPreservesTypedPreparationError(t *testing.T) {
	mgr := newLaunchErrorTestManager(t)
	underlying := worktree.ClassifyGitError(
		"fatal: 'feature/shared' is already checked out at '/tmp/sibling',",
		nil,
	)

	var workspacePath, mainRepoGitDir, worktreeID, worktreeBranch string
	err := mgr.launchApplyPrepareResult(
		&LaunchRequest{TaskID: "task-1", SessionID: "session-1"},
		&EnvPrepareResult{
			Success:      false,
			ErrorMessage: underlying.Error(),
			Error:        underlying,
		},
		&workspacePath,
		&mainRepoGitDir,
		&worktreeID,
		&worktreeBranch,
	)

	require.Error(t, err)
	require.True(t, errors.Is(err, worktree.ErrBranchCheckedOut))
	require.Contains(t, err.Error(), underlying.Error())
}

func TestLaunchApplyPrepareResultFallsBackToErrorMessage(t *testing.T) {
	mgr := newLaunchErrorTestManager(t)
	var workspacePath, mainRepoGitDir, worktreeID, worktreeBranch string
	err := mgr.launchApplyPrepareResult(
		&LaunchRequest{TaskID: "task-3", SessionID: "session-3"},
		&EnvPrepareResult{Success: false, ErrorMessage: "preparation failed"},
		&workspacePath,
		&mainRepoGitDir,
		&worktreeID,
		&worktreeBranch,
	)

	require.EqualError(t, err, "environment preparation failed: preparation failed")
}

func TestRunEnvironmentPreparerWithProgressPreservesTypedPreparationError(t *testing.T) {
	mgr := newLaunchErrorTestManager(t)
	underlying := worktree.ClassifyGitError(
		"fatal: 'feature/shared' is already used by worktree at '/tmp/sibling'",
		nil,
	)
	registry := NewPreparerRegistry(mgr.logger)
	registry.Register(models.ExecutorTypeWorktree, &typedErrorPreparer{err: underlying})
	mgr.preparerRegistry = registry

	result := mgr.runEnvironmentPreparerWithProgress(
		context.Background(),
		&LaunchRequest{
			TaskID:         "task-4",
			SessionID:      "session-4",
			ExecutorType:   string(models.ExecutorTypeWorktree),
			RepositoryPath: "/tmp/repo",
		},
		"",
		func(PrepareStep, int, int) {},
	)

	require.NotNil(t, result)
	require.False(t, result.Success)
	require.ErrorIs(t, result.Error, worktree.ErrBranchCheckedOut)
}
