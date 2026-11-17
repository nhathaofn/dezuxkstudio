import React, { useEffect, useState } from "react";
import {
  CheckCircle2,
  Clapperboard,
  FolderOpen,
  Layers,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { ErrorText } from "../../components/ui/ErrorText";
import { LoadingSkeleton } from "../../components/ui/LoadingSkeleton";
import {
  NodeTypes,
  StudioEpisodeStageValues,
  StudioGenreValues,
  StudioProductionModeValues,
  type ProviderPurpose,
  type StudioEpisodeStage,
  type StudioGenre,
  type StudioProductionMode,
} from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { useAgentScreenContext } from "../agent/screenContext";
import { MediaModelSelect } from "../mediagen/MediaModelSelect";
import { useMediaLibraryStore } from "../media/useMediaLibraryStore";
import { useSystemStore } from "../system/useSystemStore";
import {
  AssetsStagePanel,
  CreateProjectDialog,
  DeleteProjectDialog,
  ExportStagePanel,
  ScriptStagePanel,
  StoryboardStagePanel,
  VideoShotsStagePanel,
} from "./StudioStages";
import { useStudioStore } from "./useStudioStore";
import "./StudioView.css";

function stageNodeType(stage: StudioEpisodeStage): string {
  switch (stage) {
    case "script":
      return NodeTypes.SCRIPT_NORMALIZE;
    case "assets":
      return NodeTypes.EXTRACT_ASSETS;
    case "storyboard":
      return NodeTypes.AUTHOR_SHOT_PROMPTS;
    case "video":
      return NodeTypes.VIDEO_GENERATE;
    case "export":
      return NodeTypes.VIDEO_CONCAT_BASIC;
  }
}

function stageModelPurpose(stage: StudioEpisodeStage): ProviderPurpose {
  switch (stage) {
    case "script":
      return "text";
    case "assets":
    case "storyboard":
      return "image";
    case "video":
      return "video";
    case "export":
      return "tts";
  }
}

export const StudioView: React.FC = () => {
  const t = useT();
  const {
    projects,
    projectDetail,
    episodeDetail,
    activeProjectId,
    activeEpisodeId,
    activeStage,
    selectedAssetIds,
    loadingProjects,
    loadingEpisode,
    saving,
    runningStage,
    actionProgress,
    batchProgress,
    error,
    lastSummary,
    setActiveStage,
    fetchProjects,
    selectProject,
    updateProject,
    selectEpisode,
    createEpisode,
    clearError,
  } = useStudioStore();

  const openMediaDrawer = useMediaLibraryStore((s) => s.openDrawer);
  const openFolder = useSystemStore((s) => s.openFolder);

  const [showCreateDialog, setShowCreateDialog] = useState(false);
  const [showDeleteDialog, setShowDeleteDialog] = useState(false);
  const [textModel, setTextModel] = useState("");
  const [imageModel, setImageModel] = useState("");
  const [videoModel, setVideoModel] = useState("");
  const [ttsModel, setTtsModel] = useState("");

  useEffect(() => {
    void fetchProjects();
  }, [fetchProjects]);

  const activeProj = episodeDetail?.project ?? projectDetail?.project ?? null;
  const activeEp = episodeDetail?.episode ?? null;

  const selectedFiles: string[] = [];
  if (episodeDetail) {
    if (episodeDetail.final_video_path) selectedFiles.push(episodeDetail.final_video_path);
    for (const a of episodeDetail.assets) {
      if (selectedAssetIds.includes(a.id) && a.primary_path) {
        selectedFiles.push(a.primary_path);
      }
    }
    if (selectedFiles.length === 0 && activeEp?.script_json_path) {
      selectedFiles.push(activeEp.script_json_path);
    }
  }

  useAgentScreenContext({
    feature: stageNodeType(activeStage),
    selected_files: selectedFiles,
    settings: {
      ...(activeProj
        ? {
            project_id: activeProj.id,
            genre: activeProj.genre,
            production_mode: activeProj.production_mode,
          }
        : {}),
      ...(activeEp ? { episode_id: activeEp.id } : {}),
      stage: activeStage,
      ...(selectedAssetIds.length > 0 ? { selected_asset_ids: selectedAssetIds.join(",") } : {}),
    },
    last_result: runningStage ? `running: ${runningStage}` : lastSummary,
  });

  const modelPurpose = stageModelPurpose(activeStage);
  const currentModelVal =
    modelPurpose === "text"
      ? textModel
      : modelPurpose === "image"
        ? imageModel
        : modelPurpose === "video"
          ? videoModel
          : ttsModel;

  const setCurrentModelVal = (val: string) => {
    if (modelPurpose === "text") setTextModel(val);
    else if (modelPurpose === "image") setImageModel(val);
    else if (modelPurpose === "video") setVideoModel(val);
    else setTtsModel(val);
  };

  const progressPct = batchProgress?.percent ?? actionProgress?.percent ?? (runningStage ? 15 : 0);

  const progressLabel = batchProgress
    ? t("studio.common.batchProgress", {
        stage: batchProgress.stage,
        current: batchProgress.current,
        total: batchProgress.total,
        message: batchProgress.message,
      })
    : actionProgress?.message || t("studio.common.running");

  return (
    <div className="studio-layout">
      <aside className="studio-sidebar">
        <div className="studio-sidebar-section">
          <div className="studio-sidebar-header">
            <span className="studio-sidebar-title">{t("studio.sidebar.projects")}</span>
            <Button variant="primary" size="sm" onClick={() => setShowCreateDialog(true)}>
              <Plus size={13} />
              {t("studio.sidebar.newProject")}
            </Button>
          </div>

          <select
            className="input-control"
            value={activeProjectId ?? ""}
            onChange={(e) => e.target.value && void selectProject(e.target.value)}
            disabled={loadingProjects || projects.length === 0}
            aria-label={t("studio.sidebar.projects")}
          >
            {projects.length === 0 ? (
              <option value="">{t("studio.sidebar.noProjects")}</option>
            ) : (
              projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.title} ({p.aspect_ratio})
                </option>
              ))
            )}
          </select>
        </div>

        {activeProj && projectDetail && (
          <>
            <div className="studio-sidebar-section">
              <div className="studio-sidebar-header">
                <span className="studio-sidebar-title">{t("studio.sidebar.episodes")}</span>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    void createEpisode(
                      activeProj.id,
                      `Episode ${projectDetail.episodes.length + 1}`
                    )
                  }
                  disabled={saving}
                >
                  <Plus size={12} />
                  {t("studio.sidebar.newEpisode")}
                </Button>
              </div>
              <select
                className="input-control"
                value={activeEpisodeId ?? ""}
                onChange={(e) => e.target.value && void selectEpisode(e.target.value)}
                disabled={loadingEpisode}
                aria-label={t("studio.sidebar.episodes")}
              >
                {projectDetail.episodes.map((ep) => (
                  <option key={ep.id} value={ep.id}>
                    {ep.code} — {ep.title}
                  </option>
                ))}
              </select>
            </div>

            <div className="studio-sidebar-section">
              <span className="studio-sidebar-title">{t("studio.sidebar.stages")}</span>
              <nav className="studio-stepper" aria-label={t("studio.sidebar.stages")}>
                {StudioEpisodeStageValues.map((stage) => (
                  <button
                    key={stage}
                    type="button"
                    className={`studio-step-btn ${activeStage === stage ? "studio-step-btn--active" : ""}`}
                    onClick={() => setActiveStage(stage)}
                  >
                    <span>{t(`studio.stage.${stage}`)}</span>
                    {activeEp?.stage === stage && <CheckCircle2 size={14} />}
                  </button>
                ))}
              </nav>
            </div>

            <div className="studio-sidebar-section">
              <span className="studio-sidebar-title">{t("studio.sidebar.settings")}</span>

              <div className="form-group">
                <label className="field-label" htmlFor="studio-proj-genre">
                  {t("studio.modal.genre")}
                </label>
                <select
                  id="studio-proj-genre"
                  className="input-control"
                  value={activeProj.genre}
                  onChange={(e) =>
                    void updateProject({
                      id: activeProj.id,
                      genre: e.target.value as StudioGenre,
                    })
                  }
                  disabled={saving}
                >
                  {StudioGenreValues.map((g) => (
                    <option key={g} value={g}>
                      {t(`studio.modal.genre.${g}`)}
                    </option>
                  ))}
                </select>
              </div>

              <div className="form-group">
                <label className="field-label" htmlFor="studio-proj-mode">
                  {t("studio.modal.mode")}
                </label>
                <select
                  id="studio-proj-mode"
                  className="input-control"
                  value={activeProj.production_mode}
                  onChange={(e) =>
                    void updateProject({
                      id: activeProj.id,
                      production_mode: e.target.value as StudioProductionMode,
                    })
                  }
                  disabled={saving}
                >
                  {StudioProductionModeValues.map((m) => (
                    <option key={m} value={m}>
                      {t(`studio.modal.mode.${m}`)}
                    </option>
                  ))}
                </select>
              </div>

              <div className="studio-toolbar">
                <Button variant="ghost" size="sm" onClick={() => void openFolder("workspace_root")}>
                  <FolderOpen size={13} />
                  {t("studio.sidebar.openWorkspace")}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setShowDeleteDialog(true)}
                  disabled={saving || runningStage !== null}
                >
                  <Trash2 size={13} />
                  {t("studio.sidebar.deleteProject")}
                </Button>
              </div>
            </div>
          </>
        )}
      </aside>

      <section className="studio-main">
        <div className="studio-topbar">
          <div className="studio-topbar-info">
            <div className="studio-topbar-title">
              <Clapperboard size={18} />
              <span>{activeProj ? activeProj.title : t("studio.title")}</span>
              {activeProj && (
                <>
                  <Badge>{activeProj.genre}</Badge>
                  <Badge>{activeProj.production_mode}</Badge>
                  <Badge>{activeProj.aspect_ratio}</Badge>
                </>
              )}
            </div>
            <span>{activeEp ? `${activeEp.code} — ${activeEp.title}` : t("studio.subtitle")}</span>
          </div>

          <div className="studio-topbar-actions">
            {activeProj && (
              <MediaModelSelect
                purpose={modelPurpose}
                value={currentModelVal}
                onChange={setCurrentModelVal}
                disabled={runningStage !== null}
              />
            )}
            <Button variant="secondary" size="sm" onClick={openMediaDrawer}>
              <Layers size={13} />
              {t("studio.common.openLibrary")}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                activeEpisodeId ? void selectEpisode(activeEpisodeId) : void fetchProjects()
              }
              disabled={loadingEpisode || loadingProjects}
            >
              <RefreshCw size={13} />
              {t("studio.common.refresh")}
            </Button>
          </div>
        </div>

        {error && (
          <div onClick={clearError} role="presentation">
            <ErrorText error={error} />
          </div>
        )}

        {(runningStage !== null || batchProgress !== null) && (
          <div className="studio-progress-banner">
            <div className="studio-item-head">
              <span>{progressLabel}</span>
              <span>{Math.round(progressPct)}%</span>
            </div>
            <div className="studio-progress-track">
              <div
                className="studio-progress-fill"
                style={{ width: `${Math.max(4, Math.min(100, progressPct))}%` }}
              />
            </div>
          </div>
        )}

        <div className="studio-stage-scroll">
          {loadingProjects && !episodeDetail ? (
            <LoadingSkeleton />
          ) : !episodeDetail ? (
            <div className="studio-empty-state">
              <Clapperboard size={36} />
              <h2>{t("studio.empty.title")}</h2>
              <p>{t("studio.empty.desc")}</p>
              <Button variant="primary" onClick={() => setShowCreateDialog(true)}>
                <Plus size={15} />
                {t("studio.sidebar.newProject")}
              </Button>
            </div>
          ) : loadingEpisode ? (
            <LoadingSkeleton />
          ) : activeStage === "script" ? (
            <ScriptStagePanel
              key={`${episodeDetail.episode.id}:${episodeDetail.raw_script ?? ""}`}
              detail={episodeDetail}
              modelOverride={textModel}
            />
          ) : activeStage === "assets" ? (
            <AssetsStagePanel
              detail={episodeDetail}
              textModel={textModel}
              imageModel={imageModel}
            />
          ) : activeStage === "storyboard" ? (
            <StoryboardStagePanel
              detail={episodeDetail}
              textModel={textModel}
              imageModel={imageModel}
            />
          ) : activeStage === "video" ? (
            <VideoShotsStagePanel
              detail={episodeDetail}
              videoModel={videoModel}
              ttsModel={ttsModel}
            />
          ) : (
            <ExportStagePanel detail={episodeDetail} />
          )}
        </div>
      </section>

      {showCreateDialog && <CreateProjectDialog onClose={() => setShowCreateDialog(false)} />}
      {showDeleteDialog && activeProj && (
        <DeleteProjectDialog project={activeProj} onClose={() => setShowDeleteDialog(false)} />
      )}
    </div>
  );
};

export default StudioView;
