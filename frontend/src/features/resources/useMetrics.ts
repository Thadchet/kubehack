import { useEffect, useMemo, useState } from "react";
import { apiGet } from "../../api";

export interface Usage {
  // Millicores.
  cpu: number;
  // Bytes.
  memory: number;
}

export interface ObjectUsage extends Usage {
  containers?: (Usage & { name: string })[];
}

interface MetricItem extends ObjectUsage {
  namespace?: string;
  name: string;
}

interface Snapshot {
  available: boolean;
  reason?: "notInstalled" | "unavailable" | "forbidden" | "failed";
  message?: string;
  items: MetricItem[];
}

export type MetricsKind = "pods" | "nodes";

export interface Metrics {
  // Missing until the first answer, and whenever usage can't be read.
  usage?: Map<string, MetricItem>;
  // Why usage isn't shown.
  note?: string;
}

// metrics-server takes a new sample every 15 seconds by default.
const pollMs = 15000;

function noteFor(snapshot: Snapshot): string {
  switch (snapshot.reason) {
    case "notInstalled":
      return "CPU and memory aren't shown because this cluster doesn't have metrics-server.";
    case "unavailable":
      return "CPU and memory aren't shown because metrics-server isn't responding.";
    case "forbidden":
      return "CPU and memory aren't shown because you don't have permission to read metrics.";
    default:
      return `CPU and memory aren't shown. ${snapshot.message ?? ""}`;
  }
}

export function useMetrics(
  context: string | undefined,
  kind: MetricsKind | undefined,
  namespace: string,
): Metrics {
  const [snapshot, setSnapshot] = useState<Snapshot>();

  useEffect(() => {
    setSnapshot(undefined);
    if (!context || !kind) {
      return;
    }
    const query = new URLSearchParams({ kind, namespace });
    const path = `/api/v1/contexts/${encodeURIComponent(context)}/metrics?${query}`;

    let stopped = false;
    let timer: number | undefined;

    // Each poll waits for the last, so a slow cluster never builds up a queue.
    const poll = async () => {
      try {
        const next = await apiGet<Snapshot>(path);
        if (!stopped) {
          setSnapshot(next);
        }
      } catch (error) {
        if (!stopped) {
          setSnapshot({
            available: false,
            reason: "failed",
            message: (error as Error).message,
            items: [],
          });
        }
      }
      if (!stopped) {
        timer = window.setTimeout(poll, pollMs);
      }
    };
    poll();

    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [context, kind, namespace]);

  return useMemo(() => {
    if (!snapshot) {
      return {};
    }
    if (!snapshot.available) {
      return { note: noteFor(snapshot) };
    }
    return {
      usage: new Map(
        snapshot.items.map((item) => [`${item.namespace ?? ""}/${item.name}`, item]),
      ),
    };
  }, [snapshot]);
}
