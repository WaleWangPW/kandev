package worktree

import (
	"context"
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
