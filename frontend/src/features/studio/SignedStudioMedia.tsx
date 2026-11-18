import React from "react";
import { Loader2 } from "lucide-react";
import { useSignedMediaURL } from "../../lib/hooks/useSignedMediaURL";

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
}> = ({ assetId, placeholder, large }) => {
  const { url, loading, handleMediaError, mediaRef } = useSignedMediaURL(
    assetId || null,
    "preview"
  );

  if (!assetId) {
    return (
      <div
        className={large ? "studio-media-empty studio-media-empty--large" : "studio-media-empty"}
      >
        {placeholder}
      </div>
    );
  }
  if (loading && !url) {
    return (
      <div
        className={large ? "studio-media-empty studio-media-empty--large" : "studio-media-empty"}
      >
        <Loader2 size={20} className="animate-spin" />
      </div>
    );
  }
  if (!url) {
    return (
      <div
        className={large ? "studio-media-empty studio-media-empty--large" : "studio-media-empty"}
      >
        {placeholder}
      </div>
    );
  }

  return (
    <video
      ref={mediaRef as React.RefObject<HTMLVideoElement>}
      className={large ? "studio-media-video studio-media-video--large" : "studio-media-video"}
      src={url}
      controls
      preload="metadata"
      onError={handleMediaError}
    />
  );
};

export const SignedStudioAudio: React.FC<{ assetId?: string }> = ({ assetId }) => {
  const { url, handleMediaError, mediaRef } = useSignedMediaURL(assetId || null, "preview");
  if (!assetId || !url) return null;
  return (
    <audio
      ref={mediaRef as React.RefObject<HTMLAudioElement>}
      className="studio-media-audio"
      src={url}
      controls
      preload="metadata"
      onError={handleMediaError}
    />
  );
};
