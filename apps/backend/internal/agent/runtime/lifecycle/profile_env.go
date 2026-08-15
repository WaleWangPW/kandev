package lifecycle

import (
	"context"

	settingsmodels "github.com/kandev/kandev/internal/agent/settings/models"
	"github.com/kandev/kandev/internal/gitconfigenv"
)

// metadataKeyProfileEnvResolved caches resolved profile env vars on an execution
// so configureAndStartAgent does not re-resolve secrets on the same launch.
const metadataKeyProfileEnvResolved = "profile_env_resolved"

// mergeAgentProfileEnv fills missing keys in env from the agent profile's
// env_vars. Existing keys in env (office tokens, executor profile env, etc.)
// are never overwritten.
func (m *Manager) mergeAgentProfileEnv(ctx context.Context, profileID string, env map[string]string) error {
	if profileID == "" || env == nil || m.profileResolver == nil {
		return nil
	}
	info, err := m.profileResolver.ResolveProfile(ctx, profileID)
	if err != nil || info == nil {
		return ErrProfileDrift
	}
	return m.mergeAgentProfileEnvFromInfo(ctx, info, env)
}

func (m *Manager) mergeExpectedAgentProfileEnv(
	ctx context.Context, profileID string, env map[string]string, expected string,
) error {
	if profileID == "" || env == nil || m.profileResolver == nil {
		if expected != "" {
			return ErrProfileDrift
		}
		return nil
	}
	info, err := m.profileResolver.ResolveProfile(ctx, profileID)
	if err != nil || info == nil {
		if expected == "" {
			return nil
		}
		return ErrProfileDrift
	}
	if err := validateProfileFingerprint(expected, info); err != nil {
		return err
	}
	return m.mergeAgentProfileEnvFromInfo(ctx, info, env)
}

func (m *Manager) mergeAgentProfileEnvFromInfo(ctx context.Context, info *AgentProfileInfo, env map[string]string) error {
	if info == nil || env == nil || len(info.EnvVars) == 0 {
		return nil
	}
	resolved, err := m.resolveAgentProfileEnvVars(ctx, info.EnvVars)
	if err != nil {
		return err
	}
	mergeEnvFillMissing(env, resolved)
	return nil
}

func (m *Manager) cacheResolvedProfileEnv(execution *AgentExecution, resolved map[string]string) {
	if execution == nil || len(resolved) == 0 {
		return
	}
	execution.setMetadataValue(metadataKeyProfileEnvResolved, cloneStringMap(resolved))
}

func (m *Manager) mergeAgentProfileEnvForExecution(ctx context.Context, execution *AgentExecution, env map[string]string) error {
	if execution == nil {
		return nil
	}
	value, _ := execution.metadataValue(metadataKeyProfileEnvResolved)
	if cached, ok := value.(map[string]string); ok && len(cached) > 0 {
		mergeEnvFillMissing(env, cached)
		return nil
	}
	expected, err := expectedProfileFingerprintFromMetadata(execution.MetadataSnapshot())
	if err != nil {
		return err
	}
	return m.mergeExpectedAgentProfileEnv(ctx, execution.AgentProfileID, env, expected)
}

// resolvedProfileEnvFromFinal extracts the already-resolved profile entries
// from the final atomic runtime snapshot. This avoids a second secret reveal
// after a runtime instance has been created.
func resolvedProfileEnvFromFinal(envVars []settingsmodels.ProfileEnvVar, final map[string]string) map[string]string {
	if len(envVars) == 0 || len(final) == 0 {
		return nil
	}
	resolved := make(map[string]string, len(envVars))
	for _, envVar := range envVars {
		if envVar.Key == "" {
			continue
		}
		if value, ok := final[envVar.Key]; ok && value != "" {
			resolved[envVar.Key] = value
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func mergeEnvFillMissing(dst, src map[string]string) {
	if len(src) == 0 || dst == nil {
		return
	}
	for k, v := range src {
		if v == "" || gitconfigenv.IsIndexedKey(k) {
			continue
		}
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}
	merged, err := gitconfigenv.Merge(src, dst)
	if err == nil {
		gitconfigenv.CopyIndexed(dst, merged)
	}
}

// resolveAgentProfileEnvVars resolves a profile environment atomically.
// SecretID wins over Value. Any secret failure discards the entire result and
// returns a stable sanitized error; callers must not launch with a partial env.
func (m *Manager) resolveAgentProfileEnvVars(ctx context.Context, envVars []settingsmodels.ProfileEnvVar) (map[string]string, error) {
	if len(envVars) == 0 {
		return nil, nil
	}
	resolved := make(map[string]string, len(envVars))
	for _, ev := range envVars {
		key := ev.Key
		if key == "" {
			continue
		}
		if ev.SecretID != "" {
			if m.secretStore == nil {
				return nil, ErrProfileSecret
			}
			value, err := m.revealGlobalSecret(ctx, ev.SecretID)
			if err != nil {
				return nil, ErrProfileSecret
			}
			resolved[key] = value
			continue
		}
		if ev.Value != "" {
			resolved[key] = ev.Value
		}
	}
	return resolved, nil
}

func (m *Manager) revealGlobalSecret(ctx context.Context, secretID string) (string, error) {
	return revealGlobalSecret(ctx, m.secretStore, secretID)
}
