import { appConfig } from "../../config/appConfig";
import { create } from "zustand";
import {
  Actions,
  type AgentApprovalRequest,
  type AgentChatEvent,
  type AgentConversation,
  type AgentHistoryResult,
  type AgentProviderID,
  type AgentProviderInfo,
  type AgentUIContextPayload,
} from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";
import { safeStorage } from "../../lib/storage";

export type AgentDrawerTab = "chat" | "connections";
/** What the chat tab shows: the current chat or the list of earlier chats. */
export type AgentChatView = "chat" | "history";

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
  providerId?: AgentProviderID;
  streaming?: boolean;
  /** i18n key + params for a server error, translated when rendered. */
  errorKey?: string;
  errorParams?: Record<string, string>;
}

const WIDTH_STORAGE_KEY = "dezuxk-agent-drawer-width";
const DEFAULT_WIDTH = 420;
const MIN_WIDTH = 340;
const MAX_WIDTH = 680;

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
      providerId: m.provider_id,
      streaming: false,
    })),
  };
}

export function clampDrawerWidth(width: number): number {
  if (!Number.isFinite(width)) return DEFAULT_WIDTH;
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(width)));
}

function readSavedWidth(): number {
  if (typeof window === "undefined") return DEFAULT_WIDTH;
  const raw = safeStorage.get(WIDTH_STORAGE_KEY);
  if (!raw) return DEFAULT_WIDTH;
  return clampDrawerWidth(Number(raw));
}

let localMsgSeq = 0;

interface AgentState {
  panelOpen: boolean;
  panelWidth: number;
  activeTab: AgentDrawerTab;
  providers: AgentProviderInfo[];
  approvals: AgentApprovalRequest[];
  allowedDirs: string[];
  loadingProviders: boolean;
  busyAction: boolean;
  sending: boolean;
  error: ActionError | null;
  prompt: string;
  messages: AgentChatMessage[];
  /** Current chat ("" = a new chat that has no messages yet). */
  conversationId: string;
  chatView: AgentChatView;
  conversations: AgentConversation[];
  loadingConversations: boolean;

  openPanel: () => void;
  closePanel: () => void;
  togglePanel: () => void;
  setPanelWidth: (width: number, persist?: boolean) => void;
  setActiveTab: (tab: AgentDrawerTab) => void;
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
  connectProvider: (id: AgentProviderID) => Promise<void>;
  disconnect: () => Promise<void>;
  sendPrompt: () => Promise<void>;
  cancelPrompt: () => Promise<void>;
  /** Starts a new chat; the previous one stays in history. */
  newChat: () => Promise<void>;
  setChatView: (view: AgentChatView) => void;
  fetchConversations: () => Promise<void>;
  openConversation: (id: string) => Promise<void>;
  deleteConversation: (id: string) => Promise<void>;
  /** Re-sends the last user message after a failed turn, dropping the error reply. */
  retryLast: () => Promise<void>;
  resolveApproval: (id: string, approve: boolean) => Promise<void>;
  addAllowedDir: (dir: string) => Promise<void>;
  removeAllowedDir: (dir: string) => Promise<void>;
  setUIContext: (ctx: AgentUIContextPayload) => Promise<void>;

  receiveStatus: (info: AgentProviderInfo) => void;
  receiveChatEvent: (evt: AgentChatEvent) => void;
  receiveApprovals: (approvals: AgentApprovalRequest[]) => void;
}

export const useAgentStore = create<AgentState>((set, get) => ({
  panelOpen: true,
  panelWidth: readSavedWidth(),
  activeTab: "chat",
  providers: [],
  approvals: [],
  allowedDirs: [],
  loadingProviders: false,
  busyAction: false,
  sending: false,
  error: null,
  prompt: "",
  messages: [],
  conversationId: "",
  chatView: "chat",
  conversations: [],
  loadingConversations: false,

  openPanel: () => set({ panelOpen: true }),
  closePanel: () => set({ panelOpen: false }),
  togglePanel: () => set((s) => ({ panelOpen: !s.panelOpen })),

  setPanelWidth: (width, persist = false) => {
    const next = clampDrawerWidth(width);
    if (persist) {
      safeStorage.set(WIDTH_STORAGE_KEY, String(next));
    }
    set({ panelWidth: next });
  },

  setActiveTab: (activeTab) => set({ activeTab }),
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
          providers: res.providers,
          loadingProviders: false,
          ...(turnOver
            ? {
                sending: false,
                messages: s.messages.map((m) => (m.streaming ? { ...m, streaming: false } : m)),
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
    } catch {
      // Ignore history hydration errors on bootstrap
    }
  },

  fetchApprovals: async () => {
    try {
      const res = await rtClient.request(Actions.AGENT_LIST_APPROVALS, {});
      set({ approvals: res.approvals ?? [] });
    } catch {
      // Ignore approval fetch errors on bootstrap
    }
  },

  fetchAllowedDirs: async () => {
    try {
      const res = await rtClient.request(Actions.AGENT_LIST_ALLOWED_DIRS, {});
      set({ allowedDirs: res.dirs ?? [] });
    } catch {
      // Ignore allowed dirs fetch errors on bootstrap
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
      set({ busyAction: false, activeTab: "chat" });
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
      set({ messages: [], approvals: [], error: null, conversationId: "", chatView: "chat" });
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  setChatView: (chatView) => {
    set({ chatView });
    if (chatView === "history") void get().fetchConversations();
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
      set({ ...historyState(res), approvals: [], chatView: "chat" });
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

  setUIContext: async (ctx) => {
    try {
      await rtClient.request(Actions.AGENT_SET_UI_CONTEXT, ctx);
    } catch {
      // Ignore UI context sync failures when disconnected
    }
  },

  receiveStatus: (info) => {
    set((s) => {
      const exists = s.providers.some((p) => p.id === info.id);
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
          evt.type === "error")
      ) {
        msgs.push({
          id: evt.message_id || `msg-${++localMsgSeq}`,
          role: evt.type === "error" ? "error" : "assistant",
          text: "",
          thought: "",
          tools: [],
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
          const matchIdx = prevTools.findIndex(
            (item) => item.status === "running" && (!name || item.name === name)
          );
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
        case "done":
          target.streaming = false;
          msgs[idx] = target;
          return { messages: msgs, sending: false };
        case "error":
          target.streaming = false;
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
