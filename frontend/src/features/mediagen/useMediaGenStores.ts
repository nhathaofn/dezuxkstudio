import { create } from "zustand";
import {
  Actions,
  NodeTypes,
  type ImageGenerateInput,
  type ImageGenerateOutput,
  type Spec,
  type TTSGenerateInput,
  type TTSGenerateOutput,
  type VideoGenerateInput,
  type VideoGenerateOutput,
} from "../../contracts/generated";
import {
  mediaBasename,
  rtClient,
  saveMediaToDevice,
  toActionError,
  type ActionError,
} from "../../lib/rtClient";
import type { ActionProgress } from "../../contracts/generated";
import { preparePickedImage, preparePickedImages, type PickedImage } from "./types";

interface RunMediaParams<TInput, TOutput> {
  nodeType: string;
  buildInput: () => Promise<TInput>;
  setLoading: (loading: boolean) => void;
  setProgress: (p: ActionProgress | null) => void;
  setError: (e: ActionError | null) => void;
  setResult: (r: TOutput | null) => void;
}

async function runMediaNode<TInput, TOutput>({
  nodeType,
  buildInput,
  setLoading,
  setProgress,
  setError,
  setResult,
}: RunMediaParams<TInput, TOutput>): Promise<void> {
  setLoading(true);
  setProgress(null);
  setError(null);
  setResult(null);
  try {
    const input = await buildInput();
    const res = await rtClient.request(
      Actions.WORKFLOW_RUN_NODE,
      { type: nodeType, input },
      { onProgress: (p) => !p.heartbeat && setProgress(p) }
    );
    setResult(res.output as TOutput);
  } catch (err) {
    setError(toActionError(err));
  } finally {
    setLoading(false);
    setProgress(null);
  }
}

async function downloadPathWithState(
  path: string,
  setDownloading: (p: string | null) => void,
  setError: (e: ActionError | null) => void
): Promise<void> {
  setDownloading(path);
  setError(null);
  try {
    await saveMediaToDevice(path, mediaBasename(path));
  } catch (err) {
    setError(toActionError(err));
  } finally {
    setDownloading(null);
  }
}

/* ── Image Generation Store ── */

interface ImageGenState {
  prompt: string;
  refs: PickedImage[];
  model: string;
  aspectRatio: string | null;
  resolution: string | null;
  seed: string;
  outputDir: string;
  loading: boolean;
  progress: ActionProgress | null;
  error: ActionError | null;
  result: ImageGenerateOutput | null;
  downloadingPath: string | null;

  setPrompt: (prompt: string) => void;
  addRefs: (items: PickedImage[]) => void;
  removeRef: (index: number) => void;
  clearRefs: () => void;
  setModel: (model: string) => void;
  setAspectRatio: (v: string | null) => void;
  setResolution: (v: string | null) => void;
  setSeed: (v: string) => void;
  setOutputDir: (v: string) => void;
  generate: (spec?: Spec) => Promise<void>;
  downloadResult: () => Promise<void>;
  reset: () => void;
}

export const useImageGenStore = create<ImageGenState>((set, get) => ({
  prompt: "",
  refs: [],
  model: "",
  aspectRatio: null,
  resolution: null,
  seed: "",
  outputDir: "",
  loading: false,
  progress: null,
  error: null,
  result: null,
  downloadingPath: null,

  setPrompt: (prompt) => set({ prompt, error: null }),
  addRefs: (items) => set((st) => ({ refs: [...st.refs, ...items], error: null })),
  removeRef: (index) => set((st) => ({ refs: st.refs.filter((_, i) => i !== index) })),
  clearRefs: () => set({ refs: [] }),
  setModel: (model) => set({ model }),
  setAspectRatio: (aspectRatio) => set({ aspectRatio }),
  setResolution: (resolution) => set({ resolution }),
  setSeed: (seed) => set({ seed }),
  setOutputDir: (outputDir) => set({ outputDir }),

  generate: (spec) =>
    runMediaNode<ImageGenerateInput, ImageGenerateOutput>({
      nodeType: NodeTypes.IMAGE_GENERATE,
      setLoading: (loading) => set({ loading }),
      setProgress: (progress) => set({ progress }),
      setError: (error) => set({ error }),
      setResult: (result) => set({ result }),
      buildInput: async () => {
        const s = get();
        const uploadedRefs = s.refs.length > 0 ? await preparePickedImages(s.refs) : undefined;
        const specAspect = spec?.params?.find((p) => p.name === "aspect_ratio")?.default;
        const specRes = spec?.params?.find((p) => p.name === "resolution")?.default;
        const parsedSeed = s.seed.trim() !== "" ? Number.parseInt(s.seed.trim(), 10) : undefined;
        return {
          prompt: s.prompt.trim(),
          ...(uploadedRefs ? { reference_images: uploadedRefs } : {}),
          ...(s.model ? { model: s.model } : {}),
          ...(s.aspectRatio || specAspect ? { aspect_ratio: s.aspectRatio || specAspect } : {}),
          ...(s.resolution || specRes ? { resolution: s.resolution || specRes } : {}),
          ...(Number.isFinite(parsedSeed) && (parsedSeed ?? -1) >= 0 ? { seed: parsedSeed } : {}),
          ...(s.outputDir.trim() ? { output_dir: s.outputDir.trim() } : {}),
        };
      },
    }),

  downloadResult: async () => {
    const res = get().result;
    if (!res?.path) return;
    await downloadPathWithState(
      res.path,
      (downloadingPath) => set({ downloadingPath }),
      (error) => set({ error })
    );
  },

  reset: () =>
    set({
      prompt: "",
      refs: [],
      error: null,
      result: null,
      progress: null,
      loading: false,
      downloadingPath: null,
    }),
}));

