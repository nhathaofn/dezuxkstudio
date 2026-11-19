import React, { useRef, useState } from "react";
import {
  ArrowDown,
  ArrowRight,
  ArrowUp,
  Bot,
  Clapperboard,
  Download,
  FileText,
  Film,
  FolderOpen,
  Grid,
  Image as ImageIcon,
  Layers,
  Loader2,
  Paintbrush,
  Play,
  Plus,
  Save,
  Sparkles,
  Speech,
  Trash2,
  Upload,
  Volume2,
  Wand2,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { Dialog } from "../../components/ui/Dialog";
import { ErrorText } from "../../components/ui/ErrorText";
import { Switch } from "../../components/ui/Switch";
import { appConfig } from "../../config/appConfig";
import {
  NodeTypes,
  StudioAssetKindValues,
  StudioShotModeValues,
  type ProbeMediaOutput,
  type StudioAsset,
  type StudioAssetKind,
  type StudioEpisodeDetailResult,
  type StudioGenre,
  type StudioProductionMode,
  type StudioProject,
  type StudioShot,
  type StudioShotMode,
} from "../../contracts/generated";
import { isMessageKey, useT } from "../../lib/i18n";
import { mediaBasename, saveMediaToDevice } from "../../lib/rtClient";
import { askAgentAboutMediaAsset } from "../agent/screenContext";
import { useSystemStore } from "../system/useSystemStore";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { SignedStudioAudio, SignedStudioImage, SignedStudioVideo } from "./SignedStudioMedia";
import { targetVersionKey, useStudioStore } from "./useStudioStore";

export const CreateProjectDialog: React.FC<{
  onClose: () => void;
}> = ({ onClose }) => {
  const t = useT();
  const scriptSpec = useNodeSpec(NodeTypes.SCRIPT_NORMALIZE);
  const shotSpec = useNodeSpec(NodeTypes.AUTHOR_SHOT_PROMPTS);
  const genreParam = findParam(scriptSpec, "genre");
  const genreOptions = (genreParam?.options ?? []) as StudioGenre[];
  const modeParam = findParam(shotSpec, "production_mode");
  const modeOptions = (modeParam?.options ?? []) as StudioProductionMode[];
  const aspectParam = findParam(scriptSpec, "aspect_ratio");
  const aspectOptions = aspectParam?.options ?? [];
  const createProject = useStudioStore((s) => s.createProject);
  const saving = useStudioStore((s) => s.saving);
  const error = useStudioStore((s) => s.error);
  const clearError = useStudioStore((s) => s.clearError);

  const [title, setTitle] = useState("");
  const [genre, setGenre] = useState("");
  const [productionMode, setProductionMode] = useState("");
  const [aspectRatio, setAspectRatio] = useState("");
  const [stylePrompt, setStylePrompt] = useState("");
  const [autoApproveBatches, setAutoApproveBatches] = useState(false);

  const effectiveGenre = (genre || genreParam?.default || genreOptions[0] || "") as StudioGenre;
  const effectiveProductionMode = (productionMode ||
    modeParam?.default ||
    modeOptions[0] ||
    "") as StudioProductionMode;
  const effectiveAspectRatio = aspectRatio || aspectParam?.default || aspectOptions[0] || "";

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!title.trim()) return;
    const ok = await createProject({
      title: title.trim(),
      ...(effectiveGenre ? { genre: effectiveGenre } : {}),
      ...(effectiveProductionMode ? { production_mode: effectiveProductionMode } : {}),
      ...(effectiveAspectRatio ? { aspect_ratio: effectiveAspectRatio } : {}),
      style_prompt: stylePrompt.trim(),
      auto_approve_batches: autoApproveBatches,
    });
    if (ok) onClose();
  };

  return (
    <Dialog
      title={t("studio.modal.newProject")}
      onClose={onClose}
      busy={saving}
      dirty={title.trim().length > 0 || stylePrompt.trim().length > 0}
      footer={(requestClose) => (
        <>
          <Button variant="ghost" onClick={requestClose} disabled={saving}>
            {t("studio.modal.cancel")}
          </Button>
          <Button
            variant="primary"
            onClick={handleSubmit}
            disabled={saving || title.trim().length === 0}
          >
            {saving ? <Loader2 size={14} className="animate-spin" /> : <Plus size={14} />}
            {t("studio.modal.create")}
          </Button>
        </>
      )}
    >
      <form className="studio-form-stack" onSubmit={handleSubmit}>
        <ErrorText error={error} onDismiss={clearError} />
        <div className="form-group">
          <label className="field-label" htmlFor="studio-new-title">
            {t("studio.modal.projectTitle")}
          </label>
          <input
            id="studio-new-title"
            className="input-control"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={t("studio.modal.projectTitlePlaceholder")}
            autoFocus
          />
        </div>

        <div className="studio-field-row">
          <div className="form-group">
            <label className="field-label" htmlFor="studio-new-genre">
              {t("studio.modal.genre")}
            </label>
            <select
              id="studio-new-genre"
              className="input-control"
              value={effectiveGenre}
              onChange={(e) => setGenre(e.target.value)}
            >
              {genreOptions.map((g) => {
                const key = `studio.modal.genre.${g}`;
                return (
                  <option key={g} value={g}>
                    {isMessageKey(key) ? t(key) : g}
                  </option>
                );
              })}
            </select>
          </div>

          <div className="form-group">
            <label className="field-label" htmlFor="studio-new-mode">
              {t("studio.modal.mode")}
            </label>
            <select
              id="studio-new-mode"
              className="input-control"
              value={effectiveProductionMode}
              onChange={(e) => setProductionMode(e.target.value)}
            >
              {modeOptions.map((m) => {
                const key = `studio.modal.mode.${m}`;
                return (
                  <option key={m} value={m}>
                    {isMessageKey(key) ? t(key) : m}
                  </option>
                );
              })}
            </select>
          </div>

          <div className="form-group">
            <label className="field-label" htmlFor="studio-new-aspect">
              {t("studio.modal.aspectRatio")}
            </label>
            <select
              id="studio-new-aspect"
              className="input-control"
              value={effectiveAspectRatio}
              onChange={(e) => setAspectRatio(e.target.value)}
            >
              {aspectOptions.map((ar) => (
                <option key={ar} value={ar}>
                  {ar}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="form-group">
          <label className="field-label" htmlFor="studio-new-style">
            {t("studio.modal.stylePrompt")}
          </label>
          <textarea
            id="studio-new-style"
            className="studio-textarea studio-textarea--compact"
            value={stylePrompt}
            onChange={(e) => setStylePrompt(e.target.value)}
            placeholder={t("studio.modal.stylePromptPlaceholder")}
          />
        </div>

        <div className="studio-switch-row">
          <span className="field-label">{t("studio.modal.autoApprove")}</span>
          <Switch
            checked={autoApproveBatches}
            onCheckedChange={setAutoApproveBatches}
            aria-label={t("studio.modal.autoApprove")}
          />
        </div>
      </form>
    </Dialog>
  );
};

export const DeleteProjectDialog: React.FC<{
  project: StudioProject;
  onClose: () => void;
}> = ({ project, onClose }) => {
  const t = useT();
  const deleteProject = useStudioStore((s) => s.deleteProject);
  const saving = useStudioStore((s) => s.saving);
  const error = useStudioStore((s) => s.error);
  const clearError = useStudioStore((s) => s.clearError);
  const [deleteFiles, setDeleteFiles] = useState(false);

  const handleConfirm = async () => {
    const ok = await deleteProject(project.id, deleteFiles);
    if (ok) onClose();
  };

  return (
    <Dialog
      title={t("studio.sidebar.deleteProject")}
      onClose={onClose}
      busy={saving}
      size="sm"
      footer={(requestClose) => (
        <>
          <Button variant="ghost" onClick={requestClose} disabled={saving}>
            {t("studio.modal.cancel")}
          </Button>
          <Button variant="primary" onClick={handleConfirm} disabled={saving}>
            {saving ? <Loader2 size={14} className="animate-spin" /> : <Trash2 size={14} />}
            {t("studio.common.delete")}
          </Button>
        </>
      )}
    >
      <div className="studio-form-stack">
        <ErrorText error={error} onDismiss={clearError} />
        <p>{t("studio.sidebar.deleteConfirm", { title: project.title })}</p>
        <label className="studio-switch-row">
          <span className="field-label">{t("studio.sidebar.deleteWithFiles")}</span>
          <Switch
            checked={deleteFiles}
            onCheckedChange={setDeleteFiles}
            aria-label={t("studio.sidebar.deleteWithFiles")}
          />
        </label>
      </div>
    </Dialog>
  );
};

export const ScriptStagePanel: React.FC<{
  detail: StudioEpisodeDetailResult;
  modelOverride: string;
}> = ({ detail, modelOverride }) => {
  const t = useT();
  const scriptSpec = useNodeSpec(NodeTypes.SCRIPT_NORMALIZE);
  const saveScript = useStudioStore((s) => s.saveScript);
  const runStage = useStudioStore((s) => s.runStage);
  const setActiveStage = useStudioStore((s) => s.setActiveStage);
  const saving = useStudioStore((s) => s.saving);
  const runningStage = useStudioStore((s) => s.runningStage);

  const serverRaw = detail.raw_script ?? "";
  const [localDraft, setLocalDraft] = useState<string | null>(null);
  const draft = localDraft ?? serverRaw;
  const fileInputRef = useRef<HTMLInputElement>(null);

  const busy = saving || runningStage !== null;
  const parsed = detail.normalized_script;
  const scenes = parsed?.scenes ?? [];
  const beatTotal = scenes.reduce((acc, sc) => acc + (sc.beats ?? []).length, 0);

  const handleFileImport = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    const text = await file.text();
    setLocalDraft(text);
    const ok = await saveScript(text);
    if (ok) setLocalDraft(null);
  };

  const handleSaveScript = async () => {
    const ok = await saveScript(draft);
    if (ok) setLocalDraft(null);
  };

  const handleNormalize = async () => {
    if (draft !== serverRaw) {
      const ok = await saveScript(draft);
      if (!ok) return;
      setLocalDraft(null);
    }
    await runStage("script_normalize", modelOverride ? { model: modelOverride } : {});
  };

  return (
    <div className="studio-script-grid">
      <Card
        icon={FileText}
        title={t("studio.script.rawTitle")}
        actions={
          <div className="studio-toolbar">
            <input
              ref={fileInputRef}
              type="file"
              accept=".txt,.md,.markdown"
              className="studio-hidden-input"
              onChange={handleFileImport}
            />
            <Button
              variant="secondary"
              size="sm"
              onClick={() => fileInputRef.current?.click()}
              disabled={busy}
            >
              <Upload size={13} />
              {t("studio.script.importFile")}
            </Button>
          </div>
        }
      >
        <textarea
          className="studio-textarea"
          value={draft}
          onChange={(e) => setLocalDraft(e.target.value)}
          placeholder={t("studio.script.placeholder")}
          aria-label={t("studio.script.rawTitle")}
          disabled={busy}
        />

        <div className="studio-toolbar">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void handleSaveScript()}
            disabled={busy || draft === serverRaw}
          >
            <Save size={13} />
            {saving ? t("studio.common.saving") : t("studio.script.saveScript")}
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() => void handleNormalize()}
            disabled={busy || draft.trim().length === 0}
            title={scriptSpec?.description}
          >
            {runningStage === "script_normalize" ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <Sparkles size={13} />
            )}
            {t("studio.script.normalize")}
          </Button>
        </div>
      </Card>

      <Card
        icon={Sparkles}
        title={t("studio.script.parsedTitle")}
        actions={
          <div className="studio-toolbar">
            {parsed && (
              <Badge>
                {t("studio.script.sceneCount", { scenes: scenes.length, beats: beatTotal })}
              </Badge>
            )}
            {parsed && (
              <Button variant="primary" size="sm" onClick={() => setActiveStage("assets")}>
                {t("studio.script.nextToAssets")}
                <ArrowRight size={13} />
              </Button>
            )}
          </div>
        }
      >
        {!parsed ? (
          <div className="studio-media-empty">{t("studio.script.parsedEmpty")}</div>
        ) : (
          <div className="studio-scene-list">
            {scenes.map((sc) => (
              <div key={sc.code} className="studio-scene-box">
                <div className="studio-item-head">
                  <strong>
                    {sc.code} — {sc.title}
                  </strong>
                  {sc.location && <Badge>{sc.location}</Badge>}
                </div>
                {(sc.beats ?? []).map((bt) => (
                  <div key={bt.code} className="studio-beat-row">
                    <div className="studio-item-head">
                      <span>
                        <strong>{bt.code}</strong> ·{" "}
                        {t("studio.common.secondsShort", { sec: bt.duration_sec })}
                      </span>
                      {bt.asset_tags && bt.asset_tags.length > 0 && (
                        <span>{bt.asset_tags.join(" ")}</span>
                      )}
                    </div>
                    <span>{bt.description}</span>
                  </div>
                ))}
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
};

const AssetBibleCard: React.FC<{
  asset: StudioAsset;
  selected: boolean;
  busy: boolean;
  onToggleSelect: () => void;
}> = ({ asset, selected, busy, onToggleSelect }) => {
  const t = useT();
  const upsertAsset = useStudioStore((s) => s.upsertAsset);
  const uploadAssetImage = useStudioStore((s) => s.uploadAssetImage);
  const runStage = useStudioStore((s) => s.runStage);
  const activeEpisodeId = useStudioStore((s) => s.activeEpisodeId);
  const openVersionDrawer = useStudioStore((s) => s.openVersionDrawer);
  const openGridModal = useStudioStore((s) => s.openGridModal);
  const openMaskModal = useStudioStore((s) => s.openMaskModal);
  const fileRef = useRef<HTMLInputElement>(null);

  const [name, setName] = useState(asset.name);
  const [codeTag, setCodeTag] = useState(asset.code_tag);
  const [kind, setKind] = useState<StudioAssetKind>(asset.kind);
  const [description, setDescription] = useState(asset.description);
  const [prompt, setPrompt] = useState(asset.prompt);
  const [voiceProvider, setVoiceProvider] = useState(asset.voice_provider ?? "");
  const [voiceId, setVoiceId] = useState(asset.voice_id ?? "");
  const [voiceStyle, setVoiceStyle] = useState(asset.voice_style ?? "");

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    await uploadAssetImage(asset.id, file);
  };

  return (
    <div className={`studio-item-card ${selected ? "studio-item-card--selected" : ""}`}>
      <div className="studio-item-head">
        <div className="studio-item-head-left">
          <input
            type="checkbox"
            checked={selected}
            onChange={onToggleSelect}
            aria-label={asset.name}
          />
          <Badge>{t(`studio.assets.kind.${kind}`)}</Badge>
          <strong>{asset.code_tag}</strong>
        </div>
        <div className="studio-toolbar">
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              askAgentAboutMediaAsset({
                assetId: asset.primary_asset_id || undefined,
                filePath: asset.primary_path || undefined,
                fileName: asset.name,
              })
            }
            title={t("studio.common.askAgent")}
          >
            <Bot size={13} />
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              void upsertAsset({
                id: asset.id,
                project_id: asset.project_id,
                kind: asset.kind,
                name: asset.name,
                delete: true,
              })
            }
            disabled={busy}
            title={t("studio.common.delete")}
          >
            <Trash2 size={13} />
          </Button>
        </div>
      </div>

      <div className="studio-media-frame">
        <SignedStudioImage
          assetId={asset.primary_asset_id}
          alt={asset.name}
          placeholder={t("studio.assets.noPreview")}
        />
      </div>

      <div className="studio-field-row">
        <input
          className="input-control"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t("studio.assets.name")}
          aria-label={t("studio.assets.name")}
          disabled={busy}
        />
        <input
          className="input-control"
          value={codeTag}
          onChange={(e) => setCodeTag(e.target.value)}
          placeholder={t("studio.assets.codeTag")}
          aria-label={t("studio.assets.codeTag")}
          disabled={busy}
        />
        <select
          className="input-control"
          value={kind}
          onChange={(e) => setKind(e.target.value as StudioAssetKind)}
          aria-label={t("studio.assets.kind")}
          disabled={busy}
        >
          {StudioAssetKindValues.map((k) => (
            <option key={k} value={k}>
              {t(`studio.assets.kind.${k}`)}
            </option>
          ))}
        </select>
      </div>

      <textarea
        className="studio-textarea studio-textarea--compact"
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
        placeholder={t("studio.assets.prompt")}
        aria-label={t("studio.assets.prompt")}
        disabled={busy}
      />

      <input
        className="input-control"
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder={t("studio.assets.description")}
        aria-label={t("studio.assets.description")}
        disabled={busy}
      />

      {kind === "character" && (
        <div className="studio-voice-box">
          <div className="studio-voice-box__title">
            <Speech size={13} />
            <strong>{t("studio.voice.title")}</strong>
          </div>
          <div className="studio-voice-grid">
            <input
              className="input-control"
              value={voiceProvider}
              onChange={(e) => setVoiceProvider(e.target.value)}
              placeholder={t("studio.voice.provider")}
              aria-label={t("studio.voice.provider")}
              disabled={busy}
            />
            <input
              className="input-control"
              value={voiceId}
              onChange={(e) => setVoiceId(e.target.value)}
              placeholder={t("studio.voice.voiceId")}
              aria-label={t("studio.voice.voiceId")}
              disabled={busy}
            />
            <input
              className="input-control"
              value={voiceStyle}
              onChange={(e) => setVoiceStyle(e.target.value)}
              placeholder={t("studio.voice.style")}
              aria-label={t("studio.voice.style")}
              disabled={busy}
            />
          </div>
        </div>
      )}

      <div className="studio-toolbar">
        <input
          ref={fileRef}
          type="file"
          accept="image/*"
          className="studio-hidden-input"
          onChange={handleUpload}
        />
        <Button
          variant="secondary"
          size="sm"
          onClick={() =>
            void upsertAsset({
              id: asset.id,
              project_id: asset.project_id,
              kind,
              name,
              code_tag: codeTag,
              description,
              prompt,
              primary_asset_id: asset.primary_asset_id,
              primary_path: asset.primary_path,
              voice_provider: voiceProvider.trim(),
              voice_id: voiceId.trim(),
              voice_style: voiceStyle.trim(),
            })
          }
          disabled={busy || !name.trim()}
        >
          <Save size={13} />
          {t("studio.common.save")}
        </Button>
        <Button
          variant="primary"
          size="sm"
          onClick={() => void runStage("generate_assets", { asset_ids: [asset.id], force: true })}
          disabled={busy}
        >
          <ImageIcon size={13} />
          {t("studio.assets.genSheet")}
        </Button>
        <Button variant="ghost" size="sm" onClick={() => fileRef.current?.click()} disabled={busy}>
          <Upload size={13} />
          {t("studio.assets.uploadImage")}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={() =>
            openVersionDrawer({
              projectId: asset.project_id,
              episodeId: activeEpisodeId ?? undefined,
              targetKind: "asset",
              targetId: asset.id,
              label: asset.name,
              mediaPath: asset.primary_path,
              mediaAssetId: asset.primary_asset_id,
              promptExpected: prompt,
            })
          }
          disabled={busy}
        >
          <Layers size={13} />
          {t("studio.versions.button")}
        </Button>
        {asset.primary_path && (
          <>
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                openGridModal({
                  projectId: asset.project_id,
                  episodeId: activeEpisodeId ?? undefined,
                  targetKind: "asset",
                  targetId: asset.id,
                  imagePath: asset.primary_path,
                  assetId: asset.primary_asset_id,
                })
              }
              disabled={busy}
            >
              <Grid size={13} />
              {t("studio.gridSplit.button")}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                openMaskModal({
                  projectId: asset.project_id,
                  episodeId: activeEpisodeId ?? undefined,
                  targetKind: "asset",
                  targetId: asset.id,
                  imagePath: asset.primary_path,
                  assetId: asset.primary_asset_id,
                  defaultPrompt: prompt,
                })
              }
              disabled={busy}
            >
              <Paintbrush size={13} />
              {t("studio.maskEdit.button")}
            </Button>
          </>
        )}
      </div>
    </div>
  );
};

