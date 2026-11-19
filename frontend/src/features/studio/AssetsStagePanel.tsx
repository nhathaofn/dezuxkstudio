import React, { useRef, useState } from "react";
import { Bot, Grid, Image as ImageIcon, Layers, Loader2, Paintbrush, Plus, Save, Sparkles, Speech, Trash2, Upload, Wand2 } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import {
  NodeTypes,
  StudioAssetKindValues,
  type StudioAsset,
  type StudioAssetKind,
  type StudioEpisodeDetailResult,
} from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { askAgentAboutMediaAsset } from "../agent/screenContext";
import { useNodeSpec } from "../workflow/useNodeSpecs";
import { SignedStudioImage } from "./SignedStudioMedia";
import { useStudioStore } from "./useStudioStore";

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
