import type React from "react";
import { LayoutDashboard, Terminal, Wrench, type LucideIcon } from "lucide-react";
import type { MessageKey } from "../lib/i18n";

export interface NavRoute {
  path: string;
  label: MessageKey;
  icon: LucideIcon;
  /** Code-split page module; its default export is the page component. */
  load: () => Promise<{ default: React.ComponentType }>;
}

/** Single route table: drives both the router and the header navigation. */
export const navRoutes: NavRoute[] = [
  {
    path: "/",
    label: "nav.overview",
    icon: LayoutDashboard,
    load: () => import("../features/system/OverviewView"),
  },
  {
    path: "/logs",
    label: "nav.logs",
    icon: Terminal,
    load: () => import("../features/logs/LiveLogView"),
  },
  {
    path: "/tools",
    label: "nav.tools",
    icon: Wrench,
    load: () => import("../features/binaries/BinariesView"),
  },
];