export const AssetsStagePanel: React.FC<{
  detail: StudioEpisodeDetailResult;
  textModel: string;
  imageModel: string;
}> = ({ detail, textModel, imageModel }) => {
  const t = useT();
  const extractSpec = useNodeSpec(NodeTypes.EXTRACT_ASSETS);
  const runStage = useStudioStore((s) => s.runStage);
  const upsertAsset = useStudioStore((s) => s.upsertAsset);
  const runningStage = useStudioStore((s) => s.runningStage);
  const saving = useStudioStore((s) => s.saving);
  const selectedAssetIds = useStudioStore((s) => s.selectedAssetIds);
  const toggleSelected = useStudioStore((s) => s.toggleSelectedAssetId);

  const busy = saving || runningStage !== null;

  const handleAddBlankAsset = async () => {
    await upsertAsset({
      project_id: detail.project.id,
      kind: "character",
      name: t("studio.assets.defaultName", { num: detail.assets.length + 1 }),
    });
  };

  return (
    <div className="studio-form-stack">
      <div className="studio-stage-header">
        <h2 className="card-title">{t("studio.assets.title")}</h2>
        <div className="studio-toolbar">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void runStage("extract_assets", textModel ? { model: textModel } : {})}
            disabled={busy || !detail.normalized_script}
            title={extractSpec?.description}
          >
            {runningStage === "extract_assets" ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <Sparkles size={13} />
            )}
            {t("studio.assets.extract")}
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() =>
              void runStage("generate_assets", imageModel ? { model: imageModel } : {})
            }
            disabled={busy || detail.assets.length === 0}
          >
            {runningStage === "generate_assets" ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <Wand2 size={13} />
            )}
            {t("studio.assets.generateAll")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void handleAddBlankAsset()}
            disabled={busy}
          >
            <Plus size={13} />
            {t("studio.assets.addAsset")}
          </Button>
        </div>
      </div>

      {detail.assets.length === 0 ? (
        <div className="studio-empty-state">
          <Layers size={28} />
          <p>{t("studio.assets.empty")}</p>
        </div>
      ) : (
        <div className="studio-card-grid">
          {detail.assets.map((asset) => (
            <AssetBibleCard
              key={asset.id}
              asset={asset}
              selected={selectedAssetIds.includes(asset.id)}
              busy={busy}
              onToggleSelect={() => toggleSelected(asset.id)}
            />
          ))}
        </div>
      )}
    </div>
  );
};

