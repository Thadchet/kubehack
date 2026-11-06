import { useEffect, useState } from "react";
import { apiGet, apiPost } from "../../api";
import { toFailure, type Failure } from "../actions/failure";
import { contextColor } from "../cluster/contextColor";
import { CodeView } from "../editor/CodeView";
import { helmPath, type Release } from "./useReleases";

interface Props {
  context: string;
  // Missing when installing.
  release?: Release;
  defaultNamespace: string;
  onClose: () => void;
  onChanged: () => void;
}

interface Preview {
  manifest: string;
  current?: string;
}

function failureText(failure: Failure): string {
  switch (failure.kind) {
    case "stale":
      return "This release changed after you opened it, so nothing was changed. Close this and check the new revision first.";
    case "gone":
      return "This release no longer exists.";
    default:
      return failure.message;
  }
}

export function ReleaseDialog({ context, release, defaultNamespace, onClose, onChanged }: Props) {
  const upgrading = release !== undefined;
  const [name, setName] = useState(release?.name ?? "");
  const [namespace, setNamespace] = useState(release?.namespace ?? defaultNamespace);
  const [chart, setChart] = useState("");
  const [repo, setRepo] = useState("");
  const [version, setVersion] = useState("");
  const [values, setValues] = useState<string>();
  const [createNamespace, setCreateNamespace] = useState(false);
  const [wait, setWait] = useState(false);
  const [preview, setPreview] = useState<Preview>();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<Failure>();

  useEffect(() => {
    if (!release) {
      setValues("");
      return;
    }
    let stopped = false;
    const path = helmPath(
      context,
      `releases/${encodeURIComponent(release.namespace)}/${encodeURIComponent(release.name)}/values`,
    );
    apiGet<{ values: string }>(path)
      .then((reply) => {
        if (!stopped) {
          setValues(reply.values);
        }
      })
      .catch((error) => {
        if (!stopped) {
          setFailure(toFailure(error));
        }
      });
    return () => {
      stopped = true;
    };
  }, [context, release]);

  const ready =
    !busy &&
    values !== undefined &&
    name.trim() !== "" &&
    namespace.trim() !== "" &&
    (upgrading || chart.trim() !== "");

  const send = (dryRun: boolean) =>
    apiPost<Preview>(helmPath(context, upgrading ? "upgrade" : "install"), {
      name: name.trim(),
      namespace: namespace.trim(),
      chart: chart.trim(),
      repo: repo.trim(),
      version: version.trim(),
      values,
      createNamespace,
      wait,
      dryRun,
      expectedRevision: release?.revision ?? 0,
    });

  const showPreview = async () => {
    setBusy(true);
    setFailure(undefined);
    try {
      setPreview(await send(true));
    } catch (error) {
      setFailure(toFailure(error));
    }
    setBusy(false);
  };

  const apply = async () => {
    setBusy(true);
    setFailure(undefined);
    try {
      await send(false);
      onChanged();
      onClose();
    } catch (error) {
      setFailure(toFailure(error));
      setBusy(false);
      // A failed run can still leave a new failed revision.
      onChanged();
    }
  };

  const verb = upgrading ? "Upgrade" : "Install";
  const dot = { backgroundColor: contextColor(context) };

  return (
    <div
      className="edit"
      role="dialog"
      aria-label={`${verb} a Helm release`}
      data-testid="helm-dialog"
    >
      <header className="edit-head">
        <h2 className="wrap">
          {preview ? `Review ${verb.toLowerCase()} of` : verb}{" "}
          {upgrading ? release.name : name || "a release"}
        </h2>
        <span className="edit-target">
          <span className="dock-dot" aria-hidden="true" style={dot} />
          {context}
          {namespace && `, ${namespace}`}
        </span>
      </header>

      {failure && (
        <div
          className="edit-failure"
          role="alert"
          data-testid="helm-failure"
          data-kind={failure.kind}
        >
          <p className="wrap">{failureText(failure)}</p>
        </div>
      )}

      {!preview && (
        <div className="helm-fields">
          {!upgrading && (
            <>
              <label>
                Release name
                <input
                  autoFocus
                  aria-label="Release name"
                  autoComplete="off"
                  spellCheck={false}
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              </label>
              <label>
                Namespace
                <input
                  aria-label="Namespace"
                  autoComplete="off"
                  spellCheck={false}
                  value={namespace}
                  onChange={(event) => setNamespace(event.target.value)}
                />
              </label>
            </>
          )}
          <label>
            Chart
            <input
              autoFocus={upgrading}
              aria-label="Chart"
              autoComplete="off"
              spellCheck={false}
              placeholder={
                upgrading
                  ? `Leave empty to keep ${release.chart} ${release.chartVersion}`
                  : "repo/chart, oci://… or a local path"
              }
              value={chart}
              onChange={(event) => setChart(event.target.value)}
            />
          </label>
          <label>
            Repository URL
            <input
              aria-label="Repository URL"
              autoComplete="off"
              spellCheck={false}
              placeholder="Only if the chart is a bare name"
              value={repo}
              onChange={(event) => setRepo(event.target.value)}
            />
          </label>
          <label>
            Version
            <input
              aria-label="Version"
              autoComplete="off"
              spellCheck={false}
              placeholder="Latest"
              value={version}
              onChange={(event) => setVersion(event.target.value)}
            />
          </label>
          <div className="helm-options">
            {!upgrading && (
              <label>
                <input
                  type="checkbox"
                  checked={createNamespace}
                  onChange={(event) => setCreateNamespace(event.target.checked)}
                />
                Create the namespace if it's missing
              </label>
            )}
            <label>
              <input
                type="checkbox"
                checked={wait}
                onChange={(event) => setWait(event.target.checked)}
              />
              Wait until resources are ready, up to 5 minutes
            </label>
          </div>
        </div>
      )}

      {!preview && <p className="helm-values-label">Values, as YAML</p>}

      <div className="edit-body">
        {preview ? (
          preview.current !== undefined && preview.current !== "" ? (
            preview.current === preview.manifest ? (
              <p className="message">This upgrade changes no resources.</p>
            ) : (
              <CodeView key="diff" value={preview.manifest} original={preview.current} />
            )
          ) : (
            <CodeView key="manifest" value={preview.manifest} />
          )
        ) : values !== undefined ? (
          // Seeded from values, so returning from the preview keeps the edit.
          <CodeView key="values" value={values} onChange={setValues} />
        ) : (
          !failure && <p className="message muted">Loading values</p>
        )}
      </div>

      <footer className="edit-actions">
        <button type="button" onClick={onClose}>
          Cancel
        </button>
        {preview ? (
          <>
            <button type="button" disabled={busy} onClick={() => setPreview(undefined)}>
              Back to editing
            </button>
            <button
              type="button"
              className="primary"
              disabled={busy}
              data-testid="helm-apply"
              onClick={() => void apply()}
            >
              {busy ? "Working" : `${verb} on ${context}`}
            </button>
          </>
        ) : (
          <button
            type="button"
            className="primary"
            disabled={!ready}
            data-testid="helm-preview"
            onClick={() => void showPreview()}
          >
            {busy ? "Rendering" : "Preview"}
          </button>
        )}
      </footer>
    </div>
  );
}
