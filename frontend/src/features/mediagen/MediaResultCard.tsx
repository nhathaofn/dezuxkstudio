import React, { useEffect, useState } from "react";
import {
  Check,
  CheckCircle2,
  Copy,
  Download,
  FileAudio,
  Film,
  Image as ImageIcon,
  Loader2,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import type { ActionProgress } from "../../contracts/generated";
import { downloadMediaBlob, type ActionError } from "../../lib/rtClient";
import { translateServerMessage, useT } from "../../lib/i18n";
import { formatByteSize } from "./types";

export type MediaResultKind = "image" | "video" | "tts";

export interface MediaOutputSummary {
  path: string;
  mimeType: string;
  provider: string;
  model: string;
  sizeBytes: number;
  duration?: number;
}

interface MediaResultCardProps {
  kind: MediaResultKind;
  loading: boolean;
  progress: ActionProgress | null;
  error: ActionError | null;
  result: MediaOutputSummary | null;
  downloadingPath: string | null;
  onDownload: () => void;
}

export const MediaResultCard: React.FC<MediaResultCardProps> = ({
  kind,
  loading,
  progress,
  error,
  result,
  downloadingPath,
  onDownload,
}) => {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const [previewState, setPreviewState] = useState<{ path: string; url: string | null }>({
    path: "",
    url: null,
  });

  const resultPath = result?.path ?? "";
  const isFetchingPreview = Boolean(
    resultPath && previewState.path !== resultPath && typeof URL.createObjectURL === "function"
  );

  useEffect(() => {
    if (!resultPath || typeof URL.createObjectURL !== "function") return;
    let cancelled = false;
    let objectUrl: string | null = null;

    void downloadMediaBlob(resultPath)
      .then(({ blob }) => {
        if (cancelled) return;
        objectUrl = URL.createObjectURL(blob);
        setPreviewState({ path: resultPath, url: objectUrl });
      })
      .catch(() => {
        if (!cancelled) {
          setPreviewState({ path: resultPath, url: null });
        }
      });

    return () => {
      cancelled = true;
      if (objectUrl) {
        URL.revokeObjectURL(objectUrl);
      }
    };
  }, [resultPath]);

  const activePreviewUrl = resultPath && previewState.path === resultPath ? previewState.url : null;

  const KindIcon = kind === "video" ? Film : kind === "tts" ? FileAudio : ImageIcon;
  const pct =
    progress?.percent != null ? Math.max(0, Math.min(100, Math.round(progress.percent))) : 0;

  const handleCopyPath = async () => {
    if (!result?.path) return;
    try {
      await navigator.clipboard.writeText(result.path);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      // Clipboard may be blocked in unprivileged frames
    }
  };

  return (
    <Card
      icon={KindIcon}
      title={t("mediagen.card.output")}
      className="card--flex-fill"
      actions={
        result ? (
          <Badge className="watermark-badge-connected">
            <CheckCircle2 size={11} className="watermark-badge-icon" />{" "}
            {formatByteSize(result.sizeBytes)}
          </Badge>
        ) : undefined
      }
    >
      <ErrorText error={error} />

      {!loading && !result && (
        <div className="empty-state">
          <KindIcon size={34} className="empty-state__icon" />
          <div className="empty-state__title">{t("mediagen.empty.title")}</div>
          <div className="empty-state__desc">{t(`mediagen.empty.${kind}`)}</div>
        </div>
      )}

      {loading && (
        <div className="empty-state">
          <Loader2 size={28} className="animate-spin mediagen-spinner-icon" />
          <div className="empty-state__title">
            {(progress && translateServerMessage(t, progress.msg_key, progress.msg_params)) ||
              progress?.message ||
              t(`mediagen.running.${kind}`)}
          </div>
          {progress && (
            <div
              className="progress watermark-progress"
              role="progressbar"
              aria-valuenow={pct}
              aria-valuemin={0}
              aria-valuemax={100}
            >
              <div className="progress__bar" style={{ width: `${pct}%` }} />
            </div>
          )}
        </div>
      )}

      {result && !loading && (
        <div className="watermark-results-body">
          {(isFetchingPreview || activePreviewUrl) && (
            <div className="mediagen-preview-box">
              {isFetchingPreview && (
                <div className="mediagen-preview-loading">
                  <Loader2 size={20} className="animate-spin" />
                </div>
              )}
              {activePreviewUrl && kind === "image" && (
                <img src={activePreviewUrl} alt={result.model} className="mediagen-preview-img" />
              )}
              {activePreviewUrl && kind === "video" && (
                <video
                  src={activePreviewUrl}
                  controls
                  playsInline
                  className="mediagen-preview-video"
                />
              )}
              {activePreviewUrl && kind === "tts" && (
                <audio src={activePreviewUrl} controls className="mediagen-preview-audio" />
              )}
            </div>
          )}

          <div className="mediagen-meta-grid">
            <div className="mediagen-meta-item">
              <span className="mediagen-meta-label">{t("mediagen.meta.provider")}</span>
              <span className="mediagen-meta-value">{result.provider}</span>
            </div>
            <div className="mediagen-meta-item">
              <span className="mediagen-meta-label">{t("mediagen.meta.model")}</span>
              <span className="mediagen-meta-value">{result.model}</span>
            </div>
            <div className="mediagen-meta-item">
              <span className="mediagen-meta-label">{t("mediagen.meta.mime")}</span>
              <span className="mediagen-meta-value">{result.mimeType}</span>
            </div>
            {result.duration != null && result.duration > 0 && (
              <div className="mediagen-meta-item">
                <span className="mediagen-meta-label">{t("mediagen.meta.duration")}</span>
                <span className="mediagen-meta-value">{result.duration}s</span>
              </div>
            )}
          </div>

          <div className="watermark-file-row">
            <div className="watermark-file-meta">
              <div className="mediagen-meta-label">{t("watermark.result.path")}</div>
              <div className="watermark-file-path" title={result.path}>
                {result.path}
              </div>
            </div>
            <div className="watermark-file-actions">
              <button
                type="button"
                className="watermark-icon-btn"
                title={t("mediagen.action.copyPath")}
                aria-label={t("mediagen.action.copyPath")}
                onClick={handleCopyPath}
              >
                {copied ? <Check size={12} /> : <Copy size={12} />}
              </button>
              <button
                type="button"
                className="watermark-icon-btn"
                title={t("mediagen.action.download")}
                aria-label={t("mediagen.action.download")}
                onClick={onDownload}
                disabled={downloadingPath === result.path}
              >
                {downloadingPath === result.path ? (
                  <Loader2 size={12} className="animate-spin" />
                ) : (
                  <Download size={12} />
                )}
              </button>
            </div>
          </div>
        </div>
      )}
    </Card>
  );
};
