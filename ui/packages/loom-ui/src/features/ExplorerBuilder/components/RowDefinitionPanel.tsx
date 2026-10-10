import React, { useMemo } from 'react';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import {
  startingRootChoices,
  type RootChangeSelection,
} from '../authoring/rootChangeSelection';

export const RowDefinitionPanel = ({
  catalog,
  table,
  disabled,
  onChange,
}: {
  readonly catalog: ExplorerBuilderCatalog;
  readonly table: DraftTable;
  readonly disabled: boolean;
  readonly onChange: (selection: RootChangeSelection) => void;
}) => {
  const choices = useMemo(
    () => startingRootChoices(table, catalog),
    [catalog, table],
  );

  if (!table.document.rootResourceType || choices.length < 2) return null;
  const currentChoice = choices.find((choice) => choice.current);
  if (!currentChoice) return null;

  return (
    <section aria-label="Starting record type" className="mt-3 rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-slate-800">
      <h4 className="font-semibold text-slate-900">Starting record type</h4>
      <p className="mt-1 text-xs text-slate-600">Choose a connected record type for each row.</p>
      <label className="mt-2 block font-medium text-slate-900">
        <span>Choose starting record type</span>
        <select
          aria-label="Choose starting record type"
          className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 outline-blue-500 disabled:opacity-50"
          value={currentChoice.key}
          disabled={disabled}
          onChange={(event) => {
            const choice = choices.find((candidate) => candidate.key === event.currentTarget.value);
            if (choice && !choice.current) onChange(choice.selection);
          }}
        >
          {choices.map((choice) => (
            <option key={choice.key} value={choice.key}>
              {choice.resourceType}{choice.relationship ? ` via ${choice.relationship}` : ''}{choice.current ? ' (current)' : ''}
            </option>
          ))}
        </select>
      </label>
      <p className="mt-2 text-xs text-slate-600">Loom checks whether features, filters, actions, and the starting collection can be preserved before changing rows.</p>
    </section>
  );
};
