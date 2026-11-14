import { appConfig } from "../../config/appConfig";
import { create } from "zustand";
import {
  Actions,
  type AgentApprovalRequest,
  type AgentChatEvent,
  type AgentConversation,
  type AgentHistoryResult,
  type AgentLoginPrompt,
  type AgentProviderID,
  type AgentProviderInfo,
  type SubagentInvocation,
} from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";
import { useAgentDrawerStore } from "./useAgentDrawerStore";

export interface AgentToolCallItem {
  name: string;
  status: "running" | "done" | "error";
}

export interface AgentChatMessage {
  id: string;
  role: "user" | "assistant" | "error";
  text: string;
  thought?: string;
  tools?: AgentToolCallItem[];
  subagents?: SubagentInvocation[];
  providerId?: AgentProviderID;
  streaming?: boolean;
  /** i18n key + params for a server error, translated when rendered. */
  errorKey?: string;
  errorParams?: Record<string, string>;
}

/** How long "Copied" feedback stays visible after a clipboard copy in the agent panel. */
export const COPY_FEEDBACK_MS = appConfig.ui.copyFeedbackMs;

/** Maps a server history result to store state. */
function historyState(res: AgentHistoryResult): Pick<AgentState, "messages" | "conversationId"> {
  return {
    conversationId: res.conversation_id ?? "",
    messages: (res.messages ?? []).map((m) => ({
      id: m.id,
      role: m.role === "user" ? "user" : m.role === "error" ? "error" : "assistant",
      text: m.text,
      thought: m.thought || undefined,
      tools: m.tool
        ? m.tool.split(", ").map((name) => ({ name, status: "done" as const }))
        : undefined,
      subagents: m.subagents?.length ? m.subagents : undefined,
      providerId: m.provider_id,
      streaming: false,
    })),
  };
}

let localMsgSeq = 0;

interface AgentState {
  providers: AgentProviderInfo[];
  developerMode: boolean;
  approvals: AgentApprovalRequest[];
  allowedDirs: string[];
  loadingProviders: boolean;
  busyAction: boolean;
  /** What the user must do to finish a sign-in that is running right now, by provider. */
  loginPrompts: Partial<Record<AgentProviderID, AgentLoginPrompt>>;
  sending: boolean;
  error: ActionError | null;
  prompt: string;
  messages: AgentChatMessage[];
  /** Current chat ("" = a new chat that has no messages yet). */
  conversationId: string;
  conversations: AgentConversation[];
  loadingConversations: boolean;

  setPrompt: (prompt: string) => void;
  clearError: () => void;

  fetchProviders: () => Promise<void>;
  fetchHistory: () => Promise<void>;
  fetchApprovals: () => Promise<void>;
  fetchAllowedDirs: () => Promise<void>;
  probeProvider: (id: AgentProviderID) => Promise<void>;
  setCustomPath: (id: AgentProviderID, customPath: string) => Promise<void>;
  setModel: (id: AgentProviderID, model: string, effort: string) => Promise<void>;
  loginProvider: (id: AgentProviderID, apiKey?: string) => Promise<void>;
  logoutProvider: (id: AgentProviderID) => Promise<void>;
  submitLoginCode: (id: AgentProviderID, code: string) => Promise<void>;
  cancelLogin: (id: AgentProviderID) => Promise<void>;
  receiveLoginPrompt: (prompt: AgentLoginPrompt) => void;
  connectProvider: (id: AgentProviderID) => Promise<void>;
  disconnect: () => Promise<void>;
  sendPrompt: () => Promise<void>;
  cancelPrompt: () => Promise<void>;
  /** Starts a new chat; the previous one stays in history. */
  newChat: () => Promise<void>;
  fetchConversations: () => Promise<void>;
  openConversation: (id: string) => Promise<void>;
  deleteConversation: (id: string) => Promise<void>;
  /** Re-sends the last user message after a failed turn, dropping the error reply. */
  retryLast: () => Promise<void>;
  resolveApproval: (id: string, approve: boolean) => Promise<void>;
  addAllowedDir: (dir: string) => Promise<void>;
  removeAllowedDir: (dir: string) => Promise<void>;

  receiveStatus: (info: AgentProviderInfo) => void;
  receiveChatEvent: (evt: AgentChatEvent) => void;
  receiveApprovals: (approvals: AgentApprovalRequest[]) => void;
}

/**
 * The newer of two snapshots of one provider. Revision only grows, so a slow agent:list response that
 * was built before a "connected, with models" event can never overwrite it.
 */
