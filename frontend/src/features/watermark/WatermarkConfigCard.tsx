import React, { useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { Sparkles, Crosshair, Cpu, Film, FolderOpen } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { Dialog } from "../../components/ui/Dialog";
import { Switch } from "../../components/ui/Switch";
import { MediaInputNode } from "../../components/ui/MediaDropZone";
import { getBridge } from "../../lib/bridge";
import { isMessageKey, useT } from "../../lib/i18n";
import { mediaBasename } from "../../lib/rtClient";
import { useConnectionStore } from "../connection/useConnectionStore";
import { findParam, integerChoices, useNodeSpec } from "../workflow/useNodeSpecs";
import { VIDEO_WATERMARK_NODE, WATERMARK_NODE, useWatermarkStore } from "./useWatermarkStore";

export const WatermarkConfigCard: React.FC = () => {
  const t = useT();
  const endpointMode = useConnectionStore((s) => s.endpoint?.mode);
  const isRemoteMode = endpointMode === "browser" || endpointMode === "client";

  const [confirmInPlaceOpen, setConfirmInPlaceOpen] = useState(false);

  const {
    mediaKind,
    inputPath,
    inputPaths,
    videoInputPath,
    inPlace,
    outputDir,
    copyClean,
    preserveTree,
    workers,
    corner,
    videoMode,
    videoX,
    videoY,
    videoWidth,
    videoHeight,
    videoCropEdge,
    videoCropPx,
    setMediaKind,
    setInputPaths,
    setQueuedFiles,
    setVideoInputPath,
    setQueuedVideoFile,
    setInPlace,
    setOutputDir,
    setCopyClean,
    setPreserveTree,
    setWorkers,
    setCorner,
    setVideoMode,
    setVideoX,
    setVideoY,
    setVideoWidth,
    setVideoHeight,
    setVideoCropEdge,
    setVideoCropPx,
  } = useWatermarkStore(
    useShallow((s) => ({
      mediaKind: s.mediaKind,
      inputPath: s.inputPath,
      inputPaths: s.inputPaths,
      videoInputPath: s.videoInputPath,
      inPlace: s.inPlace,
      outputDir: s.outputDir,
      copyClean: s.copyClean,
      preserveTree: s.preserveTree,
      workers: s.workers,
      corner: s.corner,
      videoMode: s.videoMode,
      videoX: s.videoX,
      videoY: s.videoY,
      videoWidth: s.videoWidth,
      videoHeight: s.videoHeight,
      videoCropEdge: s.videoCropEdge,
      videoCropPx: s.videoCropPx,
      setMediaKind: s.setMediaKind,
      setInputPaths: s.setInputPaths,
      setQueuedFiles: s.setQueuedFiles,
      setVideoInputPath: s.setVideoInputPath,
      setQueuedVideoFile: s.setQueuedVideoFile,
      setInPlace: s.setInPlace,
      setOutputDir: s.setOutputDir,
      setCopyClean: s.setCopyClean,
      setPreserveTree: s.setPreserveTree,
      setWorkers: s.setWorkers,
      setCorner: s.setCorner,
      setVideoMode: s.setVideoMode,
      setVideoX: s.setVideoX,
      setVideoY: s.setVideoY,
      setVideoWidth: s.setVideoWidth,
      setVideoHeight: s.setVideoHeight,
      setVideoCropEdge: s.setVideoCropEdge,
      setVideoCropPx: s.setVideoCropPx,
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

  const videoSpec = useNodeSpec(VIDEO_WATERMARK_NODE);
  const modeParam = findParam(videoSpec, "mode");
  const xParam = findParam(videoSpec, "x");
  const yParam = findParam(videoSpec, "y");
  const widthParam = findParam(videoSpec, "width");
  const heightParam = findParam(videoSpec, "height");
  const edgeParam = findParam(videoSpec, "crop_edge");
  const cropPxParam = findParam(videoSpec, "crop_px");
  const activeVideoMode = videoMode ?? modeParam?.default ?? modeParam?.options?.[0] ?? "";
  const activeCropEdge = videoCropEdge ?? edgeParam?.default ?? edgeParam?.options?.[0] ?? "";

  const parseIntOrNull = (raw: string): number | null => {
    if (raw.trim() === "") return null;
    const n = Number.parseInt(raw, 10);
    return Number.isFinite(n) ? n : null;
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

  const renderOutputDirField = () =>
    isRemoteMode ? null : (
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
    );

  return (
    <Card icon={Sparkles} title={t("watermark.card.input")} className="card--flex-fill">
      <div className="watermark-config-stack">
        <div className="watermark-segmented" role="tablist" aria-label={t("watermark.title")}>
          <Button
            size="sm"
            variant={mediaKind === "image" ? "primary" : "secondary"}
            onClick={() => setMediaKind("image")}
            className="watermark-segmented__btn"
          >
            {t("watermark.tab.image")}
          </Button>
          <Button
            size="sm"
            variant={mediaKind === "video" ? "primary" : "secondary"}
            onClick={() => setMediaKind("video")}
            className="watermark-segmented__btn"
          >
            {t("watermark.tab.video")}
          </Button>
        </div>

        {mediaKind === "video" ? (
          <>
            <MediaInputNode
              icon={Film}
              accept="video/mp4,video/webm,video/quicktime,video/*,.mp4,.mkv,.mov,.webm"
              label={t("watermark.video.browseTitle")}
              title={t("watermark.video.dragOrClick")}
              description={t("watermark.video.dragOrClickDesc")}
              selectedCount={videoInputPath ? 1 : 0}
              selectedSummary={
                videoInputPath
                  ? t("watermark.video.selected", { name: mediaBasename(videoInputPath) })
                  : undefined
              }
              onSelected={(files, paths) => {
                if (paths && paths.length > 0) {
                  setVideoInputPath(paths[0]);
                } else if (files && files.length > 0) {
                  setQueuedVideoFile(files[0]);
                }
              }}
            />

            <div className="watermark-config-stack">
              <div className="form-group">
                <label className="field-label" htmlFor="watermark-video-mode">
                  {t("watermark.video.mode")}
                </label>
                <select
                  id="watermark-video-mode"
                  value={activeVideoMode}
                  onChange={(e) => setVideoMode(e.target.value)}
                  className="input-control"
                  disabled={!modeParam}
                >
                  {(modeParam?.options ?? []).map((opt) => {
                    const key = `watermark.video.mode.${opt}`;
                    return (
                      <option key={opt} value={opt}>
                        {isMessageKey(key) ? t(key) : opt}
                      </option>
                    );
                  })}
                </select>
              </div>

              {activeVideoMode !== "crop" ? (
                <div className="mediagen-two-col">
                  <div className="form-group">
                    <label className="field-label" htmlFor="watermark-video-x">
                      {t("watermark.video.x")}
                    </label>
                    <input
                      id="watermark-video-x"
                      type="number"
                      className="input-control input-control--mono"
                      min={xParam?.min ?? 0}
                      max={xParam?.max}
                      placeholder={xParam?.default ?? "0"}
                      value={videoX ?? ""}
                      onChange={(e) => setVideoX(parseIntOrNull(e.target.value))}
                    />
                  </div>
                  <div className="form-group">
                    <label className="field-label" htmlFor="watermark-video-y">
                      {t("watermark.video.y")}
                    </label>
                    <input
                      id="watermark-video-y"
                      type="number"
                      className="input-control input-control--mono"
                      min={yParam?.min ?? 0}
                      max={yParam?.max}
                      placeholder={yParam?.default ?? "0"}
                      value={videoY ?? ""}
                      onChange={(e) => setVideoY(parseIntOrNull(e.target.value))}
                    />
                  </div>
                  <div className="form-group">
                    <label className="field-label" htmlFor="watermark-video-w">
                      {t("watermark.video.width")}
                    </label>
                    <input
                      id="watermark-video-w"
                      type="number"
                      className="input-control input-control--mono"
                      min={widthParam?.min ?? 1}
                      max={widthParam?.max}
                      placeholder={widthParam?.default ?? ""}
                      value={videoWidth ?? ""}
                      onChange={(e) => setVideoWidth(parseIntOrNull(e.target.value))}
                    />
                  </div>
                  <div className="form-group">
                    <label className="field-label" htmlFor="watermark-video-h">
                      {t("watermark.video.height")}
                    </label>
                    <input
                      id="watermark-video-h"
                      type="number"
                      className="input-control input-control--mono"
                      min={heightParam?.min ?? 1}
                      max={heightParam?.max}
                      placeholder={heightParam?.default ?? ""}
                      value={videoHeight ?? ""}
                      onChange={(e) => setVideoHeight(parseIntOrNull(e.target.value))}
                    />
                  </div>
                </div>
              ) : (
                <div className="mediagen-two-col">
                  <div className="form-group">
                    <label className="field-label" htmlFor="watermark-video-edge">
                      {t("watermark.video.cropEdge")}
                    </label>
                    <select
                      id="watermark-video-edge"
                      value={activeCropEdge}
                      onChange={(e) => setVideoCropEdge(e.target.value)}
                      className="input-control"
                      disabled={!edgeParam}
                    >
                      {(edgeParam?.options ?? []).map((opt) => {
                        const key = `watermark.video.cropEdge.${opt}`;
                        return (
                          <option key={opt} value={opt}>
                            {isMessageKey(key) ? t(key) : opt}
                          </option>
                        );
                      })}
                    </select>
                  </div>
                  <div className="form-group">
                    <label className="field-label" htmlFor="watermark-video-crop-px">
                      {t("watermark.video.cropPx")}
                    </label>
                    <input
                      id="watermark-video-crop-px"
                      type="number"
                      className="input-control input-control--mono"
                      min={cropPxParam?.min ?? 2}
                      max={cropPxParam?.max}
                      placeholder={cropPxParam?.default ?? ""}
                      value={videoCropPx ?? ""}
                      onChange={(e) => setVideoCropPx(parseIntOrNull(e.target.value))}
                    />
                  </div>
                </div>
              )}

              {renderOutputDirField()}
            </div>
          </>
        ) : (
          <>
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
              {!inPlace && renderOutputDirField()}

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
          </>
        )}
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
