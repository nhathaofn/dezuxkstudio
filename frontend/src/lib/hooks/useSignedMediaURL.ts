import { useCallback, useEffect, useRef, useState } from "react";
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
  private attachedMedia: HTMLMediaElement | null = null;

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

    if (!trimmedId) {
      this.clearRenewTimer();
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

  public async renew(): Promise<string | null> {
    if (this.disposed) return null;
    if (!this.currentAssetId) {
      this.updateState({ url: null, ticket: null, expiresAt: null, loading: false, error: null });
      return null;
    }

    if (this.inFlightPromise) {
      return this.inFlightPromise;
    }

    this.updateState({ loading: true, error: null });

    const promise = (async () => {
      try {
        const result = await signMediaURL({
          asset_id: this.currentAssetId!,
          purpose: this.purpose,
          ttl_seconds: this.ttlSeconds,
        });

        if (this.disposed) return null;

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
          const delayMs = Math.max(1000, expMs - now - this.skewMs);
          this.renewTimer = setTimeout(() => {
            if (!this.disposed) {
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
                restore();
                el.removeEventListener?.("loadedmetadata", onLoaded);
              };
              el.addEventListener("loadedmetadata", onLoaded);
            }
          }
        }

        return result.url;
      } catch (err) {
        if (this.disposed) return null;
        const errObj = err instanceof Error ? err : new Error(String(err));
        this.updateState({ loading: false, error: errObj });
        return null;
      } finally {
        this.inFlightPromise = null;
      }
    })();

    this.inFlightPromise = promise;
    return promise;
  }

  public handleMediaError(mediaElement?: HTMLElement | null): void {
    const el = (mediaElement as HTMLMediaElement | null) || this.attachedMedia;
    if (el) {
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
    this.clearRenewTimer();
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
    return session.subscribe(setState);
  }, [session]);

  useEffect(() => {
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
