import React from "react";
import { LayoutDashboard, Terminal, Sun, Moon } from "lucide-react";
import { useRealtimeStore } from "../stores/useRealtimeStore";
import { useThemeStore } from "../stores/useThemeStore";
import { StatusIndicator } from "./StatusIndicator";
import { LatencyBadge } from "./LatencyBadge";
import { Badge } from "./ui/Badge";
import { Button } from "./ui/Button";

interface AppHeaderProps {
  activeTab: "overview" | "logs";
  onTabChange: (tab: "overview" | "logs") => void;
}

export const AppHeader: React.FC<AppHeaderProps> = ({ activeTab, onTabChange }) => {
  // Atomic selectors for zero unnecessary re-renders
  const status = useRealtimeStore((state) => state.status);
  const latency = useRealtimeStore((state) => state.latency);
  const theme = useThemeStore((state) => state.theme);
  const toggleTheme = useThemeStore((state) => state.toggleTheme);

  return (
    <header className="app-header">
      <div className="header-left">
        <span className="logo">DezuxkStudio</span>
        <Badge>Studio v1.0.0</Badge>
        <div className="header-nav">
          <Button
            variant={activeTab === "overview" ? "primary" : "secondary"}
            size="sm"
            onClick={() => onTabChange("overview")}
          >
            <LayoutDashboard size={14} style={{ marginRight: 6, verticalAlign: "text-bottom" }} />
            Overview
          </Button>
          <Button
            variant={activeTab === "logs" ? "primary" : "secondary"}
            size="sm"
            onClick={() => onTabChange("logs")}
          >
            <Terminal size={14} style={{ marginRight: 6, verticalAlign: "text-bottom" }} />
            Live Logs
          </Button>
        </div>
      </div>

      <div className="header-right">
        <StatusIndicator status={status} />
        <LatencyBadge latency={latency} />
        <Button variant="secondary" size="sm" onClick={toggleTheme} title="Toggle theme">
          {theme === "light" ? <Moon size={14} /> : <Sun size={14} />}
        </Button>
      </div>
    </header>
  );
};
