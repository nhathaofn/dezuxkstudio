import {
  Actions,
  type AgentProviderID,
  type AgentProviderLinkItem,
} from "../../contracts/generated";
import { rtClient, toActionError } from "../../lib/rtClient";
import { useAgentDrawerStore } from "./useAgentDrawerStore";
import {
  newerInfo,
  finishRunningTools,
  type AgentGet,
  type AgentSet,
  type AgentState,
} from "./agentStoreTypes";

export function createAgentConnectionSlice(
  set: AgentSet,
  get: AgentGet
): Pick<
  AgentState,
  | "fetchProviders"
  | "fetchProviderLinks"
  | "linkProvider"
  | "unlinkProvider"
  | "fetchAllowedDirs"
  | "probeProvider"
  | "setCustomPath"
  | "setModel"
  | "loginProvider"
  | "receiveLoginPrompt"
  | "submitLoginCode"
  | "cancelLogin"
  | "logoutProvider"
  | "connectProvider"
  | "disconnect"
  | "addAllowedDir"
  | "removeAllowedDir"
  | "receiveStatus"
> {
  return {
  fetchProviders: async () => {
    set({ loadingProviders: true, error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_LIST, {});
      set((s) => {
        // After a reconnect the turn's final chat event may have been missed while offline. If no
        // provider is still busy, the reply is over: unlock the input and stop the spinners
        // (fetchHistory, run on the same connect, brings the stored final text).
        const turnOver = s.sending && !res.providers.some((p) => p.busy);
        return {
          providers: res.providers.map((next) =>
            newerInfo(
              s.providers.find((p) => p.id === next.id),
              next
            )
          ),
          developerMode: Boolean(res.developer_mode),
          loadingProviders: false,
          ...(turnOver
            ? {
                sending: false,
                messages: s.messages.map((m) =>
                  m.streaming
                    ? {
                        ...m,
                        streaming: false,
                        tools: finishRunningTools(m.tools, "done"),
                        subagents: finishRunningTools(m.subagents, "done"),
                      }
                    : m
                ),
              }
            : {}),
        };
      });
    } catch (err) {
      set({ error: toActionError(err), loadingProviders: false });
    }
  },

  fetchProviderLinks: async () => {
    try {
      const res = await rtClient.request(Actions.AGENT_LIST_PROVIDER_LINKS, {});
      const mapped: Record<string, AgentProviderLinkItem> = {};
      for (const link of res.links ?? []) {
        if (link.agent_provider_id) {
          mapped[link.agent_provider_id] = link;
        }
      }
      set({ providerLinks: mapped });
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  linkProvider: async (agentProviderId, providerId, credentialId) => {
    try {
      await rtClient.request(Actions.AGENT_LINK_PROVIDER, {
        agent_provider_id: agentProviderId,
        provider_id: providerId,
        credential_id: credentialId,
      });
      const item: AgentProviderLinkItem = {
        agent_provider_id: agentProviderId,
        provider_id: providerId,
        credential_id: credentialId,
      };
      set((s) => ({
        providerLinks: { ...s.providerLinks, [agentProviderId]: item },
      }));
    } catch (err) {
      set({ error: toActionError(err) });
      throw err;
    }
  },

  unlinkProvider: async (agentProviderId) => {
    try {
      await rtClient.request(Actions.AGENT_UNLINK_PROVIDER, {
        id: agentProviderId as AgentProviderID,
      });
      set((s) => {
        const next = { ...s.providerLinks };
        delete next[agentProviderId];
        return { providerLinks: next };
      });
    } catch (err) {
      set({ error: toActionError(err) });
      throw err;
    }
  },

  fetchAllowedDirs: async () => {
    try {
      const res = await rtClient.request(Actions.AGENT_LIST_ALLOWED_DIRS, {});
      set({ allowedDirs: res.dirs ?? [] });
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  probeProvider: async (id) => {
    set({ error: null });
    try {
      const updated = await rtClient.request(Actions.AGENT_PROBE, { id });
      get().receiveStatus(updated);
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  setCustomPath: async (id, customPath) => {
    set({ error: null });
    try {
      const updated = await rtClient.request(Actions.AGENT_SET_PATH, {
        id,
        custom_path: customPath,
      });
      get().receiveStatus(updated);
    } catch (err) {
      set({ error: toActionError(err) });
      throw err;
    }
  },

  setModel: async (id, model, effort) => {
    set({ error: null });
    try {
      const updated = await rtClient.request(Actions.AGENT_SET_MODEL, { id, model, effort });
      get().receiveStatus(updated);
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  loginProvider: async (id, apiKey = "") => {
    set({ busyAction: true, error: null });
    try {
      const updated = await rtClient.request(Actions.AGENT_LOGIN, { id, api_key: apiKey });
      get().receiveStatus(updated);
      set({ busyAction: false });
    } catch (err) {
      set({ error: toActionError(err), busyAction: false });
      void get().fetchProviders();
    } finally {
      set((s) => ({ loginPrompts: { ...s.loginPrompts, [id]: undefined } }));
    }
  },

  receiveLoginPrompt: (prompt) =>
    set((s) => ({ loginPrompts: { ...s.loginPrompts, [prompt.provider_id]: prompt } })),

  submitLoginCode: async (id, code) => {
    try {
      await rtClient.request(Actions.AGENT_LOGIN_CODE, { id, code });
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  cancelLogin: async (id) => {
    set((s) => ({ loginPrompts: { ...s.loginPrompts, [id]: undefined } }));
    try {
      await rtClient.request(Actions.AGENT_LOGIN_CANCEL, { id });
    } catch {
      /* the sign-in already ended */
    }
  },

  logoutProvider: async (id) => {
    set({ busyAction: true, error: null });
    try {
      const updated = await rtClient.request(Actions.AGENT_LOGOUT, { id });
      get().receiveStatus(updated);
      set({ busyAction: false });
    } catch (err) {
      set({ error: toActionError(err), busyAction: false });
    }
  },

  connectProvider: async (id) => {
    set({ busyAction: true, error: null });
    try {
      const updated = await rtClient.request(Actions.AGENT_CONNECT, { id });
      get().receiveStatus(updated);
      const { conversationId, messages, conversations } = get();
      const convOwner =
        messages.find((m) => m.providerId)?.providerId ??
        conversations.find((c) => c.id === conversationId)?.provider_id;
      const shouldClearChat = Boolean(conversationId && convOwner && convOwner !== id);
      set({
        busyAction: false,
        ...(shouldClearChat ? { conversationId: "", messages: [], approvals: [] } : {}),
      });
      const drawer = useAgentDrawerStore.getState();
      drawer.setActiveTab("chat");
      if (shouldClearChat) {
        drawer.setChatView("chat");
      }
    } catch (err) {
      set({ error: toActionError(err), busyAction: false });
      void get().fetchProviders();
    }
  },

  disconnect: async () => {
    set({ busyAction: true, error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_DISCONNECT, {});
      set({ providers: res.providers, busyAction: false, sending: false });
    } catch (err) {
      set({ error: toActionError(err), busyAction: false });
    }
  },

  addAllowedDir: async (dir) => {
    set({ error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_ADD_ALLOWED_DIR, { dir });
      set({ allowedDirs: res.dirs ?? [] });
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  removeAllowedDir: async (dir) => {
    set({ error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_REMOVE_ALLOWED_DIR, { dir });
      set({ allowedDirs: res.dirs ?? [] });
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  receiveStatus: (info) => {
    set((s) => {
      const current = s.providers.find((p) => p.id === info.id);
      if (current && newerInfo(current, info) === current) return s; // a stale snapshot: keep what we have
      const exists = !!current;
      const next = exists
        ? s.providers.map((p) => {
            if (p.id === info.id) return info;
            if (info.active && p.active) return { ...p, active: false, busy: false };
            return p;
          })
        : [...s.providers, info];
      const stillBusy = next.some((p) => p.active && p.busy);
      return {
        providers: next,
        sending: info.active ? info.busy : stillBusy,
      };
    });
  },
  };
}
