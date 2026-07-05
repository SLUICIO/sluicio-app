// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A windowed (react-window) list that auto-loads the next page as you
// scroll near the bottom. Generic over the row type so logs, messages,
// and anything else with keyset pagination can share it. Rows are a
// fixed height and laid out on a shared CSS grid template so the header
// and body columns line up.

import { ReactNode } from "react";
import { FixedSizeList, ListChildComponentProps } from "react-window";

interface Props<T> {
  items: T[];
  // hasMore + loadMore drive the infinite scroll. loadMore is called
  // once when the viewport nears the end and another page exists.
  hasMore: boolean;
  loadingMore: boolean;
  loadMore: () => void;
  // gridTemplate is a CSS grid-template-columns value shared by the
  // header and every row so columns align.
  gridTemplate: string;
  header: ReactNode;
  renderRow: (item: T, index: number) => ReactNode;
  itemKey: (item: T, index: number) => string;
  rowHeight?: number;
  height?: number;
  empty?: ReactNode;
  // Optional per-row class (severity tints, selection) and click
  // handler (open a details drawer, etc.).
  rowClassName?: (item: T, index: number) => string;
  onRowClick?: (item: T, index: number) => void;
}

export default function VirtualInfiniteList<T>({
  items,
  hasMore,
  loadingMore,
  loadMore,
  gridTemplate,
  header,
  renderRow,
  itemKey,
  rowHeight = 36,
  height = 560,
  empty,
  rowClassName,
  onRowClick,
}: Props<T>) {
  const Row = ({ index, style }: ListChildComponentProps) => (
    <div
      className={rowClassName ? rowClassName(items[index], index) : undefined}
      onClick={onRowClick ? () => onRowClick(items[index], index) : undefined}
      style={{
        ...style,
        display: "grid",
        gridTemplateColumns: gridTemplate,
        alignItems: "center",
        gap: 12,
        padding: "0 12px",
        borderBottom: "1px solid var(--border)",
        fontSize: 13,
      }}
    >
      {renderRow(items[index], index)}
    </div>
  );

  const onItemsRendered = ({ visibleStopIndex }: { visibleStopIndex: number }) => {
    // Prefetch a little before the very end for a smooth scroll.
    if (hasMore && !loadingMore && visibleStopIndex >= items.length - 10) {
      loadMore();
    }
  };

  return (
    <div>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: gridTemplate,
          gap: 12,
          padding: "6px 12px",
          fontWeight: 600,
          fontSize: 12,
          color: "var(--muted)",
          borderBottom: "1px solid var(--border)",
        }}
      >
        {header}
      </div>

      {items.length === 0 ? (
        empty ?? <div className="placeholder">No results.</div>
      ) : (
        <FixedSizeList
          height={height}
          itemCount={items.length}
          itemSize={rowHeight}
          width="100%"
          onItemsRendered={onItemsRendered}
          itemKey={(index) => itemKey(items[index], index)}
        >
          {Row}
        </FixedSizeList>
      )}

      {loadingMore && (
        <div className="placeholder" style={{ padding: 8 }}>
          Loading more…
        </div>
      )}
    </div>
  );
}
