import React from 'react';
import type {
  ExplorerBuilderColumn,
  ExplorerColumnSourceDescriptor,
} from '../../../types';

const routeLabel = (
  step: ExplorerColumnSourceDescriptor['route'][number],
  index: number,
): React.ReactNode => (
  <li key={step.occurrenceId} className="flex flex-wrap items-baseline gap-1">
    {index > 0 ? <span aria-hidden="true">→</span> : null}
    <strong className="text-slate-800">{step.resourceType}</strong>
    {step.relationship ? (
      <span className="text-slate-600">
        via {step.relationship}
        {step.storageDirection ? ` · ${step.storageDirection.toLowerCase()}` : ''}
      </span>
    ) : (
      <span className="text-slate-500">row start</span>
    )}
  </li>
);

export const ColumnSourceInspector = ({
  column,
  descriptor,
  onClose,
  onEditInGraph,
}: {
  readonly column: ExplorerBuilderColumn;
  readonly descriptor: ExplorerColumnSourceDescriptor;
  readonly onClose: () => void;
  readonly onEditInGraph?: () => void;
}) => (
  <section
    aria-label={`Source for ${column.label}`}
    className="mt-2 rounded-lg border border-blue-200 bg-blue-50/70 p-3 text-xs text-slate-700"
  >
    <div className="flex items-start justify-between gap-3">
      <div>
        <h3 className="text-sm font-semibold text-slate-900">{column.label}</h3>
        <p className="font-mono text-[11px] text-slate-500">{column.column}</p>
      </div>
      <div className="flex gap-2">
        {onEditInGraph ? (
          <button
            type="button"
            className="rounded border border-blue-300 bg-white px-2 py-1 font-semibold text-blue-800 hover:bg-blue-100"
            onClick={onEditInGraph}
          >
            Edit in graph
          </button>
        ) : null}
        <button
          type="button"
          aria-label={`Close source for ${column.label}`}
          className="rounded px-2 py-1 font-semibold text-slate-600 hover:bg-white"
          onClick={onClose}
        >
          Close
        </button>
      </div>
    </div>
    <div className="mt-3 grid gap-3 lg:grid-cols-2">
      <div>
        <h4 className="font-semibold uppercase tracking-wide text-slate-500">
          Saved route
        </h4>
        <ol className="mt-1 space-y-1">{descriptor.route.map(routeLabel)}</ol>
      </div>
      <div>
        <h4 className="font-semibold uppercase tracking-wide text-slate-500">
          Exact source
        </h4>
        <dl className="mt-1 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-2 gap-y-1">
          <div className="contents">
            <dt className="text-slate-500">Logical type</dt>
            <dd className="break-all font-mono text-slate-800">
              {column.logicalType}
            </dd>
          </div>
          <div className="contents">
            <dt className="text-slate-500">Summary</dt>
            <dd className="break-all font-mono text-slate-800">
              {descriptor.summary}
            </dd>
          </div>
          {descriptor.facts.map((fact) => (
            <div key={`${fact.label}:${fact.value}`} className="contents">
              <dt className="text-slate-500">{fact.label}</dt>
              <dd className="break-all font-mono text-slate-800">{fact.value}</dd>
            </div>
          ))}
        </dl>
      </div>
    </div>
  </section>
);
