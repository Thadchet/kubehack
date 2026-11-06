import { useState } from "react";
import type { DockTarget } from "../dock/Dock";
import { LogView } from "./LogView";
import { useLogStream, type LogStatus } from "./useLogStream";

const statusText: Record<LogStatus, string> = {
  connecting: "Connecting",
  streaming: "Streaming",
  ended: "Stream ended",
  failed: "Could not read this log",
};

interface PanelProps {
  target: DockTarget;
  active: boolean;
}

// LogPanel stays mounted while its tab is in the background, so its stream
// keeps collecting lines. Only closing the tab ends the stream.
export function LogPanel({ target, active }: PanelProps) {
  const [container, setContainer] = useState(target.container);
  const [previous, setPrevious] = useState(false);
  const [following, setFollowing] = useState(true);
  const log = useLogStream({
    context: target.context,
    namespace: target.namespace,
    pod: target.pod,
    container,
    previous,
  });

  if (!active) {
    return null;
  }

  return (
    <div className="log-panel" role="tabpanel">
      <div className="log-toolbar">
        <span className="muted">{target.context}</span>
        {target.containers.length > 1 ? (
          <select
            aria-label="Container"
            data-testid="log-container"
            value={container}
            onChange={(event) => setContainer(event.target.value)}
          >
            {target.containers.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        ) : (
          <span>{container}</span>
        )}
        <label>
          <input
            type="checkbox"
            data-testid="log-previous"
            checked={previous}
            onChange={(event) => setPrevious(event.target.checked)}
          />
          Previous run
        </label>
        <label>
          <input
            type="checkbox"
            data-testid="log-follow"
            checked={following}
            onChange={(event) => setFollowing(event.target.checked)}
          />
          Follow
        </label>
        <span
          className="log-status"
          data-testid="log-status"
          data-tone={log.status === "failed" ? "bad" : log.status === "streaming" ? "good" : undefined}
        >
          {statusText[log.status]}
        </span>
        {(log.status === "ended" || log.status === "failed") && !previous && (
          <button type="button" onClick={log.reconnect}>
            Reconnect
          </button>
        )}
      </div>
      {log.error && (
        <p className="banner" role="alert">
          {log.error}
        </p>
      )}
      <LogView
        lines={log.lines}
        version={log.version}
        following={following}
        onFollowing={setFollowing}
      />
    </div>
  );
}

