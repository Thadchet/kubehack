import { useState } from "react";
import { parse } from "yaml";
import { apiGet } from "../../api";
import { toFailure } from "../actions/failure";
import type { APIResource } from "../cluster/useCluster";
import type { KubeObject } from "./useResourceWatch";

interface Props {
  context: string;
  resource: APIResource;
  object: KubeObject;
}

interface Decoded {
  key: string;
  value?: string;
  bytes: number;
}

function decode(key: string, encoded: string): Decoded {
  const bytes = Uint8Array.from(atob(encoded), (character) => character.charCodeAt(0));
  try {
    return { key, value: new TextDecoder("utf-8", { fatal: true }).decode(bytes), bytes: bytes.length };
  } catch {
    // Certificates in DER form and similar values are not text.
    return { key, bytes: bytes.length };
  }
}

// Keys come from the redacted watch object. Values are fetched only on request
// and live in this component's state, so closing the panel discards them.
export function SecretValues({ context, resource, object }: Props) {
  const keys = Object.keys((object.data as Record<string, string>) ?? {});
  const [values, setValues] = useState<Decoded[]>();
  const [error, setError] = useState<string>();

  const show = async () => {
    setError(undefined);
    try {
      const query = new URLSearchParams({
        group: resource.group,
        version: resource.version,
        resource: resource.resource,
        namespace: object.metadata.namespace ?? "",
        name: object.metadata.name,
      });
      const loaded = await apiGet<{ yaml: string }>(
        `/api/v1/contexts/${encodeURIComponent(context)}/object?${query}`,
      );
      const data = (parse(loaded.yaml) as { data?: Record<string, string> }).data ?? {};
      setValues(Object.entries(data).map(([key, value]) => decode(key, value)));
    } catch (reason) {
      setError(toFailure(reason).message);
    }
  };

  if (keys.length === 0) {
    return null;
  }

  return (
    <section className="detail-section">
      <h3>Values</h3>
      {values ? (
        <>
          <dl className="pairs" data-testid="secret-values">
            {values.map((item) => (
              <div key={item.key}>
                <dt>{item.key}</dt>
                <dd className="yaml-inline">{item.value ?? `Binary, ${item.bytes} bytes`}</dd>
              </div>
            ))}
          </dl>
          <button type="button" className="action" onClick={() => setValues(undefined)}>
            Hide values
          </button>
        </>
      ) : (
        <>
          <ul className="plain">
            {keys.map((key) => (
              <li key={key}>{key}</li>
            ))}
          </ul>
          <button type="button" className="action" onClick={() => void show()}>
            Show values
          </button>
        </>
      )}
      {error && (
        <p className="wrap" data-tone="bad">
          {error}
        </p>
      )}
    </section>
  );
}
