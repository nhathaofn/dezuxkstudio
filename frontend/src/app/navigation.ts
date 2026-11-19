import type React from "react";
import {
  Clapperboard,
  Film,
  Image as ImageIcon,
  LayoutDashboard,
  Sparkles,
  Volume2,
  type LucideIcon,
} from "lucide-react";
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
    path: "/studio",
    label: "nav.studio",
    icon: Clapperboard,
    load: () => import("../features/studio/StudioView"),
  },
  {
    path: "/watermark",
    label: "nav.watermark",
    icon: Sparkles,
    load: () => import("../features/watermark/WatermarkView"),
  },
  {
    path: "/image",
    label: "nav.image",
    icon: ImageIcon,
    load: () => import("../features/mediagen/ImageGenView"),
  },
  {
    path: "/video",
    label: "nav.video",
    icon: Film,
    load: () => import("../features/mediagen/VideoGenView"),
  },
  {
    path: "/tts",
    label: "nav.tts",
    icon: Volume2,
    load: () => import("../features/mediagen/TTSGenView"),
  },
];

export const settingsRoute: Omit<NavRoute, "icon"> = {
  path: "/settings",
  label: "header.settings",
  load: () => import("../features/preferences/SettingsView"),
};

/** State payload passed when performing 1-click cross-actions into /watermark */
export interface WatermarkNavState {
  asset_id?: string;
  path?: string;
}
