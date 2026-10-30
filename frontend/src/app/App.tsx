import React, { useEffect } from "react";
import { HashRouter } from "react-router-dom";
import { useConnectionStore } from "../features/connection/useConnectionStore";
import { usePreferencesStore } from "../features/preferences/usePreferencesStore";
import { isDesktop } from "../lib/bridge";
import { bootstrap } from "./bootstrap";
import { FatalScreen } from "./FatalScreen";
import { AppRoutes } from "./routes";

export const App: React.FC = () => {
  const fatal = useConnectionStore((s) => s.fatal);

  useEffect(() => {
    bootstrap();

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.isComposing || e.keyCode === 229) return;

      const isCtrlOrCmd = e.ctrlKey || e.metaKey;
      const isShift = e.shiftKey;
      const isAlt = e.altKey;
      const isKeyD = e.code === "KeyD" || e.key.toLowerCase() === "d";

      const isCtrlShiftD = isCtrlOrCmd && isShift && !isAlt && isKeyD;
      const isF12 = e.key === "F12" && !isCtrlOrCmd && !isShift && !isAlt;

      if (isCtrlShiftD) {
        e.preventDefault();
        usePreferencesStore.getState().toggleDevMode();
      } else if (isF12) {
        if (isDesktop()) {
          e.preventDefault();
        }
        usePreferencesStore.getState().toggleDevMode();
      }
    };

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  if (fatal) return <FatalScreen error={fatal} />;
  return (
    // HashRouter: the webview serves a single index.html, so path routing would 404 on reload.
    <HashRouter>
      <AppRoutes />
    </HashRouter>
  );
};

export default App;
