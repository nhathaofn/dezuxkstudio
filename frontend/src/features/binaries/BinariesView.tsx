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
  Wrench,
  X,
  XCircle,
} from "lucide-react";
import type { ActionProgress, BinaryID, BinaryInfo, BinaryStatus } from "../../contracts/generated";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { getBridge, isDesktop } from "../../lib/bridge";
import { useT, type MessageKey } from "../../lib/i18n";
import { useConnectionStore } from "../connection/useConnectionStore";
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

export const BinariesView: React.FC = () => {
  const t = useT();
  const connected = useConnectionStore((s) => s.status === "connected");

  const binaries = useBinariesStore((s) => s.binaries);
  const loading = useBinariesStore((s) => s.loading);
  const error = useBinariesStore((s) => s.error);
  const fetchBinaries = useBinariesStore((s) => s.fetchBinaries);
  const probe = useBinariesStore((s) => s.probe);
  const download = useBinariesStore((s) => s.download);
  const setCustomPath = useBinariesStore((s) => s.setCustomPath);

  const [probingIds, setProbingIds] = useState<Record<string, boolean>>({});
  const [downloadProgress, setDownloadProgress] = useState<Record<string, ActionProgress | null>>(
    {}
  );
  const [customPathInputs, setCustomPathInputs] = useState<Record<string, string>>({});
  const abortControllers = useRef<Record<string, AbortController | null>>({});

  // Cleanup in-flight downloads on unmount
  useEffect(() => {
    const ctrls = abortControllers.current;
    return () => {
      Object.values(ctrls).forEach((ctrl) => ctrl?.abort());
    };
  }, []);

  const handleProbe = async (id: BinaryID) => {
    setProbingIds((prev) => ({ ...prev, [id]: true }));
    try {
      await probe(id);
    } finally {
      setProbingIds((prev) => ({ ...prev, [id]: false }));
    }
  };

  const handleDownload = async (id: BinaryID) => {
    const ctrl = new AbortController();
    abortControllers.current[id] = ctrl;
    setDownloadProgress((prev) => ({ ...prev, [id]: { percent: 0, message: "" } }));

    try {
      await download(id, ctrl.signal, (p) => {
        setDownloadProgress((prev) => ({ ...prev, [id]: p }));
      });
    } catch {
      // Error is set in store
    } finally {
      abortControllers.current[id] = null;
      setDownloadProgress((prev) => ({ ...prev, [id]: null }));
    }
  };

  const handleCancelDownload = (id: BinaryID) => {
    abortControllers.current[id]?.abort();
  };

  const handleSaveCustomPath = async (id: BinaryID) => {
    const customPath = (customPathInputs[id] ?? "").trim();
    setProbingIds((prev) => ({ ...prev, [id]: true }));
    try {
      await setCustomPath(id, customPath);
    } finally {
      setProbingIds((prev) => ({ ...prev, [id]: false }));
    }
  };

  const handleBrowse = async (id: BinaryID, name: string) => {
    const bridge = getBridge();
    if (!bridge?.BrowseExecutable) return;
    try {
      const selected = await bridge.BrowseExecutable(`Select executable for ${name}`);
      if (selected) {
        setCustomPathInputs((prev) => ({ ...prev, [id]: selected }));
      }
    } catch (err) {
      console.error("Browse failed:", err);
    }
  };

  const handleResetCustomPath = async (id: BinaryID) => {
    setCustomPathInputs((prev) => ({ ...prev, [id]: "" }));
    setProbingIds((prev) => ({ ...prev, [id]: true }));
    try {
      await setCustomPath(id, "");
    } finally {
      setProbingIds((prev) => ({ ...prev, [id]: false }));
    }
  };

  return (
    <div className="cards-stack flex-full-height">
      <Card
        icon={Wrench}
        title={t("tools.title")}
        wide
        className="card--flex-fill"
        actions={
          <Button
            onClick={() => void fetchBinaries()}
            disabled={!connected || loading}
            title={t("tools.refresh")}
          >
            <RefreshCw size={13} className={loading ? "animate-spin" : ""} />
            {t("tools.refresh")}
          </Button>
        }
      >
        {error && <ErrorText error={error} />}

        <div className="scrollable-pane">
          <div className="tools-binary-list">
            {binaries.map((bin: BinaryInfo) => {
              const isProbing = Boolean(probingIds[bin.id]);
              const progress = downloadProgress[bin.id];
              const isDownloading = progress !== null && progress !== undefined;
              const currentInput = customPathInputs[bin.id] ?? (bin.custom_path || "");
              const isDirty = currentInput !== (bin.custom_path || "");
              const badge = getStatusBadge(bin.status, t);

              return (
                <div key={bin.id} className="binary-card-item">
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
                        {bin.name}
                      </span>
                      <Badge>{bin.executable}</Badge>
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
                      {bin.source !== "none" && <Badge>{t(`tools.source.${bin.source}`)}</Badge>}
                    </div>

                    <div className="binary-header-actions">
                      {isDownloading ? (
                        <Button size="sm" onClick={() => handleCancelDownload(bin.id)}>
                          <X size={12} />
                          {t("tools.cancel")}
                        </Button>
                      ) : (
                        <>
                          {bin.status !== "available" && bin.can_download && (
                            <Button
                              variant="primary"
                              size="sm"
                              onClick={() => void handleDownload(bin.id)}
                              disabled={isProbing}
                            >
                              <Download size={12} />
                              {t("tools.download")}
                            </Button>
                          )}
                          <Button
                            size="sm"
                            onClick={() => void handleProbe(bin.id)}
                            disabled={isProbing || isDownloading}
                          >
                            <RefreshCw size={12} className={isProbing ? "animate-spin" : ""} />
                            {t("tools.probe")}
                          </Button>
                        </>
                      )}
                    </div>
                  </div>

                  {/* Progress bar during download */}
                  {isDownloading && progress && (
                    <div
                      style={{ display: "flex", flexDirection: "column", gap: "var(--space-1)" }}
                    >
                      <div
                        style={{
                          display: "flex",
                          justifyContent: "space-between",
                          fontSize: "var(--text-xs)",
                          color: "var(--text-secondary)",
                        }}
                      >
                        <span>{progress.message || t("tools.downloading")}</span>
                        <span>{progress.percent.toFixed(1)}%</span>
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
                            width: `${Math.min(100, Math.max(0, progress.percent))}%`,
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
                    <span className="binary-meta-value">{bin.version || "—"}</span>

                    <span className="binary-meta-label">{t("tools.location")}:</span>
                    <span
                      className="binary-meta-value"
                      title={bin.resolved_path || t("tools.source.none")}
                    >
                      {bin.resolved_path || t("tools.source.none")}
                    </span>
                  </div>

                  {/* Path Override Controls */}
                  <div className="binary-input-row">
                    <input
                      type="text"
                      className="binary-path-input"
                      value={currentInput}
                      onChange={(e) =>
                        setCustomPathInputs((prev) => ({ ...prev, [bin.id]: e.target.value }))
                      }
                      placeholder={t("tools.customPathPlaceholder")}
                      disabled={isDownloading || isProbing}
                    />
                    {isDesktop() && (
                      <Button
                        size="sm"
                        onClick={() => void handleBrowse(bin.id, bin.name)}
                        disabled={isDownloading || isProbing}
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
                        onClick={() => void handleSaveCustomPath(bin.id)}
                        disabled={isDownloading || isProbing}
                        title={t("tools.savePath")}
                      >
                        <Save size={12} />
                        {t("tools.savePath")}
                      </Button>
                    )}
                    {Boolean(bin.custom_path) && !isDirty && (
                      <Button
                        size="sm"
                        onClick={() => void handleResetCustomPath(bin.id)}
                        disabled={isDownloading || isProbing}
                        title={t("tools.clearPath")}
                      >
                        <RotateCcw size={12} />
                        {t("tools.clearPath")}
                      </Button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </Card>
    </div>
  );
};

export default BinariesView;
