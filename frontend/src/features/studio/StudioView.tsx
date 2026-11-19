import React, { useEffect, useState } from "react";
import {
  CheckCircle2,
  Clapperboard,
  FolderOpen,
  Layers,
  Play,
  Plus,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { ErrorText } from "../../components/ui/ErrorText";
import { LoadingSkeleton } from "../../components/ui/LoadingSkeleton";
import { appConfig } from "../../config/appConfig";
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
import { isDesktop } from "../../lib/bridge";
import { translateServerMessage, useT } from "../../lib/i18n";
import { useAgentScreenContext } from "../agent/screenContext";
import { useAgentStore } from "../agent/useAgentStore";
import { MediaModelSelect } from "../mediagen/MediaModelSelect";
import { useMediaLibraryStore } from "../media/useMediaLibraryStore";
import { useSystemStore } from "../system/useSystemStore";
import {
  StudioGridSplitterModal,
  StudioMaskEditorModal,
  StudioPreflightDialog,
  StudioVersionDrawer,
} from "./StudioPhase2Modals";
import { StudioQueueCostDrawer } from "./StudioQueueCostDrawer";
import { formatUSD } from "./studioFormatters";
import {
  AssetsStagePanel,
  CreateProjectDialog,
  DeleteProjectDialog,
  ScriptStagePanel,
  StoryboardStagePanel,
  VideoShotsStagePanel,
} from "./StudioStages";
import { TimelineWorkspace } from "./timeline/TimelineWorkspace";
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
      return NodeTypes.RENDER_TIMELINE;
  }
}

function stageModelPurpose(stage: StudioEpisodeStage): ProviderPurpose | null {
  switch (stage) {
    case "script":
      return "text";
    case "assets":
    case "storyboard":
      return "image";
    case "video":
      return "video";
    case "export":
      return null;
  }
}

