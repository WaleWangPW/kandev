package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	settingsmodels "github.com/kandev/kandev/internal/agent/settings/models"
)

type fixedFingerprintResolver struct {
	info *AgentProfileInfo
	err  error
}

func (r *fixedFingerprintResolver) ResolveProfile(context.Context, string) (*AgentProfileInfo, error) {
	return r.info, r.err
}

func TestProfileFingerprintDeterministicAndOpaque(t *testing.T) {
	info := &AgentProfileInfo{
		ProfileID: "profile-1", AgentID: "agent-1", AgentName: "codex-acp",
		Model: "gpt-5.6-sol", Mode: "agent", ConfigOptions: map[string]string{"z": "last", "a": "first"},
		CLIFlags: []settingsmodels.CLIFlag{{Description: "display only", Flag: "--safe", Enabled: true}},
		EnvVars: []settingsmodels.ProfileEnvVar{
			{Key: "PLAIN", Value: "literal-sensitive-value"},
			{Key: "SECRET", SecretID: "opaque-secret-reference"},
		},
	}
	first, err := profileFingerprint(info)
	if err != nil {
		t.Fatalf("profileFingerprint: %v", err)
	}
	second, err := profileFingerprint(info)
	if err != nil {
		t.Fatalf("profileFingerprint repeat: %v", err)
	}
	if first != second || !IsValidProfileFingerprint(first) {
		t.Fatalf("fingerprints = %q, %q", first, second)
	}
	for _, secret := range []string{"literal-sensitive-value", "opaque-secret-reference"} {
		if strings.Contains(first, secret) {
			t.Fatalf("fingerprint exposes profile value %q", secret)
		}
	}

	// Map insertion order and presentation-only flag descriptions are not
	// launch semantics and therefore do not cause false drift.
	clone := *info
	clone.ConfigOptions = map[string]string{"a": "first", "z": "last"}
	clone.CLIFlags = []settingsmodels.CLIFlag{{Description: "renamed label", Flag: "--safe", Enabled: true}}
	third, err := profileFingerprint(&clone)
	if err != nil || third != first {
		t.Fatalf("semantically equal profile fingerprint = %q, want %q (err=%v)", third, first, err)
	}

	clone.Model = "gpt-5.6-terra"
	changed, err := profileFingerprint(&clone)
	if err != nil || changed == first {
		t.Fatalf("launch-relevant change did not alter fingerprint: %q err=%v", changed, err)
	}
}

func TestValidateExecutionProfileFingerprint(t *testing.T) {
	profile := &AgentProfileInfo{ProfileID: "profile-1", AgentID: "agent-1", Model: "model-a"}
	expected, err := profileFingerprint(profile)
	if err != nil {
		t.Fatalf("profileFingerprint: %v", err)
	}
	profile.Fingerprint = expected
	execution := &AgentExecution{AgentProfileID: "profile-1", metadata: map[string]interface{}{
		metadataKeyExpectedProfileFingerprint: expected,
	}}
	mgr := &Manager{profileResolver: &fixedFingerprintResolver{info: profile}}
	if err := mgr.validateExecutionProfileFingerprint(context.Background(), execution); err != nil {
		t.Fatalf("matching fingerprint rejected: %v", err)
	}

	profile.Fingerprint = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := mgr.validateExecutionProfileFingerprint(context.Background(), execution); !errors.Is(err, ErrProfileDrift) {
		t.Fatalf("drift error = %v, want %v", err, ErrProfileDrift)
	}

	// Rows created before profile binding carry no expected fingerprint and
	// retain their compatibility path even when profile resolution is absent.
	legacy := &AgentExecution{metadata: map[string]interface{}{}}
	if err := (&Manager{}).validateExecutionProfileFingerprint(context.Background(), legacy); err != nil {
		t.Fatalf("legacy empty fingerprint rejected: %v", err)
	}
}

func TestExpectedProfileFingerprintRejectsMalformedMetadata(t *testing.T) {
	for _, value := range []interface{}{nil, "", 42, "sha256:not-hex", strings.Repeat("a", 64)} {
		if _, err := expectedProfileFingerprintFromMetadata(map[string]interface{}{
			metadataKeyExpectedProfileFingerprint: value,
		}); !errors.Is(err, ErrProfileDrift) {
			t.Fatalf("value %#v error = %v, want %v", value, err, ErrProfileDrift)
		}
	}
}

func TestProfileFingerprintPersistsWithoutResolvedEnvironment(t *testing.T) {
	if !ShouldPersistMetadataKey(metadataKeyExpectedProfileFingerprint) {
		t.Fatal("expected profile fingerprint is not restart-persistent")
	}
	if ShouldPersistMetadataKey(metadataKeyProfileEnvResolved) {
		t.Fatal("resolved profile environment must remain memory-only")
	}
}

func TestResolveAgentProfileSanitizesBoundResolverFailure(t *testing.T) {
	mgr := &Manager{profileResolver: &fixedFingerprintResolver{err: errors.New("sensitive storage detail")}}
	_, _, err := mgr.resolveAgentProfile(context.Background(), &LaunchRequest{
		AgentProfileID:             "profile-1",
		ExpectedProfileFingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	if !errors.Is(err, ErrProfileDrift) || err.Error() != "BLOCKED_PROFILE_DRIFT" {
		t.Fatalf("resolveAgentProfile error = %v, want sanitized %v", err, ErrProfileDrift)
	}
}
