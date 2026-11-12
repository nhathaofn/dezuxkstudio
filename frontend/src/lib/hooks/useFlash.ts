import { useCallback, useEffect, useState } from "react";
import { Flash } from "./flash";

/**
 * Returns [value, show, clear]: show(v) sets value and resets it to null after `ms`.
 * The pending reset is cancelled on unmount, so no state update hits an unmounted component.
 */
export function useFlash<T = true>(ms: number): [T | null, (value: T) => void, () => void] {
  const [value, setValue] = useState<T | null>(null);
  // Created once; setValue is stable, so the Flash never needs recreating.
  const [flash] = useState(() => new Flash<T>(ms, setValue));
  useEffect(() => () => flash.dispose(), [flash]);
  const show = useCallback((v: T) => flash.show(v), [flash]);
  const clear = useCallback(() => flash.clear(), [flash]);
  return [value, show, clear];
}
