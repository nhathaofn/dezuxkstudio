import React, { useEffect, useRef, useState } from "react";
import { Check, Eraser, Grid, Paintbrush, RotateCcw, ShieldCheck, ShieldAlert } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { appConfig } from "../../config/appConfig";
import {
  NodeTypes,
  type StudioMediaTargetKind,
  type StudioQCStatus,
} from "../../contracts/generated";
import { useSignedMediaURL } from "../../lib/hooks/useSignedMediaURL";
import { isMessageKey, translateServerMessage, useT } from "../../lib/i18n";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { formatUSD } from "./studioFormatters";
import {
  targetVersionKey,
  useStudioStore,
  type StudioGridModalTarget,
  type StudioMaskModalTarget,
} from "./useStudioStore";
import {
  buildMaskDataURLFromStrokes,
  computeGridDividerPercents,
  strokeMaskPaths,
  type MaskStroke,
} from "./phase2Helpers";

export const StudioPreflightDialog: React.FC = () => {
  const t = useT();
  const pending = useStudioStore((s) => s.pendingPreflight);
  const cancel = useStudioStore((s) => s.cancelPreflight);
  const confirm = useStudioStore((s) => s.confirmPreflight);
  if (!pending) return null;

  const { preflight } = pending;
  const stageKey = `studio.batchStage.${pending.stage}`;
  const stageLabel = isMessageKey(stageKey) ? t(stageKey) : pending.stage;
  const unitKey = `studio.cost.unit.${preflight.unit}`;
  const unitLabel = isMessageKey(unitKey) ? t(unitKey) : preflight.unit;
  const usdDigits = appConfig.studio.ui.detailUsdDigits;

  return (
    <Dialog
      title={t("studio.approval.preflightTitle")}
      size="sm"
      onClose={cancel}
      footer={
        <div className="studio-dialog-footer">
          <Button variant="secondary" onClick={cancel}>
            {t("dialog.close")}
          </Button>
          <Button variant="primary" disabled={!preflight.ready} onClick={() => void confirm()}>
            <ShieldCheck size={14} />
            {t("studio.approval.confirmRun")}
          </Button>
        </div>
      }
    >
      <div className="studio-phase2-stack">
        <div className="studio-phase2-meta-grid">
          <Badge>{stageLabel}</Badge>
          <Badge>{t("studio.approval.preflightItems", { count: preflight.item_count })}</Badge>
          <Badge>
            {t("studio.approval.preflightEst", {
              usd: formatUSD(preflight.estimated_usd, usdDigits),
            })}
          </Badge>
          <Badge>{t("studio.approval.preflightSec", { sec: preflight.estimated_sec })}</Badge>
        </div>
        {preflight.model_id && (
          <div className="studio-field-hint">
            {preflight.provider_id}/{preflight.model_id} ·{" "}
            {t("studio.cost.perUnit", {
              price: formatUSD(preflight.unit_price_usd, usdDigits),
              unit: unitLabel,
            })}
          </div>
        )}
        {preflight.warning_items && preflight.warning_items.length > 0
          ? preflight.warning_items.map((w, idx) => (
              <div key={idx} className="studio-notice studio-notice--warn">
                {translateServerMessage(t, w.msg_key, w.msg_params) ?? w.message}
              </div>
            ))
          : (preflight.warnings ?? []).map((w, idx) => (
              <div key={idx} className="studio-notice studio-notice--warn">
                {w}
              </div>
            ))}
        {preflight.blocker_items && preflight.blocker_items.length > 0
          ? preflight.blocker_items.map((b, idx) => (
              <div key={idx} className="studio-notice studio-notice--error">
                {translateServerMessage(t, b.msg_key, b.msg_params) ?? b.message}
              </div>
            ))
          : (preflight.blockers ?? []).map((b, idx) => (
              <div key={idx} className="studio-notice studio-notice--error">
                {b}
              </div>
            ))}
        {!preflight.ready && (
          <div className="studio-notice studio-notice--error">{t("studio.approval.blocked")}</div>
        )}
      </div>
    </Dialog>
  );
};

