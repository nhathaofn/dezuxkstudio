import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { appConfig } from "../../config/appConfig";
import { signMediaURL } from "../rtClient/media";
import type { ActionError } from "../rtClient/errors";

export interface UseSignedMediaURLOptions {
  /** Override the default purpose ("preview"). */
  purpose?: string;
  /** Whether to automatically renew the ticket before expiration. Default is true. */
  autoRenew?: boolean;
  /** Custom clock skew in milliseconds before expiration to trigger renewal. Defaults to appConfig.media.ticketRenewSkewMs. */
  skewMs?: number;
  /** Optional custom TTL in seconds to request from the server. */
  ttlSeconds?: number;
}

export interface SignedMediaState {
  /** The fully-qualified signed streaming/download URL, or null if not yet signed or no assetId. */
  url: string | null;
  /** The scoped HMAC ticket string from the server. */
  ticket: string | null;
  /** Millisecond timestamp when the ticket expires. */
  expiresAt: number | null;
  /** Whether a sign/renew request is currently in flight. */
  loading: boolean;
  /** Error encountered while requesting the signed ticket, if any. */
  error: ActionError | Error | null;
}

export interface UseSignedMediaURLResult extends SignedMediaState {
  /** Manually triggers ticket renewal. */
  renew: () => Promise<string | null>;
  /** Error handler to attach directly to <video onError={handleMediaError}> or <audio onError={handleMediaError}>. */
  handleMediaError: (event?: React.SyntheticEvent<HTMLElement> | Event) => void;
  /** Ref to attach to <video> or <audio> element to enable automated playback recovery at currentTime. */
  mediaRef: React.RefObject<HTMLMediaElement | null>;
}

/**
 * SignedMediaSession encapsulates in-memory ticket lifecycle management:
 * - Scoped ticket acquisition on demand (never in persistent storage)
 * - Proactive renewal prior to expiration based on clock skew
 * - Automatic recovery of media playback at currentTime on ticket expiration
 */
export class SignedMediaSession {
  private state: SignedMediaState = {
    url: null,
    ticket: null,
    expiresAt: null,
    loading: false,
    error: null,
  };

  private listeners = new Set<(state: SignedMediaState) => void>();
  private renewTimer: ReturnType<typeof setTimeout> | null = null;
  private currentAssetId: string | null = null;
  private purpose: string = "preview";
  private ttlSeconds?: number;
  private autoRenew: boolean = true;
  private skewMs: number = appConfig.media.ticketRenewSkewMs;
  private disposed: boolean = false;
  private inFlightPromise: Promise<string | null> | null = null;
  private inFlightKey: string | null = null;
  private inFlightSeq: number = 0;
  private attachedMedia: HTMLMediaElement | null = null;
  private pendingLoadedCleanup: (() => void) | null = null;
  private mediaErrorRetries: number = 0;

  private recovery: {
    currentTime: number;
    wasPlaying: boolean;
    mediaElement: HTMLMediaElement | null;
  } | null = null;

  constructor(options?: UseSignedMediaURLOptions) {
    if (options) {
      if (options.purpose !== undefined) this.purpose = options.purpose;
      if (options.autoRenew !== undefined) this.autoRenew = options.autoRenew;
      if (options.skewMs !== undefined) this.skewMs = options.skewMs;
      if (options.ttlSeconds !== undefined) this.ttlSeconds = options.ttlSeconds;
    }
  }

  public getState(): SignedMediaState {
    return this.state;
  }

  public subscribe(listener: (state: SignedMediaState) => void): () => void {
    this.listeners.add(listener);
    listener(this.state);
    return () => {
      this.listeners.delete(listener);
    };
  }

  private notify() {
    if (this.disposed) return;
    for (const listener of this.listeners) {
      listener(this.state);
    }
  }

  public setMediaElement(el: HTMLMediaElement | null) {
    this.attachedMedia = el;
  }

  public configure(assetId: string | null | undefined, options?: UseSignedMediaURLOptions): void {
    if (this.disposed) return;

    const trimmedId = assetId && assetId.trim() ? assetId.trim() : null;
    const nextPurpose = options?.purpose ?? "preview";
    const nextAutoRenew = options?.autoRenew !== false;
    const nextSkewMs = options?.skewMs ?? appConfig.media.ticketRenewSkewMs;
    const nextTtl = options?.ttlSeconds;

    const changed =
      this.currentAssetId !== trimmedId ||
      this.purpose !== nextPurpose ||
      this.autoRenew !== nextAutoRenew ||
      this.skewMs !== nextSkewMs ||
      this.ttlSeconds !== nextTtl;

    this.currentAssetId = trimmedId;
    this.purpose = nextPurpose;
    this.autoRenew = nextAutoRenew;
    this.skewMs = nextSkewMs;
    this.ttlSeconds = nextTtl;

    if (changed) {
      this.mediaErrorRetries = 0;
      this.clearPendingLoadedListener();
    }

    if (!trimmedId) {
      this.clearRenewTimer();
      this.clearPendingLoadedListener();
      this.updateState({
        url: null,
        ticket: null,
        expiresAt: null,
        loading: false,
        error: null,
      });
      return;
    }

    if (changed || (!this.state.url && !this.state.loading)) {
      void this.renew();
    }
  }

