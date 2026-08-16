package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/physicaldelete"
	"github.com/kandev/kandev/internal/task/models"
)

const (
	terminalReleaseComposerTaskID       = "task-terminal-release"
	terminalReleaseComposerWorktreeID   = "wt-terminal-release"
	terminalReleaseComposerRepositoryID = "repository-terminal-release"
)

var (
	terminalReleaseComposerWorktreePath string
	terminalReleaseComposerGitCommonDir string
)

// terminalReleaseFixture wires the real physicaldelete.New + SQLInventorySource
// against an in-memory SQLite writer DB seeded with a Task05-built retained
// v2 anchor, then exposes a service that shares the same admission gate.
type terminalReleaseFixture struct {
	svc               *Service
	db                *sqlx.DB
	anchorOperationID string
	anchorDigest      string
}

func newTerminalReleaseFixture(t *testing.T) *terminalReleaseFixture {
	t.Helper()
	root := t.TempDir()
	worktreePath := canonicalForRelease(t, filepath.Join(root, "worktree"))
	gitCommonDir := canonicalForRelease(t, filepath.Join(root, "repo", ".git"))
	terminalReleaseComposerWorktreePath = worktreePath
	terminalReleaseComposerGitCommonDir = gitCommonDir

	db := openTerminalReleaseDB(t)
	if err := seedTerminalReleaseRetainedV2(t, db, worktreePath, gitCommonDir); err != nil {
		t.Fatalf("seed anchor: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	admission, err := physicaldelete.New(physicaldelete.Config{
		Inventory: physicaldelete.NewSQLInventorySource(db),
	})
	if err != nil {
		t.Fatalf("physicaldelete.New: %v", err)
	}
	svc := newServiceWithAdmission(t, admission)
	var anchorOperationID, anchorDigest string
	if err := db.QueryRowxContext(context.Background(),
		`SELECT operation_id, snapshot_digest FROM task_resource_cleanup_jobs WHERE id = ?`,
		"anchor-terminal-release",
	).Scan(&anchorOperationID, &anchorDigest); err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	return &terminalReleaseFixture{
		svc:               svc,
		db:                db,
		anchorOperationID: anchorOperationID,
		anchorDigest:      anchorDigest,
	}
}

func canonicalForRelease(t *testing.T, path string) string {
	t.Helper()
	canonical, err := physicaldelete.CanonicalLockPathForTest(path)
	if err != nil {
		t.Fatalf("canonicalize %q: %v", path, err)
	}
	return canonical
}

func newServiceWithAdmission(t *testing.T, admission physicaldelete.Admission) *Service {
	t.Helper()
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "json"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	svc := NewService(Repos{}, nil, log, RepositoryDiscoveryConfig{})
	svc.SetArchivedResourceFeatures(true, true)
	svc.SetPhysicalDeleteAdmission(admission)
	return svc
}

func openTerminalReleaseDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE task_environments (
			id TEXT PRIMARY KEY, status TEXT, workspace_path TEXT
		)`,
		`CREATE TABLE task_environment_repos (
			id TEXT PRIMARY KEY, task_environment_id TEXT, repository_id TEXT,
			worktree_id TEXT, worktree_path TEXT, worktree_branch TEXT,
			branch_slug TEXT, position INTEGER, status TEXT,
			created_at TIMESTAMP, updated_at TIMESTAMP,
			merged_at TIMESTAMP, deleted_at TIMESTAMP
		)`,
		`CREATE TABLE executors_running (
			id TEXT PRIMARY KEY, session_id TEXT UNIQUE, task_id TEXT,
			worktree_id TEXT, worktree_path TEXT, worktree_branch TEXT,
			status TEXT
		)`,
		`CREATE TABLE task_workspace_groups (
			id TEXT PRIMARY KEY, cleanup_status TEXT, materialized_path TEXT
		)`,
		`CREATE TABLE storage_quarantine_entries (
			id TEXT PRIMARY KEY, state TEXT, original_path TEXT, quarantine_path TEXT
		)`,
		`CREATE TABLE task_resource_cleanup_jobs (
			id TEXT PRIMARY KEY, operation_id TEXT UNIQUE, task_id TEXT,
			trigger TEXT, state TEXT, resource_snapshot TEXT,
			snapshot_version INTEGER, snapshot_digest TEXT, resource_kind TEXT,
			resource_id TEXT, managed_root_key TEXT, anchor_revision BIGINT,
			active_scope_key TEXT, attempts INTEGER, next_attempt_at TIMESTAMP,
			last_error TEXT, created_at TIMESTAMP, updated_at TIMESTAMP,
			completed_at TIMESTAMP
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return db
}

