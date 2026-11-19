import { create } from "zustand";
import { Actions, NodeTypes, type ActionProgress, type NodeMap } from "../../contracts/generated";
import {
  mediaBasename,
  rtClient,
  saveMediaToDevice,
  toActionError,
  uploadMediaFile,
  type ActionError,
} from "../../lib/rtClient";

export const WATERMARK_NODE = NodeTypes.WATERMARK_REMOVER;
export type WatermarkInput = NodeMap[typeof WATERMARK_NODE]["input"];
export type WatermarkNodeResult = NodeMap[typeof WATERMARK_NODE]["output"];
export type WatermarkItemResult = NonNullable<WatermarkNodeResult["results"]>[number];

export const VIDEO_WATERMARK_NODE = NodeTypes.VIDEO_WATERMARK_REMOVE;
export type VideoWatermarkInput = NodeMap[typeof VIDEO_WATERMARK_NODE]["input"];
export type VideoWatermarkNodeResult = NodeMap[typeof VIDEO_WATERMARK_NODE]["output"];

export type WatermarkMediaKind = "image" | "video";

function safeRevokeObjectURL(url: string | null | undefined): void {
  if (!url) return;
  if (typeof URL !== "undefined" && typeof URL.revokeObjectURL === "function") {
    try {
      URL.revokeObjectURL(url);
    } catch {
      // Ignored: environments without full URL mock
    }
  }
}

interface WatermarkState {
  mediaKind: WatermarkMediaKind;
  inputPath: string;
  inputPaths: string[];
  /** Browser/client File objects staged via HTTP upload when processed. */
  queuedFiles: File[];
  /** Parallel objectUrls keys aligned with queuedFiles indices to handle duplicate names safely. */
  queuedObjectKeys: string[];
  /** Video watermark input path & optional browser queued file. */
  videoInputPath: string;
  queuedVideoFile: File | null;
  videoMode: string | null;
  videoX: number | null;
  videoY: number | null;
  videoWidth: number | null;
  videoHeight: number | null;
  videoCropEdge: string | null;
  videoCropPx: number | null;
  videoResult: VideoWatermarkNodeResult | null;
  /** Maps file identifiers to ephemeral ObjectURLs for fast local preview. */
  objectUrls: Record<string, string>;
  /** Maps uploaded server staging paths back to original client filenames for display. */
  remoteNameMap: Record<string, string>;
  inPlace: boolean;
  outputDir: string;
  copyClean: boolean;
  preserveTree: boolean;
  /** null = the node's default (from workflow:specs). */
  workers: number | null;
  /** null = the node's default (from workflow:specs). */
  corner: string | null;
  loading: boolean;
  downloadingPath: string | null;
  /** Live per-image progress of the running batch (null when idle). */
  progress: ActionProgress | null;
  result: WatermarkNodeResult | null;
  error: ActionError | null;

  comparisonItem: { beforeUrl: string; afterUrl: string; title: string } | null;
  comparisonZoom: number;
  comparisonPan: { x: number; y: number };
  comparisonSliderPos: number;

  setInputPath: (path: string) => void;
  setInputPaths: (paths: string[]) => void;
  setQueuedFiles: (files: File[]) => void;
  removeQueuedFile: (target: number | string) => void;
  createTrackedObjectURL: (key: string, blob: Blob | File) => string;
  revokeTrackedObjectURL: (keyOrUrl: string) => void;
  revokeAllObjectURLs: () => void;
  setInPlace: (inPlace: boolean) => void;
  setOutputDir: (dir: string) => void;
  setCopyClean: (copyClean: boolean) => void;
  setPreserveTree: (preserveTree: boolean) => void;
  setWorkers: (workers: number) => void;
  setCorner: (corner: string) => void;
  setMediaKind: (kind: WatermarkMediaKind) => void;
  setVideoInputPath: (path: string) => void;
  setQueuedVideoFile: (file: File | null) => void;
  setVideoMode: (mode: string) => void;
  setVideoX: (x: number | null) => void;
  setVideoY: (y: number | null) => void;
  setVideoWidth: (w: number | null) => void;
  setVideoHeight: (h: number | null) => void;
  setVideoCropEdge: (edge: string) => void;
  setVideoCropPx: (px: number | null) => void;
  removeVideoWatermark: () => Promise<void>;
  setComparisonItem: (item: { beforeUrl: string; afterUrl: string; title: string } | null) => void;
  setComparisonZoom: (zoom: number | ((prev: number) => number)) => void;
  setComparisonPan: (
    pan: { x: number; y: number } | ((prev: { x: number; y: number }) => { x: number; y: number })
  ) => void;
  setComparisonSliderPos: (pos: number) => void;
  resetComparison: () => void;
  dismissError: () => void;
  removeWatermark: (pathsOverride?: string[] | string) => Promise<void>;
  downloadOutput: (path: string, preferredName?: string) => Promise<void>;
  reset: () => void;
}

