"use client";

import { IconTrash } from "@tabler/icons-react";
import { Button } from "@kandev/ui/button";
import { Label } from "@kandev/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@kandev/ui/select";
import {
  configOptionToModelOptions,
  isModelConfigOption,
  ModelConfigSelector,
  type SelectConfigOption,
} from "@/components/model-config-selector";
import type { AvailableAgent } from "@/lib/types/http";
import type { ConfigureSessionOperation, ConfigureSessionRule } from "@/lib/types/workflow-actions";
import {
  type AgentChoice,
  defaultModelForAgent,
  modelConfigOptions,
  operationRule,
} from "./workflow-session-config-shared";

export function SessionConfigRuleCard({
  rule,
  index,
  choices,
  availableAgents,
  readOnly,
  onChange,
  onRemove,
}: {
  rule: ConfigureSessionRule;
  index: number;
  choices: AgentChoice[];
  availableAgents: AvailableAgent[];
  readOnly: boolean;
  onChange: (rule: ConfigureSessionRule) => void;
  onRemove: () => void;
}) {
  const selectedAgent = availableAgents.find((agent) => agent.name === rule.agent_name);
  const configOptions = modelConfigOptions(selectedAgent?.model_config, rule);
  const modelConfigOption = configOptions.find(isModelConfigOption);
  const modelOptions = modelConfigOption
    ? configOptionToModelOptions(modelConfigOption)
    : (selectedAgent?.model_config?.available_models ?? []).map((model) => ({
        id: model.id,
        name: model.name,
        description: model.description ?? (model.id !== model.name ? model.id : undefined),
        usageMultiplier:
          typeof model.meta?.copilotUsage === "string" ? model.meta.copilotUsage : undefined,
      }));
  const currentModel = currentModelForRule(rule, modelConfigOption, selectedAgent);

  return (
    <div
      className="space-y-3 rounded-md border bg-card p-3"
      data-testid={`session-config-rule-${index}`}
    >
      <SessionConfigRuleHeader
        rule={rule}
        index={index}
        choices={choices}
        availableAgents={availableAgents}
        readOnly={readOnly}
        onChange={onChange}
        onRemove={onRemove}
      />

      {rule.operation === "set" && (
        <SessionConfigRuleSettings
          rule={rule}
          modelOptions={modelOptions}
          currentModel={currentModel}
          configOptions={configOptions}
          readOnly={readOnly}
          onChange={onChange}
        />
      )}
    </div>
  );
}

function SessionConfigRuleHeader({
  rule,
  index,
  choices,
  availableAgents,
  readOnly,
  onChange,
  onRemove,
}: {
  rule: ConfigureSessionRule;
  index: number;
  choices: AgentChoice[];
  availableAgents: AvailableAgent[];
  readOnly: boolean;
  onChange: (rule: ConfigureSessionRule) => void;
  onRemove: () => void;
}) {
  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
      <Label className="text-xs font-medium text-muted-foreground sm:w-24">When agent is</Label>
      <Select
        value={rule.agent_name}
        onValueChange={(agentName) =>
          onChange({
            agent_name: agentName,
            operation: rule.operation,
            ...(rule.operation === "set"
              ? { model: defaultModelForAgent(agentName, availableAgents) }
              : {}),
          })
        }
        disabled={readOnly}
      >
        <SelectTrigger
          className="min-h-10 w-full cursor-pointer sm:flex-1"
          data-testid={`session-config-agent-${index}`}
        >
          <SelectValue placeholder="Choose agent family" />
        </SelectTrigger>
        <SelectContent>
          {choices.map((choice) => (
            <SelectItem key={choice.name} value={choice.name}>
              {choice.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Select
        value={rule.operation}
        onValueChange={(operation) =>
          onChange(operationRule(rule, operation as ConfigureSessionOperation, availableAgents))
        }
        disabled={readOnly}
      >
        <SelectTrigger
          className="min-h-10 w-full cursor-pointer sm:w-44"
          data-testid={`session-config-operation-${index}`}
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="set">Set settings</SelectItem>
          <SelectItem value="keep">Keep current</SelectItem>
          <SelectItem value="restore_original">Restore original</SelectItem>
        </SelectContent>
      </Select>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="min-h-10 cursor-pointer self-end text-destructive hover:text-destructive sm:self-auto"
        onClick={onRemove}
        disabled={readOnly}
        aria-label={`Remove agent condition ${index + 1}`}
      >
        <IconTrash className="h-4 w-4" />
      </Button>
    </div>
  );
}

function currentModelForRule(
  rule: ConfigureSessionRule,
  modelConfigOption: SelectConfigOption | undefined,
  selectedAgent: AvailableAgent | undefined,
): string | null {
  return (
    (rule.operation === "set" ? rule.model : undefined) ||
    modelConfigOption?.currentValue ||
    selectedAgent?.model_config?.current_model_id ||
    selectedAgent?.model_config?.default_model ||
    null
  );
}

function SessionConfigRuleSettings({
  rule,
  modelOptions,
  currentModel,
  configOptions,
  readOnly,
  onChange,
}: {
  rule: Extract<ConfigureSessionRule, { operation: "set" }>;
  modelOptions: { id: string; name: string; description?: string; usageMultiplier?: string }[];
  currentModel: string | null;
  configOptions: SelectConfigOption[];
  readOnly: boolean;
  onChange: (rule: ConfigureSessionRule) => void;
}) {
  return (
    <div className="space-y-2 border-t border-border/60 pt-3">
      {modelOptions.length > 0 || configOptions.length > 0 ? (
        <ModelConfigSelector
          modelOptions={modelOptions}
          currentModel={currentModel}
          configOptions={configOptions}
          onModelChange={(model) => onChange({ ...rule, model })}
          onConfigChange={(configId, value) =>
            onChange({
              ...rule,
              config_options: { ...(rule.config_options ?? {}), [configId]: value },
            })
          }
          disabled={readOnly}
          placeholder="Choose model and session settings"
          ariaLabel={`Settings for ${rule.agent_name}`}
          triggerClassName="min-h-11 w-full sm:min-h-10"
        />
      ) : (
        <p className="rounded-md border border-border/60 p-2 text-xs text-muted-foreground">
          Model options are unavailable for this agent. Refresh agent capabilities before saving
          this condition.
        </p>
      )}
    </div>
  );
}
