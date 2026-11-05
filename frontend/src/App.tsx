import { useMemo, useState } from "react";
import { contextColor } from "./features/cluster/contextColor";
import { useCluster } from "./features/cluster/useCluster";
import { LogDock, logTarget, type LogTarget } from "./features/logs/LogDock";
import { resourceId } from "./features/resources/kinds";
import { ResourceView } from "./features/resources/ResourceView";
import type { KubeObject } from "./features/resources/useResourceWatch";
import { Sidebar } from "./features/resources/Sidebar";
import "./App.css";

const defaultResource = "/pods";

export default function App() {
  const cluster = useCluster();
  const [resourceKey, setResourceKey] = useState(defaultResource);
  const [namespace, setNamespace] = useState("");
  const [logTabs, setLogTabs] = useState<LogTarget[]>([]);
  const [activeLog, setActiveLog] = useState<string>();

  const { contexts, selected, status, resources } = cluster;
  const context = contexts?.find((item) => item.name === selected);
  const resource = useMemo(
    () =>
      resources?.find((item) => resourceId(item) === resourceKey) ??
      resources?.find((item) => resourceId(item) === defaultResource),
    [resources, resourceKey],
  );

  const selectContext = (name: string) => {
    // A namespace rarely exists under the same name in another cluster.
    setNamespace("");
    cluster.select(name);
  };

  const openLogs = (pod: KubeObject) => {
    const target = logTarget(selected, pod);
    setLogTabs((tabs) => (tabs.some((tab) => tab.id === target.id) ? tabs : [...tabs, target]));
    setActiveLog(target.id);
  };

  const closeLogs = (id: string) => {
    const index = logTabs.findIndex((tab) => tab.id === id);
    const remaining = logTabs.filter((tab) => tab.id !== id);
    setLogTabs(remaining);
    if (activeLog === id) {
      setActiveLog(remaining[Math.min(index, remaining.length - 1)]?.id);
    }
  };

  return (
    <div className="shell">
      <header
        className="band"
        style={selected ? { backgroundColor: contextColor(selected) } : undefined}
      >
        <select
          className="band-context"
          aria-label="Cluster context"
          data-testid="context-select"
          value={selected}
          disabled={!contexts?.length}
          onChange={(event) => selectContext(event.target.value)}
        >
          {contexts?.map((item) => (
            <option key={item.name} value={item.name}>
              {item.name}
            </option>
          ))}
        </select>
        <div className="band-right">
          {selected && (
            <label className="band-readonly">
              <input
                type="checkbox"
                data-testid="read-only"
                checked={cluster.readOnly}
                onChange={(event) => cluster.setReadOnly(event.target.checked)}
              />
              Read-only
            </label>
          )}
          <span className="band-status" data-testid="cluster-status">
            {!selected
              ? ""
              : !status
                ? "Connecting"
                : status.reachable
                  ? status.version
                  : "Unreachable"}
          </span>
        </div>
      </header>

      {cluster.settingsError && (
        <p className="banner" role="alert">
          Could not save read-only mode. {cluster.settingsError}
        </p>
      )}

      {cluster.contextsError && (
        <p className="message" data-tone="bad">
          Could not read kubeconfig. {cluster.contextsError}
        </p>
      )}

      {contexts?.length === 0 && (
        <p className="message">
          No clusters found in kubeconfig. Add one with your provider's CLI, then reopen Kubehack.
        </p>
      )}

      {status && !status.reachable && (
        <div className="message">
          <p>Could not reach {context?.server || selected}.</p>
          <p className="wrap muted">{status.error}</p>
          <button type="button" onClick={cluster.retry}>
            Try again
          </button>
        </div>
      )}

      {cluster.resourcesError && (
        <div className="message">
          <p className="wrap">{cluster.resourcesError}</p>
          <button type="button" onClick={cluster.retry}>
            Try again
          </button>
        </div>
      )}

      {resources && resource && (
        <div className="workspace">
          <Sidebar
            resources={resources}
            selected={resourceId(resource)}
            onSelect={setResourceKey}
          />
          <ResourceView
            context={selected}
            resource={resource}
            namespace={namespace}
            onNamespace={setNamespace}
            onOpenLogs={openLogs}
            readOnly={cluster.readOnly}
          />
        </div>
      )}

      {logTabs.length > 0 && (
        <LogDock
          tabs={logTabs}
          activeId={activeLog}
          onActivate={setActiveLog}
          onClose={closeLogs}
        />
      )}
    </div>
  );
}
