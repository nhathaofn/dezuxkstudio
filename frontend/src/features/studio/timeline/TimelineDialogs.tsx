import React, { useState } from "react";
import { FileArchive, Film, Loader2, Plus, Trash2 } from "lucide-react";
import { Button } from "../../../components/ui/Button";
import { Dialog } from "../../../components/ui/Dialog";
import { Switch } from "../../../components/ui/Switch";
import { appConfig } from "../../../config/appConfig";
import {
  NodeTypes,
  type Param,
  type StudioEditTimeline,
  type StudioNLEExportFormat,
  type StudioTimelineOp,
  type StudioTimelineOpKind,
} from "../../../contracts/generated";
import { isMessageKey, useT } from "../../../lib/i18n";
import { findParam, useNodeSpec } from "../../workflow/useNodeSpecs";
import { useStudioStore } from "../useStudioStore";

interface RenderDialogProps {
  defaultFilename: string;
  onClose: () => void;
}

function parseOptionalParamNumber(param: Param | undefined): number | undefined {
  if (param?.default === undefined || param.default === "") return undefined;
  const n = Number(param.default);
  return Number.isFinite(n) ? n : undefined;
}

export const RenderTimelineDialog: React.FC<RenderDialogProps> = ({ defaultFilename, onClose }) => {
  const t = useT();
  const spec = useNodeSpec(NodeTypes.RENDER_TIMELINE);
  const renderTimeline = useStudioStore((s) => s.renderTimeline);
  const rendering = useStudioStore((s) => s.renderingTimeline);

  const fontParam = findParam(spec, "subtitle_font_size");
  const lufsParam = findParam(spec, "loudnorm_lufs");
  const crfParam = findParam(spec, "crf");
  const presetParam = findParam(spec, "preset");
  const burnParam = findParam(spec, "burn_subtitles");
  const overwriteParam = findParam(spec, "overwrite");

  const [filename, setFilename] = useState(defaultFilename);
  const [burnSubtitlesOverride, setBurnSubtitlesOverride] = useState<boolean | null>(null);
  const [overwriteOverride, setOverwriteOverride] = useState<boolean | null>(null);
  const [fontSizeOverride, setFontSizeOverride] = useState<number | null>(null);
  const [lufsOverride, setLufsOverride] = useState<number | null>(null);
  const [crfOverride, setCrfOverride] = useState<number | null>(null);
  const [presetOverride, setPresetOverride] = useState<string | null>(null);

  const burnSubtitles = burnSubtitlesOverride ?? burnParam?.default === "true";
  const overwrite = overwriteOverride ?? overwriteParam?.default === "true";
  const fontSize = fontSizeOverride ?? parseOptionalParamNumber(fontParam);
  const loudnormLUFS = lufsOverride ?? parseOptionalParamNumber(lufsParam);
  const crf = crfOverride ?? parseOptionalParamNumber(crfParam);
  const presetOptions = presetParam?.options ?? [];
  const preset = presetOverride ?? (presetParam?.default || presetOptions[0] || "");

  const submit = async () => {
    const res = await renderTimeline({
      filename: filename.trim() || undefined,
      burn_subtitles: burnSubtitles,
      overwrite,
      ...(fontSize !== undefined ? { subtitle_font_size: fontSize } : {}),
      ...(loudnormLUFS !== undefined ? { loudnorm_lufs: loudnormLUFS } : {}),
      ...(crf !== undefined ? { crf } : {}),
      ...(preset ? { preset } : {}),
    });
    if (res) onClose();
  };

  return (
    <Dialog
      title={t("studio.timeline.renderDialogTitle")}
      onClose={onClose}
      busy={rendering}
      footer={
        <div className="studio-toolbar">
          <Button variant="ghost" onClick={onClose} disabled={rendering}>
            {t("studio.modal.cancel")}
          </Button>
          <Button variant="primary" onClick={() => void submit()} disabled={rendering}>
            {rendering ? <Loader2 size={14} className="animate-spin" /> : <Film size={14} />}
            {t("studio.timeline.startRender")}
          </Button>
        </div>
      }
    >
      <div className="studio-form-stack">
        <div className="form-group">
          <label className="field-label" htmlFor="tl-render-filename">
            {t("studio.timeline.filename")}
          </label>
          <input
            id="tl-render-filename"
            className="input-control"
            value={filename}
            onChange={(e) => setFilename(e.target.value)}
            disabled={rendering}
          />
        </div>

        <div className="studio-field-row">
          <div className="form-group">
            <label className="field-label" htmlFor="tl-render-lufs">
              {t("studio.timeline.loudnormLufs")}
            </label>
            <input
              id="tl-render-lufs"
              type="number"
              step={appConfig.studio.timeline.loudnormStepLufs}
              min={lufsParam?.min}
              max={lufsParam?.max}
              className="input-control"
              value={loudnormLUFS ?? ""}
              onChange={(e) =>
                setLufsOverride(e.target.value === "" ? null : Number(e.target.value))
              }
              disabled={rendering}
            />
          </div>

          <div className="form-group">
            <label className="field-label" htmlFor="tl-render-fontsize">
              {t("studio.timeline.subtitleFontSize")}
            </label>
            <input
              id="tl-render-fontsize"
              type="number"
              min={fontParam?.min}
              max={fontParam?.max}
              className="input-control"
              value={fontSize ?? ""}
              onChange={(e) =>
                setFontSizeOverride(e.target.value === "" ? null : Number(e.target.value))
              }
              disabled={rendering}
            />
          </div>
        </div>

        <div className="studio-field-row">
          <div className="form-group">
            <label className="field-label" htmlFor="tl-render-crf">
              {t("studio.timeline.crf")}
            </label>
            <input
              id="tl-render-crf"
              type="number"
              min={crfParam?.min}
              max={crfParam?.max}
              className="input-control"
              value={crf ?? ""}
              onChange={(e) =>
                setCrfOverride(e.target.value === "" ? null : Number(e.target.value))
              }
              disabled={rendering}
            />
          </div>

          <div className="form-group">
            <label className="field-label" htmlFor="tl-render-preset">
              {t("studio.timeline.preset")}
            </label>
            <select
              id="tl-render-preset"
              className="input-control"
              value={preset}
              onChange={(e) => setPresetOverride(e.target.value)}
              disabled={rendering}
            >
              {presetOptions.map((opt) => {
                const presetKey = `studio.timeline.presets.${opt}`;
                return (
                  <option key={opt} value={opt}>
                    {isMessageKey(presetKey) ? t(presetKey) : opt}
                  </option>
                );
              })}
            </select>
          </div>
        </div>

        <label className="studio-inline-toggle">
          <Switch
            checked={burnSubtitles}
            onCheckedChange={setBurnSubtitlesOverride}
            aria-label={t("studio.timeline.burnSubtitles")}
            disabled={rendering}
          />
          <span>{t("studio.timeline.burnSubtitles")}</span>
        </label>

        <label className="studio-inline-toggle">
          <Switch
            checked={overwrite}
            onCheckedChange={setOverwriteOverride}
            aria-label={t("studio.timeline.overwriteOutput")}
            disabled={rendering}
          />
          <span>{t("studio.timeline.overwriteOutput")}</span>
        </label>
      </div>
    </Dialog>
  );
};

