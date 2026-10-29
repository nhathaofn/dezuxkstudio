import React from "react";
import { AlertTriangle } from "lucide-react";
import { Card } from "../components/ui/Card";
import { useT } from "../lib/i18n";
import type { ActionError } from "../lib/rtClient";

/** Shown when the core cannot start (bad config, unreadable database, port in use…). */
export const FatalScreen: React.FC<{ error: ActionError }> = ({ error }) => {
  const t = useT();
  return (
    <div className="app-layout">
      <main className="app-content">
        <Card icon={AlertTriangle} title={t("app.fatal.title")} subtitle={t("app.fatal.hint")}>
          <pre className="code-viewer">
            <code>
              [{error.code}] {error.message}
            </code>
          </pre>
        </Card>
      </main>
    </div>
  );
};
