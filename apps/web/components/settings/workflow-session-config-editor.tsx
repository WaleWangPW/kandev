"use client";

import { useMemo } from "react";
import { IconPlus } from "@tabler/icons-react";
import { Button } from "@kandev/ui/button";
import { Checkbox } from "@kandev/ui/checkbox";
import { Label } from "@kandev/ui/label";
import { useAvailableAgents } from "@/hooks/domains/settings/use-available-agents";
import { useHealthyAgentProfiles } from "@/hooks/domains/settings/use-healthy-agent-profiles";
import type { WorkflowStep } from "@/lib/types/http";
import type {
  ConfigureSessionOperation,
  ConfigureSessionRule,
  OnEnterAction,
} from "@/lib/types/workflow-actions";
import {
  analyzeSessionConfigCarryForward,
  type SessionConfigCarryWarning,
} from "@/lib/workflows/session-config-carry-analysis";
import { HelpTip } from "./workflow-pipeline-editor-helpers";
import { isWorkflowStepValueDirty } from "./workflow-dirty-state";
import { SessionConfigCarryWarningPanel } from "./workflow-session-config-carry-warning";
import { SessionConfigRuleCard } from "./workflow-session-config-rule-card";
import {
  buildAgentChoices,
  configureSessionAction,
  defaultModelForAgent,
  type AgentChoice,
} from "./workflow-session-config-shared";

type SessionConfigEditorProps = {
  step: WorkflowStep;
  savedStep?: WorkflowStep;
  steps: WorkflowStep[];
  onUpdate: (updates: Partial<WorkflowStep>) => void;
  readOnly: boolean;
};

export function SessionConfigEditor({
  step,
  savedStep,
  steps,
  onUpdate,
  readOnly,
}: SessionConfigEditorProps) {
  const profiles = useHealthyAgentProfiles(step.agent_profile_id);
  const availableAgents = useAvailableAgents();
  const action = configureSessionAction(step);
  const rules = action?.config?.rules ?? [];
  const warnings = analyzeSessionConfigCarryForward(steps, step.id);
  const choices = useMemo<AgentChoice[]>(
    () => buildAgentChoices(profiles, availableAgents.items),
    [availableAgents.items, profiles],
  );
  const isDirty = isWorkflowStepValueDirty(step, savedStep, (item) =>
    JSON.stringify(configureSessionAction(item)?.config?.rules ?? []),
  );

  const updateRules = (nextRules: ConfigureSessionRule[]) => {
    const events = step.events ?? {};
    const onEnter = events.on_enter ?? [];
    const nextAction: OnEnterAction = {
      type: "configure_session",
      config: { rules: nextRules },
    };
    const nextOnEnter = action
      ? onEnter.map((candidate) =>
          candidate.type === "configure_session" ? nextAction : candidate,
        )
      : [...onEnter, nextAction];
    onUpdate({ events: { ...events, on_enter: nextOnEnter } });
  };

  const removeConfiguration = () => {
    const events = step.events ?? {};
    onUpdate({
      events: {
        ...events,
        on_enter: (events.on_enter ?? []).filter(
          (candidate) => candidate.type !== "configure_session",
        ),
      },
    });
  };

  const addRule = (agentName?: string, operation: ConfigureSessionOperation = "set") => {
    const selectedAgent = agentName ?? choices[0]?.name ?? "";
    if (!selectedAgent) return;
    updateRules([
      ...rules,
      {
        agent_name: selectedAgent,
        operation,
        ...(operation === "set"
          ? { model: defaultModelForAgent(selectedAgent, availableAgents.items) }
          : {}),
      },
    ]);
  };

  const createCarryRule = (
    warning: SessionConfigCarryWarning,
    operation: ConfigureSessionOperation,
  ) => {
    if (rules.some((rule) => rule.agent_name === warning.agentName)) return;
    addRule(warning.agentName, operation);
  };

  return (
    <section
      className="space-y-3 rounded-md border border-border/70 bg-muted/20 p-3"
      data-testid={`${step.id}-session-config-editor`}
      data-settings-dirty={isDirty}
    >
      <SessionConfigHeader
        step={step}
        enabled={!!action}
        isDirty={isDirty}
        readOnly={readOnly}
        onEnable={() => addRule()}
        onDisable={removeConfiguration}
        onAddRule={() => addRule()}
        canAddRule={choices.length > 0}
      />
      <SessionConfigBody
        step={step}
        action={action}
        rules={rules}
        warnings={warnings}
        choices={choices}
        availableAgents={availableAgents.items}
        readOnly={readOnly}
        onChooseCarryRule={createCarryRule}
        onUpdateRules={updateRules}
      />
    </section>
  );
}

