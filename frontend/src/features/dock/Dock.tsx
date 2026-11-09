import { Cable, ScrollText, SquareTerminal, X } from "lucide-react";
import { useEffect, useState, type KeyboardEvent, type PointerEvent } from "react";
import { contextColor } from "../cluster/contextColor";
import { LogPanel } from "../logs/LogPanel";
import { ForwardPanel } from "../portforward/ForwardPanel";
import type { PortForwards } from "../portforward/usePortForwards";
import { TerminalPanel } from "../terminal/TerminalPanel";

export type DockKind = "logs" | "terminal";

// Forwards span clusters, so they get one shared tab rather than one per pod.
export const forwardsTab = "forwards";

export interface DockTarget {
  id: string;
  kind: DockKind;
  context: string;
  namespace: string;
  pod: string;
  containers: string[];
  container: string;
}

interface DockProps {
  tabs: DockTarget[];
  activeId?: string;
  onActivate: (id: string) => void;
  onClose: (id: string) => void;
  showForwards: boolean;
  portForwards: PortForwards;
}

const heightKey = "kubehack.dockHeight";
const minHeight = 120;
// The view above keeps at least this much room.
const viewRoom = 160;
const keyStep = 24;

function maxHeight(): number {
  return Math.max(minHeight, window.innerHeight - viewRoom);
}

function clampHeight(height: number): number {
  return Math.round(Math.min(maxHeight(), Math.max(minHeight, height)));
}

// Storage can be missing or blocked, in which case the dock starts at its default.
function savedHeight(): number {
  try {
    const saved = Number(localStorage.getItem(heightKey));
    if (saved > 0) {
      return clampHeight(saved);
    }
  } catch {
    // Use the default below.
  }
  return clampHeight(window.innerHeight * 0.4);
}

function saveHeight(height: number) {
  try {
    localStorage.setItem(heightKey, String(height));
  } catch {
    // The height lasts until the app closes.
  }
}

function useDockHeight() {
  const [height, setHeight] = useState(savedHeight);

  useEffect(() => {
    const fit = () => setHeight((current) => clampHeight(current));
    window.addEventListener("resize", fit);
    return () => window.removeEventListener("resize", fit);
  }, []);

  const resize = (next: number) => {
    const clamped = clampHeight(next);
    setHeight(clamped);
    saveHeight(clamped);
  };

  // The handle sits on the dock's top edge, so dragging up makes the dock taller.
  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    const handle = event.currentTarget;
    const startY = event.clientY;
    const startHeight = height;
    handle.setPointerCapture(event.pointerId);
    document.body.dataset.resizing = "dock";
    const move = (moved: globalThis.PointerEvent) => resize(startHeight + startY - moved.clientY);
    const end = () => {
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", end);
      handle.removeEventListener("pointercancel", end);
      delete document.body.dataset.resizing;
    };
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", end);
    handle.addEventListener("pointercancel", end);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? keyStep * 4 : keyStep;
    const next =
      event.key === "ArrowUp"
        ? height + step
        : event.key === "ArrowDown"
          ? height - step
          : event.key === "Home"
            ? maxHeight()
            : event.key === "End"
              ? minHeight
              : undefined;
    if (next !== undefined) {
      event.preventDefault();
      resize(next);
    }
  };

  return { height, onPointerDown, onKeyDown };
}

export function Dock({
  tabs,
  activeId,
  onActivate,
  onClose,
  showForwards,
  portForwards,
}: DockProps) {
  const { height, onPointerDown, onKeyDown } = useDockHeight();
  return (
    <section
      className="dock"
      aria-label="Logs, terminals and port forwards"
      style={{ height }}
    >
      <div
        className="dock-resize"
        role="separator"
        aria-orientation="horizontal"
        aria-label="Resize the dock"
        aria-valuemin={minHeight}
        aria-valuemax={maxHeight()}
        aria-valuenow={height}
        tabIndex={0}
        title="Drag to resize"
        onPointerDown={onPointerDown}
        onKeyDown={onKeyDown}
      />
      <div className="dock-tabs" role="tablist">
        {showForwards && (
          <div className="dock-tab" data-active={activeId === forwardsTab}>
            <button
              type="button"
              role="tab"
              aria-selected={activeId === forwardsTab}
              onClick={() => onActivate(forwardsTab)}
            >
              <Cable size={14} aria-hidden="true" />
              Port forwards
            </button>
            <button
              type="button"
              className="quiet"
              aria-label="Close port forwards"
              onClick={() => onClose(forwardsTab)}
            >
              <X size={14} aria-hidden="true" />
            </button>
          </div>
        )}
        {tabs.map((tab) => (
          <div key={tab.id} className="dock-tab" data-active={tab.id === activeId}>
            <button
              type="button"
              role="tab"
              aria-selected={tab.id === activeId}
              title={`${tab.context}, ${tab.namespace}`}
              onClick={() => onActivate(tab.id)}
            >
              <span
                className="dock-dot"
                aria-hidden="true"
                style={{ backgroundColor: contextColor(tab.context) }}
              />
              {tab.kind === "terminal" ? (
                <SquareTerminal size={14} aria-hidden="true" />
              ) : (
                <ScrollText size={14} aria-hidden="true" />
              )}
              <span className="dock-label">
                {tab.kind === "terminal" ? `Shell: ${tab.pod}` : tab.pod}
              </span>
            </button>
            <button
              type="button"
              className="quiet"
              aria-label={`Close ${tab.kind === "terminal" ? "shell" : "logs"} for ${tab.pod}`}
              onClick={() => onClose(tab.id)}
            >
              <X size={14} aria-hidden="true" />
            </button>
          </div>
        ))}
      </div>
      {showForwards && (
        <ForwardPanel portForwards={portForwards} active={activeId === forwardsTab} />
      )}
      {tabs.map((tab) =>
        tab.kind === "terminal" ? (
          <TerminalPanel key={tab.id} target={tab} active={tab.id === activeId} />
        ) : (
          <LogPanel key={tab.id} target={tab} active={tab.id === activeId} />
        ),
      )}
    </section>
  );
}

type Fields = Record<string, unknown>;

function containerNames(value: unknown): string[] {
  return Array.isArray(value) ? (value as Fields[]).map((item) => String(item.name)) : [];
}

interface PodLike {
  metadata: { name: string; namespace?: string; annotations?: Record<string, string> };
  spec?: unknown;
}

export function dockTarget(kind: DockKind, context: string, pod: PodLike): DockTarget {
  const spec = (pod.spec ?? {}) as Fields;
  const main = containerNames(spec.containers);
  const preferred = pod.metadata.annotations?.["kubectl.kubernetes.io/default-container"];
  const namespace = pod.metadata.namespace ?? "default";
  return {
    id: `${kind}/${context}/${namespace}/${pod.metadata.name}`,
    kind,
    context,
    namespace,
    pod: pod.metadata.name,
    // Init containers have finished by the time a pod runs, so a shell can only
    // open in the main ones.
    containers: kind === "terminal" ? main : [...containerNames(spec.initContainers), ...main],
    container: preferred && main.includes(preferred) ? preferred : main[0],
  };
}