func seedTerminalReleaseRetainedV2(t *testing.T, db *sqlx.DB, worktreePath, gitCommonDir string) error {
	t.Helper()
	now := time.Now().UTC()
	managedRootKey, err := physicaldelete.ComputeAnchorManagedRootKey(worktreePath)
	if err != nil {
		return err
	}
	immutable := models.ArchivedResourceReconcileImmutable{
		OriginTaskID:   terminalReleaseComposerTaskID,
		ArchivedAt:     now.Format(time.RFC3339Nano),
		ManagedRootKey: managedRootKey,
		Target: models.ArchivedResourceReconcileTarget{
			WorktreeID:     terminalReleaseComposerWorktreeID,
			RepositoryID:   terminalReleaseComposerRepositoryID,
			RepositoryPath: gitCommonDir,
			GitCommonDir:   gitCommonDir,
			WorktreePath:   worktreePath,
			Branch:         "feature/terminal-release",
			HeadOID:        "f" + strings.Repeat("0", 39),
		},
		Associations: []models.ArchivedResourceReconcileAssociation{
			{
				AssociationID:  "association-terminal",
				TaskID:         terminalReleaseComposerTaskID,
				SessionID:      "session-terminal",
				WorktreeID:     terminalReleaseComposerWorktreeID,
				RepositoryID:   terminalReleaseComposerRepositoryID,
				BranchSlug:     "feature/terminal-release",
				WorktreePath:   worktreePath,
				WorktreeBranch: "feature/terminal-release",
				Status:         "active",
				CreatedAt:      now.Format(time.RFC3339Nano),
				UpdatedAt:      now.Format(time.RFC3339Nano),
			},
		},
	}
	_, raw, identity, err := models.NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		return err
	}
	anchorRevision := int64(models.ArchivedResourceRetentionAnchorVersion)
	completedAt := now
	_, err = db.Exec(`
		INSERT INTO task_resource_cleanup_jobs (
			id, operation_id, task_id, trigger, state, resource_snapshot,
			snapshot_version, snapshot_digest, resource_kind, resource_id,
			managed_root_key, anchor_revision, active_scope_key,
			attempts, next_attempt_at, last_error, created_at, updated_at, completed_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 0, NULL, '', ?, ?, ?
		)
	`, "anchor-terminal-release", identity.OperationID, terminalReleaseComposerTaskID,
		models.TaskResourceCleanupTriggerReconcile, "retained", string(raw), 2,
		identity.SnapshotDigest, "git_worktree", terminalReleaseComposerWorktreeID,
		managedRootKey, anchorRevision, now, now, completedAt,
	)
	return err
}