export const StudioView: React.FC = () => {
  const t = useT();
  const projects = useStudioStore((s) => s.projects);
  const projectDetail = useStudioStore((s) => s.projectDetail);
  const episodeDetail = useStudioStore((s) => s.episodeDetail);
  const activeProjectId = useStudioStore((s) => s.activeProjectId);
  const activeEpisodeId = useStudioStore((s) => s.activeEpisodeId);
  const activeStage = useStudioStore((s) => s.activeStage);
  const selectedAssetIds = useStudioStore((s) => s.selectedAssetIds);
  const loadingProjects = useStudioStore((s) => s.loadingProjects);
  const loadingEpisode = useStudioStore((s) => s.loadingEpisode);
  const saving = useStudioStore((s) => s.saving);
  const runningStage = useStudioStore((s) => s.runningStage);
  const actionProgress = useStudioStore((s) => s.actionProgress);
  const batchProgress = useStudioStore((s) => s.batchProgress);
  const error = useStudioStore((s) => s.error);
  const lastSummary = useStudioStore((s) => s.lastSummary);
  const setActiveStage = useStudioStore((s) => s.setActiveStage);
  const jobs = useStudioStore((s) => s.jobs);
  const costSummary = useStudioStore((s) => s.costSummary);
  const openQueueDrawer = useStudioStore((s) => s.openQueueDrawer);
  const fetchProjects = useStudioStore((s) => s.fetchProjects);
  const selectProject = useStudioStore((s) => s.selectProject);
  const updateProject = useStudioStore((s) => s.updateProject);
  const selectEpisode = useStudioStore((s) => s.selectEpisode);
  const createEpisode = useStudioStore((s) => s.createEpisode);
  const clearError = useStudioStore((s) => s.clearError);

  const approvals = useAgentStore((s) => s.approvals);
  const resolveApproval = useAgentStore((s) => s.resolveApproval);
  const studioApprovals = (approvals ?? []).filter((a) => a.tool.startsWith("studio_"));
  const activeJobsCount = (jobs ?? []).filter(
    (j) => j.status === "queued" || j.status === "submitted" || j.status === "polling"
  ).length;

  const openMediaDrawer = useMediaLibraryStore((s) => s.openDrawer);
  const openFolder = useSystemStore((s) => s.openFolder);
  const sysMode = useSystemStore((s) => s.info?.mode);
  const canOpenFolder = isDesktop() && sysMode !== "server" && sysMode !== "client";

  const [showCreateDialog, setShowCreateDialog] = useState(false);
  const [showDeleteDialog, setShowDeleteDialog] = useState(false);

  useEffect(() => {
    void fetchProjects();
  }, [fetchProjects]);

  const activeProj = episodeDetail?.project ?? projectDetail?.project ?? null;
  const activeEp = episodeDetail?.episode ?? null;
  const textModel = activeProj?.default_text_model ?? "";
  const imageModel = activeProj?.default_image_model ?? "";
  const videoModel = activeProj?.default_video_model ?? "";
  const ttsModel = activeProj?.default_tts_model ?? "";

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
    if (!activeProj || !modelPurpose) return;
    if (modelPurpose === "text") {
      void updateProject({ id: activeProj.id, default_text_model: val });
    } else if (modelPurpose === "image") {
      void updateProject({ id: activeProj.id, default_image_model: val });
    } else if (modelPurpose === "video") {
      void updateProject({ id: activeProj.id, default_video_model: val });
    } else {
      void updateProject({ id: activeProj.id, default_tts_model: val });
    }
  };

  const progressPct =
    batchProgress?.percent ??
    actionProgress?.percent ??
    (runningStage ? appConfig.studio.ui.initialStageProgressPct : 0);

  const translatedActionMsg = actionProgress
    ? (translateServerMessage(t, actionProgress.msg_key, actionProgress.msg_params) ??
      actionProgress.message)
    : "";

  const progressLabel = batchProgress
    ? (translateServerMessage(t, batchProgress.msg_key, batchProgress.msg_params) ??
      t("studio.common.batchProgress", {
        stage: batchProgress.stage,
        current: batchProgress.current,
        total: batchProgress.total,
        message: batchProgress.message,
      }))
    : translatedActionMsg || t("studio.common.running");

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
                      t("studio.sidebar.episodeDefaultTitle", {
                        num: projectDetail.episodes.length + 1,
                      })
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
                {(projectDetail.episodes ?? []).map((ep) => (
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
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => void openFolder("workspace_root")}
                  disabled={!canOpenFolder}
                  title={!canOpenFolder ? t("system.desktopOnly") : undefined}
                >
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
                  <Badge>{t(`studio.modal.genre.${activeProj.genre}`)}</Badge>
                  <Badge>{t(`studio.modal.mode.${activeProj.production_mode}`)}</Badge>
                  <Badge>{activeProj.aspect_ratio}</Badge>
                </>
              )}
            </div>
            <span>{activeEp ? `${activeEp.code} — ${activeEp.title}` : t("studio.subtitle")}</span>
          </div>

          <div className="studio-topbar-actions">
            {activeProj && modelPurpose && (
              <MediaModelSelect
                purpose={modelPurpose}
                value={currentModelVal}
                onChange={setCurrentModelVal}
                disabled={runningStage !== null}
              />
            )}
            {activeProj && (
              <>
                <Button
                  variant={studioApprovals.length > 0 ? "primary" : "secondary"}
                  size="sm"
                  onClick={() => openQueueDrawer(true)}
                >
                  {studioApprovals.length > 0 ? <ShieldAlert size={13} /> : <Play size={13} />}
                  {t("studio.queue.button")}
                  {activeJobsCount > 0 ? ` (${activeJobsCount})` : ""}
                </Button>
                <Badge
                  className={
                    activeProj.auto_approve_batches ? "studio-badge--ok" : "studio-badge--warn"
                  }
                >
                  {activeProj.auto_approve_batches ? (
                    <ShieldCheck size={11} />
                  ) : (
                    <ShieldAlert size={11} />
                  )}
                  {formatUSD(
                    costSummary?.actual_total_usd ?? 0,
                    appConfig.studio.ui.summaryUsdDigits,
                    costSummary?.currency
                  )}
                </Badge>
              </>
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

        {error && <ErrorText error={error} onDismiss={clearError} />}

        {studioApprovals.length > 0 && (
          <div className="studio-approval-bar studio-approval-bar--inline">
            <div>
              <strong>{t("studio.approval.pendingTitle")}</strong>
              <div className="studio-field-hint">{studioApprovals[0].summary}</div>
            </div>
            <div className="studio-phase2-meta-grid">
              <Button
                size="sm"
                variant="primary"
                onClick={() => void resolveApproval(studioApprovals[0].id, true)}
              >
                <ShieldCheck size={13} />
                {t("studio.approval.approve")}
              </Button>
              <Button
                size="sm"
                variant="secondary"
                onClick={() => void resolveApproval(studioApprovals[0].id, false)}
              >
                {t("studio.approval.reject")}
              </Button>
            </div>
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
                style={{
                  width: `${Math.max(appConfig.studio.ui.minProgressFillPct, Math.min(100, progressPct))}%`,
                }}
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
              key={episodeDetail.episode.id}
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
            <TimelineWorkspace key={episodeDetail.episode.id} detail={episodeDetail} />
          )}
        </div>
      </section>

      {showCreateDialog && <CreateProjectDialog onClose={() => setShowCreateDialog(false)} />}
      {showDeleteDialog && activeProj && (
        <DeleteProjectDialog project={activeProj} onClose={() => setShowDeleteDialog(false)} />
      )}
      <StudioQueueCostDrawer />
      <StudioVersionDrawer />
      <StudioGridSplitterModal />
      <StudioMaskEditorModal />
      <StudioPreflightDialog />
    </div>
  );
};

export default StudioView;
