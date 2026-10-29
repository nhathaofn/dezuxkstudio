import React, { useState } from "react";
import { Zap, Send } from "lucide-react";
import { useRealtimeStore } from "../stores/useRealtimeStore";
import { useLogStreamStore } from "../stores/useLogStreamStore";
import { Button } from "./ui/Button";

export const EngineActionTester: React.FC = () => {
  const status = useRealtimeStore((state) => state.status);
  const sendAction = useRealtimeStore((state) => state.sendAction);
  const addLog = useLogStreamStore((state) => state.addLog);

  const [action, setAction] = useState("system:info");
  const [payloadStr, setPayloadStr] = useState("{}");
  const [loading, setLoading] = useState(false);
  const [response, setResponse] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [elapsedMs, setElapsedMs] = useState<number | null>(null);

  const handleExecute = async () => {
    if (!action.trim() || status !== "connected") return;

    let parsedPayload: unknown = {};
    try {
      if (payloadStr.trim()) {
        parsedPayload = JSON.parse(payloadStr);
      }
    } catch {
      setError("Payload is not valid JSON");
      return;
    }

    setLoading(true);
    setError(null);
    setResponse(null);
    const start = performance.now();

    try {
      const res = await sendAction<unknown>(action.trim(), parsedPayload);
      const duration = Math.round(performance.now() - start);
      setElapsedMs(duration);
      const jsonRes = JSON.stringify(res, null, 2);
      setResponse(jsonRes);
      addLog("info", `[Action] ${action} executed in ${duration}ms`);
    } catch (err: unknown) {
      const duration = Math.round(performance.now() - start);
      setElapsedMs(duration);
      const errMsg = err instanceof Error ? err.message : String(err);
      setError(errMsg);
      addLog("error", `[Action] ${action} failed: ${errMsg}`);
    } finally {
      setLoading(false);
    }
  };

  const isConnected = status === "connected";

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
        <Zap size={18} style={{ color: "var(--color-primary-400)" }} />
        <h2 style={{ margin: 0 }}>Realtime Action Dispatcher</h2>
      </div>
      <p className="subtitle">
        Dispatch non-blocking asynchronous actions directly to the Go Native Core Hub.
      </p>

      <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)" }}>
        <div className="form-group">
          <label className="field-label">Action Identifier:</label>
          <div style={{ display: "flex", gap: "var(--space-2)" }}>
            <input
              type="text"
              className="input-control"
              value={action}
              onChange={(e) => setAction(e.target.value)}
              placeholder="e.g. system:info"
            />
            <Button
              variant="primary"
              size="sm"
              onClick={handleExecute}
              disabled={!isConnected || loading}
            >
              <Send size={13} style={{ marginRight: 4, verticalAlign: "text-bottom" }} />
              {loading ? "Executing..." : "Dispatch"}
            </Button>
          </div>
        </div>

        <div className="form-group">
          <label className="field-label">Payload (JSON):</label>
          <textarea
            rows={3}
            className="textarea-control"
            value={payloadStr}
            onChange={(e) => setPayloadStr(e.target.value)}
            placeholder='{"key": "value"}'
          />
        </div>

        <div className="chips-row">
          <span className="chips-label">Presets:</span>
          {["system:info", "system:ping", "system:echo"].map((preset) => (
            <button
              key={preset}
              type="button"
              className="chip-btn"
              onClick={() => {
                setAction(preset);
                if (preset === "system:echo") {
                  setPayloadStr(
                    JSON.stringify(
                      { greeting: "Hello from Go Realtime Core", timestamp: Date.now() },
                      null,
                      2
                    )
                  );
                } else {
                  setPayloadStr("{}");
                }
              }}
            >
              {preset}
            </button>
          ))}
        </div>

        {elapsedMs !== null && (
          <div style={{ fontSize: "var(--text-xs)", color: "var(--text-muted)" }}>
            Round-trip latency:{" "}
            <strong style={{ color: "var(--text-primary)" }}>{elapsedMs}ms</strong>
          </div>
        )}

        {error && (
          <div className="alert-box">
            <strong>Execution Error:</strong> {error}
          </div>
        )}

        {response && (
          <div className="form-group">
            <label className="field-label">Core Response:</label>
            <pre className="code-viewer">
              <code>{response}</code>
            </pre>
          </div>
        )}
      </div>
    </div>
  );
};
