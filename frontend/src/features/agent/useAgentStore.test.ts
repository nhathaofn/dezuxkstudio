import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Actions, type AgentProviderInfo } from "../../contracts/generated";
import { ActionError, rtClient } from "../../lib/rtClient";
import { clampDrawerWidth, finishRunningTools, useAgentStore } from "./useAgentStore";

const mockProviders: AgentProviderInfo[] = [
  {
    id: "antigravity",
    name: "Google Antigravity",
    executable: "agy",
    resolved_path: "C:\\tools\\agy.exe",
    custom_path: "",
    source: "system",
    version: "1.0.0",
    state: "disconnected",
    supported: true,
    unsupported_reason: "",
    auth_state: "unknown",
    account_info: "",
    isolated_profile_dir: "C:\\data\\agent-profiles\\antigravity",
    protocol_version: 0,
    agent_name: "",
    agent_version: "",
    last_error: "",
    active: false,
    busy: false,
    models: [],
    current_model: "",
    selected_model: "",
    selected_effort: "",
    revision: 1,
    installing: false,
    install_percent: 0,
  },
  {
    id: "codex",
    name: "OpenAI Codex",
    executable: "codex",
    resolved_path: "",
    custom_path: "",
    source: "none",
    version: "",
    state: "missing",
    supported: true,
    unsupported_reason: "",
    auth_state: "unauthenticated",
    account_info: "",
    isolated_profile_dir: "C:\\data\\agent-profiles\\codex",
    protocol_version: 0,
    agent_name: "",
    agent_version: "",
    last_error: "",
    active: false,
    busy: false,
    models: [],
    current_model: "",
    selected_model: "",
    selected_effort: "",
    revision: 1,
    installing: false,
    install_percent: 0,
  },
];

