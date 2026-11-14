import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Actions } from "../../contracts/generated";
import { ActionError, rtClient } from "../../lib/rtClient";
import { useAgentCustomizationsStore } from "./useAgentCustomizationsStore";

const initialState = useAgentCustomizationsStore.getState();

describe("useAgentCustomizationsStore", () => {
  beforeEach(() => {
    useAgentCustomizationsStore.setState(initialState);
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("manages skills and subagents via customization store actions", async () => {
    const snap = {
      dir: "C:/data/agent-customizations",
      skills: [
        {
          name: "brief-structuring",
          description: "Structure briefs",
          user_invocable: true,
          enabled: true,
          source_state: "managed" as const,
          built_in: true,
          tombstoned: false,
          body: "## Steps",
        },
      ],
      subagents: [
        {
          name: "workspace-reviewer",
          description: "Review workspace",
          skills: ["review-checklist"],
          tools: ["workspace_read_file"],
          enabled: true,
          source_state: "managed" as const,
          built_in: true,
          tombstoned: false,
          body: "Review carefully",
        },
      ],
      available_tools: ["workspace_read_file", "workspace_list_dir"],
    };

    const reqSpy = vi.spyOn(rtClient, "request").mockResolvedValue(snap as never);
    await useAgentCustomizationsStore.getState().fetchCustomizations();
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_LIST_CUSTOMIZATIONS, {});
    expect(useAgentCustomizationsStore.getState().customizationsDir).toBe(snap.dir);
    expect(useAgentCustomizationsStore.getState().skills).toHaveLength(1);
    expect(useAgentCustomizationsStore.getState().subagents).toHaveLength(1);
    expect(useAgentCustomizationsStore.getState().availableSubagentTools).toEqual([
      "workspace_read_file",
      "workspace_list_dir",
    ]);

    const okSkill = await useAgentCustomizationsStore.getState().saveSkill({
      name: "custom-skill",
      description: "Custom",
      user_invocable: true,
      enabled: true,
      body: "Body",
    });
    expect(okSkill).toBe(true);
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_SAVE_SKILL, {
      name: "custom-skill",
      description: "Custom",
      user_invocable: true,
      enabled: true,
      body: "Body",
    });

    await useAgentCustomizationsStore.getState().deleteSkill("custom-skill");
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_DELETE_SKILL, { name: "custom-skill" });
    await useAgentCustomizationsStore.getState().restoreSkill("brief-structuring");
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_RESTORE_SKILL, {
      name: "brief-structuring",
    });

    const okSub = await useAgentCustomizationsStore.getState().saveSubagent({
      name: "custom-sub",
      description: "Sub",
      skills: [],
      tools: ["workspace_read_file"],
      enabled: true,
      body: "Sub body",
    });
    expect(okSub).toBe(true);
    await useAgentCustomizationsStore.getState().deleteSubagent("custom-sub");
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_DELETE_SUBAGENT, { name: "custom-sub" });
    await useAgentCustomizationsStore.getState().restoreSubagent("workspace-reviewer");
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_RESTORE_SUBAGENT, {
      name: "workspace-reviewer",
    });
  });

  it("reloads customizations and surfaces/clears action errors", async () => {
    const reqSpy = vi.spyOn(rtClient, "request").mockResolvedValueOnce({
      dir: "C:/reloaded",
      skills: [],
      subagents: [],
      available_tools: ["workspace_read_file"],
    } as never);

    await useAgentCustomizationsStore.getState().reloadCustomizations();
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_RELOAD_CUSTOMIZATIONS, {});
    expect(useAgentCustomizationsStore.getState().customizationsDir).toBe("C:/reloaded");
    expect(useAgentCustomizationsStore.getState().loadingCustomizations).toBe(false);

    reqSpy.mockRejectedValueOnce(new ActionError("ERR_VALIDATION", "invalid skill"));
    const saved = await useAgentCustomizationsStore.getState().saveSkill({
      name: "bad",
      description: "",
      user_invocable: false,
      enabled: true,
      body: "",
    });
    expect(saved).toBe(false);
    expect(useAgentCustomizationsStore.getState().error?.message).toBe("invalid skill");

    useAgentCustomizationsStore.getState().clearError();
    expect(useAgentCustomizationsStore.getState().error).toBeNull();
  });
});
