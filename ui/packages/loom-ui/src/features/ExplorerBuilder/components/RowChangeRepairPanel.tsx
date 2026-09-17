import React from 'react';
import type {
  ExplorerBuilderCatalog,
  RowChangeUnresolvedReference,
} from '../../../types';
import { derivedOccurrences, type DraftTable } from '../authoring/model';

const alternativeLabel = (
  alternative: string,
  reference: RowChangeUnresolvedReference,
  catalog: ExplorerBuilderCatalog,
  table: DraftTable,
): string => {
  if (reference.code === 'AMBIGUOUS_ROUTE_REBASE_EDGE') {
    const edge = catalog.edges.find((candidate) => candidate.edgeId === alternative);
    if (!edge) return alternative;
    const from = catalog.nodes.find((node) => node.nodeId === edge.fromNodeId)?.resourceType;
    const to = catalog.nodes.find((node) => node.nodeId === edge.toNodeId)?.resourceType;
    return `${edge.label}${from && to ? ` (${from} to ${to})` : ''}`;
  }
  const occurrence = derivedOccurrences(table, catalog).find(
    (candidate) => candidate.id === alternative,
  );
  if (!occurrence) return alternative;
  return `${occurrence.resourceType}${occurrence.relationship ? ` via ${occurrence.relationship}` : ''}`;
};

export const RowChangeRepairPanel = ({
  unresolved,
  catalog,
  table,
  disabled,
  onChoose,
  onCancel,
}: {
  readonly unresolved: ReadonlyArray<RowChangeUnresolvedReference>;
  readonly catalog: ExplorerBuilderCatalog;
  readonly table: DraftTable;
  readonly disabled: boolean;
  readonly onChoose: (
    reference: RowChangeUnresolvedReference,
    alternative: string,
  ) => void;
  readonly onCancel: () => void;
}) => (
  <section
    className="rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950"
    aria-labelledby="row-change-repair-title"
  >
    <h2 id="row-change-repair-title" className="font-semibold">
      Choose how to preserve this table
    </h2>
    <p className="mt-1 text-xs text-amber-900">
      More than one valid FHIR relationship can keep the existing features.
      Choose the relationship you mean; Loom will assess the table again before
      changing any rows.
    </p>
    <div className="mt-3 space-y-3">
      {unresolved.map((reference) => (
        <div key={`${reference.code}:${reference.id}`}>
          <p className="text-xs font-medium">{reference.message}</p>
          <div className="mt-2 flex flex-wrap gap-2">
            {(reference.alternatives ?? []).map((alternative) => {
              const label = alternativeLabel(
                alternative,
                reference,
                catalog,
                table,
              );
              return (
                <button
                  key={alternative}
                  type="button"
                  className="rounded-md border border-amber-400 bg-white px-3 py-1.5 text-xs font-semibold text-amber-950 hover:bg-amber-100 disabled:opacity-50"
                  disabled={disabled}
                  onClick={() => onChoose(reference, alternative)}
                >
                  Use {label}
                </button>
              );
            })}
          </div>
        </div>
      ))}
    </div>
    <button
      type="button"
      className="mt-3 text-xs font-semibold text-amber-900 underline"
      disabled={disabled}
      onClick={onCancel}
    >
      Keep current rows
    </button>
  </section>
);
