import React from 'react';
import type {
  ExplorerBuilderCatalog,
  RowChangeUnresolvedReference,
} from '../../../types';
import { derivedOccurrences, type DraftTable } from '../authoring/model';
import {
  isCatalogRootRouteSelectionToken,
  resolveCatalogRootRouteSelection,
} from '../authoring/rootChangeSelection';
import { relationshipLabel } from '../constructionWorkspace/routeDisplay';

const alternativeLabel = (
  alternative: string,
  reference: RowChangeUnresolvedReference,
  catalog: ExplorerBuilderCatalog,
  table: DraftTable,
  candidateRootNodeId: string,
  snapshotIsCurrent: boolean,
): { readonly label: string; readonly available: boolean } => {
  if (reference.code === 'AMBIGUOUS_ROUTE_REBASE_EDGE') {
    const edge = catalog.edges.find((candidate) => candidate.edgeId === alternative);
    if (!edge) return { label: 'Unavailable relationship option', available: false };
    const from = catalog.nodes.find((node) => node.nodeId === edge.fromNodeId)?.resourceType;
    const to = catalog.nodes.find((node) => node.nodeId === edge.toNodeId)?.resourceType;
    const relationship = relationshipLabel({
      fromResourceType: from ?? '',
      toResourceType: to ?? '',
      relationship: edge.label,
    });
    return {
      label: `${relationship}${from && to ? ` between ${from} and ${to}` : ''}`,
      available: Boolean(from && to),
    };
  }
  if (reference.code === 'AMBIGUOUS_ROW_ROOT_OCCURRENCE') {
    if (isCatalogRootRouteSelectionToken(alternative)) {
      const selection = resolveCatalogRootRouteSelection(
        alternative,
        catalog,
        table,
        candidateRootNodeId,
        snapshotIsCurrent,
      );
      if (!selection) return { label: 'Unavailable relationship option', available: false };
      return {
        label: `Use ${selection.pathLabel} from ${selection.anchorLabel}`,
        available: true,
      };
    }
  }
  const occurrence = derivedOccurrences(table, catalog).find((candidate) => candidate.id === alternative);
  if (occurrence) {
    return {
      label: `${occurrence.resourceType}${occurrence.relationship ? ` through ${occurrence.relationship}` : ''}`,
      available: true,
    };
  }
  return { label: 'Unavailable relationship option', available: false };
};

export const RowChangeRepairPanel = ({
  unresolved,
  catalog,
  table,
  candidateRootNodeId,
  snapshotIsCurrent,
  disabled,
  onChoose,
  onCancel,
}: {
  readonly unresolved: ReadonlyArray<RowChangeUnresolvedReference>;
  readonly catalog: ExplorerBuilderCatalog;
  readonly table: DraftTable;
  readonly candidateRootNodeId: string;
  readonly snapshotIsCurrent: boolean;
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
      Your existing columns can be kept through more than one relationship.
      Choose how these records connect. Loom checks the choice before changing
      the table.
    </p>
    <div className="mt-3 space-y-3">
      {unresolved.map((reference) => (
        <div key={`${reference.code}:${reference.id}`}>
          <p className="text-xs font-medium">
            {reference.code === 'AMBIGUOUS_ROUTE_REBASE_EDGE'
              ? 'Which relationship should keep this existing column connected to the new rows?'
              : reference.message}
          </p>
          <div className="mt-2 flex flex-wrap gap-2">
            {(reference.alternatives ?? []).map((alternative) => {
              const label = alternativeLabel(
                alternative,
                reference,
                catalog,
                table,
                candidateRootNodeId,
                snapshotIsCurrent,
              );
              return (
                <button
                  key={alternative}
                  type="button"
                  className="rounded-md border border-amber-400 bg-white px-3 py-1.5 text-xs font-semibold text-amber-950 hover:bg-amber-100 disabled:opacity-50"
                  disabled={disabled || !label.available}
                  onClick={() => onChoose(reference, alternative)}
                >
                  {label.label}
                </button>
              );
            })}
          </div>
          {reference.code === 'AMBIGUOUS_ROW_ROOT_OCCURRENCE' &&
          (!snapshotIsCurrent || (reference.alternatives ?? []).some((alternative) =>
            isCatalogRootRouteSelectionToken(alternative) &&
            !resolveCatalogRootRouteSelection(
              alternative,
              catalog,
              table,
              candidateRootNodeId,
              snapshotIsCurrent,
            ))) ? (
            <p className="mt-2 text-xs text-amber-900" role="alert">
              A relationship choice is no longer available. Keep the current rows and choose the starting record type again.
            </p>
          ) : null}
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
