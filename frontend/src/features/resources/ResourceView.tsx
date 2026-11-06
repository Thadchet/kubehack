import { useEffect, useMemo, useRef, useState } from "react";
import type { APIResource } from "../cluster/useCluster";
import { ActionDialog, type ActionType } from "../actions/ActionDialog";
import type { DockKind } from "../dock/Dock";
import { EditDialog } from "../editor/EditDialog";
import { ForwardDialog } from "../portforward/ForwardDialog";
import { columnsFor, hasPhase } from "./columns";
import { DetailPanel } from "./DetailPanel";
import { resourceId, resourceLabel } from "./kinds";
import { ResourceTable, sortRows } from "./ResourceTable";
import { useMetrics, type MetricsKind } from "./useMetrics";
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
  onOpenDock: (kind: DockKind, pod: KubeObject) => void;
  onForwardStarted: () => void;
  readOnly: boolean;
}

function isTyping(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    (target.isContentEditable ||
      target.tagName === "INPUT" ||
      target.tagName === "SELECT" ||
      target.tagName === "TEXTAREA")
  );
}

export function ResourceView({
  context,
  resource,
  namespace,
  onNamespace,
  onOpenDock,
  onForwardStarted,
  readOnly,
}: Props) {
  const scope = resource.namespaced ? namespace : "";
  const watch = useResourceWatch(context, resource, scope);
  const namespaces = useResourceWatch(context, namespacesRef);

  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<string>();
  const [sort, setSort] = useState(defaultSort);
  const [editing, setEditing] = useState<KubeObject>();
  const [action, setAction] = useState<{ type: ActionType; object: KubeObject }>();
  const [forwarding, setForwarding] = useState<KubeObject>();
  const filterInput = useRef<HTMLInputElement>(null);

  const id = resourceId(resource);
  const label = resourceLabel(resource);
  const showPhase = useMemo(() => hasPhase(watch.items), [watch.items]);
  const metricsKind: MetricsKind | undefined =
    id === "/pods" ? "pods" : id === "/nodes" ? "nodes" : undefined;
  const metrics = useMetrics(context, metricsKind, scope);
  const columns = useMemo(
    () => columnsFor(id, resource.namespaced && !scope, showPhase, metrics.usage),
    [id, resource.namespaced, scope, showPhase, metrics.usage],
  );

  useEffect(() => {
    setSelected(undefined);
    setFilter("");
    setSort(defaultSort);
    setEditing(undefined);
    setAction(undefined);
    setForwarding(undefined);
  }, [context, id, scope]);

  // Turning on read-only closes anything that could still write.
  useEffect(() => {
    if (readOnly) {
      setEditing(undefined);
      setAction(undefined);
      setForwarding(undefined);
    }
  }, [readOnly]);

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
  const forwardKind = isPods ? "pod" : id === "/services" ? "service" : undefined;

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      // Keys in the dock belong to it. Esc in particular is everyday input in a
      // terminal, for example in vim.
      const inDock = event.target instanceof Element && event.target.closest(".dock");
      if (editing || action || forwarding || inDock) {
        return;
      }
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
        onOpenDock("logs", selectedObject);
        return;
      }
      if (event.key === "s" && isPods && !readOnly && selectedObject) {
        onOpenDock("terminal", selectedObject);
        return;
      }
      if (event.key === "f" && forwardKind && !readOnly && selectedObject) {
        // The dialog focuses a field before this key would be typed.
        event.preventDefault();
        setForwarding(selectedObject);
        return;
      }
      if (event.key === "e" && resource.editable && !readOnly && selectedObject) {
        setEditing(selectedObject);
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
  }, [
    rows,
    selected,
    selectedObject,
    isPods,
    forwardKind,
    onOpenDock,
    editing,
    action,
    forwarding,
    resource.editable,
    readOnly,
  ]);

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

        {metrics.note && (
          <p className="metrics-note" data-testid="metrics-note">
            {metrics.note}
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
          resource={resource}
          usage={selected ? metrics.usage?.get(selected) : undefined}
          onClose={() => setSelected(undefined)}
          onOpenLogs={isPods ? () => onOpenDock("logs", selectedObject) : undefined}
          onOpenShell={
            isPods && !readOnly ? () => onOpenDock("terminal", selectedObject) : undefined
          }
          onForward={forwardKind && !readOnly ? () => setForwarding(selectedObject) : undefined}
          onEdit={resource.editable && !readOnly ? () => setEditing(selectedObject) : undefined}
          onAction={readOnly ? undefined : (type) => setAction({ type, object: selectedObject })}
        />
      )}

      {editing && (
        <EditDialog
          context={context}
          resource={resource}
          object={editing}
          onClose={() => setEditing(undefined)}
        />
      )}

      {forwarding && forwardKind && (
        <ForwardDialog
          kind={forwardKind}
          context={context}
          object={forwarding}
          onClose={() => setForwarding(undefined)}
          onStarted={onForwardStarted}
        />
      )}

      {action && (
        <ActionDialog
          type={action.type}
          context={context}
          resource={resource}
          object={action.object}
          onClose={() => setAction(undefined)}
        />
      )}
    </div>
  );
}