const StoryboardShotCard: React.FC<{
  projectId: string;
  shot: StudioShot;
  index: number;
  total: number;
  shotModeOptions: string[];
  durMin?: number;
  durMax?: number;
  durDefault?: number;
  busy: boolean;
  onMove: (delta: number) => void;
}> = ({
  projectId,
  shot,
  index,
  total,
  shotModeOptions,
  durMin,
  durMax,
  durDefault,
  busy,
  onMove,
}) => {
  const t = useT();
  const upsertShot = useStudioStore((s) => s.upsertShot);
  const uploadShotKeyframe = useStudioStore((s) => s.uploadShotKeyframe);
  const runStage = useStudioStore((s) => s.runStage);
  const openVersionDrawer = useStudioStore((s) => s.openVersionDrawer);
  const openGridModal = useStudioStore((s) => s.openGridModal);
  const openMaskModal = useStudioStore((s) => s.openMaskModal);
  const fileRef = useRef<HTMLInputElement>(null);

  const [title, setTitle] = useState(shot.title);
  const [shotMode, setShotMode] = useState<StudioShotMode>(shot.shot_mode);
  const [durationSec, setDurationSec] = useState(shot.duration_sec);
  const [tagsText, setTagsText] = useState((shot.ref_asset_tags ?? []).join(", "));
  const [imagePrompt, setImagePrompt] = useState(shot.image_prompt);
  const [videoPrompt, setVideoPrompt] = useState(shot.video_prompt);
  const [narrationText, setNarrationText] = useState(shot.narration_text);

  const parsedTags = tagsText
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);

  return (
    <div className="studio-item-card">
      <div className="studio-item-head">
        <div className="studio-item-head-left">
          <Badge>#{shot.seq}</Badge>
          {shot.scene_code && <Badge>{shot.scene_code}</Badge>}
          <Badge>{t(`studio.shotStatus.${shot.status}`)}</Badge>
        </div>
        <div className="studio-toolbar">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onMove(-1)}
            disabled={busy || index === 0}
            title={t("studio.storyboard.moveUp")}
            aria-label={t("studio.storyboard.moveUp")}
          >
            <ArrowUp size={13} />
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onMove(1)}
            disabled={busy || index === total - 1}
            title={t("studio.storyboard.moveDown")}
            aria-label={t("studio.storyboard.moveDown")}
          >
            <ArrowDown size={13} />
          </Button>
        </div>
      </div>

      <div className="studio-media-frame">
        <SignedStudioImage
          assetId={shot.keyframe_asset_id}
          alt={shot.title}
          placeholder={t("studio.storyboard.noKeyframe")}
        />
      </div>

      <div className="studio-field-row">
        <input
          className="input-control"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder={t("studio.storyboard.shotTitle")}
          aria-label={t("studio.storyboard.shotTitle")}
          disabled={busy}
        />
        <select
          className="input-control"
          value={shotMode}
          onChange={(e) => setShotMode(e.target.value as StudioShotMode)}
          aria-label={t("studio.storyboard.shotMode")}
          disabled={busy}
        >
          {shotModeOptions.map((m) => {
            const modeKey = `studio.shotMode.${m}`;
            return (
              <option key={m} value={m}>
                {isMessageKey(modeKey) ? t(modeKey) : m}
              </option>
            );
          })}
        </select>
        <input
          type="number"
          min={durMin}
          max={durMax}
          className="input-control"
          value={durationSec}
          aria-label={t("studio.storyboard.durationSec")}
          onChange={(e) => {
            const raw = Number(e.target.value);
            const floor = durMin ?? 1;
            const ceil = durMax;
            const next = Number.isFinite(raw) && raw > 0 ? raw : (durDefault ?? floor);
            setDurationSec(
              ceil !== undefined ? Math.min(ceil, Math.max(floor, next)) : Math.max(floor, next)
            );
          }}
          disabled={busy}
        />
      </div>

      <input
        className="input-control"
        value={tagsText}
        onChange={(e) => setTagsText(e.target.value)}
        placeholder={t("studio.storyboard.tags")}
        aria-label={t("studio.storyboard.tags")}
        disabled={busy}
      />

      <textarea
        className="studio-textarea studio-textarea--compact"
        value={imagePrompt}
        onChange={(e) => setImagePrompt(e.target.value)}
        placeholder={t("studio.storyboard.imagePrompt")}
        aria-label={t("studio.storyboard.imagePrompt")}
        disabled={busy}
      />

      <textarea
        className="studio-textarea studio-textarea--compact"
        value={videoPrompt}
        onChange={(e) => setVideoPrompt(e.target.value)}
        placeholder={t("studio.storyboard.videoPrompt")}
        aria-label={t("studio.storyboard.videoPrompt")}
        disabled={busy}
      />

      <input
        className="input-control"
        value={narrationText}
        onChange={(e) => setNarrationText(e.target.value)}
        placeholder={t("studio.storyboard.narrationText")}
        aria-label={t("studio.storyboard.narrationText")}
        disabled={busy}
      />

      <div className="studio-toolbar">
        <input
          ref={fileRef}
          type="file"
          accept="image/*"
          className="studio-hidden-input"
          onChange={async (e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (f) await uploadShotKeyframe(shot.id, f);
          }}
        />
        <Button
          variant="secondary"
          size="sm"
          onClick={() =>
            void upsertShot({
              id: shot.id,
              episode_id: shot.episode_id,
              title,
              shot_mode: shotMode,
              duration_sec: durationSec,
              ref_asset_tags: parsedTags,
              image_prompt: imagePrompt,
              video_prompt: videoPrompt,
              narration_text: narrationText,
            })
          }
          disabled={busy}
        >
          <Save size={13} />
          {t("studio.common.save")}
        </Button>
        <Button
          variant="primary"
          size="sm"
          onClick={() => void runStage("generate_keyframes", { shot_ids: [shot.id], force: true })}
          disabled={busy}
        >
          <ImageIcon size={13} />
          {t("studio.storyboard.genKeyframe")}
        </Button>
        <Button variant="ghost" size="sm" onClick={() => fileRef.current?.click()} disabled={busy}>
          <Upload size={13} />
          {t("studio.storyboard.uploadKeyframe")}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={() =>
            openVersionDrawer({
              projectId,
              episodeId: shot.episode_id,
              targetKind: "keyframe",
              targetId: shot.id,
              label: shot.title,
              mediaPath: shot.keyframe_path,
              mediaAssetId: shot.keyframe_asset_id,
              promptExpected: imagePrompt,
            })
          }
          disabled={busy}
        >
          <Layers size={13} />
          {t("studio.versions.button")}
        </Button>
        {shot.keyframe_path && (
          <>
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                openGridModal({
                  projectId,
                  episodeId: shot.episode_id,
                  targetKind: "keyframe",
                  targetId: shot.id,
                  imagePath: shot.keyframe_path,
                  assetId: shot.keyframe_asset_id,
                })
              }
              disabled={busy}
            >
              <Grid size={13} />
              {t("studio.gridSplit.button")}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                openMaskModal({
                  projectId,
                  episodeId: shot.episode_id,
                  targetKind: "keyframe",
                  targetId: shot.id,
                  imagePath: shot.keyframe_path,
                  assetId: shot.keyframe_asset_id,
                  defaultPrompt: imagePrompt,
                })
              }
              disabled={busy}
            >
              <Paintbrush size={13} />
              {t("studio.maskEdit.button")}
            </Button>
          </>
        )}
      </div>
    </div>
  );
};

