import React, { useMemo, useState } from 'react';
import type { ExplorerBuilderCatalog } from '../../../types';

export const RowRootPicker = ({
  catalog,
  disabled,
  onChoose,
}: {
  readonly catalog: ExplorerBuilderCatalog;
  readonly disabled: boolean;
  readonly onChoose: (nodeId: string) => void;
}) => {
  const [query, setQuery] = useState('');
  const choices = useMemo(() => {
    const search = query.trim().toLocaleLowerCase();
    return catalog.nodes
      .filter((node) => node.rowRootEligible)
      .filter(
        (node) =>
          search.length === 0 ||
          node.resourceType.toLocaleLowerCase().includes(search),
      )
      .sort((left, right) => {
        if (left.populated !== right.populated) return left.populated ? -1 : 1;
        return left.resourceType.localeCompare(right.resourceType);
      });
  }, [catalog.nodes, query]);

  return (
    <section
      aria-label="Choose row type"
      className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm"
    >
      <h2 className="text-base font-semibold text-slate-900">
        What should one row represent?
      </h2>
      <p className="mt-0.5 text-xs text-slate-600">
        Choose the record type to start from. You can change or group rows later.
      </p>
      <label className="mt-3 block text-sm font-medium text-slate-800">
        Search row types
        <input
          aria-label="Search row types"
          value={query}
          onChange={(event) => setQuery(event.currentTarget.value)}
          placeholder="Patient, Specimen, Observation…"
          className="mt-1 min-h-11 w-full rounded-md border border-slate-300 bg-white px-3 py-2 outline-blue-500 focus:border-blue-500"
        />
      </label>
      <div className="mt-3 max-h-[28rem] min-h-32 overflow-y-auto overscroll-contain rounded-lg border border-slate-200 bg-slate-50/60 p-2">
        <div className="grid gap-1.5 sm:grid-cols-2">
          {choices.map((node) => {
            const unavailable = !node.populated;
            return (
              <button
                key={node.nodeId}
                type="button"
                aria-label={`Choose ${node.resourceType} rows`}
                disabled={disabled || unavailable}
                onClick={() => onChoose(node.nodeId)}
                className="flex min-h-12 items-center justify-between gap-3 rounded-md border border-slate-200 bg-white px-3 py-2 text-left hover:border-blue-400 hover:bg-blue-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 disabled:cursor-not-allowed disabled:opacity-55"
              >
                <span className="min-w-0 truncate font-semibold text-slate-900">
                  {node.resourceType}
                </span>
                <span className="shrink-0 text-right text-[11px] text-slate-600">
                  {unavailable
                    ? 'No records'
                    : typeof node.documentCount === 'number'
                      ? `${node.documentCount.toLocaleString()} records`
                      : 'Available'}
                </span>
              </button>
            );
          })}
        </div>
      </div>
      {choices.length === 0 ? (
        <p className="mt-4 text-sm text-slate-600">
          No eligible row types match this search.
        </p>
      ) : null}
    </section>
  );
};
