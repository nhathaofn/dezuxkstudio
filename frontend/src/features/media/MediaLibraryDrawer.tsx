import React, { useEffect, useRef } from "react";
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
import type { MediaAssetItem } from "../../contracts/generated";
import { useSignedMediaURL } from "../../lib/hooks/useSignedMediaURL";
import { mediaBasename } from "../../lib/rtClient/media";
import { useT, type MessageKey } from "../../lib/i18n";
import { askAgentAboutMediaAsset } from "../agent/screenContext";
import { useMediaLibraryStore } from "./useMediaLibraryStore";

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

const MediaAssetPreview: React.FC<{ asset: MediaAssetItem }> = ({ asset }) => {
  const { url, loading, handleMediaError, mediaRef } = useSignedMediaURL(
    asset.id,
    "preview"
  );

  const isImg = asset.kind === "image" || asset.mime_type.startsWith("image/");
  const isVid = asset.kind === "video" || asset.mime_type.startsWith("video/");
  const isAud = asset.kind === "audio" || asset.mime_type.startsWith("audio/");

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

  const {
    drawerOpen,
    assets,
    total,
    loading,
    deletingId,
    kindFilter,
    searchQuery,
    closeDrawer,
    setKindFilter,
    setSearchQuery,
    fetchAssets,
    deleteAsset,
  } = useMediaLibraryStore();

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
    { id: "", label: t("media.filter.all" as MessageKey) || "All" },
    { id: "image", label: t("media.filter.images" as MessageKey) || "Images" },
    { id: "video", label: t("media.filter.videos" as MessageKey) || "Videos" },
    { id: "audio", label: t("media.filter.audio" as MessageKey) || "Audio" },
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
      aria-label="Media Library"
    >
      {inRouter && <RouterNavigateBridge navRef={navRef} />}

      {/* Header */}
      <div className="agent-drawer__header">
        <div className="agent-drawer__title-group">
          <FolderArchive size={16} className="agent-drawer__icon" />
          <h2 className="agent-drawer__title">{t("media.library.title" as MessageKey) || "Media Library"}</h2>
          <Badge>{total}</Badge>
        </div>

        <div className="agent-drawer__actions">
          <Button
            size="sm"
            iconOnly
            onClick={() => void fetchAssets()}
            disabled={loading}
            title={t("media.action.refresh" as MessageKey) || "Refresh"}
            aria-label="Refresh library"
          >
            <RefreshCw size={13} className={loading ? "animate-spin" : ""} />
          </Button>
          <Button
            size="sm"
            iconOnly
            onClick={closeDrawer}
            title={t("tools.cancel" as MessageKey) || "Close"}
            aria-label="Close library"
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
            placeholder={t("media.search.placeholder" as MessageKey) || "Search registered assets..."}
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
                background:
                  kindFilter === k.id ? "var(--btn-primary-bg)" : "var(--bg-surface)",
                color:
                  kindFilter === k.id ? "var(--btn-primary-text)" : "var(--text-secondary)",
                cursor: "pointer",
              }}
            >
              {k.label}
            </button>
          ))}
        </div>
      </div>

      {/* Assets List */}
      <div
        style={{
          flex: 1,
          overflowY: "auto",
          padding: "var(--space-3)",
          display: "flex",
          flexDirection: "column",
          gap: "var(--space-2)",
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
            <span>{t("media.loading" as MessageKey) || "Loading media assets..."}</span>
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
              {t("media.empty.title" as MessageKey) || "No media assets"}
            </div>
            <div style={{ fontSize: "var(--text-xs)" }}>
              {t("media.empty.desc" as MessageKey) || "Generated and imported media files will appear here."}
            </div>
          </div>
        )}

        {assets.map((asset) => (
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
            }}
          >
            <MediaAssetPreview asset={asset} />

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
                  title={t("cross.sendToWatermark" as MessageKey) || "Send to Watermark"}
                  aria-label="Send to Watermark"
                >
                  <Sparkles size={12} />
                </button>
              )}

              <button
                type="button"
                className="watermark-icon-btn"
                onClick={() => handleAskAgent(asset)}
                title={t("cross.askAgent" as MessageKey) || "Ask Agent about this file"}
                aria-label="Ask Agent about this file"
              >
                <Bot size={12} />
              </button>

              <button
                type="button"
                className="watermark-icon-btn"
                onClick={() => void deleteAsset(asset.id)}
                disabled={deletingId === asset.id}
                title={t("tools.delete" as MessageKey) || "Delete"}
                aria-label="Delete media asset"
              >
                {deletingId === asset.id ? (
                  <Loader2 size={12} className="animate-spin" />
                ) : (
                  <Trash2 size={12} />
                )}
              </button>
            </div>
          </div>
        ))}
      </div>
    </aside>
  );
};

export default MediaLibraryDrawer;
