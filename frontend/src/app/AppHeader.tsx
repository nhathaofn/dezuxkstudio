import React from "react";
import { NavLink } from "react-router-dom";
import { Settings } from "lucide-react";
import { StatusIndicator } from "../features/connection/StatusIndicator";
import { useSystemStore } from "../features/system/useSystemStore";
import { useT } from "../lib/i18n";
import { navRoutes, settingsRoute } from "./navigation";

export const AppHeader: React.FC = () => {
  const t = useT();
  const build = useSystemStore((s) => s.info?.build);

  return (
    <header className="app-header">
      <div className="header-left">
        <span className="logo">{build?.product_name ?? document.title}</span>
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
        <NavLink
          to={settingsRoute.path}
          className={({ isActive }) =>
            `ds-button ds-button--sm ${isActive ? "ds-button--primary" : "ds-button--secondary"}`
          }
          title={t(settingsRoute.label)}
          aria-label={t(settingsRoute.label)}
        >
          <Settings size={14} />
        </NavLink>
      </div>
    </header>
  );
};
