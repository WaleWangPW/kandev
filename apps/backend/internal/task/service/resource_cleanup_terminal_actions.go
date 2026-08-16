package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kandev/kandev/internal/physicaldelete"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
)

var (
	ErrArchivedResourceReleaseDisabled             = errors.New("archived resource release is disabled")
	ErrArchivedResourceReleaseInvalid              = errors.New("invalid archived resource release request")
	ErrArchivedResourceReleaseUnavailable          = errors.New("archived resource release is unavailable")
	ErrArchivedResourceReleaseUnknown              = errors.New("archived resource release target is unknown")
	ErrArchivedResourceReleaseAdmissionUnavailable = errors.New("archived resource release admission is unavailable")
	ErrArchivedResourceReleaseAdmissionDenied      = errors.New("archived resource release admission was denied")
	ErrArchivedResourceReleaseAdmissionMutated     = errors.New("archived resource release admission produced a non-noop receipt")

	ErrArchivedResourceEnvironmentRetirementDisabled    = errors.New("archived resource environment retirement is disabled")
	ErrArchivedResourceEnvironmentRetirementInvalid     = errors.New("invalid archived resource environment retirement request")
	ErrArchivedResourceEnvironmentRetirementUnavailable = errors.New("archived resource environment retirement is unavailable")

	ErrArchivedResourcePendingMoveDisabled      = errors.New("archived resource pending move cancellation is disabled")
	ErrArchivedResourcePendingMoveInvalid       = errors.New("invalid archived resource pending move cancellation request")
	ErrArchivedResourcePendingMoveSessionActive = errors.New("stale pending move session is not historical terminal")
	ErrArchivedResourcePendingMoveUnavailable   = errors.New("archived resource pending move cancellation is unavailable")
)

// ArchivedResourceReleaseRequest is the metadata-only admission payload for
// the absent-target release route. The release snapshot is derived from the
// targeted retained anchor plus the exact physical/Git absence proof; the
// caller cannot self-sign the anchor identity.
type ArchivedResourceReleaseRequest struct {
	AnchorOperationID  string `json:"anchor_operation_id"`
	AnchorDigest       string `json:"anchor_digest"`
	AnchorTaskID       string `json:"anchor_task_id"`
	AnchorWorktreeID   string `json:"anchor_worktree_id"`
	AnchorRepository   string `json:"anchor_repository_id"`
	AnchorBranch       string `json:"anchor_branch"`
	AnchorHeadOID      string `json:"anchor_head_oid"`
	AnchorWorktreePath string `json:"anchor_worktree_path"`
	AnchorGitCommonDir string `json:"anchor_git_common_dir"`
	ReleasedAt         string `json:"released_at"`
}

// ArchivedResourceReleaseResult is the metadata-only success receipt.
type ArchivedResourceReleaseResult struct {
	OperationID      string `json:"operation_id"`
	State            string `json:"state"`
	Targets          int    `json:"targets"`
	PhysicalRetained bool   `json:"physical_retained"`
	PhysicalRemoved  bool   `json:"physical_removed"`
}

// ArchivedResourceEnvironmentRetirementRequest is the exact-set admission
// payload for the stale environment retirement route. The repository binds
// every persisted column per row, so the caller must supply the canonical
// generation values for every row that may be retired.
type ArchivedResourceEnvironmentRetirementRequest struct {
	EnvironmentID     string                                                   `json:"environment_id"`
	TaskID            string                                                   `json:"task_id"`
	EnvironmentStatus string                                                   `json:"environment_status"`
	Repositories      []ArchivedResourceEnvironmentRetirementRepositoryRequest `json:"repositories"`
	RetiredAt         string                                                   `json:"retired_at"`
}

