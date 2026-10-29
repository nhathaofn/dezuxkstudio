import React from "react";

export const LoadingSkeleton: React.FC = () => {
  return (
    <div className="system-card" style={{ opacity: 0.6 }}>
      <h2>Loading Module...</h2>
      <p className="subtitle">Streaming component bundle via code-splitting.</p>
    </div>
  );
};
