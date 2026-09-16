import React, { useMemo, useState } from 'react';
import type { SelectionRevision } from '../../../selection';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';

type PopulationPath = {
  readonly edgeIds: ReadonlyArray<string>;
  readonly label: string;
};

const populationPaths = (
  catalog: ExplorerBuilderCatalog,
  rootResourceType: string,
  selectionResourceType: string,
): ReadonlyArray<PopulationPath> => {
  const root = catalog.nodes.find((node) => node.resourceType === rootResourceType);
  const target = catalog.nodes.find((node) => node.resourceType === selectionResourceType);
  if (!root || !target) return [];
  if (root.nodeId === target.nodeId) {
    return [{ edgeIds: [], label: rootResourceType }];
  }
  const result: PopulationPath[] = [];
  const queue: Array<{ readonly nodeId: string; readonly edges: ReadonlyArray<string>; readonly labels: ReadonlyArray<string> }> = [
    { nodeId: root.nodeId, edges: [], labels: [rootResourceType] },
  ];
  const shortestByNode = new Map<string, number>([[root.nodeId, 0]]);
  while (queue.length > 0) {
    const current = queue.shift();
    if (!current || current.edges.length >= 4) continue;
    for (const edge of catalog.edges.filter((candidate) => candidate.fromNodeId === current.nodeId && candidate.populated !== false)) {
      const node = catalog.nodes.find((candidate) => candidate.nodeId === edge.toNodeId);
      if (!node) continue;
      const edges = [...current.edges, edge.edgeId];
      const labels = [...current.labels, `${node.resourceType} via ${edge.label}`];
      if (node.nodeId === target.nodeId) {
        result.push({ edgeIds: edges, label: labels.join(' → ') });
        continue;
      }
      const known = shortestByNode.get(node.nodeId);
      if (known !== undefined && known < edges.length) continue;
      shortestByNode.set(node.nodeId, edges.length);
      queue.push({ nodeId: node.nodeId, edges, labels });
    }
  }
  const shortest = Math.min(...result.map((path) => path.edgeIds.length));
  return result.filter((path) => path.edgeIds.length === shortest);
};

export const PopulationPanel = ({
  catalog,
  table,
  selection,
  loading,
  error,
  disabled,
  onAttach,
  onClear,
}: {
  readonly catalog: ExplorerBuilderCatalog;
  readonly table: DraftTable;
  readonly selection?: SelectionRevision;
  readonly loading: boolean;
  readonly error?: string;
  readonly disabled: boolean;
  readonly onAttach: (edgeIds: ReadonlyArray<string>) => void;
  readonly onClear: () => void;
}) => {
  const paths = useMemo(
    () => selection ? populationPaths(catalog, table.document.rootResourceType, selection.resourceType) : [],
    [catalog, selection, table.document.rootResourceType],
  );
  const [pathIndex, setPathIndex] = useState(0);
  const attached = table.document.population;
  return (
    <section aria-label="Starting collection" className="rounded-xl border border-indigo-200 bg-indigo-50 px-4 py-3 text-sm text-slate-800">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="font-semibold text-slate-900">Starting collection</h2>
          {attached ? (
            <p className="mt-1">
              {selection?.memberCount.toLocaleString() ?? 'Saved'} {selection?.resourceType ?? attached.route.at(-1)?.resourceType ?? table.document.rootResourceType} resources constrain one row per {table.document.rootResourceType}.
            </p>
          ) : selection ? (
            <p className="mt-1">
              {selection.memberCount.toLocaleString()} selected {selection.resourceType} resources are ready to constrain this table.
            </p>
          ) : (
            <p className="mt-1">No starting collection is attached. This table uses every authorized {table.document.rootResourceType} resource.</p>
          )}
          {loading ? <p className="mt-1 text-slate-600">Loading the saved selection…</p> : null}
          {error ? <p role="alert" className="mt-1 text-red-700">{error}</p> : null}
        </div>
        {attached ? (
          <button type="button" disabled={disabled} onClick={onClear} className="rounded-md border border-slate-300 bg-white px-3 py-2 font-semibold hover:bg-slate-50 disabled:opacity-40">
            Use all authorized rows
          </button>
        ) : selection && paths.length > 0 ? (
          <div className="flex flex-wrap items-center gap-2">
            {paths.length > 1 ? (
              <label className="flex items-center gap-2">
                <span className="font-medium">Connection</span>
                <select aria-label="Population connection" value={pathIndex} onChange={(event) => setPathIndex(Number(event.currentTarget.value))} className="rounded border border-slate-300 bg-white px-2 py-2">
                  {paths.map((path, index) => <option key={path.label} value={index}>{path.label}</option>)}
                </select>
              </label>
            ) : <span className="text-xs text-slate-600">{paths[0].label}</span>}
            <button type="button" disabled={disabled} onClick={() => onAttach(paths[pathIndex]?.edgeIds ?? [])} className="rounded-md bg-indigo-700 px-3 py-2 font-semibold text-white hover:bg-indigo-800 disabled:opacity-40">
              Use selected resources
            </button>
          </div>
        ) : selection && !loading ? (
          <p role="alert" className="font-medium text-amber-800">No supported path connects {table.document.rootResourceType} rows to {selection.resourceType}.</p>
        ) : null}
      </div>
    </section>
  );
};
