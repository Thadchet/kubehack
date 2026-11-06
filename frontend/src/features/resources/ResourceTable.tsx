import { useVirtualizer } from "@tanstack/react-virtual";
import { useEffect, useMemo, useRef, useState } from "react";
import type { Column } from "./columns";
import { objectKey, type KubeObject } from "./useResourceWatch";

const rowHeight = 30;
const clockInterval = 30000;

interface Sort {
  column: string;
  descending: boolean;
}

interface Props {
  columns: Column[];
  rows: KubeObject[];
  selected?: string;
  onSelect: (key: string) => void;
  sort: Sort;
  onSort: (sort: Sort) => void;
}

export function sortRows(rows: KubeObject[], columns: Column[], sort: Sort): KubeObject[] {
  const column = columns.find((item) => item.id === sort.column) ?? columns[0];
  const value = column.sortValue ?? ((item: KubeObject) => column.text(item, 0));
  const direction = sort.descending ? -1 : 1;
  // Keys are computed once, because comparing recomputes them O(n log n) times.
  return rows
    .map((item) => ({ item, key: value(item) }))
    .sort((a, b) => {
      const order =
        typeof a.key === "number" && typeof b.key === "number"
          ? a.key - b.key
          : String(a.key).localeCompare(String(b.key), undefined, { numeric: true });
      // The tie-break keeps rows from swapping places between updates.
      return order * direction || objectKey(a.item).localeCompare(objectKey(b.item));
    })
    .map(({ item }) => item);
}

export function ResourceTable({ columns, rows, selected, onSelect, sort, onSort }: Props) {
  const scroller = useRef<HTMLDivElement>(null);
  const [now, setNow] = useState(Date.now);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), clockInterval);
    return () => window.clearInterval(timer);
  }, []);

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => rowHeight,
    overscan: 12,
  });

  const selectedIndex = useMemo(
    () => rows.findIndex((item) => objectKey(item) === selected),
    [rows, selected],
  );

  useEffect(() => {
    if (selectedIndex >= 0) {
      virtualizer.scrollToIndex(selectedIndex, { align: "auto" });
    }
  }, [selectedIndex, virtualizer]);

  const tracks = columns.map((column) => column.width).join(" ");

  return (
    <div className="table" role="grid" aria-rowcount={rows.length}>
      <div className="table-head" role="row" style={{ gridTemplateColumns: tracks }}>
        {columns.map((column) => {
          const active = sort.column === column.id;
          return (
            <button
              key={column.id}
              type="button"
              role="columnheader"
              aria-sort={active ? (sort.descending ? "descending" : "ascending") : "none"}
              className={column.numeric ? "cell numeric" : "cell"}
              onClick={() =>
                onSort({ column: column.id, descending: active && !sort.descending })
              }
            >
              {column.label}
              {active && <span aria-hidden="true">{sort.descending ? " ↓" : " ↑"}</span>}
            </button>
          );
        })}
      </div>
      <div className="table-body" ref={scroller} data-testid="table-body">
        <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualizer.getVirtualItems().map((virtualRow) => {
            const item = rows[virtualRow.index];
            const key = objectKey(item);
            return (
              <div
                key={key}
                role="row"
                aria-rowindex={virtualRow.index + 1}
                aria-selected={key === selected}
                className="table-row"
                style={{
                  gridTemplateColumns: tracks,
                  height: rowHeight,
                  transform: `translateY(${virtualRow.start}px)`,
                }}
                onClick={() => onSelect(key)}
              >
                {columns.map((column) => {
                  const text = column.text(item, now);
                  return (
                    <div
                      key={column.id}
                      role="gridcell"
                      title={text}
                      data-tone={column.tone?.(item)}
                      className={column.numeric ? "cell numeric" : "cell"}
                    >
                      {text}
                    </div>
                  );
                })}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
