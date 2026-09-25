import React from 'react';
import type { ConstructionOperationFamily } from './ConstructionWorkspace';

export interface ConstructionSelectableColumn {
  readonly id: string;
  readonly label: string;
  readonly type?: string;
}

export const ConstructionColumnSelection = ({
  columns,
  selectedColumnIds,
  disabled = false,
  onToggleColumn,
  onClear,
  onOpenFamily,
}: {
  readonly columns: ReadonlyArray<ConstructionSelectableColumn>;
  readonly selectedColumnIds: ReadonlyArray<string>;
  readonly disabled?: boolean;
  readonly onToggleColumn: (columnId: string) => void;
  readonly onClear: () => void;
  readonly onOpenFamily: (family: ConstructionOperationFamily) => void;
}) => {
  const hasSelection = selectedColumnIds.length > 0;

  return (
    <section aria-label="Selected columns" data-testid="construction-column-selection" className="rounded-lg border border-slate-200 bg-white px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="mr-1 text-xs font-semibold text-slate-600">
          {hasSelection ? 'Selected columns' : 'Select columns for an action'}
        </span>
        {columns.length > 0 ? columns.map((column) => {
          const selected = selectedColumnIds.includes(column.id);
          return (
            <button
              key={column.id}
              type="button"
              aria-pressed={selected}
              aria-label={`${selected ? 'Remove' : 'Select'} ${column.label}${column.type ? ` (${column.type})` : ''}`}
              data-testid={`construction-column-${column.id}`}
              disabled={disabled}
              onClick={() => onToggleColumn(column.id)}
              className={`max-w-full truncate rounded-full border px-2.5 py-1 text-xs ${
                selected
                  ? 'border-emerald-700 bg-emerald-50 font-medium text-emerald-950'
                  : 'border-slate-300 bg-white text-slate-700 hover:border-emerald-400 hover:bg-emerald-50/50'
              } disabled:cursor-not-allowed disabled:opacity-50`}
            >
              {column.label}
              {column.type ? <span className="ml-1 text-slate-500">· {column.type}</span> : null}
            </button>
          );
        }) : (
          <span className="text-xs text-slate-500">Add a column before selecting inputs.</span>
        )}

        {hasSelection ? (
          <div className="ml-auto flex flex-wrap items-center gap-1.5">
            <span className="text-xs text-slate-500">Use selection</span>
            <button
              type="button"
              data-testid="construction-selection-keep-rows"
              disabled={disabled}
              onClick={() => onOpenFamily('KEEP_ROWS')}
              className="rounded border border-slate-300 px-2 py-1 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50"
            >
              Keep rows
            </button>
            <button
              type="button"
              aria-label="Clear selected columns"
              data-testid="construction-clear-selection"
              disabled={disabled}
              onClick={onClear}
              className="rounded px-1.5 py-1 text-xs text-slate-500 hover:bg-slate-100 disabled:opacity-50"
            >
              Clear
            </button>
          </div>
        ) : null}
      </div>
    </section>
  );
};
