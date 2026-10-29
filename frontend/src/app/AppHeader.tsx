import React from "react";
import { NavLink } from "react-router-dom";
import { Badge } from "../components/ui/Badge";
import { LatencyBadge } from "../features/connection/LatencyBadge";
import { StatusIndicator } from "../features/connection/StatusIndicator";
import { LanguageSelect, ThemeToggle } from "../features/preferences/PreferenceControls";
import { useSystemStore } from "../features/system/useSystemStore";
import { useT } from "../lib/i18n";
import { navRoutes } from "./navigation";

export const AppHeader: React.FC = () => {
  const t = useT();
  const build = useSystemStore((s) => s.info?.build);

  return (
    <header className="app-header">
      <div className="header-left">
        <span className="logo">{build?.product_name ?? document.title}</span>
        {build && <Badge>v{build.version}</Badge>}
        <nav className="header-nav" aria-label="Main">
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
        <LatencyBadge />
        <LanguageSelect />
        <ThemeToggle />
      </div>
    </header>
  );
};
