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
    <section aria-label="Operation input columns" data-testid="construction-column-selection" className="grid gap-3">
      <div>
        <h3 className="m-0 text-sm font-semibold text-slate-900">Choose input columns</h3>
        <p className="m-0 mt-1 text-xs text-slate-600">
          {hasSelection ? `${selectedColumnIds.length} selected. These fields supply the operation.` : 'Select the fields this operation uses.'}
        </p>
      </div>
      <div className="grid gap-2">
        <label className="grid gap-1 text-xs font-medium text-slate-700">
          Search fields
          <input
            aria-label="Search input columns"
            value={query}
            onChange={(event) => setQuery(event.currentTarget.value)}
            placeholder="Search columns"
            className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
          />
        </label>
        {hasSelection ? (
          <button
            type="button"
            aria-label="Clear selected input columns"
            data-testid="construction-clear-selection"
            disabled={disabled}
            onClick={onClear}
            className="justify-self-start text-xs font-medium text-blue-800 hover:underline disabled:opacity-50"
          >
            Clear
          </button>
        ) : null}
        <div role="listbox" aria-label="Available input columns" aria-multiselectable="true" className="grid max-h-72 gap-x-3 overflow-y-auto sm:grid-cols-2">
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
                className={`flex min-w-0 items-start gap-2 rounded px-2 py-1.5 text-left text-sm text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50 ${selected ? 'font-medium text-blue-900' : ''}`}
              >
                <span aria-hidden="true" className={`mt-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded border text-[10px] ${selected ? 'border-blue-700 bg-blue-700 text-white' : 'border-slate-300 bg-white text-transparent'}`}>✓</span>
                <span className="min-w-0 break-words">{column.label}{column.type ? <span className="ml-1 text-slate-500">· {column.type}</span> : null}</span>
              </button>
            );
          })}
        </div>
        {filtered.length === 0 ? <p className="px-2 py-1 text-sm text-slate-500">{columns.length === 0 ? 'No columns are available to this operation.' : 'No columns match this search.'}</p> : null}
      </div>
    </section>

  );
};