export const StoryboardStagePanel: React.FC<{
  detail: StudioEpisodeDetailResult;
  textModel: string;
  imageModel: string;
}> = ({ detail, textModel, imageModel }) => {
  const t = useT();
  const shotSpec = useNodeSpec(NodeTypes.AUTHOR_SHOT_PROMPTS);
  const shotModeParam = findParam(shotSpec, "shot_mode_default");
  const shotModes = shotModeParam?.options ?? [...StudioShotModeValues];
  const defaultShotMode = (
    detail.project.production_mode === "r2v"
      ? "r2v"
      : shotModeParam?.default || shotModes[0] || "single"
  ) as StudioShotMode;
  const defaultSceneCode =
    detail.normalized_script?.scenes?.[0]?.code ??
    detail.shots[detail.shots.length - 1]?.scene_code ??
    "";
  const durParam = findParam(shotSpec, "default_duration_sec");
  const durMin = durParam?.min;
  const durMax = durParam?.max;
  const durDefault = durParam?.default ? Number(durParam.default) : undefined;
  const runStage = useStudioStore((s) => s.runStage);
  const upsertShot = useStudioStore((s) => s.upsertShot);
  const reorderShots = useStudioStore((s) => s.reorderShots);
  const runningStage = useStudioStore((s) => s.runningStage);
  const saving = useStudioStore((s) => s.saving);

  const busy = saving || runningStage !== null;

  const handleMove = (index: number, delta: number) => {
    const nextIdx = index + delta;
    if (nextIdx < 0 || nextIdx >= detail.shots.length) return;
    const ids = detail.shots.map((s) => s.id);
    const [moved] = ids.splice(index, 1);
    ids.splice(nextIdx, 0, moved);
    void reorderShots(ids);
  };

  return (
    <div className="studio-form-stack">
      <div className="studio-stage-header">
        <h2 className="card-title">{t("studio.storyboard.title")}</h2>
        <div className="studio-toolbar">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void runStage("author_shots", textModel ? { model: textModel } : {})}
            disabled={busy || !detail.normalized_script}
            title={shotSpec?.description}
          >
            {runningStage === "author_shots" ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <Sparkles size={13} />
            )}
            {t("studio.storyboard.author")}
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() =>
              void runStage("generate_keyframes", imageModel ? { model: imageModel } : {})
            }
            disabled={busy || detail.shots.length === 0}
          >
            {runningStage === "generate_keyframes" ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <Wand2 size={13} />
            )}
            {t("studio.storyboard.generateKeyframes")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              void upsertShot({
                episode_id: detail.episode.id,
                ...(defaultSceneCode ? { scene_code: defaultSceneCode } : {}),
                title: t("studio.storyboard.defaultShotTitle", { num: detail.shots.length + 1 }),
                shot_mode: defaultShotMode,
                ...(durDefault !== undefined ? { duration_sec: durDefault } : {}),
              })
            }
            disabled={busy}
          >
            <Plus size={13} />
            {t("studio.storyboard.addShot")}
          </Button>
        </div>
      </div>

      {detail.shots.length === 0 ? (
        <div className="studio-empty-state">
          <Clapperboard size={28} />
          <p>{t("studio.storyboard.empty")}</p>
        </div>
      ) : (
        <div className="studio-card-grid">
          {detail.shots.map((shot, idx) => (
            <StoryboardShotCard
              key={shot.id}
              projectId={detail.project.id}
              shot={shot}
              index={idx}
              total={detail.shots.length}
              shotModeOptions={shotModes}
              durMin={durMin}
              durMax={durMax}
              durDefault={durDefault}
              busy={busy}
              onMove={(delta) => handleMove(idx, delta)}
            />
          ))}
        </div>
      )}
    </div>
  );
};

