import { create } from "zustand";
import { createAgentChatSlice } from "./agentChatSlice";
import { createAgentConnectionSlice } from "./agentConnectionSlice";
import type { AgentState } from "./agentStoreTypes";

export {
  COPY_FEEDBACK_MS,
  finishRunningTools,
  newerInfo,
  type AgentChatMessage,
  type AgentToolCallItem,
} from "./agentStoreTypes";

export const useAgentStore = create<AgentState>((set, get) => ({
  providers: [],
  developerMode: false,
  approvals: [],
  resolvingApprovalIds: {},
  allowedDirs: [],
  providerLinks: {},
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

  ...createAgentConnectionSlice(set, get),
  ...createAgentChatSlice(set, get),
}));
