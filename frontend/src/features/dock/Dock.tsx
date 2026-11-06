import { Cable, ScrollText, SquareTerminal, X } from "lucide-react";
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

export function Dock({
  tabs,
  activeId,
  onActivate,
  onClose,
  showForwards,
  portForwards,
}: DockProps) {
  return (
    <section className="dock" aria-label="Logs, terminals and port forwards">
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
