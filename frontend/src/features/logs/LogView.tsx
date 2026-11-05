import { useVirtualizer } from "@tanstack/react-virtual";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { maxLines } from "./useLogStream";

const lineHeight = 18;

interface Props {
  lines: string[];
  version: number;
  following: boolean;
  onFollowing: (following: boolean) => void;
}

function highlight(line: string, needle: string): ReactNode {
  if (!needle) {
    return line;
  }
  const lower = line.toLowerCase();
  const parts: ReactNode[] = [];
  let position = 0;
  for (
    let index = lower.indexOf(needle);
    index >= 0;
    index = lower.indexOf(needle, position)
  ) {
    parts.push(line.slice(position, index));
    parts.push(<mark key={index}>{line.slice(index, index + needle.length)}</mark>);
    position = index + needle.length;
  }
  parts.push(line.slice(position));
  return parts;
}

export function LogView({ lines, version, following, onFollowing }: Props) {
  const scroller = useRef<HTMLDivElement>(null);
  const [query, setQuery] = useState("");
  const [matchIndex, setMatchIndex] = useState(0);

  const needle = query.toLowerCase();
  // version changes whenever lines does; lines itself is replaced in a ref.
  const matches = useMemo(() => {
    if (!needle) {
      return [];
    }
    const found: number[] = [];
    lines.forEach((line, index) => {
      if (line.toLowerCase().includes(needle)) {
        found.push(index);
      }
    });
    return found;
  }, [lines, needle, version]);

  const virtualizer = useVirtualizer({
    count: lines.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => lineHeight,
    overscan: 20,
  });

  useEffect(() => {
    if (following && lines.length > 0) {
      virtualizer.scrollToIndex(lines.length - 1, { align: "end" });
    }
  }, [following, lines.length, version, virtualizer]);

  const goTo = (position: number) => {
    if (matches.length === 0) {
      return;
    }
    const wrapped = (position + matches.length) % matches.length;
    setMatchIndex(wrapped);
    onFollowing(false);
    virtualizer.scrollToIndex(matches[wrapped], { align: "center" });
  };

  const onSearchKey = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      goTo(matchIndex + (event.shiftKey ? -1 : 1));
    }
  };

  const currentLine = matches[Math.min(matchIndex, matches.length - 1)];

  return (
    <div className="log-view">
      <div className="log-search">
        <input
          type="search"
          placeholder="Search this log"
          aria-label="Search this log"
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setMatchIndex(0);
          }}
          onKeyDown={onSearchKey}
        />
        {needle && (
          <span className="muted" data-testid="log-matches">
            {matches.length === 0
              ? "No matches"
              : `${Math.min(matchIndex + 1, matches.length)} of ${matches.length}`}
          </span>
        )}
        {lines.length >= maxLines && (
          <span className="muted">Showing the latest {maxLines.toLocaleString()} lines</span>
        )}
      </div>
      <div
        className="log-lines"
        ref={scroller}
        data-testid="log-lines"
        // Only the reader's own actions pause following. A scroll event cannot
        // tell them apart from new lines pushing the view.
        onWheel={(event) => {
          if (event.deltaY < 0) {
            onFollowing(false);
          }
        }}
        onPointerDown={() => onFollowing(false)}
      >
        <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualizer.getVirtualItems().map((row) => (
            <div
              key={row.index}
              className={row.index === currentLine ? "log-line current" : "log-line"}
              style={{ height: lineHeight, transform: `translateY(${row.start}px)` }}
            >
              {highlight(lines[row.index], needle)}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
