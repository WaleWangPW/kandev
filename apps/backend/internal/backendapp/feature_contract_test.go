package backendapp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFeatureContract_ArchivedResourceFlagsDefaultFalse verifies that the two
// archived-resource release toggles are present in the frontend defaults and
// disabled in every shipped profile. It is intentionally dependency-free so it
// runs without a database or external services.
func TestFeatureContract_ArchivedResourceFlagsDefaultFalse(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..", "..")
	typesPath := filepath.Join(repoRoot, "apps", "web", "lib", "state", "slices", "features", "types.ts")
	profilesPath := filepath.Join(repoRoot, "profiles.yaml")

	typesSource, err := os.ReadFile(typesPath)
	if err != nil {
		t.Fatalf("read frontend feature types: %v", err)
	}

	objectRe := regexp.MustCompile(`export const defaultFeatureFlags = \{([\s\S]*?)\};`)
	match := objectRe.FindSubmatch(typesSource)
	if match == nil {
		t.Fatal("defaultFeatureFlags object not found in frontend types")
	}
	objectBody := string(match[1])

	for _, key := range []string{"archivedResourceReconcile", "archivedResourcePhysicalRelease"} {
		entryRe := regexp.MustCompile(`\b` + key + `\s*:\s*(true|false)`)
		entryMatch := entryRe.FindStringSubmatch(objectBody)
		if entryMatch == nil {
			t.Fatalf("frontend default %q is missing", key)
		}
		if entryMatch[1] != "false" {
			t.Fatalf("frontend default %q = %s, want false", key, entryMatch[1])
		}
	}

	profilesSource, err := os.ReadFile(profilesPath)
	if err != nil {
		t.Fatalf("read profiles.yaml: %v", err)
	}

	for _, envVar := range []string{
		"KANDEV_FEATURES_ARCHIVED_RESOURCE_RECONCILE",
		"KANDEV_FEATURES_ARCHIVED_RESOURCE_PHYSICAL_RELEASE",
	} {
		if !profileFlagAlwaysFalse(string(profilesSource), envVar) {
			t.Fatalf("profile flag %q is not false in all shipped profiles", envVar)
		}
	}
}

// profileFlagAlwaysFalse reports whether every profile leaf for envVar is "false".
// It tolerates arbitrary indentation but requires prod/dev/e2e to all be present.
func profileFlagAlwaysFalse(source, envVar string) bool {
	lines := strings.Split(source, "\n")
	var inBlock bool
	profilesSeen := make(map[string]bool)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, envVar+":") {
			inBlock = true
			continue
		}
		if inBlock {
			if !strings.Contains(line, ":") || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.HasPrefix(trimmed, "KANDEV_") || strings.HasPrefix(trimmed, "#") {
				break
			}
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 {
				continue
			}
			profile := strings.TrimSpace(parts[0])
			value := strings.Trim(strings.TrimSpace(parts[1]), `"`)
			switch profile {
			case "prod", "dev", "e2e":
				if value != "false" {
					return false
				}
				profilesSeen[profile] = true
			}
		}
	}
	return len(profilesSeen) == 3
}
