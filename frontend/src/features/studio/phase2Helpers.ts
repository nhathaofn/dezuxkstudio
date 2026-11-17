export interface MaskStroke {
  size: number;
  points: { x: number; y: number }[];
}

/** Normalize weights into 0..100 percentages matching backend splitAxisSpans logic. */
export function computeGridDividerPercents(weights: number[]): number[] {
  const sum = weights.reduce((acc, w) => acc + (w > 0 ? w : 1), 0);
  if (sum <= 0 || weights.length <= 1) return [];
  const cuts: number[] = [];
  let acc = 0;
  for (let i = 0; i < weights.length - 1; i++) {
    acc += weights[i] > 0 ? weights[i] : 1;
    cuts.push(Math.round((acc / sum) * 1000) / 10);
  }
  return cuts;
}

/** Render a pure 1-bit B&W mask PNG DataURL (black preserved, white edited). */
export function buildMaskDataURLFromStrokes(
  width: number,
  height: number,
  strokes: MaskStroke[]
): string {
  const off = document.createElement("canvas");
  off.width = Math.max(1, Math.round(width));
  off.height = Math.max(1, Math.round(height));
  const ctx = off.getContext("2d");
  if (!ctx) return "";
  ctx.fillStyle = "#000000";
  ctx.fillRect(0, 0, off.width, off.height);
  ctx.strokeStyle = "#ffffff";
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  for (const s of strokes) {
    if (s.points.length === 0) continue;
    ctx.lineWidth = s.size;
    ctx.beginPath();
    ctx.moveTo(s.points[0].x, s.points[0].y);
    if (s.points.length === 1) {
      ctx.lineTo(s.points[0].x + 0.01, s.points[0].y + 0.01);
    } else {
      for (let i = 1; i < s.points.length; i++) {
        ctx.lineTo(s.points[i].x, s.points[i].y);
      }
    }
    ctx.stroke();
  }
  return off.toDataURL("image/png");
}
