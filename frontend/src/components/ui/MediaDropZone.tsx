import React, { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import { UploadCloud, type LucideIcon } from "lucide-react";
import { isDesktop } from "../../lib/bridge";
import { subscribeFileDrop } from "../../lib/fileDropDispatcher";
import { pickMediaFiles } from "../../lib/filePicker";
import { useT } from "../../lib/i18n";

export interface MediaDropZoneHandle {
  browse: () => void;
}

export interface MediaDropZoneProps {
  /** Label for native file picker dialog or accessibility. */
  label?: string;
  /** Primary hint or title displayed inside the dropzone. */
  title?: string;
  /** Secondary subtitle or description displayed inside the dropzone. */
  description?: string;
  /** MIME types or file extensions accepted. Defaults to image types. */
  accept?: string;
  /** Whether multiple files can be selected or dropped. Defaults to false. */
  multiple?: boolean;
  /** Whether the dropzone is disabled. */
  disabled?: boolean;
  /** Optional custom icon component to display. */
  icon?:
    | LucideIcon
    | React.ComponentType<{ size?: number; className?: string; style?: React.CSSProperties }>;
  /** Additional custom class names for the container. */
  className?: string;
  /** Number of currently selected items to reflect in the UI. */
  selectedCount?: number;
  /** Summary text when files are selected (e.g. "3 images ready"). */
  selectedSummary?: string;
  /** Callback when files are selected via HTML file input or web drag-and-drop. */
  onFilesSelected?: (files: File[]) => void;
  /** Callback when native local file paths are selected via desktop dialog or native drag-and-drop. */
  onPathsSelected?: (paths: string[]) => void;
  /** Unified callback delivering both files and native paths if available. */
  onSelected?: (files: File[], paths?: string[]) => void;
  /** Optional custom child nodes or render function. */
  children?:
    React.ReactNode | ((state: { isDragging: boolean; browse: () => void }) => React.ReactNode);
}

const DEFAULT_IMAGE_ACCEPT = "image/png,image/jpeg,image/webp,image/*";

/**
 * MediaInputNode (MediaDropZone)
 *
 * Universal input node for media and file ingestion across DezuxkStudio.
 * Encapsulates:
 * 1. Safe desktop native file picker dialogs (no focus collisions or WebView2 crashes).
 * 2. Cross-platform drag and drop (native Wails onFileDrop + browser Drag & Drop).
 * 3. Graceful fallback for browser/remote client mode.
 */
export const MediaDropZone = forwardRef<MediaDropZoneHandle, MediaDropZoneProps>(
  (
    {
      label,
      title,
      description,
      accept = DEFAULT_IMAGE_ACCEPT,
      multiple = false,
      disabled = false,
      icon: Icon = UploadCloud,
      className = "",
      selectedCount = 0,
      selectedSummary,
      onFilesSelected,
      onPathsSelected,
      onSelected,
      children,
    },
    ref
  ) => {
    const t = useT();
    const resolvedLabel = label ?? t("dropzone.selectMedia");
    const fileInputRef = useRef<HTMLInputElement | null>(null);
    const containerRef = useRef<HTMLDivElement | null>(null);
    const [isDragging, setIsDragging] = useState(false);

    const inferredKind: "image" | "video" | "audio" = accept.includes("video")
      ? "video"
      : accept.includes("audio")
        ? "audio"
        : "image";

    const handleBrowse = async () => {
      if (disabled) return;

      if (isDesktop()) {
        const res = await pickMediaFiles({
          title: resolvedLabel,
          multiple,
          accept,
          kind: inferredKind,
        });

        if (res.paths.length > 0) {
          onPathsSelected?.(res.paths);
          onSelected?.([], res.paths);
        }
        return;
      }

      // Browser / remote mode: trigger the local input element directly
      fileInputRef.current?.click();
    };

    useImperativeHandle(ref, () => ({
      browse: handleBrowse,
    }));

    // Centralized native desktop Wails onFileDrop support so multiple dropzones do not clobber each other
    useEffect(() => {
      if (disabled || !isDesktop()) return;

      const unsub = subscribeFileDrop((x, y, paths) => {
        setIsDragging(false);
        const target =
          typeof document.elementFromPoint === "function" ? document.elementFromPoint(x, y) : null;
        const rect = containerRef.current?.getBoundingClientRect();
        const isInsideRect =
          rect !== undefined &&
          rect.width > 0 &&
          rect.height > 0 &&
          x >= rect.left &&
          x <= rect.right &&
          y >= rect.top &&
          y <= rect.bottom;
        const isInsideContainer =
          isInsideRect ||
          (containerRef.current !== null &&
            target !== null &&
            (containerRef.current === target || containerRef.current.contains(target)));

        if (isInsideContainer && paths && paths.length > 0) {
          const validPaths = multiple ? paths : [paths[0]];
          onPathsSelected?.(validPaths);
          onSelected?.([], validPaths);
        }
      });

      return () => {
        unsub();
      };
    }, [disabled, multiple, onPathsSelected, onSelected]);

    const handleDragOver = (e: React.DragEvent) => {
      e.preventDefault();
      if (!disabled) {
        setIsDragging(true);
      }
    };

    const handleDragLeave = (e: React.DragEvent) => {
      e.preventDefault();
      setIsDragging(false);
    };

    const handleDrop = (e: React.DragEvent) => {
      e.preventDefault();
      setIsDragging(false);
      if (disabled) return;

      // In desktop mode, native Wails onFileDrop handles absolute path delivery
      if (isDesktop()) return;

      const files = Array.from(e.dataTransfer.files || []);
      if (files.length === 0) return;

      const chosen = multiple ? files : [files[0]];
      const nativePaths = chosen
        .map((f) => (f as unknown as { path?: string }).path)
        .filter((p): p is string => Boolean(p));

      onFilesSelected?.(chosen);
      if (nativePaths.length > 0) {
        onPathsSelected?.(nativePaths);
      }
      onSelected?.(chosen, nativePaths.length > 0 ? nativePaths : undefined);
    };

    const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
      const files = Array.from(e.target.files || []);
      e.target.value = "";
      if (files.length === 0) return;

      const chosen = multiple ? files : [files[0]];
      const nativePaths = chosen
        .map((f) => (f as unknown as { path?: string }).path)
        .filter((p): p is string => Boolean(p));

      onFilesSelected?.(chosen);
      if (nativePaths.length > 0) {
        onPathsSelected?.(nativePaths);
      }
      onSelected?.(chosen, nativePaths.length > 0 ? nativePaths : undefined);
    };

    const handleKeyDown = (e: React.KeyboardEvent) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        void handleBrowse();
      }
    };

    const containerClasses = [
      "watermark-dropzone",
      isDragging ? "watermark-dropzone--active" : "",
      disabled ? "is-disabled" : "",
      className,
    ]
      .filter(Boolean)
      .join(" ");

    return (
      <div
        ref={containerRef}
        role="button"
        tabIndex={disabled ? -1 : 0}
        className={containerClasses}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
        onClick={handleBrowse}
        onKeyDown={handleKeyDown}
        aria-disabled={disabled}
        aria-label={resolvedLabel}
      >
        <input
          ref={fileInputRef}
          type="file"
          accept={accept}
          multiple={multiple}
          disabled={disabled}
          className="watermark-dropzone__input"
          onChange={handleInputChange}
          onClick={(e) => e.stopPropagation()}
        />

        {typeof children === "function" ? (
          children({ isDragging, browse: handleBrowse })
        ) : children ? (
          children
        ) : (
          <>
            <Icon size={22} className="watermark-dropzone__icon" />
            <div className="watermark-dropzone__title">
              {selectedCount > 0
                ? selectedSummary || t("dropzone.filesSelected", { count: selectedCount })
                : title || t("dropzone.prompt")}
            </div>
            {description && <div className="watermark-dropzone__desc">{description}</div>}
          </>
        )}
      </div>
    );
  }
);

MediaDropZone.displayName = "MediaDropZone";

/** Alias reflecting its architectural role as DezuxkStudio's Input Node for media. */
export const MediaInputNode = MediaDropZone;
