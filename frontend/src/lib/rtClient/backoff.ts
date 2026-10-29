export interface BackoffOptions {
  baseDelayMs: number;
  maxDelayMs: number;
  factor: number;
}

/**
 * Exponential backoff with "full jitter": a uniformly random delay in
 * [0, min(max, base * factor^attempt)], which spreads reconnect storms.
 */
export function backoffDelay(
  attempt: number,
  o: BackoffOptions,
  random: () => number = Math.random
): number {
  const cap = Math.min(o.maxDelayMs, o.baseDelayMs * Math.pow(o.factor, Math.max(0, attempt)));
  return Math.floor(random() * cap);
}
