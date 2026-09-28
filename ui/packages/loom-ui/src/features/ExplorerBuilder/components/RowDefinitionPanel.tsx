import React, { useMemo } from 'react';
import type { ExplorerBuilderCatalog } from '../../../types';
import { derivedOccurrences, type DraftTable } from '../authoring/model';

type RowChoice = {
  readonly occurrenceId: string;
  readonly nodeId: string;
  readonly resourceType: string;
  readonly relationship?: string;
};

export const RowDefinitionPanel = ({
  catalog,
  table,
  disabled,
  onChange,
}: {
  readonly catalog: ExplorerBuilderCatalog;
  readonly table: DraftTable;
  readonly disabled: boolean;
  readonly onChange: (nodeId: string, occurrenceId: string) => void;
}) => {
  const choices = useMemo<ReadonlyArray<RowChoice>>(() => {
    const eligibleNodeIds = new Set(
      catalog.nodes.filter((node) => node.rowRootEligible).map((node) => node.nodeId),
    );
    return derivedOccurrences(table, catalog)
      .filter((occurrence) => eligibleNodeIds.has(occurrence.nodeId))
      .map((occurrence) => ({
        occurrenceId: occurrence.id,
        nodeId: occurrence.nodeId,
        resourceType: occurrence.resourceType,
        relationship: occurrence.relationship,
      }));
  }, [catalog, table]);

  if (!table.document.rootResourceType || choices.length < 2) return null;

  return (
    <section aria-label="Row occurrence settings" className="rounded-lg border border-slate-200 p-3 text-sm text-slate-800">
      <label className="block font-medium text-slate-900">
        <span>One row per</span>
        <select
          aria-label="One row per"
          className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 outline-blue-500 disabled:opacity-50"
          value="base"
          disabled={disabled}
          onChange={(event) => {
            const choice = choices.find((candidate) => candidate.occurrenceId === event.currentTarget.value);
            if (choice && choice.occurrenceId !== 'base') onChange(choice.nodeId, choice.occurrenceId);
          }}
        >
          {choices.map((choice) => (
            <option key={choice.occurrenceId} value={choice.occurrenceId}>
              {choice.resourceType}{choice.relationship ? ` via ${choice.relationship}` : ''}
            </option>
          ))}
        </select>
      </label>
      <p className="mt-2 text-xs text-slate-500">Loom checks that features, filters, actions, and the starting collection can be preserved before changing rows.</p>
    </section>
  );
};
