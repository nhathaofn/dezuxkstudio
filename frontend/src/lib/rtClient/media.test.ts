import { describe, expect, it, vi, afterEach } from "vitest";
import {
  downloadMediaBlob,
  httpBaseUrl,
  mediaBasename,
  uploadMediaFile,
  type Endpoint,
} from "./index";

const makeEndpoint = (overrides: Partial<Endpoint> = {}): Endpoint => ({
  url: "wss://core.example.com:8443/ws",
  token: "secret-token",
  mode: "client",
  actionTimeoutMs: 30_000,
  longActionTimeoutMs: 600_000,
  pingIntervalMs: 30_000,
  checkUpdatesOnStartup: false,
  source: "bridge",
  ...overrides,
});

describe("rtClient media HTTP staging helpers", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("converts ws/wss endpoints to matching http/https base URLs", () => {
    expect(httpBaseUrl("ws://127.0.0.1:8080/ws")).toBe("http://127.0.0.1:8080");
    expect(httpBaseUrl("wss://core.example.com:8443/ws")).toBe("https://core.example.com:8443");
    expect(httpBaseUrl("wss://proxy.example.com/studio/ws")).toBe(
      "https://proxy.example.com/studio"
    );
  });

  it("extracts basenames across POSIX and Windows separators", () => {
    expect(mediaBasename("D:\\data\\media\\staging\\abc\\pic_nowm.png")).toBe("pic_nowm.png");
    expect(mediaBasename("/var/lib/studio/media/staging/id/out.jpg")).toBe("out.jpg");
    expect(mediaBasename("plain.webp")).toBe("plain.webp");
  });

  it("uploadMediaFile posts multipart FormData with Bearer authorization", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ path: "/data/media/staging/123/test.png", name: "test.png", size: 4 }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        )
      );
    vi.stubGlobal("fetch", fetchMock);

    const file = new File([new Uint8Array([1, 2, 3, 4])], "test.png", { type: "image/png" });
    const uploaded = await uploadMediaFile(file, makeEndpoint());

    expect(uploaded).toEqual({
      path: "/data/media/staging/123/test.png",
      name: "test.png",
      size: 4,
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://core.example.com:8443/api/media/upload",
      expect.objectContaining({
        method: "POST",
        headers: { Authorization: "Bearer secret-token" },
      })
    );
    const body = fetchMock.mock.calls[0][1]?.body as FormData;
    expect(body).toBeInstanceOf(FormData);
    expect(body.get("file")).toBeInstanceOf(File);
  });

  it("downloadMediaBlob sends Bearer token and extracts Content-Disposition filename", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(
      new Response(new Uint8Array([9, 8, 7]), {
        status: 200,
        headers: {
          "Content-Type": "image/png",
          "Content-Disposition": 'attachment; filename="clean_nowm.png"',
        },
      })
    );
    vi.stubGlobal("fetch", fetchMock);

    const { blob, filename } = await downloadMediaBlob(
      "/data/media/staging/123/clean_nowm.png",
      makeEndpoint({ url: "ws://127.0.0.1:9000/ws", token: "" })
    );
    expect(filename).toBe("clean_nowm.png");
    expect(blob.size).toBe(3);
    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:9000/api/media/download?path=%2Fdata%2Fmedia%2Fstaging%2F123%2Fclean_nowm.png",
      expect.objectContaining({ method: "GET", headers: undefined })
    );
  });

  it("maps HTTP JSON error responses into ActionError", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              code: "ERR_VALIDATION",
              message: "only PNG, JPEG, and WebP images are supported",
            },
          }),
          { status: 415, headers: { "Content-Type": "application/json" } }
        )
      )
    );

    const file = new File([new Uint8Array([1])], "bad.txt", { type: "text/plain" });
    await expect(uploadMediaFile(file, makeEndpoint())).rejects.toMatchObject({
      code: "ERR_VALIDATION",
      message: "only PNG, JPEG, and WebP images are supported",
    });
  });
});
