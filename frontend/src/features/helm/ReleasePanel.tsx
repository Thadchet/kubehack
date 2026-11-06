import { useEffect, useState } from "react";
import { apiGet } from "../../api";
import { toFailure } from "../actions/failure";
import { formatAge } from "../resources/columns";
import { helmPath, statusTone, type Release } from "./useReleases";

interface Revision {
  revision: number;
  status: string;
  chart: string;
  chartVersion: string;
  appVersion: string;
  updated: string;
  description: string;
}

interface Detail {
  notes: string;
  history: Revision[];
}

interface Props {
  context: string;
  release: Release;
  onClose: () => void;
  // Both are missing while the context is read-only.
  onUpgrade?: () => void;
  onUninstall?: () => void;
  onRollback?: (revision: number) => void;
}

export function ReleasePanel({
  context,
  release,
  onClose,
  onUpgrade,
  onUninstall,
  onRollback,
}: Props) {
  const [detail, setDetail] = useState<Detail>();
  const [error, setError] = useState<string>();
  const { name, namespace, revision, status } = release;

  useEffect(() => {
    let stopped = false;
    const path = helmPath(
      context,
      `releases/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`,
    );
    apiGet<Detail>(path)
      .then((reply) => {
        if (!stopped) {
          setDetail(reply);
          setError(undefined);
        }
      })
      .catch((failure) => {
        if (!stopped) {
          setError(toFailure(failure).message);
        }
      });
    return () => {
      stopped = true;
    };
    // A new revision or status means the history has changed.
  }, [context, namespace, name, revision, status]);

  const now = Date.now();

  return (
    <aside className="detail" aria-label="Release details">
      <header className="detail-head">
        <div>
          <p className="muted">Helm release</p>
          <h2 className="wrap">{name}</h2>
          <div className="detail-actions">
            {onUpgrade && (
              <button type="button" className="action" onClick={onUpgrade}>
                Upgrade
              </button>
            )}
            {onUninstall && (
              <button type="button" className="action" data-tone="bad" onClick={onUninstall}>
                Uninstall
              </button>
            )}
          </div>
        </div>
        <button type="button" className="quiet" onClick={onClose} aria-label="Close details">
          ✕
        </button>
      </header>
      <div className="detail-body">
        <section className="detail-section">
          <dl className="facts">
            <dt>Namespace</dt>
            <dd>{namespace}</dd>
            <dt>Status</dt>
            <dd data-tone={statusTone(status)}>{status}</dd>
            <dt>Chart</dt>
            <dd>
              {release.chart} {release.chartVersion}
            </dd>
            {release.appVersion && (
              <>
                <dt>App version</dt>
                <dd>{release.appVersion}</dd>
              </>
            )}
          </dl>
        </section>

        {error && (
          <p className="message" data-tone="bad">
            {error}
          </p>
        )}

        {detail && (
          <section className="detail-section">
            <h3>History</h3>
            <ul className="plain" data-testid="helm-history">
              {detail.history.map((item) => (
                <li key={item.revision} className="helm-revision">
                  <div>
                    <strong>Revision {item.revision}</strong>{" "}
                    <span data-tone={statusTone(item.status)}>{item.status}</span>
                    <span className="muted"> {formatAge(item.updated, now)} ago</span>
                    <div className="muted">
                      {item.chart} {item.chartVersion}
                    </div>
                    <div className="muted wrap">{item.description}</div>
                  </div>
                  {onRollback && item.revision !== revision && (
                    <button
                      type="button"
                      className="action"
                      onClick={() => onRollback(item.revision)}
                    >
                      Roll back
                    </button>
                  )}
                </li>
              ))}
            </ul>
          </section>
        )}

        {detail?.notes && (
          <section className="detail-section">
            <h3>Notes</h3>
            <pre className="yaml">{detail.notes}</pre>
          </section>
        )}
      </div>
    </aside>
  );
}