describe("useAgentStore", () => {
  beforeEach(() => {
    useAgentStore.setState({
      panelOpen: false,
      panelWidth: 420,
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
    });
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("toggles drawer open state and clamps drawer width", () => {
    const store = useAgentStore.getState();
    expect(store.panelOpen).toBe(false);
    store.togglePanel();
    expect(useAgentStore.getState().panelOpen).toBe(true);
    store.closePanel();
    expect(useAgentStore.getState().panelOpen).toBe(false);

    expect(clampDrawerWidth(100)).toBe(340);
    expect(clampDrawerWidth(9999)).toBe(680);
    expect(clampDrawerWidth(Number.NaN)).toBe(420);
  });

  it("unlocks a chat stuck in 'sending' when a reconnect shows no turn is running", async () => {
    useAgentStore.setState({
      sending: true,
      messages: [
        { id: "m1", role: "assistant", text: "partial", providerId: "codex", streaming: true },
      ],
    });
    vi.spyOn(rtClient, "request").mockResolvedValueOnce({ providers: mockProviders });
    await useAgentStore.getState().fetchProviders();
    const s = useAgentStore.getState();
    expect(s.sending).toBe(false);
    expect(s.messages[0].streaming).toBe(false);
  });

  it("keeps 'sending' while the server still reports a busy provider", async () => {
    useAgentStore.setState({ sending: true, messages: [] });
    vi.spyOn(rtClient, "request").mockResolvedValueOnce({
      providers: [{ ...mockProviders[0], active: true, busy: true }, mockProviders[1]],
    });
    await useAgentStore.getState().fetchProviders();
    expect(useAgentStore.getState().sending).toBe(true);
  });

  it("fetches providers and connects to selected provider", async () => {
    vi.spyOn(rtClient, "request").mockResolvedValueOnce({ providers: mockProviders });
    await useAgentStore.getState().fetchProviders();
    expect(useAgentStore.getState().providers).toHaveLength(2);

    const connected: AgentProviderInfo = {
      ...mockProviders[0],
      state: "connected",
      active: true,
      protocol_version: 1,
    };
    useAgentStore.getState().setActiveTab("connections");
    vi.spyOn(rtClient, "request").mockResolvedValueOnce(connected);

    await useAgentStore.getState().connectProvider("antigravity");
    const state = useAgentStore.getState();
    expect(state.activeTab).toBe("chat");
    expect(state.providers.find((p) => p.id === "antigravity")?.active).toBe(true);
  });

  it("streams chat deltas, thoughts, tool calls, and handles early events", async () => {
    useAgentStore.setState({
      providers: [{ ...mockProviders[0], state: "connected", active: true }],
      prompt: "What can you do?",
    });

    let resolvePrompt!: (v: { message_id: string; provider_id: "antigravity" }) => void;
    vi.spyOn(rtClient, "request").mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePrompt = resolve;
        })
    );

    const sendPromise = useAgentStore.getState().sendPrompt();
    expect(useAgentStore.getState().sending).toBe(true);
    expect(useAgentStore.getState().prompt).toBe("");
    expect(useAgentStore.getState().messages).toHaveLength(2);

    useAgentStore.getState().receiveChatEvent({
      type: "thought",
      provider_id: "antigravity",
      message_id: "msg-42",
      text: "planning...",
      error: "",
    });
    useAgentStore.getState().receiveChatEvent({
      type: "tool_call",
      provider_id: "antigravity",
      message_id: "msg-42",
      tool: "studio_list_capabilities",
      text: "studio_list_capabilities",
      error: "",
    });
    useAgentStore.getState().receiveChatEvent({
      type: "tool_result",
      provider_id: "antigravity",
      message_id: "msg-42",
      tool: "studio_list_capabilities",
      text: "completed",
      error: "",
    });
    useAgentStore.getState().receiveChatEvent({
      type: "delta",
      provider_id: "antigravity",
      message_id: "msg-42",
      text: "I can help with DezuxkStudio features!",
      error: "",
    });

    resolvePrompt({ message_id: "msg-42", provider_id: "antigravity" });
    await sendPromise;

    useAgentStore.getState().receiveChatEvent({
      type: "done",
      provider_id: "antigravity",
      message_id: "msg-42",
      text: "",
      error: "",
    });

    const msgs = useAgentStore.getState().messages;
    expect(msgs).toHaveLength(2);
    expect(msgs[1].id).toBe("msg-42");
    expect(msgs[1].thought).toBe("planning...");
    expect(msgs[1].tools).toEqual([{ name: "studio_list_capabilities", status: "done" }]);
    expect(msgs[1].text).toBe("I can help with DezuxkStudio features!");
    expect(msgs[1].streaming).toBe(false);
    expect(useAgentStore.getState().sending).toBe(false);
  });

  it.each([
    ["fetchHistory", "agent history unavailable"],
    ["fetchApprovals", "approval queue unavailable"],
    ["fetchAllowedDirs", "allowed dirs unavailable"],
  ] as const)("%s surfaces a load failure instead of swallowing it", async (action, msg) => {
    useAgentStore.setState({ error: null });
    vi.spyOn(rtClient, "request").mockRejectedValueOnce(new ActionError("ERR_UNAVAILABLE", msg));
    await useAgentStore.getState()[action]();
    expect(useAgentStore.getState().error?.message).toContain(msg);
    useAgentStore.getState().clearError();
    expect(useAgentStore.getState().error).toBeNull();
  });

  it("converts failed sendPrompt RPC into an error bubble", async () => {
    useAgentStore.setState({
      providers: [{ ...mockProviders[0], state: "connected", active: true }],
      prompt: "Will fail",
    });

    vi.spyOn(rtClient, "request").mockRejectedValueOnce(
      new ActionError("ERR_UNAVAILABLE", "agent offline")
    );

    await useAgentStore.getState().sendPrompt();
    const state = useAgentStore.getState();
    expect(state.sending).toBe(false);
    expect(state.messages[1].role).toBe("error");
    expect(state.messages[1].text).toContain("agent offline");
  });

  it("starts a new chat on the server, keeping history", async () => {
    useAgentStore.setState({
      providers: [{ ...mockProviders[0], state: "connected", active: true }],
      messages: [{ id: "m1", role: "user", text: "test" }],
    });

    const reqSpy = vi.spyOn(rtClient, "request").mockResolvedValueOnce({});
    await useAgentStore.getState().newChat();

    expect(useAgentStore.getState().messages).toEqual([]);
    expect(useAgentStore.getState().conversationId).toBe("");
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_NEW_CONVERSATION, {});
  });
});

describe("retryLast and newChat", () => {
  beforeEach(() => {
    useAgentStore.setState({
      providers: [{ ...mockProviders[0], active: true, state: "connected" }],
      messages: [
        { id: "u1", role: "user", text: "first" },
        { id: "a1", role: "assistant", text: "ok" },
        { id: "u2", role: "user", text: "translate this" },
        { id: "e2", role: "error", text: "timed out" },
      ],
      sending: false,
      prompt: "",
      error: null,
    });
  });
  afterEach(() => vi.restoreAllMocks());

  it("re-sends the last user message and drops the failed exchange", async () => {
    const req = vi
      .spyOn(rtClient, "request")
      .mockResolvedValue({ message_id: "msg-9", provider_id: "antigravity" } as never);
    await useAgentStore.getState().retryLast();
    expect(req).toHaveBeenCalledWith(Actions.AGENT_SEND_PROMPT, { prompt: "translate this" });
    const texts = useAgentStore.getState().messages.map((m) => `${m.role}:${m.text}`);
    expect(texts).toEqual(["user:first", "assistant:ok", "user:translate this", "assistant:"]);
  });

  it("keeps the conversation when the server refuses to clear it", async () => {
    vi.spyOn(rtClient, "request").mockRejectedValue(new ActionError("ERR_CONFLICT", "busy"));
    await useAgentStore.getState().newChat();
    expect(useAgentStore.getState().messages).toHaveLength(4);
    expect(useAgentStore.getState().error?.code).toBe("ERR_CONFLICT");
  });
});

