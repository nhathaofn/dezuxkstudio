import React, { useRef } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";

interface VirtualListProps<T> {
  items: T[];
  estimateSize: number;
  getItemKey: (item: T, index: number) => string | number;
  renderItem: (item: T, index: number) => React.ReactNode;
  className?: string;
  height?: string | number;
}

export function VirtualList<T>({
  items,
  estimateSize,
  getItemKey,
  renderItem,
  className = "",
  height = "320px",
}: VirtualListProps<T>) {
  const parentRef = useRef<HTMLDivElement>(null);

  // eslint-disable-next-line react-hooks/incompatible-library -- TanStack Virtual safely handles its own measurement cache
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => estimateSize,
    getItemKey: (index) => getItemKey(items[index], index),
    overscan: 5,
  });

  return (
    <div
      ref={parentRef}
      className={`virtual-list-container ${className}`}
      style={{
        height,
        overflowY: "auto",
        position: "relative",
      }}
    >
      <div
        style={{
          height: `${virtualizer.getTotalSize()}px`,
          width: "100%",
          position: "relative",
        }}
      >
        {virtualizer.getVirtualItems().map((virtualRow) => (
          <div
            key={virtualRow.key}
            data-index={virtualRow.index}
            ref={virtualizer.measureElement}
            style={{
              position: "absolute",
              top: 0,
              left: 0,
              width: "100%",
              transform: `translateY(${virtualRow.start}px)`,
            }}
          >
            {renderItem(items[virtualRow.index], virtualRow.index)}
          </div>
        ))}
      </div>
    </div>
  );
}
