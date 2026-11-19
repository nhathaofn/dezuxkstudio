import React, { useRef, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  Clapperboard,
  Grid,
  Image as ImageIcon,
  Layers,
  Loader2,
  Paintbrush,
  Plus,
  Save,
  Sparkles,
  Upload,
  Wand2,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import {
  NodeTypes,
  StudioShotModeValues,
  type StudioEpisodeDetailResult,
  type StudioShot,
  type StudioShotMode,
} from "../../contracts/generated";
import { isMessageKey, useT } from "../../lib/i18n";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { SignedStudioImage } from "./SignedStudioMedia";
import { useStudioStore } from "./useStudioStore";

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
