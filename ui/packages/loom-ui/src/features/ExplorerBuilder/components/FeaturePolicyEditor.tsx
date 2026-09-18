import React from 'react';
import type {
  ExplorerBuilderCandidate,
  ExplorerBuilderColumn,
  ExplorerColumnSource,
} from '../../../types';

export const RelatedFeatureCreator = ({
  resourceLabel,
  disabled,
  onAdd,
}: {
  readonly resourceLabel: string;
  readonly disabled: boolean;
  readonly onAdd: (source: ExplorerColumnSource, title: string) => void;
}) => (
  <div className="flex items-center gap-1.5">
    <span className="text-[11px] font-medium text-slate-600">Add relationship feature</span>
    <button
      type="button"
      disabled={disabled}
      className="rounded border border-slate-300 bg-white px-2 py-1 text-[11px] font-semibold text-slate-700 hover:bg-slate-50 disabled:opacity-40"
      onClick={() =>
        onAdd(
          { kind: 'aggregate', aggregate: { operation: 'COUNT' } },
          `${resourceLabel} count`,
        )
      }
    >
      Count
    </button>
    <button
      type="button"
      disabled={disabled}
      className="rounded border border-slate-300 bg-white px-2 py-1 text-[11px] font-semibold text-slate-700 hover:bg-slate-50 disabled:opacity-40"
      onClick={() =>
        onAdd(
          { kind: 'aggregate', aggregate: { operation: 'EXISTS' } },
          `Has ${resourceLabel}`,
        )
      }
    >
      Yes / no
    </button>
  </div>
);

const nestedValueLabels = {
  VALUE: 'Single value',
  INDEXED: 'Indexed value',
  FIRST: 'First value',
  ALL: 'All values',
  DISTINCT: 'Unique values',
} as const;

const resourceAggregateLabels = {
  COUNT: 'Count matching resources',
  EXISTS: 'Whether any resource matches',
} as const;

const fieldAggregateLabels = {
  COUNT_DISTINCT: 'Count unique values',
  DISTINCT_VALUES: 'Collect unique values',
  MIN: 'Minimum value',
  MAX: 'Maximum value',
} as const;

const relatedValueReductionLabels = {
  FIRST_BY_RESOURCE_KEY: 'First record by stable resource key',
  DISTINCT_VALUES: fieldAggregateLabels.DISTINCT_VALUES,
  COUNT_DISTINCT: fieldAggregateLabels.COUNT_DISTINCT,
  MIN: fieldAggregateLabels.MIN,
  MAX: fieldAggregateLabels.MAX,
} as const;

type ResourceAggregateOperation = keyof typeof resourceAggregateLabels;
type FieldAggregateOperation = keyof typeof fieldAggregateLabels;

const projectionExplanation = (
  mode: keyof typeof nestedValueLabels,
  resourceLabel: string,
): string => {
  switch (mode) {
    case 'ALL':
      return `Keeps every repeated value within each ${resourceLabel}.`;
    case 'DISTINCT':
      return `Keeps unique repeated values within each ${resourceLabel}; order and frequency are discarded.`;
    case 'FIRST':
      return `Keeps the first repeated value within each ${resourceLabel}; later values are discarded.`;
    case 'INDEXED':
      return `Keeps values by their stable position within each ${resourceLabel}.`;
    case 'VALUE':
      return `Keeps the scalar value from each ${resourceLabel}.`;
  }
};

