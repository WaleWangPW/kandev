package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/physicaldelete"
	"github.com/kandev/kandev/internal/task/models"
)

// admissionOrderSpy records the order in which the sealed admission runs
// relative to the repository CAS. It can also synthesize deny / unavailable
// receipts to verify the fail-closed contract.
type admissionOrderSpy struct {
	mu                  sync.Mutex
	calls               int
	receipts            []physicaldelete.Request
	deny                bool
	denyError           error
	mutateReceipt       bool
	mutatedAction       physicaldelete.Action
	mutatedExecutor     physicaldelete.Executor
	mutatedResourceKind physicaldelete.ResourceKind
}

func (s *admissionOrderSpy) BeginProvisional(_ context.Context, _ physicaldelete.CreateRequest) (physicaldelete.ProvisionalLease, error) {
	return physicaldelete.ProvisionalLease{}, errors.New("provisional rollback not in use")
}

func (s *admissionOrderSpy) Execute(_ context.Context, req physicaldelete.Request) (physicaldelete.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.receipts = append(s.receipts, req)
	if s.denyError != nil {
		return physicaldelete.Receipt{}, s.denyError
	}
	if s.deny {
		return physicaldelete.Receipt{
			Decision:     physicaldelete.DecisionDeny,
			Reason:       physicaldelete.DenialProtected,
			Action:       req.Action,
			ResourceKind: req.Resource.Kind,
			ResourceID:   req.Resource.ID,
			Executor:     req.Executor,
			Mutated:      false,
			At:           time.Now().UTC(),
		}, physicaldelete.ErrProtectedResource
	}
	// Default success path returns a sealed no-op receipt with the requested
	// action/executor/kind preserved. Per-field overrides (when non-zero)
	// mutate the corresponding receipt field so each validation rule has a
	// dedicated failure mode. mutateReceipt also flips Mutated=true so the
	// service's "no physical mutation" guard fires.
	action := req.Action
	executor := req.Executor
	resourceKind := req.Resource.Kind
	if s.mutatedAction != "" {
		action = s.mutatedAction
	}
	if s.mutatedExecutor != "" {
		executor = s.mutatedExecutor
	}
	if s.mutatedResourceKind != "" {
		resourceKind = s.mutatedResourceKind
	}
	if !s.mutateReceipt && executor == "" {
		// Preserve ExecutorNone on the success path.
		executor = physicaldelete.ExecutorNone
	}
	return physicaldelete.Receipt{
		Decision:     physicaldelete.DecisionDeny,
		Reason:       "",
		Action:       action,
		ResourceKind: resourceKind,
		ResourceID:   req.Resource.ID,
		Executor:     executor,
		Mutated:      s.mutateReceipt,
		At:           time.Now().UTC(),
	}, nil
}

func (s *admissionOrderSpy) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// releaseRepoSpy tracks every call made to the release repository after the
// service has invoked admission. The spy fails the test if any terminal call
// happens without a prior admission call.
type releaseRepoSpy struct {
	mu         sync.Mutex
	calls      []string
	shouldFail bool
}

func (r *releaseRepoSpy) ReleaseAbsentArchivedResourceAnchor(_ context.Context, job *models.TaskResourceCleanupJob) (*models.ArchivedResourceReleaseAdmission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "release:"+job.OperationID)
	if r.shouldFail {
		return nil, errors.New("repo failure")
	}
	completedAt := time.Now().UTC()
	return &models.ArchivedResourceReleaseAdmission{
		Job: &models.TaskResourceCleanupJob{
			OperationID: job.OperationID,
			State:       models.TaskResourceCleanupStateReleased,
			CompletedAt: &completedAt,
		},
		Reason: "release_absent",
	}, nil
}

func (r *releaseRepoSpy) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// terminalReleaseRepos satisfies both TaskResourceCleanupRepository and
// ArchivedResourceTerminalRepository. The release spy is the only method that
// is meaningful for the release path; every other method panics so any
// unexpected repo read/mutation fails the test loudly.
type terminalReleaseRepos struct {
	release *releaseRepoSpy
}

func (r *terminalReleaseRepos) ReleaseAbsentArchivedResourceAnchor(ctx context.Context, job *models.TaskResourceCleanupJob) (*models.ArchivedResourceReleaseAdmission, error) {
	return r.release.ReleaseAbsentArchivedResourceAnchor(ctx, job)
}

func (r *terminalReleaseRepos) CancelStaleArchivedResourcePendingMove(context.Context, *models.TaskResourceCleanupJob) (bool, error) {
	return false, nil
}

func (r *terminalReleaseRepos) RetireStaleArchivedResourceEnvironmentReference(context.Context, *models.TaskResourceCleanupJob) (*models.ArchivedResourceEnvironmentRetirementIdentity, error) {
	return &models.ArchivedResourceEnvironmentRetirementIdentity{}, nil
}