export const useWatermarkStore = create<WatermarkState>((set, get) => ({
  mediaKind: "image",
  inputPath: "",
  inputPaths: [],
  queuedFiles: [],
  queuedObjectKeys: [],
  videoInputPath: "",
  queuedVideoFile: null,
  videoMode: null,
  videoX: null,
  videoY: null,
  videoWidth: null,
  videoHeight: null,
  videoCropEdge: null,
  videoCropPx: null,
  videoResult: null,
  objectUrls: {},
  remoteNameMap: {},
  inPlace: false,
  outputDir: "",
  copyClean: true,
  preserveTree: false,
  workers: null,
  corner: null,
  loading: false,
  downloadingPath: null,
  progress: null,
  result: null,
  error: null,
  comparisonItem: null,
  comparisonZoom: 1.0,
  comparisonPan: { x: 0, y: 0 },
  comparisonSliderPos: 50,

  createTrackedObjectURL: (key: string, blob: Blob | File) => {
    const existing = get().objectUrls[key];
    if (existing) {
      safeRevokeObjectURL(existing);
    }
    let url = "";
    if (typeof URL !== "undefined" && typeof URL.createObjectURL === "function") {
      try {
        url = URL.createObjectURL(blob);
      } catch {
        url = "";
      }
    }
    set((s) => ({
      objectUrls: { ...s.objectUrls, [key]: url },
    }));
    return url;
  },

  revokeTrackedObjectURL: (keyOrUrl: string) => {
    const { objectUrls } = get();
    let targetKey: string | null = null;
    let targetUrl: string | null = null;

    if (objectUrls[keyOrUrl]) {
      targetKey = keyOrUrl;
      targetUrl = objectUrls[keyOrUrl];
    } else {
      for (const [k, u] of Object.entries(objectUrls)) {
        if (u === keyOrUrl) {
          targetKey = k;
          targetUrl = u;
          break;
        }
      }
    }

    if (targetUrl) {
      safeRevokeObjectURL(targetUrl);
    } else if (keyOrUrl.startsWith("blob:")) {
      safeRevokeObjectURL(keyOrUrl);
    }

    if (targetKey) {
      set((s) => {
        const next = { ...s.objectUrls };
        delete next[targetKey!];
        return { objectUrls: next };
      });
    }
  },

  revokeAllObjectURLs: () => {
    const { objectUrls } = get();
    for (const url of Object.values(objectUrls)) {
      safeRevokeObjectURL(url);
    }
    set({ objectUrls: {} });
  },

  setInputPath: (inputPath) => {
    get().revokeAllObjectURLs();
    set({
      inputPath,
      inputPaths: inputPath ? [inputPath] : [],
      queuedFiles: [],
      queuedObjectKeys: [],
      objectUrls: {},
      remoteNameMap: {},
      error: null,
      result: null,
    });
  },

  setInputPaths: (inputPaths) => {
    get().revokeAllObjectURLs();
    set({
      inputPaths,
      inputPath: inputPaths[0] || "",
      queuedFiles: [],
      queuedObjectKeys: [],
      objectUrls: {},
      remoteNameMap: {},
      error: null,
      result: null,
    });
  },

  setQueuedFiles: (queuedFiles) => {
    get().revokeAllObjectURLs();
    const newObjectUrls: Record<string, string> = {};
    const queuedObjectKeys: string[] = [];
    for (let idx = 0; idx < queuedFiles.length; idx++) {
      const file = queuedFiles[idx];
      const key = !Object.prototype.hasOwnProperty.call(newObjectUrls, file.name)
        ? file.name
        : `${idx}:${file.name}`;
      queuedObjectKeys.push(key);
      if (typeof URL !== "undefined" && typeof URL.createObjectURL === "function") {
        try {
          newObjectUrls[key] = URL.createObjectURL(file);
        } catch {
          // Ignored in test/node environments
        }
      }
    }
    set({
      queuedFiles,
      queuedObjectKeys,
      inputPaths: queuedFiles.map((f) => f.name),
      inputPath: queuedFiles[0]?.name || "",
      objectUrls: newObjectUrls,
      remoteNameMap: {},
      error: null,
      result: null,
    });
  },

  removeQueuedFile: (target: number | string) => {
    const { queuedFiles, queuedObjectKeys, objectUrls } = get();
    const removeIndices = new Set<number>();
    if (typeof target === "number") {
      if (target >= 0 && target < queuedFiles.length) {
        removeIndices.add(target);
      }
    } else {
      queuedFiles.forEach((f, idx) => {
        if (f.name === target) removeIndices.add(idx);
      });
    }
    if (removeIndices.size === 0) return;

    const nextUrls = { ...objectUrls };
    const nextFiles: File[] = [];
    const nextKeys: string[] = [];
    for (let idx = 0; idx < queuedFiles.length; idx++) {
      const key = queuedObjectKeys[idx] ?? queuedFiles[idx].name;
      if (removeIndices.has(idx)) {
        const url = nextUrls[key];
        if (url) safeRevokeObjectURL(url);
        delete nextUrls[key];
      } else {
        nextFiles.push(queuedFiles[idx]);
        nextKeys.push(key);
      }
    }
    if (typeof target === "string" && nextUrls[target]) {
      safeRevokeObjectURL(nextUrls[target]);
      delete nextUrls[target];
    }
    set({
      queuedFiles: nextFiles,
      queuedObjectKeys: nextKeys,
      inputPaths: nextFiles.map((f) => f.name),
      inputPath: nextFiles[0]?.name || "",
      objectUrls: nextUrls,
    });
  },

  setInPlace: (inPlace) => set({ inPlace }),
  setOutputDir: (outputDir) => set({ outputDir }),
  setCopyClean: (copyClean) => set({ copyClean }),
  setPreserveTree: (preserveTree) => set({ preserveTree }),
  setWorkers: (workers) => set({ workers }),
  setCorner: (corner) => set({ corner }),
  setMediaKind: (mediaKind) => set({ mediaKind, error: null }),
  setVideoInputPath: (videoInputPath) => {
    get().revokeTrackedObjectURL("video:queued");
    set({ videoInputPath, queuedVideoFile: null, videoResult: null, error: null });
  },
  setQueuedVideoFile: (queuedVideoFile) => {
    if (queuedVideoFile) {
      get().createTrackedObjectURL("video:queued", queuedVideoFile);
      set({
        queuedVideoFile,
        videoInputPath: queuedVideoFile.name,
        videoResult: null,
        error: null,
      });
    } else {
      get().revokeTrackedObjectURL("video:queued");
      set({ queuedVideoFile: null, videoInputPath: "", videoResult: null, error: null });
    }
  },
  setVideoMode: (videoMode) => set({ videoMode }),
  setVideoX: (videoX) => set({ videoX }),
  setVideoY: (videoY) => set({ videoY }),
  setVideoWidth: (videoWidth) => set({ videoWidth }),
  setVideoHeight: (videoHeight) => set({ videoHeight }),
  setVideoCropEdge: (videoCropEdge) => set({ videoCropEdge }),
  setVideoCropPx: (videoCropPx) => set({ videoCropPx }),
  setComparisonItem: (comparisonItem) => {
    const prev = get().comparisonItem;
    if (prev) {
      if (prev.beforeUrl?.startsWith("blob:") && prev.beforeUrl !== comparisonItem?.beforeUrl) {
        safeRevokeObjectURL(prev.beforeUrl);
      }
      if (prev.afterUrl?.startsWith("blob:") && prev.afterUrl !== comparisonItem?.afterUrl) {
        safeRevokeObjectURL(prev.afterUrl);
      }
    }
    set({
      comparisonItem,
      comparisonZoom: 1.0,
      comparisonPan: { x: 0, y: 0 },
      comparisonSliderPos: 50,
    });
  },
  setComparisonZoom: (zoom) =>
    set((s) => ({
      comparisonZoom: typeof zoom === "function" ? zoom(s.comparisonZoom) : zoom,
    })),
  setComparisonPan: (pan) =>
    set((s) => ({
      comparisonPan: typeof pan === "function" ? pan(s.comparisonPan) : pan,
    })),
  setComparisonSliderPos: (comparisonSliderPos) => set({ comparisonSliderPos }),
  resetComparison: () => {
    const prev = get().comparisonItem;
    if (prev) {
      if (prev.beforeUrl?.startsWith("blob:")) safeRevokeObjectURL(prev.beforeUrl);
      if (prev.afterUrl?.startsWith("blob:")) safeRevokeObjectURL(prev.afterUrl);
    }
    set({
      comparisonItem: null,
      comparisonZoom: 1.0,
      comparisonPan: { x: 0, y: 0 },
      comparisonSliderPos: 50,
    });
  },
  dismissError: () => set({ error: null }),

  removeWatermark: async (pathsOverride?: string[] | string) => {
    const queued = !pathsOverride ? get().queuedFiles : [];
    const uploadingQueued = queued.length > 0;

    let targets: string[] = [];
    if (typeof pathsOverride === "string") {
      targets = [pathsOverride];
    } else if (Array.isArray(pathsOverride)) {
      targets = pathsOverride;
    } else if (!uploadingQueued) {
      targets =
        get().inputPaths.length > 0 ? get().inputPaths : get().inputPath ? [get().inputPath] : [];
    }

    if (targets.length === 0 && !uploadingQueued) return;

    const { inPlace, outputDir, workers, corner, copyClean, preserveTree } = get();
    set({ loading: true, progress: null, error: null, result: null });

    try {
      if (uploadingQueued) {
        const uploadedPaths: string[] = [];
        const nameMap: Record<string, string> = {};
        for (let i = 0; i < queued.length; i++) {
          const file = queued[i];
          set({
            progress: {
              percent: Math.round((i / queued.length) * 15),
              message: `Uploading images (${i + 1} / ${queued.length})...`,
              msg_key: "watermark.uploading",
              msg_params: { done: String(i + 1), total: String(queued.length) },
            },
          });
          const staged = await uploadMediaFile(file);
          uploadedPaths.push(staged.path);
          nameMap[staged.path] = file.name;
        }
        targets = uploadedPaths;
        set({ remoteNameMap: nameMap });
      }

      const input: WatermarkInput = {
        images: targets.map((p) => ({ localUrl: p, kind: "image" })),
        inplace: inPlace,
        // Staged uploads write sibling outputs in staging so /api/media/download can serve them.
        output_dir: uploadingQueued ? "" : outputDir,
        copy_clean: copyClean,
        preserve_tree: preserveTree,
        ...(workers !== null ? { workers } : {}),
        ...(corner !== null ? { corner } : {}),
      };

      const res = await rtClient.request(
        Actions.WORKFLOW_RUN_NODE,
        { type: WATERMARK_NODE, input },
        { onProgress: (p) => !p.heartbeat && set({ progress: p }) }
      );
      set({ result: res.output as WatermarkNodeResult, loading: false, progress: null });
    } catch (err) {
      set({ error: toActionError(err), loading: false, progress: null });
    }
  },

  removeVideoWatermark: async () => {
    const {
      videoInputPath,
      queuedVideoFile,
      outputDir,
      videoMode,
      videoX,
      videoY,
      videoWidth,
      videoHeight,
      videoCropEdge,
      videoCropPx,
    } = get();
    if (!videoInputPath && !queuedVideoFile) return;
    set({ loading: true, progress: null, error: null, videoResult: null });
    try {
      let target = videoInputPath;
      let uploading = false;
      if (queuedVideoFile) {
        uploading = true;
        set({
          progress: {
            percent: 5,
            message: "Uploading video...",
            msg_key: "watermark.video.uploading",
            msg_params: {},
          },
        });
        const staged = await uploadMediaFile(queuedVideoFile);
        target = staged.path;
        set((s) => ({
          remoteNameMap: { ...s.remoteNameMap, [staged.path]: queuedVideoFile.name },
        }));
      }
      const input: VideoWatermarkInput = {
        video_path: { localUrl: target, kind: "video" },
        ...(videoMode ? { mode: videoMode } : {}),
        ...(videoX !== null ? { x: videoX } : {}),
        ...(videoY !== null ? { y: videoY } : {}),
        ...(videoWidth !== null ? { width: videoWidth } : {}),
        ...(videoHeight !== null ? { height: videoHeight } : {}),
        ...(videoCropEdge ? { crop_edge: videoCropEdge } : {}),
        ...(videoCropPx !== null ? { crop_px: videoCropPx } : {}),
        ...(!uploading && outputDir.trim() ? { output_dir: outputDir.trim() } : {}),
      };
      const res = await rtClient.request(
        Actions.WORKFLOW_RUN_NODE,
        { type: VIDEO_WATERMARK_NODE, input },
        { onProgress: (p) => !p.heartbeat && set({ progress: p }) }
      );
      set({ videoResult: res.output as VideoWatermarkNodeResult, loading: false, progress: null });
    } catch (err) {
      set({ error: toActionError(err), loading: false, progress: null });
    }
  },

  downloadOutput: async (path, preferredName) => {
    set({ downloadingPath: path, error: null });
    try {
      await saveMediaToDevice(path, preferredName || mediaBasename(path));
    } catch (err) {
      set({ error: toActionError(err) });
    } finally {
      set({ downloadingPath: null });
    }
  },

  reset: () => {
    get().revokeAllObjectURLs();
    const prevComp = get().comparisonItem;
    if (prevComp) {
      if (prevComp.beforeUrl?.startsWith("blob:")) safeRevokeObjectURL(prevComp.beforeUrl);
      if (prevComp.afterUrl?.startsWith("blob:")) safeRevokeObjectURL(prevComp.afterUrl);
    }
    set({
      inputPath: "",
      inputPaths: [],
      queuedFiles: [],
      queuedObjectKeys: [],
      videoInputPath: "",
      queuedVideoFile: null,
      videoResult: null,
      objectUrls: {},
      remoteNameMap: {},
      downloadingPath: null,
      result: null,
      error: null,
      loading: false,
      progress: null,
      comparisonItem: null,
      comparisonZoom: 1.0,
      comparisonPan: { x: 0, y: 0 },
      comparisonSliderPos: 50,
    });
  },
}));
