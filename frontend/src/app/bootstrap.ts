import { Events, NativeEvents } from "../contracts/generated";
import { useAgentCustomizationsStore } from "../features/agent/useAgentCustomizationsStore";
import { useAgentStore } from "../features/agent/useAgentStore";
import { useBinariesStore } from "../features/binaries/useBinariesStore";
import { useProvidersStore } from "../features/providers/useProvidersStore";
import { useConnectionStore } from "../features/connection/useConnectionStore";
import { uiLog } from "../features/logs/useLogStore";
import { useMediaLibraryStore } from "../features/media/useMediaLibraryStore";
import { usePreferencesStore } from "../features/preferences/usePreferencesStore";
import { useSystemStore } from "../features/system/useSystemStore";
import { useStudioStore } from "../features/studio/useStudioStore";
import { useNodeSpecsStore } from "../features/workflow/useNodeSpecs";
import { onNativeEvent } from "../lib/bridge";
import { ActionError, rtClient } from "../lib/rtClient";

let started = false;

/**
 * Wires app-wide subscriptions exactly once (safe under React StrictMode) and
 * starts the connection. Feature data is (re)loaded on every successful connect.
 */
export function bootstrap(): void {
  if (started) return;
  started = true;

  onNativeEvent(NativeEvents.APP_FATAL, (body) => {
    useConnectionStore.getState().setFatal(ActionError.fromBody(body));
  });

  rtClient.on(Events.PREFERENCES_UPDATED, (p) => usePreferencesStore.getState().receive(p));
  rtClient.on(Events.PROVIDERS_CHANGED, (p) => {
    useProvidersStore.getState().receive(p);
    void useAgentStore.getState().fetchProviderLinks();
  });
  rtClient.on(Events.AGENT_STATUS_CHANGED, (p) => useAgentStore.getState().receiveStatus(p));
  rtClient.on(Events.AGENT_LOGIN_PROMPT, (p) => useAgentStore.getState().receiveLoginPrompt(p));
  rtClient.on(Events.AGENT_CHAT_EVENT, (p) => useAgentStore.getState().receiveChatEvent(p));
  rtClient.on(Events.AGENT_APPROVALS, (p) =>
    useAgentStore.getState().receiveApprovals(p.approvals)
  );
  rtClient.on(Events.AGENT_CUSTOMIZATIONS_CHANGED, (p) =>
    useAgentCustomizationsStore.getState().receiveCustomizations(p)
  );
  rtClient.on(Events.STUDIO_PROJECT_CHANGED, (p) =>
    useStudioStore.getState().receiveProjectChanged(p)
  );
  rtClient.on(Events.STUDIO_EPISODE_UPDATED, (p) =>
    useStudioStore.getState().receiveEpisodeUpdated(p)
  );
  rtClient.on(Events.STUDIO_BATCH_PROGRESS, (p) =>
    useStudioStore.getState().receiveBatchProgress(p)
  );
  rtClient.on(Events.STUDIO_QUEUE_UPDATED, (p) => useStudioStore.getState().receiveQueueUpdated(p));
  rtClient.on(Events.STUDIO_COST_UPDATED, (p) => useStudioStore.getState().receiveCostUpdated(p));
  rtClient.on(Events.STUDIO_TIMELINE_CHANGED, (p) =>
    useStudioStore.getState().receiveTimelineChanged(p)
  );
  rtClient.on(Events.MEDIA_ASSETS_CHANGED, () =>
    useMediaLibraryStore.getState().receiveAssetsChanged()
  );
  rtClient.on(Events.BINARIES_PROGRESS, (bp) =>
    useBinariesStore.getState().receiveBinaryProgress(bp)
  );
  let lastProgressLine = "";
  let progressFlushTimer: ReturnType<typeof setTimeout> | null = null;
  rtClient.on(Events.ACTION_PROGRESS, (p) => {
    // Heartbeats only keep a request alive; coalesce non-heartbeat updates to avoid spamming uiLog.
    if (p.heartbeat) return;
    lastProgressLine = `progress ${Math.round(p.percent)}% ${p.message}`;
    if (progressFlushTimer !== null) return;
    progressFlushTimer = setTimeout(() => {
      progressFlushTimer = null;
      if (lastProgressLine) {
        uiLog.info(lastProgressLine);
        lastProgressLine = "";
      }
    }, 150);
  });

  let firstConnect = true;
  rtClient.onStatus((status) => {
    useConnectionStore.setState(
      status === "connected" ? { status } : { status, latencyMs: null }
    );
    uiLog.info(`connection: ${status}`);
    if (status !== "connected") {
      useStudioStore.getState().clearTransientState();
      return;
    }
    void usePreferencesStore.getState().load();
    void useSystemStore.getState().loadInfo();
    // A screen opened before the socket was up (or on a different server) gets fresh node specs.
    void useNodeSpecsStore.getState().load(true);
    void useBinariesStore.getState().fetchBinaries();
    void useProvidersStore.getState().list();
    void useProvidersStore.getState().fetchDefaults();
    void useAgentStore.getState().fetchProviders();
    void useAgentStore.getState().fetchProviderLinks();
    void useAgentStore.getState().fetchHistory();
    void useAgentStore.getState().fetchApprovals();
    void useAgentStore.getState().fetchAllowedDirs();
    void useAgentCustomizationsStore.getState().fetchCustomizations();
    if (!firstConnect) {
      void useStudioStore.getState().resyncAfterReconnect();
    } else if (useStudioStore.getState().needsInitialConnectSync) {
      void useStudioStore.getState().fetchProjects();
    }
    if (firstConnect && useConnectionStore.getState().endpoint?.checkUpdatesOnStartup) {
      void useSystemStore.getState().checkUpdate();
    }
    firstConnect = false;
  });

  void useConnectionStore.getState().connect();
}
