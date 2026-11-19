import React, { useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import { useSignedMediaURL } from "../../lib/hooks/useSignedMediaURL";

function useMediaInViewport(lazy: boolean): {
  containerRef: React.RefObject<HTMLDivElement>;
  inView: boolean;
} {
  const containerRef = useRef<HTMLDivElement>(null);
  const [observedInView, setObservedInView] = useState(
    () => !lazy || typeof IntersectionObserver === "undefined"
  );

  useEffect(() => {
    if (!lazy || typeof IntersectionObserver === "undefined") {
      return;
    }
    const el = containerRef.current;
    if (!el) return;
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (entry) {
          setObservedInView(entry.isIntersecting);
        }
      },
      { rootMargin: "120px" }
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [lazy]);

  return {
    containerRef,
    inView: !lazy || typeof IntersectionObserver === "undefined" || observedInView,
  };
}

function useDecoderCleanup(
  mediaRef: React.RefObject<HTMLMediaElement | null>,
  activeSrc: string | null
) {
  useEffect(() => {
    const el = mediaRef.current;
    return () => {
      if (!el) return;
      el.pause();
      el.removeAttribute("src");
      if (typeof el.load === "function") {
        el.load();
      }
    };
  }, [mediaRef, activeSrc]);
}

export const SignedStudioImage: React.FC<{
  assetId?: string;
  alt: string;
  placeholder: string;
}> = ({ assetId, alt, placeholder }) => {
  const { url, loading, handleMediaError } = useSignedMediaURL(assetId || null, "preview");

  if (!assetId) {
    return <div className="studio-media-empty">{placeholder}</div>;
  }
  if (loading && !url) {
    return (
      <div className="studio-media-empty">
        <Loader2 size={18} className="animate-spin" />
      </div>
    );
  }
  if (!url) {
    return <div className="studio-media-empty">{placeholder}</div>;
  }
  return (
    <img
      className="studio-media-img"
      src={url}
      alt={alt}
      loading="lazy"
      onError={handleMediaError}
    />
  );
};

export const SignedStudioVideo: React.FC<{
  assetId?: string;
  placeholder: string;
  large?: boolean;
  lazy?: boolean;
}> = ({ assetId, placeholder, large, lazy = true }) => {
  const { containerRef, inView } = useMediaInViewport(lazy && Boolean(assetId));
  const { url, loading, handleMediaError, mediaRef } = useSignedMediaURL(
    assetId && inView ? assetId : null,
    "preview"
  );
  useDecoderCleanup(mediaRef, url);

  if (!assetId) {
    return (
      <div
        className={large ? "studio-media-empty studio-media-empty--large" : "studio-media-empty"}
      >
        {placeholder}
      </div>
    );
  }

  return (
    <div ref={containerRef} style={{ width: "100%", height: "100%" }}>
      {!inView || (loading && !url) ? (
        <div
          className={large ? "studio-media-empty studio-media-empty--large" : "studio-media-empty"}
        >
          <Loader2 size={20} className="animate-spin" />
        </div>
      ) : !url ? (
        <div
          className={large ? "studio-media-empty studio-media-empty--large" : "studio-media-empty"}
        >
          {placeholder}
        </div>
      ) : (
        <video
          ref={mediaRef as React.RefObject<HTMLVideoElement>}
          className={large ? "studio-media-video studio-media-video--large" : "studio-media-video"}
          src={url}
          controls
          preload="metadata"
          onError={handleMediaError}
        />
      )}
    </div>
  );
};

export const SignedStudioAudio: React.FC<{ assetId?: string; lazy?: boolean }> = ({
  assetId,
  lazy = true,
}) => {
  const { containerRef, inView } = useMediaInViewport(lazy && Boolean(assetId));
  const { url, handleMediaError, mediaRef } = useSignedMediaURL(
    assetId && inView ? assetId : null,
    "preview"
  );
  useDecoderCleanup(mediaRef, url);

  if (!assetId) return null;
  return (
    <div ref={containerRef}>
      {inView && url ? (
        <audio
          ref={mediaRef as React.RefObject<HTMLAudioElement>}
          className="studio-media-audio"
          src={url}
          controls
          preload="metadata"
          onError={handleMediaError}
        />
      ) : null}
    </div>
  );
};
