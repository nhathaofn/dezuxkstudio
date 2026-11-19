import {
  Actions,
  NodeTypes,
  type LipSyncGenerateInput,
  type LipSyncGenerateOutput,
  type ProbeMediaOutput,
  type StudioBatchStatus,
  type StudioProjectChangedAction,
  type TTSDubMuxInput,
  type TTSDubMuxOutput,
} from "../../contracts/generated";
import { rtClient, toActionError, uploadMediaFile } from "../../lib/rtClient";
import { normalizeEpisodeDetail } from "./studioFormatters";
import { isHeavyStudioStage, type StudioGet, type StudioSet, type StudioState } from "./storeTypes";

export function createStageSlice(
  set: StudioSet,
  get: StudioGet
): Pick<
  StudioState,
  | "saveScript"
  | "upsertAsset"
  | "uploadAssetImage"
  | "upsertShot"
  | "uploadShotKeyframe"
  | "reorderShots"
  | "runStage"
  | "probeShot"
  | "runShotDubMux"
  | "runShotLipSync"
  | "receiveProjectChanged"
  | "receiveEpisodeUpdated"
  | "receiveBatchProgress"
> {
  return {
    saveScript: async (rawScript) => {
      const epId = get().activeEpisodeId;
      if (!epId) return false;
      set({ saving: true, error: null });
      try {
        const detail = normalizeEpisodeDetail(
          await rtClient.request(Actions.STUDIO_EPISODE_SAVE_SCRIPT, {
            episode_id: epId,
            raw_script: rawScript,
          })
        );
        set({
          episodeDetail: detail,
          saving: false,
          lastSummary: detail.episode.raw_script_path || detail.episode.code,
        });
        return true;
      } catch (err) {
        set({ error: toActionError(err), saving: false });
        return false;
      }
    },

    upsertAsset: async (payload) => {
      set({ saving: true, error: null });
      try {
        const res = await rtClient.request(Actions.STUDIO_ASSET_UPSERT, payload);
        set((s) => ({
          episodeDetail: s.episodeDetail
            ? { ...s.episodeDetail, assets: res.assets ?? [] }
            : s.episodeDetail,
          projectDetail: s.projectDetail
            ? { ...s.projectDetail, assets: res.assets ?? [] }
            : s.projectDetail,
          saving: false,
        }));
        return true;
      } catch (err) {
        set({ error: toActionError(err), saving: false });
        return false;
      }
    },

    uploadAssetImage: async (assetId, file) => {
      const ep = get().episodeDetail;
      const asset = ep?.assets.find((a) => a.id === assetId);
      if (!ep || !asset) return false;
      set({ saving: true, error: null });
      try {
        const uploaded = await uploadMediaFile(file);
        const reg = await rtClient.request(Actions.MEDIA_REGISTER_INPUT, {
          file_path: uploaded.path,
          kind: "image",
          source_node: "studio.asset_upload",
        });
        return await get().upsertAsset({
          id: asset.id,
          project_id: asset.project_id,
          kind: asset.kind,
          name: asset.name,
          code_tag: asset.code_tag,
          description: asset.description,
          prompt: asset.prompt,
          primary_asset_id: reg.id,
          primary_path: reg.file_path,
        });
      } catch (err) {
        set({ error: toActionError(err), saving: false });
        return false;
      }
    },

    upsertShot: async (payload) => {
      set({ saving: true, error: null });
      try {
        const res = await rtClient.request(Actions.STUDIO_SHOT_UPSERT, payload);
        set((s) => ({
          episodeDetail: s.episodeDetail
            ? normalizeEpisodeDetail({ ...s.episodeDetail, shots: res.shots ?? [] })
            : s.episodeDetail,
          saving: false,
        }));
        return true;
      } catch (err) {
        set({ error: toActionError(err), saving: false });
        return false;
      }
    },

    uploadShotKeyframe: async (shotId, file) => {
      const ep = get().episodeDetail;
      const shot = ep?.shots.find((sh) => sh.id === shotId);
      if (!ep || !shot) return false;
      set({ saving: true, error: null });
      try {
        const uploaded = await uploadMediaFile(file);
        const reg = await rtClient.request(Actions.MEDIA_REGISTER_INPUT, {
          file_path: uploaded.path,
          kind: "image",
          source_node: "studio.keyframe_upload",
        });
        return await get().upsertShot({
          id: shot.id,
          episode_id: shot.episode_id,
          keyframe_asset_id: reg.id,
          keyframe_path: reg.file_path,
          status:
            shot.status === "pending" || shot.status === "failed" ? "keyframe_ready" : shot.status,
        });
      } catch (err) {
        set({ error: toActionError(err), saving: false });
        return false;
      }
    },

    reorderShots: async (shotIDs) => {
      const epId = get().activeEpisodeId;
      if (!epId) return;
      set({ saving: true, error: null });
      try {
        const res = await rtClient.request(Actions.STUDIO_SHOT_REORDER, {
          episode_id: epId,
          shot_ids: shotIDs,
        });
        set((s) => ({
          episodeDetail: s.episodeDetail
            ? normalizeEpisodeDetail({ ...s.episodeDetail, shots: res.shots ?? [] })
            : s.episodeDetail,
          saving: false,
        }));
      } catch (err) {
        set({ error: toActionError(err), saving: false });
      }
    },

    runStage: async (stage, opts = {}) => {
      const epId = get().activeEpisodeId;
      if (!epId) return false;
      const proj = get().projectDetail?.project;
      if (proj && !proj.auto_approve_batches && !opts.approved && isHeavyStudioStage(stage)) {
        const preflight = await get().runBatchPreflight(stage, opts.model, undefined);
        if (!preflight) {
          return false;
        }
        set({ pendingPreflight: { mode: "stage", stage, opts: { ...opts }, preflight } });
        return false;
      }
      set({
        runningStage: stage,
        actionProgress: null,
        batchProgress: null,
        error: null,
      });
      try {
        const res = await rtClient.request(
          Actions.STUDIO_EPISODE_RUN_STAGE,
          {
            episode_id: epId,
            stage,
            ...opts,
          },
          {
            onProgress: (p) => {
              if (!p.heartbeat) set({ actionProgress: p });
            },
          }
        );
        set({
          episodeDetail: normalizeEpisodeDetail(res.episode),
          runningStage: null,
          actionProgress: null,
          lastSummary: res.summary || stage,
        });
        return true;
      } catch (err) {
        set({
          error: toActionError(err),
          runningStage: null,
          actionProgress: null,
        });
        return false;
      }
    },

    probeShot: async (shot) => {
      if (!shot.video_path) return null;
      try {
        const res = await rtClient.request(Actions.WORKFLOW_RUN_NODE, {
          type: NodeTypes.PROBE_MEDIA,
          input: {
            media: { kind: "video", localUrl: shot.video_path },
          },
        });
        const out = res.output as ProbeMediaOutput;
        set((s) => ({
          probeMap: { ...s.probeMap, [shot.id]: out },
        }));
        return out;
      } catch (err) {
        set({ error: toActionError(err) });
        return null;
      }
    },

    receiveProjectChanged: (evt) => {
      void get().fetchProjects();
      const curPid = get().activeProjectId;
      if (evt.action === ("synced" satisfies StudioProjectChangedAction) && !evt.project_id) {
        if (curPid) {
          void get().selectProject(curPid, { preserveEpisode: true });
        }
        return;
      }
      if (
        evt.project_id &&
        evt.project_id === curPid &&
        evt.action !== ("deleted" satisfies StudioProjectChangedAction)
      ) {
        void get().selectProject(evt.project_id, { preserveEpisode: true });
      }
    },

    receiveEpisodeUpdated: (evt) => {
      const curPid = get().activeProjectId;
      if (evt.project_id && evt.project_id === curPid && evt.episode_id) {
        const known = get().projectDetail?.episodes.some((e) => e.id === evt.episode_id);
        if (!known) {
          void get().selectProject(curPid, { preserveEpisode: true });
        }
      }
      if (evt.episode_id && evt.episode_id === get().activeEpisodeId && !get().runningStage) {
        void get().selectEpisode(evt.episode_id, { preserveStage: true });
      }
    },

    receiveBatchProgress: (evt) => {
      if (evt.episode_id === get().activeEpisodeId) {
        set({
          batchProgress: evt.status === ("running" satisfies StudioBatchStatus) ? evt : null,
        });
      }
    },

    runShotDubMux: async ({
      shot,
      videoPath,
      audioPath,
      text,
      modelRef,
      voice,
      overflowMode,
      duckOriginal,
      originalVolume,
    }) => {
      const vid = (videoPath ?? shot.video_path).trim();
      if (!vid) return false;
      const epId = get().activeEpisodeId ?? shot.episode_id;
      const aud = (audioPath ?? shot.audio_path ?? "").trim();
      const spoken = (text ?? shot.narration_text ?? "").trim();
      const input: TTSDubMuxInput = {
        video_path: { localUrl: vid, kind: "video" },
        ...(aud ? { audio_path: { localUrl: aud, kind: "audio" } } : {}),
        ...(spoken ? { text: spoken } : {}),
        ...(modelRef?.trim() ? { model_ref: modelRef.trim() } : {}),
        ...(voice?.trim() ? { voice: voice.trim() } : {}),
        ...(overflowMode ? { overflow_mode: overflowMode } : {}),
        ...(duckOriginal !== undefined ? { duck_original: duckOriginal } : {}),
        ...(originalVolume !== undefined && Number.isFinite(originalVolume)
          ? { original_volume: originalVolume }
          : {}),
      };
      set({ saving: true, actionProgress: null, error: null });
      try {
        const res = await rtClient.request(
          Actions.WORKFLOW_RUN_NODE,
          { type: NodeTypes.TTS_DUB_MUX, input },
          { onProgress: (p) => !p.heartbeat && set({ actionProgress: p }) }
        );
        const out = res.output as TTSDubMuxOutput;
        const upserted = await rtClient.request(Actions.STUDIO_SHOT_UPSERT, {
          id: shot.id,
          episode_id: epId,
          video_path: out.path,
          ...(out.asset_id ? { video_asset_id: out.asset_id } : {}),
          ...(out.audio_path ? { audio_path: out.audio_path } : {}),
        });
        set((s) => ({
          saving: false,
          actionProgress: null,
          episodeDetail: s.episodeDetail
            ? normalizeEpisodeDetail({ ...s.episodeDetail, shots: upserted.shots ?? [] })
            : s.episodeDetail,
          lastSummary: out.path,
        }));
        return true;
      } catch (err) {
        set({ saving: false, actionProgress: null, error: toActionError(err) });
        return false;
      }
    },

    runShotLipSync: async ({ shot, videoPath, audioPath, modelRef, faceIndex, prompt }) => {
      const vid = (videoPath ?? shot.video_path).trim();
      const aud = (audioPath ?? shot.audio_path).trim();
      if (!vid || !aud) return false;
      const epId = get().activeEpisodeId ?? shot.episode_id;
      const input: LipSyncGenerateInput = {
        video_path: { localUrl: vid, kind: "video" },
        audio_path: { localUrl: aud, kind: "audio" },
        ...(modelRef?.trim() ? { model_ref: modelRef.trim() } : {}),
        ...(faceIndex !== undefined && Number.isFinite(faceIndex) ? { face_index: faceIndex } : {}),
        ...(prompt?.trim() ? { prompt: prompt.trim() } : {}),
      };
      set({ saving: true, actionProgress: null, error: null });
      try {
        const res = await rtClient.request(
          Actions.WORKFLOW_RUN_NODE,
          { type: NodeTypes.LIPSYNC_GENERATE, input },
          { onProgress: (p) => !p.heartbeat && set({ actionProgress: p }) }
        );
        const out = res.output as LipSyncGenerateOutput;
        const upserted = await rtClient.request(Actions.STUDIO_SHOT_UPSERT, {
          id: shot.id,
          episode_id: epId,
          video_path: out.path,
          ...(out.asset_id ? { video_asset_id: out.asset_id } : {}),
        });
        set((s) => ({
          saving: false,
          actionProgress: null,
          episodeDetail: s.episodeDetail
            ? normalizeEpisodeDetail({ ...s.episodeDetail, shots: upserted.shots ?? [] })
            : s.episodeDetail,
          lastSummary: out.path,
        }));
        return true;
      } catch (err) {
        set({ saving: false, actionProgress: null, error: toActionError(err) });
        return false;
      }
    },
  };
}
