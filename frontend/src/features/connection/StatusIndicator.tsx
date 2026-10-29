import React from "react";
import { useT } from "../../lib/i18n";
import { useConnectionStore } from "./useConnectionStore";

export const StatusIndicator: React.FC = () => {
  const t = useT();
  const status = useConnectionStore((s) => s.status);
  return (
    <div className="status-indicator" aria-live="polite">
      <span className={`status-dot status-dot--${status}`} />
      <span className="status-label">{t(`status.${status}`)}</span>
    </div>
  );
};
