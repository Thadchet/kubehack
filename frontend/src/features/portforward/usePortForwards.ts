import { useCallback, useEffect, useState } from "react";
import { apiPost, openSocket } from "../../api";
import { toFailure } from "../actions/failure";

export interface PortForward {
  id: string;
  context: string;
  namespace: string;
  kind: "pod" | "service";
  name: string;
  port: number;
  localPort: number;
  pod?: string;
  state: "starting" | "active" | "lost";
  message?: string;
}

export interface PortForwards {
  forwards: PortForward[];
  error?: string;
  clearError: () => void;
  restart: (id: string) => void;
  stop: (id: string) => void;
}

const firstRetryMs = 1000;
const maxRetryMs = 10000;

export function usePortForwards(): PortForwards {
  const [forwards, setForwards] = useState<PortForward[]>([]);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let stopped = false;
    let socket: WebSocket | undefined;
    let retryTimer: number | undefined;
    let retryMs = firstRetryMs;

    const connect = async () => {
      const opened = await openSocket("/api/v1/portforwards/watch");
      if (stopped) {
        opened.close();
        return;
      }
      socket = opened;
      opened.onmessage = (event) => {
        retryMs = firstRetryMs;
        setForwards(JSON.parse(event.data) as PortForward[]);
      };
      opened.onclose = () => {
        if (!stopped) {
          retryTimer = window.setTimeout(connect, retryMs);
          retryMs = Math.min(retryMs * 2, maxRetryMs);
        }
      };
    };
    connect();

    return () => {
      stopped = true;
      window.clearTimeout(retryTimer);
      socket?.close();
    };
  }, []);

  const send = useCallback(async (id: string, action: "restart" | "stop") => {
    setError(undefined);
    try {
      await apiPost(`/api/v1/portforwards/${id}/${action}`, {});
    } catch (failure) {
      const { kind, message } = toFailure(failure);
      // Other reconnect failures are recorded on the forward itself.
      if (action === "stop" || kind === "readOnly") {
        setError(message);
      }
    }
  }, []);

  const restart = useCallback((id: string) => void send(id, "restart"), [send]);
  const stop = useCallback((id: string) => void send(id, "stop"), [send]);

  const clearError = useCallback(() => setError(undefined), []);

  return { forwards, error, clearError, restart, stop };
}

export function forwardAddress(forward: PortForward): string {
  return `127.0.0.1:${forward.localPort}`;
}

export function forwardTarget(forward: PortForward): string {
  return `${forward.kind} ${forward.name}:${forward.port}`;
}
