import { useCallback, useEffect, useRef, useState } from "react";
import { apiGet } from "../../api";
import { toFailure, type Failure } from "../actions/failure";

export interface Release {
  name: string;
  namespace: string;
  revision: number;
  status: string;
  chart: string;
  chartVersion: string;
  appVersion: string;
  updated: string;
}

export interface ReleaseState {
  releases?: Release[];
  failure?: Failure;
  refresh: () => void;
}

// Reading releases decodes every revision's Secret, so this stays unhurried.
const pollMs = 10000;

export function helmPath(context: string, rest: string): string {
  return `/api/v1/contexts/${encodeURIComponent(context)}/helm/${rest}`;
}

export function useReleases(context: string, namespace: string): ReleaseState {
  const [releases, setReleases] = useState<Release[]>();
  const [failure, setFailure] = useState<Failure>();
  const poll = useRef<() => void>(() => undefined);

  useEffect(() => {
    setReleases(undefined);
    setFailure(undefined);

    let stopped = false;
    let timer: number | undefined;
    const query = new URLSearchParams({ namespace });

    const load = async () => {
      window.clearTimeout(timer);
      try {
        const next = await apiGet<Release[]>(helmPath(context, `releases?${query}`));
        if (!stopped) {
          setReleases(next);
          setFailure(undefined);
        }
      } catch (error) {
        if (!stopped) {
          setFailure(toFailure(error));
        }
      }
      if (!stopped) {
        window.clearTimeout(timer);
        timer = window.setTimeout(load, pollMs);
      }
    };
    poll.current = () => void load();
    void load();

    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [context, namespace]);

  const refresh = useCallback(() => poll.current(), []);
  return { releases, failure, refresh };
}

export function statusTone(status: string): "good" | "warn" | "bad" | "neutral" {
  if (status === "deployed") {
    return "good";
  }
  if (status === "failed") {
    return "bad";
  }
  return status === "superseded" ? "neutral" : "warn";
}
