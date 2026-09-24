import React, { useMemo } from 'react';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';

type RowChoice = {
  readonly nodeId: string;
  readonly resourceType: string;
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
  readonly onChange: (nodeId: string) => void;
}) => {
  const choices = useMemo<ReadonlyArray<RowChoice>>(() => {
    return catalog.nodes
      .filter((node) => node.rowRootEligible && (node.populated || node.resourceType === table.document.rootResourceType))
      .map((node) => ({ nodeId: node.nodeId, resourceType: node.resourceType }))
      .sort((left, right) => left.resourceType.localeCompare(right.resourceType));
  }, [catalog.nodes, table.document.rootResourceType]);

  const current = choices.find((choice) => choice.resourceType === table.document.rootResourceType);

  if (!table.document.rootResourceType || choices.length === 0) return null;

  return (
    <section aria-label="Row definition" className="rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm text-slate-800 shadow-sm">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="min-w-0">
          <h2 className="font-semibold text-slate-900">Rows</h2>
          <p className="mt-0.5 text-xs text-slate-600">Each row represents one record of this type.</p>
        </div>
        <label className="flex min-h-11 items-center gap-2 font-medium text-slate-900">
          <span className="whitespace-nowrap">One row per</span>
          <select
            aria-label="One row per"
            className="min-h-11 min-w-40 rounded-md border border-slate-300 bg-white px-3 py-2 outline-blue-500 focus:border-blue-500 disabled:opacity-50"
            value={current?.nodeId ?? ''}
            disabled={disabled || choices.length === 1}
            onChange={(event) => {
              const choice = choices.find((candidate) => candidate.nodeId === event.currentTarget.value);
              if (choice && choice.resourceType !== table.document.rootResourceType) onChange(choice.nodeId);
            }}
          >
            {choices.map((choice) => (
              <option key={choice.nodeId} value={choice.nodeId}>
                {choice.resourceType}
              </option>
            ))}
          </select>
        </label>
      </div>
      {choices.length === 1 ? (
        <p className="sr-only">This is the only populated row type available in the project.</p>
      ) : (
        <p className="mt-1 text-[11px] text-slate-500">Changing rows restarts this table and clears its current columns and settings after confirmation.</p>
      )}
    </section>
  );
};
