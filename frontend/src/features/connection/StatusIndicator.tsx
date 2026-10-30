import React, { useEffect } from "react";
import { appConfig } from "../../config/appConfig";
import { useT } from "../../lib/i18n";
import { rtClient } from "../../lib/rtClient";
import { useConnectionStore } from "./useConnectionStore";

export const StatusIndicator: React.FC = () => {
  const t = useT();
  const status = useConnectionStore((s) => s.status);
  const latency = useConnectionStore((s) => s.latencyMs);
  const setLatency = useConnectionStore((s) => s.setLatency);

  useEffect(() => {
    if (status !== "connected") return;
    let alive = true;
    const probe = () =>
      rtClient
        .ping()
        .then((ms) => alive && setLatency(ms))
        .catch(() => alive && setLatency(null));
    probe();
    const timer = setInterval(probe, appConfig.latencyProbeIntervalMs);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [status, setLatency]);

  return (
    <div
      className="connection-status-pill"
      aria-live="polite"
      title={`${t(`status.${status}`)}${latency !== null ? ` (${latency} ms)` : ""}`}
    >
      <span className={`status-dot status-dot--${status}`} />
      <span className="latency-indicator">{latency !== null ? `${latency} ms` : "-- ms"}</span>
    </div>
  );
};
