import { appConfig } from "../../config/appConfig";
import { rtClient } from "./client";
import type { Endpoint } from "./endpoint";
import { ActionError, ClientErrorCodes } from "./errors";
import type { ErrorBody } from "../../contracts/generated";

export interface UploadedMedia {
  path: string;
  name: string;
  size: number;
}

/** Converts a ws(s) endpoint URL into its matching http(s) server base URL. */
export function httpBaseUrl(wsUrl: string): string {
  const u = new URL(wsUrl);
  u.protocol = u.protocol === "wss:" ? "https:" : "http:";
  const cleanPath = u.pathname.endsWith(appConfig.wsPath)
    ? u.pathname.slice(0, -appConfig.wsPath.length)
    : u.pathname;
  u.pathname = cleanPath.replace(/\/+$/, "");
  u.search = "";
  u.hash = "";
  return u.toString().replace(/\/+$/, "");
}

/** Returns the final path segment for display and default download filenames. */
export function mediaBasename(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, "");
  const parts = trimmed.split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

function resolveActiveEndpoint(endpoint?: Endpoint | null): Endpoint {
  const ep = endpoint ?? rtClient.getEndpoint();
  if (!ep || !ep.url) {
    throw new ActionError(ClientErrorCodes.NOT_CONNECTED, "not connected to the server");
  }
  return ep;
}

function authHeaders(token: string): Record<string, string> | undefined {
  return token ? { Authorization: `Bearer ${token}` } : undefined;
}

async function parseHttpError(res: Response, fallbackMsg: string): Promise<ActionError> {
  try {
    const body = (await res.json()) as { error?: ErrorBody };
    if (
      body?.error &&
      typeof body.error.code === "string" &&
      typeof body.error.message === "string"
    ) {
      return ActionError.fromBody(body.error);
    }
  } catch {
    // Non-JSON error body; fall back to HTTP status.
  }
  if (res.status === 401 || res.status === 403) {
    return new ActionError("ERR_UNAUTHORIZED", `${fallbackMsg} (HTTP ${res.status})`);
  }
  if (res.status === 404) {
    return new ActionError("ERR_NOT_FOUND", `${fallbackMsg} (HTTP ${res.status})`);
  }
  if (res.status === 400 || res.status === 413 || res.status === 415) {
    return new ActionError("ERR_VALIDATION", `${fallbackMsg} (HTTP ${res.status})`);
  }
  return new ActionError("ERR_INTERNAL", `${fallbackMsg} (HTTP ${res.status})`);
}

/** Uploads a browser File to the core server's authenticated media staging endpoint. */
export async function uploadMediaFile(
  file: File,
  endpoint?: Endpoint | null
): Promise<UploadedMedia> {
  const ep = resolveActiveEndpoint(endpoint);
  const url = `${httpBaseUrl(ep.url)}${appConfig.mediaUploadPath}`;
  const form = new FormData();
  form.append("file", file, file.name);

  const res = await fetch(url, {
    method: "POST",
    body: form,
    headers: authHeaders(ep.token),
  });
  if (!res.ok) {
    throw await parseHttpError(res, `Upload failed for ${file.name}`);
  }
  return (await res.json()) as UploadedMedia;
}

/** Downloads a media file from the core server via authenticated HTTP GET. */
export async function downloadMediaBlob(
  path: string,
  endpoint?: Endpoint | null
): Promise<{ blob: Blob; filename: string }> {
  const ep = resolveActiveEndpoint(endpoint);
  const url = `${httpBaseUrl(ep.url)}${appConfig.mediaDownloadPath}?path=${encodeURIComponent(path)}`;
  const res = await fetch(url, {
    method: "GET",
    headers: authHeaders(ep.token),
  });
  if (!res.ok) {
    throw await parseHttpError(res, `Download failed for ${mediaBasename(path)}`);
  }
  const blob = await res.blob();
  const filename =
    parseDispositionFilename(res.headers.get("Content-Disposition")) || mediaBasename(path);
  return { blob, filename };
}

/** Downloads a media file from the server and triggers a browser Save-As download. */
export async function saveMediaToDevice(
  path: string,
  preferredName?: string,
  endpoint?: Endpoint | null
): Promise<void> {
  const { blob, filename } = await downloadMediaBlob(path, endpoint);
  const objectUrl = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = objectUrl;
  link.download = preferredName || filename || "image.png";
  link.rel = "noopener";
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(objectUrl), 1000);
}

function parseDispositionFilename(header: string | null): string | null {
  if (!header) return null;
  const starMatch = /filename\*=UTF-8''([^;]+)/i.exec(header);
  if (starMatch?.[1]) {
    try {
      return decodeURIComponent(starMatch[1]);
    } catch {
      return starMatch[1];
    }
  }
  const quotedMatch = /filename="([^"]+)"/i.exec(header);
  if (quotedMatch?.[1]) return quotedMatch[1];
  const bareMatch = /filename=([^;\s]+)/i.exec(header);
  return bareMatch?.[1] ?? null;
}
