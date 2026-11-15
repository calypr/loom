import React, { useState } from 'react';
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
  const [query, setQuery] = useState('');
  const search = query.trim().toLowerCase();
  const filtered = columns.filter((column) =>
    !search || column.label.toLowerCase().includes(search) || (column.type ?? '').toLowerCase().includes(search));

  return (
    <section aria-label="Operation input columns" data-testid="construction-column-selection" className="px-0 py-1">
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
      <div className="mt-2">
        <div className="border-b border-slate-100 p-1.5">
          <input
            aria-label="Search input columns"
            value={query}
            onChange={(event) => setQuery(event.currentTarget.value)}
            placeholder="Search columns"
            className="w-full rounded border border-slate-300 px-2 py-1 text-xs"
          />
        </div>
        <div role="listbox" aria-label="Available input columns" aria-multiselectable="true" className="max-h-44 overflow-y-auto p-1">
          {filtered.map((column) => {
            const selected = selectedColumnIds.includes(column.id);
            return (
              <button
                key={column.id}
                type="button"
                role="option"
                aria-selected={selected}
                aria-pressed={selected}
                aria-label={`${selected ? 'Remove' : 'Select'} ${column.label}${column.type ? ` (${column.type})` : ''}`}
                data-testid={`construction-column-${column.id}`}
                disabled={disabled}
                onClick={() => onToggleColumn(column.id)}
                className={`flex w-full items-center justify-between gap-2 rounded px-2 py-1 text-left text-xs ${
                  selected ? 'bg-emerald-50 font-medium text-emerald-950' : 'text-slate-700 hover:bg-slate-50'
                } disabled:cursor-not-allowed disabled:opacity-50`}
              >
                <span className="min-w-0 truncate">{column.label}{column.type ? <span className="ml-1 text-slate-500">· {column.type}</span> : null}</span>
                {selected ? <span aria-hidden="true" className="shrink-0 text-emerald-700">✓</span> : null}
              </button>
            );
          })}
          {filtered.length === 0 ? <p className="px-2 py-1 text-xs text-slate-500">{columns.length === 0 ? 'No columns are available to this operation.' : 'No columns match this search.'}</p> : null}
        </div>
      </div>
    </section>
  );
};
