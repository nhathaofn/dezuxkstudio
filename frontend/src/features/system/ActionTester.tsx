import React, { useState } from "react";
import { Send, Zap } from "lucide-react";
import { Actions } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import { ActionError, rtClient, toActionError } from "../../lib/rtClient";
import { uiLog } from "../logs/useLogStore";
import { useConnectionStore } from "../connection/useConnectionStore";

const presets: { action: string; payload: unknown }[] = [
  { action: Actions.SYSTEM_INFO, payload: {} },
  { action: Actions.SYSTEM_PING, payload: {} },
  { action: Actions.SYSTEM_ECHO, payload: { hello: "world" } },
  { action: Actions.PREFERENCES_GET, payload: {} },
];

/** Developer tool: sends any action (untyped on purpose) and shows the raw response. */
export const ActionTester: React.FC = () => {
  const t = useT();
  const connected = useConnectionStore((s) => s.status === "connected");

  const [action, setAction] = useState<string>(Actions.SYSTEM_INFO);
  const [payload, setPayload] = useState("{}");
  const [busy, setBusy] = useState(false);
  const [response, setResponse] = useState<string | null>(null);
  const [error, setError] = useState<ActionError | null>(null);
  const [elapsed, setElapsed] = useState<number | null>(null);

  const run = async () => {
    let parsed: unknown;
    try {
      parsed = payload.trim() ? JSON.parse(payload) : {};
    } catch {
      setError(new ActionError("ERR_VALIDATION", t("tester.invalidJson")));
      return;
    }
    setBusy(true);
    setError(null);
    setResponse(null);
    const start = performance.now();
    try {
      const res = await rtClient.requestRaw(action.trim(), parsed);
      setResponse(JSON.stringify(res, null, 2));
      uiLog.info(`${action} ok`);
    } catch (err) {
      const e = toActionError(err);
      setError(e);
      uiLog.error(`${action} failed: [${e.code}] ${e.message}`);
    } finally {
      setElapsed(Math.round(performance.now() - start));
      setBusy(false);
    }
  };

  return (
    <Card icon={Zap} title={t("tester.title")} subtitle={t("tester.subtitle")}>
      <div className="stack">
        <div className="form-group">
          <label className="field-label">{t("tester.action")}</label>
          <div className="row">
            <input
              className="input-control"
              value={action}
              onChange={(e) => setAction(e.target.value)}
            />
            <Button variant="primary" onClick={() => void run()} disabled={!connected || busy}>
              <Send size={13} /> {busy ? t("tester.dispatching") : t("tester.dispatch")}
            </Button>
          </div>
        </div>
        <div className="form-group">
          <label className="field-label">{t("tester.payload")}</label>
          <textarea
            rows={3}
            className="textarea-control"
            value={payload}
            onChange={(e) => setPayload(e.target.value)}
          />
        </div>
        <div className="chips-row">
          <span className="chips-label">{t("tester.presets")}</span>
          {presets.map((p) => (
            <button
              key={p.action}
              type="button"
              className="chip-btn"
              onClick={() => {
                setAction(p.action);
                setPayload(JSON.stringify(p.payload, null, 2));
              }}
            >
              {p.action}
            </button>
          ))}
        </div>
        {elapsed !== null && (
          <div className="muted-text">{t("tester.latency", { ms: elapsed })}</div>
        )}
        <ErrorText error={error} />
        {response && (
          <div className="form-group">
            <label className="field-label">{t("tester.response")}</label>
            <pre className="code-viewer">
              <code>{response}</code>
            </pre>
          </div>
        )}
      </div>
    </Card>
  );
};