func (r *terminalReleaseRepos) CreateTaskResourceCleanupJob(context.Context, *models.TaskResourceCleanupJob) error {
	return errors.New("terminalReleaseRepos.CreateTaskResourceCleanupJob must not be invoked")
}

func (r *terminalReleaseRepos) HasActiveTaskResourceCleanupJob(context.Context, string) (bool, error) {
	return false, nil
}

func (r *terminalReleaseRepos) UpdateTaskResourceCleanupSnapshot(context.Context, string, string) error {
	return nil
}

func (r *terminalReleaseRepos) GetTaskResourceCleanupJob(context.Context, string) (*models.TaskResourceCleanupJob, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) GetTaskResourceCleanupJobByOperationID(context.Context, string) (*models.TaskResourceCleanupJob, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) ListPreparedTaskResourceCleanupJobs(context.Context) ([]*models.TaskResourceCleanupJob, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) ListDueTaskResourceCleanupJobs(_ context.Context, _ time.Time, _ int) ([]*models.TaskResourceCleanupJob, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) StartPreparedTaskResourceCleanupJob(context.Context, string) (bool, error) {
	return false, nil
}

func (r *terminalReleaseRepos) MarkTaskResourceCleanupJobRunning(context.Context, string) (bool, error) {
	return false, nil
}

func (r *terminalReleaseRepos) CompleteClaimedTaskResourceCleanupJob(context.Context, string, int, models.TaskResourceCleanupState, string, *time.Time) (bool, error) {
	return false, nil
}

func (r *terminalReleaseRepos) CompleteTaskResourceCleanupJob(context.Context, string, models.TaskResourceCleanupState, string, *time.Time) error {
	return nil
}

func (r *terminalReleaseRepos) CancelArchiveTaskResourceCleanupJobs(context.Context, string) error {
	return nil
}

func (r *terminalReleaseRepos) ResetRunningTaskResourceCleanupJobs(context.Context) error {
	return nil
}

func (r *terminalReleaseRepos) AdmitArchivedResourceReconcile(context.Context, *models.TaskResourceCleanupJob) (*models.ArchivedResourceReconcileAdmission, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) ClaimArchivedResourceReconcileJob(context.Context, string) (*models.TaskResourceCleanupJob, bool, error) {
	return nil, false, nil
}

func (r *terminalReleaseRepos) CompleteArchivedResourceReconcileRetention(context.Context, string, int) (*models.ArchivedResourceReconcileCompletion, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) CancelNeverClaimedArchivedResourceReconcile(context.Context, *models.TaskResourceCleanupJob) (bool, error) {
	return false, nil
}

func (r *terminalReleaseRepos) ListArchivedResourceReconcileJobsByTaskID(context.Context, string) ([]*models.TaskResourceCleanupJob, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) ListDueArchivedResourceReconcileJobs(_ context.Context, _ time.Time, _ int) ([]*models.TaskResourceCleanupJob, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) ListRunningArchivedResourceReconcileJobs(context.Context) ([]*models.TaskResourceCleanupJob, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) GetRunningArchivedResourceReconcileJob(context.Context, string) (*models.TaskResourceCleanupJob, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) RestoreArchivedResourceReconcileRetention(context.Context, string) (bool, error) {
	return false, nil
}

func (r *terminalReleaseRepos) AdmitArchivedResourceGroupReconcile(context.Context, *models.TaskResourceCleanupJob) (*models.ArchivedResourceReconcileAdmission, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) CompleteArchivedResourceGroupReconcileRetention(context.Context, string, int) (*models.ArchivedResourceReconcileCompletion, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) RestoreArchivedResourceGroupReconcileRetention(context.Context, string) (bool, error) {
	return false, nil
}

func (r *terminalReleaseRepos) ListArchivedResourceGroupReconcileJobsByParticipant(context.Context, string) ([]*models.TaskResourceCleanupJob, error) {
	return nil, nil
}

func (r *terminalReleaseRepos) ResolveArchivedResourceReconcileUnarchive(context.Context, string, time.Time, string, bool) (bool, error) {
	return false, nil
}

