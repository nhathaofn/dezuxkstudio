import React from "react";
import { ErrorText } from "../components/ui/ErrorText";
import { useConnectionStore } from "../features/connection/useConnectionStore";
import { usePreferencesStore } from "../features/preferences/usePreferencesStore";
import { useT } from "../lib/i18n";

/** App-wide notices: lost connection and failures that would otherwise be invisible. */
export const StatusBanner: React.FC = () => {
  const t = useT();
  const status = useConnectionStore((s) => s.status);
  const resolved = useConnectionStore((s) => s.resolved);
  const hasEndpoint = useConnectionStore((s) => s.endpoint !== null);
  const prefsError = usePreferencesStore((s) => s.error);

  let notice: string | null = null;
  if (status === "reconnecting") notice = t("connection.lost");
  else if (resolved && !hasEndpoint) notice = t("connection.noEndpoint");

  if (!notice && !prefsError) return null;

  return (
    <div className="status-banner">
      {notice && (
        <div className="alert-box" role="status">
          {notice}
        </div>
      )}
      <ErrorText error={prefsError} />
    </div>
  );
};
