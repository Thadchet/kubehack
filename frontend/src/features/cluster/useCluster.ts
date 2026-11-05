import { useCallback, useEffect, useState } from "react";
import { apiGet } from "../../api";

export interface KubeContext {
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

export interface ClusterStatus {
  reachable: boolean;
  version?: string;
  error?: string;
}

export interface APIResource {
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

function contextPath(name: string, suffix: string): string {
  return `/api/v1/contexts/${encodeURIComponent(name)}/${suffix}`;
}

export function useCluster() {
  const [contexts, setContexts] = useState<KubeContext[]>();
  const [contextsError, setContextsError] = useState<string>();
  const [selected, setSelected] = useState("");
  const [status, setStatus] = useState<ClusterStatus>();
  const [resources, setResources] = useState<APIResource[]>();
  const [resourcesError, setResourcesError] = useState<string>();
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    apiGet<ContextList>("/api/v1/contexts")
      .then((result) => {
        setContexts(result.contexts);
        setSelected(result.current || (result.contexts[0]?.name ?? ""));
      })
      .catch((error: Error) => setContextsError(error.message));
  }, []);

  useEffect(() => {
    setStatus(undefined);
    setResources(undefined);
    setResourcesError(undefined);
    if (!selected) {
      return;
    }

    // A slow cluster must not overwrite the result of a newer selection.
    let stale = false;
    const load = async () => {
      try {
        const next = await apiGet<ClusterStatus>(contextPath(selected, "status"));
        if (stale) {
          return;
        }
        setStatus(next);
        if (next.reachable) {
          const discovery = await apiGet<DiscoveryResult>(contextPath(selected, "resources"));
          if (!stale) {
            setResources(discovery.resources);
          }
        }
      } catch (error) {
        if (!stale) {
          setResourcesError((error as Error).message);
        }
      }
    };
    void load();
    return () => {
      stale = true;
    };
  }, [selected, attempt]);

  const retry = useCallback(() => setAttempt((value) => value + 1), []);

  return {
    contexts,
    contextsError,
    selected,
    select: setSelected,
    status,
    resources,
    resourcesError,
    retry,
  };
}
