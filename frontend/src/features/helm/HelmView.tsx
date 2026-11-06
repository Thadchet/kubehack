import { useEffect, useMemo, useRef, useState } from "react";
import { formatAge, type Column } from "../resources/columns";
import { ResourceTable, sortRows } from "../resources/ResourceTable";
import {
  objectKey,
  useResourceWatch,
  type KubeObject,
  type ResourceRef,
} from "../resources/useResourceWatch";
import { ReleaseConfirm } from "./ReleaseConfirm";
import { ReleaseDialog } from "./ReleaseDialog";
import { ReleasePanel } from "./ReleasePanel";
import { statusTone, useReleases, type Release } from "./useReleases";

const namespacesRef: ResourceRef = { group: "", version: "v1", resource: "namespaces" };
const defaultSort = { column: "name", descending: false };

interface Props {
  context: string;
  namespace: string;
  onNamespace: (namespace: string) => void;
  readOnly: boolean;
}

type Dialog =
  | { type: "install" }
  | { type: "upgrade"; release: Release }
  | { type: "uninstall"; release: Release }
  | { type: "rollback"; release: Release; revision: number };

// Rows take the shape of a Kubernetes object so that the resource table can show
// them. Releases aren't objects, but each has a name, a namespace and an age.
function toRow(release: Release): KubeObject {
  return {
    metadata: {
      name: release.name,
      namespace: release.namespace,
      creationTimestamp: release.updated,
    },
    release,
  };
}

function releaseOf(row: KubeObject): Release {
  return row.release as Release;
}

function columnsFor(showNamespace: boolean): Column[] {
  const columns: Column[] = [
    {
      id: "name",
      label: "Name",
      width: "minmax(140px, 1.2fr)",
      text: (item) => item.metadata.name,
    },
  ];
  if (showNamespace) {
    columns.push({
      id: "namespace",
      label: "Namespace",
      width: "minmax(100px, 0.5fr)",
      text: (item) => item.metadata.namespace ?? "",
    });
  }
  columns.push(
    {
      id: "status",
      label: "Status",
      width: "120px",
      text: (item) => releaseOf(item).status,
      tone: (item) => statusTone(releaseOf(item).status),
    },
    {
      id: "chart",
      label: "Chart",
      width: "minmax(100px, 0.7fr)",
      text: (item) => `${releaseOf(item).chart} ${releaseOf(item).chartVersion}`,
    },
    {
      id: "app",
      label: "App version",
      width: "84px",
      text: (item) => releaseOf(item).appVersion,
    },
    {
      id: "revision",
      label: "Revision",
      width: "72px",
      numeric: true,
      text: (item) => String(releaseOf(item).revision),
      sortValue: (item) => releaseOf(item).revision,
    },
    {
      id: "updated",
      label: "Updated",
      width: "72px",
      numeric: true,
      text: (item, now) => formatAge(item.metadata.creationTimestamp, now),
      sortValue: (item) => -Date.parse(item.metadata.creationTimestamp ?? "0"),
    },
  );
  return columns;
}

export function HelmView({ context, namespace, onNamespace, readOnly }: Props) {
  const { releases, failure, refresh } = useReleases(context, namespace);
  const namespaces = useResourceWatch(context, namespacesRef);
  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<string>();
  const [sort, setSort] = useState(defaultSort);
  const [dialog, setDialog] = useState<Dialog>();
  const filterInput = useRef<HTMLInputElement>(null);

  const columns = useMemo(() => columnsFor(!namespace), [namespace]);
  const rows = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    const matching = (releases ?? [])
      .filter((item) => item.name.toLowerCase().includes(needle))
      .map(toRow);
    return sortRows(matching, columns, sort);
  }, [releases, filter, columns, sort]);

  const selectedRow = rows.find((row) => objectKey(row) === selected);
  const selectedRelease = selectedRow && releaseOf(selectedRow);

  useEffect(() => {
    setSelected(undefined);
    setDialog(undefined);
  }, [context, namespace]);

  // Turning on read-only closes anything that could still write.
  useEffect(() => {
    if (readOnly) {
      setDialog(undefined);
    }
  }, [readOnly]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as Element | null;
      if (dialog || target?.closest?.(".dock")) {
        return;
      }
      if (event.key === "Escape") {
        setSelected(undefined);
        return;
      }
      if (target?.matches?.("input, select, textarea") || event.metaKey || event.ctrlKey) {
        return;
      }
      if (event.key === "/") {
        event.preventDefault();
        filterInput.current?.focus();
        return;
      }
      const step = { ArrowDown: 1, j: 1, ArrowUp: -1, k: -1 }[event.key];
      if (step && rows.length > 0) {
        event.preventDefault();
        const current = rows.findIndex((row) => objectKey(row) === selected);
        const next = current < 0 ? 0 : Math.min(rows.length - 1, Math.max(0, current + step));
        setSelected(objectKey(rows[next]));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dialog, rows, selected]);

  const namespaceNames = useMemo(
    () => namespaces.items.map((item) => item.metadata.name).sort(),
    [namespaces.items],
  );
  const place = namespace ? `the ${namespace} namespace` : "this cluster";

  return (
    <div className="view">
      <div className="main">
        <header className="toolbar">
          <h1>Helm releases</h1>
          <span className="count" data-testid="row-count">
            {rows.length.toLocaleString()}
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
          <select
            className="namespace"
            aria-label="Namespace"
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
          {!readOnly && (
            <button
              type="button"
              className="action helm-install"
              data-testid="helm-install"
              onClick={() => setDialog({ type: "install" })}
            >
              Install a chart
            </button>
          )}
        </header>

        {failure && (
          <p className="banner" role="alert">
            {failure.kind === "forbidden"
              ? `You don't have permission to read Helm releases in ${place}.`
              : failure.message}
          </p>
        )}

        {releases === undefined ? (
          !failure && <p className="message muted">Loading releases</p>
        ) : rows.length === 0 ? (
          <p className="message muted">
            {filter ? `No releases match “${filter}”.` : `No Helm releases in ${place}.`}
          </p>
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

      {selectedRelease && (
        <ReleasePanel
          key={selected}
          context={context}
          release={selectedRelease}
          onClose={() => setSelected(undefined)}
          onUpgrade={
            readOnly ? undefined : () => setDialog({ type: "upgrade", release: selectedRelease })
          }
          onUninstall={
            readOnly ? undefined : () => setDialog({ type: "uninstall", release: selectedRelease })
          }
          onRollback={
            readOnly
              ? undefined
              : (revision) => setDialog({ type: "rollback", release: selectedRelease, revision })
          }
        />
      )}

      {(dialog?.type === "install" || dialog?.type === "upgrade") && (
        <ReleaseDialog
          context={context}
          release={dialog.type === "upgrade" ? dialog.release : undefined}
          defaultNamespace={namespace || "default"}
          onClose={() => setDialog(undefined)}
          onChanged={refresh}
        />
      )}

      {(dialog?.type === "uninstall" || dialog?.type === "rollback") && (
        <ReleaseConfirm
          context={context}
          release={dialog.release}
          revision={dialog.type === "rollback" ? dialog.revision : undefined}
          onClose={() => setDialog(undefined)}
          onChanged={refresh}
        />
      )}
    </div>
  );
}
