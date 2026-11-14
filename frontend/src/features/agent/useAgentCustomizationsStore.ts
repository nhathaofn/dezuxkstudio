import { create } from "zustand";
import {
  Actions,
  type AgentCustomizationsResult,
  type AgentSaveSkillPayload,
  type AgentSaveSubagentPayload,
  type SkillInfo,
  type SubagentInfo,
} from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";

interface AgentCustomizationsState {
  customizationsDir: string;
  skills: SkillInfo[];
  subagents: SubagentInfo[];
  availableSubagentTools: string[];
  loadingCustomizations: boolean;
  error: ActionError | null;

  clearError: () => void;
  fetchCustomizations: () => Promise<void>;
  reloadCustomizations: () => Promise<void>;
  saveSkill: (payload: AgentSaveSkillPayload) => Promise<boolean>;
  deleteSkill: (name: string) => Promise<void>;
  restoreSkill: (name: string) => Promise<void>;
  saveSubagent: (payload: AgentSaveSubagentPayload) => Promise<boolean>;
  deleteSubagent: (name: string) => Promise<void>;
  restoreSubagent: (name: string) => Promise<void>;
  receiveCustomizations: (snap: AgentCustomizationsResult) => void;
}

export const useAgentCustomizationsStore = create<AgentCustomizationsState>((set, get) => ({
  customizationsDir: "",
  skills: [],
  subagents: [],
  availableSubagentTools: [],
  loadingCustomizations: false,
  error: null,

  clearError: () => set({ error: null }),

  fetchCustomizations: async () => {
    set({ loadingCustomizations: true });
    try {
      const res = await rtClient.request(Actions.AGENT_LIST_CUSTOMIZATIONS, {});
      get().receiveCustomizations(res);
    } catch (err) {
      set({ error: toActionError(err) });
    } finally {
      set({ loadingCustomizations: false });
    }
  },

  reloadCustomizations: async () => {
    set({ loadingCustomizations: true, error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_RELOAD_CUSTOMIZATIONS, {});
      get().receiveCustomizations(res);
    } catch (err) {
      set({ error: toActionError(err) });
    } finally {
      set({ loadingCustomizations: false });
    }
  },

  saveSkill: async (payload) => {
    set({ error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_SAVE_SKILL, payload);
      get().receiveCustomizations(res);
      return true;
    } catch (err) {
      set({ error: toActionError(err) });
      return false;
    }
  },

  deleteSkill: async (name) => {
    set({ error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_DELETE_SKILL, { name });
      get().receiveCustomizations(res);
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  restoreSkill: async (name) => {
    set({ error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_RESTORE_SKILL, { name });
      get().receiveCustomizations(res);
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  saveSubagent: async (payload) => {
    set({ error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_SAVE_SUBAGENT, payload);
      get().receiveCustomizations(res);
      return true;
    } catch (err) {
      set({ error: toActionError(err) });
      return false;
    }
  },

  deleteSubagent: async (name) => {
    set({ error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_DELETE_SUBAGENT, { name });
      get().receiveCustomizations(res);
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  restoreSubagent: async (name) => {
    set({ error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_RESTORE_SUBAGENT, { name });
      get().receiveCustomizations(res);
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  receiveCustomizations: (snap) => {
    set({
      customizationsDir: snap.dir ?? "",
      skills: snap.skills ?? [],
      subagents: snap.subagents ?? [],
      availableSubagentTools: snap.available_tools ?? [],
    });
  },
}));
