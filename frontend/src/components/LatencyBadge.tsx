import React from "react";

interface Props {
  latency: number | null;
}

export const LatencyBadge: React.FC<Props> = ({ latency }) => {
  if (latency === null) return null;

  return <div className="latency-indicator">{latency} ms</div>;
};