func terminalReleaseRequestFixture() ArchivedResourceReleaseRequest {
	return ArchivedResourceReleaseRequest{
		AnchorOperationID:  "operation-anchor",
		AnchorDigest:       "digest",
		AnchorTaskID:       "task-anchor",
		AnchorWorktreeID:   "wt-anchor",
		AnchorRepository:   "repository-company",
		AnchorBranch:       "feature/synthetic",
		AnchorHeadOID:      strings.Repeat("a", 40),
		AnchorWorktreePath: "/tmp/release/path",
		AnchorGitCommonDir: "/tmp/release/.git",
		ReleasedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func buildReleaseTestService(t *testing.T, admission physicaldelete.Admission, repo *releaseRepoSpy) *Service {
	t.Helper()
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "json"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	svc := NewService(Repos{}, nil, log, RepositoryDiscoveryConfig{})
	svc.SetArchivedResourceFeatures(true, true)
	svc.SetPhysicalDeleteAdmission(admission)
	svc.resourceCleanups = &terminalReleaseRepos{release: repo}
	return svc
}

func TestTerminalReleaseRunsAdmissionBeforeAnyRepositoryCall(t *testing.T) {
	admission := &admissionOrderSpy{}
	repo := &releaseRepoSpy{}
	svc := buildReleaseTestService(t, admission, repo)

	result, err := svc.ReleaseAbsentArchivedResourceTarget(context.Background(), terminalReleaseRequestFixture())
	if err != nil {
		t.Fatalf("ReleaseAbsentArchivedResourceTarget: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if admission.callCount() != 1 {
		t.Fatalf("admission call count = %d, want 1", admission.callCount())
	}
	if repo.callCount() != 1 {
		t.Fatalf("repo call count = %d, want 1", repo.callCount())
	}
	if len(admission.receipts) != 1 {
		t.Fatalf("recorded admission receipts = %d, want 1", len(admission.receipts))
	}
	got := admission.receipts[0]
	if got.Action != physicaldelete.ActionReleaseAbsent ||
		got.Authority != physicaldelete.AuthorityAdmin ||
		got.Executor != physicaldelete.ExecutorNone ||
		got.Resource.Kind != physicaldelete.ResourceKindEnvironmentRepo ||
		got.Resource.Path != "/tmp/release/path" ||
		got.Resource.ID != "wt-anchor" {
		t.Fatalf("admission request shape wrong: %#v", got)
	}
}

func TestTerminalReleaseFailsClosedWhenAdmissionIsNil(t *testing.T) {
	repo := &releaseRepoSpy{}
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "json"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	svc := NewService(Repos{}, nil, log, RepositoryDiscoveryConfig{})
	svc.SetArchivedResourceFeatures(true, true)
	// Intentionally NOT wiring admission: it stays nil.
	svc.resourceCleanups = &terminalReleaseRepos{release: repo}

	_, err = svc.ReleaseAbsentArchivedResourceTarget(context.Background(), terminalReleaseRequestFixture())
	if !errors.Is(err, ErrArchivedResourceReleaseAdmissionUnavailable) {
		t.Fatalf("nil admission error = %v, want ErrArchivedResourceReleaseAdmissionUnavailable", err)
	}
	if repo.callCount() != 0 {
		t.Fatalf("repo called %d times despite nil admission; want 0", repo.callCount())
	}
}

func TestTerminalReleaseFailsClosedWhenAdmissionDeny(t *testing.T) {
	admission := &admissionOrderSpy{deny: true}
	repo := &releaseRepoSpy{}
	svc := buildReleaseTestService(t, admission, repo)

	_, err := svc.ReleaseAbsentArchivedResourceTarget(context.Background(), terminalReleaseRequestFixture())
	if !errors.Is(err, ErrArchivedResourceReleaseAdmissionDenied) {
		t.Fatalf("denied admission error = %v, want ErrArchivedResourceReleaseAdmissionDenied", err)
	}
	if repo.callCount() != 0 {
		t.Fatalf("repo called %d times despite denied admission; want 0", repo.callCount())
	}
	if admission.callCount() != 1 {
		t.Fatalf("admission should still be invoked once; got %d", admission.callCount())
	}
}

func TestTerminalReleaseFailsClosedWhenAdmissionReturnsMutatedReceipt(t *testing.T) {
	admission := &admissionOrderSpy{mutateReceipt: true}
	repo := &releaseRepoSpy{}
	svc := buildReleaseTestService(t, admission, repo)

	_, err := svc.ReleaseAbsentArchivedResourceTarget(context.Background(), terminalReleaseRequestFixture())
	if !errors.Is(err, ErrArchivedResourceReleaseAdmissionMutated) {
		t.Fatalf("mutated receipt error = %v, want ErrArchivedResourceReleaseAdmissionMutated", err)
	}
	if repo.callCount() != 0 {
		t.Fatalf("repo called %d times despite mutated receipt; want 0", repo.callCount())
	}
}

func TestTerminalReleaseFailsClosedWhenAdmissionReturnsWrongExecutor(t *testing.T) {
	admission := &admissionOrderSpy{
		mutatedExecutor: physicaldelete.ExecutorFilesystem,
	}
	repo := &releaseRepoSpy{}
	svc := buildReleaseTestService(t, admission, repo)

	_, err := svc.ReleaseAbsentArchivedResourceTarget(context.Background(), terminalReleaseRequestFixture())
	if !errors.Is(err, ErrArchivedResourceReleaseAdmissionMutated) {
		t.Fatalf("wrong-executor receipt error = %v, want ErrArchivedResourceReleaseAdmissionMutated", err)
	}
	if repo.callCount() != 0 {
		t.Fatalf("repo called %d times despite wrong executor; want 0", repo.callCount())
	}
}

func TestTerminalReleaseFailsClosedWhenAdmissionReturnsWrongAction(t *testing.T) {
	admission := &admissionOrderSpy{
		mutatedAction: physicaldelete.ActionRegisteredWorktreeRemove,
	}
	repo := &releaseRepoSpy{}
	svc := buildReleaseTestService(t, admission, repo)

	_, err := svc.ReleaseAbsentArchivedResourceTarget(context.Background(), terminalReleaseRequestFixture())
	if !errors.Is(err, ErrArchivedResourceReleaseAdmissionMutated) {
		t.Fatalf("wrong-action receipt error = %v, want ErrArchivedResourceReleaseAdmissionMutated", err)
	}
	if repo.callCount() != 0 {
		t.Fatalf("repo called %d times despite wrong action; want 0", repo.callCount())
	}
}

func TestTerminalReleaseFailsClosedWhenAdmissionInventoryIncomplete(t *testing.T) {
	admission := &admissionOrderSpy{denyError: physicaldelete.ErrInventoryIncomplete}
	repo := &releaseRepoSpy{}
	svc := buildReleaseTestService(t, admission, repo)

	_, err := svc.ReleaseAbsentArchivedResourceTarget(context.Background(), terminalReleaseRequestFixture())
	if !errors.Is(err, ErrArchivedResourceReleaseAdmissionDenied) {
		t.Fatalf("incomplete inventory error = %v, want ErrArchivedResourceReleaseAdmissionDenied", err)
	}
	if repo.callCount() != 0 {
		t.Fatalf("repo called %d times despite incomplete inventory; want 0", repo.callCount())
	}
}

func TestTerminalReleaseFailsClosedWhenAdmissionLockedOut(t *testing.T) {
	admission := &admissionOrderSpy{denyError: physicaldelete.ErrLockUnavailable}
	repo := &releaseRepoSpy{}
	svc := buildReleaseTestService(t, admission, repo)

	_, err := svc.ReleaseAbsentArchivedResourceTarget(context.Background(), terminalReleaseRequestFixture())
	if !errors.Is(err, ErrArchivedResourceReleaseAdmissionDenied) {
		t.Fatalf("locked-out error = %v, want ErrArchivedResourceReleaseAdmissionDenied", err)
	}
	if repo.callCount() != 0 {
		t.Fatalf("repo called %d times despite lock unavailable; want 0", repo.callCount())
	}
}

func TestTerminalReleaseFailsClosedWhenAdmissionUnavailable(t *testing.T) {
	admission := &admissionOrderSpy{denyError: errors.New("admission exploded")}
	repo := &releaseRepoSpy{}
	svc := buildReleaseTestService(t, admission, repo)

	_, err := svc.ReleaseAbsentArchivedResourceTarget(context.Background(), terminalReleaseRequestFixture())
	if !errors.Is(err, ErrArchivedResourceReleaseAdmissionUnavailable) {
		t.Fatalf("unavailable admission error = %v, want ErrArchivedResourceReleaseAdmissionUnavailable", err)
	}
	if repo.callCount() != 0 {
		t.Fatalf("repo called %d times despite unavailable admission; want 0", repo.callCount())
	}
}

func TestTerminalReleaseReceiptContractHoldsOnSuccess(t *testing.T) {
	admission := &admissionOrderSpy{}
	repo := &releaseRepoSpy{}
	svc := buildReleaseTestService(t, admission, repo)

	result, err := svc.ReleaseAbsentArchivedResourceTarget(context.Background(), terminalReleaseRequestFixture())
	if err != nil {
		t.Fatalf("ReleaseAbsentArchivedResourceTarget: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.PhysicalRetained || result.PhysicalRemoved {
		t.Fatalf("physical contract wrong: retained=%v removed=%v", result.PhysicalRetained, result.PhysicalRemoved)
	}
	if result.OperationID == "" || result.State != string(models.TaskResourceCleanupStateReleased) {
		t.Fatalf("result metadata wrong: op=%q state=%q", result.OperationID, result.State)
	}
	if result.Targets != 1 {
		t.Fatalf("targets = %d, want 1", result.Targets)
	}
}
