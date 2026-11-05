import { useEffect, useState } from "react";
import { openSocket } from "./api";

export interface KubeObject {
  kind?: string;
  metadata: {
    name: string;
    namespace?: string;
    creationTimestamp?: string;
  };
  status?: { phase?: string };
}

export interface ResourceRef {
  group: string;
  version: string;
  resource: string;
}

interface WatchMessage {
  type: "events" | "synced" | "failed" | "recovered";
  events?: { op: "added" | "modified" | "deleted"; object: KubeObject }[];
  message?: string;
  reason?: string;
}

export interface WatchState {
  items: KubeObject[];
  synced: boolean;
  connected: boolean;
  error?: string;
}

const initialState: WatchState = { items: [], synced: false, connected: false };
const firstRetryMs = 1000;
const maxRetryMs = 10000;

function objectKey(object: KubeObject): string {
  return `${object.metadata.namespace ?? ""}/${object.metadata.name}`;
}

export function useResourceWatch(
  context: string | undefined,
  ref: ResourceRef | undefined,
  namespace = "",
): WatchState {
  const [state, setState] = useState(initialState);
  const group = ref?.group ?? "";
  const version = ref?.version;
  const resource = ref?.resource;

  useEffect(() => {
    setState(initialState);
    if (!context || !version || !resource) {
      return;
    }

    const query = new URLSearchParams({ group, version, resource, namespace });
    const path = `/api/v1/contexts/${encodeURIComponent(context)}/watch?${query}`;

    let stopped = false;
    let socket: WebSocket | undefined;
    let retryTimer: number | undefined;
    let retryMs = firstRetryMs;
    let items = new Map<string, KubeObject>();
    // Each connection replays the full state. It builds up here and replaces
    // items at "synced", so the table keeps its rows during a reconnect and
    // rows deleted while offline disappear.
    let incoming: Map<string, KubeObject> | undefined;

    const publish = () =>
      setState((previous) => ({ ...previous, items: [...items.values()] }));

    const handle = (message: WatchMessage) => {
      switch (message.type) {
        case "events": {
          const target = incoming ?? items;
          for (const { op, object } of message.events ?? []) {
            if (op === "deleted") {
              target.delete(objectKey(object));
            } else {
              target.set(objectKey(object), object);
            }
          }
          if (!incoming) {
            publish();
          }
          break;
        }
        case "synced":
          items = incoming ?? items;
          incoming = undefined;
          retryMs = firstRetryMs;
          // "failed" can arrive before "synced", so this must not clear error.
          setState((previous) => ({
            ...previous,
            items: [...items.values()],
            synced: true,
            connected: true,
          }));
          break;
        case "failed":
          setState((previous) => ({ ...previous, error: message.message }));
          break;
        case "recovered":
          setState((previous) => ({ ...previous, error: undefined }));
          break;
      }
    };

    const reconnect = () => {
      if (stopped) {
        return;
      }
      setState((previous) => ({ ...previous, connected: false }));
      retryTimer = window.setTimeout(connect, retryMs);
      retryMs = Math.min(retryMs * 2, maxRetryMs);
    };

    const connect = async () => {
      const next = await openSocket(path);
      if (stopped) {
        next.close();
        return;
      }
      socket = next;
      incoming = new Map();
      // The server repeats any current failure after the connection opens.
      next.onopen = () =>
        setState((previous) => ({ ...previous, connected: true, error: undefined }));
      next.onmessage = (event) => handle(JSON.parse(event.data) as WatchMessage);
      next.onclose = reconnect;
    };

    connect().catch(reconnect);

    return () => {
      stopped = true;
      window.clearTimeout(retryTimer);
      socket?.close();
    };
  }, [context, group, version, resource, namespace]);

  return state;
}
