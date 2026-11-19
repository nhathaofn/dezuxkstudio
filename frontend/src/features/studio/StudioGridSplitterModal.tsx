import React, { useState } from "react";
import { Grid } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { ErrorText } from "../../components/ui/ErrorText";
import { appConfig } from "../../config/appConfig";
import { NodeTypes, type StudioMediaTargetKind } from "../../contracts/generated";
import { useSignedMediaURL } from "../../lib/hooks/useSignedMediaURL";
import { useT } from "../../lib/i18n";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { computeGridDividerPercents } from "./phase2Helpers";
import { targetVersionKey, useStudioStore, type StudioGridModalTarget } from "./useStudioStore";

const equalGridWeights = (count: number) =>
  Array.from({ length: count }, () =>
    Math.round(appConfig.studio.gridSplitter.weightTotalPct / Math.max(1, count))
  );

export const clampGridBound = (val: number, minVal?: number, maxVal?: number) => {
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
  const error = useStudioStore((s) => s.error);
  const clearError = useStudioStore((s) => s.clearError);

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
        <ErrorText error={error} onDismiss={clearError} />
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
