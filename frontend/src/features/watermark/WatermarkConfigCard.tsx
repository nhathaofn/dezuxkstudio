import React, { useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { Sparkles, Crosshair, Cpu, FolderOpen } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { Dialog } from "../../components/ui/Dialog";
import { Switch } from "../../components/ui/Switch";
import { MediaInputNode } from "../../components/ui/MediaDropZone";
import { getBridge } from "../../lib/bridge";
import { isMessageKey, useT } from "../../lib/i18n";
import { useConnectionStore } from "../connection/useConnectionStore";
import { findParam, integerChoices, useNodeSpec } from "../workflow/useNodeSpecs";
import { WATERMARK_NODE, useWatermarkStore } from "./useWatermarkStore";

export const WatermarkConfigCard: React.FC = () => {
  const t = useT();
  const endpointMode = useConnectionStore((s) => s.endpoint?.mode);
  const isRemoteMode = endpointMode === "browser" || endpointMode === "client";

  const [confirmInPlaceOpen, setConfirmInPlaceOpen] = useState(false);

  const {
    inputPath,
    inputPaths,
    inPlace,
    outputDir,
    copyClean,
    preserveTree,
    workers,
    corner,
    setInputPaths,
    setQueuedFiles,
    setInPlace,
    setOutputDir,
    setCopyClean,
    setPreserveTree,
    setWorkers,
    setCorner,
  } = useWatermarkStore(
    useShallow((s) => ({
      inputPath: s.inputPath,
      inputPaths: s.inputPaths,
      inPlace: s.inPlace,
      outputDir: s.outputDir,
      copyClean: s.copyClean,
      preserveTree: s.preserveTree,
      workers: s.workers,
      corner: s.corner,
      setInputPaths: s.setInputPaths,
      setQueuedFiles: s.setQueuedFiles,
      setInPlace: s.setInPlace,
      setOutputDir: s.setOutputDir,
      setCopyClean: s.setCopyClean,
      setPreserveTree: s.setPreserveTree,
      setWorkers: s.setWorkers,
      setCorner: s.setCorner,
    }))
  );

  // Choices, limits and defaults come from the node's spec, never from the UI.
  const spec = useNodeSpec(WATERMARK_NODE);
  const cornerParam = findParam(spec, "corner");
  const workersParam = findParam(spec, "workers");
  const cornerValue = corner ?? cornerParam?.default ?? "";
  const workersValue = workers ?? Number(workersParam?.default ?? 0);
  const workerChoices = integerChoices(workersParam);
  const cornerLabel = (option: string) => {
    const key = `watermark.corner.${option.replace(/-/g, "_")}`;
    return isMessageKey(key) ? t(key) : option;
  };

  const handleBrowseOutputDir = async () => {
    const bridge = getBridge();
    if (bridge?.BrowseDirectory) {
      try {
        const selected = await bridge.BrowseDirectory(t("watermark.outputDir"));
        if (selected) {
          setOutputDir(selected);
        }
      } catch (err) {
        console.error("BrowseDirectory failed", err);
      }
    }
  };

  const totalFiles = inputPaths.length > 0 ? inputPaths.length : inputPath ? 1 : 0;

  return (
    <Card icon={Sparkles} title={t("watermark.card.input")} className="card--flex-fill">
      <div className="watermark-config-stack">
        {/* Universal Media Input Node */}
        <MediaInputNode
          multiple
          label={t("watermark.browse.title")}
          title={t("watermark.dragOrClick")}
          description={t("watermark.dragOrClickDesc")}
          selectedCount={totalFiles}
          selectedSummary={
            totalFiles > 0 ? t("watermark.readyFiles", { count: totalFiles }) : undefined
          }
          onSelected={(files, paths) => {
            if (paths && paths.length > 0) {
              setInputPaths(paths);
            } else if (files && files.length > 0) {
              setQueuedFiles(files);
            }
          }}
        />

        {/* Config Fields */}
        <div className="watermark-config-stack">
          {/* Corner Selection */}
          <div className="form-group">
            <label className="field-label watermark-label-with-icon">
              <Crosshair size={12} />
              {t("watermark.corner")}
            </label>
            <select
              value={cornerValue}
              onChange={(e) => setCorner(e.target.value)}
              className="input-control"
              disabled={!cornerParam}
            >
              {(cornerParam?.options ?? []).map((opt) => (
                <option key={opt} value={opt}>
                  {cornerLabel(opt)}
                </option>
              ))}
            </select>
          </div>

          {/* Multi-threading Workers */}
          <div className="form-group">
            <label className="field-label watermark-label-with-icon">
              <Cpu size={12} />
              {t("watermark.workers")}
            </label>
            <select
              value={workersValue}
              onChange={(e) => setWorkers(Number(e.target.value))}
              className="input-control"
              disabled={!workersParam}
            >
              {workerChoices.map((n) => (
                <option key={n} value={n}>
                  {n === 0
                    ? t("watermark.workers.auto")
                    : t("watermark.workers.count", { count: n })}
                </option>
              ))}
            </select>
          </div>

          {/* Output Mode Switch */}
          <div className="form-group">
            <label className="field-label">{t("watermark.outputMode")}</label>
            <div className="watermark-segmented">
              <Button
                size="sm"
                variant={!inPlace ? "primary" : "secondary"}
                onClick={() => setInPlace(false)}
                className="watermark-segmented__btn"
              >
                {t("watermark.mode.copy")}
              </Button>
              <Button
                size="sm"
                variant={inPlace ? "primary" : "secondary"}
                onClick={() => {
                  if (!inPlace) setConfirmInPlaceOpen(true);
                }}
                className="watermark-segmented__btn"
              >
                {t("watermark.mode.inplace")}
              </Button>
            </div>
          </div>

          {/* Output Directory Input with Folder Picker (desktop mode only) */}
          {!inPlace && !isRemoteMode && (
            <div className="form-group">
              <label className="field-label">{t("watermark.outputDir")}</label>
              <div className="watermark-dir-row">
                <input
                  type="text"
                  placeholder={t("watermark.outputDir.placeholder")}
                  value={outputDir}
                  onChange={(e) => setOutputDir(e.target.value)}
                  className="input-control input-control--mono"
                />
                <Button
                  type="button"
                  size="md"
                  iconOnly
                  onClick={handleBrowseOutputDir}
                  title={t("watermark.browseFolder")}
                  aria-label={t("watermark.browseFolder")}
                >
                  <FolderOpen size={14} />
                </Button>
              </div>
            </div>
          )}

          {/* Copy Clean & Preserve Tree Options (only when not in-place) */}
          {!inPlace && (
            <div className="watermark-config-stack">
              <div className="watermark-toggle-row">
                <div>
                  <div className="field-label" style={{ marginBottom: 0 }}>
                    {t("watermark.copyClean.label")}
                  </div>
                  <div className="muted-text" style={{ fontSize: "var(--text-xs)" }}>
                    {t("watermark.copyClean.desc")}
                  </div>
                </div>
                <Switch
                  checked={copyClean}
                  onCheckedChange={setCopyClean}
                  aria-label={t("watermark.copyClean.label")}
                />
              </div>

              <div className="watermark-toggle-row">
                <div>
                  <div className="field-label" style={{ marginBottom: 0 }}>
                    {t("watermark.preserveTree.label")}
                  </div>
                  <div className="muted-text" style={{ fontSize: "var(--text-xs)" }}>
                    {t("watermark.preserveTree.desc")}
                  </div>
                </div>
                <Switch
                  checked={preserveTree}
                  onCheckedChange={setPreserveTree}
                  aria-label={t("watermark.preserveTree.label")}
                />
              </div>
            </div>
          )}
        </div>
      </div>

      {confirmInPlaceOpen && (
        <Dialog
          onClose={() => setConfirmInPlaceOpen(false)}
          title={t("watermark.mode.inplaceConfirmTitle")}
          size="sm"
        >
          <p className="muted-text" style={{ marginTop: 0 }}>
            {t("watermark.mode.inplaceConfirmBody")}
          </p>
          <div
            style={{
              display: "flex",
              justifyContent: "flex-end",
              gap: "var(--space-2)",
              marginTop: "var(--space-4)",
            }}
          >
            <Button variant="secondary" size="sm" onClick={() => setConfirmInPlaceOpen(false)}>
              {t("watermark.mode.inplaceCancelBtn")}
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={() => {
                setInPlace(true);
                setConfirmInPlaceOpen(false);
              }}
            >
              {t("watermark.mode.inplaceConfirmBtn")}
            </Button>
          </div>
        </Dialog>
      )}
    </Card>
  );
};

export default WatermarkConfigCard;
