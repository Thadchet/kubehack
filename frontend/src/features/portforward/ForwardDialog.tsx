import { useState, type FormEvent } from "react";
import { apiPost } from "../../api";
import { toFailure, type Failure } from "../actions/failure";
import { contextColor } from "../cluster/contextColor";
import type { KubeObject } from "../resources/useResourceWatch";

export type ForwardKind = "pod" | "service";

interface Props {
  kind: ForwardKind;
  context: string;
  object: KubeObject;
  onClose: () => void;
  onStarted: () => void;
}

interface KnownPort {
  port: number;
  label: string;
}

type Fields = Record<string, unknown>;

function list(value: unknown): Fields[] {
  return Array.isArray(value) ? (value as Fields[]) : [];
}

function tcp(port: Fields): boolean {
  return port.protocol === undefined || port.protocol === "TCP";
}

// Port-forwarding only carries TCP, so UDP ports are left out.
function knownPorts(kind: ForwardKind, object: KubeObject): KnownPort[] {
  const spec = object.spec as Fields | undefined;
  const ports =
    kind === "service"
      ? list(spec?.ports).filter(tcp).map((port) => ({ port: port.port, name: port.name }))
      : list(spec?.containers).flatMap((container) =>
          list(container.ports)
            .filter(tcp)
            .map((port) => ({ port: port.containerPort, name: port.name })),
        );
  return ports.map(({ port, name }) => ({
    port: Number(port),
    label: name ? `${String(port)} (${String(name)})` : String(port),
  }));
}

function validPort(value: string, allowEmpty: boolean): boolean {
  if (value.trim() === "") {
    return allowEmpty;
  }
  const port = Number(value);
  return Number.isInteger(port) && port >= 1 && port <= 65535;
}

function failureText(failure: Failure, localPort: string): string {
  switch (failure.kind) {
    case "portInUse":
      return `Port ${localPort} is already in use on this computer. Choose another, or leave it empty.`;
    case "gone":
      return "This object no longer exists.";
    default:
      return failure.message;
  }
}

export function ForwardDialog({ kind, context, object, onClose, onStarted }: Props) {
  const { name, namespace = "default" } = object.metadata;
  const ports = knownPorts(kind, object);
  const [port, setPort] = useState(ports[0] ? String(ports[0].port) : "");
  const [localPort, setLocalPort] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<Failure>();

  const ready = !busy && validPort(port, false) && validPort(localPort, true);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!ready) {
      return;
    }
    setBusy(true);
    setFailure(undefined);
    try {
      await apiPost(`/api/v1/contexts/${encodeURIComponent(context)}/portforwards`, {
        namespace,
        kind,
        name,
        port: Number(port),
        localPort: Number(localPort),
      });
      onStarted();
      onClose();
    } catch (error) {
      setFailure(toFailure(error));
      setBusy(false);
    }
  };

  return (
    <div className="scrim">
      <form
        className="confirm"
        role="dialog"
        aria-label={`Forward a port to ${kind} ${name}`}
        data-testid="forward-dialog"
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
          <h2>Forward a port</h2>
          <p className="wrap">
            <strong>{name}</strong> in {namespace}
          </p>

          <label className="confirm-field">
            {kind === "service" ? "Service port" : "Pod port"}
            <input
              autoFocus
              inputMode="numeric"
              aria-label={kind === "service" ? "Service port" : "Pod port"}
              autoComplete="off"
              value={port}
              onChange={(event) => setPort(event.target.value)}
            />
          </label>
          {ports.length > 1 && (
            <div className="forward-ports">
              {ports.map((item) => (
                <button
                  key={item.label}
                  type="button"
                  className="action"
                  aria-pressed={String(item.port) === port}
                  onClick={() => setPort(String(item.port))}
                >
                  {item.label}
                </button>
              ))}
            </div>
          )}

          <label className="confirm-field">
            Local port
            <input
              inputMode="numeric"
              aria-label="Local port"
              placeholder="Any free port"
              autoComplete="off"
              value={localPort}
              onChange={(event) => setLocalPort(event.target.value)}
            />
          </label>
          <p className="muted">Only this computer can connect.</p>

          {failure && (
            <p className="wrap" role="alert" data-tone="bad" data-testid="forward-failure">
              {failureText(failure, localPort)}
            </p>
          )}
        </div>
        <footer className="edit-actions">
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="primary" disabled={!ready} data-testid="forward-submit">
            {busy ? "Connecting" : "Start forwarding"}
          </button>
        </footer>
      </form>
    </div>
  );
}