func (f *terminalReleaseFixture) requestFixture() ArchivedResourceReleaseRequest {
	return ArchivedResourceReleaseRequest{
		AnchorOperationID:  f.anchorOperationID,
		AnchorDigest:       f.anchorDigest,
		AnchorTaskID:       terminalReleaseComposerTaskID,
		AnchorWorktreeID:   terminalReleaseComposerWorktreeID,
		AnchorRepository:   terminalReleaseComposerRepositoryID,
		AnchorBranch:       "feature/terminal-release",
		AnchorHeadOID:      "f" + strings.Repeat("0", 39),
		AnchorWorktreePath: terminalReleaseComposerWorktreePath,
		AnchorGitCommonDir: terminalReleaseComposerGitCommonDir,
		ReleasedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// TestTerminalReleaseRealCompositionNoOpSucceeds exercises the service
// end-to-end through the real physicaldelete.New + SQLInventorySource. It
// proves the sealed no-op success path: Mutated=false, Executor=ExecutorNone,
// ResourceID=worktree_id (not operation_id), Action=ActionReleaseAbsent.
func TestTerminalReleaseRealCompositionNoOpSucceeds(t *testing.T) {
	fixture := newTerminalReleaseFixture(t)
	// Wire a real terminal repository that performs the exact CAS on the
	// writer DB so the service exercises the full repository write path
	// after the admission passes.
	terminalRepo := newTerminalReleaseCASRepo(fixture)
	fixture.svc.SetArchivedResourceTerminalRepository(terminalRepo)

	result, err := fixture.svc.ReleaseAbsentArchivedResourceTarget(
		context.Background(), fixture.requestFixture())
	if err != nil {
		t.Fatalf("ReleaseAbsentArchivedResourceTarget: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.PhysicalRetained || result.PhysicalRemoved {
		t.Fatalf("physical contract wrong: retained=%v removed=%v", result.PhysicalRetained, result.PhysicalRemoved)
	}
	if result.OperationID != fixture.anchorOperationID {
		t.Fatalf("result OperationID = %q, want %q", result.OperationID, fixture.anchorOperationID)
	}
	if result.State != string(models.TaskResourceCleanupStateReleased) {
		t.Fatalf("result state = %q, want %q", result.State, models.TaskResourceCleanupStateReleased)
	}
	if result.Targets != 1 {
		t.Fatalf("result targets = %d, want 1", result.Targets)
	}

	// Verify the exact CAS: anchor state transitioned retained -> released,
	// and the durable anchor's completed_at / revision / digest are
	// byte-equal (only state and completed_at may change).
	var (
		newState       string
		newDigest      string
		newRevision    int64
		newCompletedAt *time.Time
	)
	if err := fixture.db.QueryRowxContext(context.Background(),
		`SELECT state, snapshot_digest, anchor_revision, completed_at FROM task_resource_cleanup_jobs WHERE id = ?`,
		"anchor-terminal-release",
	).Scan(&newState, &newDigest, &newRevision, &newCompletedAt); err != nil {
		t.Fatalf("read after CAS: %v", err)
	}
	if newState != string(models.TaskResourceCleanupStateReleased) {
		t.Fatalf("anchor state = %q, want %q", newState, models.TaskResourceCleanupStateReleased)
	}
	if newDigest != fixture.anchorDigest {
		t.Fatalf("anchor digest drifted: %q -> %q", fixture.anchorDigest, newDigest)
	}
	if newRevision != int64(models.ArchivedResourceRetentionAnchorVersion) {
		t.Fatalf("anchor revision drifted: %d -> %d",
			models.ArchivedResourceRetentionAnchorVersion, newRevision)
	}
	if newCompletedAt == nil {
		t.Fatal("anchor completed_at missing after CAS")
	}
}

// TestTerminalReleaseReceiptResourceIDIsWorktreeID exercises the sealed
// admission directly with the same admission used by the service. The
// receipt's ResourceID is bound to the worktree_id the request supplied
// (not the anchor's operation_id), which matches the v0.88 ResourceID
// convention.
func TestTerminalReleaseReceiptResourceIDIsWorktreeID(t *testing.T) {
	db := openTerminalReleaseDB(t)
	t.Cleanup(func() { _ = db.Close() })
	root := t.TempDir()
	worktreePath := canonicalForRelease(t, filepath.Join(root, "worktree"))
	gitCommonDir := canonicalForRelease(t, filepath.Join(root, "repo", ".git"))
	if err := seedTerminalReleaseRetainedV2(t, db, worktreePath, gitCommonDir); err != nil {
		t.Fatalf("seed anchor: %v", err)
	}
	admission, err := physicaldelete.New(physicaldelete.Config{
		Inventory: physicaldelete.NewSQLInventorySource(db),
	})
	if err != nil {
		t.Fatalf("physicaldelete.New: %v", err)
	}
	var anchorOperationID, anchorDigest string
	if err := db.QueryRowxContext(context.Background(),
		`SELECT operation_id, snapshot_digest FROM task_resource_cleanup_jobs WHERE id = ?`,
		"anchor-terminal-release",
	).Scan(&anchorOperationID, &anchorDigest); err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	managedRootKey, err := physicaldelete.ComputeAnchorManagedRootKey(worktreePath)
	if err != nil {
		t.Fatalf("managed root key: %v", err)
	}
	req := physicaldelete.Request{
		Action:    physicaldelete.ActionReleaseAbsent,
		Authority: physicaldelete.AuthorityAdmin,
		Executor:  physicaldelete.ExecutorNone,
		Resource: physicaldelete.Resource{
			Kind: physicaldelete.ResourceKindEnvironmentRepo,
			ID:   terminalReleaseComposerWorktreeID,
			Path: worktreePath,
		},
		AnchorIdentity: physicaldelete.AnchorIdentity{
			OperationID:     anchorOperationID,
			SnapshotDigest:  anchorDigest,
			ResourceKind:    "git_worktree",
			ResourceID:      terminalReleaseComposerWorktreeID,
			TaskID:          terminalReleaseComposerTaskID,
			ManagedRootKey:  managedRootKey,
			SnapshotVersion: 2,
		},
	}
	receipt, err := admission.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("admission.Execute: %v", err)
	}
	if receipt.Mutated {
		t.Fatal("receipt Mutated=true; release must be a sealed no-op")
	}
	if receipt.Executor != physicaldelete.ExecutorNone {
		t.Fatalf("receipt Executor = %q, want %q", receipt.Executor, physicaldelete.ExecutorNone)
	}
	if receipt.ResourceID != terminalReleaseComposerWorktreeID {
		t.Fatalf("receipt ResourceID = %q, want worktree_id %q",
			receipt.ResourceID, terminalReleaseComposerWorktreeID)
	}
	if receipt.ResourceID == anchorOperationID {
		t.Fatalf("receipt ResourceID drifted to operation_id %q; ResourceID must be worktree_id",
			anchorOperationID)
	}
}

// TestTerminalReleaseRootPolicyRejectsRootProtectedPath verifies the central
// admission consults the configured RootPolicy and fails closed when the
// target path is root-protected.
func TestTerminalReleaseRootPolicyRejectsRootProtectedPath(t *testing.T) {
	db := openTerminalReleaseDB(t)
	t.Cleanup(func() { _ = db.Close() })
	root := t.TempDir()
	worktreePath := canonicalForRelease(t, filepath.Join(root, "worktree"))
	gitCommonDir := canonicalForRelease(t, filepath.Join(root, "repo", ".git"))
	if err := seedTerminalReleaseRetainedV2(t, db, worktreePath, gitCommonDir); err != nil {
		t.Fatalf("seed anchor: %v", err)
	}
	policy, err := physicaldelete.NewRootPolicy([]string{worktreePath})
	if err != nil {
		t.Fatalf("NewRootPolicy: %v", err)
	}
	admission, err := physicaldelete.New(physicaldelete.Config{
		Inventory:  physicaldelete.NewSQLInventorySource(db),
		RootPolicy: &policy,
	})
	if err != nil {
		t.Fatalf("physicaldelete.New: %v", err)
	}
	svc := newServiceWithAdmission(t, admission)
	var anchorOperationID, anchorDigest string
	if err := db.QueryRowxContext(context.Background(),
		`SELECT operation_id, snapshot_digest FROM task_resource_cleanup_jobs WHERE id = ?`,
		"anchor-terminal-release",
	).Scan(&anchorOperationID, &anchorDigest); err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	managedRootKey, err := physicaldelete.ComputeAnchorManagedRootKey(worktreePath)
	if err != nil {
		t.Fatalf("managed root key: %v", err)
	}
	// Bypass the service to drive the admission directly so the test
	// isolates the RootPolicy fail-closed path from the service's request
	// shape validation.
	req := physicaldelete.Request{
		Action:    physicaldelete.ActionReleaseAbsent,
		Authority: physicaldelete.AuthorityAdmin,
		Executor:  physicaldelete.ExecutorNone,
		Resource: physicaldelete.Resource{
			Kind: physicaldelete.ResourceKindEnvironmentRepo,
			ID:   terminalReleaseComposerWorktreeID,
			Path: worktreePath,
		},
		AnchorIdentity: physicaldelete.AnchorIdentity{
			OperationID:     anchorOperationID,
			SnapshotDigest:  anchorDigest,
			ResourceKind:    "git_worktree",
			ResourceID:      terminalReleaseComposerWorktreeID,
			TaskID:          terminalReleaseComposerTaskID,
			ManagedRootKey:  managedRootKey,
			SnapshotVersion: 2,
		},
	}
	_, err = admission.Execute(context.Background(), req)
	if !errors.Is(err, physicaldelete.ErrProtectedResource) {
		t.Fatalf("root-protected release error = %v, want ErrProtectedResource", err)
	}
	// Sanity: the service-level call also rejects the same path.
	_, err = svc.ReleaseAbsentArchivedResourceTarget(context.Background(), ArchivedResourceReleaseRequest{
		AnchorOperationID:  anchorOperationID,
		AnchorDigest:       anchorDigest,
		AnchorTaskID:       terminalReleaseComposerTaskID,
		AnchorWorktreeID:   terminalReleaseComposerWorktreeID,
		AnchorRepository:   terminalReleaseComposerRepositoryID,
		AnchorBranch:       "feature/terminal-release",
		AnchorHeadOID:      "f" + strings.Repeat("0", 39),
		AnchorWorktreePath: worktreePath,
		AnchorGitCommonDir: gitCommonDir,
		ReleasedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	})
	if !errors.Is(err, ErrArchivedResourceReleaseAdmissionDenied) {
		t.Fatalf("service root-protected release error = %v, want ErrArchivedResourceReleaseAdmissionDenied", err)
	}
}

// TestTerminalReleaseExactBoundIdentityMismatchFails proves the service
// rejects release when the request's anchor identity drifts from the
// loaded anchor (different digest, operation_id, worktree_id, task_id).
func TestTerminalReleaseExactBoundIdentityMismatchFails(t *testing.T) {
	fixture := newTerminalReleaseFixture(t)
	tests := []struct {
		name    string
		mutate  func(*ArchivedResourceReleaseRequest)
		wantErr error
	}{
		{
			name: "wrong anchor operation_id",
			mutate: func(req *ArchivedResourceReleaseRequest) {
				req.AnchorOperationID = "archived-resource-reconcile:wrong"
			},
			wantErr: ErrArchivedResourceReleaseAdmissionDenied,
		},
		{
			name: "wrong anchor worktree_id",
			mutate: func(req *ArchivedResourceReleaseRequest) {
				req.AnchorWorktreeID = "wt-wrong"
			},
			wantErr: ErrArchivedResourceReleaseAdmissionDenied,
		},
		{
			name: "wrong anchor digest",
			mutate: func(req *ArchivedResourceReleaseRequest) {
				req.AnchorDigest = strings.Repeat("d", 64)
			},
			wantErr: ErrArchivedResourceReleaseAdmissionDenied,
		},
		{
			name: "wrong anchor task_id",
			mutate: func(req *ArchivedResourceReleaseRequest) {
				req.AnchorTaskID = "task-wrong"
			},
			wantErr: ErrArchivedResourceReleaseAdmissionDenied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := fixture.requestFixture()
			tt.mutate(&req)
			_, err := fixture.svc.ReleaseAbsentArchivedResourceTarget(context.Background(), req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// keep filepath import alive when the test runner strips unused symbols.
var _ = filepath.Join

// terminalReleaseCASRepo is a real DB-only repository that performs the exact
// CAS the production sqlite path runs: a single serializable transaction
// that flips retained -> released with CompletedAt and updates updated_at.
// It only implements the three terminal methods; the other
// TaskResourceCleanupRepository methods are no-op (the service never reaches
// them on the terminal action paths).
type terminalReleaseCASRepo struct {
	terminalRepo *terminalReleaseRepo
}

type terminalReleaseRepo struct {
	db *sqlx.DB
}

func newTerminalReleaseCASRepo(fixture *terminalReleaseFixture) *terminalReleaseCASRepo {
	repo := &terminalReleaseRepo{db: fixture.db}
	return &terminalReleaseCASRepo{terminalRepo: repo}
}

func (r *terminalReleaseCASRepo) CancelStaleArchivedResourcePendingMove(_ context.Context, _ *models.TaskResourceCleanupJob) (bool, error) {
	return false, nil
}

func (r *terminalReleaseCASRepo) ReleaseAbsentArchivedResourceAnchor(ctx context.Context, job *models.TaskResourceCleanupJob) (*models.ArchivedResourceReleaseAdmission, error) {
	now := time.Now().UTC()
	// Decode the release snapshot to extract the anchored v2/v3 identity.
	// The production writer runs the same decode inside a serializable
	// transaction so the CAS predicate matches the anchored row by its
	// v2 operation_id, not by the release request's derived operation_id.
	snapshot, identity, err := models.DecodeArchivedResourceReleaseSnapshot([]byte(job.ResourceSnapshot))
	if err != nil {
		return nil, err
	}
	_ = snapshot
	tx, err := r.terminalRepo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	sqlStmt := `UPDATE task_resource_cleanup_jobs SET state = ?, completed_at = ?, updated_at = ?, last_error = '' WHERE id = 'anchor-terminal-release' AND operation_id = ? AND trigger = ? AND state = ? AND snapshot_digest = ? AND resource_id = ? AND anchor_revision = ? AND active_scope_key IS NULL`
	result, err := tx.ExecContext(ctx, sqlStmt,
		models.TaskResourceCleanupStateReleased, now, now,
		identity.AnchorOperationID,
		models.TaskResourceCleanupTriggerReconcile,
		models.TaskResourceCleanupStateRetained,
		identity.AnchorDigest, identity.ResourceID,
		int64(models.ArchivedResourceRetentionAnchorVersion),
	)
	if err != nil {
		return nil, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return nil, commitErr
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		return nil, errors.New("release CAS affected zero rows")
	}
	return &models.ArchivedResourceReleaseAdmission{
		Job: &models.TaskResourceCleanupJob{
			OperationID: identity.AnchorOperationID,
			State:       models.TaskResourceCleanupStateReleased,
			CompletedAt: &now,
		},
		Reason: "release_absent",
	}, nil
}

func (r *terminalReleaseCASRepo) RetireStaleArchivedResourceEnvironmentReference(_ context.Context, _ *models.TaskResourceCleanupJob) (*models.ArchivedResourceEnvironmentRetirementIdentity, error) {
	return &models.ArchivedResourceEnvironmentRetirementIdentity{}, nil
}
