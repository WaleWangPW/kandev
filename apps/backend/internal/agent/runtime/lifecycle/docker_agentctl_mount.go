package lifecycle

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// prepareDockerAgentctlMount resolves a helper for the Docker daemon's actual
// platform and materializes it below Kandev home. On macOS this avoids trying
// to bind-mount an /Applications path that a Colima VM cannot see.
func (cm *ContainerManager) prepareDockerAgentctlMount(ctx context.Context) (string, error) {
	platform := SSHRemotePlatform{GOOS: sshRemoteGOOSLinux, GOARCH: sshRemoteGOARCHAMD64}
	if cm.resolveDockerPlatform != nil {
		resolved, err := cm.resolveDockerPlatform(ctx)
		if err != nil {
			return "", fmt.Errorf("resolve docker daemon platform: %w", err)
		}
		platform = resolved
	}
	if err := requireSupportedRemotePlatform(platform); err != nil {
		return "", fmt.Errorf("unsupported docker daemon platform: %w", err)
	}

	source, err := cm.resolveAgentctlBinary(platform)
	if err != nil {
		return "", fmt.Errorf("agentctl helper for docker daemon: %w", err)
	}
	return materializeDockerAgentctlBinary(source, cm.kandevHomeDir, platform)
}

func normalizeDockerArchitecture(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "aarch64":
		return sshRemoteGOARCHARM64
	case "x86_64", "x86-64":
		return sshRemoteGOARCHAMD64
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}

func materializeDockerAgentctlBinary(source, kandevHomeDir string, platform SSHRemotePlatform) (string, error) {
	if kandevHomeDir == "" {
		return source, nil
	}
	dir := filepath.Join(kandevHomeDir, "docker-bin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create docker helper directory: %w", err)
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		if err != nil {
			return "", fmt.Errorf("inspect docker helper directory: %w", err)
		}
		return "", fmt.Errorf("docker helper directory is not a real directory")
	}

	destination := filepath.Join(dir, agentctlBinaryName(platform))
	if sameFileSHA256(source, destination) {
		return destination, nil
	}
	if info, err := os.Lstat(destination); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing to replace symlinked docker helper")
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect docker helper: %w", err)
	}

	src, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open agentctl helper: %w", err)
	}
	defer src.Close()
	tmp, err := os.CreateTemp(dir, ".agentctl-*")
	if err != nil {
		return "", fmt.Errorf("create docker helper temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return "", fmt.Errorf("copy agentctl helper: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", fmt.Errorf("chmod docker helper: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close docker helper: %w", err)
	}
	if err := os.Rename(tmpName, destination); err != nil {
		return "", fmt.Errorf("install docker helper: %w", err)
	}
	return destination, nil
}

func sameFileSHA256(source, destination string) bool {
	sourceHash, err := fileSHA256(source)
	if err != nil {
		return false
	}
	destinationHash, err := fileSHA256(destination)
	return err == nil && sourceHash == destinationHash
}

func fileSHA256(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}
