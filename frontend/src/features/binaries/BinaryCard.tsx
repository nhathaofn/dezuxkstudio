import React, { useEffect, useRef, useState } from "react";
import {
  CheckCircle2,
  Cpu,
  Download,
  FolderOpen,
  RefreshCw,
  RotateCcw,
  Save,
  Search,
  X,
  XCircle,
} from "lucide-react";
import type { ActionProgress, BinaryID, BinaryInfo, BinaryStatus } from "../../contracts/generated";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { getBridge, isDesktop } from "../../lib/bridge";
import { useT, type MessageKey } from "../../lib/i18n";
import { useBinariesStore } from "./useBinariesStore";

function getStatusBadge(status: BinaryStatus, t: (k: MessageKey) => string) {
  switch (status) {
    case "available":
      return {
        color: "var(--status-connected, #10b981)",
        icon: <CheckCircle2 size={13} style={{ color: "var(--status-connected, #10b981)" }} />,
        label: t("tools.status.available"),
      };
    case "missing":
      return {
        color: "var(--status-connecting, #f59e0b)",
        icon: <Search size={13} style={{ color: "var(--status-connecting, #f59e0b)" }} />,
        label: t("tools.status.missing"),
      };
    case "invalid":
    default:
      return {
        color: "var(--status-disconnected, #ef4444)",
        icon: <XCircle size={13} style={{ color: "var(--status-disconnected, #ef4444)" }} />,
        label: t("tools.status.invalid"),
      };
  }
}

export interface BinaryCardProps {
  binary: BinaryInfo;
  onProbe?: (id: BinaryID) => Promise<void>;
  onDownload?: (
    id: BinaryID,
    signal: AbortSignal,
    onProgress: (p: ActionProgress) => void
  ) => Promise<void>;
  onSetCustomPath?: (id: BinaryID, customPath: string) => Promise<void>;
  disabled?: boolean;
}

