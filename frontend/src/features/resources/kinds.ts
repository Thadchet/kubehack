import type { APIResource } from "../cluster/useCluster";

export interface ResourceSection {
  title: string;
  resources: APIResource[];
}

// Keys are "group/resource". Order here is the order in the sidebar.
const knownSections: [string, string[]][] = [
  [
    "Workloads",
    [
      "/pods",
      "apps/deployments",
      "apps/statefulsets",
      "apps/daemonsets",
      "apps/replicasets",
      "batch/jobs",
      "batch/cronjobs",
    ],
  ],
  [
    "Network",
    [
      "/services",
      "networking.k8s.io/ingresses",
      "discovery.k8s.io/endpointslices",
      "networking.k8s.io/networkpolicies",
    ],
  ],
  [
    "Config",
    [
      "/configmaps",
      "/secrets",
      "autoscaling/horizontalpodautoscalers",
      "policy/poddisruptionbudgets",
      "/resourcequotas",
      "/limitranges",
    ],
  ],
  ["Storage", ["/persistentvolumeclaims", "/persistentvolumes", "storage.k8s.io/storageclasses"]],
  [
    "Access",
    [
      "/serviceaccounts",
      "rbac.authorization.k8s.io/roles",
      "rbac.authorization.k8s.io/rolebindings",
      "rbac.authorization.k8s.io/clusterroles",
      "rbac.authorization.k8s.io/clusterrolebindings",
    ],
  ],
  ["Cluster", ["/nodes", "/namespaces", "/events", "apiextensions.k8s.io/customresourcedefinitions"]],
];

// events.k8s.io serves the same objects as core events.
const hidden = new Set(["events.k8s.io/events"]);

function isBuiltIn(group: string): boolean {
  return !group.includes(".") || group.endsWith(".k8s.io");
}

export function resourceId(item: APIResource): string {
  return `${item.group}/${item.resource}`;
}

export function resourceLabel(item: APIResource): string {
  // Plural kinds read better than lowercase resource names. A kind can already
  // be plural, as Endpoints is.
  const { kind } = item;
  if (kind.endsWith("ss")) {
    return `${kind}es`;
  }
  if (kind.endsWith("s")) {
    return kind;
  }
  return `${kind.replace(/([^aeiou])y$/, "$1ie")}s`;
}

export function groupResources(resources: APIResource[]): ResourceSection[] {
  const byId = new Map(resources.map((item) => [resourceId(item), item]));
  const placed = new Set<string>();

  const sections = knownSections.map(([title, ids]) => {
    const items = ids.flatMap((id) => {
      const item = byId.get(id);
      if (item) {
        placed.add(id);
      }
      return item ? [item] : [];
    });
    return { title, resources: items };
  });

  const rest = resources.filter(
    (item) => !placed.has(resourceId(item)) && !hidden.has(resourceId(item)),
  );
  const byLabel = (a: APIResource, b: APIResource) =>
    resourceLabel(a).localeCompare(resourceLabel(b));
  sections.push({
    title: "Custom resources",
    resources: rest.filter((item) => !isBuiltIn(item.group)).sort(byLabel),
  });
  sections.push({
    title: "Other",
    resources: rest.filter((item) => isBuiltIn(item.group)).sort(byLabel),
  });
  return sections.filter((section) => section.resources.length > 0);
}
