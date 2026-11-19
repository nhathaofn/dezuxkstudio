import React, { useRef, useState } from "react";
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
import { translateServerMessage, useT, type MessageKey } from "../../lib/i18n";
import { useSystemStore } from "../system/useSystemStore";
import { useBinariesStore } from "./useBinariesStore";

function getStatusBadge(status: BinaryStatus, t: (k: MessageKey) => string) {
  switch (status) {
    case "available":
      return {
        // label stays in the normal text colour; only errors are red
        color: undefined,
        icon: <CheckCircle2 size={13} style={{ color: "var(--status-connected)" }} />,
        label: t("tools.status.available"),
      };
    case "missing":
      return {
        // label stays in the normal text colour; only errors are red
        color: undefined,
        icon: <Search size={13} style={{ color: "var(--status-connecting)" }} />,
        label: t("tools.status.missing"),
      };
    case "invalid":
    default:
      return {
        color: "var(--status-disconnected)",
        icon: <XCircle size={13} style={{ color: "var(--status-disconnected)" }} />,
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
  const storeCancelDownload = useBinariesStore((s) => s.cancelDownload);
  const storeSetCustomPath = useBinariesStore((s) => s.setCustomPath);
  const storeProgress = useBinariesStore((s) => s.activeProgress[binary.id]);

  const [isProbing, setIsProbing] = useState(false);
  const [downloadProgress, setDownloadProgress] = useState<ActionProgress | null>(null);
  const [customPathDraft, setCustomPathDraft] = useState<string | null>(null);
  const customAbortRef = useRef<AbortController | null>(null);

  const activeProgress = downloadProgress ?? storeProgress ?? null;
  const customPathInput = customPathDraft ?? (binary.custom_path || "");

  const isDownloading = activeProgress !== null;
  const isDirty = customPathDraft !== null && customPathDraft !== (binary.custom_path || "");
  const badge = getStatusBadge(binary.status, t);

  const sysMode = useSystemStore((s) => s.info?.mode);
  const isRemote = sysMode === "server" || sysMode === "client";
  const desktopOnlyTooltip = t("system.desktopOnly");
  const canBrowse = isDesktop() && !isRemote;

  const isResume = Boolean(
    activeProgress?.is_resume ||
    activeProgress?.binary_progress?.is_resume ||
    activeProgress?.msg_params?.is_resume === "true"
  );

  const resumedFromBytes =
    activeProgress?.resumed_from_bytes ||
    activeProgress?.binary_progress?.resumed_from_bytes ||
    (activeProgress?.msg_params?.resumed_from ? Number(activeProgress.msg_params.resumed_from) : 0);

  const resumedFromMB = (() => {
    if (activeProgress?.msg_params?.resumed_from_mb) {
      return activeProgress.msg_params.resumed_from_mb;
    }
    if (resumedFromBytes > 0) {
      return (resumedFromBytes / (1024 * 1024)).toFixed(1);
    }
    return null;
  })();

  const getPathWarning = (): string | null => {
    if (customPathDraft !== null) {
      const trimmed = customPathDraft.trim();
      if (trimmed.length === 0) {
        return t("tools.pathWarning.empty");
      }
      const pathWithoutDrive = trimmed.replace(/^[a-zA-Z]:/, "");
      if (/[<>"|?*]/.test(pathWithoutDrive)) {
        return t("tools.pathWarning.invalidChars");
      }
      if (
        !trimmed.toLowerCase().endsWith(".exe") &&
        !trimmed.toLowerCase().endsWith(binary.executable.toLowerCase())
      ) {
        return t("tools.pathWarning.expectedExe", { executable: binary.executable });
      }
      return null;
    }
    if (binary.custom_path && binary.status === "invalid") {
      return t("tools.pathWarning.invalidConfigured");
    }
    return null;
  };

  const pathWarning = getPathWarning();

  const mbText = (() => {
    if (activeProgress?.msg_params?.done) {
      if (activeProgress.msg_params.total) {
        return `${activeProgress.msg_params.done} / ${activeProgress.msg_params.total} MB`;
      }
      return `${activeProgress.msg_params.done} MB`;
    }
    const match = activeProgress?.message?.match(/(\d+(?:\.\d+)?(?:\s*\/\s*\d+(?:\.\d+)?)?\s*MB)/i);
    return match ? match[1] : null;
  })();

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
    if (onDownload) {
      const ctrl = new AbortController();
      customAbortRef.current = ctrl;
      setDownloadProgress({ percent: 0, message: "" });
      try {
        await onDownload(binary.id, ctrl.signal, (p) => setDownloadProgress(p));
      } catch {
        // Error handled by caller/store
      } finally {
        customAbortRef.current = null;
        setDownloadProgress(null);
      }
      return;
    }

    try {
      await storeDownload(binary.id);
    } catch {
      // Error handled in store
    }
  };

  const handleCancelDownload = () => {
    if (customAbortRef.current) {
      customAbortRef.current.abort();
      return;
    }
    storeCancelDownload(binary.id);
  };

  const handleSaveCustomPath = async () => {
    const customPath = customPathInput.trim();
    if (!customPath || (pathWarning && customPathDraft !== null)) return;
    setIsProbing(true);
    try {
      const runner = onSetCustomPath ?? storeSetCustomPath;
      await runner(binary.id, customPath);
      setCustomPathDraft(null);
    } catch {
      // Error handled in store
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
    } catch {
      // Error handled in store
    } finally {
      setIsProbing(false);
    }
  };

  return (
    <div className="binary-card-item">
      {/* Header: Name, Executable, Status, Source, and Primary Actions */}
      <div className="binary-card-header">
        <div className="binary-header-left">
          <Cpu size={16} className="binary-header-icon" />
          <span className="binary-name">{binary.name}</span>
          <Badge>{binary.executable}</Badge>
          <span className="binary-status-inline">
            {badge.icon}
            <span className="binary-status-label" style={{ color: badge.color }}>
              {badge.label}
            </span>
          </span>
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
      {isDownloading && activeProgress && (
        <div className="binary-progress-wrap">
          <div className="binary-progress-meta">
            <span className="binary-progress-status">
              {translateServerMessage(t, activeProgress.msg_key, activeProgress.msg_params) ||
                activeProgress.message ||
                t("tools.downloading")}
              {isResume && (
                <Badge
                  className="binary-resumed-badge"
                  style={{
                    marginLeft: "var(--space-2)",
                    fontSize: "var(--text-xs)",
                    color: "var(--status-connecting)",
                  }}
                >
                  {resumedFromMB
                    ? t("tools.resumedFrom", { mb: resumedFromMB })
                    : t("tools.resumed")}
                </Badge>
              )}
            </span>
            <span className="binary-progress-metrics">
              {mbText && (
                <span className="binary-progress-mb" style={{ marginRight: "var(--space-2)" }}>
                  {mbText}
                </span>
              )}
              <span
                className="binary-progress-pct"
                style={{ fontWeight: "var(--weight-semibold)" }}
              >
                {activeProgress.percent.toFixed(1)}%
              </span>
            </span>
          </div>
          <div className="progress">
            <div
              className="progress__bar"
              style={{
                width: `${Math.min(100, Math.max(0, activeProgress.percent))}%`,
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
        <span className="binary-meta-value" title={binary.resolved_path || t("tools.source.none")}>
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
          aria-invalid={Boolean(pathWarning)}
        />
        <Button
          size="sm"
          onClick={() => void handleBrowse()}
          disabled={isDownloading || isProbing || disabled || !canBrowse}
          title={!canBrowse ? desktopOnlyTooltip : t("tools.browse")}
          aria-disabled={!canBrowse}
        >
          <FolderOpen size={12} />
          {t("tools.browse")}
        </Button>
        {isDirty && (
          <Button
            variant="primary"
            size="sm"
            onClick={() => void handleSaveCustomPath()}
            disabled={isDownloading || isProbing || disabled || Boolean(pathWarning)}
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
      {pathWarning && (
        <div
          className="binary-path-warning"
          role="alert"
          style={{
            display: "flex",
            alignItems: "center",
            gap: "var(--space-1)",
            marginTop: "var(--space-1)",
            fontSize: "var(--text-xs)",
            color: "var(--status-disconnected)",
          }}
        >
          <XCircle size={12} style={{ flexShrink: 0 }} />
          <span>{pathWarning}</span>
        </div>
      )}
    </div>
  );
};

export default BinaryCard;
