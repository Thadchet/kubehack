import { useCallback, useEffect, useState } from "react";
import { apiGet, apiPost, ApiError } from "../../api";
import { contextColor } from "../cluster/contextColor";
import type { APIResource } from "../cluster/useCluster";
import type { KubeObject } from "../resources/useResourceWatch";
import { CodeView } from "./CodeView";

interface EditableObject {
  yaml: string;
  resourceVersion: string;
  uid: string;
}

interface ApplyResult {
  before: string;
  after: string;
  applied: boolean;
}

interface Failure {
  kind: "fieldConflict" | "stale" | "gone" | "invalid" | "forbidden" | "failed";
  message: string;
  conflicts?: { field: string; message: string }[];
}

interface Props {
  context: string;
  resource: APIResource;
  object: KubeObject;
  onClose: () => void;
}

function toFailure(error: unknown): Failure {
  if (error instanceof ApiError) {
    try {
      const parsed = JSON.parse(error.body) as Failure;
      if (parsed.kind) {
        return parsed;
      }
    } catch {
      // Plain-text bodies come from request validation.
    }
    return { kind: "failed", message: error.body };
  }
  return { kind: "failed", message: (error as Error).message };
}

export function EditDialog({ context, resource, object, onClose }: Props) {
  const { name, namespace = "" } = object.metadata;
  const isSecret = resource.group === "" && resource.resource === "secrets";

  // Secret values leave the server only after the user asks for them.
  const [revealed, setRevealed] = useState(!isSecret);
  const [loaded, setLoaded] = useState<EditableObject>();
  // Bumping this remounts the editor with freshly loaded text.
  const [loadCount, setLoadCount] = useState(0);
  const [text, setText] = useState("");
  const [preview, setPreview] = useState<ApplyResult>();
  const [force, setForce] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<Failure>();

  const load = useCallback(async () => {
    setBusy(true);
    setFailure(undefined);
    setPreview(undefined);
    setForce(false);
    try {
      const query = new URLSearchParams({
        group: resource.group,
        version: resource.version,
        resource: resource.resource,
        namespace,
        name,
      });
      const next = await apiGet<EditableObject>(
        `/api/v1/contexts/${encodeURIComponent(context)}/object?${query}`,
      );
      setLoaded(next);
      setText(next.yaml);
      setLoadCount((value) => value + 1);
    } catch (error) {
      setFailure(toFailure(error));
    } finally {
      setBusy(false);
    }
  }, [context, resource, namespace, name]);

  useEffect(() => {
    if (revealed) {
      void load();
    }
  }, [revealed, load]);

  const submit = async (dryRun: boolean, useForce: boolean) => {
    if (!loaded) {
      return;
    }
    setBusy(true);
    setFailure(undefined);
    try {
      const result = await apiPost<ApplyResult>(
        `/api/v1/contexts/${encodeURIComponent(context)}/apply`,
        {
          group: resource.group,
          version: resource.version,
          resource: resource.resource,
          namespace,
          name,
          yaml: text,
          resourceVersion: loaded.resourceVersion,
          uid: loaded.uid,
          dryRun,
          force: useForce,
        },
      );
      if (result.applied) {
        onClose();
        return;
      }
      setForce(useForce);
      setPreview(result);
    } catch (error) {
      setPreview(undefined);
      setFailure(toFailure(error));
    } finally {
      setBusy(false);
    }
  };

  const unchanged = preview !== undefined && preview.before === preview.after;
  const applyStyle = { backgroundColor: contextColor(context) };

  return (
    <div className="edit" role="dialog" aria-label={`Edit ${resource.kind} ${name}`}>
      <header className="edit-head">
        <h2 className="wrap">
          {preview ? "Review changes to" : "Edit"} {resource.kind} {name}
        </h2>
        <span className="edit-target">
          <span className="dock-dot" aria-hidden="true" style={applyStyle} />
          {context}
          {namespace && `, ${namespace}`}
        </span>
      </header>

      {failure && (
        <div className="edit-failure" role="alert" data-testid="edit-failure" data-kind={failure.kind}>
          {failure.kind === "fieldConflict" ? (
            <>
              <p>Another tool manages these fields, so this change was not applied.</p>
              <ul className="plain">
                {failure.conflicts?.map((conflict) => (
                  <li key={conflict.field}>
                    <strong>{conflict.field}</strong>{" "}
                    <span className="muted">{conflict.message}</span>
                  </li>
                ))}
              </ul>
              <p className="muted">
                Forcing takes these fields over. The other tool may change them back.
              </p>
            </>
          ) : failure.kind === "stale" ? (
            <p>
              This object changed after you opened it, so nothing was applied. Reload to edit the
              latest version.
            </p>
          ) : failure.kind === "gone" ? (
            <p>This object no longer exists, so nothing was applied.</p>
          ) : (
            <p className="wrap">{failure.message}</p>
          )}
        </div>
      )}

      <div className="edit-body">
        {!revealed ? (
          <div className="message">
            <p>Editing shows this Secret's values on screen.</p>
            <button type="button" onClick={() => setRevealed(true)}>
              Show values and edit
            </button>
          </div>
        ) : preview ? (
          unchanged ? (
            <p className="message">
              This edit changes nothing. Removing a field that another tool manages leaves it in
              place.
            </p>
          ) : (
            <CodeView key="diff" value={preview.after} original={preview.before} />
          )
        ) : loaded ? (
          // Seeded from text, not loaded.yaml, so returning from the preview keeps
          // the edit on screen and what is shown is what gets applied.
          <CodeView key={loadCount} value={text} onChange={setText} />
        ) : (
          !failure && <p className="message muted">Loading</p>
        )}
      </div>

      <footer className="edit-actions">
        <button type="button" onClick={onClose}>
          Cancel
        </button>
        {preview && (
          <button type="button" onClick={() => setPreview(undefined)}>
            Back to editing
          </button>
        )}
        {failure?.kind === "stale" && (
          <button type="button" disabled={busy} onClick={() => void load()}>
            Discard edits and reload
          </button>
        )}
        {failure?.kind === "fieldConflict" && (
          <button type="button" disabled={busy} onClick={() => void submit(true, true)}>
            Preview with force
          </button>
        )}
        {loaded && !preview && failure?.kind !== "stale" && failure?.kind !== "gone" && (
          <button
            type="button"
            className="primary"
            disabled={busy}
            data-testid="edit-preview"
            onClick={() => void submit(true, false)}
          >
            Preview changes
          </button>
        )}
        {preview && !unchanged && (
          <button
            type="button"
            className="primary"
            style={applyStyle}
            disabled={busy}
            data-testid="edit-apply"
            onClick={() => void submit(false, force)}
          >
            {force ? "Force apply" : "Apply"} to {context}
          </button>
        )}
      </footer>
    </div>
  );
}
