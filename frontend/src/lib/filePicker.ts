import { getBridge, isDesktop } from "./bridge";

export interface PickMediaOptions {
  title?: string;
  multiple?: boolean;
  accept?: string;
  kind?: "image" | "video" | "audio" | "directory" | "executable";
}

export interface PickMediaResult {
  paths: string[];
  files: File[];
}

/**
 * Universal safe media & file picker.
 *
 * In desktop mode (Wails WebView2):
 * - Delegates exclusively to native Win32 dialogs via BridgeApi.
 * - When user cancels or closes the dialog, it returns empty and stops immediately.
 * - Crucially, it NEVER falls back to HTML file inputs in desktop mode, preventing
 *   concurrent modal dialogs and the WebView2 focus crash (MoveFocus -> The parameter is incorrect).
 *
 * In browser/remote mode:
 * - Falls back cleanly to HTML5 input element file picking.
 */
export async function pickMediaFiles(options: PickMediaOptions = {}): Promise<PickMediaResult> {
  const {
    title = "Select File",
    multiple = false,
    accept = "image/png,image/jpeg,image/webp",
    kind = "image",
  } = options;

  const bridge = getBridge();
  if (isDesktop() && bridge) {
    try {
      if (kind === "directory" && bridge.BrowseDirectory) {
        const picked = await bridge.BrowseDirectory(title);
        return {
          paths: picked ? [picked] : [],
          files: [],
        };
      }

      if (
        (kind === "executable" || kind === "video" || kind === "audio") &&
        bridge.BrowseExecutable
      ) {
        const picked = await bridge.BrowseExecutable(title);
        return {
          paths: picked ? [picked] : [],
          files: [],
        };
      }

      // Default kind === "image"
      if (multiple && bridge.BrowseImages) {
        const picked = await bridge.BrowseImages(title);
        return {
          paths: Array.isArray(picked) ? picked.filter(Boolean) : [],
          files: [],
        };
      }

      if (bridge.BrowseImage) {
        const picked = await bridge.BrowseImage(title);
        return {
          paths: picked ? [picked] : [],
          files: [],
        };
      }
    } catch (err) {
      console.error("Desktop file picker error:", err);
      return { paths: [], files: [] };
    }
  }

  // Fallback for browser / remote mode: standard HTML5 file picker
  return pickBrowserFiles({ multiple, accept });
}

/**
 * Prompts the user to pick files using an ephemeral HTML file input element.
 */
export function pickBrowserFiles(options: {
  multiple?: boolean;
  accept?: string;
}): Promise<PickMediaResult> {
  return new Promise((resolve) => {
    if (typeof document === "undefined") {
      resolve({ paths: [], files: [] });
      return;
    }

    const input = document.createElement("input");
    input.type = "file";
    input.multiple = Boolean(options.multiple);
    if (options.accept) {
      input.accept = options.accept;
    }
    input.style.display = "none";
    input.setAttribute("aria-hidden", "true");

    let resolved = false;
    const cleanup = () => {
      if (!resolved) {
        resolved = true;
        input.remove();
      }
    };

    input.onchange = () => {
      const fileList = input.files;
      const files = fileList ? Array.from(fileList) : [];
      cleanup();
      resolve({ paths: [], files });
    };

    input.oncancel = () => {
      cleanup();
      resolve({ paths: [], files: [] });
    };

    document.body.appendChild(input);
    input.click();
  });
}
