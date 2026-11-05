import { useEffect, useState } from "react";
import { apiGet } from "./api";
import { useResourceWatch } from "./useResourceWatch";
import "./App.css";

interface KubeContext {
  name: string;
  cluster: string;
  user: string;
  namespace: string;
  server: string;
  authCommand?: string;
}

interface ContextList {
  current: string;
  contexts: KubeContext[];
}

interface ClusterStatus {
  reachable: boolean;
  version?: string;
  error?: string;
}

interface APIResource {
  group: string;
  version: string;
  resource: string;
  kind: string;
  namespaced: boolean;
}

interface DiscoveryResult {
  resources: APIResource[];
  warnings?: string[];
}

function resourceId(item: APIResource): string {
  return `${item.group}/${item.version}/${item.resource}`;
}

function age(timestamp?: string): string {
  if (!timestamp) {
    return "";
  }
  const seconds = Math.max(0, (Date.now() - Date.parse(timestamp)) / 1000);
  if (seconds < 120) {
    return `${Math.floor(seconds)}s`;
  }
  if (seconds < 7200) {
    return `${Math.floor(seconds / 60)}m`;
  }
  if (seconds < 172800) {
    return `${Math.floor(seconds / 3600)}h`;
  }
  return `${Math.floor(seconds / 86400)}d`;
}

export default function App() {
  const [list, setList] = useState<ContextList>();
  const [listError, setListError] = useState<string>();
  const [selected, setSelected] = useState("");
  const [status, setStatus] = useState<ClusterStatus>();
  const [discovery, setDiscovery] = useState<DiscoveryResult>();
  const [discoveryError, setDiscoveryError] = useState<string>();
  const [resourceKey, setResourceKey] = useState("");

  useEffect(() => {
    apiGet<ContextList>("/api/v1/contexts")
      .then((result) => {
        setList(result);
        setSelected(result.current || (result.contexts[0]?.name ?? ""));
      })
      .catch((error: Error) => setListError(error.message));
  }, []);

  useEffect(() => {
    if (!selected) {
      return;
    }
    // A slow cluster must not overwrite the status of a newer selection.
    let stale = false;
    setStatus(undefined);
    apiGet<ClusterStatus>(`/api/v1/contexts/${encodeURIComponent(selected)}/status`)
      .then((result) => {
        if (!stale) {
          setStatus(result);
        }
      })
      .catch((error: Error) => {
        if (!stale) {
          setStatus({ reachable: false, error: error.message });
        }
      });
    return () => {
      stale = true;
    };
  }, [selected]);

  const reachable = status?.reachable ?? false;

  useEffect(() => {
    setDiscovery(undefined);
    setDiscoveryError(undefined);
    if (!selected || !reachable) {
      return;
    }
    let stale = false;
    apiGet<DiscoveryResult>(`/api/v1/contexts/${encodeURIComponent(selected)}/resources`)
      .then((result) => {
        if (!stale) {
          setDiscovery(result);
          setResourceKey((current) => {
            const known = result.resources.some((item) => resourceId(item) === current);
            return known ? current : "/v1/pods";
          });
        }
      })
      .catch((error: Error) => {
        if (!stale) {
          setDiscoveryError(error.message);
        }
      });
    return () => {
      stale = true;
    };
  }, [selected, reachable]);

  const resource = discovery?.resources.find((item) => resourceId(item) === resourceKey);
  const watch = useResourceWatch(reachable ? selected : undefined, resource);

  const context = list?.contexts.find((item) => item.name === selected);

  return (
    <main className="app">
      <h1>kubehack</h1>

      {listError && <p className="error">Could not read kubeconfig: {listError}</p>}
      {list?.contexts.length === 0 && <p>No contexts found in kubeconfig.</p>}

      {list && list.contexts.length > 0 && (
        <label className="field">
          Context
          <select
            data-testid="context-select"
            value={selected}
            onChange={(event) => setSelected(event.target.value)}
          >
            {list.contexts.map((item) => (
              <option key={item.name} value={item.name}>
                {item.name}
              </option>
            ))}
          </select>
        </label>
      )}

      {context && (
        <dl className="details">
          <dt>Server</dt>
          <dd>{context.server || "Not set"}</dd>
          <dt>Namespace</dt>
          <dd>{context.namespace || "default"}</dd>
          <dt>Auth plugin</dt>
          <dd>{context.authCommand || "None"}</dd>
          <dt>Status</dt>
          <dd data-testid="cluster-status">
            {!status && "Connecting…"}
            {status?.reachable && `Connected (${status.version})`}
            {status && !status.reachable && <span className="error">{status.error}</span>}
          </dd>
        </dl>
      )}

      {discoveryError && <p className="error">Could not list resources: {discoveryError}</p>}

      {discovery && (
        <label className="field">
          Resource
          <select
            data-testid="resource-select"
            value={resourceKey}
            onChange={(event) => setResourceKey(event.target.value)}
          >
            {discovery.resources.map((item) => (
              <option key={resourceId(item)} value={resourceId(item)}>
                {item.resource}
                {item.group ? `.${item.group}` : ""}
              </option>
            ))}
          </select>
        </label>
      )}

      {resource && (
        <section>
          <p data-testid="watch-state">
            {watch.items.length} items ·{" "}
            {!watch.connected ? "reconnecting…" : watch.error ? "not updating" : "live"}
            {!watch.synced && watch.connected && " · loading"}
          </p>
          {watch.error && <p className="error">{watch.error}</p>}
          <table className="resources">
            <thead>
              <tr>
                <th>Name</th>
                {resource.namespaced && <th>Namespace</th>}
                <th>Status</th>
                <th>Age</th>
              </tr>
            </thead>
            <tbody>
              {watch.items.map((item) => (
                <tr key={`${item.metadata.namespace}/${item.metadata.name}`}>
                  <td>{item.metadata.name}</td>
                  {resource.namespaced && <td>{item.metadata.namespace}</td>}
                  <td>{item.status?.phase}</td>
                  <td>{age(item.metadata.creationTimestamp)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
    </main>
  );
}