  private clearPendingLoadedListener() {
    if (this.pendingLoadedCleanup !== null) {
      this.pendingLoadedCleanup();
      this.pendingLoadedCleanup = null;
    }
  }

  public clearRenewTimer() {
    if (this.renewTimer !== null) {
      clearTimeout(this.renewTimer);
      this.renewTimer = null;
    }
  }

  private updateState(partial: Partial<SignedMediaState>) {
    this.state = { ...this.state, ...partial };
    this.notify();
  }

  /** React StrictMode remounts after simulated unmount: restore session from disposed state cleanly. */
  public revive(): void {
    if (!this.disposed) return;
    this.disposed = false;
    this.mediaErrorRetries = 0;
    this.inFlightPromise = null;
    this.inFlightKey = null;
  }

  public async renew(): Promise<string | null> {
    if (this.disposed) return null;
    const requestedId = this.currentAssetId;
    const requestedPurpose = this.purpose;
    const requestedTtl = this.ttlSeconds;
    if (!requestedId) {
      this.inFlightPromise = null;
      this.inFlightKey = null;
      this.updateState({ url: null, ticket: null, expiresAt: null, loading: false, error: null });
      return null;
    }

    const reqKey = `${requestedId}:${requestedPurpose}:${requestedTtl ?? ""}`;
    if (this.inFlightPromise && this.inFlightKey === reqKey) {
      return this.inFlightPromise;
    }

    this.updateState({ loading: true, error: null });

    const reqSeq = ++this.inFlightSeq;
    const promise: Promise<string | null> = (async () => {
      try {
        const result = await signMediaURL({
          asset_id: requestedId,
          purpose: requestedPurpose,
          ttl_seconds: requestedTtl,
        });

        if (
          this.disposed ||
          this.currentAssetId !== requestedId ||
          this.purpose !== requestedPurpose ||
          this.ttlSeconds !== requestedTtl
        ) {
          return null;
        }

        // Convert seconds to milliseconds if timestamp is Unix seconds (< 1e11)
        const expMs = result.expires_at < 1e11 ? result.expires_at * 1000 : result.expires_at;

        this.updateState({
          url: result.url,
          ticket: result.ticket,
          expiresAt: expMs,
          loading: false,
          error: null,
        });

        // Schedule proactive renewal before expiration
        if (this.autoRenew) {
          this.clearRenewTimer();
          const now = Date.now();
          const delayMs = Math.max(
            appConfig.media.minTicketRenewDelayMs,
            expMs - now - this.skewMs
          );
          this.renewTimer = setTimeout(() => {
            if (!this.disposed && this.currentAssetId === requestedId) {
              this.mediaErrorRetries = 0;
              void this.renew();
            }
          }, delayMs);
        }

        // Check if we need to recover playback after expired ticket error
        if (this.recovery) {
          const rec = this.recovery;
          this.recovery = null;
          const el = rec.mediaElement || this.attachedMedia;
          if (el) {
            this.clearPendingLoadedListener();
            const restore = () => {
              try {
                if (rec.currentTime > 0) {
                  el.currentTime = rec.currentTime;
                }
                if (rec.wasPlaying) {
                  void el.play?.().catch?.(() => {});
                }
              } catch {
                // Ignore seek issues
              }
            };

            if (el.readyState >= 1) {
              restore();
            } else if (typeof el.addEventListener === "function") {
              const onLoaded = () => {
                this.pendingLoadedCleanup = null;
                el.removeEventListener?.("loadedmetadata", onLoaded);
                restore();
              };
              this.pendingLoadedCleanup = () => {
                el.removeEventListener?.("loadedmetadata", onLoaded);
              };
              el.addEventListener("loadedmetadata", onLoaded);
            }
          }
        }

        return result.url;
      } catch (err) {
        if (
          this.disposed ||
          this.currentAssetId !== requestedId ||
          this.purpose !== requestedPurpose ||
          this.ttlSeconds !== requestedTtl
        ) {
          return null;
        }
        const errObj = err instanceof Error ? err : new Error(String(err));
        this.updateState({ loading: false, error: errObj });
        return null;
      } finally {
        if (this.inFlightSeq === reqSeq) {
          this.inFlightPromise = null;
          this.inFlightKey = null;
        }
      }
    })();

    this.inFlightPromise = promise;
    this.inFlightKey = reqKey;
    return promise;
  }

  public handleMediaError(mediaElement?: HTMLElement | null): void {
    if (this.mediaErrorRetries >= appConfig.media.maxMediaErrorRetries) {
      return;
    }
    this.mediaErrorRetries += 1;
    const el = (mediaElement as HTMLMediaElement | null) || this.attachedMedia;
    if (el && typeof el.currentTime === "number") {
      this.recovery = {
        currentTime: el.currentTime || 0,
        wasPlaying: !el.paused && !el.ended,
        mediaElement: el,
      };
    }
    void this.renew();
  }

