import { useMemo, useState } from "react";
import type { APIResource } from "../cluster/useCluster";
import { groupResources, resourceId, resourceLabel } from "./kinds";

interface Props {
  resources: APIResource[];
  selected?: string;
  onSelect: (id: string) => void;
}

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
        {sections.map((section) => (
          <section key={section.title}>
            <h2>{section.title}</h2>
            {section.resources.map((item) => (
              <button
                key={resourceId(item)}
                type="button"
                title={item.group || "core"}
                aria-current={resourceId(item) === selected ? "page" : undefined}
                onClick={() => onSelect(resourceId(item))}
              >
                {resourceLabel(item)}
              </button>
            ))}
          </section>
        ))}
        {sections.length === 0 && <p className="muted">No resource type matches “{query}”.</p>}
      </div>
    </nav>
  );
}
