import { useState, type FormEvent } from "react";
import { apiPost } from "../../api";
import { toFailure, type Failure } from "../actions/failure";
import { contextColor } from "../cluster/contextColor";
import { helmPath, type Release } from "./useReleases";

interface Props {
  context: string;
  release: Release;
  // Set to roll back to that revision. Otherwise this uninstalls.
  revision?: number;
  onClose: () => void;
  onChanged: () => void;
}

function failureText(failure: Failure): string {
  switch (failure.kind) {
    case "stale":
      return "This release changed after you opened it, so nothing was changed. Check the new revision first.";
    case "gone":
      return "This release no longer exists.";
    default:
      return failure.message;
  }
}

export function ReleaseConfirm({ context, release, revision, onClose, onChanged }: Props) {
  const rollingBack = revision !== undefined;
  const [typedName, setTypedName] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<Failure>();

  const ready = !busy && (rollingBack || typedName === release.name);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!ready) {
      return;
    }
    setBusy(true);
    setFailure(undefined);
    try {
      await apiPost(helmPath(context, rollingBack ? "rollback" : "uninstall"), {
        namespace: release.namespace,
        name: release.name,
        revision: revision ?? 0,
        expectedRevision: release.revision,
      });
      onChanged();
      onClose();
    } catch (error) {
      setFailure(toFailure(error));
      setBusy(false);
      onChanged();
    }
  };

  return (
    <div className="scrim">
      <form
        className="confirm"
        role="alertdialog"
        aria-label={`${rollingBack ? "Roll back" : "Uninstall"} release ${release.name}`}
        data-testid="helm-confirm"
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
          <h2>{rollingBack ? `Roll back to revision ${revision}` : "Uninstall release"}</h2>
          <p className="wrap">
            <strong>{release.name}</strong> in {release.namespace}
          </p>

          {rollingBack ? (
            <p className="muted">
              This restores the chart and values of revision {revision} as a new revision,{" "}
              {release.revision + 1}.
            </p>
          ) : (
            <label className="confirm-field">
              This removes the release's resources and its history, and can't be undone. Type the
              name to confirm.
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

          {failure && (
            <p className="wrap" role="alert" data-tone="bad" data-testid="helm-confirm-failure">
              {failureText(failure)}
            </p>
          )}
        </div>
        <footer className="edit-actions">
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            autoFocus={rollingBack}
            type="submit"
            className={rollingBack ? "primary" : "danger"}
            disabled={!ready}
            data-testid="helm-confirm-submit"
          >
            {busy ? "Working" : `${rollingBack ? "Roll back" : "Uninstall"} on ${context}`}
          </button>
        </footer>
      </form>
    </div>
  );
}
