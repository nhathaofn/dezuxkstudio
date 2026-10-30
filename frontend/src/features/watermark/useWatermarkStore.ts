import { create } from "zustand";
import { Actions } from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";

export interface WatermarkItemResult {
  path: string;
  out_path: string;
  detected: boolean;
  score: number;
  size?: number;
  error?: string;
}

export interface WatermarkNodeResult {
  image: {
    localUrl: string;
    kind: string;
    mimeType?: string;
  };
  images?: Array<{
    localUrl: string;
    kind: string;
    mimeType?: string;
  }>;
  detected: boolean;
  total_count?: number;
  detected_count?: number;
  results?: WatermarkItemResult[];
}

interface WatermarkState {
  inputPath: string;
  inputPaths: string[];
  inPlace: boolean;
  outputDir: string;
  workers: number;
  corner: string;
  loading: boolean;
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
  workers: 0,
  corner: "bottom-right",
  loading: false,
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

    set({ loading: true, error: null, result: null });
    try {
      const res = await rtClient.request(Actions.WORKFLOW_RUN_NODE, {
        type: "tool.watermark_remover",
        input: {
          image: targets.length === 1 ? targets[0] : undefined,
          images: targets,
          inplace: get().inPlace,
          output_dir: get().outputDir,
          workers: get().workers,
          corner: get().corner,
        },
      });
      set({ result: res.output as WatermarkNodeResult, loading: false });
    } catch (err) {
      set({ error: toActionError(err), loading: false });
    }
  },

  reset: () =>
    set({
      inputPath: "",
      inputPaths: [],
      result: null,
      error: null,
      loading: false,
    }),
}));
