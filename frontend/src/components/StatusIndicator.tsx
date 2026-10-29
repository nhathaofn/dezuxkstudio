import React from "react";
import type { ConnectionStatus } from "../schemas/realtime.schema";

interface Props {
  status: ConnectionStatus;
}

export const StatusIndicator: React.FC<Props> = ({ status }) => {
  return (
    <div className="status-indicator">
      <span className={`status-dot status-dot--${status}`} />
      <span className="status-label">{status}</span>
    </div>
  );
};
