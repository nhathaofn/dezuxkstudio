import React, { useEffect, useRef } from "react";
import { appConfig } from "../../../config/appConfig";

export const WaveformCanvas: React.FC<{ peaks: number[] }> = React.memo(({ peaks }) => {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const {
    waveformWidthStepPx,
    waveformHeightPx,
    waveformBarMinHeightPx,
    waveformBarMaxHeightPx,
    waveformBarWidthPx,
  } = appConfig.studio.timeline;
  const width = Math.max(1, peaks.length * waveformWidthStepPx);
  const height = waveformHeightPx;

  useEffect(() => {
    const cvs = canvasRef.current;
    if (!cvs) return;
    const ctx = cvs.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, width, height);
    const computedColor =
      typeof window !== "undefined"
        ? getComputedStyle(cvs).getPropertyValue("--timeline-waveform-bar").trim() ||
          getComputedStyle(document.documentElement).getPropertyValue("--accent").trim()
        : "";
    ctx.fillStyle = computedColor || "currentColor";
    for (let idx = 0; idx < peaks.length; idx++) {
      const h = Math.max(waveformBarMinHeightPx, Math.round(peaks[idx] * waveformBarMaxHeightPx));
      ctx.fillRect(idx * waveformWidthStepPx, height - h, waveformBarWidthPx, h);
    }
  }, [
    peaks,
    width,
    height,
    waveformWidthStepPx,
    waveformBarMinHeightPx,
    waveformBarMaxHeightPx,
    waveformBarWidthPx,
  ]);

  return (
    <canvas
      ref={canvasRef}
      className="timeline-clip-waveform"
      width={width}
      height={height}
      aria-hidden="true"
    />
  );
});
