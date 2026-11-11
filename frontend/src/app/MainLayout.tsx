import React, { Suspense, useEffect } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { LoadingSkeleton } from "../components/ui/LoadingSkeleton";
import { AgentDrawer } from "../features/agent/AgentDrawer";
import { useAgentStore } from "../features/agent/useAgentStore";
import { useScreenContextStore } from "../features/agent/screenContext";
import { useT } from "../lib/i18n";
import { navRoutes, settingsRoute } from "./navigation";
import { useConnectionStore } from "../features/connection/useConnectionStore";
import { AppHeader } from "./AppHeader";
import { StatusBanner } from "./StatusBanner";

export const MainLayout: React.FC = () => {
  const { pathname } = useLocation();
  const connected = useConnectionStore((s) => s.status === "connected");
  const screenContext = useScreenContextStore((s) => s.context);
  const t = useT();

  // Tell the agent what the user is looking at: route, screen name, and whatever the screen published
  // (loaded files, current settings, last result). Debounced so typing in a field does not spam the backend.
  useEffect(() => {
    if (!connected) return;
    const route = [...navRoutes, settingsRoute].find((r) => r.path === pathname);
    const handle = window.setTimeout(() => {
      void useAgentStore.getState().setUIContext({
        route: pathname,
        note: "",
        screen: route ? t(route.label) : undefined,
        ...(screenContext ?? {}),
      });
    }, 300);
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
      </div>
    </div>
  );
};
