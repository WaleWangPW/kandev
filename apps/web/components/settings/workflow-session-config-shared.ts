import type { AgentProfileOption } from "@/lib/state/slices";
import type { WorkflowStep } from "@/lib/types/http";
import type {
  ConfigureSessionOperation,
  ConfigureSessionRule,
  OnEnterAction,
} from "@/lib/types/workflow-actions";
import {
  isModelConfigOption,
  type DynamicConfigOption,
  type SelectConfigOption,
} from "@/components/model-config-selector";
import type { AvailableAgent } from "@/lib/types/http";

export type AgentChoice = { name: string; label: string };

export type ConfigureSessionAction = Extract<OnEnterAction, { type: "configure_session" }>;

export function configureSessionAction(step: WorkflowStep): ConfigureSessionAction | undefined {
  return step.events?.on_enter?.find(
    (candidate): candidate is ConfigureSessionAction => candidate.type === "configure_session",
  );
}

export function buildAgentChoices(
  profiles: AgentProfileOption[],
  availableAgents: AvailableAgent[],
): AgentChoice[] {
  const choices = new Map<string, string>();
  for (const profile of profiles) {
    choices.set(profile.agent_name, profile.label.split(" • ")[0] || profile.agent_name);
  }
  for (const agent of availableAgents) choices.set(agent.name, agent.display_name || agent.name);
  return [...choices.entries()].map(([name, label]) => ({ name, label }));
}

export function defaultModelForAgent(
  agentName: string,
  availableAgents: AvailableAgent[],
): string | undefined {
  const config = availableAgents.find((agent) => agent.name === agentName)?.model_config;
  return config?.current_model_id || config?.default_model || config?.available_models[0]?.id;
}

export function modelConfigOptions(
  modelConfig: AvailableAgent["model_config"] | undefined,
  rule: ConfigureSessionRule,
): SelectConfigOption[] {
  return (modelConfig?.config_options ?? []).map((option) => {
    const dynamicOption: DynamicConfigOption = {
      type: option.type,
      id: option.id,
      name: option.name,
      description: option.description,
      currentValue: isModelConfigOption(option)
        ? rule.model || option.current_value
        : rule.config_options?.[option.id] || option.current_value,
      category: option.category,
      options: option.options,
    };
    return { ...dynamicOption, options: option.options ?? [] };
  });
}

export function operationRule(
  rule: ConfigureSessionRule,
  operation: ConfigureSessionOperation,
): ConfigureSessionRule {
  if (operation === "set") return { ...rule, operation, model: rule.model || undefined };
  return { agent_name: rule.agent_name, operation };
}
