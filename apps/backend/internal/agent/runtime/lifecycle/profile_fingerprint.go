package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	settingsmodels "github.com/kandev/kandev/internal/agent/settings/models"
)

const (
	profileFingerprintPrefix              = "sha256:"
	metadataKeyExpectedProfileFingerprint = "profile_fingerprint_expected"
	// MetadataKeyExpectedProfileFingerprint is the durable WorkspaceInfo /
	// AgentExecution binding projected from TaskSession.AgentProfileSnapshot.
	// Absence is the only legacy compatibility shape; an explicitly present
	// empty or malformed value is drift and must fail closed.
	MetadataKeyExpectedProfileFingerprint = metadataKeyExpectedProfileFingerprint
)

var (
	// ErrProfileDrift is deliberately stable and detail-free. Profile launch
	// bindings are safe to surface, but callers must not accidentally append
	// profile environment values while reporting a mismatch.
	ErrProfileDrift = errors.New("BLOCKED_PROFILE_DRIFT")
	// ErrProfileSecret is the public boundary for any profile secret lookup
	// failure. The underlying store/reveal error is never wrapped because it may
	// contain a secret identifier or provider detail.
	ErrProfileSecret = errors.New("BLOCKED_PROFILE_SECRET")
)

type fingerprintCLIFlag struct {
	Flag    string `json:"flag"`
	Enabled bool   `json:"enabled"`
}

type profileFingerprintSnapshot struct {
	ProfileID                  string                         `json:"profile_id"`
	AgentID                    string                         `json:"agent_id"`
	AgentName                  string                         `json:"agent_name"`
	Model                      string                         `json:"model"`
	Mode                       string                         `json:"mode"`
	FallbackModel              string                         `json:"fallback_model"`
	AutoFallback               bool                           `json:"auto_fallback"`
	ConfigOptions              map[string]string              `json:"config_options"`
	AutoApprove                bool                           `json:"auto_approve"`
	DangerouslySkipPermissions bool                           `json:"dangerously_skip_permissions"`
	AllowIndexing              bool                           `json:"allow_indexing"`
	CLIFlags                   []fingerprintCLIFlag           `json:"cli_flags"`
	CommandPrefix              string                         `json:"command_prefix"`
	EnvVars                    []settingsmodels.ProfileEnvVar `json:"env_vars"`
	CLIPassthrough             bool                           `json:"cli_passthrough"`
	NativeSessionResume        bool                           `json:"native_session_resume"`
	SupportsMCP                bool                           `json:"supports_mcp"`
}

// profileFingerprint computes a deterministic, secret-free launch binding.
// The returned value contains only a digest. Secret references and literal
// environment values influence the binding but are never returned or logged.
func profileFingerprint(info *AgentProfileInfo) (string, error) {
	if info == nil {
		return "", ErrProfileDrift
	}
	flags := make([]fingerprintCLIFlag, len(info.CLIFlags))
	for i, flag := range info.CLIFlags {
		flags[i] = fingerprintCLIFlag{Flag: flag.Flag, Enabled: flag.Enabled}
	}
	if flags == nil {
		flags = []fingerprintCLIFlag{}
	}
	envVars := append([]settingsmodels.ProfileEnvVar(nil), info.EnvVars...)
	if envVars == nil {
		envVars = []settingsmodels.ProfileEnvVar{}
	}
	configOptions := make(map[string]string, len(info.ConfigOptions))
	for key, value := range info.ConfigOptions {
		configOptions[key] = value
	}
	snapshot := profileFingerprintSnapshot{
		ProfileID: info.ProfileID, AgentID: info.AgentID, AgentName: info.AgentName,
		Model: info.Model, Mode: info.Mode, FallbackModel: info.FallbackModel,
		AutoFallback: info.AutoFallback, ConfigOptions: configOptions,
		AutoApprove: info.AutoApprove, DangerouslySkipPermissions: info.DangerouslySkipPermissions,
		AllowIndexing: info.AllowIndexing,
		CLIFlags:      flags, CommandPrefix: info.CommandPrefix, EnvVars: envVars,
		CLIPassthrough: info.CLIPassthrough, NativeSessionResume: info.NativeSessionResume,
		SupportsMCP: info.SupportsMCP,
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return "", ErrProfileDrift
	}
	digest := sha256.Sum256(canonical)
	return profileFingerprintPrefix + hex.EncodeToString(digest[:]), nil
}

func resolvedProfileFingerprint(info *AgentProfileInfo) (string, error) {
	if info == nil {
		return "", ErrProfileDrift
	}
	if info.Fingerprint == "" {
		return profileFingerprint(info)
	}
	if !validProfileFingerprint(info.Fingerprint) {
		return "", ErrProfileDrift
	}
	return info.Fingerprint, nil
}

func validProfileFingerprint(value string) bool {
	if len(value) != len(profileFingerprintPrefix)+sha256.Size*2 || !strings.HasPrefix(value, profileFingerprintPrefix) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, profileFingerprintPrefix))
	return err == nil
}

// IsValidProfileFingerprint exposes only shape validation to the orchestrator
// snapshot boundary. Fingerprint computation remains owned by lifecycle.
func IsValidProfileFingerprint(value string) bool { return validProfileFingerprint(value) }

func validateProfileFingerprint(expected string, info *AgentProfileInfo) error {
	if expected == "" { // Explicit compatibility path for pre-binding rows.
		return nil
	}
	if !validProfileFingerprint(expected) {
		return ErrProfileDrift
	}
	actual, err := resolvedProfileFingerprint(info)
	if err != nil || actual != expected {
		return ErrProfileDrift
	}
	return nil
}

func expectedProfileFingerprintFromMetadata(metadata map[string]interface{}) (string, error) {
	if metadata == nil {
		return "", nil
	}
	value, exists := metadata[metadataKeyExpectedProfileFingerprint]
	if !exists {
		return "", nil
	}
	fingerprint, ok := value.(string)
	if !ok || !validProfileFingerprint(fingerprint) {
		return "", ErrProfileDrift
	}
	return fingerprint, nil
}

// validateExecutionProfileFingerprint closes the prepare/start and
// restart/resume TOCTOU boundary. It must run before any process mutation.
func (m *Manager) validateExecutionProfileFingerprint(ctx context.Context, execution *AgentExecution) error {
	if execution == nil {
		return ErrProfileDrift
	}
	expected, err := expectedProfileFingerprintFromMetadata(execution.MetadataSnapshot())
	if err != nil || expected == "" {
		return err
	}
	if m.profileResolver == nil || execution.AgentProfileID == "" {
		return ErrProfileDrift
	}
	info, err := m.profileResolver.ResolveProfile(ctx, execution.AgentProfileID)
	if err != nil {
		return ErrProfileDrift
	}
	return validateProfileFingerprint(expected, info)
}
