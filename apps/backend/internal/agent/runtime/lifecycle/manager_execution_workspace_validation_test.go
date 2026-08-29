package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

func TestValidateWorkspaceInfoForExecutionRejectsRepoBackedNonGitPath(t *testing.T) {
	path := t.TempDir()
	err := validateWorkspaceInfoForExecution(context.Background(), &WorkspaceInfo{
		ExecutorType:  string(models.ExecutorTypeLocal),
		WorkspacePath: path,
		WorkspaceRepositories: []WorkspaceRepositorySpec{{
			RepositoryID: "repository-1", RepositoryPath: path, RepoName: "repository",
		}},
	})
	if err == nil {
		t.Fatal("validateWorkspaceInfoForExecution() error = nil, want rejection")
	}
}

func TestValidateWorkspaceInfoForExecutionAcceptsCanonicalLocalRepository(t *testing.T) {
	repository := initGitRepo(t)
	err := validateWorkspaceInfoForExecution(context.Background(), &WorkspaceInfo{
		ExecutorType:  string(models.ExecutorTypeLocal),
		WorkspacePath: repository,
		WorkspaceRepositories: []WorkspaceRepositorySpec{{
			RepositoryID: "repository-1", RepositoryPath: repository, RepoName: "repository",
		}},
	})
	if err != nil {
		t.Fatalf("validateWorkspaceInfoForExecution() error = %v", err)
	}
}

func TestValidateWorkspaceInfoForExecutionAcceptsMultiRepoTaskRoot(t *testing.T) {
	root := t.TempDir()
	first := initGitRepo(t)
	second := initGitRepo(t)
	if err := linkDirectory(first, filepath.Join(root, "first")); err != nil {
		t.Fatal(err)
	}
	if err := linkDirectory(second, filepath.Join(root, "second")); err != nil {
		t.Fatal(err)
	}
	err := validateWorkspaceInfoForExecution(context.Background(), &WorkspaceInfo{
		ExecutorType:  string(models.ExecutorTypeLocal),
		WorkspacePath: root,
		WorkspaceRepositories: []WorkspaceRepositorySpec{
			{RepositoryID: "repository-1", RepositoryPath: first, RepoName: "first"},
			{RepositoryID: "repository-2", RepositoryPath: second, RepoName: "second"},
		},
	})
	if err != nil {
		t.Fatalf("validateWorkspaceInfoForExecution() error = %v", err)
	}
}

func TestValidateWorkspaceInfoForExecutionSkipsRepoLessWorkspace(t *testing.T) {
	err := validateWorkspaceInfoForExecution(context.Background(), &WorkspaceInfo{
		ExecutorType:  string(models.ExecutorTypeLocal),
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("validateWorkspaceInfoForExecution() error = %v", err)
	}
}

func linkDirectory(source, destination string) error {
	return os.Symlink(source, destination)
}
