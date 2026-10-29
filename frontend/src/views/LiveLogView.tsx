import React from "react";
import { Terminal, Plus, Trash2 } from "lucide-react";
import { useLogStreamStore, type LogEntry } from "../stores/useLogStreamStore";
import { VirtualList } from "../components/ui/VirtualList";
import { Button } from "../components/ui/Button";

export const LiveLogView: React.FC = () => {
  // Strict atomic selector: only subscribes to logs array
  const logs = useLogStreamStore((s) => s.logs);
  const clearLogs = useLogStreamStore((s) => s.clearLogs);
  const addLog = useLogStreamStore((s) => s.addLog);

  const handleSimulate = () => {
    addLog("info", `Event dispatch test at ${new Date().toLocaleTimeString()}`);
  };

  return (
    <div className="system-card system-card--wide">
      <div className="card-header-row">
        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
          <Terminal size={18} style={{ color: "var(--color-primary-400)" }} />
          <h2 style={{ margin: 0 }}>Live Event Stream (Virtualized)</h2>
        </div>
        <div className="card-actions-row">
          <Button variant="secondary" size="sm" onClick={handleSimulate}>
            <Plus size={13} style={{ marginRight: 4, verticalAlign: "text-bottom" }} />
            Event
          </Button>
          <Button variant="secondary" size="sm" onClick={clearLogs}>
            <Trash2 size={13} style={{ marginRight: 4, verticalAlign: "text-bottom" }} />
            Clear
          </Button>
        </div>
      </div>
      <p className="subtitle">
        High-frequency stream bounded to 500 items max with TanStack Virtual (0 memory leak).
      </p>

      {logs.length === 0 ? (
        <div className="url-display">
          <code>No active stream logs yet.</code>
        </div>
      ) : (
        <VirtualList<LogEntry>
          items={logs}
          estimateSize={28}
          height={240}
          getItemKey={(item) => item.id}
          renderItem={(item) => (
            <div className={`log-item log-item--${item.level}`}>
              <span className="log-time">{new Date(item.timestamp).toLocaleTimeString()}</span>
              <span>{item.message}</span>
            </div>
          )}
        />
      )}
    </div>
  );
};

export default LiveLogView;