export function newerInfo(
  current: AgentProviderInfo | undefined,
  incoming: AgentProviderInfo
): AgentProviderInfo {
  return current && current.revision > incoming.revision ? current : incoming;
}

/** Marks every still-running tool of a finished reply with status (done or error). */
export function finishRunningTools<T extends { status: string }>(
  tools: T[] | undefined,
  status: "done" | "error"
): T[] | undefined {
  if (!tools?.some((t) => t.status === "running")) return tools;
  return tools.map((t) => (t.status === "running" ? { ...t, status } : t));
}

export const useAgentStore = create<AgentState>((set, get) => ({
  providers: [],
  developerMode: false,
  approvals: [],
  allowedDirs: [],
  loadingProviders: false,
  busyAction: false,
  loginPrompts: {},
  sending: false,
  error: null,
  prompt: "",
  messages: [],
  conversationId: "",
  conversations: [],
  loadingConversations: false,

  setPrompt: (prompt) => set({ prompt }),
  clearError: () => set({ error: null }),

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

  fetchHistory: async () => {
    try {
      const res = await rtClient.request(Actions.AGENT_HISTORY, {});
      set(historyState(res));
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  fetchApprovals: async () => {
    try {
      const res = await rtClient.request(Actions.AGENT_LIST_APPROVALS, {});
      set({ approvals: res.approvals ?? [] });
    } catch (err) {
      set({ error: toActionError(err) });
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

  sendPrompt: async () => {
    const text = get().prompt.trim();
    const active = get().providers.find((p) => p.active);
    if (!text || !active || get().sending) return;

    const seq = ++localMsgSeq;
    const userMsgId = `user-${seq}`;
    const pendingAssistantId = `pending-${seq}`;

    set((s) => ({
      prompt: "",
      sending: true,
      error: null,
      messages: [
        ...s.messages,
        { id: userMsgId, role: "user", text, providerId: active.id },
        {
          id: pendingAssistantId,
          role: "assistant",
          text: "",
          thought: "",
          tools: [],
          providerId: active.id,
          streaming: true,
        },
      ],
    }));

    try {
      const res = await rtClient.request(Actions.AGENT_SEND_PROMPT, { prompt: text });
      set((s) => {
        const hasAuthoritative = s.messages.some((m) => m.id === res.message_id);
        return {
          conversationId: res.conversation_id || s.conversationId,
          // If streamed events already created the real message, drop the placeholder instead of
          // leaving an empty bubble that streams forever.
          messages: hasAuthoritative
            ? s.messages.filter((m) => m.id !== pendingAssistantId)
            : s.messages.map((m) =>
                m.id === pendingAssistantId ? { ...m, id: res.message_id } : m
              ),
        };
      });
    } catch (err) {
      const actionErr = toActionError(err);
      set((s) => ({
        sending: false,
        error: actionErr,
        messages: s.messages.map((m) =>
          m.id === pendingAssistantId
            ? { ...m, role: "error", text: actionErr.message, streaming: false }
            : m
        ),
      }));
    }
  },

  cancelPrompt: async () => {
    try {
      await rtClient.request(Actions.AGENT_CANCEL_PROMPT, {});
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  newChat: async () => {
    // Switch on the server first: if it refuses (a reply is still running) the visible chat must
    // stay in sync with the server's current one.
    try {
      await rtClient.request(Actions.AGENT_NEW_CONVERSATION, {});
      set({ messages: [], approvals: [], error: null, conversationId: "" });
      useAgentDrawerStore.getState().setChatView("chat");
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  fetchConversations: async () => {
    set({ loadingConversations: true });
    try {
      const res = await rtClient.request(Actions.AGENT_LIST_CONVERSATIONS, {});
      set({ conversations: res.conversations ?? [] });
    } catch (err) {
      set({ error: toActionError(err) });
    } finally {
      set({ loadingConversations: false });
    }
  },

  openConversation: async (id) => {
    set({ error: null });
    try {
      const res = await rtClient.request(Actions.AGENT_OPEN_CONVERSATION, { id });
      set({ ...historyState(res), approvals: [] });
      useAgentDrawerStore.getState().setChatView("chat");
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  deleteConversation: async (id) => {
    set({ error: null });
    try {
      await rtClient.request(Actions.AGENT_DELETE_CONVERSATION, { id });
      set((s) => ({
        conversations: s.conversations.filter((c) => c.id !== id),
        // Deleting the current chat leaves a new, empty one.
        ...(s.conversationId === id ? { messages: [], conversationId: "" } : {}),
      }));
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  retryLast: async () => {
    const { messages, sending } = get();
    if (sending) return;
    let userIdx = -1;
    for (let i = messages.length - 1; i >= 0; i--) {
      if (messages[i].role === "user") {
        userIdx = i;
        break;
      }
    }
    if (userIdx === -1) return;
    const text = messages[userIdx].text;
    // Drop the failed exchange; sendPrompt appends the user message and a fresh reply again.
    set({ messages: messages.slice(0, userIdx), prompt: text });
    await get().sendPrompt();
  },

  resolveApproval: async (id, approve) => {
    set({ error: null });
    try {
      await rtClient.request(Actions.AGENT_RESOLVE_APPROVAL, { id, approve });
    } catch (err) {
      set({ error: toActionError(err) });
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

  receiveApprovals: (approvals) => set({ approvals: approvals ?? [] }),

  receiveChatEvent: (evt) => {
    set((s) => {
      const msgs = [...s.messages];
      let idx = msgs.findIndex((m) => m.id === evt.message_id);
      if (idx === -1) {
        // Events can arrive before the send request resolves; they belong to the newest placeholder.
        for (let i = msgs.length - 1; i >= 0; i--) {
          if (msgs[i].streaming && msgs[i].id.startsWith("pending-")) {
            idx = i;
            break;
          }
        }
      }
      if (
        idx === -1 &&
        (evt.type === "delta" ||
          evt.type === "thought" ||
          evt.type === "tool_call" ||
          evt.type === "subagent_start" ||
          evt.type === "error")
      ) {
        msgs.push({
          id: evt.message_id || `msg-${++localMsgSeq}`,
          role: evt.type === "error" ? "error" : "assistant",
          text: "",
          thought: "",
          tools: [],
          subagents: [],
          providerId: evt.provider_id,
          streaming: evt.type !== "error",
        });
        idx = msgs.length - 1;
      }

      if (idx === -1) {
        return evt.type === "done" || evt.type === "error" ? { sending: false } : s;
      }

      const target = { ...msgs[idx] };
      if (evt.message_id) {
        target.id = evt.message_id;
      }

      const findSubagentIdx = (list: NonNullable<AgentChatMessage["subagents"]>) => {
        if (evt.subagent_id) {
          const byId = list.findIndex((sa) => sa.id === evt.subagent_id);
          if (byId >= 0) return byId;
        }
        if (evt.subagent_name) {
          for (let i = list.length - 1; i >= 0; i--) {
            if (list[i].status === "running" && list[i].name === evt.subagent_name) return i;
          }
        }
        for (let i = list.length - 1; i >= 0; i--) {
          if (list[i].status === "running") return i;
        }
        return -1;
      };

      switch (evt.type) {
        case "delta":
          target.text = `${target.text}${evt.text}`;
          msgs[idx] = target;
          return { messages: msgs };
        case "thought":
          target.thought = `${target.thought || ""}${evt.text}`;
          msgs[idx] = target;
          return { messages: msgs };
        case "tool_call": {
          const name = evt.tool || evt.text;
          const prevTools = target.tools ?? [];
          target.tools = [...prevTools, { name, status: "running" }];
          msgs[idx] = target;
          return { messages: msgs };
        }
        case "tool_result": {
          const name = evt.tool || evt.text;
          const prevTools = [...(target.tools ?? [])];
          let matchIdx = prevTools.findIndex(
            (item) => item.status === "running" && (!name || item.name === name)
          );
          // Agents may title the result differently from the call: end the oldest running tool instead
          // of leaving it spinning next to a duplicate finished row.
          if (matchIdx < 0) matchIdx = prevTools.findIndex((item) => item.status === "running");
          // The backend signals failure through `error`; the text is tool output and may mention "error".
          const nextStatus = evt.error ? "error" : "done";
          if (matchIdx >= 0) {
            prevTools[matchIdx] = { ...prevTools[matchIdx], status: nextStatus };
          } else if (name) {
            prevTools.push({ name, status: nextStatus });
          }
          target.tools = prevTools;
          msgs[idx] = target;
          return { messages: msgs };
        }
        case "subagent_start": {
          const prevSubs = [...(target.subagents ?? [])];
          const subId = evt.subagent_id || evt.subagent_name || `sub-${prevSubs.length + 1}`;
          const subName = evt.subagent_name || evt.text || subId;
          const existingIdx = prevSubs.findIndex((sa) => sa.id === subId);
          if (existingIdx >= 0) {
            prevSubs[existingIdx] = {
              ...prevSubs[existingIdx],
              name: subName,
              provider: evt.subagent_provider ?? prevSubs[existingIdx].provider,
              status: "running",
            };
          } else {
            prevSubs.push({
              id: subId,
              name: subName,
              provider: evt.subagent_provider,
              status: "running",
              text: "",
              tools: [],
            });
          }
          target.subagents = prevSubs;
          msgs[idx] = target;
          return { messages: msgs };
        }
        case "subagent_delta": {
          const prevSubs = [...(target.subagents ?? [])];
          const subIdx = findSubagentIdx(prevSubs);
          if (subIdx >= 0) {
            const cur = prevSubs[subIdx];
            prevSubs[subIdx] = { ...cur, text: `${cur.text ?? ""}${evt.text}` };
          } else {
            const subId = evt.subagent_id || evt.subagent_name || `sub-${prevSubs.length + 1}`;
            prevSubs.push({
              id: subId,
              name: evt.subagent_name || subId,
              provider: evt.subagent_provider,
              status: "running",
              text: evt.text,
              tools: [],
            });
          }
          target.subagents = prevSubs;
          msgs[idx] = target;
          return { messages: msgs };
        }
        case "subagent_tool_call": {
          const prevSubs = [...(target.subagents ?? [])];
          const toolName = evt.tool || evt.text;
          const subIdx = findSubagentIdx(prevSubs);
          if (subIdx >= 0) {
            const cur = prevSubs[subIdx];
            const nextTools = toolName ? [...(cur.tools ?? []), toolName] : (cur.tools ?? []);
            prevSubs[subIdx] = { ...cur, tools: nextTools };
          } else {
            const subId = evt.subagent_id || evt.subagent_name || `sub-${prevSubs.length + 1}`;
            prevSubs.push({
              id: subId,
              name: evt.subagent_name || subId,
              provider: evt.subagent_provider,
              status: "running",
              text: "",
              tools: toolName ? [toolName] : [],
            });
          }
          target.subagents = prevSubs;
          msgs[idx] = target;
          return { messages: msgs };
        }
        case "subagent_tool_result": {
          const prevSubs = [...(target.subagents ?? [])];
          const toolName = evt.tool || evt.text;
          const subIdx = findSubagentIdx(prevSubs);
          if (subIdx >= 0 && toolName) {
            const cur = prevSubs[subIdx];
            const curTools = cur.tools ?? [];
            if (!curTools.includes(toolName)) {
              prevSubs[subIdx] = { ...cur, tools: [...curTools, toolName] };
              target.subagents = prevSubs;
              msgs[idx] = target;
              return { messages: msgs };
            }
          }
          return s;
        }
        case "subagent_done": {
          const prevSubs = [...(target.subagents ?? [])];
          const subIdx = findSubagentIdx(prevSubs);
          const nextStatus = evt.error ? "error" : "done";
          if (subIdx >= 0) {
            const cur = prevSubs[subIdx];
            prevSubs[subIdx] = {
              ...cur,
              status: nextStatus,
              error: evt.error || cur.error,
              text: cur.text || evt.text || undefined,
            };
          } else {
            const subId = evt.subagent_id || evt.subagent_name || `sub-${prevSubs.length + 1}`;
            prevSubs.push({
              id: subId,
              name: evt.subagent_name || subId,
              provider: evt.subagent_provider,
              status: nextStatus,
              text: evt.text || undefined,
              tools: [],
              error: evt.error || undefined,
            });
          }
          target.subagents = prevSubs;
          msgs[idx] = target;
          return { messages: msgs };
        }
        case "done":
          target.streaming = false;
          // The turn is over, so no tool can still be running (a lost or unmatched result event).
          target.tools = finishRunningTools(target.tools, "done");
          target.subagents = finishRunningTools(target.subagents, "done");
          msgs[idx] = target;
          return { messages: msgs, sending: false };
        case "error":
          target.streaming = false;
          target.tools = finishRunningTools(target.tools, "error");
          target.subagents = finishRunningTools(target.subagents, "error");
          target.errorKey = evt.error_key;
          target.errorParams = evt.error_params;
          if (!target.text) {
            target.role = "error";
            target.text = evt.error || "Agent error";
          } else {
            target.text = `${target.text}\n\n[${evt.error || "Agent error"}]`;
          }
          msgs[idx] = target;
          return { messages: msgs, sending: false };
        default:
          return s;
      }
    });
  },
}));
