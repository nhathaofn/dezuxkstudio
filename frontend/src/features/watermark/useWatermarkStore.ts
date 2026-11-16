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
  inputPath: string;
  inputPaths: string[];
  /** Browser/client File objects staged via HTTP upload when processed. */
  queuedFiles: File[];
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
  removeQueuedFile: (fileName: string) => void;
  createTrackedObjectURL: (key: string, blob: Blob | File) => string;
  revokeTrackedObjectURL: (keyOrUrl: string) => void;
  revokeAllObjectURLs: () => void;
  setInPlace: (inPlace: boolean) => void;
  setOutputDir: (dir: string) => void;
  setCopyClean: (copyClean: boolean) => void;
  setPreserveTree: (preserveTree: boolean) => void;
  setWorkers: (workers: number) => void;
  setCorner: (corner: string) => void;
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
  inputPath: "",
  inputPaths: [],
  queuedFiles: [],
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
      objectUrls: {},
      remoteNameMap: {},
      error: null,
      result: null,
    });
  },

  setQueuedFiles: (queuedFiles) => {
    get().revokeAllObjectURLs();
    const newObjectUrls: Record<string, string> = {};
    if (typeof URL !== "undefined" && typeof URL.createObjectURL === "function") {
      for (const file of queuedFiles) {
        try {
          const url = URL.createObjectURL(file);
          newObjectUrls[file.name] = url;
        } catch {
          // Ignored in test/node environments
        }
      }
    }
    set({
      queuedFiles,
      inputPaths: queuedFiles.map((f) => f.name),
      inputPath: queuedFiles[0]?.name || "",
      objectUrls: newObjectUrls,
      remoteNameMap: {},
      error: null,
      result: null,
    });
  },

  removeQueuedFile: (fileName: string) => {
    const { queuedFiles, objectUrls } = get();
    const url = objectUrls[fileName];
    if (url) {
      safeRevokeObjectURL(url);
    }
    const nextUrls = { ...objectUrls };
    delete nextUrls[fileName];
    const nextFiles = queuedFiles.filter((f) => f.name !== fileName);
    set({
      queuedFiles: nextFiles,
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
