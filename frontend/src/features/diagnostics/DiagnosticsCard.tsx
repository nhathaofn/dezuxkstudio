import React, { useEffect, useRef, useState } from "react";
import { Folder, LifeBuoy, X } from "lucide-react";
import { Actions, type ActionProgress, type DiagnosticsBundle } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";
import { useConnectionStore } from "../connection/useConnectionStore";
import { useSystemStore } from "../system/useSystemStore";

/** Reference UI for a long-running action: progress bar + cancel via AbortController. */
export const DiagnosticsCard: React.FC = () => {
  const t = useT();
  const connected = useConnectionStore((s) => s.status === "connected");
  const openFolder = useSystemStore((s) => s.openFolder);

  const [progress, setProgress] = useState<ActionProgress | null>(null);
  const [result, setResult] = useState<DiagnosticsBundle | null>(null);
  const [error, setError] = useState<ActionError | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => () => abortRef.current?.abort(), []);

  const run = async () => {
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setError(null);
    setResult(null);
    setProgress({ percent: 0, message: "" });
    try {
      setResult(
        await rtClient.request(
          Actions.DIAGNOSTICS_EXPORT,
          {},
          { signal: ctrl.signal, onProgress: setProgress }
        )
      );
    } catch (err) {
      setError(toActionError(err));
    } finally {
      abortRef.current = null;
      setProgress(null);
    }
  };

  const running = progress !== null;

  return (
    <Card
      icon={LifeBuoy}
      title={t("diagnostics.title")}
      subtitle={t("diagnostics.subtitle")}
      actions={
        running ? (
          <Button onClick={() => abortRef.current?.abort()}>
            <X size={13} /> {t("diagnostics.cancel")}
          </Button>
        ) : (
          <Button variant="primary" onClick={() => void run()} disabled={!connected}>
            {t("diagnostics.export")}
          </Button>
        )
      }
    >
      {running && (
        <div
          className="progress"
          role="progressbar"
          aria-valuenow={Math.round(progress.percent)}
          aria-valuemin={0}
          aria-valuemax={100}
        >
          <div className="progress__bar" style={{ width: `${progress.percent}%` }} />
        </div>
      )}
      {result && (
        <div className="row row--wrap">
          <span className="muted-text">{t("diagnostics.done", { path: result.path })}</span>
          <Button onClick={() => void openFolder("diagnostics").then(setError)}>
            <Folder size={13} /> {t("diagnostics.openFolder")}
          </Button>
        </div>
      )}
      <ErrorText error={error} />
    </Card>
  );
};
