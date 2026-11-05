import { useEffect, useMemo, useRef, useState } from "react";
import type { APIResource } from "../cluster/useCluster";
import { columnsFor, hasPhase } from "./columns";
import { DetailPanel } from "./DetailPanel";
import { resourceId, resourceLabel } from "./kinds";
import { ResourceTable, sortRows } from "./ResourceTable";
import {
  objectKey,
  useResourceWatch,
  type KubeObject,
  type ResourceRef,
} from "./useResourceWatch";

const namespacesRef: ResourceRef = { group: "", version: "v1", resource: "namespaces" };
const defaultSort = { column: "name", descending: false };

interface Props {
  context: string;
  resource: APIResource;
  namespace: string;
  onNamespace: (namespace: string) => void;
  onOpenLogs: (pod: KubeObject) => void;
}

function isTyping(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    (target.tagName === "INPUT" || target.tagName === "SELECT" || target.tagName === "TEXTAREA")
  );
}

export function ResourceView({ context, resource, namespace, onNamespace, onOpenLogs }: Props) {
  const scope = resource.namespaced ? namespace : "";
  const watch = useResourceWatch(context, resource, scope);
  const namespaces = useResourceWatch(context, namespacesRef);

  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<string>();
  const [sort, setSort] = useState(defaultSort);
  const filterInput = useRef<HTMLInputElement>(null);

  const id = resourceId(resource);
  const label = resourceLabel(resource);
  const showPhase = useMemo(() => hasPhase(watch.items), [watch.items]);
  const columns = useMemo(
    () => columnsFor(id, resource.namespaced && !scope, showPhase),
    [id, resource.namespaced, scope, showPhase],
  );

  useEffect(() => {
    setSelected(undefined);
    setFilter("");
    setSort(defaultSort);
  }, [context, id, scope]);

  const rows = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    const matching = needle
      ? watch.items.filter((item) => item.metadata.name.toLowerCase().includes(needle))
      : watch.items;
    return sortRows(matching, columns, sort);
  }, [watch.items, filter, columns, sort]);

  // The panel closes by itself if the object is deleted while open.
  const selectedObject = watch.items.find((item) => objectKey(item) === selected);
  const isPods = id === "/pods";

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        if (document.activeElement === filterInput.current) {
          setFilter("");
          filterInput.current?.blur();
        } else {
          setSelected(undefined);
        }
        return;
      }
      if (isTyping(event.target) || event.metaKey || event.ctrlKey || event.altKey) {
        return;
      }
      if (event.key === "/") {
        event.preventDefault();
        filterInput.current?.focus();
        return;
      }
      if (event.key === "l" && isPods && selectedObject) {
        onOpenLogs(selectedObject);
        return;
      }
      const step = { ArrowDown: 1, j: 1, ArrowUp: -1, k: -1 }[event.key];
      if (step && rows.length > 0) {
        event.preventDefault();
        const current = rows.findIndex((item) => objectKey(item) === selected);
        const next = current < 0 ? 0 : Math.min(rows.length - 1, Math.max(0, current + step));
        setSelected(objectKey(rows[next]));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [rows, selected, selectedObject, isPods, onOpenLogs]);

  const namespaceNames = useMemo(
    () => namespaces.items.map((item) => item.metadata.name).sort(),
    [namespaces.items],
  );

  const place = scope ? `the ${scope} namespace` : "this cluster";
  const forbidden = watch.errorReason === "Forbidden";

  return (
    <div className="view">
      <div className="main">
        <header className="toolbar">
          <h1>{label}</h1>
          <span className="count" data-testid="row-count">
            {filter ? `${rows.length.toLocaleString()} of ` : ""}
            {watch.items.length.toLocaleString()}
          </span>
          <input
            ref={filterInput}
            type="search"
            className="filter"
            placeholder="Filter by name ( / )"
            aria-label="Filter by name"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
          {resource.namespaced &&
            (namespaces.error ? (
              // Users who cannot list namespaces can still name one they can read.
              <input
                className="namespace"
                placeholder="Namespace"
                aria-label="Namespace"
                defaultValue={namespace}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    onNamespace(event.currentTarget.value.trim());
                  }
                }}
              />
            ) : (
              <select
                className="namespace"
                aria-label="Namespace"
                data-testid="namespace-select"
                value={namespace}
                onChange={(event) => onNamespace(event.target.value)}
              >
                <option value="">All namespaces</option>
                {namespace && !namespaceNames.includes(namespace) && (
                  <option value={namespace}>{namespace}</option>
                )}
                {namespaceNames.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
            ))}
          <span
            className="liveness"
            data-tone={!watch.connected || watch.error ? "bad" : "good"}
            data-testid="watch-state"
          >
            {!watch.connected ? "Reconnecting" : watch.error ? "Not updating" : "Live"}
          </span>
        </header>

        {watch.error && (
          <p className="banner" role="alert">
            {forbidden
              ? `You don't have permission to read ${label.toLowerCase()} in ${place}.${
                  resource.namespaced && !scope ? " Choose a namespace you can read." : ""
                }`
              : watch.error}
          </p>
        )}

        {watch.synced && rows.length === 0 ? (
          <p className="message muted">
            {filter
              ? `No ${label.toLowerCase()} match “${filter}”.`
              : `No ${label.toLowerCase()} in ${place}.`}
          </p>
        ) : !watch.synced && watch.items.length === 0 ? (
          !watch.error && <p className="message muted">Loading {label.toLowerCase()}</p>
        ) : (
          <ResourceTable
            columns={columns}
            rows={rows}
            selected={selected}
            onSelect={setSelected}
            sort={sort}
            onSort={setSort}
          />
        )}
      </div>

      {selectedObject && (
        <DetailPanel
          key={selected}
          context={context}
          object={selectedObject}
          kindLabel={resource.kind}
          onClose={() => setSelected(undefined)}
          onOpenLogs={isPods ? () => onOpenLogs(selectedObject) : undefined}
        />
      )}
    </div>
  );
}
