import { useCallback, useEffect, useState } from "react";
import { apiGet, apiPost } from "../../api";
import { toFailure, type Failure } from "../actions/failure";
import { contextColor } from "../cluster/contextColor";
import type { APIResource } from "../cluster/useCluster";
import type { KubeObject } from "../resources/useResourceWatch";
import { BinaryNote } from "../resources/DataSection";
import { CodeView } from "./CodeView";
import {
  applyValues,
  dataKind,
  formatValues,
  keptKeys,
  printObject,
  valuesFromYaml,
  type BinaryValue,
} from "./dataValues";

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

interface Props {
  context: string;
  resource: APIResource;
  object: KubeObject;
  // True once the user has already asked to see a Secret's values.
  revealed?: boolean;
  onClose: () => void;
}

type Mode = "json" | "yaml";

export function EditDialog({ context, resource, object, revealed: alreadyRevealed, onClose }: Props) {
  const { name, namespace = "" } = object.metadata;
  const kind = dataKind(resource);

  // Secret values leave the server only after the user asks for them.
  const [revealed, setRevealed] = useState(kind !== "secret" || alreadyRevealed === true);
  const [loaded, setLoaded] = useState<EditableObject>();
  // Bumping this remounts the editor with freshly loaded text.
  const [loadCount, setLoadCount] = useState(0);
  // YAML of the whole object. In JSON mode, jsonText holds the edit instead.
  const [text, setText] = useState("");
  const [mode, setMode] = useState<Mode>(kind ? "json" : "yaml");
  const [jsonText, setJsonText] = useState("");
  const [binary, setBinary] = useState<BinaryValue[]>([]);
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
      if (kind) {
        const data = valuesFromYaml(next.yaml, kind);
        setJsonText(formatValues(data.values));
        setBinary(data.binary);
      }
      setLoadCount((value) => value + 1);
    } catch (error) {
      setFailure(toFailure(error));
    } finally {
      setBusy(false);
    }
  }, [context, resource, kind, namespace, name]);

  useEffect(() => {
    if (revealed) {
      void load();
    }
  }, [revealed, load]);

  const switchMode = (next: Mode) => {
    if (next === mode || !kind) {
      return;
    }
    try {
      if (next === "yaml") {
        setText(printObject(applyValues(text, jsonText, kind)));
      } else {
        const data = valuesFromYaml(text, kind);
        setJsonText(formatValues(data.values));
        setBinary(data.binary);
      }
      setFailure(undefined);
      setMode(next);
    } catch (error) {
      setFailure({ kind: "invalid", message: (error as Error).message });
    }
  };

  const submit = async (dryRun: boolean, useForce: boolean) => {
    if (!loaded) {
      return;
    }
    setBusy(true);
    setFailure(undefined);
    try {
      // The API server also reads JSON, which leaves no YAML quoting to get wrong.
      const edited = kind && mode === "json" ? JSON.stringify(applyValues(text, jsonText, kind)) : text;
      const result = await apiPost<ApplyResult>(
        `/api/v1/contexts/${encodeURIComponent(context)}/apply`,
        {
          group: resource.group,
          version: resource.version,
          resource: resource.resource,
          namespace,
          name,
          yaml: edited,
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
  // Stored Secret values are base64, which would make the comparison unreadable.
  const showJson = kind !== undefined && mode === "json";
  const asShown = (yamlText: string) =>
    showJson ? formatValues(valuesFromYaml(yamlText, kind).values) : yamlText;
  const kept = showJson && preview ? keptKeys(preview.after, jsonText, kind) : [];
  const applyStyle = { backgroundColor: contextColor(context) };

  return (
    <div className="edit" role="dialog" aria-label={`Edit ${resource.kind} ${name}`}>
      <header className="edit-head">
        <h2 className="wrap">
          {preview ? "Review changes to" : "Edit"} {resource.kind} {name}
        </h2>
        {kind && loaded && !preview && (
          <div className="edit-modes" role="tablist" aria-label="Edit as">
            {(["json", "yaml"] as const).map((item) => (
              <button
                key={item}
                type="button"
                role="tab"
                aria-selected={mode === item}
                data-testid={`edit-mode-${item}`}
                onClick={() => switchMode(item)}
              >
                {item === "json" ? "Values as JSON" : "Whole object as YAML"}
              </button>
            ))}
          </div>
        )}
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
            <CodeView
              key="diff"
              value={asShown(preview.after)}
              original={asShown(preview.before)}
              language={showJson ? "json" : "yaml"}
            />
          )
        ) : loaded ? (
          // Seeded from the edit, not loaded.yaml, so returning from the preview
          // keeps it on screen and what is shown is what gets applied.
          <CodeView
            key={`${mode}/${loadCount}`}
            value={showJson ? jsonText : text}
            onChange={showJson ? setJsonText : setText}
            language={mode}
          />
        ) : (
          !failure && <p className="message muted">Loading</p>
        )}
      </div>

      <footer className="edit-actions">
        {showJson && (
          <div className="edit-note">
            {!preview ? (
              <BinaryNote values={binary} />
            ) : (
              kept.length > 0 && (
                <p className="wrap" data-tone="warn" data-testid="edit-kept">
                  Not removed, because another tool manages them: {kept.join(", ")}
                </p>
              )
            )}
          </div>
        )}
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