interface ExportNLEDialogProps {
  defaultProjectName: string;
  onClose: () => void;
}

export const ExportNLEDraftDialog: React.FC<ExportNLEDialogProps> = ({
  defaultProjectName,
  onClose,
}) => {
  const t = useT();
  const spec = useNodeSpec(NodeTypes.EXPORT_NLE_DRAFT);
  const exportNLEDraft = useStudioStore((s) => s.exportNLEDraft);
  const exporting = useStudioStore((s) => s.exportingNLE);

  const formatParam = findParam(spec, "format");
  const includeParam = findParam(spec, "include_media_copy");
  const formatOptions = (formatParam?.options ?? []) as StudioNLEExportFormat[];

  const [formatOverride, setFormatOverride] = useState<StudioNLEExportFormat | null>(null);
  const [projectName, setProjectName] = useState(defaultProjectName);
  const [includeMediaOverride, setIncludeMediaOverride] = useState<boolean | null>(null);

  const format =
    formatOverride ?? ((formatParam?.default || formatOptions[0] || "") as StudioNLEExportFormat);
  const includeMedia =
    includeMediaOverride ?? (includeParam?.default ? includeParam.default === "true" : false);

  const submit = async () => {
    if (!format) return;
    const out = await exportNLEDraft({
      format,
      project_name: projectName.trim() || undefined,
      include_media_copy: includeMedia,
    });
    if (out) onClose();
  };

  return (
    <Dialog
      title={t("studio.timeline.exportNLETitle")}
      onClose={onClose}
      busy={exporting}
      footer={
        <div className="studio-toolbar">
          <Button variant="ghost" onClick={onClose} disabled={exporting}>
            {t("studio.modal.cancel")}
          </Button>
          <Button variant="primary" onClick={() => void submit()} disabled={exporting || !format}>
            {exporting ? <Loader2 size={14} className="animate-spin" /> : <FileArchive size={14} />}
            {t("studio.timeline.startExportNLE")}
          </Button>
        </div>
      }
    >
      <div className="studio-form-stack">
        <div className="form-group">
          <label className="field-label" htmlFor="tl-nle-format">
            {t("studio.timeline.nleFormat")}
          </label>
          <select
            id="tl-nle-format"
            className="input-control"
            value={format}
            onChange={(e) => setFormatOverride(e.target.value as StudioNLEExportFormat)}
            disabled={exporting}
          >
            {formatOptions.map((fmt) => {
              const fmtKey = `studio.timeline.nleFormats.${fmt}`;
              return (
                <option key={fmt} value={fmt}>
                  {isMessageKey(fmtKey) ? t(fmtKey) : fmt}
                </option>
              );
            })}
          </select>
        </div>

        <div className="form-group">
          <label className="field-label" htmlFor="tl-nle-name">
            {t("studio.timeline.nleProjectName")}
          </label>
          <input
            id="tl-nle-name"
            className="input-control"
            value={projectName}
            onChange={(e) => setProjectName(e.target.value)}
            disabled={exporting}
          />
        </div>

        {(format === "capcut" || format === "jianying") && (
          <label className="studio-inline-toggle">
            <Switch
              checked={includeMedia}
              onCheckedChange={setIncludeMediaOverride}
              aria-label={t("studio.timeline.includeMediaCopy")}
              disabled={exporting}
            />
            <span>{t("studio.timeline.includeMediaCopy")}</span>
          </label>
        )}
      </div>
    </Dialog>
  );
};

