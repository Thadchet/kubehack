import type { Usage } from "./useMetrics";
import { objectKey, type KubeObject } from "./useResourceWatch";

export type Tone = "good" | "warn" | "bad" | "neutral";

export interface Column {
  id: string;
  label: string;
  // A CSS grid track.
  width: string;
  text: (item: KubeObject, now: number) => string;
  // Sort key, when the displayed text would sort wrongly.
  sortValue?: (item: KubeObject) => number | string;
  tone?: (item: KubeObject) => Tone;
  numeric?: boolean;
}

type Fields = Record<string, unknown>;

function read(item: unknown, ...path: string[]): unknown {
  let value = item;
  for (const key of path) {
    value = (value as Fields | undefined)?.[key];
  }
  return value;
}

function list(item: unknown, ...path: string[]): Fields[] {
  const value = read(item, ...path);
  return Array.isArray(value) ? (value as Fields[]) : [];
}

function number(item: unknown, ...path: string[]): number {
  const value = read(item, ...path);
  return typeof value === "number" ? value : 0;
}

export function formatAge(timestamp: string | undefined, now: number): string {
  if (!timestamp) {
    return "";
  }
  const seconds = Math.max(0, (now - Date.parse(timestamp)) / 1000);
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

// podStatus follows what `kubectl get pods` prints: a container's waiting or
// terminated reason says more than the phase, which stays "Running" during a
// crash loop.
export function podStatus(pod: KubeObject): string {
  if (pod.metadata.deletionTimestamp) {
    return "Terminating";
  }
  const containers = [
    ...list(pod, "status", "initContainerStatuses"),
    ...list(pod, "status", "containerStatuses"),
  ];
  for (const container of containers) {
    const reason =
      read(container, "state", "waiting", "reason") ??
      read(container, "state", "terminated", "reason");
    if (typeof reason === "string" && reason !== "Completed") {
      return reason;
    }
  }
  return (read(pod, "status", "reason") ?? read(pod, "status", "phase") ?? "Unknown") as string;
}

function statusTone(status: string): Tone {
  if (["Running", "Succeeded", "Completed", "Active", "Bound", "Ready"].includes(status)) {
    return "good";
  }
  if (["Pending", "ContainerCreating", "PodInitializing", "Terminating"].includes(status)) {
    return "warn";
  }
  return status ? "bad" : "neutral";
}

function ratio(ready: number, total: number): string {
  return `${ready}/${total}`;
}

function ratioTone(ready: number, total: number): Tone {
  return ready >= total ? "good" : "warn";
}

function podReady(pod: KubeObject): [number, number] {
  const statuses = list(pod, "status", "containerStatuses");
  const total = list(pod, "spec", "containers").length;
  return [statuses.filter((status) => status.ready === true).length, total];
}

function podRestarts(pod: KubeObject): number {
  return list(pod, "status", "containerStatuses").reduce(
    (sum, status) => sum + number(status, "restartCount"),
    0,
  );
}

function nodeStatus(node: KubeObject): string {
  const ready = list(node, "status", "conditions").find((item) => item.type === "Ready");
  const state = ready?.status === "True" ? "Ready" : "NotReady";
  return read(node, "spec", "unschedulable") ? `${state}, unschedulable` : state;
}

const replicas: Column[] = [
  {
    id: "ready",
    label: "Ready",
    width: "80px",
    numeric: true,
    text: (item) =>
      ratio(number(item, "status", "readyReplicas"), number(item, "spec", "replicas")),
    sortValue: (item) => number(item, "status", "readyReplicas"),
    tone: (item) =>
      ratioTone(number(item, "status", "readyReplicas"), number(item, "spec", "replicas")),
  },
];

const extraColumns: Record<string, Column[]> = {
  "/pods": [
    {
      id: "status",
      label: "Status",
      width: "150px",
      text: podStatus,
      tone: (item) => statusTone(podStatus(item)),
    },
    {
      id: "ready",
      label: "Ready",
      width: "64px",
      numeric: true,
      text: (item) => ratio(...podReady(item)),
      sortValue: (item) => podReady(item)[0],
      tone: (item) => ratioTone(...podReady(item)),
    },
    {
      id: "restarts",
      label: "Restarts",
      width: "72px",
      numeric: true,
      text: (item) => String(podRestarts(item)),
      sortValue: podRestarts,
      tone: (item) => (podRestarts(item) > 0 ? "warn" : "neutral"),
    },
    {
      id: "node",
      label: "Node",
      width: "minmax(100px, 0.6fr)",
      text: (item) => String(read(item, "spec", "nodeName") ?? ""),
    },
  ],
  "apps/deployments": replicas,
  "apps/statefulsets": replicas,
  "apps/replicasets": replicas,
  "apps/daemonsets": [
    {
      id: "ready",
      label: "Ready",
      width: "80px",
      numeric: true,
      text: (item) =>
        ratio(
          number(item, "status", "numberReady"),
          number(item, "status", "desiredNumberScheduled"),
        ),
      sortValue: (item) => number(item, "status", "numberReady"),
      tone: (item) =>
        ratioTone(
          number(item, "status", "numberReady"),
          number(item, "status", "desiredNumberScheduled"),
        ),
    },
  ],
  "/services": [
    {
      id: "type",
      label: "Type",
      width: "110px",
      text: (item) => String(read(item, "spec", "type") ?? ""),
    },
    {
      id: "clusterIP",
      label: "Cluster IP",
      width: "130px",
      text: (item) => String(read(item, "spec", "clusterIP") ?? ""),
    },
  ],
  "/nodes": [
    {
      id: "status",
      label: "Status",
      width: "170px",
      text: nodeStatus,
      tone: (item) => (nodeStatus(item).startsWith("Ready") ? "good" : "bad"),
    },
    {
      id: "version",
      label: "Version",
      width: "120px",
      text: (item) => String(read(item, "status", "nodeInfo", "kubeletVersion") ?? ""),
    },
  ],
  "/events": [
    {
      id: "type",
      label: "Type",
      width: "80px",
      text: (item) => String(item.type ?? ""),
      tone: (item) => (item.type === "Warning" ? "warn" : "neutral"),
    },
    {
      id: "reason",
      label: "Reason",
      width: "150px",
      text: (item) => String(item.reason ?? ""),
    },
    {
      id: "object",
      label: "Object",
      width: "minmax(120px, 0.7fr)",
      text: (item) =>
        `${read(item, "involvedObject", "kind") ?? ""}/${read(item, "involvedObject", "name") ?? ""}`,
    },
  ],
};

const genericStatus: Column = {
  id: "status",
  label: "Status",
  width: "120px",
  text: (item) => String(read(item, "status", "phase") ?? ""),
  tone: (item) => statusTone(String(read(item, "status", "phase") ?? "")),
};

export function hasPhase(items: KubeObject[]): boolean {
  return items.some((item) => read(item, "status", "phase") !== undefined);
}

const binaryUnits = ["Ki", "Mi", "Gi", "Ti", "Pi", "Ei"];
const decimalUnits: Record<string, number> = {
  n: 1e-9,
  u: 1e-6,
  m: 1e-3,
  k: 1e3,
  M: 1e6,
  G: 1e9,
  T: 1e12,
  P: 1e15,
  E: 1e18,
};

// parseQuantity reads the Kubernetes forms that appear in node status, such as
// "3920m" or "8039412Ki". An unreadable value gives 0.
export function parseQuantity(value: unknown): number {
  const match = /^([0-9.]+)([a-zA-Z]*)$/.exec(String(value ?? ""));
  if (!match) {
    return 0;
  }
  const [, amount, unit] = match;
  const binary = binaryUnits.indexOf(unit);
  const scale = binary >= 0 ? 1024 ** (binary + 1) : unit ? decimalUnits[unit] : 1;
  return scale ? Number(amount) * scale : 0;
}

export function formatCpu(millicores: number): string {
  return millicores < 1000 ? `${millicores}m` : (millicores / 1000).toFixed(2);
}

export function formatMemory(bytes: number): string {
  const mebibytes = bytes / 1024 ** 2;
  return mebibytes < 1024 ? `${Math.round(mebibytes)}Mi` : `${(mebibytes / 1024).toFixed(2)}Gi`;
}

function share(used: number, total: number): number | undefined {
  return total > 0 ? Math.round((used / total) * 100) : undefined;
}

function shareTone(percent: number | undefined): Tone {
  if (percent === undefined || percent < 80) {
    return "neutral";
  }
  return percent < 95 ? "warn" : "bad";
}

function usageColumns(usage: Map<string, Usage>, isNode: boolean): Column[] {
  const find = (item: KubeObject) => usage.get(objectKey(item));
  const column = (
    id: string,
    label: string,
    width: string,
    field: keyof Usage,
    format: (value: number) => string,
    // Allocatable, in the same units as the usage.
    total: (item: KubeObject) => number,
  ): Column => {
    const percent = (item: KubeObject) => {
      const found = find(item);
      return found && isNode ? share(found[field], total(item)) : undefined;
    };
    return {
      id,
      label,
      width,
      numeric: true,
      // Pods that aren't running have no usage, which is different from zero.
      text: (item) => {
        const found = find(item);
        if (!found) {
          return "";
        }
        const value = percent(item);
        return value === undefined ? format(found[field]) : `${format(found[field])} (${value}%)`;
      },
      sortValue: (item) => find(item)?.[field] ?? -1,
      tone: (item) => shareTone(percent(item)),
    };
  };
  return [
    column(
      "cpu",
      "CPU",
      isNode ? "110px" : "64px",
      "cpu",
      formatCpu,
      (item) => parseQuantity(read(item, "status", "allocatable", "cpu")) * 1000,
    ),
    column("memory", "Memory", isNode ? "120px" : "72px", "memory", formatMemory, (item) =>
      parseQuantity(read(item, "status", "allocatable", "memory")),
    ),
  ];
}

export function columnsFor(
  id: string,
  showNamespace: boolean,
  showPhase: boolean,
  usage?: Map<string, Usage>,
): Column[] {
  const name: Column = {
    id: "name",
    label: "Name",
    width: "minmax(180px, 1.4fr)",
    text: (item) => item.metadata.name,
  };
  const namespace: Column = {
    id: "namespace",
    label: "Namespace",
    width: "minmax(100px, 0.5fr)",
    text: (item) => item.metadata.namespace ?? "",
  };
  const age: Column = {
    id: "age",
    label: "Age",
    width: "64px",
    numeric: true,
    text: (item, now) => formatAge(item.metadata.creationTimestamp, now),
    // Newest first when ascending, like the displayed number growing.
    sortValue: (item) => -Date.parse(item.metadata.creationTimestamp ?? "0"),
  };
  return [
    name,
    ...(showNamespace ? [namespace] : []),
    ...(extraColumns[id] ?? (showPhase ? [genericStatus] : [])),
    ...(usage ? usageColumns(usage, id === "/nodes") : []),
    age,
  ];
}