  public dispose(): void {
    this.disposed = true;
    this.inFlightPromise = null;
    this.inFlightKey = null;
    this.clearRenewTimer();
    this.clearPendingLoadedListener();
    this.listeners.clear();
    this.attachedMedia = null;
    this.recovery = null;
  }
}

/**
 * React hook to request and maintain a signed streaming/download URL for media assets.
 * Calls media:sign_url only on demand, never stores tickets in localStorage,
 * proactively renews before expiry, and recovers playback at currentTime when ticket expires.
 */
export function useSignedMediaURL(
  assetId: string | null | undefined,
  purposeOrOptions: string | UseSignedMediaURLOptions = "preview"
): UseSignedMediaURLResult {
  const purpose =
    typeof purposeOrOptions === "string" ? purposeOrOptions : purposeOrOptions.purpose;
  const autoRenew = typeof purposeOrOptions === "string" ? undefined : purposeOrOptions.autoRenew;
  const skewMs = typeof purposeOrOptions === "string" ? undefined : purposeOrOptions.skewMs;
  const ttlSeconds = typeof purposeOrOptions === "string" ? undefined : purposeOrOptions.ttlSeconds;

  const [session] = useState(
    () => new SignedMediaSession({ purpose, autoRenew, skewMs, ttlSeconds })
  );
  const [state, setState] = useState<SignedMediaState>(() => session.getState());
  const mediaRef = useRef<HTMLMediaElement | null>(null);

  useEffect(() => {
    session.setMediaElement(mediaRef.current);
  });

  useEffect(() => {
    session.revive();
    return session.subscribe(setState);
  }, [session]);

  useEffect(() => {
    session.revive();
    session.configure(assetId, { purpose, autoRenew, skewMs, ttlSeconds });
  }, [session, assetId, purpose, autoRenew, skewMs, ttlSeconds]);

  useEffect(() => {
    return () => {
      session.dispose();
    };
  }, [session]);

  const handleMediaError = useCallback(
    (event?: React.SyntheticEvent<HTMLElement> | Event) => {
      const el =
        (event?.currentTarget as HTMLMediaElement | null) ||
        (event?.target as HTMLMediaElement | null) ||
        mediaRef.current;
      session.handleMediaError(el);
    },
    [session]
  );

  const renew = useCallback(() => session.renew(), [session]);

  return {
    url: state.url,
    ticket: state.ticket,
    expiresAt: state.expiresAt,
    loading: state.loading,
    error: state.error,
    renew,
    handleMediaError,
    mediaRef,
  };
}

export interface UseSignedMediaURLsResult {
  urls: Record<string, string>;
  handleAssetMediaError: (assetId?: string | null, mediaElement?: HTMLElement | null) => void;
}

/**
 * Maintains SignedMediaSession instances for a dynamic set of asset IDs (e.g. multi-track timelines),
 * providing proactive ticket renewal, bounded media error recovery, and deterministic unmount cleanup.
 */
export function useSignedMediaURLs(
  assetIds: readonly string[],
  purpose = "preview"
): UseSignedMediaURLsResult {
  const sessionsRef = useRef<Map<string, { session: SignedMediaSession; unsubscribe: () => void }>>(
    new Map()
  );
  const [rawUrls, setRawUrls] = useState<Record<string, string>>({});

  useEffect(() => {
    const map = sessionsRef.current;
    const wanted = new Set(assetIds.map((id) => id.trim()).filter(Boolean));

    for (const [id, entry] of map.entries()) {
      if (!wanted.has(id)) {
        entry.unsubscribe();
        entry.session.dispose();
        map.delete(id);
      }
    }

    for (const id of wanted) {
      const existing = map.get(id);
      if (existing) {
        existing.session.configure(id, { purpose });
        continue;
      }
      const session = new SignedMediaSession({ purpose });
      session.configure(id, { purpose });
      const unsubscribe = session.subscribe((s) => {
        if (s.url) {
          setRawUrls((prev) => (prev[id] === s.url ? prev : { ...prev, [id]: s.url! }));
        }
      });
      map.set(id, { session, unsubscribe });
    }
  }, [assetIds, purpose]);

  useEffect(() => {
    const map = sessionsRef.current;
    return () => {
      for (const entry of map.values()) {
        entry.unsubscribe();
        entry.session.dispose();
      }
      map.clear();
    };
  }, []);

  const urls = useMemo(() => {
    const out: Record<string, string> = {};
    for (const rawId of assetIds) {
      const id = rawId.trim();
      if (id && rawUrls[id]) {
        out[id] = rawUrls[id];
      }
    }
    return out;
  }, [assetIds, rawUrls]);

  const handleAssetMediaError = useCallback(
    (assetId?: string | null, mediaElement?: HTMLElement | null) => {
      const id = assetId?.trim();
      if (!id) return;
      sessionsRef.current.get(id)?.session.handleMediaError(mediaElement);
    },
    []
  );

  return { urls, handleAssetMediaError };
}