function SessionConfigBody({
  step,
  action,
  rules,
  warnings,
  choices,
  availableAgents,
  readOnly,
  onChooseCarryRule,
  onUpdateRules,
}: {
  step: WorkflowStep;
  action: ReturnType<typeof configureSessionAction>;
  rules: ConfigureSessionRule[];
  warnings: SessionConfigCarryWarning[];
  choices: AgentChoice[];
  availableAgents: ReturnType<typeof useAvailableAgents>["items"];
  readOnly: boolean;
  onChooseCarryRule: (
    warning: SessionConfigCarryWarning,
    operation: ConfigureSessionOperation,
  ) => void;
  onUpdateRules: (rules: ConfigureSessionRule[]) => void;
}) {
  return (
    <>
      {step.agent_profile_id && (
        <p className="rounded-md border border-amber-500/30 bg-amber-500/10 p-2 text-xs text-amber-200">
          Remove the fixed profile override before configuring the original session. The two
          behaviors are mutually exclusive.
        </p>
      )}
      {!action && warnings.length > 0 && !step.agent_profile_id && (
        <SessionConfigCarryWarningPanel
          warnings={warnings}
          onChoose={onChooseCarryRule}
          readOnly={readOnly}
        />
      )}
      {action && (
        <SessionConfigRuleList
          rules={rules}
          warnings={warnings}
          choices={choices}
          availableAgents={availableAgents}
          readOnly={readOnly}
          onChooseCarryRule={onChooseCarryRule}
          onUpdateRules={onUpdateRules}
        />
      )}
    </>
  );
}

function SessionConfigRuleList({
  rules,
  warnings,
  choices,
  availableAgents,
  readOnly,
  onChooseCarryRule,
  onUpdateRules,
}: {
  rules: ConfigureSessionRule[];
  warnings: SessionConfigCarryWarning[];
  choices: AgentChoice[];
  availableAgents: ReturnType<typeof useAvailableAgents>["items"];
  readOnly: boolean;
  onChooseCarryRule: (
    warning: SessionConfigCarryWarning,
    operation: ConfigureSessionOperation,
  ) => void;
  onUpdateRules: (rules: ConfigureSessionRule[]) => void;
}) {
  return (
    <div className="space-y-3">
      {warnings.length > 0 && (
        <SessionConfigCarryWarningPanel
          warnings={warnings}
          onChoose={onChooseCarryRule}
          readOnly={readOnly}
        />
      )}
      {rules.map((rule, index) => (
        <SessionConfigRuleCard
          key={`${rule.agent_name}-${index}`}
          rule={rule}
          index={index}
          choices={choices}
          availableAgents={availableAgents}
          readOnly={readOnly}
          onChange={(nextRule) => {
            const nextRules = rules.slice();
            nextRules[index] = nextRule;
            onUpdateRules(nextRules);
          }}
          onRemove={() =>
            onUpdateRules(rules.filter((_, candidateIndex) => candidateIndex !== index))
          }
        />
      ))}
      {rules.length === 0 && (
        <p className="text-xs text-muted-foreground">
          Add a condition to choose which initial agent receives settings.
        </p>
      )}
    </div>
  );
}

function SessionConfigHeader({
  step,
  enabled,
  isDirty,
  readOnly,
  onEnable,
  onDisable,
  onAddRule,
  canAddRule,
}: {
  step: WorkflowStep;
  enabled: boolean;
  isDirty: boolean;
  readOnly: boolean;
  onEnable: () => void;
  onDisable: () => void;
  onAddRule: () => void;
  canAddRule: boolean;
}) {
  const disabled = readOnly || !!step.agent_profile_id;
  return (
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="flex min-w-0 items-start gap-2">
        <Checkbox
          id={`${step.id}-configure-session`}
          checked={enabled}
          onCheckedChange={(checked) => (checked === true ? onEnable() : onDisable())}
          disabled={disabled}
          data-settings-dirty={isDirty}
        />
        <div className="min-w-0">
          <Label htmlFor={`${step.id}-configure-session`} className="text-sm font-medium">
            Configure original session
          </Label>
          <p className="text-xs text-muted-foreground">
            Change the existing conversation tab only when its initial agent family matches a rule.
          </p>
        </div>
        <HelpTip text="Rules apply to the task's first agent session. They never create or switch conversation tabs." />
      </div>
      {enabled && !readOnly && (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="min-h-10 cursor-pointer"
          onClick={onAddRule}
          disabled={!canAddRule}
          data-testid={`${step.id}-add-session-config-rule`}
        >
          <IconPlus className="mr-1.5 h-4 w-4" />
          Add condition
        </Button>
      )}
    </div>
  );
}
