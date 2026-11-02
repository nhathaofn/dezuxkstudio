import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ActionProgress, BinaryID, BinaryInfo } from "../../contracts/generated";
import { rtClient, ActionError } from "../../lib/rtClient";
import { useBinariesStore } from "./useBinariesStore";

const mockBinaries: BinaryInfo[] = [
  {
    id: "ffmpeg",
    name: "FFmpeg",
    description: "Universal media converter, transcoder, and video filter engine.",
    executable: "ffmpeg.exe",
    resolved_path: "",
    custom_path: "",
    source: "none",
    version: "",
    status: "missing",
    can_download: true,
  },
  {
    id: "ffprobe",
    name: "FFprobe",
    description: "Multimedia stream analyzer and container metadata extractor.",
    executable: "ffprobe.exe",
    resolved_path: "",
    custom_path: "",
    source: "none",
    version: "",
    status: "missing",
    can_download: true,
  },
  {
    id: "yt_dlp",
    name: "yt-dlp",
    description: "Video and audio stream downloader and extractor from web platforms.",
    executable: "yt-dlp.exe",
    resolved_path: "",
    custom_path: "",
    source: "none",
    version: "",
    status: "missing",
    can_download: true,
  },
];

describe("useBinariesStore (R3)", () => {
  beforeEach(() => {
    useBinariesStore.setState({
      binaries: [],
      loading: false,
      error: null,
    });
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("Tier 1 — Feature Coverage", () => {
    it("fetches binaries list and updates store state", async () => {
      vi.spyOn(rtClient, "request").mockResolvedValueOnce({
        binaries: mockBinaries,
      });

      const { fetchBinaries } = useBinariesStore.getState();
      await fetchBinaries();

      const state = useBinariesStore.getState();
      expect(state.loading).toBe(false);
      expect(state.error).toBeNull();
      expect(state.binaries).toHaveLength(3);
      expect(state.binaries[0].id).toBe("ffmpeg");
      expect(state.binaries[1].id).toBe("ffprobe");
      expect(state.binaries[2].id).toBe("yt_dlp");
    });

    it("probes an individual binary and updates its metadata in place", async () => {
      useBinariesStore.setState({ binaries: [...mockBinaries] });

      const updatedFfmpeg: BinaryInfo = {
        ...mockBinaries[0],
        status: "available",
        version: "7.1",
        resolved_path: "C:\\tools\\ffmpeg.exe",
        source: "tools",
      };

      vi.spyOn(rtClient, "request").mockResolvedValueOnce(updatedFfmpeg);

      const { probe } = useBinariesStore.getState();
      await probe("ffmpeg");

      const state = useBinariesStore.getState();
      const ffmpeg = state.binaries.find((b) => b.id === "ffmpeg");
      expect(ffmpeg?.status).toBe("available");
      expect(ffmpeg?.version).toBe("7.1");
      expect(ffmpeg?.resolved_path).toBe("C:\\tools\\ffmpeg.exe");

      // Verify other binaries were untouched
      const ffprobe = state.binaries.find((b) => b.id === "ffprobe");
      expect(ffprobe?.status).toBe("missing");
    });

    it("sets custom binary path and updates store", async () => {
      useBinariesStore.setState({ binaries: [...mockBinaries] });

      const updatedYtDlp: BinaryInfo = {
        ...mockBinaries[2],
        custom_path: "D:\\custom\\yt-dlp.exe",
        status: "available",
        version: "2024.10.22",
      };

      vi.spyOn(rtClient, "request").mockResolvedValueOnce(updatedYtDlp);

      const { setCustomPath } = useBinariesStore.getState();
      await setCustomPath("yt_dlp", "D:\\custom\\yt-dlp.exe");

      const state = useBinariesStore.getState();
      const ytDlp = state.binaries.find((b) => b.id === "yt_dlp");
      expect(ytDlp?.custom_path).toBe("D:\\custom\\yt-dlp.exe");
      expect(ytDlp?.version).toBe("2024.10.22");
    });

    it("downloads binary tool with progress callback", async () => {
      useBinariesStore.setState({ binaries: [...mockBinaries] });

      const downloadedFfmpeg: BinaryInfo = {
        ...mockBinaries[0],
        status: "available",
        version: "7.1",
        source: "tools",
      };

      const progressEvents: ActionProgress[] = [];
      const onProgress = (p: ActionProgress) => progressEvents.push(p);

      vi.spyOn(rtClient, "request").mockImplementationOnce(async (_action, _payload, opts) => {
        opts?.onProgress?.({ percent: 50, message: "Downloading..." });
        opts?.onProgress?.({ percent: 100, message: "Extracting..." });
        return downloadedFfmpeg;
      });

      const ctrl = new AbortController();
      const { download } = useBinariesStore.getState();
      await download("ffmpeg", ctrl.signal, onProgress);

      expect(progressEvents).toHaveLength(2);
      expect(progressEvents[0].percent).toBe(50);
      expect(progressEvents[1].percent).toBe(100);

      const state = useBinariesStore.getState();
      const ffmpeg = state.binaries.find((b) => b.id === "ffmpeg");
      expect(ffmpeg?.status).toBe("available");
    });
  });

  describe("Tier 2 — Boundary & Corner Cases", () => {
    it("handles fetchBinaries failure gracefully and records error in store", async () => {
      vi.spyOn(rtClient, "request").mockRejectedValueOnce(
        new ActionError("ERR_TIMEOUT", "Connection to core timed out")
      );

      const { fetchBinaries } = useBinariesStore.getState();
      await fetchBinaries();

      const state = useBinariesStore.getState();
      expect(state.loading).toBe(false);
      expect(state.error).not.toBeNull();
      expect(state.error?.code).toBe("ERR_TIMEOUT");
    });

    it("handles probe on an ID not in store gracefully without crashing", async () => {
      useBinariesStore.setState({ binaries: [...mockBinaries] });

      const unknownBinary: BinaryInfo = {
        id: "nonexistent" as unknown as BinaryID,
        name: "Unknown",
        description: "",
        executable: "",
        resolved_path: "",
        custom_path: "",
        source: "none",
        version: "",
        status: "invalid",
        can_download: false,
      };

      vi.spyOn(rtClient, "request").mockResolvedValueOnce(unknownBinary);

      const { probe } = useBinariesStore.getState();
      await probe("nonexistent" as unknown as BinaryID);

      const state = useBinariesStore.getState();
      // Original 3 binaries remain unchanged
      expect(state.binaries).toHaveLength(3);
    });

    it("handles resetting custom path with empty string", async () => {
      useBinariesStore.setState({
        binaries: [
          {
            ...mockBinaries[0],
            custom_path: "C:\\custom\\ffmpeg.exe",
          },
        ],
      });

      const resetBinary: BinaryInfo = {
        ...mockBinaries[0],
        custom_path: "",
      };

      vi.spyOn(rtClient, "request").mockResolvedValueOnce(resetBinary);

      const { setCustomPath } = useBinariesStore.getState();
      await setCustomPath("ffmpeg", "");

      const state = useBinariesStore.getState();
      expect(state.binaries[0].custom_path).toBe("");
    });
  });

  describe("Tier 3 — Cross-Feature Combinations", () => {
    it("propagates download failure and rethrows for UI error boundary", async () => {
      useBinariesStore.setState({ binaries: [...mockBinaries] });

      const networkError = new ActionError(
        "ERR_INTERNAL",
        "Download failed: 503 Service Unavailable"
      );
      vi.spyOn(rtClient, "request").mockRejectedValueOnce(networkError);

      const ctrl = new AbortController();
      const { download } = useBinariesStore.getState();

      await expect(download("ffmpeg", ctrl.signal, () => {})).rejects.toThrow(
        "Download failed: 503 Service Unavailable"
      );

      const state = useBinariesStore.getState();
      expect(state.error?.code).toBe("ERR_INTERNAL");
    });
  });

  describe("Tier 4 — Real-World Application Scenarios", () => {
    it("simulates complete lifecycle: fetch -> probe -> custom path -> verified status", async () => {
      // 1. Initial list response
      vi.spyOn(rtClient, "request")
        .mockResolvedValueOnce({ binaries: mockBinaries }) // fetch
        .mockResolvedValueOnce({ ...mockBinaries[0], status: "available", version: "7.1" }) // probe ffmpeg
        .mockResolvedValueOnce({
          ...mockBinaries[1],
          custom_path: "C:\\opt\\ffprobe.exe",
          status: "available",
          version: "7.1",
        }); // set path ffprobe

      const store = useBinariesStore.getState();

      // Step 1: Fetch
      await store.fetchBinaries();
      expect(useBinariesStore.getState().binaries).toHaveLength(3);
      expect(useBinariesStore.getState().binaries[0].status).toBe("missing");

      // Step 2: Probe ffmpeg
      await store.probe("ffmpeg");
      expect(useBinariesStore.getState().binaries.find((b) => b.id === "ffmpeg")?.status).toBe(
        "available"
      );

      // Step 3: Configure custom path for ffprobe
      await store.setCustomPath("ffprobe", "C:\\opt\\ffprobe.exe");
      const ffprobe = useBinariesStore.getState().binaries.find((b) => b.id === "ffprobe");
      expect(ffprobe?.custom_path).toBe("C:\\opt\\ffprobe.exe");
      expect(ffprobe?.status).toBe("available");
    });
  });
});
