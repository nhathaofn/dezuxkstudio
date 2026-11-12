import { Events, NativeEvents } from "../contracts/generated";
import { useAgentStore } from "../features/agent/useAgentStore";
import { useBinariesStore } from "../features/binaries/useBinariesStore";
import { useProvidersStore } from "../features/providers/useProvidersStore";
import { useConnectionStore } from "../features/connection/useConnectionStore";
import { uiLog } from "../features/logs/useLogStore";
import { usePreferencesStore } from "../features/preferences/usePreferencesStore";
import { useSystemStore } from "../features/system/useSystemStore";
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
  rtClient.on(Events.PROVIDERS_CHANGED, (p) => useProvidersStore.getState().receive(p));
  rtClient.on(Events.AGENT_STATUS_CHANGED, (p) => useAgentStore.getState().receiveStatus(p));
  rtClient.on(Events.AGENT_CHAT_EVENT, (p) => useAgentStore.getState().receiveChatEvent(p));
  rtClient.on(Events.AGENT_APPROVALS, (p) =>
    useAgentStore.getState().receiveApprovals(p.approvals)
  );
  rtClient.on(Events.ACTION_PROGRESS, (p) => {
    // Heartbeats only keep a request alive; logging them would flood the live log buffer.
    if (!p.heartbeat) uiLog.info(`progress ${Math.round(p.percent)}% ${p.message}`);
  });

  let firstConnect = true;
  rtClient.onStatus((status) => {
    uiLog.info(`connection: ${status}`);
    if (status !== "connected") return;
    void usePreferencesStore.getState().load();
    void useSystemStore.getState().loadInfo();
    void useBinariesStore.getState().fetchBinaries();
    void useProvidersStore.getState().list();
    void useProvidersStore.getState().fetchDefaults();
    void useAgentStore.getState().fetchProviders();
    void useAgentStore.getState().fetchHistory();
    void useAgentStore.getState().fetchApprovals();
    void useAgentStore.getState().fetchAllowedDirs();
    if (firstConnect && useConnectionStore.getState().endpoint?.checkUpdatesOnStartup) {
      void useSystemStore.getState().checkUpdate();
    }
    firstConnect = false;
  });

  void useConnectionStore.getState().connect();
}
