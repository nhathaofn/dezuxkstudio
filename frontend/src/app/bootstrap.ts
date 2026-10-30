import { Events, NativeEvents } from "../contracts/generated";
import { useBinariesStore } from "../features/binaries/useBinariesStore";
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
  rtClient.on(Events.ACTION_PROGRESS, (p) =>
    uiLog.info(`progress ${Math.round(p.percent)}% ${p.message}`)
  );

  let firstConnect = true;
  rtClient.onStatus((status) => {
    uiLog.info(`connection: ${status}`);
    if (status !== "connected") return;
    void usePreferencesStore.getState().load();
    void useSystemStore.getState().loadInfo();
    void useBinariesStore.getState().fetchBinaries();
    if (firstConnect && useConnectionStore.getState().endpoint?.checkUpdatesOnStartup) {
      void useSystemStore.getState().checkUpdate();
    }
    firstConnect = false;
  });

  void useConnectionStore.getState().connect();
}