describe("conversations", () => {
  afterEach(() => vi.restoreAllMocks());

  it("shows the agent panel when the app starts", () => {
    expect(useAgentStore.getInitialState().panelOpen).toBe(true);
    expect(useAgentStore.getInitialState().conversationId).toBe("");
  });

  it("opens an earlier chat as the current one", async () => {
    useAgentStore.setState({ chatView: "history", messages: [], conversationId: "" });
    vi.spyOn(rtClient, "request").mockResolvedValue({
      conversation_id: "c1",
      messages: [{ id: "m1", provider_id: "codex", role: "user", text: "earlier", created_at: 1 }],
    } as never);
    await useAgentStore.getState().openConversation("c1");
    const s = useAgentStore.getState();
    expect(s.conversationId).toBe("c1");
    expect(s.chatView).toBe("chat");
    expect(s.messages.map((m) => m.text)).toEqual(["earlier"]);
  });

  it("deleting the open chat leaves a new, empty one", async () => {
    useAgentStore.setState({
      conversationId: "c1",
      messages: [{ id: "m1", role: "user", text: "x" }],
      conversations: [
        {
          id: "c1",
          title: "a",
          provider_id: "codex",
          created_at: 1,
          updated_at: 1,
          message_count: 1,
        },
        {
          id: "c2",
          title: "b",
          provider_id: "codex",
          created_at: 1,
          updated_at: 1,
          message_count: 1,
        },
      ],
    });
    const req = vi.spyOn(rtClient, "request").mockResolvedValue({} as never);
    await useAgentStore.getState().deleteConversation("c1");
    expect(req).toHaveBeenCalledWith(Actions.AGENT_DELETE_CONVERSATION, { id: "c1" });
    const s = useAgentStore.getState();
    expect(s.conversations.map((c) => c.id)).toEqual(["c2"]);
    expect(s.conversationId).toBe("");
    expect(s.messages).toEqual([]);
  });

  it("clears an active conversation when connecting a different provider", async () => {
    useAgentStore.setState({
      providers: mockProviders,
      activeTab: "connections",
      conversationId: "c-codex",
      messages: [{ id: "m1", role: "user", text: "codex prompt", providerId: "codex" }],
      approvals: [
        {
          id: "ap-1",
          provider_id: "codex",
          tool: "workspace_write_file",
          title: "Write",
          summary: "Write file",
          created_at: "2026-01-01T00:00:00Z",
        },
      ],
    });
    const connectedClaude = {
      ...mockProviders[0],
      id: "claude" as const,
      name: "Claude Code",
      state: "connected" as const,
      active: true,
    };
    vi.spyOn(rtClient, "request").mockResolvedValueOnce(connectedClaude);

    await useAgentStore.getState().connectProvider("claude");
    const s = useAgentStore.getState();
    expect(s.activeTab).toBe("chat");
    expect(s.conversationId).toBe("");
    expect(s.messages).toEqual([]);
    expect(s.approvals).toEqual([]);
  });

  it("preserves the open conversation when reconnecting its own provider", async () => {
    useAgentStore.setState({
      providers: mockProviders,
      activeTab: "connections",
      conversationId: "c-codex",
      messages: [{ id: "m1", role: "user", text: "codex prompt", providerId: "codex" }],
    });
    const connectedCodex = {
      ...mockProviders[0],
      id: "codex" as const,
      name: "Codex CLI",
      state: "connected" as const,
      active: true,
    };
    vi.spyOn(rtClient, "request").mockResolvedValueOnce(connectedCodex);

    await useAgentStore.getState().connectProvider("codex");
    const s = useAgentStore.getState();
    expect(s.conversationId).toBe("c-codex");
    expect(s.messages.map((m) => m.id)).toEqual(["m1"]);
  });
});

