import { useState } from "react";
import { contextColor } from "../cluster/contextColor";
import { LogView } from "./LogView";
import { useLogStream, type LogStatus } from "./useLogStream";

export interface LogTarget {
  id: string;
  context: string;
  namespace: string;
  pod: string;
  containers: string[];
  container: string;
}

const statusText: Record<LogStatus, string> = {
  connecting: "Connecting",
  streaming: "Streaming",
  ended: "Stream ended",
  failed: "Could not read this log",
};

interface PanelProps {
  target: LogTarget;
  active: boolean;
}

// LogPanel stays mounted while its tab is in the background, so its stream
// keeps collecting lines. Only closing the tab ends the stream.
function LogPanel({ target, active }: PanelProps) {
  const [container, setContainer] = useState(target.container);
  const [previous, setPrevious] = useState(false);
  const [following, setFollowing] = useState(true);
  const log = useLogStream({
    context: target.context,
    namespace: target.namespace,
    pod: target.pod,
    container,
    previous,
  });

  if (!active) {
    return null;
  }

  return (
    <div className="log-panel" role="tabpanel">
      <div className="log-toolbar">
        <span className="muted">{target.context}</span>
        {target.containers.length > 1 ? (
          <select
            aria-label="Container"
            data-testid="log-container"
            value={container}
            onChange={(event) => setContainer(event.target.value)}
          >
            {target.containers.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        ) : (
          <span>{container}</span>
        )}
        <label>
          <input
            type="checkbox"
            data-testid="log-previous"
            checked={previous}
            onChange={(event) => setPrevious(event.target.checked)}
          />
          Previous run
        </label>
        <label>
          <input
            type="checkbox"
            data-testid="log-follow"
            checked={following}
            onChange={(event) => setFollowing(event.target.checked)}
          />
          Follow
        </label>
        <span
          className="log-status"
          data-testid="log-status"
          data-tone={log.status === "failed" ? "bad" : log.status === "streaming" ? "good" : undefined}
        >
          {statusText[log.status]}
        </span>
        {(log.status === "ended" || log.status === "failed") && !previous && (
          <button type="button" onClick={log.reconnect}>
            Reconnect
          </button>
        )}
      </div>
      {log.error && (
        <p className="banner" role="alert">
          {log.error}
        </p>
      )}
      <LogView
        lines={log.lines}
        version={log.version}
        following={following}
        onFollowing={setFollowing}
      />
    </div>
  );
}

interface DockProps {
  tabs: LogTarget[];
  activeId?: string;
  onActivate: (id: string) => void;
  onClose: (id: string) => void;
}

export function LogDock({ tabs, activeId, onActivate, onClose }: DockProps) {
  return (
    <section className="dock" aria-label="Logs">
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
              {tab.pod}
            </button>
            <button
              type="button"
              className="quiet"
              aria-label={`Close logs for ${tab.pod}`}
              onClick={() => onClose(tab.id)}
            >
              ✕
            </button>
          </div>
        ))}
      </div>
      {tabs.map((tab) => (
        <LogPanel key={tab.id} target={tab} active={tab.id === activeId} />
      ))}
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

export function logTarget(context: string, pod: PodLike): LogTarget {
  const spec = (pod.spec ?? {}) as Fields;
  const main = containerNames(spec.containers);
  const preferred = pod.metadata.annotations?.["kubectl.kubernetes.io/default-container"];
  const namespace = pod.metadata.namespace ?? "default";
  return {
    id: `${context}/${namespace}/${pod.metadata.name}`,
    context,
    namespace,
    pod: pod.metadata.name,
    containers: [...containerNames(spec.initContainers), ...main],
    container: preferred && main.includes(preferred) ? preferred : main[0],
  };
}