/* ── Video Generation Store ── */

interface VideoGenState {
  prompt: string;
  startImage: PickedImage | null;
  endImage: PickedImage | null;
  refs: PickedImage[];
  model: string;
  aspectRatio: string | null;
  resolution: string | null;
  duration: number | null;
  generateAudio: boolean;
  seed: string;
  outputDir: string;
  loading: boolean;
  progress: ActionProgress | null;
  error: ActionError | null;
  result: VideoGenerateOutput | null;
  downloadingPath: string | null;

  setPrompt: (prompt: string) => void;
  setStartImage: (img: PickedImage | null) => void;
  setEndImage: (img: PickedImage | null) => void;
  addRefs: (items: PickedImage[]) => void;
  removeRef: (index: number) => void;
  setModel: (model: string) => void;
  setAspectRatio: (v: string | null) => void;
  setResolution: (v: string | null) => void;
  setDuration: (v: number | null) => void;
  setGenerateAudio: (v: boolean) => void;
  setSeed: (v: string) => void;
  setOutputDir: (v: string) => void;
  generate: (spec?: Spec) => Promise<void>;
  downloadResult: () => Promise<void>;
  reset: () => void;
}

export const useVideoGenStore = create<VideoGenState>((set, get) => ({
  prompt: "",
  startImage: null,
  endImage: null,
  refs: [],
  model: "",
  aspectRatio: null,
  resolution: null,
  duration: null,
  generateAudio: false,
  seed: "",
  outputDir: "",
  loading: false,
  progress: null,
  error: null,
  result: null,
  downloadingPath: null,

  setPrompt: (prompt) => set({ prompt, error: null }),
  setStartImage: (startImage) => set({ startImage, error: null }),
  setEndImage: (endImage) => set({ endImage, error: null }),
  addRefs: (items) => set((st) => ({ refs: [...st.refs, ...items], error: null })),
  removeRef: (index) => set((st) => ({ refs: st.refs.filter((_, i) => i !== index) })),
  setModel: (model) => set({ model }),
  setAspectRatio: (aspectRatio) => set({ aspectRatio }),
  setResolution: (resolution) => set({ resolution }),
  setDuration: (duration) => set({ duration }),
  setGenerateAudio: (generateAudio) => set({ generateAudio }),
  setSeed: (seed) => set({ seed }),
  setOutputDir: (outputDir) => set({ outputDir }),

  generate: (spec) =>
    runMediaNode<VideoGenerateInput, VideoGenerateOutput>({
      nodeType: NodeTypes.VIDEO_GENERATE,
      setLoading: (loading) => set({ loading }),
      setProgress: (progress) => set({ progress }),
      setError: (error) => set({ error }),
      setResult: (result) => set({ result }),
      buildInput: async () => {
        const s = get();
        const startRef = s.startImage ? await preparePickedImage(s.startImage) : undefined;
        const endRef = s.endImage ? await preparePickedImage(s.endImage) : undefined;
        const refImgs = s.refs.length > 0 ? await preparePickedImages(s.refs) : undefined;
        const specAspect = spec?.params?.find((p) => p.name === "aspect_ratio")?.default;
        const specRes = spec?.params?.find((p) => p.name === "resolution")?.default;
        const parsedSeed = s.seed.trim() !== "" ? Number.parseInt(s.seed.trim(), 10) : undefined;
        return {
          ...(s.prompt.trim() ? { prompt: s.prompt.trim() } : {}),
          ...(startRef ? { start_image: startRef } : {}),
          ...(endRef ? { end_image: endRef } : {}),
          ...(refImgs ? { reference_images: refImgs } : {}),
          ...(s.model ? { model: s.model } : {}),
          ...(s.aspectRatio || specAspect ? { aspect_ratio: s.aspectRatio || specAspect } : {}),
          ...(s.resolution || specRes ? { resolution: s.resolution || specRes } : {}),
          ...(s.duration && s.duration > 0 ? { duration: s.duration } : {}),
          generate_audio: s.generateAudio,
          ...(Number.isFinite(parsedSeed) && (parsedSeed ?? -1) >= 0 ? { seed: parsedSeed } : {}),
          ...(s.outputDir.trim() ? { output_dir: s.outputDir.trim() } : {}),
        };
      },
    }),

  downloadResult: async () => {
    const res = get().result;
    if (!res?.path) return;
    await downloadPathWithState(
      res.path,
      (downloadingPath) => set({ downloadingPath }),
      (error) => set({ error })
    );
  },

  reset: () =>
    set({
      prompt: "",
      startImage: null,
      endImage: null,
      refs: [],
      error: null,
      result: null,
      progress: null,
      loading: false,
      downloadingPath: null,
    }),
}));