export const BinaryCard: React.FC<BinaryCardProps> = ({
  binary,
  onProbe,
  onDownload,
  onSetCustomPath,
  disabled = false,
}) => {
  const t = useT();
  const storeProbe = useBinariesStore((s) => s.probe);
  const storeDownload = useBinariesStore((s) => s.download);
  const storeSetCustomPath = useBinariesStore((s) => s.setCustomPath);

  const [isProbing, setIsProbing] = useState(false);
  const [downloadProgress, setDownloadProgress] = useState<ActionProgress | null>(null);
  const [customPathDraft, setCustomPathDraft] = useState<string | null>(null);
  const abortControllerRef = useRef<AbortController | null>(null);

  const customPathInput = customPathDraft ?? (binary.custom_path || "");

  useEffect(() => {
    return () => {
      abortControllerRef.current?.abort();
    };
  }, []);

  const isDownloading = downloadProgress !== null;
  const isDirty = customPathDraft !== null && customPathDraft !== (binary.custom_path || "");
  const badge = getStatusBadge(binary.status, t);

  const handleProbe = async () => {
    setIsProbing(true);
    try {
      if (onProbe) {
        await onProbe(binary.id);
      } else {
        await storeProbe(binary.id);
      }
    } finally {
      setIsProbing(false);
    }
  };

  const handleDownload = async () => {
    const ctrl = new AbortController();
    abortControllerRef.current = ctrl;
    setDownloadProgress({ percent: 0, message: "" });

    try {
      const runner = onDownload ?? storeDownload;
      await runner(binary.id, ctrl.signal, (p) => setDownloadProgress(p));
    } catch {
      // Error handled in store
    } finally {
      abortControllerRef.current = null;
      setDownloadProgress(null);
    }
  };

  const handleCancelDownload = () => {
    abortControllerRef.current?.abort();
  };

  const handleSaveCustomPath = async () => {
    const customPath = customPathInput.trim();
    setIsProbing(true);
    try {
      const runner = onSetCustomPath ?? storeSetCustomPath;
      await runner(binary.id, customPath);
      setCustomPathDraft(null);
    } finally {
      setIsProbing(false);
    }
  };

  const handleBrowse = async () => {
    const bridge = getBridge();
    if (!bridge?.BrowseExecutable) return;
    try {
      const selected = await bridge.BrowseExecutable(
        t("tools.browse.title", { name: binary.name })
      );
      if (selected) {
        setCustomPathDraft(selected);
      }
    } catch (err) {
      console.error("Browse failed:", err);
    }
  };

  const handleResetCustomPath = async () => {
    setCustomPathDraft(null);
    setIsProbing(true);
    try {
      const runner = onSetCustomPath ?? storeSetCustomPath;
      await runner(binary.id, "");
    } finally {
      setIsProbing(false);
    }
  };

  return (
    <div className="binary-card-item">
      {/* Header: Name, Executable, Status, Source, and Primary Actions */}
      <div className="binary-card-header">
        <div className="binary-header-left">
          <Cpu size={16} style={{ color: "var(--text-secondary)", flexShrink: 0 }} />
          <span
            style={{
              fontWeight: "var(--weight-semibold)",
              fontSize: "var(--text-sm)",
              color: "var(--text-primary)",
            }}
          >
            {binary.name}
          </span>
          <Badge>{binary.executable}</Badge>
          <div
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "var(--space-1)",
              marginLeft: "var(--space-1)",
            }}
          >
            {badge.icon}
            <span
              style={{
                fontSize: "var(--text-xs)",
                fontWeight: "var(--weight-medium)",
                color: badge.color,
                whiteSpace: "nowrap",
              }}
            >
              {badge.label}
            </span>
          </div>
          {binary.source !== "none" && <Badge>{t(`tools.source.${binary.source}`)}</Badge>}
        </div>

        <div className="binary-header-actions">
          {isDownloading ? (
            <Button size="sm" onClick={handleCancelDownload}>
              <X size={12} />
              {t("tools.cancel")}
            </Button>
          ) : (
            <>
              {binary.status !== "available" && binary.can_download && (
                <Button
                  variant="primary"
                  size="sm"
                  onClick={() => void handleDownload()}
                  disabled={isProbing || disabled}
                >
                  <Download size={12} />
                  {t("tools.download")}
                </Button>
              )}
              <Button
                size="sm"
                onClick={() => void handleProbe()}
                disabled={isProbing || isDownloading || disabled}
              >
                <RefreshCw size={12} className={isProbing ? "animate-spin" : ""} />
                {t("tools.probe")}
              </Button>
            </>
          )}
        </div>
      </div>

      {/* Progress bar during download */}
      {isDownloading && downloadProgress && (
        <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-1)" }}>
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              fontSize: "var(--text-xs)",
              color: "var(--text-secondary)",
            }}
          >
            <span>{downloadProgress.message || t("tools.downloading")}</span>
            <span>{downloadProgress.percent.toFixed(1)}%</span>
          </div>
          <div
            style={{
              height: "6px",
              backgroundColor: "var(--bg-surface-elevated)",
              borderRadius: "var(--radius-full)",
              overflow: "hidden",
              border: "1px solid var(--border-subtle)",
            }}
          >
            <div
              style={{
                height: "100%",
                width: `${Math.min(100, Math.max(0, downloadProgress.percent))}%`,
                backgroundColor: "var(--primary, #3b82f6)",
                transition: "width 200ms ease",
              }}
            />
          </div>
        </div>
      )}

      {/* Metadata Details */}
      <div className="binary-meta-box">
        <span className="binary-meta-label">{t("tools.version")}:</span>
        <span className="binary-meta-value">{binary.version || "—"}</span>

        <span className="binary-meta-label">{t("tools.location")}:</span>
        <span
          className="binary-meta-value"
          title={binary.resolved_path || t("tools.source.none")}
        >
          {binary.resolved_path || t("tools.source.none")}
        </span>
      </div>

      {/* Path Override Controls */}
      <div className="binary-input-row">
        <input
          type="text"
          className="binary-path-input"
          value={customPathInput}
          onChange={(e) => setCustomPathDraft(e.target.value)}
          placeholder={t("tools.customPathPlaceholder")}
          disabled={isDownloading || isProbing || disabled}
        />
        {isDesktop() && (
          <Button
            size="sm"
            onClick={() => void handleBrowse()}
            disabled={isDownloading || isProbing || disabled}
            title={t("tools.browse")}
          >
            <FolderOpen size={12} />
            {t("tools.browse")}
          </Button>
        )}
        {isDirty && (
          <Button
            variant="primary"
            size="sm"
            onClick={() => void handleSaveCustomPath()}
            disabled={isDownloading || isProbing || disabled}
            title={t("tools.savePath")}
          >
            <Save size={12} />
            {t("tools.savePath")}
          </Button>
        )}
        {Boolean(binary.custom_path) && !isDirty && (
          <Button
            size="sm"
            onClick={() => void handleResetCustomPath()}
            disabled={isDownloading || isProbing || disabled}
            title={t("tools.clearPath")}
          >
            <RotateCcw size={12} />
            {t("tools.clearPath")}
          </Button>
        )}
      </div>
    </div>
  );
};

export default BinaryCard;
