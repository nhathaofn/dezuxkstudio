import { Actions } from "../../contracts/generated";
import { rtClient, toActionError } from "../../lib/rtClient";
import { useAgentDrawerStore } from "./useAgentDrawerStore";
import {
  finishRunningTools,
  historyState,
  type AgentChatMessage,
  type AgentGet,
  type AgentSet,
  type AgentState,
} from "./agentStoreTypes";

let localMsgSeq = 0;

export function createAgentChatSlice(
  set: AgentSet,
  get: AgentGet
): Pick<
  AgentState,
  | "setPrompt"
  | "clearError"
  | "fetchHistory"
  | "fetchApprovals"
  | "sendPrompt"
  | "cancelPrompt"
  | "retryLast"
  | "newChat"
  | "fetchConversations"
  | "openConversation"
  | "deleteConversation"
  | "resolveApproval"
  | "receiveApprovals"
  | "receiveChatEvent"
> {
  return {
  setPrompt: (prompt) => set({ prompt }),
  clearError: () => set({ error: null }),

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
    const { resolvingApprovalIds, approvals } = get();
    // Anti-double-click guard: reject concurrent clicks on the same approval
    if (resolvingApprovalIds[id]) {
      return;
    }
    const current = approvals.find((a) => a.id === id);
    if (current?.status && current.status !== "pending") {
      return;
    }

    // Optimistically update status to executing or rejected
    const targetStatus = approve ? "executing" : "rejected";
    set((s) => ({
      error: null,
      resolvingApprovalIds: { ...s.resolvingApprovalIds, [id]: true },
      approvals: s.approvals.map((a) => (a.id === id ? { ...a, status: targetStatus } : a)),
    }));

    try {
      await rtClient.request(Actions.AGENT_RESOLVE_APPROVAL, { id, approve });
      // On success: remove resolved approval from pending list
      set((s) => ({
        approvals: s.approvals.filter((a) => a.id !== id),
      }));
    } catch (err) {
      // On failure: revert optimistic status back to pending
      set((s) => ({
        error: toActionError(err),
        approvals: s.approvals.map((a) => (a.id === id ? { ...a, status: "pending" } : a)),
      }));
    } finally {
      set((s) => {
        const nextResolving = { ...s.resolvingApprovalIds };
        delete nextResolving[id];
        return { resolvingApprovalIds: nextResolving };
      });
    }
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
  };
}
