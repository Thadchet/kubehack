import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";
import { contextColor } from "../cluster/contextColor";
import {
  forwardAddress,
  forwardTarget,
  type PortForward,
  type PortForwards,
} from "./usePortForwards";

interface Props {
  portForwards: PortForwards;
  active: boolean;
}

function openInBrowser(forward: PortForward) {
  const url = `http://${forwardAddress(forward)}`;
  if ("go" in window) {
    BrowserOpenURL(url);
  } else {
    window.open(url, "_blank", "noopener");
  }
}

function statusText(forward: PortForward): string {
  switch (forward.state) {
    case "active":
      return "Forwarding";
    case "starting":
      return "Connecting";
    default:
      return "Stopped";
  }
}

export function ForwardPanel({ portForwards, active }: Props) {
  const { forwards, restart, stop } = portForwards;
  if (!active) {
    return null;
  }
  return (
    <div className="forward-panel" role="tabpanel" data-testid="forward-panel">
      {forwards.length === 0 ? (
        <p className="message muted">
          No port forwards. Select a pod or service, then press f.
        </p>
      ) : (
        <table className="forward-table">
          <thead>
            <tr>
              <th>Local address</th>
              <th>Target</th>
              <th>Cluster</th>
              <th>Status</th>
              <th aria-label="Actions" />
            </tr>
          </thead>
          <tbody>
            {forwards.map((forward) => (
              <tr key={forward.id} data-testid="forward-row" data-state={forward.state}>
                <td className="forward-address">
                  {forward.localPort > 0 ? forwardAddress(forward) : "Choosing a port"}
                </td>
                <td>
                  {forwardTarget(forward)}
                  <div className="muted">
                    {forward.namespace}
                    {forward.kind === "service" && forward.pod && `, through pod ${forward.pod}`}
                  </div>
                </td>
                <td>
                  <span
                    className="dock-dot"
                    aria-hidden="true"
                    style={{ backgroundColor: contextColor(forward.context) }}
                  />
                  {forward.context}
                </td>
                <td>
                  <span data-tone={forward.state === "lost" ? "bad" : "good"}>
                    {statusText(forward)}
                  </span>
                  {forward.message && <div className="wrap muted">{forward.message}</div>}
                </td>
                <td className="forward-actions">
                  {forward.state === "active" && (
                    <button type="button" className="action" onClick={() => openInBrowser(forward)}>
                      Open in browser
                    </button>
                  )}
                  {forward.state === "lost" && (
                    <button type="button" className="action" onClick={() => restart(forward.id)}>
                      Reconnect
                    </button>
                  )}
                  <button type="button" className="action" onClick={() => stop(forward.id)}>
                    {forward.state === "lost" ? "Remove" : "Stop"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
