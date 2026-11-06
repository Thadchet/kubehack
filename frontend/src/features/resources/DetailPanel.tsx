import {
  Cable,
  Maximize2,
  Pencil,
  RotateCcw,
  ScrollText,
  SquareTerminal,
  Trash2,
  X,
} from "lucide-react";
import { useMemo, useState } from "react";
import { stringify } from "yaml";
import type { ActionType } from "../actions/ActionDialog";
import type { APIResource } from "../cluster/useCluster";
import { dataKind } from "../editor/dataValues";
import { formatAge, formatCpu, formatMemory } from "./columns";
import { DataSection } from "./DataSection";
import type { ObjectUsage } from "./useMetrics";
import { useResourceWatch, type KubeObject, type ResourceRef } from "./useResourceWatch";

const eventsRef: ResourceRef = { group: "", version: "v1", resource: "events" };

type Tab = "overview" | "yaml" | "events";

interface Props {
  context: string;
  object: KubeObject;
  resource: APIResource;
  usage?: ObjectUsage;
  onClose: () => void;
  onOpenLogs?: () => void;
  onOpenShell?: () => void;
  onForward?: () => void;
  onEdit?: (revealed?: boolean) => void;
  // Missing while the context is read-only.
  onAction?: (type: ActionType) => void;
}

type Fields = Record<string, unknown>;

function fields(value: unknown): Fields[] {
  return Array.isArray(value) ? (value as Fields[]) : [];
}

