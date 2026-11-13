import { mediaBasename, uploadMediaFile } from "../../lib/rtClient";
import type {
  MediaRef,
  ModelCandidate,
  ProviderDefaultsResult,
  ProviderPurpose,
  PurposeDefault,
} from "../../contracts/generated";

export function resolvePurposeDefault(
  defaults: ProviderDefaultsResult | null,
  purpose: ProviderPurpose
): PurposeDefault | undefined {
  return defaults?.purposes.find((p) => p.purpose === purpose);
}

export function resolveSelectedCandidate(
  defaults: ProviderDefaultsResult | null,
  purpose: ProviderPurpose,
  modelRef: string
): ModelCandidate | undefined {
  const entry = resolvePurposeDefault(defaults, purpose);
  if (!entry) return undefined;
  const target = modelRef || entry.effective;
  return entry.candidates.find((c) => c.ref === target);
}

export interface PickedImage {
  /** Local filesystem path (Desktop bridge) when available without upload. */
  localPath?: string;
  /** Browser File object requiring upload to core staging dir before execution. */
  file?: File;
  /** Human-readable display label (filename or path). */
  label: string;
}

export function fromLocalPath(path: string): PickedImage {
  return { localPath: path, label: mediaBasename(path) || path };
}

export function fromBrowserFile(file: File): PickedImage {
  return { file, label: file.name };
}

export async function preparePickedImage(item: PickedImage): Promise<MediaRef> {
  if (item.localPath) {
    return { localUrl: item.localPath, kind: "image" };
  }
  if (item.file) {
    const uploaded = await uploadMediaFile(item.file);
    return { localUrl: uploaded.path, kind: "image" };
  }
  return { localUrl: item.label, kind: "image" };
}

export async function preparePickedImages(items: PickedImage[]): Promise<MediaRef[]> {
  const refs: MediaRef[] = [];
  for (const item of items) {
    refs.push(await preparePickedImage(item));
  }
  return refs;
}

export function formatByteSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let val = bytes;
  let idx = 0;
  while (val >= 1024 && idx < units.length - 1) {
    val /= 1024;
    idx++;
  }
  return `${val >= 10 || idx === 0 ? val.toFixed(0) : val.toFixed(1)} ${units[idx]}`;
}