export const FeaturePolicyEditor = ({
  column,
  candidate,
  candidates,
  related,
  resourceLabel,
  disabled,
  onSourceChange,
  onContributorChange,
}: {
  readonly column: ExplorerBuilderColumn;
  readonly candidate?: ExplorerBuilderCandidate;
  readonly candidates: ReadonlyArray<ExplorerBuilderCandidate>;
  readonly related: boolean;
  readonly resourceLabel: string;
  readonly disabled: boolean;
  readonly onSourceChange: (source: ExplorerColumnSource) => void;
  readonly onContributorChange: (
    contributor: ExplorerBuilderColumn['contributor'],
  ) => void;
}) => {
  if (column.source.kind === 'field') {
    const source = column.source;
    const currentMode = source.field.projectionMode ?? 'FIRST';
    const projectionModes = candidate?.projectionModes ?? [currentMode];
    const uniqueModes = [...new Set(projectionModes)];

    return (
      <div className="col-span-full flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-600">
        {related && source.field.relatedSelection ? (
          <label className="flex items-center gap-1.5 font-medium text-slate-700">
            <span>Across records</span>
            <select
              aria-label={`Across related ${resourceLabel} records for ${column.label}`}
              className="max-w-64 rounded border border-slate-300 bg-white px-1.5 py-0.5 text-xs font-normal"
              value="FIRST_BY_RESOURCE_KEY"
              disabled={disabled}
              onChange={(event) => {
                const operation = event.currentTarget.value;
                if (operation === 'FIRST_BY_RESOURCE_KEY') return;
                onSourceChange({
                  kind: 'aggregate',
                  aggregate: {
                    operation: operation as FieldAggregateOperation,
                    path: source.field.path,
                  },
                });
              }}
            >
              {Object.entries(relatedValueReductionLabels).map(([value, label]) => (
                <option key={value} value={value}>{label}</option>
              ))}
            </select>
          </label>
        ) : null}
        {uniqueModes.length > 1 ? (
          <label className="flex items-center gap-1.5 font-medium text-slate-700">
            <span>Repeated values</span>
            <select
              aria-label={`Repeated values for ${column.label}`}
              className="rounded border border-slate-300 bg-white px-1.5 py-0.5 text-xs font-normal"
              value={currentMode}
              disabled={disabled}
              onChange={(event) =>
                onSourceChange({
                  ...source,
                  field: {
                    ...source.field,
                    projectionMode: event.currentTarget.value as keyof typeof nestedValueLabels,
                  },
                })
              }
            >
              {uniqueModes.map((mode) => (
                <option key={mode} value={mode}>
                  {nestedValueLabels[mode]}
                </option>
              ))}
            </select>
          </label>
        ) : null}
        <span>{projectionExplanation(currentMode, resourceLabel)}</span>
        {source.field.relatedSelection ? (
          <label className="flex items-start gap-1 text-amber-800">
            <input
              type="checkbox"
              aria-label={`Allow first related value for ${column.label}`}
              checked={source.field.relatedSelection.acknowledged}
              disabled={disabled}
              onChange={(event) =>
                onSourceChange({
                  ...source,
                  field: {
                    ...source.field,
                    relatedSelection: {
                      kind: 'first-by-resource-key',
                      acknowledged: event.currentTarget.checked,
                    },
                  },
                })
              }
            />
            Keep only the first related record by resource key. Other records are omitted.
          </label>
        ) : null}
      </div>
    );
  }

  if (column.source.kind === 'aggregate') {
    const path = column.source.aggregate.path;
    const operation = column.source.aggregate.operation;
    const options = path && related ? relatedValueReductionLabels : path ? fieldAggregateLabels : resourceAggregateLabels;
    const summary = path
      ? `${fieldAggregateLabels[operation as FieldAggregateOperation] ?? 'Reduces values'} from ${path} across matching ${resourceLabel} resources.`
      : `${operation === 'COUNT' ? 'Counts' : 'Checks for'} matching ${resourceLabel} resources.`;

    return (
      <div className="col-span-full flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-600">
        <label className="flex items-center gap-1.5 font-medium text-slate-700">
          <span>{path && related ? 'Across records' : 'Calculation'}</span>
          <select
            aria-label={path && related
              ? `Across related ${resourceLabel} records for ${column.label}`
              : `Calculation for ${column.label}`}
            className="rounded border border-slate-300 bg-white px-1.5 py-0.5 text-xs font-normal"
            value={operation}
            disabled={disabled || operation === 'CONTAINS_ALL'}
            onChange={(event) => {
              const nextOperation = event.currentTarget.value as
                | ResourceAggregateOperation
                | FieldAggregateOperation
                | 'FIRST_BY_RESOURCE_KEY';
              if (nextOperation === 'FIRST_BY_RESOURCE_KEY') {
                if (!path) return;
                onSourceChange({
                  kind: 'field',
                  field: {
                    path,
                    projectionMode: candidate?.defaultProjectionMode ?? 'VALUE',
                    relatedSelection: {
                      kind: 'first-by-resource-key',
                      acknowledged: false,
                    },
                  },
                });
                return;
              }
              onSourceChange({
                kind: 'aggregate',
                aggregate: path
                  ? { operation: nextOperation as FieldAggregateOperation, path }
                  : { operation: nextOperation as ResourceAggregateOperation },
              });
            }}
          >
            {Object.entries(options).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
            {operation === 'CONTAINS_ALL' ? (
              <option value="CONTAINS_ALL">Contains every required value</option>
            ) : null}
          </select>
        </label>
        <span>{summary}</span>
        <label className="flex items-center gap-1.5 font-medium text-slate-700">
          <span>Contributors</span>
          <select
            aria-label={`Contributors for ${column.label}`}
            className="max-w-64 rounded border border-slate-300 bg-white px-1.5 py-0.5 text-xs font-normal"
            value={column.contributor?.candidateId ?? ''}
            disabled={disabled}
            onChange={(event) => {
              const selected = candidates.find(
                ({ candidateId }) => candidateId === event.currentTarget.value,
              );
              if (!selected) {
                onContributorChange(undefined);
                return;
              }
              onContributorChange({
                candidateId: selected.candidateId,
                operator: 'EXISTS',
                ...(selected.repeated ? { quantifier: 'ANY' as const } : {}),
              });
            }}
          >
            <option value="">All matching {resourceLabel} resources</option>
            {candidates.map((candidateOption) => (
              <option key={candidateOption.candidateId} value={candidateOption.candidateId}>
                Where {candidateOption.label} exists
              </option>
            ))}
          </select>
        </label>
      </div>
    );
  }

  return null;
};
