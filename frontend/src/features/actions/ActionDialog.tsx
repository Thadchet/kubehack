import { useState, type FormEvent } from "react";
import { apiPost } from "../../api";
import { contextColor } from "../cluster/contextColor";
import type { APIResource } from "../cluster/useCluster";
import type { KubeObject } from "../resources/useResourceWatch";
import { toFailure, type Failure } from "./failure";

export type ActionType = "delete" | "scale" | "restart";

interface Props {
  type: ActionType;
  context: string;
  resource: APIResource;
  object: KubeObject;
  onClose: () => void;
}

function currentReplicas(object: KubeObject): number {
  const replicas = (object.spec as { replicas?: unknown } | undefined)?.replicas;
  return typeof replicas === "number" ? replicas : 0;
}

function failureText(failure: Failure, type: ActionType): string {
  switch (failure.kind) {
    case "stale":
      return `This object was replaced after you opened it, so nothing was ${
        type === "delete" ? "deleted" : "changed"
      }. Check the new one first.`;
    case "gone":
      return "This object no longer exists.";
    default:
      return failure.message;
  }
}

export function ActionDialog({ type, context, resource, object, onClose }: Props) {
  const { name, namespace = "", uid = "" } = object.metadata;
  const [typedName, setTypedName] = useState("");
  const current = currentReplicas(object);
  const [replicas, setReplicas] = useState(String(current));
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<Failure>();

  const parsedReplicas = Number(replicas);
  const validReplicas =
    replicas.trim() !== "" && Number.isInteger(parsedReplicas) && parsedReplicas >= 0;

  const ready =
    !busy &&
    (type === "delete"
      ? typedName === name
      : type === "scale"
        ? validReplicas && parsedReplicas !== current
        : true);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!ready) {
      return;
    }
    setBusy(true);
    setFailure(undefined);
    try {
      await apiPost(`/api/v1/contexts/${encodeURIComponent(context)}/${type}`, {
        group: resource.group,
        version: resource.version,
        resource: resource.resource,
        namespace,
        name,
        uid,
        replicas: parsedReplicas,
      });
      onClose();
    } catch (error) {
      setFailure(toFailure(error));
      setBusy(false);
    }
  };

  const label =
    type === "delete"
      ? `Delete from ${context}`
      : type === "scale"
        ? `Scale to ${validReplicas ? parsedReplicas : "…"} on ${context}`
        : `Restart on ${context}`;

  return (
    <div className="scrim">
      <form
        className="confirm"
        role="alertdialog"
        aria-label={`${type} ${resource.kind} ${name}`}
        data-testid="action-dialog"
        onSubmit={submit}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            onClose();
          }
        }}
      >
        <div className="confirm-band" style={{ backgroundColor: contextColor(context) }}>
          {context}
        </div>
        <div className="confirm-body">
          <h2>
            {type === "delete" ? "Delete" : type === "scale" ? "Scale" : "Restart"} {resource.kind}
          </h2>
          <p className="wrap">
            <strong>{name}</strong>
            {namespace && ` in ${namespace}`}
          </p>

          {type === "delete" && (
            <label className="confirm-field">
              This can't be undone. Type the name to confirm.
              <input
                autoFocus
                aria-label="Type the name to confirm"
                autoComplete="off"
                spellCheck={false}
                value={typedName}
                onChange={(event) => setTypedName(event.target.value)}
              />
            </label>
          )}

          {type === "scale" && (
            <label className="confirm-field">
              Replicas, currently {current}
              <input
                autoFocus
                type="number"
                min={0}
                aria-label="Replicas"
                value={replicas}
                onChange={(event) => setReplicas(event.target.value)}
              />
              {validReplicas && parsedReplicas === 0 && (
                <span data-tone="warn">This stops every pod of this {resource.kind}.</span>
              )}
            </label>
          )}

          {type === "restart" && (
            <p className="muted">
              Pods are replaced following this {resource.kind}'s rollout settings.
            </p>
          )}

          {failure && (
            <p className="wrap" role="alert" data-tone="bad" data-testid="action-failure">
              {failureText(failure, type)}
            </p>
          )}
        </div>
        <footer className="edit-actions">
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className={type === "delete" ? "danger" : "primary"}
            disabled={!ready}
            data-testid="action-submit"
          >
            {label}
          </button>
        </footer>
      </form>
    </div>
  );
}
