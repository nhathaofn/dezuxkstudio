import React, { useEffect, useRef, useState } from "react";
import { useInRouterContext, useNavigate } from "react-router-dom";
import {
  Bot,
  FileAudio,
  Film,
  FolderArchive,
  Image as ImageIcon,
  Loader2,
  RefreshCw,
  Search,
  Sparkles,
  Trash2,
  X,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { ErrorText } from "../../components/ui/ErrorText";
import { VirtualList } from "../../components/ui/VirtualList";
import type { MediaAssetItem } from "../../contracts/generated";
import { useSignedMediaURL } from "../../lib/hooks/useSignedMediaURL";
import { mediaBasename } from "../../lib/rtClient/media";
import { toActionError } from "../../lib/rtClient/errors";
import { useT } from "../../lib/i18n";
import { askAgentAboutMediaAsset } from "../agent/screenContext";
import { useMediaLibraryStore } from "./useMediaLibraryStore";

function safeRevokeObjectURL(url: string | null | undefined): void {
  if (!url || typeof URL === "undefined" || typeof URL.revokeObjectURL !== "function") return;
  try {
    URL.revokeObjectURL(url);
  } catch (_err) {
    void _err;
  }
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

const RouterNavigateBridge: React.FC<{
  navRef: React.MutableRefObject<((path: string, opts?: { state?: unknown }) => void) | null>;
}> = ({ navRef }) => {
  const navigate = useNavigate();
  useEffect(() => {
    navRef.current = navigate;
  }, [navigate, navRef]);
  return null;
};

const MediaAssetPreview: React.FC<{
  asset: MediaAssetItem;
  registerObjectURL?: (url: string) => void;
}> = ({ asset, registerObjectURL }) => {
  const { url, loading, handleMediaError, mediaRef } = useSignedMediaURL(asset.id, "preview");

  const previewUrlsRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    const urls = previewUrlsRef.current;
    return () => {
      urls.forEach((u) => {
        safeRevokeObjectURL(u);
      });
      urls.clear();
    };
  }, [asset.id]);

  useEffect(() => {
    if (url && url.startsWith("blob:")) {
      previewUrlsRef.current.add(url);
      registerObjectURL?.(url);
    }
  }, [url, registerObjectURL]);

  const isImg = asset.kind === "image" || asset.mime_type.startsWith("image/");
  const isVid = asset.kind === "video" || asset.mime_type.startsWith("video/");
  const isAud = asset.kind === "audio" || asset.mime_type.startsWith("audio/");

  useEffect(() => {
    if (!isVid) return;
    const vidEl = mediaRef.current as HTMLVideoElement | null;
    return () => {
      if (!vidEl) return;
      try {
        vidEl.pause();
        vidEl.removeAttribute("src");
        vidEl.load();
      } catch {
        // ignore non-DOM test cleanup
      }
    };
  }, [isVid, mediaRef, asset.id]);

  if (loading && !url) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          width: "56px",
          height: "56px",
          background: "var(--bg-surface)",
          borderRadius: "var(--radius-sm)",
          color: "var(--text-muted)",
        }}
      >
        <Loader2 size={16} className="animate-spin" />
      </div>
    );
  }

  if (url && isImg) {
    return (
      <img
        src={url}
        alt={mediaBasename(asset.file_path)}
        onError={handleMediaError}
        style={{
          width: "56px",
          height: "56px",
          objectFit: "cover",
          borderRadius: "var(--radius-sm)",
          border: "var(--border-width) solid var(--border-default)",
        }}
      />
    );
  }

  if (url && isVid) {
    return (
      <video
        ref={mediaRef as React.RefObject<HTMLVideoElement>}
        src={url}
        muted
        playsInline
        onError={handleMediaError}
        style={{
          width: "56px",
          height: "56px",
          objectFit: "cover",
          borderRadius: "var(--radius-sm)",
          border: "var(--border-width) solid var(--border-default)",
        }}
      />
    );
  }

  const FallbackIcon = isVid ? Film : isAud ? FileAudio : ImageIcon;

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        width: "56px",
        height: "56px",
        background: "var(--bg-surface)",
        borderRadius: "var(--radius-sm)",
        color: "var(--text-muted)",
        border: "var(--border-width) solid var(--border-default)",
      }}
    >
      <FallbackIcon size={20} />
    </div>
  );
};

