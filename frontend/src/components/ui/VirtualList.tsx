/* eslint-disable react-refresh/only-export-components */
import React, {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import { appConfig } from "../../config/appConfig";

export const DEFAULT_ITEM_HEIGHT = appConfig.virtualization.defaultItemHeight;
export const DEFAULT_OVERSCAN = appConfig.virtualization.overscan;

export interface VirtualItem<T> {
  item: T;
  index: number;
  offset: number;
  size: number;
}

export interface VirtualRangeOptions<T> {
  items: readonly T[];
  itemHeight?: number | ((index: number, item: T) => number);
  scrollTop: number;
  viewportHeight: number;
  overscan?: number;
}

export interface VirtualRangeResult<T> {
  totalHeight: number;
  startIndex: number;
  endIndex: number;
  visibleItems: VirtualItem<T>[];
  offsets: number[];
  sizes: number[];
}

export interface VirtualLayout {
  totalHeight: number;
  offsets: number[];
  sizes: number[];
  isDynamic: boolean;
}

/** Precomputes prefix-sum offsets and sizes separately from scroll state so scroll is O(log N). */
export function computeVirtualLayout<T>(
  items: readonly T[],
  itemHeight?: number | ((index: number, item: T) => number)
): VirtualLayout {
  const count = items.length;
  if (count === 0) {
    return { totalHeight: 0, offsets: [], sizes: [], isDynamic: false };
  }
  const isDynamic = typeof itemHeight === "function";
  const offsets = new Array<number>(count);
  const sizes = new Array<number>(count);
  let totalHeight: number;

  if (isDynamic) {
    let runningOffset = 0;
    for (let i = 0; i < count; i++) {
      const sz = Math.max(1, Math.round(itemHeight(i, items[i])));
      offsets[i] = runningOffset;
      sizes[i] = sz;
      runningOffset += sz;
    }
    totalHeight = runningOffset;
  } else {
    const fixedHeight =
      typeof itemHeight === "number" && itemHeight > 0
        ? Math.round(itemHeight)
        : DEFAULT_ITEM_HEIGHT;
    for (let i = 0; i < count; i++) {
      offsets[i] = i * fixedHeight;
      sizes[i] = fixedHeight;
    }
    totalHeight = count * fixedHeight;
  }

  return { totalHeight, offsets, sizes, isDynamic };
}

/** Binary-searches a precomputed layout for the visible slice at scrollTop. */
export function sliceVirtualRange<T>(
  items: readonly T[],
  layout: VirtualLayout,
  scrollTop: number,
  viewportHeight: number,
  overscan?: number
): VirtualRangeResult<T> {
  const count = items.length;
  const { totalHeight, offsets, sizes, isDynamic } = layout;

  if (count === 0) {
    return { totalHeight: 0, startIndex: 0, endIndex: -1, visibleItems: [], offsets, sizes };
  }

  const overscanCount = typeof overscan === "number" && overscan >= 0 ? overscan : DEFAULT_OVERSCAN;

  if (viewportHeight <= 0) {
    const initialRenderLimit = Math.min(count - 1, Math.max(0, overscanCount * 2 + 10));
    const visibleItems: VirtualItem<T>[] = [];
    for (let i = 0; i <= initialRenderLimit; i++) {
      visibleItems.push({ item: items[i], index: i, offset: offsets[i], size: sizes[i] });
    }
    return {
      totalHeight,
      startIndex: 0,
      endIndex: initialRenderLimit,
      visibleItems,
      offsets,
      sizes,
    };
  }

  const clampedScrollTop = Math.max(0, scrollTop);
  const viewportBottom = clampedScrollTop + viewportHeight;

  let rawStart = 0;
  let rawEnd = count - 1;

  if (isDynamic) {
    let low = 0;
    let high = count - 1;
    while (low <= high) {
      const mid = (low + high) >> 1;
      if (offsets[mid] + sizes[mid] > clampedScrollTop) {
        rawStart = mid;
        high = mid - 1;
      } else {
        low = mid + 1;
      }
    }
    low = 0;
    high = count - 1;
    while (low <= high) {
      const mid = (low + high) >> 1;
      if (offsets[mid] < viewportBottom) {
        rawEnd = mid;
        low = mid + 1;
      } else {
        high = mid - 1;
      }
    }
  } else {
    const fixedHeight = sizes[0] ?? DEFAULT_ITEM_HEIGHT;
    rawStart = Math.floor(clampedScrollTop / fixedHeight);
    rawEnd = Math.floor(viewportBottom / fixedHeight);
  }

  const startIndex = Math.max(0, rawStart - overscanCount);
  const endIndex = Math.min(count - 1, Math.max(startIndex, rawEnd + overscanCount));

  const visibleItems: VirtualItem<T>[] = [];
  for (let i = startIndex; i <= endIndex; i++) {
    visibleItems.push({ item: items[i], index: i, offset: offsets[i], size: sizes[i] });
  }

  return { totalHeight, startIndex, endIndex, visibleItems, offsets, sizes };
}

/**
 * Pure calculation function for virtualized windowing.
 * Computes prefix-sum offsets, total height, and visible item slice with overscan.
 */
export function computeVirtualRange<T>(options: VirtualRangeOptions<T>): VirtualRangeResult<T> {
  const { items, itemHeight, scrollTop, viewportHeight, overscan } = options;
  const layout = computeVirtualLayout(items, itemHeight);
  return sliceVirtualRange(items, layout, scrollTop, viewportHeight, overscan);
}

export interface VirtualListHandle {
  /** Scroll to a specific item index with optional alignment and behavior. */
  scrollToIndex: (
    index: number,
    behavior?: ScrollBehavior,
    align?: "start" | "center" | "end" | "auto"
  ) => void;
  /** Scroll directly to a vertical pixel offset. */
  scrollToOffset: (offset: number, behavior?: ScrollBehavior) => void;
  /** Returns the current scroll top. */
  getScrollTop: () => number;
  /** Returns the total computed height of all items. */
  getTotalHeight: () => number;
  /** Returns the scroll container HTMLDivElement. */
  getContainer: () => HTMLDivElement | null;
}

export interface VirtualListProps<T> {
  /** Array of items to display. */
  items: readonly T[];
  /** Render function for each visible item. */
  renderItem: (item: T, index: number) => React.ReactNode;
  /** Fixed item height in px, or a function returning the height for item at index. */
  itemHeight?: number | ((index: number, item: T) => number);
  /** Alias for itemHeight (supports estimated or fixed row heights). */
  estimateSize?: number | ((index: number, item: T) => number);
  /** Extra rendered items outside the visible viewport. Defaults to appConfig.virtualization.overscan. */
  overscan?: number;
  /** Container height (e.g. 400, "100%", "360px"). Defaults to "100%". */
  height?: number | string;
  /** Container max-height constraint. */
  maxHeight?: number | string;
  /** Container CSS class name. */
  className?: string;
  /** Container inline styles. */
  style?: React.CSSProperties;
  /** Inner virtual container CSS class name. */
  innerClassName?: string;
  /** Inner virtual container inline styles. */
  innerStyle?: React.CSSProperties;
  /** Item key extractor. Defaults to index. */
  getItemKey?: (item: T, index: number) => string | number;
  /** Placeholder rendered when items array is empty. */
  emptyPlaceholder?: React.ReactNode;
  /** Callback fired whenever the container is scrolled. */
  onScroll?: (scrollTop: number, event: React.UIEvent<HTMLDivElement>) => void;
  /** Enables automatic ResizeObserver measurement for variable/wrapped row heights. */
  variableHeight?: boolean;
  /** Accessibility role attribute. Defaults to "feed". */
  role?: string;
  /** Accessibility label. */
  ariaLabel?: string;
}

function VirtualListInner<T>(
  props: VirtualListProps<T>,
  ref: React.ForwardedRef<VirtualListHandle>
) {
  const {
    items,
    renderItem,
    itemHeight,
    estimateSize,
    overscan = DEFAULT_OVERSCAN,
    height = "100%",
    maxHeight,
    className = "",
    style,
    innerClassName = "",
    innerStyle,
    getItemKey,
    emptyPlaceholder,
    onScroll,
    variableHeight = false,
    role = "feed",
    ariaLabel,
  } = props;

  const effectiveItemHeight = itemHeight ?? estimateSize ?? DEFAULT_ITEM_HEIGHT;

  const containerRef = useRef<HTMLDivElement | null>(null);
  const scrollRafRef = useRef<number | null>(null);
  const pendingScrollTopRef = useRef<number>(0);
  const rowObserverRef = useRef<ResizeObserver | null>(null);
  const [measuredHeights, setMeasuredHeights] = useState<Map<string | number, number>>(
    () => new Map()
  );
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState<number>(() => {
    if (typeof height === "number" && height > 0) return height;
    return 0;
  });

  useEffect(() => {
    if (!variableHeight || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => {
      setMeasuredHeights((prev) => {
        let next: Map<string | number, number> | null = null;
        for (const entry of entries) {
          const el = entry.target as HTMLElement;
          const rawKey = el.dataset.virtualMeasureKey;
          if (!rawKey) continue;
          const measured = Math.ceil(
            entry.borderBoxSize?.[0]?.blockSize ?? entry.contentRect.height
          );
          const cur = (next ?? prev).get(rawKey);
          if (measured > 0 && cur !== measured) {
            if (!next) next = new Map(prev);
            next.set(rawKey, measured);
          }
        }
        return next ?? prev;
      });
    });
    rowObserverRef.current = observer;
    return () => {
      observer.disconnect();
      rowObserverRef.current = null;
    };
  }, [variableHeight]);

  const attachRowMeasure = useCallback(
    (el: HTMLDivElement | null) => {
      if (!variableHeight || !el || !rowObserverRef.current) return;
      rowObserverRef.current.observe(el);
    },
    [variableHeight]
  );

  // Track viewport height changes on mount and resize
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const measure = () => {
      const clientHeight = el.clientHeight;
      if (clientHeight > 0) {
        setViewportHeight(clientHeight);
      }
    };

    measure();

    if (typeof ResizeObserver !== "undefined") {
      const observer = new ResizeObserver((entries) => {
        for (const entry of entries) {
          const h = Math.round(entry.contentRect.height);
          if (h > 0) {
            setViewportHeight(h);
          }
        }
      });
      observer.observe(el);
      return () => observer.disconnect();
    }

    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, [height]);

  useEffect(() => {
    return () => {
      if (scrollRafRef.current !== null && typeof cancelAnimationFrame === "function") {
        cancelAnimationFrame(scrollRafRef.current);
        scrollRafRef.current = null;
      }
    };
  }, []);

  const layout = useMemo(() => {
    if (!variableHeight) {
      return computeVirtualLayout(items, effectiveItemHeight);
    }
    const baseFn =
      typeof effectiveItemHeight === "function" ? effectiveItemHeight : () => effectiveItemHeight;
    const heightResolver = (index: number, item: T) => {
      const key = String(getItemKey ? getItemKey(item, index) : index);
      return measuredHeights.get(key) ?? baseFn(index, item);
    };
    return computeVirtualLayout(items, heightResolver);
  }, [items, effectiveItemHeight, variableHeight, getItemKey, measuredHeights]);

  const virtualRange = useMemo(() => {
    return sliceVirtualRange(items, layout, scrollTop, viewportHeight, overscan);
  }, [items, layout, scrollTop, viewportHeight, overscan]);

  const handleScroll = useCallback(
    (e: React.UIEvent<HTMLDivElement>) => {
      pendingScrollTopRef.current = e.currentTarget.scrollTop;
      if (onScroll) {
        onScroll(pendingScrollTopRef.current, e);
      }
      if (typeof requestAnimationFrame === "function") {
        if (scrollRafRef.current === null) {
          scrollRafRef.current = requestAnimationFrame(() => {
            scrollRafRef.current = null;
            setScrollTop(pendingScrollTopRef.current);
          });
        }
      } else {
        setScrollTop(pendingScrollTopRef.current);
      }
    },
    [onScroll]
  );

  const scrollToIndex = useCallback(
    (
      index: number,
      behavior: ScrollBehavior = "smooth",
      align: "start" | "center" | "end" | "auto" = "auto"
    ) => {
      const el = containerRef.current;
      if (!el || items.length === 0) return;
      const targetIndex = Math.max(0, Math.min(items.length - 1, index));
      const targetOffset = virtualRange.offsets[targetIndex] ?? 0;
      const targetSize = virtualRange.sizes[targetIndex] ?? DEFAULT_ITEM_HEIGHT;
      const currentViewport = el.clientHeight || viewportHeight || DEFAULT_ITEM_HEIGHT * 5;

      let destination = targetOffset;
      if (align === "center") {
        destination = targetOffset - (currentViewport - targetSize) / 2;
      } else if (align === "end") {
        destination = targetOffset - currentViewport + targetSize;
      } else if (align === "auto") {
        const currentTop = el.scrollTop;
        const currentBottom = currentTop + currentViewport;
        if (targetOffset >= currentTop && targetOffset + targetSize <= currentBottom) {
          return; // Already fully in viewport
        }
        if (targetOffset < currentTop) {
          destination = targetOffset;
        } else {
          destination = targetOffset - currentViewport + targetSize;
        }
      }

      const maxScroll = Math.max(0, virtualRange.totalHeight - currentViewport);
      const clampedDestination = Math.max(0, Math.min(maxScroll, Math.round(destination)));
      el.scrollTo({ top: clampedDestination, behavior });
    },
    [
      items.length,
      virtualRange.offsets,
      virtualRange.sizes,
      virtualRange.totalHeight,
      viewportHeight,
    ]
  );

  const scrollToOffset = useCallback((offset: number, behavior: ScrollBehavior = "smooth") => {
    const el = containerRef.current;
    if (!el) return;
    el.scrollTo({ top: Math.max(0, offset), behavior });
  }, []);

  useImperativeHandle(
    ref,
    () => ({
      scrollToIndex,
      scrollToOffset,
      getScrollTop: () => containerRef.current?.scrollTop ?? scrollTop,
      getTotalHeight: () => virtualRange.totalHeight,
      getContainer: () => containerRef.current,
    }),
    [scrollToIndex, scrollToOffset, scrollTop, virtualRange.totalHeight]
  );

  if (items.length === 0 && emptyPlaceholder) {
    return <>{emptyPlaceholder}</>;
  }

  return (
    <div
      ref={containerRef}
      className={`virtual-list ${className}`.trim()}
      onScroll={handleScroll}
      role={role}
      aria-label={ariaLabel}
      tabIndex={0}
      style={{
        position: "relative",
        overflowY: "auto",
        overflowX: "hidden",
        height: typeof height === "number" ? `${height}px` : height,
        maxHeight: typeof maxHeight === "number" ? `${maxHeight}px` : maxHeight,
        boxSizing: "border-box",
        ...style,
      }}
    >
      <div
        className={`virtual-list__inner ${innerClassName}`.trim()}
        style={{
          position: "relative",
          height: `${virtualRange.totalHeight}px`,
          width: "100%",
          pointerEvents: "auto",
          ...innerStyle,
        }}
      >
        {virtualRange.visibleItems.map(({ item, index, offset, size }) => {
          const itemKey = getItemKey ? getItemKey(item, index) : index;
          return (
            <div
              key={itemKey}
              ref={variableHeight ? attachRowMeasure : undefined}
              data-virtual-index={index}
              data-virtual-measure-key={variableHeight ? String(itemKey) : undefined}
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                ...(variableHeight ? { minHeight: `${size}px` } : { height: `${size}px` }),
                transform: `translateY(${offset}px)`,
                boxSizing: "border-box",
              }}
            >
              {renderItem(item, index)}
            </div>
          );
        })}
      </div>
    </div>
  );
}

export const VirtualList = forwardRef(VirtualListInner) as <T>(
  props: VirtualListProps<T> & { ref?: React.ForwardedRef<VirtualListHandle> }
) => React.ReactElement;

export default VirtualList;
