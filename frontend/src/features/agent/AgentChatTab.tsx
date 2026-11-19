import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { appConfig } from "../../config/appConfig";
import type { VirtualListHandle } from "../../components/ui/VirtualList";
import { AgentComposer } from "./AgentComposer";
import { AgentHistoryList } from "./AgentHistoryList";
import { AgentMessageList } from "./AgentMessageList";
import { useAgentCustomizationsStore } from "./useAgentCustomizationsStore";
import { useAgentDrawerStore } from "./useAgentDrawerStore";
import { useAgentStore } from "./useAgentStore";

export interface AgentChatTabProps {
  connectedToCore: boolean;
}

const STICK_TO_BOTTOM_PX = 80;
const COMPOSER_MAX_HEIGHT = appConfig.ui.composerMaxHeight;

export const AgentChatTab: React.FC<AgentChatTabProps> = ({ connectedToCore }) => {
  const chatView = useAgentDrawerStore((s) => s.chatView);
  return chatView === "history" ? (
    <AgentHistoryList />
  ) : (
    <AgentChatPane connectedToCore={connectedToCore} />
  );
};

const AgentChatPane: React.FC<AgentChatTabProps> = ({ connectedToCore }) => {
  const providers = useAgentStore((s) => s.providers);
  const conversationId = useAgentStore((s) => s.conversationId);
  const conversations = useAgentStore((s) => s.conversations);
  const messages = useAgentStore((s) => s.messages);
  const approvals = useAgentStore((s) => s.approvals);
  const prompt = useAgentStore((s) => s.prompt);
  const skills = useAgentCustomizationsStore((s) => s.skills);
  const sending = useAgentStore((s) => s.sending);
  const busyAction = useAgentStore((s) => s.busyAction);
  const setPrompt = useAgentStore((s) => s.setPrompt);
  const setActiveTab = useAgentDrawerStore((s) => s.setActiveTab);
  const connectProvider = useAgentStore((s) => s.connectProvider);
  const newChat = useAgentStore((s) => s.newChat);
  const sendPrompt = useAgentStore((s) => s.sendPrompt);
  const cancelPrompt = useAgentStore((s) => s.cancelPrompt);
  const retryLast = useAgentStore((s) => s.retryLast);
  const resolveApproval = useAgentStore((s) => s.resolveApproval);

  const listRef = useRef<VirtualListHandle | null>(null);
  const scrollRafRef = useRef<number | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const stickRef = useRef(true);
  const [showJump, setShowJump] = useState(false);
  const [wrongProviderDismissed, setWrongProviderDismissed] = useState(false);
  const [slashIdx, setSlashIdx] = useState(0);
  const [slashDismissedFor, setSlashDismissedFor] = useState<string | null>(null);
  const activeProvider = providers.find((p) => p.active);
  const convOwnerId = conversationId
    ? (messages.find((m) => m.providerId)?.providerId ??
      conversations.find((c) => c.id === conversationId)?.provider_id)
    : undefined;
  const convOwner = convOwnerId ? providers.find((p) => p.id === convOwnerId) : undefined;
  const wrongProvider = Boolean(activeProvider && convOwnerId && convOwnerId !== activeProvider.id);
  const convOwnerName = convOwner?.name ?? convOwnerId ?? "";
  const canSend =
    connectedToCore && !!activeProvider && !wrongProvider && !!prompt.trim() && !sending;

  const slashQuery = useMemo(() => {
    if (slashDismissedFor !== null && slashDismissedFor === prompt) return null;
    const m = /^\/([a-z0-9_-]*)$/i.exec(prompt);
    return m ? m[1].toLowerCase() : null;
  }, [prompt, slashDismissedFor]);

  const slashSkills = useMemo(() => {
    if (slashQuery === null) return [];
    return skills
      .filter((sk) => !sk.tombstoned && sk.enabled && sk.user_invocable)
      .filter(
        (sk) =>
          !slashQuery ||
          sk.name.toLowerCase().includes(slashQuery) ||
          sk.description.toLowerCase().includes(slashQuery)
      )
      .slice(0, 8);
  }, [skills, slashQuery]);

  const activeSlashIdx = Math.min(slashIdx, Math.max(0, slashSkills.length - 1));

  // Follow new content only while the user is at the bottom; reading older messages is not interrupted.
  const onScroll = useCallback((_scrollTop: number, e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_TO_BOTTOM_PX;
    stickRef.current = atBottom;
    setShowJump(!atBottom);
  }, []);

  const scrollToBottom = useCallback(
    (smooth = false) => {
      const handle = listRef.current;
      const el = handle?.getContainer();
      if (handle && messages.length > 0) {
        handle.scrollToIndex(messages.length - 1, smooth ? "smooth" : "auto", "end");
      }
      if (el) {
        el.scrollTo({ top: el.scrollHeight, behavior: smooth ? "smooth" : "auto" });
      }
      stickRef.current = true;
      setShowJump(false);
    },
    [messages.length]
  );

  useEffect(() => {
    if (!stickRef.current || messages.length === 0) return;
    if (typeof requestAnimationFrame !== "function") {
      scrollToBottom();
      return;
    }
    if (scrollRafRef.current !== null) return;
    scrollRafRef.current = requestAnimationFrame(() => {
      scrollRafRef.current = null;
      if (stickRef.current) {
        scrollToBottom();
      }
    });
  }, [messages, approvals, scrollToBottom]);

  useEffect(() => {
    return () => {
      if (scrollRafRef.current !== null && typeof cancelAnimationFrame === "function") {
        cancelAnimationFrame(scrollRafRef.current);
        scrollRafRef.current = null;
      }
    };
  }, []);

  // Grow the composer with its content.
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, COMPOSER_MAX_HEIGHT)}px`;
  }, [prompt]);

  // Focus the composer when an agent becomes available.
  useEffect(() => {
    if (activeProvider && connectedToCore) inputRef.current?.focus();
  }, [activeProvider?.id, connectedToCore]); // eslint-disable-line react-hooks/exhaustive-deps

  const insertSlashSkill = useCallback(
    (name: string) => {
      setPrompt(`/${name} `);
      setSlashDismissedFor(null);
      setSlashIdx(0);
      inputRef.current?.focus();
    },
    [setPrompt]
  );

  const handlePromptChange = (nextVal: string) => {
    if (slashDismissedFor !== null && nextVal !== slashDismissedFor) {
      setSlashDismissedFor(null);
    }
    setSlashIdx(0);
    setPrompt(nextVal);
  };

  const send = () => {
    if (!canSend) return;
    stickRef.current = true; // a new question always brings the reply into view
    void sendPrompt();
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (slashSkills.length > 0 && !e.nativeEvent.isComposing) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSlashIdx((i) => (i + 1) % slashSkills.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setSlashIdx((i) => (i - 1 + slashSkills.length) % slashSkills.length);
        return;
      }
      if ((e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) && slashSkills[activeSlashIdx]) {
        e.preventDefault();
        insertSlashSkill(slashSkills[activeSlashIdx].name);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setSlashDismissedFor(prompt);
        return;
      }
    }
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      send();
    } else if (e.key === "Escape" && sending) {
      e.preventDefault();
      void cancelPrompt();
    }
  };

  const pickSuggestion = (text: string) => {
    setPrompt(text);
    inputRef.current?.focus();
  };

  const lastMsg = messages[messages.length - 1];
  const canRetry = !sending && !!activeProvider && !wrongProvider && lastMsg?.role === "error";


  return (
    <div className="agent-chat-tab">
      <AgentMessageList
        listRef={listRef}
        providers={providers}
        messages={messages}
        activeProvider={activeProvider}
        sending={sending}
        canRetry={canRetry}
        showJump={showJump}
        onScroll={onScroll}
        onPickSuggestion={pickSuggestion}
        onGoConnections={() => setActiveTab("connections")}
        onRetry={() => void retryLast()}
        onScrollToBottom={() => scrollToBottom(true)}
      />
      <AgentComposer
        connectedToCore={connectedToCore}
        activeProvider={activeProvider}
        convOwner={convOwner}
        wrongProvider={wrongProvider}
        wrongProviderDismissed={wrongProviderDismissed}
        convOwnerName={convOwnerName}
        approvals={approvals}
        prompt={prompt}
        sending={sending}
        busyAction={busyAction}
        canSend={canSend}
        slashSkills={slashSkills}
        activeSlashIdx={activeSlashIdx}
        inputRef={inputRef}
        onDismissWrongProvider={() => setWrongProviderDismissed(true)}
        onSwitchProvider={(id) => void connectProvider(id)}
        onNewChat={() => void newChat()}
        onResolveApproval={(id, approved) => void resolveApproval(id, approved)}
        onPromptChange={handlePromptChange}
        onKeyDown={handleKeyDown}
        onInsertSlashSkill={insertSlashSkill}
        onSend={send}
        onCancel={() => void cancelPrompt()}
      />
    </div>
  );
};
