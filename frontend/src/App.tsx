import { Cable, ChevronDown } from "lucide-react";
import { useLayoutEffect, useMemo, useState } from "react";
import { contextColor, contextHue } from "./features/cluster/contextColor";
import { useCluster } from "./features/cluster/useCluster";
import {
  Dock,
  dockTarget,
  forwardsTab,
  type DockKind,
  type DockTarget,
} from "./features/dock/Dock";
import { HelmView } from "./features/helm/HelmView";
import { ForwardAlerts } from "./features/portforward/ForwardAlerts";
import { usePortForwards } from "./features/portforward/usePortForwards";
import { resourceId } from "./features/resources/kinds";
import { ResourceView } from "./features/resources/ResourceView";
import type { KubeObject } from "./features/resources/useResourceWatch";
import { helmReleasesId, Sidebar } from "./features/resources/Sidebar";
import "./App.css";

const defaultResource = "/pods";

export default function App() {
  const cluster = useCluster();
  const [resourceKey, setResourceKey] = useState(defaultResource);
  const [namespace, setNamespace] = useState("");
  const [dockTabs, setDockTabs] = useState<DockTarget[]>([]);
  const [showForwards, setShowForwards] = useState(false);
  const [activeTab, setActiveTab] = useState<string>();
  const portForwards = usePortForwards();

  const { contexts, selected, status, resources } = cluster;
  const context = contexts?.find((item) => item.name === selected);
  const resource = useMemo(
    () =>
      resources?.find((item) => resourceId(item) === resourceKey) ??
      resources?.find((item) => resourceId(item) === defaultResource),
    [resources, resourceKey],
  );

  const showHelm = resourceKey === helmReleasesId;

  // Every surface derives from this, so the whole app shows which cluster is open.
  useLayoutEffect(() => {
    if (selected) {
      document.documentElement.style.setProperty("--hue", String(contextHue(selected)));
    }
  }, [selected]);

  const selectContext = (name: string) => {
    // A namespace rarely exists under the same name in another cluster.
    setNamespace("");
    cluster.select(name);
  };

  const openTab = (kind: DockKind, pod: KubeObject) => {
    const target = dockTarget(kind, selected, pod);
    setDockTabs((tabs) => (tabs.some((tab) => tab.id === target.id) ? tabs : [...tabs, target]));
    setActiveTab(target.id);
  };

  const openForwards = () => {
    setShowForwards(true);
    setActiveTab(forwardsTab);
  };

  // Closing the tab only hides the list. The forwards keep running.
  const closeTab = (id: string) => {
    if (id === forwardsTab) {
      setShowForwards(false);
      if (activeTab === id) {
        setActiveTab(dockTabs[0]?.id);
      }
      return;
    }
    const index = dockTabs.findIndex((tab) => tab.id === id);
    const remaining = dockTabs.filter((tab) => tab.id !== id);
    setDockTabs(remaining);
    if (activeTab === id) {
      const next = remaining[Math.min(index, remaining.length - 1)]?.id;
      setActiveTab(next ?? (showForwards ? forwardsTab : undefined));
    }
  };

  const forwardCount = portForwards.forwards.length;

  return (
    <div className="shell">
      <header className="band">
        <div
          className="band-cluster"
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
          <ChevronDown size={14} aria-hidden="true" />
        </div>
        <span
          className="band-status"
          data-testid="cluster-status"
          data-tone={status ? (status.reachable ? "good" : "bad") : undefined}
        >
          {!selected
            ? ""
            : !status
              ? "Connecting"
              : status.reachable
                ? status.version
                : "Unreachable"}
        </span>
        <div className="band-right">
          {forwardCount > 0 && (
            <button
              type="button"
              className="band-forwards"
              data-testid="forwards-button"
              onClick={openForwards}
            >
              <Cable size={14} aria-hidden="true" />
              Port forwards: {forwardCount}
            </button>
          )}
          {selected && (
            <label className="band-readonly" data-on={cluster.readOnly}>
              <input
                type="checkbox"
                data-testid="read-only"
                checked={cluster.readOnly}
                onChange={(event) => cluster.setReadOnly(event.target.checked)}
              />
              Read-only
            </label>
          )}
        </div>
      </header>

      <div className="notices">
        <ForwardAlerts portForwards={portForwards} />

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

      </div>

      {resources && resource && (
        <div className="workspace">
          <Sidebar
            resources={resources}
            selected={showHelm ? helmReleasesId : resourceId(resource)}
            onSelect={setResourceKey}
          />
          {showHelm ? (
            <HelmView
              context={selected}
              namespace={namespace}
              onNamespace={setNamespace}
              readOnly={cluster.readOnly}
            />
          ) : (
            <ResourceView
              context={selected}
              resource={resource}
              namespace={namespace}
              onNamespace={setNamespace}
              onOpenDock={openTab}
              onForwardStarted={openForwards}
              readOnly={cluster.readOnly}
            />
          )}
        </div>
      )}

      {(dockTabs.length > 0 || showForwards) && (
        <Dock
          tabs={dockTabs}
          activeId={activeTab}
          onActivate={setActiveTab}
          onClose={closeTab}
          showForwards={showForwards}
          portForwards={portForwards}
        />
      )}
    </div>
  );
}
