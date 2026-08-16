package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

// ArchivedResourceGroupReconcileRequest binds the complete logical ownership
// of one worktree. There is deliberately no route task ID: every participant is
// authorized and locked before the repository admission transaction.
type ArchivedResourceGroupReconcileRequest struct {
	ExpectedTasks    []ArchivedResourceGroupTaskRequest          `json:"expected_tasks"`
	ExpectedBranches []ArchivedResourceGroupBranchRequest        `json:"expected_branches"`
	Target           ArchivedResourceGroupReconcileTargetRequest `json:"target"`
}

type ArchivedResourceGroupTaskRequest struct {
	TaskID     string `json:"task_id"`
	ArchivedAt string `json:"archived_at"`
}

type ArchivedResourceGroupBranchRequest struct {
	Branch  string `json:"branch"`
	HeadOID string `json:"head_oid"`
}

type ArchivedResourceGroupReconcileTargetRequest struct {
	WorktreeID     string                                             `json:"worktree_id"`
	RepositoryID   string                                             `json:"repository_id"`
	RepositoryPath string                                             `json:"repository_path"`
	GitCommonDir   string                                             `json:"git_common_dir"`
	WorktreePath   string                                             `json:"worktree_path"`
	Associations   []ArchivedResourceGroupReconcileAssociationRequest `json:"associations"`
}

