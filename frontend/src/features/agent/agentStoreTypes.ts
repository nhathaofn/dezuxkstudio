import { appConfig } from "../../config/appConfig";
import type {
  AgentApprovalRequest,
  AgentChatEvent,
  AgentConversation,
  AgentHistoryResult,
  AgentLoginPrompt,
  AgentProviderID,
  AgentProviderInfo,
  AgentProviderLinkItem,
  SubagentInvocation,
} from "../../contracts/generated";
import type { ActionError } from "../../lib/rtClient";

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
export function historyState(res: AgentHistoryResult): Pick<AgentState, "messages" | "conversationId"> {
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

export interface AgentState {
  providers: AgentProviderInfo[];
  developerMode: boolean;
  approvals: AgentApprovalRequest[];
  resolvingApprovalIds: Record<string, boolean>;
  allowedDirs: string[];
  providerLinks: Record<string, AgentProviderLinkItem>;
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
  fetchProviderLinks: () => Promise<void>;
  linkProvider: (agentProviderId: string, providerId: string, credentialId: string) => Promise<void>;
  unlinkProvider: (agentProviderId: string) => Promise<void>;
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

export type AgentSet = (
  partial: Partial<AgentState> | ((s: AgentState) => Partial<AgentState>)
) => void;
export type AgentGet = () => AgentState;