describe("tool spinners", () => {
  const evt = (type: "tool_call" | "tool_result" | "done" | "error", tool = "", error = "") => ({
    type,
    provider_id: "antigravity" as const,
    message_id: "m-tools",
    text: tool,
    tool,
    error,
  });

  beforeEach(() => {
    useAgentStore.setState({ messages: [], sending: true });
  });

  it("ends a running tool even when the result is titled differently", () => {
    const s = useAgentStore.getState();
    s.receiveChatEvent(evt("tool_call", "dezuxk/workflow_run_node"));
    s.receiveChatEvent(evt("tool_result", "Run Watermark Remover"));
    const tools = useAgentStore.getState().messages[0].tools ?? [];
    expect(tools).toHaveLength(1);
    expect(tools[0].status).toBe("done");
  });

  it("stops every spinner when the reply is done", () => {
    const s = useAgentStore.getState();
    s.receiveChatEvent(evt("tool_call", "a"));
    s.receiveChatEvent(evt("tool_call", "b"));
    s.receiveChatEvent(evt("done"));
    const msg = useAgentStore.getState().messages[0];
    expect(msg.tools?.every((t) => t.status === "done")).toBe(true);
    expect(msg.streaming).toBe(false);
  });

  it("marks running tools as failed when the reply errors", () => {
    const s = useAgentStore.getState();
    s.receiveChatEvent(evt("tool_call", "a"));
    s.receiveChatEvent(evt("error", "", "boom"));
    expect(useAgentStore.getState().messages[0].tools?.[0].status).toBe("error");
  });

  it("finishRunningTools leaves finished tools alone", () => {
    const tools = [
      { name: "x", status: "error" as const },
      { name: "y", status: "running" as const },
    ];
    expect(finishRunningTools(tools, "done")).toEqual([
      { name: "x", status: "error" },
      { name: "y", status: "done" },
    ]);
    expect(finishRunningTools(undefined, "done")).toBeUndefined();
  });
});

describe("provider status snapshots", () => {
  const base = mockProviders[1];
  const connectedWithModels = {
    ...base,
    state: "connected" as const,
    active: true,
    models: [{ id: "gpt-x", name: "GPT X", description: "", efforts: [], default_effort: "" }],
    revision: 20,
  };
  const staleListSnapshot = {
    ...base,
    state: "connected" as const,
    active: true,
    models: [],
    revision: 10,
  };

  beforeEach(() => {
    useAgentStore.setState({ providers: [] });
  });

  it("a late agent:list answer does not overwrite a newer event with models", async () => {
    useAgentStore.getState().receiveStatus(connectedWithModels);
    vi.spyOn(rtClient, "request").mockResolvedValueOnce({ providers: [staleListSnapshot] });
    await useAgentStore.getState().fetchProviders();
    const codex = useAgentStore.getState().providers.find((p) => p.id === "codex");
    expect(codex?.models).toHaveLength(1);
  });

  it("a stale event is ignored too, and does not demote the active provider", () => {
    const active = { ...connectedWithModels, id: "claude" as const, revision: 30 };
    useAgentStore.getState().receiveStatus(active);
    useAgentStore
      .getState()
      .receiveStatus({ ...staleListSnapshot, id: "claude" as const, revision: 5 });
    expect(useAgentStore.getState().providers.find((p) => p.id === "claude")?.models).toHaveLength(
      1
    );
  });

  it("a newer snapshot replaces an older one", () => {
    useAgentStore.getState().receiveStatus(staleListSnapshot);
    useAgentStore.getState().receiveStatus(connectedWithModels);
    expect(useAgentStore.getState().providers.find((p) => p.id === "codex")?.models).toHaveLength(
      1
    );
  });

  it("tracks developer_mode returned by agent:list", async () => {
    useAgentStore.setState({ developerMode: false });
    vi.spyOn(rtClient, "request").mockResolvedValueOnce({
      providers: [connectedWithModels],
      developer_mode: true,
    });
    await useAgentStore.getState().fetchProviders();
    expect(useAgentStore.getState().developerMode).toBe(true);
  });
});