function Pairs({ title, values }: { title: string; values?: Record<string, string> }) {
  const entries = Object.entries(values ?? {});
  if (entries.length === 0) {
    return null;
  }
  return (
    <section className="detail-section">
      <h3>{title}</h3>
      <dl className="pairs">
        {entries.map(([key, value]) => (
          <div key={key}>
            <dt>{key}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

interface OverviewProps {
  context: string;
  resource: APIResource;
  object: KubeObject;
  usage?: ObjectUsage;
  onEdit?: (revealed: boolean) => void;
}

function usageText(usage: { cpu: number; memory: number }): string {
  return `CPU ${formatCpu(usage.cpu)}, memory ${formatMemory(usage.memory)}`;
}

function Overview({ context, resource, object, usage, onEdit }: OverviewProps) {
  const { metadata } = object;
  const status = object.status as Fields | undefined;
  const conditions = fields(status?.conditions);
  const containers = fields(status?.containerStatuses);
  const kind = dataKind(resource);

  return (
    <>
      <section className="detail-section">
        <dl className="facts">
          {metadata.namespace && (
            <>
              <dt>Namespace</dt>
              <dd>{metadata.namespace}</dd>
            </>
          )}
          {usage && (
            <>
              <dt>Using</dt>
              <dd data-testid="detail-usage">{usageText(usage)}</dd>
            </>
          )}
          <dt>Created</dt>
          <dd>
            {metadata.creationTimestamp && new Date(metadata.creationTimestamp).toLocaleString()}
          </dd>
          {metadata.ownerReferences?.map((owner) => (
            <div className="fact-row" key={`${owner.kind}/${owner.name}`}>
              <dt>Owned by</dt>
              <dd>
                {owner.kind} {owner.name}
              </dd>
            </div>
          ))}
        </dl>
      </section>

      {kind && (
        <DataSection
          kind={kind}
          context={context}
          resource={resource}
          object={object}
          onEdit={onEdit}
        />
      )}

      {containers.length > 0 && (
        <section className="detail-section">
          <h3>Containers</h3>
          <ul className="plain">
            {containers.map((container) => {
              const used = usage?.containers?.find((item) => item.name === container.name);
              return (
                <li key={String(container.name)}>
                  <strong>{String(container.name)}</strong>
                  <span data-tone={container.ready ? "good" : "warn"}>
                    {container.ready ? " ready" : " not ready"}
                  </span>
                  {Number(container.restartCount) > 0 &&
                    `, ${String(container.restartCount)} restarts`}
                  <div className="muted wrap">{String(container.image)}</div>
                  {used && <div className="muted">{usageText(used)}</div>}
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {conditions.length > 0 && (
        <section className="detail-section">
          <h3>Conditions</h3>
          <ul className="plain">
            {conditions.map((condition) => (
              <li key={String(condition.type)}>
                {String(condition.type)}{" "}
                <span data-tone={condition.status === "True" ? "good" : "neutral"}>
                  {String(condition.status)}
                </span>
                {typeof condition.message === "string" && condition.message && (
                  <div className="muted wrap">{condition.message}</div>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      <Pairs title="Labels" values={metadata.labels} />
      <Pairs title="Annotations" values={metadata.annotations} />
    </>
  );
}

function Events({ context, object }: { context: string; object: KubeObject }) {
  // Events of cluster-scoped objects, such as nodes, live in "default".
  const watch = useResourceWatch(context, eventsRef, object.metadata.namespace ?? "default");
  const now = Date.now();

  const events = useMemo(
    () =>
      watch.items
        .filter((event) => (event.involvedObject as Fields)?.uid === object.metadata.uid)
        .sort((a, b) => eventTime(b).localeCompare(eventTime(a))),
    [watch.items, object.metadata.uid],
  );

  if (watch.error) {
    return (
      <p className="message" data-tone="bad">
        {watch.error}
      </p>
    );
  }
  if (!watch.synced) {
    return <p className="message muted">Loading events</p>;
  }
  if (events.length === 0) {
    return (
      <p className="message muted">No recent events. Kubernetes keeps them for about an hour.</p>
    );
  }
  return (
    <ul className="events">
      {events.map((event) => (
        <li key={event.metadata.name}>
          <div>
            <strong data-tone={event.type === "Warning" ? "warn" : undefined}>
              {String(event.reason)}
            </strong>
            <span className="muted"> {formatAge(eventTime(event), now)} ago</span>
            {Number(event.count) > 1 && (
              <span className="muted">, {String(event.count)} times</span>
            )}
          </div>
          <div className="wrap">{String(event.message)}</div>
        </li>
      ))}
    </ul>
  );
}

function eventTime(event: KubeObject): string {
  return String(event.lastTimestamp ?? event.eventTime ?? event.metadata.creationTimestamp ?? "");
}

export function DetailPanel({
  context,
  object,
  resource,
  usage,
  onClose,
  onOpenLogs,
  onOpenShell,
  onForward,
  onEdit,
  onAction,
}: Props) {
  const kindLabel = resource.kind;
  const [tab, setTab] = useState<Tab>("overview");
  const yaml = useMemo(() => (tab === "yaml" ? stringify(object) : ""), [tab, object]);

  return (
    <aside className="detail" aria-label={`${kindLabel} details`}>
      <header className="detail-head">
        <div>
          <p className="muted">{kindLabel}</p>
          <h2 className="wrap">{object.metadata.name}</h2>
          <div className="detail-actions">
            {onOpenLogs && (
              <button type="button" className="action" onClick={onOpenLogs}>
                <ScrollText size={14} aria-hidden="true" />
                Logs <kbd>l</kbd>
              </button>
            )}
            {onOpenShell && (
              <button type="button" className="action" onClick={onOpenShell}>
                <SquareTerminal size={14} aria-hidden="true" />
                Shell <kbd>s</kbd>
              </button>
            )}
            {onForward && (
              <button type="button" className="action" onClick={onForward}>
                <Cable size={14} aria-hidden="true" />
                Forward <kbd>f</kbd>
              </button>
            )}
            {onEdit && (
              <button type="button" className="action" onClick={() => onEdit()}>
                <Pencil size={14} aria-hidden="true" />
                Edit <kbd>e</kbd>
              </button>
            )}
            {onAction && resource.scalable && (
              <button type="button" className="action" onClick={() => onAction("scale")}>
                <Maximize2 size={14} aria-hidden="true" />
                Scale
              </button>
            )}
            {onAction && resource.restartable && (
              <button type="button" className="action" onClick={() => onAction("restart")}>
                <RotateCcw size={14} aria-hidden="true" />
                Restart
              </button>
            )}
            {onAction && resource.deletable && (
              <button
                type="button"
                className="action"
                data-tone="bad"
                onClick={() => onAction("delete")}
              >
                <Trash2 size={14} aria-hidden="true" />
                Delete
              </button>
            )}
          </div>
        </div>
        <button type="button" className="quiet" onClick={onClose} aria-label="Close details">
          <X size={16} aria-hidden="true" />
        </button>
      </header>
      <div className="tabs" role="tablist">
        {(["overview", "yaml", "events"] as const).map((item) => (
          <button
            key={item}
            type="button"
            role="tab"
            aria-selected={tab === item}
            onClick={() => setTab(item)}
          >
            {item === "yaml" ? "YAML" : item[0].toUpperCase() + item.slice(1)}
          </button>
        ))}
      </div>
      <div className="detail-body">
        {tab === "overview" && (
          <Overview
            context={context}
            resource={resource}
            object={object}
            usage={usage}
            onEdit={onEdit}
          />
        )}
        {tab === "yaml" && (
          <>
            {object.kind === "Secret" && (
              <p className="muted">Values are hidden here, so they appear empty.</p>
            )}
            <pre className="yaml">{yaml}</pre>
          </>
        )}
        {tab === "events" && <Events context={context} object={object} />}
      </div>
    </aside>
  );
}
