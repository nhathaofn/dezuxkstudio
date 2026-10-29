import React, { useEffect, useState, Suspense, lazy } from "react";
import { useRealtimeStore } from "./stores/useRealtimeStore";
import { useThemeStore } from "./stores/useThemeStore";
import { AppHeader } from "./components/AppHeader";
import { LoadingSkeleton } from "./components/ui/LoadingSkeleton";
import "./styles/index.css";

// Dynamic Code Splitting with React.lazy
const OverviewView = lazy(() => import("./views/OverviewView"));
const LiveLogView = lazy(() => import("./views/LiveLogView"));

export const App: React.FC = () => {
  const [activeTab, setActiveTab] = useState<"overview" | "logs">("overview");

  // Atomic selectors
  const initRealtime = useRealtimeStore((state) => state.init);
  const runPing = useRealtimeStore((state) => state.runPing);
  const status = useRealtimeStore((state) => state.status);
  const initTheme = useThemeStore((state) => state.initTheme);

  useEffect(() => {
    initTheme();
    initRealtime();

    const interval = setInterval(() => {
      if (status === "connected") {
        runPing();
      }
    }, 3000);

    return () => clearInterval(interval);
  }, [initRealtime, initTheme, runPing, status]);

  return (
    <div className="app-layout">
      <AppHeader activeTab={activeTab} onTabChange={setActiveTab} />
      <main className="app-content">
        <Suspense fallback={<LoadingSkeleton />}>
          {activeTab === "overview" && <OverviewView />}
          {activeTab === "logs" && <LiveLogView />}
        </Suspense>
      </main>
    </div>
  );
};

export default App;
