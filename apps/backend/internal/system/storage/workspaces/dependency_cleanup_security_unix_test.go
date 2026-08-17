//go:build !windows

package workspaces

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveDependencyDirectoryDoesNotFollowWorkspaceReplacement(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	external := filepath.Join(parent, "external")
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(external, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "owned.txt"), []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "node_modules", "external.txt"), []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}

	originalRoot := root + ".original"
	err := removeDependencyDirectoryWithHook(
		context.Background(), root, filepath.Join(root, "node_modules"),
		func() {
			if err := os.Rename(root, originalRoot); err != nil {
				t.Fatalf("rename workspace: %v", err)
			}
			if err := os.Symlink(external, root); err != nil {
				t.Fatalf("replace workspace with symlink: %v", err)
			}
		},
	)
	if err != nil {
		t.Fatalf("removeDependencyDirectory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(external, "node_modules", "external.txt")); err != nil {
		t.Fatalf("external payload changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(originalRoot, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("original dependency directory still exists: %v", err)
	}
}

func TestRemoveDependencyDirectoryRejectsSymlinkedWorkspaceRoot(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "workspace-real")
	root := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(filepath.Join(realRoot, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, root); err != nil {
		t.Fatal(err)
	}
	err := removeDependencyDirectory(context.Background(), root, filepath.Join(root, "node_modules"))
	if err == nil {
		t.Fatal("removeDependencyDirectory accepted a symlinked workspace root")
	}
	if _, err := os.Stat(filepath.Join(realRoot, "node_modules")); err != nil {
		t.Fatalf("symlink target changed: %v", err)
	}
}
