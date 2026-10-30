import { create } from "zustand";
import { Actions } from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";

export interface WatermarkNodeResult {
  image: {
    localUrl: string;
    kind: string;
    mimeType?: string;
  };
  detected: boolean;
}

interface WatermarkState {
  inputPath: string;
  inPlace: boolean;
  outputDir: string;
  loading: boolean;
  result: WatermarkNodeResult | null;
  error: ActionError | null;

  setInputPath: (path: string) => void;
  setInPlace: (inPlace: boolean) => void;
  setOutputDir: (dir: string) => void;
  removeWatermark: (pathOverride?: string) => Promise<void>;
  reset: () => void;
}

export const useWatermarkStore = create<WatermarkState>((set, get) => ({
  inputPath: "",
  inPlace: false,
  outputDir: "",
  loading: false,
  result: null,
  error: null,

  setInputPath: (inputPath) => set({ inputPath, error: null, result: null }),
  setInPlace: (inPlace) => set({ inPlace }),
  setOutputDir: (outputDir) => set({ outputDir }),

  removeWatermark: async (pathOverride?: string) => {
    const targetPath = pathOverride ?? get().inputPath;
    if (!targetPath) return;

    set({ loading: true, error: null, result: null });
    try {
      const res = await rtClient.request(Actions.WORKFLOW_RUN_NODE, {
        type: "tool.watermark_remover",
        input: {
          image: targetPath,
          inplace: get().inPlace,
          output_dir: get().outputDir,
        },
      });
      set({ result: res.output as WatermarkNodeResult, loading: false });
    } catch (err) {
      set({ error: toActionError(err), loading: false });
    }
  },

  reset: () => set({ inputPath: "", result: null, error: null, loading: false }),
}));
