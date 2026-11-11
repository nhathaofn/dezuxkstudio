import React from "react";
import { NavLink } from "react-router-dom";
import { Bot, Settings } from "lucide-react";
import { useAgentStore } from "../features/agent/useAgentStore";
import { StatusIndicator } from "../features/connection/StatusIndicator";
import { useSystemStore } from "../features/system/useSystemStore";
import { useT } from "../lib/i18n";
import { navRoutes, settingsRoute } from "./navigation";

export const AppHeader: React.FC = () => {
  const t = useT();
  const build = useSystemStore((s) => s.info?.build);
  const agentOpen = useAgentStore((s) => s.panelOpen);
  const toggleAgentPanel = useAgentStore((s) => s.togglePanel);
  const agentOnline = useAgentStore((s) => s.providers.some((p) => p.active));

  return (
    <header className="app-header">
      <div className="app-header-inner">
        <div className="header-left">
          <span className="logo">{build?.product_name ?? document.title}</span>
          <nav className="header-nav" aria-label={t("header.nav.ariaLabel")}>
            {navRoutes.map(({ path, label, icon: Icon }) => (
              <NavLink
                key={path}
                to={path}
                end
                className={({ isActive }) =>
                  `ds-button ds-button--sm ${isActive ? "ds-button--primary" : "ds-button--secondary"}`
                }
              >
                <Icon size={14} /> {t(label)}
              </NavLink>
            ))}
          </nav>
        </div>
        <div className="header-right">
          <StatusIndicator />
          <button
            type="button"
            className={`ds-button ds-button--sm ds-button--icon agent-bot-btn ${agentOpen ? "ds-button--primary" : "ds-button--secondary"}`}
            onClick={toggleAgentPanel}
            title={t(agentOpen ? "header.closeAgent" : "header.openAgent")}
            aria-label={t(agentOpen ? "header.closeAgent" : "header.openAgent")}
            aria-expanded={agentOpen}
          >
            <Bot size={14} />
            {agentOnline && <span className="agent-bot-btn__dot" aria-hidden="true" />}
          </button>
          <NavLink
            to={settingsRoute.path}
            className={({ isActive }) =>
              `ds-button ds-button--sm ds-button--icon ${isActive ? "ds-button--primary" : "ds-button--secondary"}`
            }
            title={t(settingsRoute.label)}
            aria-label={t(settingsRoute.label)}
          >
            <Settings size={14} />
          </NavLink>
        </div>
      </div>
    </header>
  );
};