export const MediaLibraryDrawer: React.FC = () => {
  const t = useT();
  const inRouter = useInRouterContext();
  const navRef = useRef<((path: string, opts?: { state?: unknown }) => void) | null>(null);

  const drawerOpen = useMediaLibraryStore((s) => s.drawerOpen);
  const assets = useMediaLibraryStore((s) => s.assets);
  const total = useMediaLibraryStore((s) => s.total);
  const loading = useMediaLibraryStore((s) => s.loading);
  const deletingId = useMediaLibraryStore((s) => s.deletingId);
  const error = useMediaLibraryStore((s) => s.error);
  const kindFilter = useMediaLibraryStore((s) => s.kindFilter);
  const searchQuery = useMediaLibraryStore((s) => s.searchQuery);
  const closeDrawer = useMediaLibraryStore((s) => s.closeDrawer);
  const setKindFilter = useMediaLibraryStore((s) => s.setKindFilter);
  const setSearchQuery = useMediaLibraryStore((s) => s.setSearchQuery);
  const fetchAssets = useMediaLibraryStore((s) => s.fetchAssets);
  const deleteAsset = useMediaLibraryStore((s) => s.deleteAsset);

  const [pendingDelete, setPendingDelete] = useState<MediaAssetItem | null>(null);
  const [deleteDiskFile, setDeleteDiskFile] = useState(false);

  const drawerObjectURLsRef = useRef<Map<string, string>>(new Map());
  useEffect(() => {
    const urlsMap = drawerObjectURLsRef.current;
    return () => {
      urlsMap.forEach((u) => {
        safeRevokeObjectURL(u);
      });
      urlsMap.clear();
    };
  }, []);

  const handleConfirmDelete = async () => {
    if (!pendingDelete) return;
    const targetId = pendingDelete.id;
    const existing = drawerObjectURLsRef.current.get(targetId);
    if (existing) {
      safeRevokeObjectURL(existing);
      drawerObjectURLsRef.current.delete(targetId);
    }
    try {
      await deleteAsset(targetId, deleteDiskFile);
      setPendingDelete(null);
      setDeleteDiskFile(false);
    } catch {
      // Error surfaced via store.error banner inside drawer
    }
  };

  if (!drawerOpen) return null;

  const handleSendToWatermark = (asset: MediaAssetItem) => {
    if (navRef.current) {
      navRef.current("/watermark", {
        state: { asset_id: asset.id, path: asset.file_path },
      });
      closeDrawer();
    }
  };

  const handleAskAgent = (asset: MediaAssetItem) => {
    askAgentAboutMediaAsset({
      assetId: asset.id,
      filePath: asset.file_path,
      mimeType: asset.mime_type,
    });
  };

  const kinds: Array<{ id: string; label: string }> = [
    { id: "", label: t("media.filter.all") },
    { id: "image", label: t("media.filter.images") },
    { id: "video", label: t("media.filter.videos") },
    { id: "audio", label: t("media.filter.audio") },
  ];

  return (
    <aside
      className="agent-drawer"
      style={{
        position: "fixed",
        top: 0,
        right: 0,
        bottom: 0,
        width: "420px",
        maxWidth: "90vw",
        zIndex: 50,
        display: "flex",
        flexDirection: "column",
        background: "var(--bg-surface)",
        borderLeft: "var(--border-width) solid var(--border-default)",
        boxShadow: "var(--shadow-elevation-high, 0 10px 25px rgba(0,0,0,0.25))",
      }}
      aria-label={t("media.library.title")}
    >
      {inRouter && <RouterNavigateBridge navRef={navRef} />}

      {/* Header */}
      <div className="agent-drawer__header">
        <div className="agent-drawer__title-group">
          <FolderArchive size={16} className="agent-drawer__icon" />
          <h2 className="agent-drawer__title">{t("media.library.title")}</h2>
          <Badge>{total}</Badge>
        </div>

        <div className="agent-drawer__actions">
          <Button
            size="sm"
            iconOnly
            onClick={() => void fetchAssets()}
            disabled={loading}
            title={t("media.action.refresh")}
            aria-label={t("media.action.refresh")}
          >
            <RefreshCw size={13} className={loading ? "animate-spin" : ""} />
          </Button>
          <Button
            size="sm"
            iconOnly
            onClick={closeDrawer}
            title={t("tools.cancel")}
            aria-label={t("tools.cancel")}
          >
            <X size={14} />
          </Button>
        </div>
      </div>

      {/* Search and Filter */}
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: "var(--space-2)",
          padding: "var(--space-3)",
          borderBottom: "var(--border-width) solid var(--border-subtle)",
          background: "var(--bg-surface-elevated)",
        }}
      >
        <div style={{ position: "relative", width: "100%" }}>
          <Search
            size={13}
            style={{
              position: "absolute",
              left: "10px",
              top: "50%",
              transform: "translateY(-50%)",
              color: "var(--text-muted)",
            }}
          />
          <input
            type="text"
            className="input-control"
            style={{ paddingLeft: "30px", width: "100%" }}
            placeholder={t("media.search.placeholder")}
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
        </div>

        <div style={{ display: "flex", gap: "var(--space-1)" }}>
          {kinds.map((k) => (
            <button
              key={k.id}
              type="button"
              onClick={() => setKindFilter(k.id)}
              style={{
                padding: "2px 8px",
                fontSize: "var(--text-xs)",
                borderRadius: "var(--radius-sm)",
                border: "var(--border-width) solid var(--border-default)",
                background: kindFilter === k.id ? "var(--btn-primary-bg)" : "var(--bg-surface)",
                color: kindFilter === k.id ? "var(--btn-primary-text)" : "var(--text-secondary)",
                cursor: "pointer",
              }}
            >
              {k.label}
            </button>
          ))}
        </div>

        {error && (
          <div className="alert-box alert-box--error">
            <ErrorText error={toActionError(error)} />
          </div>
        )}
      </div>

      {/* Assets List */}
      <div
        style={{
          flex: 1,
          overflow: "hidden",
          display: "flex",
          flexDirection: "column",
        }}
      >
        {loading && assets.length === 0 && (
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              padding: "var(--space-8)",
              color: "var(--text-muted)",
              gap: "var(--space-2)",
            }}
          >
            <Loader2 size={16} className="animate-spin" />
            <span>{t("media.loading")}</span>
          </div>
        )}

        {!loading && assets.length === 0 && (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              justifyContent: "center",
              padding: "var(--space-8)",
              textAlign: "center",
              color: "var(--text-muted)",
              gap: "var(--space-2)",
            }}
          >
            <FolderArchive size={28} />
            <div style={{ fontWeight: "var(--weight-semibold)", color: "var(--text-primary)" }}>
              {t("media.empty.title")}
            </div>
            <div style={{ fontSize: "var(--text-xs)" }}>{t("media.empty.desc")}</div>
          </div>
        )}

        {assets.length > 0 && (
          <VirtualList
            items={assets}
            height="100%"
            itemHeight={80}
            getItemKey={(asset) => asset.id}
            className="media-library-virtual-list"
            style={{
              flex: 1,
              padding: "var(--space-3)",
            }}
            renderItem={(asset) => (
              <div
                key={asset.id}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "var(--space-3)",
                  padding: "var(--space-2)",
                  background: "var(--bg-surface-elevated)",
                  border: "var(--border-width) solid var(--border-default)",
                  borderRadius: "var(--radius-md)",
                  height: "72px",
                  boxSizing: "border-box",
                }}
              >
                <MediaAssetPreview
                  asset={asset}
                  registerObjectURL={(u) => drawerObjectURLsRef.current.set(asset.id, u)}
                />

                <div style={{ flex: 1, minWidth: 0 }}>
                  <div
                    style={{
                      fontWeight: "var(--weight-semibold)",
                      fontSize: "var(--text-sm)",
                      color: "var(--text-primary)",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                    title={asset.file_path}
                  >
                    {mediaBasename(asset.file_path)}
                  </div>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: "var(--space-2)",
                      fontSize: "var(--text-xs)",
                      color: "var(--text-secondary)",
                      marginTop: "2px",
                    }}
                  >
                    <span>{asset.kind}</span>
                    <span>•</span>
                    <span>{formatBytes(asset.size_bytes)}</span>
                  </div>
                </div>

                <div style={{ display: "flex", alignItems: "center", gap: "var(--space-1)" }}>
                  {(asset.kind === "image" || asset.mime_type.startsWith("image/")) && (
                    <button
                      type="button"
                      className="watermark-icon-btn"
                      onClick={() => handleSendToWatermark(asset)}
                      title={t("cross.sendToWatermark")}
                      aria-label={t("cross.sendToWatermark")}
                    >
                      <Sparkles size={12} />
                    </button>
                  )}

                  <button
                    type="button"
                    className="watermark-icon-btn"
                    onClick={() => handleAskAgent(asset)}
                    title={t("cross.askAgent")}
                    aria-label={t("cross.askAgent")}
                  >
                    <Bot size={12} />
                  </button>

                  <button
                    type="button"
                    className="watermark-icon-btn"
                    onClick={() => {
                      setDeleteDiskFile(false);
                      setPendingDelete(asset);
                    }}
                    disabled={deletingId === asset.id}
                    title={t("tools.delete")}
                    aria-label={t("tools.delete")}
                  >
                    {deletingId === asset.id ? (
                      <Loader2 size={12} className="animate-spin" />
                    ) : (
                      <Trash2 size={12} />
                    )}
                  </button>
                </div>
              </div>
            )}
          />
        )}
      </div>

      {pendingDelete && (
        <Dialog
          size="sm"
          title={t("media.deleteConfirm.title")}
          busy={deletingId === pendingDelete.id}
          onClose={() => {
            setPendingDelete(null);
            setDeleteDiskFile(false);
          }}
          footer={(requestClose) => (
            <div style={{ display: "flex", justifyContent: "flex-end", gap: "var(--space-2)" }}>
              <Button size="sm" onClick={requestClose} disabled={deletingId === pendingDelete.id}>
                {t("tools.cancel")}
              </Button>
              <Button
                size="sm"
                variant="primary"
                disabled={deletingId === pendingDelete.id}
                onClick={() => void handleConfirmDelete()}
              >
                {t("tools.delete")}
              </Button>
            </div>
          )}
        >
          <p style={{ marginBottom: "var(--space-2)" }}>
            {t("media.deleteConfirm.desc", { name: mediaBasename(pendingDelete.file_path) })}
          </p>
          <label style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
            <input
              type="checkbox"
              checked={deleteDiskFile}
              onChange={(e) => setDeleteDiskFile(e.target.checked)}
            />
            <span>{t("media.deleteConfirm.deleteDisk")}</span>
          </label>
        </Dialog>
      )}
    </aside>
  );
};

export default MediaLibraryDrawer;
