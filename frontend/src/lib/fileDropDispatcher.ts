import { onFileDrop } from "./bridge";

export type FileDropListener = (x: number, y: number, paths: string[]) => void;

const subscribers = new Set<FileDropListener>();
let unsubNative: (() => void) | null = null;

function dispatchFileDrop(x: number, y: number, paths: string[]): void {
  for (const listener of Array.from(subscribers)) {
    listener(x, y, paths);
  }
}

/**
 * Registers a dropzone hit-test listener with a single underlying Wails OnFileDrop binding.
 * Unsubscribes the native binding only when the final active dropzone unmounts.
 */
export function subscribeFileDrop(listener: FileDropListener): () => void {
  if (subscribers.size === 0 && !unsubNative) {
    unsubNative = onFileDrop(dispatchFileDrop, false);
  }
  subscribers.add(listener);

  return () => {
    subscribers.delete(listener);
    if (subscribers.size === 0 && unsubNative) {
      try {
        unsubNative();
      } finally {
        unsubNative = null;
      }
    }
  };
}

/** Testing helper returning current subscriber count. */
export function activeFileDropSubscriberCount(): number {
  return subscribers.size;
}
