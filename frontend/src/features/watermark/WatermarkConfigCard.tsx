import React, { useEffect, useRef, useState } from "react";
import { Sparkles, UploadCloud, Crosshair, Cpu, FolderOpen } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { getBridge, isDesktop, onFileDrop } from "../../lib/bridge";
import { isMessageKey, useT } from "../../lib/i18n";
import { findParam, integerChoices, useNodeSpec } from "../workflow/useNodeSpecs";
import { WATERMARK_NODE, useWatermarkStore } from "./useWatermarkStore";

export const WatermarkConfigCard: React.FC = () => {
  const t = useT();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const dropzoneRef = useRef<HTMLDivElement>(null);
  const [dragActive, setDragActive] = useState(false);

  const {
    inputPath,
    inputPaths,
    inPlace,
    outputDir,
    workers,
    corner,
    setInputPath,
    setInputPaths,
    setInPlace,
    setOutputDir,
    setWorkers,
    setCorner,
  } = useWatermarkStore();

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

  useEffect(() => {
    const isImageFile = (p: string) => /\.(png|jpe?g|webp)$/i.test(p);

    const unsub = onFileDrop((x, y, paths) => {
      setDragActive(false);
      const target = document.elementFromPoint(x, y);
      const rect = dropzoneRef.current?.getBoundingClientRect();
      const isInsideRect =
        rect !== undefined &&
        x >= rect.left &&
        x <= rect.right &&
        y >= rect.top &&
        y <= rect.bottom;
      const isInsideDropzone =
        isInsideRect ||
        (dropzoneRef.current !== null &&
          target !== null &&
          (dropzoneRef.current === target || dropzoneRef.current.contains(target)));

      if (isInsideDropzone) {
        const validPaths = paths.filter(isImageFile);
        if (validPaths.length > 0) {
          setInputPaths(validPaths);
        }
      }
    }, false);

    return () => {
      unsub();
    };
  }, [setInputPaths]);

  const handleBrowse = async () => {
    const bridge = getBridge();
    if (bridge?.BrowseImages) {
      try {
        const selected = await bridge.BrowseImages(t("watermark.browse.title"));
        if (selected && selected.length > 0) {
          setInputPaths(selected);
        }
      } catch (err) {
        console.error("BrowseImages failed", err);
      }
    } else if (bridge?.BrowseImage) {
      try {
        const selected = await bridge.BrowseImage(t("watermark.browse.title"));
        if (selected) {
          setInputPath(selected);
        }
      } catch (err) {
        console.error("BrowseImage failed", err);
      }
    } else {
      fileInputRef.current?.click();
    }
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

  const extractPathsFromFileList = (files: FileList | null): string[] => {
    if (!files || files.length === 0) return [];
    const paths: string[] = [];
    for (let i = 0; i < files.length; i++) {
      const file = files[i];
      const filePath = (file as unknown as { path?: string }).path || file.name;
      if (filePath) paths.push(filePath);
    }
    return paths;
  };

  const handleFileInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const paths = extractPathsFromFileList(e.target.files);
    if (paths.length > 0) {
      setInputPaths(paths);
    }
  };

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault();
    setDragActive(true);
  };

  const handleDragLeave = (e: React.DragEvent) => {
    e.preventDefault();
    setDragActive(false);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragActive(false);
    if (isDesktop()) {
      // In desktop mode (Windows WebView2), DOM DragEvent loses absolute paths.
      // Native Wails onFileDrop handles setting input paths with absolute paths.
      return;
    }
    const paths = extractPathsFromFileList(e.dataTransfer.files);
    if (paths.length > 0) {
      setInputPaths(paths);
    }
  };

  const handleDropzoneKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      void handleBrowse();
    }
  };

  const totalFiles = inputPaths.length > 0 ? inputPaths.length : inputPath ? 1 : 0;

  return (
    <Card icon={Sparkles} title={t("watermark.card.input")} className="card--flex-fill">
      <div className="watermark-config-stack">
        {/* Compact Dropzone */}
        <div
          ref={dropzoneRef}
          role="button"
          tabIndex={0}
          onDragOver={handleDragOver}
          onDragLeave={handleDragLeave}
          onDrop={handleDrop}
          onClick={handleBrowse}
          onKeyDown={handleDropzoneKeyDown}
          className={`watermark-dropzone${dragActive ? " watermark-dropzone--active" : ""}`}
        >
          <input
            ref={fileInputRef}
            type="file"
            multiple
            accept="image/png,image/jpeg,image/webp"
            className="watermark-dropzone__input"
            onChange={handleFileInputChange}
          />
          <UploadCloud size={22} className="watermark-dropzone__icon" />
          <div className="watermark-dropzone__title">
            {totalFiles > 0
              ? t("watermark.readyFiles", { count: totalFiles })
              : t("watermark.dragOrClick")}
          </div>
          <div className="watermark-dropzone__desc">
            {totalFiles > 0 ? t("watermark.changeFiles") : t("watermark.dragOrClickDesc")}
          </div>
        </div>

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
                onClick={() => setInPlace(true)}
                className="watermark-segmented__btn"
              >
                {t("watermark.mode.inplace")}
              </Button>
            </div>
          </div>

          {/* Output Directory Input with Folder Picker */}
          {!inPlace && (
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
        </div>
      </div>
    </Card>
  );
};

export default WatermarkConfigCard;
