package worktree

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/kandev/kandev/internal/physicaldelete"
)

func (m *Manager) requireAdmission() error {
	if m.admission == nil {
		return fmt.Errorf("worktree manager: physical-delete admission is not configured")
	}
	return nil
}

// gateManagedRootDeletion submits one sealed admission request for a managed-
// root physical action and returns the typed denial so the caller can stop
// before any Git, filesystem, or runtime mutation. Every managed-root consumer
// (remove, recreate, quarantine prune, office GC, handoff cleanup, factory
// reset, storage permanent delete) must funnel through this helper. Missing
// admission or the deliberately sealed executor returns
// DenialExecutorUnavailable, which is the correct fail-closed signal in this
// release — physical execution remains unavailable and the consumer must not
// proceed with the underlying destructive operation.
func (m *Manager) gateManagedRootDeletion(
	ctx context.Context,
	authority physicaldelete.Authority,
	executor physicaldelete.Executor,
	action physicaldelete.Action,
	resourceKind physicaldelete.ResourceKind,
	resourceID, worktreePath, commonDir string,
) (physicaldelete.Receipt, error) {
	if err := m.requireAdmission(); err != nil {
		return physicaldelete.Receipt{}, err
	}
	if worktreePath == "" {
		return physicaldelete.Receipt{}, fmt.Errorf(
			"worktree manager: %s requires a managed-root path", action)
	}
	absPath, err := filepath.Abs(filepath.Clean(worktreePath))
	if err != nil {
		return physicaldelete.Receipt{}, fmt.Errorf(
			"worktree manager: canonicalize %s path %s: %w", action, worktreePath, err)
	}
	// Capture the immutable Lstat identity observed immediately before the
	// sealed gate runs. The admission verifier rejects any request whose
	// anchor info does not match the post-lock filesystem state, so this
	// snapshot must be taken here and not after any mutation.
	anchor, anchorErr := physicaldelete.CaptureAnchor(absPath)
	resource := physicaldelete.Resource{
		Kind:      resourceKind,
		ID:        resourceID,
		Path:      absPath,
		RootPath:  absPath,
		CommonDir: commonDir,
	}
	if anchorErr == nil {
		resource.Anchor = &anchor
	}
	request := physicaldelete.Request{
		Action:    action,
		Authority: authority,
		Executor:  executor,
		Resource:  resource,
	}
	receipt, err := m.admission.Execute(ctx, request)
	if err == nil {
		return receipt, nil
	}
	// Missing admission, sealed executor, or any other typed denial must
	// propagate so the caller short-circuits before mutating the filesystem,
	// Git registration, branch ref, database row, or runtime. The executor is
	// deliberately unavailable in this release; DenialExecutorUnavailable is
	// the canonical fail-closed signal.
	switch {
	case errors.Is(err, physicaldelete.ErrExecutorUnavailable):
		return receipt, physicaldelete.ErrExecutorUnavailable
	case errors.Is(err, physicaldelete.ErrInvalidRequest),
		errors.Is(err, physicaldelete.ErrInventoryIncomplete),
		errors.Is(err, physicaldelete.ErrLockUnavailable),
		errors.Is(err, physicaldelete.ErrProtectedResource),
		errors.Is(err, physicaldelete.ErrAnchorMismatch):
		return receipt, err
	default:
		return receipt, err
	}
}

func (m *Manager) gitCommonDir(ctx context.Context, repoPath string) (string, error) {
	cmd := m.newNonInteractiveGitCmd(ctx, repoPath, "rev-parse", "--git-common-dir")
	output, err := cmd.Output()
	commonDir := strings.TrimSpace(string(output))
	if err != nil || commonDir == "" {
		return "", fmt.Errorf("canonical git common dir for %s: %w", repoPath, err)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repoPath, commonDir)
	}
	abs, err := filepath.Abs(filepath.Clean(commonDir))
	if err != nil {
		return "", fmt.Errorf("canonicalize git common dir %s: %w", commonDir, err)
	}
	return abs, nil
}

func (m *Manager) beginProvisionalLease(
	ctx context.Context,
	req CreateRequest,
	worktreePath string,
) (*physicaldelete.ProvisionalLease, error) {
	if err := m.requireAdmission(); err != nil {
		return nil, err
	}
	commonDir, err := m.gitCommonDir(ctx, req.RepositoryPath)
	if err != nil {
		return nil, err
	}
	absPath, err := filepath.Abs(filepath.Clean(worktreePath))
	if err != nil {
		return nil, fmt.Errorf("canonicalize provisional path %s: %w", worktreePath, err)
	}
	lease, err := m.admission.BeginProvisional(ctx, physicaldelete.CreateRequest{
		Authority: physicaldelete.AuthorityWorktree,
		Identity: physicaldelete.ProvisionalIdentity{
			TaskOwnerID: req.TaskID, SessionOwnerID: req.SessionID,
			CreationOperationID: uuid.NewString(), ManagedRootID: req.TaskDirName,
			CanonicalPath: absPath, CommonDir: commonDir,
		},
		Resource: physicaldelete.Resource{
			Kind: physicaldelete.ResourceKindProvisional, ID: absPath,
			Path: absPath, RootPath: absPath, CommonDir: commonDir,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("begin provisional lease for %s: %w", absPath, err)
	}
	return &lease, nil
}
