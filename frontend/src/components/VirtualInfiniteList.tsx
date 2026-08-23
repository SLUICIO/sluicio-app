// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A windowed (react-window) list that auto-loads the next page as you
// scroll near the bottom. Generic over the row type so logs, messages,
// and anything else with keyset pagination can share it. Rows are a
// fixed height and laid out on a shared CSS grid template so the header
// and body columns line up.
//
// # Why `fill` exists
//
// react-window needs its viewport in pixels, and a caller that puts this
// list in a flex column has no honest number to give it. Callers used to
// guess with `window.innerHeight - <a constant>`, one constant per page,
// measured once and never on resize. A guess that is too large does not
// merely look wrong: the list overflows its slot and paints underneath
// the card's own footer, so the last rows cannot be clicked at all. That
// was the Messages tab, where the chrome above the list (breadcrumbs,
// tabs, filter bar, the save-as-view hint) is taller than any constant
// anticipated.
//
// In `fill` mode the list measures the space it was actually given and
// re-measures when that changes. The measured box carries `min-height:0`
// and `overflow:hidden`, so its height is decided by the parent and can
// never be pushed back by the list's own content — measuring cannot feed
// itself. `height` stays the fallback for callers that genuinely sit in
// a page that scrolls, where there is no container height to measure.

import { ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
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
  // The list viewport height in px, used when `fill` is off or while a
  // fill-mode measurement is still pending.
  height?: number;
  // Size to the container instead of `height`. Requires an ancestor
  // chain with a definite height (a flex column with min-h-0), which is
  // what every card-with-footer layout in the app already is.
  fill?: boolean;
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
  fill = false,
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

  // In fill mode this tracks the measured body height. It starts at the
  // `height` prop so the first paint is close rather than empty.
  const bodyRef = useRef<HTMLDivElement | null>(null);
  const [measured, setMeasured] = useState(height);

  // A zero reading means the ancestor chain never resolved to a definite
  // height. Keep the last good value rather than collapsing the list to
  // nothing on a caller that turned `fill` on by mistake.
  const remeasure = useCallback(() => {
    const el = bodyRef.current;
    if (!el) return;
    const h = Math.round(el.getBoundingClientRect().height);
    if (h > 0) setMeasured((prev) => (prev === h ? prev : h));
  }, []);

  // Measured after every commit, deliberately with no dependency array.
  // A ResizeObserver alone is not enough: it is not merely absent in old
  // browsers, it can be PRESENT AND NEVER DELIVER — embedded and
  // headless webviews with no rendering lifecycle are the case we hit,
  // and there the list would silently keep the fallback height and the
  // bug this mode exists to fix would come back. A layout-effect
  // measurement runs on commit regardless, before the browser paints.
  // It costs one getBoundingClientRect per render and converges in a
  // single extra pass, because a measurement equal to the current state
  // sets nothing.
  useLayoutEffect(remeasure);

  useEffect(() => {
    if (!fill) return;
    window.addEventListener("resize", remeasure);
    // The observer is the addition rather than the mechanism: it catches
    // container changes that re-render nothing here, such as a sidebar
    // collapsing or a filter row wrapping to two lines.
    const el = bodyRef.current;
    const ro =
      el && typeof ResizeObserver !== "undefined"
        ? new ResizeObserver(remeasure)
        : null;
    ro?.observe(el!);
    return () => {
      window.removeEventListener("resize", remeasure);
      ro?.disconnect();
    };
  }, [fill, remeasure]);

  const listHeight = fill ? measured : height;

  const onItemsRendered = ({ visibleStopIndex }: { visibleStopIndex: number }) => {
    // Prefetch a little before the very end for a smooth scroll.
    if (hasMore && !loadingMore && visibleStopIndex >= items.length - 10) {
      loadMore();
    }
  };

  return (
    <div
      style={
        fill
          ? { height: "100%", minHeight: 0, display: "flex", flexDirection: "column" }
          : undefined
      }
    >
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

      {/* The measured box. `overflow: hidden` is load-bearing in fill
          mode, not cosmetic: without it the list's pixel height would
          feed back into the box's height and the observer would chase
          its own tail. */}
      <div
        ref={bodyRef}
        style={fill ? { flex: 1, minHeight: 0, overflow: "hidden" } : undefined}
      >
        {items.length === 0 ? (
          empty ?? <div className="placeholder">No results.</div>
        ) : (
          <FixedSizeList
            height={listHeight}
            itemCount={items.length}
            itemSize={rowHeight}
            width="100%"
            onItemsRendered={onItemsRendered}
            itemKey={(index) => itemKey(items[index], index)}
          >
            {Row}
          </FixedSizeList>
        )}
      </div>

      {loadingMore && (
        <div className="placeholder" style={{ padding: 8 }}>
          Loading more…
        </div>
      )}
    </div>
  );
}
