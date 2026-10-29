import React, { useEffect } from "react";
import { HashRouter } from "react-router-dom";
import { useConnectionStore } from "../features/connection/useConnectionStore";
import { bootstrap } from "./bootstrap";
import { FatalScreen } from "./FatalScreen";
import { AppRoutes } from "./routes";

export const App: React.FC = () => {
  const fatal = useConnectionStore((s) => s.fatal);

  useEffect(() => {
    bootstrap();
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
