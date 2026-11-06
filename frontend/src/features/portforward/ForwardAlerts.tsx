import { useEffect, useState } from "react";
import { forwardAddress, forwardTarget, type PortForwards } from "./usePortForwards";

interface Props {
  portForwards: PortForwards;
}

// These sit above everything, because a forward can stop while the user is
// looking at another cluster and something else is still pointed at its port.
export function ForwardAlerts({ portForwards }: Props) {
  const { forwards, error, clearError, restart } = portForwards;
  const [dismissed, setDismissed] = useState<string[]>([]);

  // A forward that recovers and drops again deserves a new alert.
  useEffect(() => {
    setDismissed((ids) => {
      const kept = ids.filter((id) =>
        forwards.some((forward) => forward.id === id && forward.state === "lost"),
      );
      return kept.length === ids.length ? ids : kept;
    });
  }, [forwards]);

  const lost = forwards.filter(
    (forward) => forward.state === "lost" && !dismissed.includes(forward.id),
  );

  return (
    <>
      {error && (
        <div className="forward-alert" role="alert" data-testid="forward-error">
          <p className="wrap">{error}</p>
          <button type="button" className="action" onClick={clearError}>
            Dismiss
          </button>
        </div>
      )}
      {lost.map((forward) => (
        <div className="forward-alert" role="alert" key={forward.id} data-testid="forward-alert">
          <p className="wrap">
            Port forward stopped: {forwardAddress(forward)} to {forwardTarget(forward)} on{" "}
            {forward.context}. {forward.message}
          </p>
          <button type="button" className="action" onClick={() => restart(forward.id)}>
            Reconnect
          </button>
          <button
            type="button"
            className="action"
            onClick={() => setDismissed((ids) => [...ids, forward.id])}
          >
            Dismiss
          </button>
        </div>
      ))}
    </>
  );
}
