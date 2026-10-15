import React from 'react';
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
}: {
  readonly columns: ReadonlyArray<ConstructionSelectableColumn>;
  readonly selectedColumnIds: ReadonlyArray<string>;
  readonly disabled?: boolean;
  readonly onToggleColumn: (columnId: string) => void;
  readonly onClear: () => void;
}) => {
  const hasSelection = selectedColumnIds.length > 0;

  return (
    <section aria-label="Operation input columns" data-testid="construction-column-selection" className="rounded-lg border border-slate-200 bg-slate-50 px-3 py-2">
      <div className="mb-1 flex items-center gap-2">
        <h3 className="text-xs font-semibold text-slate-700">Choose input columns</h3>
        <span className="text-xs text-slate-500">{hasSelection ? `${selectedColumnIds.length} selected` : 'Select the fields this operation uses'}</span>
        {hasSelection ? (
          <button
            type="button"
            aria-label="Clear selected input columns"
            data-testid="construction-clear-selection"
            disabled={disabled}
            onClick={onClear}
            className="ml-auto rounded px-1.5 py-0.5 text-xs text-slate-500 hover:bg-white disabled:opacity-50"
          >
            Clear
          </button>
        ) : null}
      </div>
      <div className="flex flex-wrap items-center gap-2">
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
          <span className="text-xs text-slate-500">No columns are available to this operation.</span>
        )}
      </div>
    </section>
  );
};