type ArchivedResourceEnvironmentRetirementRepositoryRequest struct {
	ID             string `json:"id"`
	RepositoryID   string `json:"repository_id"`
	BranchSlug     string `json:"branch_slug,omitempty"`
	WorktreeID     string `json:"worktree_id,omitempty"`
	WorktreePath   string `json:"worktree_path,omitempty"`
	WorktreeBranch string `json:"worktree_branch,omitempty"`
	Position       int    `json:"position"`
	Status         string `json:"status,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	MergedAt       string `json:"merged_at,omitempty"`
	DeletedAt      string `json:"deleted_at,omitempty"`
}

// ArchivedResourceEnvironmentRetirementResult is the metadata-only success
// receipt.
type ArchivedResourceEnvironmentRetirementResult struct {
	EnvironmentID    string `json:"environment_id"`
	State            string `json:"state"`
	Repositories     int    `json:"repositories"`
	PhysicalRetained bool   `json:"physical_retained"`
	PhysicalRemoved  bool   `json:"physical_removed"`
}

// ArchivedResourcePendingMoveCancelRequest is the exact-generation cancel
// payload for a historical session's stale pending move.
type ArchivedResourcePendingMoveCancelRequest struct {
	PendingMoveID        string `json:"pending_move_id"`
	PendingMoveOperation string `json:"pending_move_operation"`
	SessionID            string `json:"session_id"`
	SnapshotVersion      int    `json:"snapshot_version"`
	SnapshotDigest       string `json:"snapshot_digest"`
	ResourceKind         string `json:"resource_kind"`
	ResourceID           string `json:"resource_id"`
	ManagedRootKey       string `json:"managed_root_key"`
	ResourceSnapshot     string `json:"resource_snapshot"`
	TaskID               string `json:"task_id"`
}

// ArchivedResourcePendingMoveCancelResult is the metadata-only success receipt.
type ArchivedResourcePendingMoveCancelResult struct {
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
	Targets     int    `json:"targets"`
}

// ArchivedResourceTerminalRepository is the repository surface used by the
// three terminal DB-only actions. It is intentionally a sub-interface so the
// service stays free of unrelated cleanup APIs.
type ArchivedResourceTerminalRepository interface {
	CancelStaleArchivedResourcePendingMove(ctx context.Context, expected *models.TaskResourceCleanupJob) (bool, error)
	ReleaseAbsentArchivedResourceAnchor(ctx context.Context, job *models.TaskResourceCleanupJob) (*models.ArchivedResourceReleaseAdmission, error)
	RetireStaleArchivedResourceEnvironmentReference(
		ctx context.Context,
		job *models.TaskResourceCleanupJob,
	) (*models.ArchivedResourceEnvironmentRetirementIdentity, error)
}

func (s *Service) archivedResourceTerminalRepo() (ArchivedResourceTerminalRepository, error) {
	if s.archivedResourceTerminalRepository != nil {
		return s.archivedResourceTerminalRepository, nil
	}
	if s.resourceCleanups == nil {
		return nil, ErrArchivedResourceReleaseUnavailable
	}
	repo, ok := s.resourceCleanups.(ArchivedResourceTerminalRepository)
	if !ok || repo == nil {
		return nil, ErrArchivedResourceReleaseUnavailable
	}
	return repo, nil
}

func (s *Service) archivedResourceTerminalRepoFor(envErr error) (ArchivedResourceTerminalRepository, error) {
	repo, err := s.archivedResourceTerminalRepo()
	if err != nil {
		return nil, envErr
	}
	return repo, nil
}

func (s *Service) CancelStaleArchivedResourcePendingMove(
	ctx context.Context,
	req ArchivedResourcePendingMoveCancelRequest,
) (*ArchivedResourcePendingMoveCancelResult, error) {
	if !s.archivedResourceReconcileEnabled() {
		return nil, ErrArchivedResourcePendingMoveDisabled
	}
	if err := validateArchivedResourcePendingMoveRequest(req); err != nil {
		return nil, err
	}
	repo, err := s.archivedResourceTerminalRepoFor(ErrArchivedResourcePendingMoveUnavailable)
	if err != nil {
		return nil, err
	}
	expected, err := buildStalePendingMoveExpected(req)
	if err != nil {
		return nil, err
	}
	if err := s.requirePendingMoveSessionTerminal(ctx, req.SessionID, expected.TaskID); err != nil {
		return nil, err
	}
	cancelled, err := repo.CancelStaleArchivedResourcePendingMove(ctx, expected)
	if err != nil {
		return nil, err
	}
	if !cancelled {
		return nil, fmt.Errorf("%w: stale pending move was not cancelled", ErrArchivedResourcePendingMoveInvalid)
	}
	return &ArchivedResourcePendingMoveCancelResult{
		OperationID: expected.OperationID,
		State:       string(models.TaskResourceCleanupStateCancelled),
		Targets:     1,
	}, nil
}

func (s *Service) requirePendingMoveSessionTerminal(
	ctx context.Context,
	sessionID string,
	taskID string,
) error {
	if s.sessions == nil {
		return ErrArchivedResourcePendingMoveUnavailable
	}
	session, err := s.sessions.GetTaskSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrArchivedResourcePendingMoveInvalid, err)
	}
	if session == nil {
		return fmt.Errorf("%w: session %q is missing", ErrArchivedResourcePendingMoveInvalid, sessionID)
	}
	if session.TaskID != taskID {
		return fmt.Errorf("%w: session task_id does not bind pending move", ErrArchivedResourcePendingMoveInvalid)
	}
	if !isTerminalArchivedReconcileSession(session.State) {
		return fmt.Errorf("%w: session %q is in state %q", ErrArchivedResourcePendingMoveSessionActive, sessionID, session.State)
	}
	return nil
}

func validateArchivedResourcePendingMoveRequest(req ArchivedResourcePendingMoveCancelRequest) error {
	if req.PendingMoveID == "" || req.PendingMoveOperation == "" {
		return fmt.Errorf("%w: pending move identity is required", ErrArchivedResourcePendingMoveInvalid)
	}
	if req.SessionID == "" {
		return fmt.Errorf("%w: session id is required", ErrArchivedResourcePendingMoveInvalid)
	}
	if req.TaskID == "" {
		return fmt.Errorf("%w: task id is required", ErrArchivedResourcePendingMoveInvalid)
	}
	if req.ResourceSnapshot == "" || req.SnapshotDigest == "" {
		return fmt.Errorf("%w: pending move snapshot bytes are required", ErrArchivedResourcePendingMoveInvalid)
	}
	if req.SnapshotVersion != models.ArchivedResourceReconcileSnapshotVersion {
		return fmt.Errorf("%w: only v2 anchors carry stale pending moves", ErrArchivedResourcePendingMoveInvalid)
	}
	if req.ResourceKind == "" || req.ResourceID == "" || req.ManagedRootKey == "" {
		return fmt.Errorf("%w: pending move identity headers are required", ErrArchivedResourcePendingMoveInvalid)
	}
	return nil
}

func buildStalePendingMoveExpected(
	req ArchivedResourcePendingMoveCancelRequest,
) (*models.TaskResourceCleanupJob, error) {
	scope := req.PendingMoveOperation
	expected := &models.TaskResourceCleanupJob{
		ID:               req.PendingMoveID,
		OperationID:      req.PendingMoveOperation,
		TaskID:           req.TaskID,
		Trigger:          models.TaskResourceCleanupTriggerReconcile,
		State:            models.TaskResourceCleanupStatePending,
		ResourceSnapshot: req.ResourceSnapshot,
		SnapshotVersion:  req.SnapshotVersion,
		SnapshotDigest:   req.SnapshotDigest,
		ResourceKind:     req.ResourceKind,
		ResourceID:       req.ResourceID,
		ManagedRootKey:   req.ManagedRootKey,
		AnchorRevision:   0,
		ActiveScopeKey:   &scope,
	}
	if _, _, err := models.ValidateArchivedResourceReconcileJobHeaders(expected); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrArchivedResourcePendingMoveInvalid, err)
	}
	return expected, nil
}

func (s *Service) ReleaseAbsentArchivedResourceTarget(
	ctx context.Context,
	req ArchivedResourceReleaseRequest,
) (*ArchivedResourceReleaseResult, error) {
	if !s.archivedResourcePhysicalReleaseFlagOn() {
		return nil, ErrArchivedResourceReleaseDisabled
	}
	if err := validateArchivedResourceReleaseRequest(req); err != nil {
		return nil, err
	}
	// The release admission must run through the sealed central physicaldelete
	// admission BEFORE any terminal repository read or mutation. A nil or
	// unavailable admission is fail-closed — zero repo reads, zero repo writes.
	if s.physicalDeleteAdmission == nil {
		return nil, ErrArchivedResourceReleaseAdmissionUnavailable
	}
	if err := s.invokeAbsentTargetAdmission(ctx, req); err != nil {
		return nil, err
	}
	repo, err := s.archivedResourceTerminalRepoFor(ErrArchivedResourceReleaseUnavailable)
	if err != nil {
		return nil, err
	}
	job, err := buildArchivedResourceReleaseJob(req)
	if err != nil {
		return nil, err
	}
	admission, err := repo.ReleaseAbsentArchivedResourceAnchor(ctx, job)
	if err != nil {
		return nil, err
	}
	if admission == nil || admission.Job == nil {
		return nil, ErrArchivedResourceReleaseUnknown
	}
	return &ArchivedResourceReleaseResult{
		OperationID:      admission.Job.OperationID,
		State:            string(admission.Job.State),
		Targets:          1,
		PhysicalRetained: true,
		PhysicalRemoved:  false,
	}, nil
}

// invokeAbsentTargetAdmission runs the sealed ActionReleaseAbsent admission
// and validates the receipt. The receipt is the only authoritative proof that
// the targeted retained anchor's path and Git registration are absent from
// the writer-DB inventory and root policy. A denied, mutated, or non-noop
// receipt fails closed before any repository access.
func (s *Service) invokeAbsentTargetAdmission(
	ctx context.Context,
	req ArchivedResourceReleaseRequest,
) error {
	// The release request binds to the retained anchor by its canonical
	// identity (operation_id, snapshot_digest, etc.). The admission
	// identifies the anchor by its worktree_id (Resource.ID) so the receipt
	// observes the v0.88 ResourceID convention; every other identity field
	// is verified inside the sealed admission.
	managedRootKey, err := physicaldelete.ComputeAnchorManagedRootKey(req.AnchorWorktreePath)
	if err != nil {
		return err
	}
	physicalRequest := physicaldelete.Request{
		Action:    physicaldelete.ActionReleaseAbsent,
		Authority: physicaldelete.AuthorityAdmin,
		Executor:  physicaldelete.ExecutorNone,
		Force:     false,
		Resource: physicaldelete.Resource{
			Kind: physicaldelete.ResourceKindEnvironmentRepo,
			ID:   req.AnchorWorktreeID,
			Path: req.AnchorWorktreePath,
		},
		AnchorIdentity: physicaldelete.AnchorIdentity{
			OperationID:     req.AnchorOperationID,
			SnapshotDigest:  req.AnchorDigest,
			ResourceKind:    "git_worktree",
			ResourceID:      req.AnchorWorktreeID,
			TaskID:          req.AnchorTaskID,
			ManagedRootKey:  managedRootKey,
			SnapshotVersion: 2,
		},
	}
	receipt, err := s.physicalDeleteAdmission.Execute(ctx, physicalRequest)
	if err != nil {
		if errors.Is(err, physicaldelete.ErrInvalidRequest) ||
			errors.Is(err, physicaldelete.ErrInventoryIncomplete) ||
			errors.Is(err, physicaldelete.ErrProtectedResource) ||
			errors.Is(err, physicaldelete.ErrLockUnavailable) ||
			errors.Is(err, physicaldelete.ErrReleaseNotAdmitted) {
			return ErrArchivedResourceReleaseAdmissionDenied
		}
		return ErrArchivedResourceReleaseAdmissionUnavailable
	}
	if receipt.Mutated {
		return ErrArchivedResourceReleaseAdmissionMutated
	}
	if receipt.Executor != physicaldelete.ExecutorNone {
		return ErrArchivedResourceReleaseAdmissionMutated
	}
	if receipt.Action != physicaldelete.ActionReleaseAbsent {
		return ErrArchivedResourceReleaseAdmissionMutated
	}
	if receipt.ResourceKind != physicaldelete.ResourceKindEnvironmentRepo ||
		receipt.ResourceID != req.AnchorWorktreeID {
		return ErrArchivedResourceReleaseAdmissionMutated
	}
	return nil
}

func (s *Service) archivedResourcePhysicalReleaseFlagOn() bool {
	s.archivedResourceFeatureMu.Lock()
	defer s.archivedResourceFeatureMu.Unlock()
	return s.archivedResourcePhysicalReleaseOn
}

func validateArchivedResourceReleaseRequest(req ArchivedResourceReleaseRequest) error {
	for name, value := range map[string]string{
		"anchor_operation_id":   req.AnchorOperationID,
		"anchor_digest":         req.AnchorDigest,
		"anchor_task_id":        req.AnchorTaskID,
		"anchor_worktree_id":    req.AnchorWorktreeID,
		"anchor_repository_id":  req.AnchorRepository,
		"anchor_branch":         req.AnchorBranch,
		"anchor_head_oid":       req.AnchorHeadOID,
		"anchor_worktree_path":  req.AnchorWorktreePath,
		"anchor_git_common_dir": req.AnchorGitCommonDir,
		"released_at":           req.ReleasedAt,
	} {
		if value == "" {
			return fmt.Errorf("%w: %s is required", ErrArchivedResourceReleaseInvalid, name)
		}
	}
	return nil
}

func buildArchivedResourceReleaseJob(
	req ArchivedResourceReleaseRequest,
) (*models.TaskResourceCleanupJob, error) {
	immutable := models.ArchivedResourceReleaseImmutable{
		AnchorOperationID:  req.AnchorOperationID,
		AnchorDigest:       req.AnchorDigest,
		AnchorTaskID:       req.AnchorTaskID,
		AnchorWorktreeID:   req.AnchorWorktreeID,
		AnchorRepository:   req.AnchorRepository,
		AnchorBranch:       req.AnchorBranch,
		AnchorHeadOID:      req.AnchorHeadOID,
		AnchorWorktreePath: req.AnchorWorktreePath,
		AnchorGitCommonDir: req.AnchorGitCommonDir,
	}
	release := models.ArchivedResourceReleaseReleaseProof{
		PhysicalPath: req.AnchorWorktreePath,
		GitWorktreeRegistration: models.ArchivedResourceReleaseGitRegistration{
			WorktreePath: req.AnchorWorktreePath,
			Branch:       req.AnchorBranch,
			HeadOID:      req.AnchorHeadOID,
		},
		ReleasedAt: req.ReleasedAt,
	}
	_, raw, identity, err := models.NewArchivedResourceReleaseSnapshot(immutable, release)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrArchivedResourceReleaseInvalid, err)
	}
	managedRootKey, err := models.ArchivedResourceReleaseManagedRootKey(req.AnchorWorktreePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrArchivedResourceReleaseInvalid, err)
	}
	now := time.Now().UTC()
	return &models.TaskResourceCleanupJob{
		ID:               identity.OperationID,
		OperationID:      identity.OperationID,
		TaskID:           req.AnchorTaskID,
		Trigger:          models.TaskResourceCleanupTriggerReconcile,
		State:            models.TaskResourceCleanupStatePending,
		ResourceSnapshot: string(raw),
		SnapshotVersion:  models.ArchivedResourceReconcileSnapshotVersion,
		SnapshotDigest:   identity.SnapshotDigest,
		ResourceKind:     identity.ResourceKind,
		ResourceID:       identity.ResourceID,
		ManagedRootKey:   managedRootKey,
		AnchorRevision:   0,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

func (s *Service) RetireStaleArchivedResourceEnvironmentReference(
	ctx context.Context,
	req ArchivedResourceEnvironmentRetirementRequest,
) (*ArchivedResourceEnvironmentRetirementResult, error) {
	if !s.archivedResourceReconcileEnabled() {
		return nil, ErrArchivedResourceEnvironmentRetirementDisabled
	}
	if err := validateArchivedResourceEnvironmentRetirementRequest(req); err != nil {
		return nil, err
	}
	repo, err := s.archivedResourceTerminalRepoFor(ErrArchivedResourceEnvironmentRetirementUnavailable)
	if err != nil {
		return nil, err
	}
	job, err := buildArchivedResourceEnvironmentRetirementJob(req)
	if err != nil {
		return nil, err
	}
	identity, err := repo.RetireStaleArchivedResourceEnvironmentReference(ctx, job)
	if err != nil {
		return nil, err
	}
	if identity == nil {
		return nil, ErrArchivedResourceEnvironmentRetirementUnavailable
	}
	return &ArchivedResourceEnvironmentRetirementResult{
		EnvironmentID:    req.EnvironmentID,
		State:            string(models.TaskResourceCleanupStateSucceeded),
		Repositories:     len(req.Repositories),
		PhysicalRetained: true,
		PhysicalRemoved:  false,
	}, nil
}

func validateArchivedResourceEnvironmentRetirementRequest(
	req ArchivedResourceEnvironmentRetirementRequest,
) error {
	if req.EnvironmentID == "" {
		return fmt.Errorf("%w: environment id is required", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	if req.TaskID == "" {
		return fmt.Errorf("%w: task id is required", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	if req.EnvironmentStatus != "stopped" && req.EnvironmentStatus != "failed" {
		return fmt.Errorf("%w: environment status must be stopped or failed", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	if len(req.Repositories) == 0 {
		return fmt.Errorf("%w: at least one repository is required", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	if len(req.Repositories) > models.ArchivedResourceEnvironmentRetirementMaxRepositories {
		return fmt.Errorf("%w: too many repositories", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	if req.RetiredAt == "" {
		return fmt.Errorf("%w: retired_at is required", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	for i, repo := range req.Repositories {
		if repo.ID == "" || repo.RepositoryID == "" || repo.WorktreeID == "" || repo.WorktreeBranch == "" {
			return fmt.Errorf("%w: repository %d identity is required", ErrArchivedResourceEnvironmentRetirementInvalid, i)
		}
		if repo.CreatedAt == "" || repo.UpdatedAt == "" {
			return fmt.Errorf("%w: repository %d timestamps are required", ErrArchivedResourceEnvironmentRetirementInvalid, i)
		}
	}
	return nil
}

func buildArchivedResourceEnvironmentRetirementJob(
	req ArchivedResourceEnvironmentRetirementRequest,
) (*models.TaskResourceCleanupJob, error) {
	repositories := make([]models.ArchivedResourceEnvironmentRetirementRepository, 0, len(req.Repositories))
	for _, repo := range req.Repositories {
		repositories = append(repositories, models.ArchivedResourceEnvironmentRetirementRepository{
			ID:             repo.ID,
			RepositoryID:   repo.RepositoryID,
			BranchSlug:     repo.BranchSlug,
			WorktreeID:     repo.WorktreeID,
			WorktreePath:   repo.WorktreePath,
			WorktreeBranch: repo.WorktreeBranch,
			Position:       repo.Position,
			Status:         repo.Status,
			CreatedAt:      repo.CreatedAt,
			UpdatedAt:      repo.UpdatedAt,
			MergedAt:       repo.MergedAt,
			DeletedAt:      repo.DeletedAt,
		})
	}
	immutable := models.ArchivedResourceEnvironmentRetirementImmutable{
		EnvironmentID:     req.EnvironmentID,
		TaskID:            req.TaskID,
		EnvironmentStatus: req.EnvironmentStatus,
		Repositories:      repositories,
	}
	proof := models.ArchivedResourceEnvironmentRetirementProof{RetiredAt: req.RetiredAt}
	_, raw, identity, err := models.NewArchivedResourceEnvironmentRetirementSnapshot(immutable, proof)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrArchivedResourceEnvironmentRetirementInvalid, err)
	}
	managedRootKey, _ := models.ArchivedResourceEnvironmentRetirementManagedRootKey(req.Repositories[0].WorktreePath)
	now := time.Now().UTC()
	return &models.TaskResourceCleanupJob{
		ID:               identity.OperationID,
		OperationID:      identity.OperationID,
		TaskID:           req.TaskID,
		Trigger:          models.TaskResourceCleanupTriggerReconcile,
		State:            models.TaskResourceCleanupStatePending,
		ResourceSnapshot: string(raw),
		SnapshotVersion:  models.ArchivedResourceEnvironmentRetirementSnapshotVersion,
		SnapshotDigest:   identity.SnapshotDigest,
		ResourceKind:     identity.ResourceKind,
		ResourceID:       identity.ResourceID,
		ManagedRootKey:   managedRootKey,
		AnchorRevision:   0,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

// Compile-time guard: ensure the runtime repository implementation continues to
// satisfy ArchivedResourceTerminalRepository as the surface evolves.
var _ ArchivedResourceTerminalRepository = (repository.ArchivedResourceReconcileRepository)(nil)
