import { useMemo, useState } from "react";
import { apiGet } from "../../api";
import { toFailure } from "../actions/failure";
import type { APIResource } from "../cluster/useCluster";
import {
  formatValues,
  readValues,
  valuesFromYaml,
  type BinaryValue,
  type DataKind,
  type DataValues,
} from "../editor/dataValues";
import type { KubeObject } from "./useResourceWatch";

interface Props {
  kind: DataKind;
  context: string;
  resource: APIResource;
  object: KubeObject;
  // Missing while the context is read-only.
  onEdit?: (revealed: boolean) => void;
}

export function BinaryNote({ values }: { values: BinaryValue[] }) {
  if (values.length === 0) {
    return null;
  }
  const list = values.map((item) => `${item.key} (${item.bytes} bytes)`).join(", ");
  return <p className="muted wrap">Binary, not shown as JSON: {list}</p>;
}

function Values({ data }: { data: DataValues }) {
  return (
    <>
      <pre className="yaml" data-testid="data-values">
        {formatValues(data.values)}
      </pre>
      <BinaryNote values={data.binary} />
    </>
  );
}

// Secret keys come from the redacted watch object. Their values are fetched only
// on request and live in this component's state, so closing the panel discards
// them.
function SecretData({ context, resource, object, onEdit }: Omit<Props, "kind">) {
  const keys = Object.keys((object.data as Record<string, string>) ?? {});
  const [data, setData] = useState<DataValues>();
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
      setData(valuesFromYaml(loaded.yaml, "secret"));
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
      {data ? (
        <>
          <Values data={data} />
          <div className="detail-actions">
            {onEdit && (
              <button type="button" className="action" onClick={() => onEdit(true)}>
                Edit values
              </button>
            )}
            <button type="button" className="action" onClick={() => setData(undefined)}>
              Hide values
            </button>
          </div>
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

function ConfigMapData({ object, onEdit }: Pick<Props, "object" | "onEdit">) {
  const data = useMemo(() => readValues(object, "configmap"), [object]);
  if (Object.keys(data.values).length === 0 && data.binary.length === 0) {
    return null;
  }
  return (
    <section className="detail-section">
      <h3>Values</h3>
      <Values data={data} />
      {onEdit && (
        <button type="button" className="action" onClick={() => onEdit(true)}>
          Edit values
        </button>
      )}
    </section>
  );
}

export function DataSection({ kind, ...props }: Props) {
  return kind === "secret" ? <SecretData {...props} /> : <ConfigMapData {...props} />;
}
