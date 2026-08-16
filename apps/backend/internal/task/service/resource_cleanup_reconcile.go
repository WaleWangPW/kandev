package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
)

var (
	ErrArchivedResourceReconcileDisabled    = errors.New("archived resource reconcile is disabled")
	ErrArchivedResourceReconcileInvalid     = errors.New("invalid archived resource reconcile request")
	ErrArchivedResourceReconcileConflict    = errors.New("archived resource reconcile conflict")
	ErrArchivedResourceReconcileUnavailable = errors.New("archived resource reconcile is unavailable")
	ErrArchivedResourceOutcomeUnknown       = errors.New("archived resource mutation outcome is unknown")
)

// ArchivedResourceReconcileRequest is the exact metadata request accepted by
// the admin HTTP surface. The task ID is bound by the route and is therefore
// deliberately not caller-controlled inside the body.
type ArchivedResourceReconcileRequest struct {
	ExpectedArchivedAt string                                 `json:"expected_archived_at"`
	Target             ArchivedResourceReconcileTargetRequest `json:"target"`
}

type ArchivedResourceReconcileTargetRequest struct {
	WorktreeID     string                                        `json:"worktree_id"`
	RepositoryID   string                                        `json:"repository_id"`
	RepositoryPath string                                        `json:"repository_path"`
	GitCommonDir   string                                        `json:"git_common_dir"`
	WorktreePath   string                                        `json:"worktree_path"`
	Branch         string                                        `json:"branch"`
	HeadOID        string                                        `json:"head_oid"`
	Associations   []ArchivedResourceReconcileAssociationRequest `json:"associations"`
}

