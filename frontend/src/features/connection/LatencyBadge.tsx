import React, { useEffect } from "react";
import { appConfig } from "../../config/appConfig";
import { rtClient } from "../../lib/rtClient";
import { useConnectionStore } from "./useConnectionStore";

/** Shows round-trip latency; probes only while connected and mounted. */
export const LatencyBadge: React.FC = () => {
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

  if (latency === null) return null;
  return <div className="latency-indicator">{latency} ms</div>;
};
