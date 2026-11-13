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

interface WatermarkState {
  inputPath: string;
  inputPaths: string[];
  /** Browser/client File objects staged via HTTP upload when processed. */
  queuedFiles: File[];
  /** Maps uploaded server staging paths back to original client filenames for display. */
  remoteNameMap: Record<string, string>;
  inPlace: boolean;
  outputDir: string;
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

  setInputPath: (path: string) => void;
  setInputPaths: (paths: string[]) => void;
  setQueuedFiles: (files: File[]) => void;
  setInPlace: (inPlace: boolean) => void;
  setOutputDir: (dir: string) => void;
  setWorkers: (workers: number) => void;
  setCorner: (corner: string) => void;
  removeWatermark: (pathsOverride?: string[] | string) => Promise<void>;
  downloadOutput: (path: string, preferredName?: string) => Promise<void>;
  reset: () => void;
}

export const useWatermarkStore = create<WatermarkState>((set, get) => ({
  inputPath: "",
  inputPaths: [],
  queuedFiles: [],
  remoteNameMap: {},
  inPlace: false,
  outputDir: "",
  workers: null,
  corner: null,
  loading: false,
  downloadingPath: null,
  progress: null,
  result: null,
  error: null,

  setInputPath: (inputPath) =>
    set({
      inputPath,
      inputPaths: inputPath ? [inputPath] : [],
      queuedFiles: [],
      remoteNameMap: {},
      error: null,
      result: null,
    }),

  setInputPaths: (inputPaths) =>
    set({
      inputPaths,
      inputPath: inputPaths[0] || "",
      queuedFiles: [],
      remoteNameMap: {},
      error: null,
      result: null,
    }),

  setQueuedFiles: (queuedFiles) =>
    set({
      queuedFiles,
      inputPaths: queuedFiles.map((f) => f.name),
      inputPath: queuedFiles[0]?.name || "",
      remoteNameMap: {},
      error: null,
      result: null,
    }),

  setInPlace: (inPlace) => set({ inPlace }),
  setOutputDir: (outputDir) => set({ outputDir }),
  setWorkers: (workers) => set({ workers }),
  setCorner: (corner) => set({ corner }),

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

    const { inPlace, outputDir, workers, corner } = get();
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

  reset: () =>
    set({
      inputPath: "",
      inputPaths: [],
      queuedFiles: [],
      remoteNameMap: {},
      downloadingPath: null,
      result: null,
      error: null,
      loading: false,
      progress: null,
    }),
}));
