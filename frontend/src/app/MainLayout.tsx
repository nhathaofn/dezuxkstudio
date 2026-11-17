import React, { Suspense, useEffect } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { LoadingSkeleton } from "../components/ui/LoadingSkeleton";
import { AgentDrawer } from "../features/agent/AgentDrawer";
import { MediaLibraryDrawer } from "../features/media/MediaLibraryDrawer";
import { useAgentDrawerStore } from "../features/agent/useAgentDrawerStore";
import { useScreenContextStore } from "../features/agent/screenContext";
import { useT } from "../lib/i18n";
import { navRoutes, settingsRoute } from "./navigation";
import { useConnectionStore } from "../features/connection/useConnectionStore";
import { usePreferencesStore } from "../features/preferences/usePreferencesStore";
import { WorkspaceOnboardingDialog } from "../features/preferences/WorkspaceOnboardingDialog";
import { useSystemStore } from "../features/system/useSystemStore";
import { AppHeader } from "./AppHeader";
import { StatusBanner } from "./StatusBanner";
import { appConfig } from "../config/appConfig";

export const MainLayout: React.FC = () => {
  const { pathname } = useLocation();
  const connected = useConnectionStore((s) => s.status === "connected");
  const sysInfo = useSystemStore((s) => s.info);
  const prefsLoaded = usePreferencesStore((s) => s.loaded);
  const onboardingCompleted = usePreferencesStore((s) => s.prefs.onboarding_completed);
  const showOnboarding =
    connected && sysInfo?.mode === "desktop" && prefsLoaded && !onboardingCompleted;
  const screenContext = useScreenContextStore((s) => s.context);
  const t = useT();

  // Tell the agent what the user is looking at: route, screen name, and whatever the screen published
  // (loaded files, current settings, last result). Debounced so typing in a field does not spam the backend.
  useEffect(() => {
    if (!connected) return;
    const route = [...navRoutes, settingsRoute].find((r) => r.path === pathname);
    const handle = window.setTimeout(() => {
      void useAgentDrawerStore.getState().setUIContext({
        route: pathname,
        note: "",
        screen: route ? t(route.label) : undefined,
        ...(screenContext ?? {}),
      });
    }, appConfig.ui.screenContextDebounceMs);
    return () => window.clearTimeout(handle);
  }, [pathname, connected, screenContext, t]);

  return (
    <div className="app-layout">
      <AppHeader />
      <div className="app-workspace">
        <main className="app-content">
          <StatusBanner />
          <Suspense fallback={<LoadingSkeleton />}>
            <Outlet />
          </Suspense>
        </main>
        <AgentDrawer />
        <MediaLibraryDrawer />
        {showOnboarding && <WorkspaceOnboardingDialog />}
      </div>
    </div>
  );
};