export const VideoShotsStagePanel: React.FC<{
  detail: StudioEpisodeDetailResult;
  videoModel: string;
  ttsModel: string;
}> = ({ detail, videoModel, ttsModel }) => {
  const t = useT();
  const videoSpec = useNodeSpec(NodeTypes.VIDEO_GENERATE);
  const resParam = findParam(videoSpec, "resolution");
  const resOptions = resParam?.options ?? [];
  const [resolution, setResolution] = useState("");
  const effectiveResolution = resolution || resParam?.default || resOptions[0] || "";

  const runStage = useStudioStore((s) => s.runStage);
  const enqueueBatch = useStudioStore((s) => s.enqueueBatch);
  const extractLastFrame = useStudioStore((s) => s.extractLastFrame);
  const openVersionDrawer = useStudioStore((s) => s.openVersionDrawer);
  const qcByTarget = useStudioStore((s) => s.qcByTarget);
  const probeShot = useStudioStore((s) => s.probeShot);
  const probeMap = useStudioStore((s) => s.probeMap);
  const runningStage = useStudioStore((s) => s.runningStage);
  const saving = useStudioStore((s) => s.saving);
  const busy = saving || runningStage !== null;

  const renderProbeBadge = (probe?: ProbeMediaOutput) => {
    if (!probe) return null;
    const dur = probe.duration_sec ?? 0;
    const width = probe.width ?? 0;
    const height = probe.height ?? 0;
    const fps = probe.fps ?? 0;
    const silentRatio = probe.silent_ratio ?? 0;
    const isSilent = !probe.has_audio || silentRatio > appConfig.studio.ui.probeSilenceWarnRatio;
    return (
      <div className="studio-toolbar">
        <Badge>
          {t("studio.video.probeMeta", {
            duration: dur.toFixed(1),
            width,
            height,
            fps: Math.round(fps),
          })}
        </Badge>
        {isSilent ? (
          <Badge className="studio-badge--warn">
            {t("studio.video.silentWarn", { ratio: Math.round(silentRatio * 100) })}
          </Badge>
        ) : (
          <Badge className="studio-badge--ok">{t("studio.video.hasAudio")}</Badge>
        )}
      </div>
    );
  };

  return (
    <div className="studio-form-stack">
      <div className="studio-stage-header">
        <h2 className="card-title">{t("studio.video.title")}</h2>
        <div className="studio-toolbar">
          <select
            className="input-control"
            value={effectiveResolution}
            onChange={(e) => setResolution(e.target.value)}
            aria-label={t("studio.video.resolution")}
            disabled={busy}
          >
            {resOptions.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void runStage("generate_tts", ttsModel ? { model: ttsModel } : {})}
            disabled={busy || detail.shots.length === 0}
          >
            {runningStage === "generate_tts" ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <Volume2 size={13} />
            )}
            {t("studio.video.generateTTS")}
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() =>
              void runStage("generate_videos", {
                resolution: effectiveResolution,
                ...(videoModel ? { model: videoModel } : {}),
              })
            }
            disabled={busy || detail.shots.length === 0}
          >
            {runningStage === "generate_videos" ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <Film size={13} />
            )}
            {t("studio.video.generateAll")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void runStage("probe_shots")}
            disabled={busy || !detail.shots.some((s) => Boolean(s.video_path))}
          >
            {t("studio.video.probeAll")}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() =>
              void enqueueBatch({
                stage: "generate_videos",
                ...(videoModel ? { model: videoModel } : {}),
              })
            }
            disabled={busy || detail.shots.length === 0}
          >
            <Play size={13} />
            {t("studio.queue.enqueueBatch")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void enqueueBatch({ stage: "lipsync" })}
            disabled={busy || !detail.shots.some((s) => Boolean(s.video_path && s.audio_path))}
          >
            <Speech size={13} />
            {t("studio.voice.lipSyncButton")}
          </Button>
        </div>
      </div>

      {detail.shots.length === 0 ? (
        <div className="studio-empty-state">
          <Film size={28} />
          <p>{t("studio.video.empty")}</p>
        </div>
      ) : (
        <div className="studio-card-grid">
          {detail.shots.map((shot, idx) => {
            const probe = probeMap[shot.id];
            const nextShot = detail.shots[idx + 1];
            const latestQC = qcByTarget[targetVersionKey("video", shot.id)];
            return (
              <div key={shot.id} className="studio-item-card">
                <div className="studio-item-head">
                  <div className="studio-item-head-left">
                    <Badge>#{shot.seq}</Badge>
                    <strong>{shot.title}</strong>
                  </div>
                  <div className="studio-phase2-meta-grid">
                    {latestQC && (
                      <Badge
                        className={
                          latestQC.verdict === "pass"
                            ? "studio-badge--ok"
                            : latestQC.verdict === "fail"
                              ? "studio-badge--error"
                              : "studio-badge--warn"
                        }
                      >
                        {t(`studio.qc.verdict.${latestQC.verdict}` as never)}
                      </Badge>
                    )}
                    <Badge>{t(`studio.shotStatus.${shot.status}`)}</Badge>
                  </div>
                </div>

                <div className="studio-media-frame">
                  <SignedStudioVideo
                    assetId={shot.video_asset_id}
                    placeholder={t("studio.video.noClip")}
                  />
                </div>

                <SignedStudioAudio assetId={shot.audio_asset_id} />
                {renderProbeBadge(probe)}

                {shot.narration_text && <p>{shot.narration_text}</p>}

                <div className="studio-toolbar">
                  <Button
                    variant="primary"
                    size="sm"
                    onClick={() =>
                      void runStage("generate_videos", {
                        shot_ids: [shot.id],
                        resolution: effectiveResolution,
                        force: true,
                        ...(videoModel ? { model: videoModel } : {}),
                      })
                    }
                    disabled={busy}
                  >
                    <Film size={13} />
                    {t("studio.video.genClip")}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() =>
                      void runStage("generate_tts", {
                        shot_ids: [shot.id],
                        force: true,
                        ...(ttsModel ? { model: ttsModel } : {}),
                      })
                    }
                    disabled={busy || (!shot.narration_text && !shot.dialogues?.length)}
                  >
                    <Volume2 size={13} />
                    {t("studio.video.genTTS")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      openVersionDrawer({
                        projectId: detail.project.id,
                        episodeId: detail.episode.id,
                        targetKind: "video",
                        targetId: shot.id,
                        label: shot.title,
                        mediaPath: shot.video_path,
                        mediaAssetId: shot.video_asset_id,
                        promptExpected: shot.video_prompt,
                        referencePaths: shot.keyframe_path ? [shot.keyframe_path] : undefined,
                      })
                    }
                    disabled={busy}
                  >
                    <Layers size={13} />
                    {t("studio.versions.button")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      if (nextShot) {
                        void extractLastFrame({
                          source_shot_id: shot.id,
                          target_shot_id: nextShot.id,
                        });
                      }
                    }}
                    disabled={busy || !shot.video_path || !nextShot}
                    title={
                      nextShot
                        ? t("studio.extractFrame.button")
                        : t("studio.extractFrame.noNextShot")
                    }
                  >
                    <ImageIcon size={13} />
                    {t("studio.extractFrame.button")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => void probeShot(shot)}
                    disabled={busy || !shot.video_path}
                  >
                    {t("studio.video.probe")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      askAgentAboutMediaAsset({
                        assetId: shot.video_asset_id || undefined,
                        filePath: shot.video_path || undefined,
                        fileName: shot.title,
                      })
                    }
                    title={t("studio.common.askAgent")}
                  >
                    <Bot size={13} />
                  </Button>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};

export const ExportStagePanel: React.FC<{
  detail: StudioEpisodeDetailResult;
}> = ({ detail }) => {
  const t = useT();
  const concatSpec = useNodeSpec(NodeTypes.VIDEO_CONCAT_BASIC);
  const resDefault = findParam(concatSpec, "resolution")?.default ?? "";
  const fpsParam = findParam(concatSpec, "fps");
  const fpsMin = fpsParam?.min;
  const fpsMax = fpsParam?.max;
  const fpsDefault = fpsParam?.default ? Number(fpsParam.default) : undefined;

  const [resolution, setResolution] = useState("");
  const [fps, setFps] = useState<number | null>(null);
  const [narrationPath, setNarrationPath] = useState("");
  const effectiveResolution = resolution || resDefault;
  const effectiveFPS = fps ?? fpsDefault;
  const runStage = useStudioStore((s) => s.runStage);
  const runningStage = useStudioStore((s) => s.runningStage);
  const openFolder = useSystemStore((s) => s.openFolder);

  const readyShots = detail.shots.filter((s) => Boolean(s.video_path));
  const audioShots = detail.shots.filter((s) => Boolean(s.audio_path));
  const busy = runningStage !== null;

  return (
    <div className="studio-form-stack">
      <Card
        icon={Clapperboard}
        title={t("studio.export.title")}
        actions={
          <Badge>
            {t("studio.export.clipsReady", {
              ready: readyShots.length,
              total: detail.shots.length,
            })}
          </Badge>
        }
      >
        <div className="studio-field-row">
          <div className="form-group">
            <label className="field-label" htmlFor="studio-export-res">
              {t("studio.export.resolution")}
            </label>
            <input
              id="studio-export-res"
              className="input-control"
              value={effectiveResolution}
              onChange={(e) => setResolution(e.target.value)}
              disabled={busy}
            />
          </div>

          <div className="form-group">
            <label className="field-label" htmlFor="studio-export-fps">
              {t("studio.export.fps")}
            </label>
            <input
              id="studio-export-fps"
              type="number"
              min={fpsMin}
              max={fpsMax}
              className="input-control"
              value={effectiveFPS ?? ""}
              onChange={(e) => setFps(e.target.value === "" ? null : Number(e.target.value))}
              disabled={busy}
            />
          </div>

          <div className="form-group">
            <label className="field-label" htmlFor="studio-export-narration">
              {t("studio.export.narrationTrack")}
            </label>
            <select
              id="studio-export-narration"
              className="input-control"
              value={narrationPath}
              onChange={(e) => setNarrationPath(e.target.value)}
              disabled={busy}
            >
              <option value="">{t("studio.export.narrationNone")}</option>
              {audioShots.map((s) => (
                <option key={s.id} value={s.audio_path}>
                  #{s.seq} {s.title} ({mediaBasename(s.audio_path)})
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="studio-toolbar">
          <Button
            variant="primary"
            onClick={() =>
              void runStage("concat_video", {
                ...(effectiveResolution ? { resolution: effectiveResolution } : {}),
                ...(effectiveFPS !== undefined ? { fps: effectiveFPS } : {}),
                ...(narrationPath ? { narration_audio_path: narrationPath } : {}),
              })
            }
            disabled={busy || readyShots.length === 0}
          >
            {runningStage === "concat_video" ? (
              <Loader2 size={14} className="animate-spin" />
            ) : (
              <Clapperboard size={14} />
            )}
            {t("studio.export.assemble")}
          </Button>

          {detail.final_video_path && (
            <>
              <Button
                variant="secondary"
                onClick={() =>
                  void saveMediaToDevice(
                    detail.final_video_path!,
                    mediaBasename(detail.final_video_path!),
                    undefined,
                    detail.final_video_asset_id
                  )
                }
              >
                <Download size={14} />
                {t("studio.export.download")}
              </Button>
              <Button variant="ghost" onClick={() => void openFolder("workspace_root")}>
                <FolderOpen size={14} />
                {t("studio.export.openFolder")}
              </Button>
              <Button
                variant="ghost"
                onClick={() =>
                  askAgentAboutMediaAsset({
                    assetId: detail.final_video_asset_id,
                    filePath: detail.final_video_path,
                    mimeType: "video/mp4",
                  })
                }
              >
                <Bot size={14} />
                {t("studio.common.askAgent")}
              </Button>
            </>
          )}
        </div>
      </Card>

      <Card icon={Film} title={detail.episode.title}>
        <div className="studio-media-frame studio-media-frame--export">
          <SignedStudioVideo
            assetId={detail.final_video_asset_id}
            placeholder={t("studio.export.noOutput")}
            large
          />
        </div>
        {detail.final_video_path && (
          <p>{t("studio.export.readyOutput", { path: detail.final_video_path })}</p>
        )}
      </Card>
    </div>
  );
};
