import React, { useState } from "react";
import { Server, Edit2, RotateCcw, Check, X } from "lucide-react";
import { useRealtimeStore } from "../stores/useRealtimeStore";
import { clearSavedTargetUrl } from "../services/rtClient/urlResolver";
import { Button } from "./ui/Button";

export const ServerConfigCard: React.FC = () => {
  const currentUrl = useRealtimeStore((state) => state.currentUrl);
  const updateUrl = useRealtimeStore((state) => state.updateUrl);
  const resetToAuto = useRealtimeStore((state) => state.resetToAuto);

  const [isEditing, setIsEditing] = useState(false);
  const [inputUrl, setInputUrl] = useState("");

  const handleStartEdit = () => {
    setInputUrl(currentUrl);
    setIsEditing(true);
  };

  const handleApply = () => {
    const trimmed = inputUrl.trim();
    if (trimmed) {
      updateUrl(trimmed);
    }
    setIsEditing(false);
  };

  const handleCancel = () => {
    setIsEditing(false);
  };

  const handleResetAuto = async () => {
    clearSavedTargetUrl();
    setIsEditing(false);
    await resetToAuto();
  };

  return (
    <div className="system-card">
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "var(--space-2)",
          marginBottom: "var(--space-2)",
        }}
      >
        <Server size={18} style={{ color: "var(--color-primary-400)" }} />
        <h2 style={{ margin: 0 }}>Architecture Ready</h2>
      </div>
      <p className="subtitle">
        React Thin Client powered by Zustand & Zod, connected via full-duplex WebSocket.
      </p>

      <div className="server-config">
        <label className="field-label">Server Endpoint:</label>
        {!isEditing ? (
          <div className="url-display">
            <code>{currentUrl || "(resolving dynamic endpoint...)"}</code>
            <div style={{ display: "flex", gap: "var(--space-2)" }}>
              <Button variant="secondary" size="sm" onClick={handleStartEdit}>
                <Edit2 size={13} style={{ marginRight: 4, verticalAlign: "text-bottom" }} />
                Edit
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={handleResetAuto}
                title="Reset to auto-discovered port"
              >
                <RotateCcw size={13} style={{ marginRight: 4, verticalAlign: "text-bottom" }} />
                Auto
              </Button>
            </div>
          </div>
        ) : (
          <div className="url-edit">
            <input
              type="text"
              className="input-control"
              value={inputUrl}
              onChange={(e) => setInputUrl(e.target.value)}
              placeholder="ws://host:port/ws"
            />
            <Button variant="primary" size="sm" onClick={handleApply}>
              <Check size={14} style={{ marginRight: 4, verticalAlign: "text-bottom" }} />
              Save
            </Button>
            <Button variant="secondary" size="sm" onClick={handleCancel}>
              <X size={14} style={{ marginRight: 4, verticalAlign: "text-bottom" }} />
              Cancel
            </Button>
          </div>
        )}
      </div>
    </div>
  );
};