/* ── TTS Generation Store ── */

interface TTSGenState {
  text: string;
  model: string;
  voice: string;
  format: string | null;
  speed: string;
  language: string;
  outputDir: string;
  loading: boolean;
  progress: ActionProgress | null;
  error: ActionError | null;
  result: TTSGenerateOutput | null;
  downloadingPath: string | null;

  setText: (text: string) => void;
  setModel: (model: string) => void;
  setVoice: (voice: string) => void;
  setFormat: (v: string | null) => void;
  setSpeed: (v: string) => void;
  setLanguage: (v: string) => void;
  setOutputDir: (v: string) => void;
  generate: (spec?: Spec) => Promise<void>;
  downloadResult: () => Promise<void>;
  reset: () => void;
}

export const useTTSGenStore = create<TTSGenState>((set, get) => ({
  text: "",
  model: "",
  voice: "",
  format: null,
  speed: "1.0",
  language: "",
  outputDir: "",
  loading: false,
  progress: null,
  error: null,
  result: null,
  downloadingPath: null,

  setText: (text) => set({ text, error: null }),
  setModel: (model) => set({ model }),
  setVoice: (voice) => set({ voice }),
  setFormat: (format) => set({ format }),
  setSpeed: (speed) => set({ speed }),
  setLanguage: (language) => set({ language }),
  setOutputDir: (outputDir) => set({ outputDir }),

  generate: (spec) =>
    runMediaNode<TTSGenerateInput, TTSGenerateOutput>({
      nodeType: NodeTypes.TTS_GENERATE,
      setLoading: (loading) => set({ loading }),
      setProgress: (progress) => set({ progress }),
      setError: (error) => set({ error }),
      setResult: (result) => set({ result }),
      buildInput: async () => {
        const s = get();
        const specFmt = spec?.params?.find((p) => p.name === "format")?.default;
        const parsedSpeed = s.speed.trim() !== "" ? Number.parseFloat(s.speed.trim()) : undefined;
        return {
          text: s.text.trim(),
          ...(s.model ? { model: s.model } : {}),
          ...(s.voice.trim() ? { voice: s.voice.trim() } : {}),
          ...(s.format || specFmt ? { format: s.format || specFmt } : {}),
          ...(Number.isFinite(parsedSpeed) && (parsedSpeed ?? 0) > 0 ? { speed: parsedSpeed } : {}),
          ...(s.language.trim() ? { language: s.language.trim() } : {}),
          ...(s.outputDir.trim() ? { output_dir: s.outputDir.trim() } : {}),
        };
      },
    }),

  downloadResult: async () => {
    const res = get().result;
    if (!res?.path) return;
    await downloadPathWithState(
      res.path,
      (downloadingPath) => set({ downloadingPath }),
      (error) => set({ error })
    );
  },

  reset: () =>
    set({
      text: "",
      error: null,
      result: null,
      progress: null,
      loading: false,
      downloadingPath: null,
    }),
}));