describe("subagent events and customizations", () => {
  afterEach(() => vi.restoreAllMocks());

  it("aggregates subagent lifecycle events into the streaming assistant message", () => {
    useAgentStore.setState({
      messages: [
        {
          id: "pending-1",
          role: "assistant",
          text: "",
          thought: "",
          tools: [],
          subagents: [],
          streaming: true,
        },
      ],
      sending: true,
    });

    const s = useAgentStore.getState();
    s.receiveChatEvent({
      message_id: "msg-10",
      provider_id: "antigravity",
      type: "subagent_start",
      text: "",
      error: "",
      subagent_id: "sub-1",
      subagent_name: "workspace-reviewer",
      subagent_provider: "claude",
    });
    s.receiveChatEvent({
      message_id: "msg-10",
      provider_id: "antigravity",
      type: "subagent_tool_call",
      text: "workspace_read_file",
      error: "",
      tool: "workspace_read_file",
      subagent_id: "sub-1",
      subagent_name: "workspace-reviewer",
    });
    s.receiveChatEvent({
      message_id: "msg-10",
      provider_id: "antigravity",
      type: "subagent_tool_result",
      text: "ok",
      error: "",
      tool: "workspace_read_file",
      subagent_id: "sub-1",
      subagent_name: "workspace-reviewer",
    });
    s.receiveChatEvent({
      message_id: "msg-10",
      provider_id: "antigravity",
      type: "subagent_delta",
      text: "Reviewed ",
      error: "",
      subagent_id: "sub-1",
    });
    s.receiveChatEvent({
      message_id: "msg-10",
      provider_id: "antigravity",
      type: "subagent_delta",
      text: "cleanly.",
      error: "",
      subagent_id: "sub-1",
    });
    s.receiveChatEvent({
      message_id: "msg-10",
      provider_id: "antigravity",
      type: "subagent_done",
      text: "",
      error: "",
      subagent_id: "sub-1",
    });
    s.receiveChatEvent({
      message_id: "msg-10",
      provider_id: "antigravity",
      type: "done",
      text: "",
      error: "",
    });

    const msg = useAgentStore.getState().messages[0];
    expect(msg.id).toBe("msg-10");
    expect(msg.streaming).toBe(false);
    expect(msg.subagents).toEqual([
      {
        id: "sub-1",
        name: "workspace-reviewer",
        provider: "claude",
        status: "done",
        text: "Reviewed cleanly.",
        tools: ["workspace_read_file"],
      },
    ]);
  });

  it("finalizes still-running subagents on error and restores history subagents", async () => {
    useAgentStore.setState({ messages: [], sending: true });
    const s = useAgentStore.getState();
    s.receiveChatEvent({
      message_id: "msg-11",
      provider_id: "codex",
      type: "subagent_start",
      text: "",
      error: "",
      subagent_id: "sub-2",
      subagent_name: "content-drafter",
      subagent_provider: "codex",
    });
    s.receiveChatEvent({
      message_id: "msg-11",
      provider_id: "codex",
      type: "error",
      text: "",
      error: "turn aborted",
    });
    expect(useAgentStore.getState().messages[0].subagents?.[0].status).toBe("error");

    vi.spyOn(rtClient, "request").mockResolvedValueOnce({
      conversation_id: "c-sub",
      messages: [
        {
          id: "m-sub",
          provider_id: "claude",
          role: "assistant",
          text: "Summary",
          created_at: 2,
          subagents: [
            {
              id: "sub-h1",
              name: "workspace-reviewer",
              provider: "claude",
              status: "done",
              text: "All good",
              tools: ["workspace_list_dir"],
            },
          ],
        },
      ],
    } as never);
    await useAgentStore.getState().openConversation("c-sub");
    expect(useAgentStore.getState().messages[0].subagents?.[0].name).toBe("workspace-reviewer");
    expect(useAgentStore.getState().messages[0].subagents?.[0].tools).toEqual([
      "workspace_list_dir",
    ]);
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
    await useAgentStore.getState().fetchCustomizations();
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_LIST_CUSTOMIZATIONS, {});
    expect(useAgentStore.getState().customizationsDir).toBe(snap.dir);
    expect(useAgentStore.getState().skills).toHaveLength(1);
    expect(useAgentStore.getState().subagents).toHaveLength(1);
    expect(useAgentStore.getState().availableSubagentTools).toEqual([
      "workspace_read_file",
      "workspace_list_dir",
    ]);

    const okSkill = await useAgentStore.getState().saveSkill({
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

    await useAgentStore.getState().deleteSkill("custom-skill");
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_DELETE_SKILL, { name: "custom-skill" });
    await useAgentStore.getState().restoreSkill("brief-structuring");
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_RESTORE_SKILL, {
      name: "brief-structuring",
    });

    const okSub = await useAgentStore.getState().saveSubagent({
      name: "custom-sub",
      description: "Sub",
      skills: [],
      tools: ["workspace_read_file"],
      enabled: true,
      body: "Sub body",
    });
    expect(okSub).toBe(true);
    await useAgentStore.getState().deleteSubagent("custom-sub");
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_DELETE_SUBAGENT, { name: "custom-sub" });
    await useAgentStore.getState().restoreSubagent("workspace-reviewer");
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_RESTORE_SUBAGENT, {
      name: "workspace-reviewer",
    });
  });
});
