package runtimeflags

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/profiles"
)

func TestDefinitionsIncludeOfficeExperimentalMetadata(t *testing.T) {
	def, ok := DefinitionByKey("features.office")
	if !ok {
		t.Fatal("features.office definition missing")
	}
	if def.EnvVar != "KANDEV_FEATURES_OFFICE" {
		t.Fatalf("EnvVar = %q, want KANDEV_FEATURES_OFFICE", def.EnvVar)
	}
	if def.Stability != StabilityExperimental {
		t.Fatalf("Stability = %q, want %q", def.Stability, StabilityExperimental)
	}
	if def.RiskDescription == "" {
		t.Fatal("RiskDescription empty")
	}
	if !def.RestartRequired {
		t.Fatal("RestartRequired = false, want true")
	}
}

// TestDefinitionsExcludePlugins pins the graduation of the plugin system out
// of the feature-flag tier: plugins ship in the base product, so no toggle may
// reappear in Settings > System > Feature Toggles.
func TestDefinitionsExcludePlugins(t *testing.T) {
	if _, ok := DefinitionByKey("features.plugins"); ok {
		t.Fatal("features.plugins definition present; plugins are a base feature and must not be toggleable")
	}
	for _, def := range Definitions() {
		if def.EnvVar == "KANDEV_FEATURES_PLUGINS" {
			t.Fatalf("definition %q still binds KANDEV_FEATURES_PLUGINS", def.Key)
		}
	}
}

func TestDefinitionsIncludeAppStatusBarMetadata(t *testing.T) {
	def, ok := DefinitionByKey("features.appStatusBar")
	if !ok {
		t.Fatal("features.appStatusBar definition missing")
	}
	if def.EnvVar != "KANDEV_FEATURES_APP_STATUS_BAR" {
		t.Fatalf("EnvVar = %q, want KANDEV_FEATURES_APP_STATUS_BAR", def.EnvVar)
	}
	if !def.RestartRequired {
		t.Fatal("RestartRequired = false, want true")
	}
}

func TestDefinitionsIncludeClaudeBackgroundPromptHandoffMetadata(t *testing.T) {
	def, ok := DefinitionByKey("features.claudeBackgroundPromptHandoff")
	if !ok {
		t.Fatal("features.claudeBackgroundPromptHandoff definition missing")
	}
	if def.EnvVar != "KANDEV_FEATURES_CLAUDE_BACKGROUND_PROMPT_HANDOFF" {
		t.Fatalf(
			"EnvVar = %q, want KANDEV_FEATURES_CLAUDE_BACKGROUND_PROMPT_HANDOFF",
			def.EnvVar,
		)
	}
	if def.Stability != StabilityExperimental {
		t.Fatalf("Stability = %q, want %q", def.Stability, StabilityExperimental)
	}
	if def.RiskLevel != RiskHigh {
		t.Fatalf("RiskLevel = %q, want %q", def.RiskLevel, RiskHigh)
	}
	if def.RiskDescription == "" {
		t.Fatal("RiskDescription empty")
	}
	if !def.RestartRequired {
		t.Fatal("RestartRequired = false, want true")
	}
	if !def.Mutable {
		t.Fatal("Mutable = false, want true")
	}
}
func TestDefinitionsExposeSingleUserFacingDebugToggle(t *testing.T) {
	def, ok := DefinitionByKey("debug.devMode")
	if !ok {
		t.Fatal("debug.devMode definition missing")
	}
	if def.EnvVar != "KANDEV_DEBUG_DEV_MODE" {
		t.Fatalf("EnvVar = %q, want KANDEV_DEBUG_DEV_MODE", def.EnvVar)
	}
	if len(def.ImpliedEnvVars) == 0 {
		t.Fatal("Debug mode should imply subordinate debug env vars")
	}
	if _, ok := DefinitionByKey("debug.agentMessages"); ok {
		t.Fatal("debug.agentMessages must not be a top-level user-facing toggle")
	}
}

// TestFeatureBindingsCoverConfigFields keeps the typed config, profile
// defaults, and runtime-flag registry in lockstep. This test is intentionally
// reflective so adding a new boolean feature cannot silently skip one of the
// binding layers.
func TestFeatureBindingsCoverConfigFields(t *testing.T) {
	defaults, err := profiles.FeatureFlagDefaults()
	if err != nil {
		t.Fatalf("FeatureFlagDefaults: %v", err)
	}

	typeOfFeatures := reflect.TypeOf(config.FeaturesConfig{})
	seenKeys := make(map[string]struct{}, len(registrations))
	for _, registration := range registrations {
		key := registration.definition.Key
		if _, exists := seenKeys[key]; exists {
			t.Fatalf("duplicate runtime flag registration for %q", key)
		}
		seenKeys[key] = struct{}{}
		if registration.read == nil || registration.apply == nil {
			t.Fatalf("runtime flag registration %q is missing a config binding", key)
		}
	}
	for i := 0; i < typeOfFeatures.NumField(); i++ {
		field := typeOfFeatures.Field(i)
		if field.Type.Kind() != reflect.Bool {
			t.Fatalf("FeaturesConfig.%s has type %s; feature flags must be bool", field.Name, field.Type)
		}

		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "" || jsonName == "-" {
			t.Fatalf("FeaturesConfig.%s is missing a JSON name", field.Name)
		}
		mapstructureName := strings.Split(field.Tag.Get("mapstructure"), ",")[0]
		if mapstructureName == "" || mapstructureName == "-" {
			t.Fatalf("FeaturesConfig.%s is missing a mapstructure name", field.Name)
		}

		key := "features." + jsonName
		definition, ok := DefinitionByKey(key)
		if !ok {
			t.Fatalf("FeaturesConfig.%s has no runtime flag registration for %q", field.Name, key)
		}
		expectedEnvVar := "KANDEV_FEATURES_" + strings.ToUpper(mapstructureName)
		if definition.EnvVar != expectedEnvVar {
			t.Fatalf("FeaturesConfig.%s EnvVar = %q, want %q", field.Name, definition.EnvVar, expectedEnvVar)
		}
		if _, ok := defaults[mapstructureName]; !ok {
			t.Fatalf("FeaturesConfig.%s has no profile default for %q", field.Name, mapstructureName)
		}

		cfg := &config.Config{}
		features := reflect.ValueOf(&cfg.Features).Elem()
		ApplyStatesToConfig(cfg, []RuntimeFlagState{{Key: key, EffectiveValue: true}})
		if got := features.Field(i).Bool(); !got {
			t.Fatalf("ApplyStatesToConfig(%q) did not enable target field %s", key, field.Name)
		}
		for j := 0; j < features.NumField(); j++ {
			if j != i && features.Field(j).Bool() {
				t.Fatalf("ApplyStatesToConfig(%q) changed unrelated field %s", key, typeOfFeatures.Field(j).Name)
			}
		}

		values := ValuesFromConfig(cfg)
		if got, ok := values[key]; !ok || !got {
			t.Fatalf("ValuesFromConfig(%q) = %v, want true after enabling %s", key, got, field.Name)
		}

		ApplyStatesToConfig(cfg, []RuntimeFlagState{{Key: key, EffectiveValue: false}})
		if got := features.Field(i).Bool(); got {
			t.Fatalf("ApplyStatesToConfig(%q) left target field %s enabled", key, field.Name)
		}
		if got := ValuesFromConfig(cfg)[key]; got {
			t.Fatalf("ValuesFromConfig(%q) = true after disabling %s", key, field.Name)
		}
	}
}