// ArchivedResourceGroupReconcileAssociationRequest names one durable
// task_environment_repos row. SessionID is any session bound to the row's
// task environment; it locates the owning environment and is never persisted.
type ArchivedResourceGroupReconcileAssociationRequest struct {
	AssociationID string `json:"association_id"`
	TaskID        string `json:"task_id"`
	SessionID     string `json:"session_id"`
	Branch        string `json:"branch"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// ReconcileArchivedResourceGroup admits, claims, and completes exactly one v3
// DB-only group while holding every participant task lock in canonical order.
func (s *Service) ReconcileArchivedResourceGroup(
	ctx context.Context,
	req ArchivedResourceGroupReconcileRequest,
) (*ArchivedResourceReconcileResult, error) {
	if !s.archivedResourceReconcileEnabled() {
		return nil, ErrArchivedResourceReconcileDisabled
	}
	taskIDs, err := validateArchivedResourceGroupRequestShape(req)
	if err != nil {
		return nil, err
	}
	var result *ArchivedResourceReconcileResult
	err = s.withArchivedResourceTaskLocks(taskIDs, func() error {
		if !s.archivedResourceReconcileEnabled() {
			return ErrArchivedResourceReconcileDisabled
		}
		for _, taskID := range taskIDs {
			if err := s.authorizeArchivedResourceGroupTask(ctx, taskID); err != nil {
				return err
			}
		}
		job, buildErr := s.buildArchivedResourceGroupReconcileJob(ctx, req)
		if buildErr != nil {
			return buildErr
		}
		repo, repoErr := s.archivedResourceReconcileRepo()
		if repoErr != nil {
			return repoErr
		}
		admission, admitErr := repo.AdmitArchivedResourceGroupReconcile(ctx, job)
		if admitErr != nil {
			return archivedResourceReconcileWriteError("admit group", admitErr)
		}
		if admission == nil || admission.Job == nil {
			return ErrArchivedResourceReconcileUnavailable
		}
		if admission.Job.State == models.TaskResourceCleanupStateRetained {
			result = archivedResourceGroupReconcileResult(admission.Job, len(taskIDs), len(req.Target.Associations))
			return nil
		}
		if admission.Job.State != models.TaskResourceCleanupStatePending || !admission.Created {
			return fmt.Errorf("%w: operation is already in state %q", ErrArchivedResourceReconcileConflict, admission.Job.State)
		}
		claimed, ok, claimErr := repo.ClaimArchivedResourceReconcileJob(ctx, admission.Job.ID)
		if claimErr != nil {
			return archivedResourceReconcileWriteError("claim group", claimErr)
		}
		if !ok {
			return fmt.Errorf("%w: operation was not claimable", ErrArchivedResourceReconcileConflict)
		}
		claimed, claimErr = s.rebindClaimedArchivedResourceReconcileJob(ctx, repo, admission.Job, claimed)
		if claimErr != nil {
			return claimErr
		}
		completion, _, completeErr := s.completeArchivedResourceReconcileJob(ctx, repo, claimed)
		if completeErr != nil {
			return completeErr
		}
		if completion == nil || completion.Job == nil {
			return ErrArchivedResourceReconcileUnavailable
		}
		result = archivedResourceGroupReconcileResult(completion.Job, len(taskIDs), completion.AssociationsUnbound)
		return nil
	})
	return result, err
}

func (s *Service) authorizeArchivedResourceGroupTask(ctx context.Context, taskID string) error {
	userID, scoped := callerScope(ctx)
	if !scoped {
		return nil
	}
	if s.tasks == nil || s.workspaces == nil {
		return repoerrors.ErrTaskNotFound
	}
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil || task == nil || task.WorkspaceID == "" {
		return repoerrors.ErrTaskNotFound
	}
	workspace, err := s.workspaces.GetWorkspace(ctx, task.WorkspaceID)
	if err != nil || !workspaceVisibleTo(workspace, userID) {
		return repoerrors.ErrTaskNotFound
	}
	return nil
}

func validateArchivedResourceGroupRequestShape(req ArchivedResourceGroupReconcileRequest) ([]string, error) {
	if len(req.ExpectedTasks) == 0 || len(req.ExpectedTasks) > models.ArchivedResourceReconcileMaxTasks ||
		len(req.ExpectedBranches) == 0 || len(req.ExpectedBranches) > models.ArchivedResourceReconcileMaxBranches ||
		len(req.Target.Associations) == 0 || len(req.Target.Associations) > models.ArchivedResourceReconcileMaxAssociations {
		return nil, fmt.Errorf("%w: group inventory count is out of bounds", ErrArchivedResourceReconcileInvalid)
	}
	taskIDs := make([]string, 0, len(req.ExpectedTasks))
	seen := make(map[string]struct{}, len(req.ExpectedTasks))
	for _, task := range req.ExpectedTasks {
		if task.TaskID == "" {
			return nil, fmt.Errorf("%w: group task id is required", ErrArchivedResourceReconcileInvalid)
		}
		if _, exists := seen[task.TaskID]; exists {
			return nil, fmt.Errorf("%w: duplicate group task", ErrArchivedResourceReconcileInvalid)
		}
		seen[task.TaskID] = struct{}{}
		taskIDs = append(taskIDs, task.TaskID)
	}
	for _, association := range req.Target.Associations {
		if _, exists := seen[association.TaskID]; !exists {
			return nil, fmt.Errorf("%w: group association owner is not an expected task", ErrArchivedResourceReconcileInvalid)
		}
	}
	sort.Strings(taskIDs)
	return taskIDs, nil
}

func (s *Service) buildArchivedResourceGroupReconcileJob(
	ctx context.Context,
	req ArchivedResourceGroupReconcileRequest,
) (*models.TaskResourceCleanupJob, error) {
	tasks := make([]models.ArchivedResourceGroupReconcileTask, 0, len(req.ExpectedTasks))
	for _, expected := range req.ExpectedTasks {
		task, err := s.archivedResourceTaskForReconcile(ctx, expected.TaskID, expected.ArchivedAt)
		if err != nil {
			return nil, err
		}
		if err := s.validateArchivedResourceSessions(ctx, expected.TaskID); err != nil {
			return nil, err
		}
		if err := s.validateArchivedResourceRuntime(ctx, expected.TaskID); err != nil {
			return nil, err
		}
		tasks = append(tasks, models.ArchivedResourceGroupReconcileTask{
			TaskID: expected.TaskID, ArchivedAt: task.ArchivedAt.UTC().Format(time.RFC3339Nano),
		})
	}

	branches := make([]models.ArchivedResourceReconcileBranch, 0, len(req.ExpectedBranches))
	branchHeads := make(map[string]string, len(req.ExpectedBranches))
	for _, expected := range req.ExpectedBranches {
		if _, exists := branchHeads[expected.Branch]; exists {
			return nil, fmt.Errorf("%w: duplicate group branch", ErrArchivedResourceReconcileInvalid)
		}
		branchHeads[expected.Branch] = expected.HeadOID
		branches = append(branches, models.ArchivedResourceReconcileBranch{Branch: expected.Branch, HeadOID: expected.HeadOID})
	}

	associations, err := s.loadArchivedResourceGroupAssociationRows(ctx, req, branchHeads)
	if err != nil {
		return nil, err
	}
	rootKey, err := models.ArchivedResourceManagedRootKey(req.Target.WorktreePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrArchivedResourceReconcileInvalid, err)
	}
	snapshot, raw, identity, err := models.NewArchivedResourceGroupReconcileSnapshot(models.ArchivedResourceGroupReconcileImmutable{
		Tasks: tasks, ManagedRootKey: rootKey,
		Target: models.ArchivedResourceGroupReconcileTarget{
			WorktreeID: req.Target.WorktreeID, RepositoryID: req.Target.RepositoryID,
			RepositoryPath: req.Target.RepositoryPath, GitCommonDir: req.Target.GitCommonDir,
			WorktreePath: req.Target.WorktreePath,
		},
		Branches: branches, Associations: associations,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrArchivedResourceReconcileInvalid, err)
	}
	return models.NewArchivedResourceGroupReconcileJob(snapshot, raw, identity), nil
}

func (s *Service) validateArchivedResourceRuntime(ctx context.Context, taskID string) error {
	if s.executors == nil {
		return ErrArchivedResourceReconcileUnavailable
	}
	rows, err := s.executors.ListExecutorsRunningByTaskID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("%w: read executor inventory: %v", ErrArchivedResourceReconcileConflict, err)
	}
	if len(rows) != 0 {
		return fmt.Errorf("%w: executor inventory is active or unknown", ErrArchivedResourceReconcileConflict)
	}
	return nil
}

func (s *Service) loadArchivedResourceGroupAssociationRows(
	ctx context.Context,
	req ArchivedResourceGroupReconcileRequest,
	branches map[string]string,
) ([]models.ArchivedResourceReconcileAssociation, error) {
	if s.sessions == nil {
		return nil, ErrArchivedResourceReconcileUnavailable
	}
	worktrees, ok := s.tasks.(repository.SessionWorktreeRepository)
	if !ok || worktrees == nil {
		return nil, ErrArchivedResourceReconcileUnavailable
	}
	seen := make(map[string]*models.TaskEnvironmentRepo, len(req.Target.Associations))
	result := make([]models.ArchivedResourceReconcileAssociation, 0, len(req.Target.Associations))
	for _, requested := range req.Target.Associations {
		if requested.AssociationID == "" || requested.TaskID == "" || requested.SessionID == "" || requested.Branch == "" {
			return nil, fmt.Errorf("%w: group association identity is required", ErrArchivedResourceReconcileInvalid)
		}
		if _, exists := seen[requested.AssociationID]; exists {
			return nil, fmt.Errorf("%w: duplicate group association", ErrArchivedResourceReconcileInvalid)
		}
		if _, exists := branches[requested.Branch]; !exists {
			return nil, fmt.Errorf("%w: group association branch is unbound", ErrArchivedResourceReconcileInvalid)
		}
		session, err := s.sessions.GetTaskSession(ctx, requested.SessionID)
		if err != nil || session == nil || session.TaskID != requested.TaskID {
			return nil, fmt.Errorf("%w: group association owner drifted", ErrArchivedResourceReconcileConflict)
		}
		rows, err := worktrees.ListTaskSessionWorktrees(ctx, requested.SessionID)
		if err != nil {
			return nil, fmt.Errorf("%w: list group association: %v", ErrArchivedResourceReconcileConflict, err)
		}
		rows, err = s.archivedResourceAssociationHistory(ctx, requested.SessionID, rows)
		if err != nil {
			return nil, err
		}
		match, err := findArchivedResourceAssociation(rows, requested.AssociationID)
		if err != nil {
			return nil, err
		}
		generationMatches := match != nil && canonicalTimeEqual(requested.CreatedAt, match.CreatedAt)
		if match != nil && match.Status == "active" {
			generationMatches = generationMatches && canonicalTimeEqual(requested.UpdatedAt, match.UpdatedAt)
		} else if match != nil && match.Status == "deleted" {
			generationMatches = generationMatches && isCanonicalUTCTimestamp(requested.UpdatedAt)
		}
		if match == nil || (match.Status != "active" && match.Status != "deleted") ||
			match.WorktreeID != req.Target.WorktreeID || match.RepositoryID != req.Target.RepositoryID ||
			match.WorktreePath != req.Target.WorktreePath || match.WorktreeBranch != requested.Branch || !generationMatches {
			return nil, fmt.Errorf("%w: group association %q generation or target drifted", ErrArchivedResourceReconcileConflict, requested.AssociationID)
		}
		updatedAt := match.UpdatedAt.UTC().Format(time.RFC3339Nano)
		if match.Status == "deleted" {
			updatedAt = requested.UpdatedAt
		}
		seen[requested.AssociationID] = match
		// The snapshot session slot binds the row's task_environment_id: the
		// stable v0.88 participant identity the repository loaders derive.
		result = append(result, models.ArchivedResourceReconcileAssociation{
			AssociationID: match.ID, TaskID: requested.TaskID, SessionID: match.TaskEnvironmentID,
			WorktreeID: match.WorktreeID, RepositoryID: match.RepositoryID,
			BranchSlug: match.BranchSlug, WorktreePath: match.WorktreePath,
			WorktreeBranch: match.WorktreeBranch, Status: "active",
			CreatedAt: match.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: updatedAt,
		})
	}
	return result, nil
}

func archivedResourceGroupReconcileResult(job *models.TaskResourceCleanupJob, tasks, associations int) *ArchivedResourceReconcileResult {
	result := archivedResourceReconcileResult(job, associations)
	result.Tasks = tasks
	return result
}

func (s *Service) withArchivedResourceTaskLocks(taskIDs []string, fn func() error) error {
	if len(taskIDs) == 0 || fn == nil {
		return fmt.Errorf("%w: task ids and callback are required", ErrArchivedResourceReconcileInvalid)
	}
	ids := append([]string(nil), taskIDs...)
	sort.Strings(ids)
	locks := make([]interface {
		Lock()
		Unlock()
	}, 0, len(ids))
	for index, taskID := range ids {
		if taskID == "" || (index > 0 && ids[index-1] == taskID) {
			return fmt.Errorf("%w: task lock set is invalid", ErrArchivedResourceReconcileInvalid)
		}
		locks = append(locks, s.archivedResourceTaskLock(taskID))
	}
	for _, lock := range locks {
		lock.Lock()
	}
	defer func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].Unlock()
		}
	}()
	return fn()
}