export interface ShotMediaOption {
  key: string;
  label: string;
  path: string;
  assetId?: string;
  durationMs: number;
}

export interface AddClipDialogProps {
  timeline: StudioEditTimeline;
  shotMediaOptions: ShotMediaOption[];
  busy: boolean;
  trackId: string;
  shotKey: string;
  customPath: string;
  clipLabel: string;
  durationMs: number;
  onChangeTrackId: (val: string) => void;
  onChangeShotKey: (val: string) => void;
  onChangeCustomPath: (val: string) => void;
  onChangeLabel: (val: string) => void;
  onChangeDurationMs: (val: number) => void;
  onSubmit: (e: React.FormEvent) => void;
  onClose: () => void;
}

export const AddClipDialog: React.FC<AddClipDialogProps> = ({
  timeline,
  shotMediaOptions,
  busy,
  trackId,
  shotKey,
  customPath,
  clipLabel,
  durationMs,
  onChangeTrackId,
  onChangeShotKey,
  onChangeCustomPath,
  onChangeLabel,
  onChangeDurationMs,
  onSubmit,
  onClose,
}) => {
  const t = useT();
  return (
    <Dialog
      title={t("studio.timeline.addClipTitle")}
      size="sm"
      onClose={onClose}
      footer={(requestClose) => (
        <>
          <Button variant="ghost" onClick={requestClose} disabled={busy}>
            {t("studio.modal.cancel")}
          </Button>
          <Button
            variant="primary"
            disabled={
              busy || !(customPath.trim() || shotMediaOptions.find((o) => o.key === shotKey)?.path)
            }
            onClick={(e) => onSubmit(e)}
          >
            <Plus size={14} />
            {t("studio.timeline.addClipBtn")}
          </Button>
        </>
      )}
    >
      <form className="studio-form-stack" onSubmit={onSubmit}>
        <div className="form-group">
          <label className="field-label" htmlFor="tl-add-clip-track">
            {t("studio.timeline.addClipTrack")}
          </label>
          <select
            id="tl-add-clip-track"
            className="input-control"
            value={trackId}
            onChange={(e) => onChangeTrackId(e.target.value)}
          >
            {(timeline.tracks ?? []).map((tr) => (
              <option key={tr.id} value={tr.id}>
                {tr.name || tr.id}
              </option>
            ))}
          </select>
        </div>
        {shotMediaOptions.length > 0 && (
          <div className="form-group">
            <label className="field-label" htmlFor="tl-add-clip-shot">
              {t("studio.timeline.addClipSource")}
            </label>
            <select
              id="tl-add-clip-shot"
              className="input-control"
              value={shotKey}
              onChange={(e) => {
                const nextKey = e.target.value;
                onChangeShotKey(nextKey);
                const hit = shotMediaOptions.find((o) => o.key === nextKey);
                if (hit) onChangeDurationMs(hit.durationMs);
              }}
            >
              {shotMediaOptions.map((o) => (
                <option key={o.key} value={o.key}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
        )}
        <div className="form-group">
          <label className="field-label" htmlFor="tl-add-clip-custom">
            {t("studio.timeline.addClipCustomPath")}
          </label>
          <input
            id="tl-add-clip-custom"
            className="input-control input-control--mono"
            value={customPath}
            onChange={(e) => onChangeCustomPath(e.target.value)}
          />
        </div>
        <div className="studio-field-row">
          <div className="form-group">
            <label className="field-label" htmlFor="tl-add-clip-label">
              {t("studio.timeline.clipLabel")}
            </label>
            <input
              id="tl-add-clip-label"
              className="input-control"
              value={clipLabel}
              onChange={(e) => onChangeLabel(e.target.value)}
            />
          </div>
          <div className="form-group">
            <label className="field-label" htmlFor="tl-add-clip-dur">
              {t("studio.timeline.addClipDurationMs")}
            </label>
            <input
              id="tl-add-clip-dur"
              type="number"
              min={100}
              step={100}
              className="input-control input-control--mono"
              value={durationMs}
              onChange={(e) => onChangeDurationMs(Math.max(100, Number(e.target.value) || 4000))}
            />
          </div>
        </div>
      </form>
    </Dialog>
  );
};

export interface ReplaceTimelineConfirmDialogProps {
  timeline: StudioEditTimeline;
  busy: boolean;
  onApplyOps: (ops: StudioTimelineOp[], summary: string) => Promise<boolean>;
  onClose: () => void;
}

export const ReplaceTimelineConfirmDialog: React.FC<ReplaceTimelineConfirmDialogProps> = ({
  timeline,
  busy,
  onApplyOps,
  onClose,
}) => {
  const t = useT();
  return (
    <Dialog
      title={t("studio.timeline.replaceConfirmTitle")}
      size="sm"
      onClose={onClose}
      footer={(requestClose) => (
        <>
          <Button variant="ghost" onClick={requestClose} disabled={busy}>
            {t("studio.modal.cancel")}
          </Button>
          <Button
            variant="primary"
            disabled={busy}
            onClick={() => {
              void onApplyOps(
                [
                  {
                    op: "replace_timeline" satisfies StudioTimelineOpKind,
                    timeline: {
                      version: timeline.version,
                      fps: timeline.fps,
                      width: timeline.width,
                      height: timeline.height,
                      tracks: [],
                      subtitles: [],
                      markers: [],
                    },
                  },
                ],
                t("studio.timeline.op.replaceTimeline")
              );
              onClose();
            }}
          >
            <Trash2 size={14} />
            {t("studio.timeline.replaceConfirmAction")}
          </Button>
        </>
      )}
    >
      <p>{t("studio.timeline.replaceConfirmBody")}</p>
    </Dialog>
  );
};
