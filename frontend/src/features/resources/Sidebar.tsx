import { Ship } from "lucide-react";
import { Fragment, useMemo, useState } from "react";
import type { APIResource } from "../cluster/useCluster";
import { groupResources, resourceId, resourceLabel } from "./kinds";
import { resourceIcon } from "./resourceIcons";

interface Props {
  resources: APIResource[];
  selected?: string;
  onSelect: (id: string) => void;
}

// Helm releases aren't an API resource, but they are chosen in the same place.
export const helmReleasesId = "helm/releases";

export function Sidebar({ resources, selected, onSelect }: Props) {
  const [query, setQuery] = useState("");

  const sections = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const matching = resources.filter(
      (item) =>
        !needle ||
        resourceLabel(item).toLowerCase().includes(needle) ||
        item.group.includes(needle),
    );
    return groupResources(matching);
  }, [resources, query]);

  const needle = query.trim().toLowerCase();
  const showHelm = "helm releases".includes(needle);
  const helmSection = (
    <section>
      <h2>Helm</h2>
      <button
        type="button"
        aria-current={selected === helmReleasesId ? "page" : undefined}
        data-testid="helm-releases"
        onClick={() => onSelect(helmReleasesId)}
      >
        <Ship size={15} aria-hidden="true" />
        <span>Releases</span>
      </button>
    </section>
  );

  return (
    <nav className="sidebar" aria-label="Resource types">
      <input
        type="search"
        className="sidebar-search"
        placeholder="Find a resource type"
        aria-label="Find a resource type"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      <div className="sidebar-list">
        {sections.length === 0 && showHelm && helmSection}
        {sections.map((section, index) => (
          <Fragment key={section.title}>
            <section>
              <h2>{section.title}</h2>
              {section.resources.map((item) => {
                const Icon = resourceIcon(resourceId(item));
                return (
                  <button
                    key={resourceId(item)}
                    type="button"
                    title={item.group || "core"}
                    aria-current={resourceId(item) === selected ? "page" : undefined}
                    onClick={() => onSelect(resourceId(item))}
                  >
                    <Icon size={15} aria-hidden="true" />
                    <span>{resourceLabel(item)}</span>
                  </button>
                );
              })}
            </section>
            {index === 0 && showHelm && helmSection}
          </Fragment>
        ))}
        {sections.length === 0 && !showHelm && (
          <p className="muted">No resource type matches “{query}”.</p>
        )}
      </div>
    </nav>
  );
}
