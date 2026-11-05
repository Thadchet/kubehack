import { useEffect, useRef, useState } from "react";
import { openSocket } from "../../api";

// Bounds memory for pods that log heavily. Old lines drop off the top.
export const maxLines = 20000;
const tailLines = 1000;

// Log viewers show text, so color codes would only appear as noise.
const ansiPattern = /\u001b\[[0-9;?]*[ -/]*[@-~]/g;

export type LogStatus = "connecting" | "streaming" | "ended" | "failed";

export interface LogSource {
  context: string;
  namespace: string;
  pod: string;
  container: string;
  previous: boolean;
}

interface LogMessage {
  type: "lines" | "end" | "error";
  lines?: string[];
  message?: string;
}

export function useLogStream({ context, namespace, pod, container, previous }: LogSource) {
  const lines = useRef<string[]>([]);
  const [version, setVersion] = useState(0);
  const [status, setStatus] = useState<LogStatus>("connecting");
  const [error, setError] = useState<string>();
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    lines.current = [];
    setVersion((value) => value + 1);
    setStatus("connecting");
    setError(undefined);

    const query = new URLSearchParams({
      namespace,
      pod,
      container,
      previous: String(previous),
      // A previous container has stopped, so there is nothing to follow.
      follow: String(!previous),
      tail: String(tailLines),
    });
    const path = `/api/v1/contexts/${encodeURIComponent(context)}/logs?${query}`;

    let stopped = false;
    let socket: WebSocket | undefined;

    const handle = (message: LogMessage) => {
      switch (message.type) {
        case "lines": {
          const incoming = (message.lines ?? []).map((line) => line.replace(ansiPattern, ""));
          const next = lines.current.concat(incoming);
          lines.current = next.length > maxLines ? next.slice(next.length - maxLines) : next;
          setVersion((value) => value + 1);
          setStatus("streaming");
          break;
        }
        case "end":
          setStatus("ended");
          break;
        case "error":
          setError(message.message);
          setStatus("failed");
          break;
      }
    };

    openSocket(path)
      .then((next) => {
        if (stopped) {
          next.close();
          return;
        }
        socket = next;
        next.onopen = () => setStatus("streaming");
        next.onmessage = (event) => handle(JSON.parse(event.data) as LogMessage);
        next.onclose = () => {
          if (!stopped) {
            setStatus((current) => (current === "failed" ? current : "ended"));
          }
        };
      })
      .catch((reason: Error) => {
        setError(reason.message);
        setStatus("failed");
      });

    return () => {
      stopped = true;
      socket?.close();
    };
  }, [context, namespace, pod, container, previous, attempt]);

  return {
    lines: lines.current,
    version,
    status,
    error,
    reconnect: () => setAttempt((value) => value + 1),
  };
}
