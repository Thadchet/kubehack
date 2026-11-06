import { contextColor } from "../cluster/contextColor";
import { LogPanel } from "../logs/LogPanel";
import { TerminalPanel } from "../terminal/TerminalPanel";

export type DockKind = "logs" | "terminal";

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
}

export function Dock({ tabs, activeId, onActivate, onClose }: DockProps) {
  return (
    <section className="dock" aria-label="Logs and terminals">
      <div className="dock-tabs" role="tablist">
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
              {tab.kind === "terminal" ? `Shell: ${tab.pod}` : tab.pod}
            </button>
            <button
              type="button"
              className="quiet"
              aria-label={`Close ${tab.kind === "terminal" ? "shell" : "logs"} for ${tab.pod}`}
              onClick={() => onClose(tab.id)}
            >
              ✕
            </button>
          </div>
        ))}
      </div>
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