export const StudioVersionDrawer: React.FC = () => {
  const t = useT();
  const target = useStudioStore((s) => s.versionDrawerTarget);
  const openVersionDrawer = useStudioStore((s) => s.openVersionDrawer);
  const versionsByTarget = useStudioStore((s) => s.versionsByTarget);
  const qcByTarget = useStudioStore((s) => s.qcByTarget);
  const selectVersion = useStudioStore((s) => s.selectVersion);
  const runQCReview = useStudioStore((s) => s.runQCReview);
  const saving = useStudioStore((s) => s.saving);

  if (!target) return null;
  const key = targetVersionKey(target.targetKind, target.targetId);
  const versions = versionsByTarget[key] ?? [];
  const latestQC = qcByTarget[key];
  const activeVersion = versions.find((v) => v.is_selected) ?? versions[0];

  const handleRunQC = () => {
    void runQCReview({
      project_id: target.projectId,
      episode_id: target.episodeId,
      target_kind: target.targetKind,
      target_id: target.targetId,
      media_version_id: activeVersion?.id,
      media_path: activeVersion?.media_path || target.mediaPath,
      reference_paths: target.referencePaths,
      prompt_expected: target.promptExpected,
    });
  };

  return (
    <Dialog
      title={`${t("studio.versions.title")} · ${target.label}`}
      size="lg"
      onClose={() => openVersionDrawer(null)}
    >
      <div className="studio-phase2-stack">
        <div className="studio-phase2-toolbar">
          <Button
            size="sm"
            variant="secondary"
            disabled={saving || !target.mediaPath}
            onClick={handleRunQC}
          >
            {latestQC?.verdict === ("pass" satisfies StudioQCStatus) ? (
              <ShieldCheck size={14} />
            ) : (
              <ShieldAlert size={14} />
            )}
            {t("studio.qc.button")}
          </Button>
          {latestQC && (
            <div className="studio-phase2-meta-grid">
              <Badge
                className={
                  latestQC.verdict === ("pass" satisfies StudioQCStatus)
                    ? "studio-badge--ok"
                    : latestQC.verdict === ("fail" satisfies StudioQCStatus)
                      ? "studio-badge--error"
                      : "studio-badge--warn"
                }
              >
                {t(`studio.qc.verdict.${latestQC.verdict}` as never)}
              </Badge>
              <Badge>{t("studio.qc.score", { score: latestQC.overall_score.toFixed(1) })}</Badge>
            </div>
          )}
        </div>

        {latestQC ? (
          <div className="studio-qc-box">
            {latestQC.vision_summary && (
              <p className="studio-qc-summary">{latestQC.vision_summary}</p>
            )}
            {(latestQC.issues ?? []).length > 0 && (
              <ul className="studio-qc-issues">
                {(latestQC.issues ?? []).map((iss, idx) => {
                  const sevKey = `studio.qc.severity.${iss.severity}`;
                  const catKey = `studio.qc.category.${iss.category}`;
                  const sevLabel = isMessageKey(sevKey) ? t(sevKey) : iss.severity;
                  const catLabel = isMessageKey(catKey) ? t(catKey) : iss.category;
                  return (
                    <li key={idx}>
                      <strong>
                        {t("studio.qc.issueTag", { severity: sevLabel, category: catLabel })}
                      </strong>{" "}
                      {translateServerMessage(t, iss.msg_key, iss.msg_params) ?? iss.message}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        ) : (
          <p className="studio-field-hint">{t("studio.qc.noReport")}</p>
        )}

        {versions.length === 0 ? (
          <p className="studio-field-hint">{t("studio.versions.empty")}</p>
        ) : (
          <div className="studio-version-grid">
            {versions.map((v) => (
              <div
                key={v.id}
                className={`studio-version-card ${v.is_selected ? "studio-version-card--selected" : ""}`}
              >
                <div className="studio-version-card__head">
                  <strong>{t("studio.versions.versionLabel", { num: v.version_num })}</strong>
                  <div className="studio-phase2-meta-grid">
                    {v.is_selected && (
                      <Badge className="studio-badge--ok">{t("studio.versions.active")}</Badge>
                    )}
                    <Badge>{t(`studio.qc.verdict.${v.qc_status}` as never)}</Badge>
                  </div>
                </div>
                <div className="studio-field-hint">
                  {v.model_ref || v.source_node} ·{" "}
                  {formatUSD(v.cost_usd, appConfig.studio.ui.detailUsdDigits)}
                </div>
                {v.prompt_used && <p className="studio-version-prompt">{v.prompt_used}</p>}
                <div className="studio-field-hint studio-path-truncate">{v.media_path}</div>
                {!v.is_selected && (
                  <Button
                    size="sm"
                    variant="primary"
                    disabled={saving}
                    onClick={() => void selectVersion(v.id)}
                  >
                    <Check size={13} />
                    {t("studio.versions.select")}
                  </Button>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </Dialog>
  );
};

const equalGridWeights = (count: number) =>
  Array.from({ length: count }, () =>
    Math.round(appConfig.studio.gridSplitter.weightTotalPct / Math.max(1, count))
  );

const clampGridBound = (val: number, minVal?: number, maxVal?: number) => {
  let out = val;
  if (minVal !== undefined) out = Math.max(minVal, out);
  if (maxVal !== undefined) out = Math.min(maxVal, out);
  return out;
};

export const StudioGridSplitterModal: React.FC = () => {
  const gridSpec = useNodeSpec(NodeTypes.GRID_SPLIT);
  const target = useStudioStore((s) => s.gridModalTarget);
  if (!target) return null;

  const rowsParam = findParam(gridSpec, "rows");
  const colsParam = findParam(gridSpec, "cols");
  const marginParam = findParam(gridSpec, "margin_px");
  const gutterParam = findParam(gridSpec, "gutter_px");

  const defaultRows = rowsParam?.default ? Number(rowsParam.default) : (rowsParam?.min ?? 1);
  const defaultCols = colsParam?.default ? Number(colsParam.default) : (colsParam?.min ?? 1);
  const defaultMargin = marginParam?.default
    ? Number(marginParam.default)
    : (marginParam?.min ?? 0);
  const defaultGutter = gutterParam?.default
    ? Number(gutterParam.default)
    : (gutterParam?.min ?? 0);

  return (
    <StudioGridSplitterBody
      key={`${target.targetId}:${target.imagePath}:${defaultRows}:${defaultCols}:${defaultMargin}:${defaultGutter}`}
      target={target}
      defaultRows={defaultRows}
      defaultCols={defaultCols}
      defaultMargin={defaultMargin}
      defaultGutter={defaultGutter}
      rowsMin={rowsParam?.min}
      rowsMax={rowsParam?.max}
      colsMin={colsParam?.min}
      colsMax={colsParam?.max}
      marginMin={marginParam?.min}
      marginMax={marginParam?.max}
      gutterMin={gutterParam?.min}
      gutterMax={gutterParam?.max}
    />
  );
};

interface StudioGridSplitterBodyProps {
  target: StudioGridModalTarget;
  defaultRows: number;
  defaultCols: number;
  defaultMargin: number;
  defaultGutter: number;
  rowsMin?: number;
  rowsMax?: number;
  colsMin?: number;
  colsMax?: number;
  marginMin?: number;
  marginMax?: number;
  gutterMin?: number;
  gutterMax?: number;
}

const StudioGridSplitterBody: React.FC<StudioGridSplitterBodyProps> = ({
  target,
  defaultRows,
  defaultCols,
  defaultMargin,
  defaultGutter,
  rowsMin,
  rowsMax,
  colsMin,
  colsMax,
  marginMin,
  marginMax,
  gutterMin,
  gutterMax,
}) => {
  const t = useT();
  const openGridModal = useStudioStore((s) => s.openGridModal);
  const splitGrid = useStudioStore((s) => s.splitGrid);
  const versionsByTarget = useStudioStore((s) => s.versionsByTarget);
  const selectVersion = useStudioStore((s) => s.selectVersion);
  const saving = useStudioStore((s) => s.saving);

  const [rows, setRows] = useState<number>(defaultRows);
  const [cols, setCols] = useState<number>(defaultCols);
  const [marginPx, setMarginPx] = useState<number>(defaultMargin);
  const [gutterPx, setGutterPx] = useState<number>(defaultGutter);
  const [rowWeights, setRowWeights] = useState<number[]>(() => equalGridWeights(defaultRows));
  const [colWeights, setColWeights] = useState<number[]>(() => equalGridWeights(defaultCols));
  const { url, handleMediaError } = useSignedMediaURL(target.assetId || null, "preview");

  const handleRowsChange = (nextRows: number) => {
    const clamped = clampGridBound(nextRows, rowsMin ?? 1, rowsMax);
    setRows(clamped);
    setRowWeights((prev) =>
      Array.from(
        { length: clamped },
        (_, i) =>
          prev[i] ?? Math.round(appConfig.studio.gridSplitter.weightTotalPct / Math.max(1, clamped))
      )
    );
  };

  const handleColsChange = (nextCols: number) => {
    const clamped = clampGridBound(nextCols, colsMin ?? 1, colsMax);
    setCols(clamped);
    setColWeights((prev) =>
      Array.from(
        { length: clamped },
        (_, i) =>
          prev[i] ?? Math.round(appConfig.studio.gridSplitter.weightTotalPct / Math.max(1, clamped))
      )
    );
  };

  const hCuts = computeGridDividerPercents(rowWeights);
  const vCuts = computeGridDividerPercents(colWeights);
  const panelKey = targetVersionKey(
    target.targetKind || ("grid_panel" satisfies StudioMediaTargetKind),
    target.targetId || target.projectId
  );
  const recentPanels = (versionsByTarget[panelKey] ?? []).filter(
    (v) => v.source_node === NodeTypes.GRID_SPLIT
  );

  const handleSplit = () => {
    void splitGrid({
      project_id: target.projectId,
      episode_id: target.episodeId,
      target_kind: target.targetKind,
      target_id: target.targetId,
      image_path: target.imagePath,
      rows,
      cols,
      margin_px: marginPx,
      gutter_px: gutterPx,
      row_weights: rowWeights,
      col_weights: colWeights,
    });
  };

  return (
    <Dialog title={t("studio.gridSplit.title")} size="lg" onClose={() => openGridModal(null)}>
      <div className="studio-phase2-stack">
        <div className="studio-grid-controls">
          <label className="studio-field-inline">
            <span>{t("studio.gridSplit.rows")}</span>
            <input
              type="number"
              min={rowsMin}
              max={rowsMax}
              value={rows}
              onChange={(e) => handleRowsChange(Number(e.target.value) || rowsMin || 1)}
            />
          </label>
          <label className="studio-field-inline">
            <span>{t("studio.gridSplit.cols")}</span>
            <input
              type="number"
              min={colsMin}
              max={colsMax}
              value={cols}
              onChange={(e) => handleColsChange(Number(e.target.value) || colsMin || 1)}
            />
          </label>
          <label className="studio-field-inline">
            <span>{t("studio.gridSplit.margin")}</span>
            <input
              type="number"
              min={marginMin}
              max={marginMax}
              value={marginPx}
              onChange={(e) =>
                setMarginPx(clampGridBound(Number(e.target.value) || 0, marginMin ?? 0, marginMax))
              }
            />
          </label>
          <label className="studio-field-inline">
            <span>{t("studio.gridSplit.gutter")}</span>
            <input
              type="number"
              min={gutterMin}
              max={gutterMax}
              value={gutterPx}
              onChange={(e) =>
                setGutterPx(clampGridBound(Number(e.target.value) || 0, gutterMin ?? 0, gutterMax))
              }
            />
          </label>
          <Button size="sm" variant="primary" disabled={saving} onClick={handleSplit}>
            <Grid size={14} />
            {t("studio.gridSplit.splitButton")}
          </Button>
        </div>

        <div className="studio-grid-preview-frame">
          {url ? (
            <div className="studio-media-overlay-stage">
              <img
                src={url}
                alt={t("studio.gridSplit.previewAlt")}
                className="studio-grid-preview-img"
                onError={handleMediaError}
              />
              {hCuts.map((pct, i) => (
                <div
                  key={`h-${i}`}
                  className="studio-grid-line studio-grid-line--h"
                  style={{ top: `${pct}%` }}
                />
              ))}
              {vCuts.map((pct, i) => (
                <div
                  key={`v-${i}`}
                  className="studio-grid-line studio-grid-line--v"
                  style={{ left: `${pct}%` }}
                />
              ))}
            </div>
          ) : (
            <>
              <div className="studio-field-hint">{target.imagePath}</div>
              {hCuts.map((pct, i) => (
                <div
                  key={`h-${i}`}
                  className="studio-grid-line studio-grid-line--h"
                  style={{ top: `${pct}%` }}
                />
              ))}
              {vCuts.map((pct, i) => (
                <div
                  key={`v-${i}`}
                  className="studio-grid-line studio-grid-line--v"
                  style={{ left: `${pct}%` }}
                />
              ))}
            </>
          )}
        </div>

        <div className="studio-grid-weights">
          {rowWeights.map((w, i) => (
            <label key={`rw-${i}`} className="studio-field-inline">
              <span>{t("studio.gridSplit.rowSplitPct", { index: i + 1 })}</span>
              <input
                type="range"
                min={appConfig.studio.gridSplitter.minWeightPct}
                max={appConfig.studio.gridSplitter.maxWeightPct}
                value={w}
                onChange={(e) =>
                  setRowWeights((prev) =>
                    prev.map((x, idx) => (idx === i ? Number(e.target.value) : x))
                  )
                }
              />
            </label>
          ))}
          {colWeights.map((w, i) => (
            <label key={`cw-${i}`} className="studio-field-inline">
              <span>{t("studio.gridSplit.colSplitPct", { index: i + 1 })}</span>
              <input
                type="range"
                min={appConfig.studio.gridSplitter.minWeightPct}
                max={appConfig.studio.gridSplitter.maxWeightPct}
                value={w}
                onChange={(e) =>
                  setColWeights((prev) =>
                    prev.map((x, idx) => (idx === i ? Number(e.target.value) : x))
                  )
                }
              />
            </label>
          ))}
        </div>

        {recentPanels.length > 0 && (
          <div className="studio-version-grid">
            {recentPanels.map((p) => (
              <div key={p.id} className="studio-version-card">
                <div className="studio-version-card__head">
                  <strong>{t("studio.versions.versionLabel", { num: p.version_num })}</strong>
                  <Button
                    size="sm"
                    variant="secondary"
                    disabled={saving}
                    onClick={() => void selectVersion(p.id)}
                  >
                    {t("studio.gridSplit.applyTarget")}
                  </Button>
                </div>
                <div className="studio-field-hint studio-path-truncate">{p.media_path}</div>
              </div>
            ))}
          </div>
        )}
      </div>
    </Dialog>
  );
};

export const StudioMaskEditorModal: React.FC = () => {
  const target = useStudioStore((s) => s.maskModalTarget);
  if (!target) return null;
  return <StudioMaskEditorBody key={`${target.targetId}:${target.imagePath}`} target={target} />;
};

const StudioMaskEditorBody: React.FC<{ target: StudioMaskModalTarget }> = ({ target }) => {
  const t = useT();
  const theme = usePreferencesStore((s) => s.prefs.theme);
  const openMaskModal = useStudioStore((s) => s.openMaskModal);
  const applyMaskEdit = useStudioStore((s) => s.applyMaskEdit);
  const saving = useStudioStore((s) => s.saving);

  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [imgSize, setImgSize] = useState<{ w: number; h: number }>({
    w: appConfig.studio.maskEditor.defaultWidthPx,
    h: appConfig.studio.maskEditor.defaultHeightPx,
  });
  const [imgLoaded, setImgLoaded] = useState(!target.assetId);
  const [brushSize, setBrushSize] = useState<number>(appConfig.studio.maskEditor.defaultBrushPx);
  const [strokes, setStrokes] = useState<MaskStroke[]>([]);
  const [drawing, setDrawing] = useState(false);
  const [prompt, setPrompt] = useState(() => target.defaultPrompt ?? "");
  const { url, handleMediaError } = useSignedMediaURL(target.assetId || null, "preview");

  useEffect(() => {
    const cvs = canvasRef.current;
    if (!cvs) return;
    const ctx = cvs.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, cvs.width, cvs.height);
    const tokenColor =
      typeof window !== "undefined"
        ? getComputedStyle(cvs).getPropertyValue("--studio-mask-brush-stroke").trim()
        : "";
    ctx.strokeStyle =
      tokenColor ||
      (typeof document !== "undefined"
        ? getComputedStyle(document.documentElement).getPropertyValue("--accent").trim()
        : "");
    strokeMaskPaths(ctx, strokes);
  }, [strokes, imgSize, theme]);

  const pointFromEvent = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const cvs = canvasRef.current!;
    const rect = cvs.getBoundingClientRect();
    const scaleX = cvs.width / Math.max(1, rect.width);
    const scaleY = cvs.height / Math.max(1, rect.height);
    return {
      x: (e.clientX - rect.left) * scaleX,
      y: (e.clientY - rect.top) * scaleY,
    };
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

  return (
    <Dialog title={t("studio.maskEdit.title")} size="lg" onClose={() => openMaskModal(null)}>
      <div className="studio-phase2-stack">
        <p className="studio-field-hint">{t("studio.maskEdit.hint")}</p>
        <div className="studio-grid-controls">
          <label className="studio-field-inline">
            <span>{t("studio.maskEdit.brushSizePx", { size: brushSize })}</span>
            <input
              type="range"
              min={appConfig.studio.maskEditor.minBrushPx}
              max={appConfig.studio.maskEditor.maxBrushPx}
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
            onClick={() => setStrokes([])}
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
              <canvas
                ref={canvasRef}
                width={imgSize.w}
                height={imgSize.h}
                className="studio-mask-canvas"
                onPointerDown={(e) => {
                  if (!imgLoaded) return;
                  e.currentTarget.setPointerCapture(e.pointerId);
                  setDrawing(true);
                  const pt = pointFromEvent(e);
                  setStrokes((prev) => [...prev, { size: brushSize, points: [pt] }]);
                }}
                onPointerMove={(e) => {
                  if (!drawing || !imgLoaded) return;
                  const pt = pointFromEvent(e);
                  setStrokes((prev) => {
                    if (prev.length === 0) return prev;
                    const next = [...prev];
                    const last = next[next.length - 1];
                    next[next.length - 1] = { ...last, points: [...last.points, pt] };
                    return next;
                  });
                }}
                onPointerUp={() => setDrawing(false)}
                onPointerCancel={() => setDrawing(false)}
                onLostPointerCapture={() => setDrawing(false)}
              />
            </div>
          ) : (
            <canvas
              ref={canvasRef}
              width={imgSize.w}
              height={imgSize.h}
              className="studio-mask-canvas studio-mask-canvas--standalone"
              onPointerDown={(e) => {
                e.currentTarget.setPointerCapture(e.pointerId);
                setDrawing(true);
                const pt = pointFromEvent(e);
                setStrokes((prev) => [...prev, { size: brushSize, points: [pt] }]);
              }}
              onPointerMove={(e) => {
                if (!drawing) return;
                const pt = pointFromEvent(e);
                setStrokes((prev) => {
                  if (prev.length === 0) return prev;
                  const next = [...prev];
                  const last = next[next.length - 1];
                  next[next.length - 1] = { ...last, points: [...last.points, pt] };
                  return next;
                });
              }}
              onPointerUp={() => setDrawing(false)}
              onPointerCancel={() => setDrawing(false)}
              onLostPointerCapture={() => setDrawing(false)}
            />
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
