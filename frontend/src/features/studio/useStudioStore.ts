import { create } from "zustand";
import { createProjectSlice } from "./projectSlice";
import { createQueueSlice } from "./queueSlice";
import { createStageSlice } from "./stageSlice";
import { targetVersionKey, type StudioState, type StudioVersionModalTarget, type StudioGridModalTarget, type StudioMaskModalTarget, type StudioPendingPreflight } from "./storeTypes";
import { createTimelineSlice } from "./timelineSlice";
import { createVersionsSlice } from "./versionsSlice";

export {
  targetVersionKey,
  type StudioState,
  type StudioVersionModalTarget,
  type StudioGridModalTarget,
  type StudioMaskModalTarget,
  type StudioPendingPreflight,
};

export const useStudioStore = create<StudioState>((set, get) => ({
  projects: [],
  projectsDir: "",
  projectDetail: null,
  episodeDetail: null,
  activeProjectId: null,
  activeEpisodeId: null,
  activeStage: "script",
  selectedAssetIds: [],
  loadingProjects: false,
  loadingEpisode: false,
  needsInitialConnectSync: false,
  saving: false,
  runningStage: null,
  actionProgress: null,
  batchProgress: null,
  probeMap: {},
  jobs: [],
  costSummary: null,
  versionsByTarget: {},
  qcByTarget: {},
  queueDrawerOpen: false,
  versionDrawerTarget: null,
  gridModalTarget: null,
  maskModalTarget: null,
  pendingPreflight: null,
  timelineResult: null,
  timelineLoading: false,
  renderingTimeline: false,
  exportingNLE: false,
  lastRenderResult: null,
  lastNLEExport: null,
  peaksLoadingClipIds: {},
  error: null,
  lastSummary: "",

  ...createProjectSlice(set, get),
  ...createStageSlice(set, get),
  ...createQueueSlice(set, get),
  ...createVersionsSlice(set, get),
  ...createTimelineSlice(set, get),
}));
