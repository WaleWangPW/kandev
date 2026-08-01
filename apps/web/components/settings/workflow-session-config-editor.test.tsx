import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { WorkflowStep } from "@/lib/types/http";
import { SessionConfigEditor } from "./workflow-session-config-editor";

const CODEX_AGENT = "codex";
const EFFORT_OPTION = "reasoning_effort";
const MODEL_ID = "gpt-5.6-sol";

vi.mock("@/hooks/domains/settings/use-healthy-agent-profiles", () => ({
  useHealthyAgentProfiles: () => [
    {
      id: "codex-profile",
      label: "Codex • Default",
      agent_id: "codex-agent",
      agent_name: CODEX_AGENT,
      cli_passthrough: false,
    },
  ],
}));

vi.mock("@/hooks/domains/settings/use-available-agents", () => ({
  useAvailableAgents: () => ({
    items: [
      {
        name: CODEX_AGENT,
        display_name: "Codex",
        model_config: {
          default_model: MODEL_ID,
          current_model_id: MODEL_ID,
          available_models: [{ id: MODEL_ID, name: "5.6 Sol" }],
          config_options: [
            {
              type: "select",
              id: EFFORT_OPTION,
              name: "Effort",
              current_value: "high",
              options: [
                { value: "high", name: "High" },
                { value: "max", name: "Max" },
              ],
            },
          ],
          supports_dynamic_models: true,
        },
      },
    ],
  }),
}));

afterEach(cleanup);

function step(id: string, position: number, events?: WorkflowStep["events"]): WorkflowStep {
  return {
    id,
    workflow_id: "workflow-1" as WorkflowStep["workflow_id"],
    name: id,
    position,
    color: "bg-muted",
    events,
    created_at: "",
    updated_at: "",
  };
}

describe("SessionConfigEditor", () => {
  it("adds a conditional rule and exposes the model settings picker", () => {
    const onUpdate = vi.fn();
    render(
      <SessionConfigEditor
        step={step("work", 0)}
        steps={[step("work", 0)]}
        onUpdate={onUpdate}
        readOnly={false}
      />,
    );

    fireEvent.click(screen.getByLabelText("Configure original session"));

    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({
        events: expect.objectContaining({
          on_enter: [
            expect.objectContaining({
              type: "configure_session",
              config: {
                rules: [expect.objectContaining({ agent_name: CODEX_AGENT, operation: "set" })],
              },
            }),
          ],
        }),
      }),
    );
  });

  it("offers keep, restore, and set-new choices for carried settings", () => {
    const onUpdate = vi.fn();
    const source = step("work", 0, {
      on_enter: [
        {
          type: "configure_session",
          config: { rules: [{ agent_name: CODEX_AGENT, operation: "set", model: MODEL_ID }] },
        },
      ],
      on_turn_complete: [{ type: "move_to_next" }],
    });
    render(
      <SessionConfigEditor
        step={step("review", 1)}
        steps={[source, step("review", 1)]}
        onUpdate={onUpdate}
        readOnly={false}
      />,
    );

    expect(screen.getByTestId("session-config-carry-warning")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Restore" }));
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({
        events: expect.objectContaining({
          on_enter: [
            expect.objectContaining({
              type: "configure_session",
              config: {
                rules: [{ agent_name: CODEX_AGENT, operation: "restore_original" }],
              },
            }),
          ],
        }),
      }),
    );
  });

  it("disables conditional configuration while a fixed profile override is selected", () => {
    render(
      <SessionConfigEditor
        step={{ ...step("work", 0), agent_profile_id: "codex-profile" }}
        steps={[]}
        onUpdate={vi.fn()}
        readOnly={false}
      />,
    );

    expect(
      screen.getByLabelText("Configure original session").getAttribute("disabled"),
    ).not.toBeNull();
    expect(screen.getByText(/mutually exclusive/i)).toBeTruthy();
  });
});
