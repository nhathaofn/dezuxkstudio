import { create } from "zustand";
import { Actions, NodeTypes, type ActionProgress, type NodeMap } from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";

export const WATERMARK_NODE = NodeTypes.WATERMARK_REMOVER;
export type WatermarkInput = NodeMap[typeof WATERMARK_NODE]["input"];
export type WatermarkNodeResult = NodeMap[typeof WATERMARK_NODE]["output"];
export type WatermarkItemResult = NonNullable<WatermarkNodeResult["results"]>[number];

interface WatermarkState {
  inputPath: string;
  inputPaths: string[];
  inPlace: boolean;
  outputDir: string;
  /** null = the node's default (from workflow:specs). */
  workers: number | null;
  /** null = the node's default (from workflow:specs). */
  corner: string | null;
  loading: boolean;
  /** Live per-image progress of the running batch (null when idle). */
  progress: ActionProgress | null;
  result: WatermarkNodeResult | null;
  error: ActionError | null;

  setInputPath: (path: string) => void;
  setInputPaths: (paths: string[]) => void;
  setInPlace: (inPlace: boolean) => void;
  setOutputDir: (dir: string) => void;
  setWorkers: (workers: number) => void;
  setCorner: (corner: string) => void;
  removeWatermark: (pathsOverride?: string[] | string) => Promise<void>;
  reset: () => void;
}

export const useWatermarkStore = create<WatermarkState>((set, get) => ({
  inputPath: "",
  inputPaths: [],
  inPlace: false,
  outputDir: "",
  workers: null,
  corner: null,
  loading: false,
  progress: null,
  result: null,
  error: null,

  setInputPath: (inputPath) =>
    set({
      inputPath,
      inputPaths: inputPath ? [inputPath] : [],
      error: null,
      result: null,
    }),

  setInputPaths: (inputPaths) =>
    set({
      inputPaths,
      inputPath: inputPaths[0] || "",
      error: null,
      result: null,
    }),

  setInPlace: (inPlace) => set({ inPlace }),
  setOutputDir: (outputDir) => set({ outputDir }),
  setWorkers: (workers) => set({ workers }),
  setCorner: (corner) => set({ corner }),

  removeWatermark: async (pathsOverride?: string[] | string) => {
    let targets: string[];
    if (typeof pathsOverride === "string") {
      targets = [pathsOverride];
    } else if (Array.isArray(pathsOverride)) {
      targets = pathsOverride;
    } else {
      targets =
        get().inputPaths.length > 0 ? get().inputPaths : get().inputPath ? [get().inputPath] : [];
    }

    if (targets.length === 0) return;

    const { inPlace, outputDir, workers, corner } = get();
    const input: WatermarkInput = {
      images: targets.map((p) => ({ localUrl: p, kind: "image" })),
      inplace: inPlace,
      output_dir: outputDir,
      ...(workers !== null ? { workers } : {}),
      ...(corner !== null ? { corner } : {}),
    };

    set({ loading: true, progress: null, error: null, result: null });
    try {
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

  reset: () =>
    set({
      inputPath: "",
      inputPaths: [],
      result: null,
      error: null,
      loading: false,
      progress: null,
    }),
}));
