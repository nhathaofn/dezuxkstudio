import React, { useCallback, useEffect, useRef, useState } from "react";
import { Eraser, Paintbrush, RotateCcw } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { ErrorText } from "../../components/ui/ErrorText";
import { appConfig } from "../../config/appConfig";
import { NodeTypes } from "../../contracts/generated";
import { useSignedMediaURL } from "../../lib/hooks/useSignedMediaURL";
import { useT } from "../../lib/i18n";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import {
  buildMaskDataURLFromStrokes,
  clampGridBound,
  strokeMaskPaths,
  type MaskStroke,
} from "./phase2Helpers";
import { useStudioStore, type StudioMaskModalTarget } from "./useStudioStore";

export const StudioMaskEditorModal: React.FC = () => {
  const target = useStudioStore((s) => s.maskModalTarget);
  if (!target) return null;
  return <StudioMaskEditorBody key={`${target.targetId}:${target.imagePath}`} target={target} />;
};

const StudioMaskEditorBody: React.FC<{ target: StudioMaskModalTarget }> = ({ target }) => {
  const t = useT();
  const theme = usePreferencesStore((s) => s.prefs.theme);
  const maskSpec = useNodeSpec(NodeTypes.MASK_EDIT);
  const brushSpec = findParam(maskSpec, "brush_size");
  const minBrushPx = brushSpec?.min ?? appConfig.studio.maskEditor.minBrushPx;
  const maxBrushPx = brushSpec?.max ?? appConfig.studio.maskEditor.maxBrushPx;
  const defaultBrushPx = clampGridBound(
    brushSpec?.default
      ? Number(brushSpec.default) || appConfig.studio.maskEditor.defaultBrushPx
      : appConfig.studio.maskEditor.defaultBrushPx,
    minBrushPx,
    maxBrushPx
  );

  const openMaskModal = useStudioStore((s) => s.openMaskModal);
  const applyMaskEdit = useStudioStore((s) => s.applyMaskEdit);
  const saving = useStudioStore((s) => s.saving);
  const error = useStudioStore((s) => s.error);
  const clearError = useStudioStore((s) => s.clearError);

  const committedCanvasRef = useRef<HTMLCanvasElement>(null);
  const activeCanvasRef = useRef<HTMLCanvasElement>(null);
  const activeStrokeRef = useRef<MaskStroke | null>(null);
  const brushColorRef = useRef<string>("");

  const [imgSize, setImgSize] = useState<{ w: number; h: number }>({
    w: appConfig.studio.maskEditor.defaultWidthPx,
    h: appConfig.studio.maskEditor.defaultHeightPx,
  });
  const [imgLoaded, setImgLoaded] = useState(!target.assetId);
  const [brushSize, setBrushSize] = useState<number>(defaultBrushPx);
  const [strokes, setStrokes] = useState<MaskStroke[]>([]);
  const [prompt, setPrompt] = useState(() => target.defaultPrompt ?? "");
  const { url, handleMediaError } = useSignedMediaURL(target.assetId || null, "preview");

  const resolveBrushColor = useCallback(() => {
    const cvs = activeCanvasRef.current ?? committedCanvasRef.current;
    if (typeof window !== "undefined" && cvs) {
      const tokenColor = getComputedStyle(cvs)
        .getPropertyValue("--studio-mask-brush-stroke")
        .trim();
      if (tokenColor) {
        brushColorRef.current = tokenColor;
        return tokenColor;
      }
    }
    if (typeof document !== "undefined") {
      const fallback = getComputedStyle(document.documentElement)
        .getPropertyValue("--accent")
        .trim();
      brushColorRef.current = fallback;
      return fallback;
    }
    return "";
  }, []);

  // Re-cache brush stroke CSS variable only when theme/canvas mounts, never per pointermove.
  useEffect(() => {
    resolveBrushColor();
  }, [theme, imgSize, resolveBrushColor]);

  // Redraw committed strokes layer only when strokes commit/undo/clear or dimensions/theme change.
  useEffect(() => {
    const cvs = committedCanvasRef.current;
    if (!cvs) return;
    const ctx = cvs.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, cvs.width, cvs.height);
    ctx.strokeStyle = brushColorRef.current || resolveBrushColor();
    strokeMaskPaths(ctx, strokes);
  }, [strokes, imgSize, theme, resolveBrushColor]);

  const drawActiveStroke = () => {
    const cvs = activeCanvasRef.current;
    if (!cvs) return;
    const ctx = cvs.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, cvs.width, cvs.height);
    const active = activeStrokeRef.current;
    if (!active) return;
    ctx.strokeStyle = brushColorRef.current || resolveBrushColor();
    strokeMaskPaths(ctx, [active]);
  };

  const pointFromEvent = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const cvs = e.currentTarget;
    const rect = cvs.getBoundingClientRect();
    const scaleX = cvs.width / Math.max(1, rect.width);
    const scaleY = cvs.height / Math.max(1, rect.height);
    return {
      x: (e.clientX - rect.left) * scaleX,
      y: (e.clientY - rect.top) * scaleY,
    };
  };

  const finishActiveStroke = () => {
    const completed = activeStrokeRef.current;
    activeStrokeRef.current = null;
    drawActiveStroke();
    if (completed && completed.points.length > 0) {
      setStrokes((prev) => [...prev, completed]);
    }
  };

  const handleApply = () => {
    if (!prompt.trim() || strokes.length === 0) return;
    const dataUrl = buildMaskDataURLFromStrokes(imgSize.w, imgSize.h, strokes);
    void applyMaskEdit({
      project_id: target.projectId,
      episode_id: target.episodeId,
      target_kind: target.targetKind,
      target_id: target.targetId,
      image_path: target.imagePath,
      mask_data_base64: dataUrl,
      prompt: prompt.trim(),
    });
  };

  const renderCanvases = (standalone: boolean) => (
    <>
      <canvas
        ref={committedCanvasRef}
        width={imgSize.w}
        height={imgSize.h}
        className={
          standalone ? "studio-mask-canvas studio-mask-canvas--standalone" : "studio-mask-canvas"
        }
      />
      <canvas
        ref={activeCanvasRef}
        width={imgSize.w}
        height={imgSize.h}
        className="studio-mask-canvas"
        onPointerDown={(e) => {
          if (url && !imgLoaded) return;
          e.currentTarget.setPointerCapture(e.pointerId);
          const pt = pointFromEvent(e);
          activeStrokeRef.current = { size: brushSize, points: [pt] };
          drawActiveStroke();
        }}
        onPointerMove={(e) => {
          const active = activeStrokeRef.current;
          if (!active || (url && !imgLoaded)) return;
          const pt = pointFromEvent(e);
          active.points.push(pt);
          drawActiveStroke();
        }}
        onPointerUp={finishActiveStroke}
        onPointerCancel={finishActiveStroke}
        onLostPointerCapture={finishActiveStroke}
      />
    </>
  );

  return (
    <Dialog title={t("studio.maskEdit.title")} size="lg" onClose={() => openMaskModal(null)}>
      <div className="studio-phase2-stack">
        <ErrorText error={error} onDismiss={clearError} />
        <p className="studio-field-hint">{t("studio.maskEdit.hint")}</p>
        <div className="studio-grid-controls">
          <label className="studio-field-inline">
            <span>{t("studio.maskEdit.brushSizePx", { size: brushSize })}</span>
            <input
              type="range"
              min={minBrushPx}
              max={maxBrushPx}
              value={brushSize}
              aria-label={t("studio.maskEdit.brushSize")}
              onChange={(e) => setBrushSize(Number(e.target.value))}
            />
          </label>
          <Button
            size="sm"
            variant="secondary"
            disabled={strokes.length === 0}
            onClick={() => setStrokes((s) => s.slice(0, -1))}
          >
            <RotateCcw size={13} />
            {t("studio.maskEdit.undo")}
          </Button>
          <Button
            size="sm"
            variant="secondary"
            disabled={strokes.length === 0}
            onClick={() => {
              activeStrokeRef.current = null;
              drawActiveStroke();
              setStrokes([]);
            }}
          >
            <Eraser size={13} />
            {t("studio.maskEdit.clear")}
          </Button>
        </div>

        <div className="studio-mask-canvas-wrap">
          {url ? (
            <div className="studio-media-overlay-stage">
              <img
                src={url}
                alt={t("studio.maskEdit.baseImageAlt")}
                className="studio-mask-base-img"
                onError={(e) => {
                  setImgLoaded(true);
                  handleMediaError(e);
                }}
                onLoad={(e) => {
                  const el = e.currentTarget;
                  if (el.naturalWidth > 0 && el.naturalHeight > 0) {
                    setImgSize({ w: el.naturalWidth, h: el.naturalHeight });
                  }
                  setImgLoaded(true);
                }}
              />
              {renderCanvases(false)}
            </div>
          ) : (
            <div className="studio-media-overlay-stage">{renderCanvases(true)}</div>
          )}
        </div>

        <label className="studio-field-col">
          <span>{t("studio.maskEdit.promptLabel")}</span>
          <input
            type="text"
            className="studio-input"
            value={prompt}
            placeholder={t("studio.maskEdit.promptPlaceholder")}
            onChange={(e) => setPrompt(e.target.value)}
          />
        </label>

        <div className="studio-dialog-footer">
          <Button variant="secondary" onClick={() => openMaskModal(null)}>
            {t("dialog.close")}
          </Button>
          <Button
            variant="primary"
            disabled={saving || !prompt.trim() || strokes.length === 0}
            onClick={handleApply}
          >
            <Paintbrush size={14} />
            {t("studio.maskEdit.apply")}
          </Button>
        </div>
      </div>
    </Dialog>
  );
};