// ArchivedResourceReconcileAssociationRequest names one durable
// task_environment_repos row. SessionID is any session bound to the row's
// task environment; it locates the owning environment and is never persisted.
// The canonical snapshot binds the environment id itself.
type ArchivedResourceReconcileAssociationRequest struct {
	AssociationID string `json:"association_id"`
	SessionID     string `json:"session_id"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type archivedResourceSessionWorktreeHistoryReader interface {
	ListTaskSessionWorktreesIncludingInactive(ctx context.Context, sessionID string) ([]*models.TaskEnvironmentRepo, error)
}

// ArchivedResourceReconcileResult is metadata only. It contains no target,
// path, repository, session, or filesystem content.
type ArchivedResourceReconcileResult struct {
	OperationID         string `json:"operation_id"`
	State               string `json:"state"`
	Tasks               int    `json:"tasks,omitempty"`
	Targets             int    `json:"targets"`
	AssociationsUnbound int    `json:"associations_unbound"`
	PhysicalRetained    bool   `json:"physical_retained"`
	PhysicalRemoved     bool   `json:"physical_removed"`
}

func (s *Service) SetArchivedResourceFeatures(reconcileEnabled, physicalReleaseEnabled bool) {
	s.archivedResourceFeatureMu.Lock()
	s.archivedResourceReconcileOn = reconcileEnabled
	s.archivedResourcePhysicalReleaseOn = physicalReleaseEnabled
	s.archivedResourceFeatureMu.Unlock()
}

func (s *Service) archivedResourceReconcileEnabled() bool {
	s.archivedResourceFeatureMu.Lock()
	defer s.archivedResourceFeatureMu.Unlock()
	return s.archivedResourceReconcileOn
}

func (s *Service) archivedResourceReconcileRepo() (repository.ArchivedResourceReconcileRepository, error) {
	if s.resourceCleanups == nil {
		return nil, ErrArchivedResourceReconcileUnavailable
	}
	repo, ok := s.resourceCleanups.(repository.ArchivedResourceReconcileRepository)
	if !ok || repo == nil {
		return nil, ErrArchivedResourceReconcileUnavailable
	}
	return repo, nil
}

func (s *Service) archivedResourceTaskLock(taskID string) *sync.Mutex {
	s.archivedResourceLocksMu.Lock()
	defer s.archivedResourceLocksMu.Unlock()
	if s.archivedResourceLocks == nil {
		s.archivedResourceLocks = make(map[string]*sync.Mutex)
	}
	lock := s.archivedResourceLocks[taskID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.archivedResourceLocks[taskID] = lock
	}
	return lock
}

func (s *Service) withArchivedResourceTaskLock(ctx context.Context, taskID string, fn func() error) error {
	if taskID == "" || fn == nil {
		return fmt.Errorf("%w: task id and callback are required", ErrArchivedResourceReconcileInvalid)
	}
	lock := s.archivedResourceTaskLock(taskID)
	lock.Lock()
	defer lock.Unlock()
	return fn()
}

// ReconcileArchivedResource admits, claims, and completes one exact DB-only
// operation while holding the same task lock used by unarchive. No physical
// executor is reachable from this method.
func (s *Service) ReconcileArchivedResource(
	ctx context.Context,
	taskID string,
	req ArchivedResourceReconcileRequest,
) (*ArchivedResourceReconcileResult, error) {
	if !s.archivedResourceReconcileEnabled() {
		return nil, ErrArchivedResourceReconcileDisabled
	}
	if taskID == "" {
		return nil, fmt.Errorf("%w: task id is required", ErrArchivedResourceReconcileInvalid)
	}
	lock := s.archivedResourceTaskLock(taskID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.AuthorizeTaskAccess(ctx, taskID); err != nil {
		return nil, err
	}
	job, err := s.buildArchivedResourceReconcileJob(ctx, taskID, req)
	if err != nil {
		return nil, err
	}
	repo, err := s.archivedResourceReconcileRepo()
	if err != nil {
		return nil, err
	}
	admission, err := repo.AdmitArchivedResourceReconcile(ctx, job)
	if err != nil {
		return nil, archivedResourceReconcileWriteError("admit", err)
	}
	if admission == nil || admission.Job == nil {
		return nil, ErrArchivedResourceReconcileUnavailable
	}
	if admission.Job.State == models.TaskResourceCleanupStateRetained {
		return archivedResourceReconcileResult(admission.Job, jobSnapshotAssociations(job)), nil
	}
	if admission.Job.State != models.TaskResourceCleanupStatePending || !admission.Created {
		return nil, fmt.Errorf("%w: operation is already in state %q", ErrArchivedResourceReconcileConflict, admission.Job.State)
	}
	claimed, ok, err := repo.ClaimArchivedResourceReconcileJob(ctx, admission.Job.ID)
	if err != nil {
		return nil, archivedResourceReconcileWriteError("claim", err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: operation was not claimable", ErrArchivedResourceReconcileConflict)
	}
	claimed, err = s.rebindClaimedArchivedResourceReconcileJob(ctx, repo, admission.Job, claimed)
	if err != nil {
		return nil, err
	}
	completion, _, err := s.completeArchivedResourceReconcileJob(ctx, repo, claimed)
	if err != nil {
		return nil, err
	}
	if completion == nil || completion.Job == nil {
		return nil, ErrArchivedResourceReconcileUnavailable
	}
	return archivedResourceReconcileResult(completion.Job, completion.AssociationsUnbound), nil
}

func jobSnapshotAssociations(job *models.TaskResourceCleanupJob) int {
	if job == nil {
		return 0
	}
	snapshot, _, err := models.DecodeArchivedResourceReconcileSnapshot([]byte(job.ResourceSnapshot))
	if err != nil {
		return 0
	}
	return len(snapshot.Immutable.Associations)
}

func archivedResourceReconcileResult(job *models.TaskResourceCleanupJob, associations int) *ArchivedResourceReconcileResult {
	return &ArchivedResourceReconcileResult{
		OperationID:         job.OperationID,
		State:               string(job.State),
		Targets:             1,
		AssociationsUnbound: associations,
		PhysicalRetained:    true,
		PhysicalRemoved:     false,
	}
}

func (s *Service) buildArchivedResourceReconcileJob(
	ctx context.Context,
	taskID string,
	req ArchivedResourceReconcileRequest,
) (*models.TaskResourceCleanupJob, error) {
	task, err := s.archivedResourceTaskForReconcile(ctx, taskID, req.ExpectedArchivedAt)
	if err != nil {
		return nil, err
	}
	if err := validateArchivedResourceAssociationCount(req); err != nil {
		return nil, err
	}
	if err := s.validateArchivedResourceSessions(ctx, taskID); err != nil {
		return nil, err
	}
	byAssociation, err := s.loadArchivedResourceAssociationRows(ctx, req)
	if err != nil {
		return nil, err
	}
	rootKey, err := models.ArchivedResourceManagedRootKey(req.Target.WorktreePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrArchivedResourceReconcileInvalid, err)
	}
	associations := make([]models.ArchivedResourceReconcileAssociation, 0, len(req.Target.Associations))
	for _, requested := range req.Target.Associations {
		row := byAssociation[requested.AssociationID]
		associations = append(associations, archivedResourceAssociationSnapshot(taskID, row, requested))
	}
	_, raw, identity, err := models.NewArchivedResourceReconcileSnapshot(models.ArchivedResourceReconcileImmutable{
		OriginTaskID: taskID, ArchivedAt: task.ArchivedAt.UTC().Format(time.RFC3339Nano), ManagedRootKey: rootKey,
		Target: models.ArchivedResourceReconcileTarget{
			WorktreeID: req.Target.WorktreeID, RepositoryID: req.Target.RepositoryID,
			RepositoryPath: req.Target.RepositoryPath, GitCommonDir: req.Target.GitCommonDir,
			WorktreePath: req.Target.WorktreePath, Branch: req.Target.Branch, HeadOID: req.Target.HeadOID,
		},
		Associations: associations,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrArchivedResourceReconcileInvalid, err)
	}
	snapshot, _, err := models.DecodeArchivedResourceReconcileSnapshot(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: canonical snapshot: %v", ErrArchivedResourceReconcileInvalid, err)
	}
	return models.NewArchivedResourceReconcileJob(snapshot, raw, identity), nil
}

func (s *Service) archivedResourceTaskForReconcile(
	ctx context.Context,
	taskID string,
	expectedArchivedAt string,
) (*models.Task, error) {
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil || task.ArchivedAt == nil {
		return nil, fmt.Errorf("%w: task is not archived", ErrArchivedResourceReconcileConflict)
	}
	if !canonicalTimeEqual(expectedArchivedAt, task.ArchivedAt.UTC()) {
		return nil, fmt.Errorf("%w: archive generation drifted", ErrArchivedResourceReconcileConflict)
	}
	return task, nil
}

func validateArchivedResourceAssociationCount(req ArchivedResourceReconcileRequest) error {
	if len(req.Target.Associations) == 0 || len(req.Target.Associations) > models.ArchivedResourceReconcileMaxAssociations {
		return fmt.Errorf("%w: target must contain one to %d associations", ErrArchivedResourceReconcileInvalid, models.ArchivedResourceReconcileMaxAssociations)
	}
	return nil
}

func (s *Service) validateArchivedResourceSessions(ctx context.Context, taskID string) error {
	if s.sessions == nil {
		return ErrArchivedResourceReconcileUnavailable
	}
	sessions, err := s.sessions.ListTaskSessions(ctx, taskID)
	if err != nil {
		return fmt.Errorf("%w: list sessions: %v", ErrArchivedResourceReconcileConflict, err)
	}
	for _, session := range sessions {
		if session == nil || !isTerminalArchivedReconcileSession(session.State) {
			return fmt.Errorf("%w: session inventory is not terminal", ErrArchivedResourceReconcileConflict)
		}
	}
	return nil
}

func (s *Service) loadArchivedResourceAssociationRows(
	ctx context.Context,
	req ArchivedResourceReconcileRequest,
) (map[string]*models.TaskEnvironmentRepo, error) {
	worktrees, ok := s.tasks.(repository.SessionWorktreeRepository)
	if !ok || worktrees == nil {
		return nil, ErrArchivedResourceReconcileUnavailable
	}
	byAssociation := make(map[string]*models.TaskEnvironmentRepo, len(req.Target.Associations))
	for _, requested := range req.Target.Associations {
		if err := validateArchivedResourceAssociationRequest(requested, byAssociation); err != nil {
			return nil, err
		}
		rows, err := worktrees.ListTaskSessionWorktrees(ctx, requested.SessionID)
		if err != nil {
			return nil, fmt.Errorf("%w: list association: %v", ErrArchivedResourceReconcileConflict, err)
		}
		rows, err = s.archivedResourceAssociationHistory(ctx, requested.SessionID, rows)
		if err != nil {
			return nil, err
		}
		match, err := findArchivedResourceAssociation(rows, requested.AssociationID)
		if err != nil {
			return nil, err
		}
		if err := validateArchivedResourceAssociationMatch(match, requested, req.Target); err != nil {
			return nil, err
		}
		copy := *match
		byAssociation[requested.AssociationID] = &copy
	}
	return byAssociation, nil
}

func (s *Service) archivedResourceAssociationHistory(
	ctx context.Context,
	sessionID string,
	rows []*models.TaskEnvironmentRepo,
) ([]*models.TaskEnvironmentRepo, error) {
	history, ok := s.tasks.(archivedResourceSessionWorktreeHistoryReader)
	if !ok {
		return rows, nil
	}
	historyRows, err := history.ListTaskSessionWorktreesIncludingInactive(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: list association history: %v", ErrArchivedResourceReconcileConflict, err)
	}
	return historyRows, nil
}

func validateArchivedResourceAssociationRequest(
	requested ArchivedResourceReconcileAssociationRequest,
	seen map[string]*models.TaskEnvironmentRepo,
) error {
	if requested.AssociationID == "" || requested.SessionID == "" {
		return fmt.Errorf("%w: association identity is required", ErrArchivedResourceReconcileInvalid)
	}
	if _, exists := seen[requested.AssociationID]; exists {
		return fmt.Errorf("%w: duplicate association", ErrArchivedResourceReconcileInvalid)
	}
	return nil
}

func findArchivedResourceAssociation(rows []*models.TaskEnvironmentRepo, id string) (*models.TaskEnvironmentRepo, error) {
	var match *models.TaskEnvironmentRepo
	for _, row := range rows {
		if row == nil || row.ID != id {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("%w: association identity is ambiguous", ErrArchivedResourceReconcileConflict)
		}
		match = row
	}
	return match, nil
}

func validateArchivedResourceAssociationMatch(
	match *models.TaskEnvironmentRepo,
	requested ArchivedResourceReconcileAssociationRequest,
	target ArchivedResourceReconcileTargetRequest,
) error {
	generationMatches := match != nil && canonicalTimeEqual(requested.CreatedAt, match.CreatedAt)
	if match != nil && match.Status == "active" {
		generationMatches = generationMatches && canonicalTimeEqual(requested.UpdatedAt, match.UpdatedAt)
	} else if match != nil && match.Status == "deleted" {
		generationMatches = generationMatches && isCanonicalUTCTimestamp(requested.UpdatedAt)
	}
	if match == nil || (match.Status != "active" && match.Status != "deleted") ||
		match.WorktreeID != target.WorktreeID || match.RepositoryID != target.RepositoryID ||
		match.WorktreePath != target.WorktreePath || match.WorktreeBranch != target.Branch ||
		!generationMatches {
		return fmt.Errorf("%w: association %q generation or target drifted", ErrArchivedResourceReconcileConflict, requested.AssociationID)
	}
	return nil
}

// archivedResourceAssociationSnapshot binds the v0.88 environment identity:
// the snapshot session slot carries the row's task_environment_id, the stable
// participant identity the repository loaders derive for the same row.
func archivedResourceAssociationSnapshot(
	taskID string,
	row *models.TaskEnvironmentRepo,
	requested ArchivedResourceReconcileAssociationRequest,
) models.ArchivedResourceReconcileAssociation {
	updatedAt := row.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if row.Status == "deleted" {
		updatedAt = requested.UpdatedAt
	}
	return models.ArchivedResourceReconcileAssociation{
		AssociationID: row.ID, TaskID: taskID, SessionID: row.TaskEnvironmentID,
		WorktreeID: row.WorktreeID, RepositoryID: row.RepositoryID,
		BranchSlug: row.BranchSlug, WorktreePath: row.WorktreePath,
		WorktreeBranch: row.WorktreeBranch, Status: "active",
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: updatedAt,
	}
}

func canonicalTimeEqual(raw string, want time.Time) bool {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && parsed.Location() == time.UTC && parsed.Format(time.RFC3339Nano) == raw && parsed.Equal(want.UTC())
}

func isCanonicalUTCTimestamp(raw string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && parsed.Location() == time.UTC && parsed.Format(time.RFC3339Nano) == raw
}

func isTerminalArchivedReconcileSession(state models.TaskSessionState) bool {
	return state == models.TaskSessionStateCompleted || state == models.TaskSessionStateFailed || state == models.TaskSessionStateCancelled
}

// UnarchiveArchivedResourceTask seals the task mutation and every cleanup/
// retention transition into one repository transaction. No callback or
// non-transactional fallback can run between association restoration and the
// exact task archive-generation CAS.
func (s *Service) UnarchiveArchivedResourceTask(
	ctx context.Context,
	taskID string,
	expectedArchivedAt time.Time,
	expectedCascadeID string,
) (bool, error) {
	if taskID == "" || expectedArchivedAt.IsZero() {
		return false, fmt.Errorf("%w: exact task archive generation is required", ErrArchivedResourceReconcileInvalid)
	}
	for convergenceAttempt := 0; convergenceAttempt < 3; convergenceAttempt++ {
		lockTaskIDs, err := s.archivedResourceUnarchiveLockTaskIDs(ctx, taskID)
		if err != nil {
			return false, err
		}
		retryWithExpandedSet := false
		unarchived := false
		err = s.withArchivedResourceTaskLocks(lockTaskIDs, func() error {
			currentTaskIDs, discoverErr := s.archivedResourceUnarchiveLockTaskIDs(ctx, taskID)
			if discoverErr != nil {
				return discoverErr
			}
			if !containsAllStrings(lockTaskIDs, currentTaskIDs) {
				retryWithExpandedSet = true
				return nil
			}
			reconcileRepo, repoErr := s.archivedResourceReconcileRepo()
			if repoErr != nil {
				return repoErr
			}
			var resolveErr error
			unarchived, resolveErr = reconcileRepo.ResolveArchivedResourceReconcileUnarchive(
				ctx, taskID, expectedArchivedAt, expectedCascadeID,
				s.archivedResourceReconcileEnabled(),
			)
			if resolveErr != nil {
				if errors.Is(resolveErr, repository.ErrTransactionOutcomeUnknown) {
					return fmt.Errorf("%w: resolve exact unarchive: %v", ErrArchivedResourceOutcomeUnknown, resolveErr)
				}
				return fmt.Errorf("%w: resolve exact unarchive: %v", ErrArchivedResourceReconcileConflict, resolveErr)
			}
			return nil
		})
		if err != nil {
			return false, err
		}
		if !retryWithExpandedSet {
			return unarchived, nil
		}
	}
	return false, fmt.Errorf("%w: group participant lock inventory did not converge", ErrArchivedResourceReconcileConflict)
}

func (s *Service) archivedResourceUnarchiveLockTaskIDs(ctx context.Context, taskID string) ([]string, error) {
	lockTaskIDs := []string{taskID}
	reconcileRepo, err := s.archivedResourceReconcileRepo()
	if errors.Is(err, ErrArchivedResourceReconcileUnavailable) {
		return lockTaskIDs, nil
	}
	if err != nil {
		return nil, err
	}
	jobs, err := reconcileRepo.ListArchivedResourceGroupReconcileJobsByParticipant(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("%w: discover retained group: %v", ErrArchivedResourceReconcileConflict, err)
	}
	for _, job := range jobs {
		snapshot, _, validateErr := models.ValidateArchivedResourceGroupReconcileJobHeaders(job)
		if validateErr != nil {
			return nil, fmt.Errorf("%w: validate retained group: %v", ErrArchivedResourceReconcileConflict, validateErr)
		}
		for _, participant := range snapshot.Immutable.Tasks {
			lockTaskIDs = append(lockTaskIDs, participant.TaskID)
		}
	}
	return uniqueSortedStrings(lockTaskIDs), nil
}

func (s *Service) cancelArchiveTaskResourceCleanupLocked(ctx context.Context, taskID string) error {
	if s.resourceCleanups == nil {
		return nil
	}
	if err := s.resourceCleanups.CancelArchiveTaskResourceCleanupJobs(ctx, taskID); err != nil {
		return err
	}
	if err := s.cancelAndJoinArchiveTaskResourceCleanupRuns(ctx, taskID); err != nil {
		return err
	}
	return nil
}

func archivedResourceReconcileWriteError(phase string, err error) error {
	if errors.Is(err, repository.ErrTransactionOutcomeUnknown) {
		return fmt.Errorf("%w: %s: %w", ErrArchivedResourceOutcomeUnknown, phase, err)
	}
	return fmt.Errorf("%w: %s: %v", ErrArchivedResourceReconcileConflict, phase, err)
}

func (s *Service) rebindClaimedArchivedResourceReconcileJob(
	ctx context.Context,
	repo repository.ArchivedResourceReconcileRepository,
	expected *models.TaskResourceCleanupJob,
	claimed *models.TaskResourceCleanupJob,
) (*models.TaskResourceCleanupJob, error) {
	if expected != nil && claimed != nil &&
		claimed.State == models.TaskResourceCleanupStateRunning &&
		claimed.Attempts == expected.Attempts+1 &&
		sameArchivedResourceReconcileIdentity(expected, claimed) {
		return claimed, nil
	}
	if expected == nil {
		return nil, fmt.Errorf("%w: claimed operation has no expected generation", ErrArchivedResourceOutcomeUnknown)
	}
	current, err := repo.GetRunningArchivedResourceReconcileJob(ctx, expected.ID)
	if err != nil || current == nil || current.Attempts != expected.Attempts+1 ||
		!sameArchivedResourceReconcileIdentity(expected, current) {
		return nil, fmt.Errorf("%w: claimed operation identity cannot be rebound", ErrArchivedResourceOutcomeUnknown)
	}
	return current, nil
}

func (s *Service) completeArchivedResourceReconcileJob(
	ctx context.Context,
	repo repository.ArchivedResourceReconcileRepository,
	job *models.TaskResourceCleanupJob,
) (*models.ArchivedResourceReconcileCompletion, bool, error) {
	if job == nil || job.State != models.TaskResourceCleanupStateRunning || job.Attempts <= 0 {
		return nil, false, fmt.Errorf("%w: exact running claim is required", ErrArchivedResourceReconcileConflict)
	}
	var completion *models.ArchivedResourceReconcileCompletion
	var err error
	if job.SnapshotVersion == models.ArchivedResourceGroupReconcileSnapshotVersion {
		completion, err = repo.CompleteArchivedResourceGroupReconcileRetention(ctx, job.ID, job.Attempts)
	} else if job.SnapshotVersion == models.ArchivedResourceReconcileSnapshotVersion {
		completion, err = repo.CompleteArchivedResourceReconcileRetention(ctx, job.ID, job.Attempts)
	} else {
		return nil, false, fmt.Errorf("%w: unsupported reconcile snapshot", ErrArchivedResourceReconcileConflict)
	}
	if err == nil {
		if completion == nil || completion.Job == nil {
			return nil, false, fmt.Errorf("%w: completion returned no durable receipt", ErrArchivedResourceOutcomeUnknown)
		}
		return completion, false, nil
	}
	if errors.Is(err, repository.ErrTransactionOutcomeUnknown) {
		return nil, false, fmt.Errorf("%w: complete: %w", ErrArchivedResourceOutcomeUnknown, err)
	}
	current, readErr := s.resourceCleanups.GetTaskResourceCleanupJob(ctx, job.ID)
	if readErr == nil && sameBlockedArchivedResourceReconcileCompletion(job, current) {
		return nil, true, fmt.Errorf("%w: complete: %v", ErrArchivedResourceReconcileConflict, err)
	}
	if readErr == nil && sameRunningArchivedResourceReconcileCandidate(job, current) {
		return nil, false, archivedResourceReconcileWriteError("complete", err)
	}
	if readErr != nil {
		return nil, false, fmt.Errorf("%w: completion durable-state readback failed: %w", ErrArchivedResourceOutcomeUnknown, readErr)
	}
	return nil, false, fmt.Errorf("%w: completion state cannot be rebound", ErrArchivedResourceOutcomeUnknown)
}

func sameBlockedArchivedResourceReconcileCompletion(
	running *models.TaskResourceCleanupJob,
	blocked *models.TaskResourceCleanupJob,
) bool {
	return running != nil && blocked != nil &&
		running.State == models.TaskResourceCleanupStateRunning &&
		blocked.State == models.TaskResourceCleanupStateBlocked &&
		running.Attempts == blocked.Attempts && running.Attempts > 0 &&
		blocked.LastError != "" && blocked.NextAttemptAt == nil && blocked.CompletedAt == nil &&
		running.CreatedAt.Equal(blocked.CreatedAt) && !blocked.UpdatedAt.Before(running.UpdatedAt) &&
		sameArchivedResourceReconcileIdentity(running, blocked)
}

func archivedResourceReconcileLockTaskIDs(
	job *models.TaskResourceCleanupJob,
) ([]string, error) {
	if job == nil {
		return nil, fmt.Errorf("%w: nil reconcile job", ErrArchivedResourceReconcileConflict)
	}
	switch job.SnapshotVersion {
	case models.ArchivedResourceReconcileSnapshotVersion:
		if _, _, err := models.ValidateArchivedResourceReconcileJobHeaders(job); err != nil {
			return nil, err
		}
		return uniqueSortedStrings([]string{job.TaskID}), nil
	case models.ArchivedResourceGroupReconcileSnapshotVersion:
		snapshot, _, err := models.ValidateArchivedResourceGroupReconcileJobHeaders(job)
		if err != nil {
			return nil, err
		}
		taskIDs := make([]string, 0, len(snapshot.Immutable.Tasks))
		for _, participant := range snapshot.Immutable.Tasks {
			taskIDs = append(taskIDs, participant.TaskID)
		}
		return uniqueSortedStrings(taskIDs), nil
	default:
		return nil, fmt.Errorf("%w: unsupported reconcile snapshot", ErrArchivedResourceReconcileConflict)
	}
}

func (s *Service) recoverRunningArchivedResourceReconcileJobs(
	ctx context.Context,
	repo repository.ArchivedResourceReconcileRepository,
) error {
	jobs, err := repo.ListRunningArchivedResourceReconcileJobs(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range jobs {
		lockTaskIDs, lockErr := archivedResourceReconcileLockTaskIDs(candidate)
		if lockErr != nil {
			return lockErr
		}
		if err := s.withArchivedResourceTaskLocks(lockTaskIDs, func() error {
			if !s.archivedResourceReconcileEnabled() {
				return ErrArchivedResourceReconcileDisabled
			}
			current, loadErr := repo.GetRunningArchivedResourceReconcileJob(ctx, candidate.ID)
			if loadErr != nil {
				return fmt.Errorf("%w: running recovery cannot be rebound: %w", ErrArchivedResourceOutcomeUnknown, loadErr)
			}
			if !sameRunningArchivedResourceReconcileCandidate(candidate, current) {
				return fmt.Errorf("%w: running recovery identity drifted", ErrArchivedResourceOutcomeUnknown)
			}
			_, blocked, completeErr := s.completeArchivedResourceReconcileJob(ctx, repo, current)
			if blocked {
				return nil
			}
			return completeErr
		}); err != nil {
			return err
		}
	}
	return nil
}

func sameRunningArchivedResourceReconcileCandidate(
	listed *models.TaskResourceCleanupJob,
	current *models.TaskResourceCleanupJob,
) bool {
	return listed != nil && current != nil &&
		listed.State == models.TaskResourceCleanupStateRunning &&
		current.State == models.TaskResourceCleanupStateRunning &&
		listed.Attempts == current.Attempts && listed.Attempts > 0 &&
		listed.LastError == current.LastError && listed.UpdatedAt.Equal(current.UpdatedAt) &&
		sameArchivedResourceReconcileIdentity(listed, current)
}

type archivedResourceReconcileWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// StartArchivedResourceReconcileWorker is gated before every selector. The
// disabled path performs no job or snapshot inspection. Enabled startup first
// resolves exact interrupted running claims without resetting or re-claiming
// them, so their attempt generation remains unchanged.
func (s *Service) StartArchivedResourceReconcileWorker(ctx context.Context) error {
	if !s.archivedResourceReconcileEnabled() {
		return nil
	}
	repo, err := s.archivedResourceReconcileRepo()
	if err != nil {
		return err
	}
	if err := s.recoverRunningArchivedResourceReconcileJobs(ctx, repo); err != nil {
		return err
	}
	if err := s.processDueArchivedResourceReconcileJobs(ctx); err != nil {
		return err
	}
	workerCtx, cancel := context.WithCancel(ctx)
	worker := &archivedResourceReconcileWorker{cancel: cancel, done: make(chan struct{})}
	s.archivedResourceWorkerMu.Lock()
	if s.archivedResourceWorker != nil {
		s.archivedResourceWorkerMu.Unlock()
		cancel()
		return nil
	}
	s.archivedResourceWorker = worker
	s.archivedResourceWorkerMu.Unlock()
	go func() {
		defer close(worker.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				if err := s.processDueArchivedResourceReconcileJobs(workerCtx); err != nil && workerCtx.Err() == nil && s.logger != nil {
					s.logger.Warn("process archived resource reconcile jobs", zap.Error(err))
				}
			}
		}
	}()
	return nil
}

func (s *Service) StopArchivedResourceReconcileWorker() {
	s.archivedResourceWorkerMu.Lock()
	worker := s.archivedResourceWorker
	s.archivedResourceWorker = nil
	s.archivedResourceWorkerMu.Unlock()
	if worker != nil {
		worker.cancel()
		<-worker.done
	}
}

func (s *Service) processDueArchivedResourceReconcileJobs(ctx context.Context) error {
	if !s.archivedResourceReconcileEnabled() {
		return nil
	}
	repo, err := s.archivedResourceReconcileRepo()
	if err != nil {
		return err
	}
	jobs, err := repo.ListDueArchivedResourceReconcileJobs(ctx, time.Now().UTC(), 100)
	if err != nil {
		return err
	}
	for _, candidate := range jobs {
		if candidate == nil {
			continue
		}
		lockTaskIDs, lockErr := archivedResourceReconcileLockTaskIDs(candidate)
		if lockErr != nil {
			return lockErr
		}
		claimErr := s.withArchivedResourceTaskLocks(lockTaskIDs, func() error {
			if !s.archivedResourceReconcileEnabled() {
				return ErrArchivedResourceReconcileDisabled
			}
			claimed, ok, err := repo.ClaimArchivedResourceReconcileJob(ctx, candidate.ID)
			if err != nil || !ok {
				if err != nil {
					return archivedResourceReconcileWriteError("claim due", err)
				}
				return fmt.Errorf("%w: due operation was not claimable", ErrArchivedResourceReconcileConflict)
			}
			claimed, err = s.rebindClaimedArchivedResourceReconcileJob(ctx, repo, candidate, claimed)
			if err != nil {
				return err
			}
			_, blocked, completeErr := s.completeArchivedResourceReconcileJob(ctx, repo, claimed)
			if blocked {
				return nil
			}
			return completeErr
		})
		if claimErr != nil {
			return claimErr
		}
	}
	return nil
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func containsAllStrings(have, want []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, value := range have {
		set[value] = struct{}{}
	}
	for _, value := range want {
		if _, exists := set[value]; !exists {
			return false
		}
	}
	return true
}

func sameArchivedResourceReconcileIdentity(left, right *models.TaskResourceCleanupJob) bool {
	if left == nil || right == nil {
		return false
	}
	leftScope, rightScope := "", ""
	if left.ActiveScopeKey != nil {
		leftScope = *left.ActiveScopeKey
	}
	if right.ActiveScopeKey != nil {
		rightScope = *right.ActiveScopeKey
	}
	return left.ID == right.ID && left.OperationID == right.OperationID && left.TaskID == right.TaskID &&
		left.Trigger == right.Trigger && left.ResourceSnapshot == right.ResourceSnapshot &&
		left.SnapshotVersion == right.SnapshotVersion && left.SnapshotDigest == right.SnapshotDigest &&
		left.ResourceKind == right.ResourceKind && left.ResourceID == right.ResourceID &&
		left.ManagedRootKey == right.ManagedRootKey && left.AnchorRevision == right.AnchorRevision &&
		leftScope == rightScope && left.CreatedAt.Equal(right.CreatedAt)
}
