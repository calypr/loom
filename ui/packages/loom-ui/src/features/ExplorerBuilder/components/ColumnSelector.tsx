import React, { useEffect, useMemo, useRef, useState } from 'react';
import type {
  AggregateOperationCapability,
  ColumnTransformationChange,
  ExplorerBuilderCandidate,
  ExplorerBuilderCatalog,
  ExplorerBuilderColumn,
  ExplorerColumnSourceDescriptor,
  ExplorerColumnSource,
} from '../../../types';
import type {
  ConfiguredColumnContext,
  ConfiguredColumnContextResponse,
} from '../../../interpretation';
import { derivedOccurrences, type DraftTable } from '../authoring/model';
import {
  FeaturePolicyEditor,
  RelatedFeatureCreator,
} from './FeaturePolicyEditor';
import {
  ColumnSourceInspector,
} from './ColumnSourceInspector';
import { useVirtualViewport, virtualRange } from './virtualization';

const CANDIDATE_ROW_HEIGHT = 128;

const titleForResource = (value: string): string =>
  value
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .replace(/[_.]/g, ' ')
    .replace(/\b\w/g, (letter) => letter.toUpperCase());

const safeName = (value: string): string => {
  const normalized = value
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .toLowerCase()
    .replace(/[^a-z0-9_]/g, '_')
    .replace(/^_+|_+$/g, '');
  if (!normalized) return 'column';
  return /^[0-9]/.test(normalized) ? `x_${normalized}` : normalized;
};

const candidateFieldName = (fieldPath: string): string => {
  const segments = fieldPath
    .replace(/^root\./, '')
    .split('.')
    .map((segment) => segment.replace(/\[\]/g, ''))
    .filter(Boolean);
  if (segments.length > 1 && segments[segments.length - 1] === 'value')
    segments.pop();
  return safeName(segments.join('_'));
};

const isTabularCandidate = (candidate: ExplorerBuilderCandidate): boolean =>
  !['', 'unknown', 'object', 'array'].includes(
    candidate.logicalType.trim().toLowerCase(),
  );

type InitialPresentation = 'TABLE' | 'FILTER' | 'CHART';

const candidateColumnName = (
  candidate: ExplorerBuilderCandidate,
  occurrenceId: string,
  resourceType: string,
  existing: ReadonlySet<string>,
): string => {
  const leaf = candidateFieldName(candidate.fieldPath);
  const resourcePrefix = resourceType.trim() ? safeName(resourceType) : '';
  const base =
    occurrenceId === 'base'
      ? [resourcePrefix, leaf].filter(Boolean).join('_')
      : `${safeName(occurrenceId)}__${leaf}`;
  let value = base;
  let suffix = 2;
  while (existing.has(value)) value = `${base}_${suffix++}`;
  return value;
};

const sourceSummary = (column: ExplorerBuilderColumn): string =>
  [column.logicalType, column.source.kind].filter(Boolean).join(' · ');

const ConfiguredColumnRow = ({
  column,
  order,
  disabled,
  filterable,
  chartable,
  candidate,
  resolution,
  candidates,
  related,
  rowContext,
  resourceLabel,
  onChange,
  onSourceChange,
  onContributorChange,
  onTransformationChange,
  onMoveToEnd,
  moveToEndDisabled,
  onRemove,
  onInspect,
}: {
  readonly column: ExplorerBuilderColumn;
  readonly order: number;
  readonly disabled: boolean;
  readonly filterable: boolean;
  readonly chartable: boolean;
  readonly candidate?: ExplorerBuilderCandidate;
  readonly resolution?: ConfiguredColumnContext['resolution'];
  readonly candidates: ReadonlyArray<ExplorerBuilderCandidate>;
  readonly related: boolean;
  readonly rowContext: AggregateOperationCapability['rowContext'] | undefined;
  readonly resourceLabel: string;
  readonly onChange: (value: ExplorerBuilderColumn) => void;
  readonly onSourceChange: (column: string, source: ExplorerColumnSource) => void;
  readonly onContributorChange: (
    column: string,
    contributor: ExplorerBuilderColumn['contributor'],
  ) => void;
  readonly onTransformationChange: (
    column: string,
    change: ColumnTransformationChange,
  ) => void;
  readonly onMoveToEnd: () => void;
  readonly moveToEndDisabled: boolean;
  readonly onRemove: () => void;
  readonly onInspect: () => void;
}) => {
  const [label, setLabel] = useState(column.label);

  const commitLabel = () => {
    const next = label.trim();
    if (!next) {
      setLabel(column.label);
      return;
    }
    if (next !== column.label) onChange({ ...column, label: next });
  };
  const visible = column.table?.visible ?? Boolean(column.table);

  return (
    <div className="grid grid-cols-[minmax(0,1fr)_3.5rem_3.5rem_3.5rem_2rem_2rem] items-center gap-2 border-b border-slate-200 px-2 py-1.5 last:border-b-0 hover:bg-slate-50/70">
      <div className="min-w-0">
        <input
          aria-label={`Display name for configured ${column.label}`}
          className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm font-medium text-slate-800 outline-blue-500 focus:border-blue-500"
          value={label}
          disabled={disabled}
          onChange={(event) => setLabel(event.currentTarget.value)}
          onBlur={commitLabel}
          onKeyDown={(event) => {
            if (event.key === 'Enter') event.currentTarget.blur();
            if (event.key === 'Escape') {
              setLabel(column.label);
              event.currentTarget.blur();
            }
          }}
        />
        <div className="break-all px-1 font-mono text-[10px] leading-tight text-slate-400">
          {column.column} · {sourceSummary(column)}
        </div>
        {resolution && resolution.state !== 'READY' ? (
          <p className="mt-1 px-1 text-[11px] text-amber-800">{resolution.reason}</p>
        ) : null}
        <button
          type="button"
          className="ml-1 mt-1 text-[11px] font-semibold text-blue-700 hover:text-blue-900 hover:underline"
          onClick={onInspect}
        >
          Column details
        </button>
      </div>
      <label className="flex justify-center" title="Display in table">
        <span className="sr-only">Table</span>
        <input
          aria-label={`Display ${column.label} in table`}
          type="checkbox"
          checked={visible}
          disabled={disabled}
          onChange={(event) =>
            onChange({
              ...column,
              table: {
                ...(column.table ?? {}),
                visible: event.currentTarget.checked,
                order: column.table?.order ?? order,
              },
            })
          }
        />
      </label>
      <label
        className="flex justify-center"
        title={
          filterable
            ? 'Use as filter'
            : 'Filters are unavailable for this field type'
        }
      >
        <span className="sr-only">Filter</span>
        <input
          aria-label={`Use ${column.label} as filter`}
          type="checkbox"
          checked={Boolean(column.filter)}
          disabled={disabled || (!filterable && !column.filter)}
          onChange={(event) =>
            onChange({
              ...column,
              filter: event.currentTarget.checked
                ? { label: column.label }
                : undefined,
            })
          }
        />
      </label>
      <label
        className="flex justify-center"
        title={
          chartable
            ? 'Use as chart'
            : 'Charts are unavailable for this field type'
        }
      >
        <span className="sr-only">Chart</span>
        <input
          aria-label={`Use ${column.label} as chart`}
          type="checkbox"
          checked={Boolean(column.chart)}
          disabled={disabled || (!chartable && !column.chart)}
          onChange={(event) =>
            onChange({
              ...column,
              chart: event.currentTarget.checked
                ? { type: 'bar', title: column.label }
                : undefined,
            })
          }
        />
      </label>
      <button
        type="button"
        aria-label={`Move ${column.label} to end`}
        title="Move to end"
        className="h-6 w-6 rounded text-sm leading-none text-slate-600 hover:bg-slate-100 disabled:opacity-30"
        disabled={disabled || moveToEndDisabled}
        onClick={onMoveToEnd}
      >
        ↓
      </button>
      <button
        type="button"
        aria-label={`Remove ${column.label}`}
        title="Remove configured column"
        className="h-6 w-6 rounded text-base leading-none text-red-600 hover:bg-red-50 disabled:opacity-40"
        disabled={disabled}
        onClick={onRemove}
      >
        ×
      </button>
      <FeaturePolicyEditor
        column={column}
        candidate={candidate}
        candidates={candidates}
        related={related}
        rowContext={rowContext}
        resourceLabel={resourceLabel}
        disabled={disabled}
        onSourceChange={(source) => onSourceChange(column.column, source)}
        onContributorChange={(contributor) =>
          onContributorChange(column.column, contributor)
        }
        onTransformationChange={(change) =>
          onTransformationChange(column.column, change)
        }
      />
    </div>
  );
};

const AvailableColumnRow = ({
  candidate,
  displayName,
  disabled,
  onDisplayNameChange,
  onAdd,
}: {
  readonly candidate: ExplorerBuilderCandidate;
  readonly displayName: string;
  readonly disabled: boolean;
  readonly onDisplayNameChange: (value: string) => void;
  readonly onAdd: (
    displayName: string,
    initialPresentation: InitialPresentation,
  ) => void;
}) => {
  const normalizedDisplayName = displayName.trim();

  return (
    <div className="grid grid-cols-[minmax(0,1fr)_3.5rem_3.5rem_3.5rem_2rem_2rem] items-center gap-2 border-b border-slate-200 px-2 py-1.5 last:border-b-0 hover:bg-blue-50/40">
      <div className="min-w-0">
        <input
          aria-label={`Display name for available ${candidate.label}`}
          className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm font-medium text-slate-700 outline-blue-500 focus:border-blue-500"
          value={displayName}
          disabled={disabled}
          onChange={(event) => onDisplayNameChange(event.currentTarget.value)}
          onBlur={() => {
            if (!displayName.trim()) onDisplayNameChange(candidate.label);
          }}
        />
        <div className="break-all px-1 font-mono text-[10px] leading-tight text-slate-400">
          {candidate.fieldPath} · {candidate.logicalType}
          {candidate.repeated ? ' · repeated' : ''}
        </div>
      </div>
      <label className="flex justify-center" title="Add to table">
        <span className="sr-only">Table</span>
        <input
          aria-label={`Add ${normalizedDisplayName || candidate.label} to table`}
          type="checkbox"
          checked={false}
          disabled={disabled || !normalizedDisplayName}
          onChange={(event) =>
            event.currentTarget.checked && onAdd(normalizedDisplayName, 'TABLE')
          }
        />
      </label>
      <label
        className="flex justify-center"
        title={
          candidate.filterable
            ? 'Add as filter'
            : 'Filters are unavailable for this field type'
        }
      >
        <input
          aria-label={`Add ${normalizedDisplayName || candidate.label} as filter`}
          type="checkbox"
          checked={false}
          disabled={disabled || !normalizedDisplayName || !candidate.filterable}
          onChange={(event) =>
            event.currentTarget.checked &&
            onAdd(normalizedDisplayName, 'FILTER')
          }
        />
      </label>
      <label
        className="flex justify-center"
        title={
          candidate.chartable
            ? 'Add as chart'
            : 'Charts are unavailable for this field type'
        }
      >
        <input
          aria-label={`Add ${normalizedDisplayName || candidate.label} as chart`}
          type="checkbox"
          checked={false}
          disabled={disabled || !normalizedDisplayName || !candidate.chartable}
          onChange={(event) =>
            event.currentTarget.checked && onAdd(normalizedDisplayName, 'CHART')
          }
        />
      </label>
      <span />
      <span />
    </div>
  );
};

export const ColumnSelector = ({
  catalog,
  interpretationContext,
  table,
  occurrenceId,
  focusColumn,
  disabled,
  loadingCandidates = false,
  onAdd,
  onAddAll,
  onAddSource,
  onChange,
  onColumnsChange,
  onSourceChange,
  onContributorChange = () => undefined,
  onTransformationChange = () => undefined,
  onRemove,
  onInspectSource,
  onEditInGraph,
  showAvailable = true,
}: {
  readonly catalog: ExplorerBuilderCatalog;
  readonly interpretationContext?: ConfiguredColumnContextResponse;
  readonly table?: DraftTable;
  readonly occurrenceId: string;
  readonly focusColumn?: string;
  readonly disabled: boolean;
  readonly loadingCandidates?: boolean;
  readonly onAdd: (
    candidate: ExplorerBuilderCandidate,
    displayName: string,
    initialPresentation: InitialPresentation,
  ) => void;
  readonly onAddAll: (
    candidates: ReadonlyArray<ExplorerBuilderCandidate>,
  ) => void;
  readonly onAddSource?: (source: ExplorerColumnSource, title: string) => void;
  readonly onChange: (column: ExplorerBuilderColumn) => void;
  readonly onColumnsChange?: (
    columns: ReadonlyArray<ExplorerBuilderColumn>,
  ) => void;
  readonly onSourceChange: (column: string, source: ExplorerColumnSource) => void;
  readonly onContributorChange?: (
    column: string,
    contributor: ExplorerBuilderColumn['contributor'],
  ) => void;
  readonly onTransformationChange?: (
    column: string,
    change: ColumnTransformationChange,
  ) => void;
  readonly onRemove: (column: string) => void;
  readonly onInspectSource?: (
    column: ExplorerBuilderColumn,
  ) => Promise<ExplorerColumnSourceDescriptor>;
  readonly onEditInGraph?: (column: ExplorerBuilderColumn) => void;
  readonly showAvailable?: boolean;
}) => {
  const [query, setQuery] = useState('');
  const [inspection, setInspection] = useState<{
    readonly column: string;
    readonly loading: boolean;
    readonly descriptor?: ExplorerColumnSourceDescriptor;
    readonly error?: string;
  }>();
  const inspectionGeneration = useRef(0);
  useEffect(() => {
    if (focusColumn) setQuery(focusColumn);
  }, [focusColumn]);
  const [availableDisplayNames, setAvailableDisplayNames] = useState<Readonly<Record<string, string>>>({});
  const { viewport, ref: candidateScrollRef } =
    useVirtualViewport<HTMLDivElement>();
  const occurrences = derivedOccurrences(table, catalog);
  const occurrence = occurrences.find(
    (candidate) => candidate.id === occurrenceId,
  );
  const resourceType =
    catalog.nodes.find((node) => node.nodeId === occurrence?.nodeId)
      ?.resourceType ?? 'resource';
  const configured = useMemo(
    () =>
      showAvailable
        ? (table?.document.columns ?? []).filter(
            (column) => column.occurrenceId === occurrenceId,
          )
        : (table?.document.columns ?? []),
    [occurrenceId, showAvailable, table?.document.columns],
  );
  const inspectedColumn = configured.find(
    (column) => column.column === inspection?.column,
  );
  const inspectColumn = async (column: ExplorerBuilderColumn) => {
    if (!onInspectSource) return;
    const generation = ++inspectionGeneration.current;
    setInspection({ column: column.column, loading: true });
    try {
      const descriptor = await onInspectSource(column);
      if (generation !== inspectionGeneration.current) return;
      if (descriptor.column !== column.column) {
        throw new Error('Loom returned source details for a different column.');
      }
      setInspection({ column: column.column, loading: false, descriptor });
    } catch (error) {
      if (generation !== inspectionGeneration.current) return;
      setInspection({
        column: column.column,
        loading: false,
        error:
          error instanceof Error
            ? error.message
            : 'Loom could not load this column source.',
      });
    }
  };
  const contextColumns = useMemo(
    () => new Map(
      (interpretationContext?.columns ?? [])
        .filter((item) => item.outputId === table?.outputId)
        .map((item) => [item.column, item]),
    ),
    [interpretationContext?.columns, table?.outputId],
  );
  const configuredResolutions = useMemo(
    () => new Map(configured.map((column) => [column.column, contextColumns.get(column.column)?.resolution])),
    [configured, contextColumns],
  );
  const configuredCandidateIds = useMemo(
    () => new Set(
      [...configuredResolutions.values()].flatMap((resolution) =>
        resolution?.state === 'READY' ? resolution.capabilityCandidateIds : [],
      ),
    ),
    [configuredResolutions],
  );
  const catalogCandidatesById = useMemo(
    () => new Map((catalog.candidates ?? []).map((candidate) => [candidate.candidateId, candidate])),
    [catalog.candidates],
  );
  const configuredCapabilities = useMemo(
    () => new Map<string, ExplorerBuilderCandidate | undefined>(configured.map((column) => {
      const resolution = configuredResolutions.get(column.column);
      if (resolution?.state !== 'READY') return [column.column, undefined];
      const matches = resolution.capabilityCandidateIds
        .map((candidateId) => catalogCandidatesById.get(candidateId))
        .filter((candidate): candidate is ExplorerBuilderCandidate => candidate !== undefined);
      return [column.column, matches.length === 1 ? matches[0] : undefined];
    })),
    [catalogCandidatesById, configured, configuredResolutions],
  );
  const available = useMemo(
    () =>
      showAvailable
        ? (catalog.candidates ?? []).filter(
            (candidate) =>
              candidate.nodeId === occurrence?.nodeId &&
              isTabularCandidate(candidate) &&
              !configuredCandidateIds.has(candidate.candidateId),
          )
        : [],
    [catalog.candidates, configuredCandidateIds, occurrence?.nodeId, showAvailable],
  );
  const normalizedQuery = query.trim().toLowerCase();
  const rows = useMemo(
    () =>
      [
        ...configured.map((column) => ({
          kind: 'configured' as const,
          column,
        })),
        ...available.map((candidate) => ({
          kind: 'available' as const,
          candidate,
        })),
      ]
        .filter((row) => {
          if (!normalizedQuery) return true;
          const value =
            row.kind === 'configured'
              ? `${row.column.label} ${row.column.column} ${sourceSummary(row.column)}`
              : `${row.candidate.label} ${row.candidate.fieldPath} ${row.candidate.logicalType}`;
          return value.toLowerCase().includes(normalizedQuery);
        })
        .sort((left, right) => {
          const leftLabel =
            left.kind === 'configured'
              ? configuredCapabilities.get(left.column.column)?.label ?? sourceSummary(left.column)
              : left.candidate.label;
          const rightLabel =
            right.kind === 'configured'
              ? configuredCapabilities.get(right.column.column)?.label ?? sourceSummary(right.column)
              : right.candidate.label;
          return leftLabel.localeCompare(rightLabel);
        }),
    [available, configured, configuredCapabilities, normalizedQuery],
  );
  const rowRange = virtualRange({
    count: rows.length,
    offset: viewport.scrollTop,
    viewport: viewport.height,
    itemSize: CANDIDATE_ROW_HEIGHT,
    overscan: 3,
  });
  const addCandidate = (
    candidate: ExplorerBuilderCandidate,
    displayName: string,
    initialPresentation: InitialPresentation,
  ) => {
    if (disabled) return;
    onAdd(candidate, displayName, initialPresentation);
  };
  const allTableColumnsSelected =
    available.length === 0 &&
    configured.length > 0 &&
    configured.every(
      (column) => column.table?.visible ?? Boolean(column.table),
    );
  const visibleConfigured = (table?.document.columns ?? [])
    .filter((column) => column.table?.visible ?? Boolean(column.table))
    .map((column, index) => ({ column, index }))
    .sort(
      (left, right) =>
        (left.column.table?.order ?? Number.MAX_SAFE_INTEGER) -
          (right.column.table?.order ?? Number.MAX_SAFE_INTEGER) ||
        left.index - right.index,
    )
    .map(({ column }) => column);
  const lastVisibleColumn = visibleConfigured.at(-1)?.column;
  const moveColumnToEnd = (column: ExplorerBuilderColumn) => {
    if (visibleConfigured.every((item) => item.table?.order !== undefined)) {
      const maximumOrder = Math.max(
        -1,
        ...visibleConfigured.map((item) => item.table?.order ?? -1),
      );
      onChange({
        ...column,
        table: { ...(column.table ?? {}), visible: true, order: maximumOrder + 1 },
      });
      return;
    }
    const updates = visibleConfigured
      .filter((item) => item.column !== column.column)
      .concat(column)
      .flatMap((item, order) =>
        item.table?.order === order
          ? []
          : [{ ...item, table: { ...(item.table ?? {}), visible: true, order } }],
      );
    if (onColumnsChange) onColumnsChange(updates);
    else updates.forEach(onChange);
  };
  const toggleAllTableColumns = () => {
    if (disabled) return;
    if (allTableColumnsSelected) {
      configured.forEach((column) =>
        onChange({
          ...column,
          table: {
            ...(column.table ?? {}),
            visible: false,
          },
        }),
      );
      return;
    }
    configured
      .filter((column) => !(column.table?.visible ?? Boolean(column.table)))
      .forEach((column, order) =>
        onChange({
          ...column,
          table: {
            ...(column.table ?? {}),
            visible: true,
            order: column.table?.order ?? order,
          },
        }),
      );
    if (available.length > 0) onAddAll(available);
  };

  return (
    <aside className="flex h-[min(70dvh,52rem)] min-h-[43rem] min-w-0 flex-col overflow-hidden bg-slate-100/40 p-3">
      <div className="flex min-w-0 items-start gap-3">
        <div className="min-w-0 flex-1">
          <h2 className="text-base font-semibold text-slate-900">
            {showAvailable
              ? `${titleForResource(resourceType)} columns`
              : 'Your columns'}
          </h2>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {occurrenceId !== 'base' && onAddSource ? (
            <RelatedFeatureCreator
              resourceLabel={titleForResource(resourceType)}
              disabled={disabled}
              onAdd={onAddSource}
            />
          ) : null}
          <button
            type="button"
            disabled={
              disabled || (configured.length === 0 && available.length === 0)
            }
            aria-pressed={allTableColumnsSelected}
            onClick={toggleAllTableColumns}
            className="rounded border border-blue-300 bg-blue-50 px-2.5 py-1 text-[11px] font-semibold text-blue-800 hover:bg-blue-100 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {allTableColumnsSelected
              ? 'Deselect all table columns'
              : 'Select all table columns'}
          </button>
          <span className="rounded bg-slate-100 px-2 py-1 text-[11px] text-slate-600">
            {showAvailable
              ? `${configured.length} configured · ${available.length} available`
              : `${configured.length} configured`}
          </span>
        </div>
      </div>
      {!occurrence ? (
        <p className="mt-4 rounded border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900">
          Select a resource in the configured traversal to inspect its columns.
        </p>
      ) : (
        <>
          <input
            aria-label="Search columns"
            className="mt-2 rounded border border-slate-300 px-2.5 py-1.5 text-sm outline-blue-500"
            value={query}
            onChange={(event) => setQuery(event.currentTarget.value)}
            placeholder={
              showAvailable
                ? 'Search labels, column names, or field paths'
                : 'Search your configured columns'
            }
          />
          {inspectedColumn && inspection?.loading ? (
            <p className="mt-2 rounded-lg border border-blue-200 bg-blue-50 p-3 text-xs text-blue-900" role="status">
              Loading the saved route and exact source…
            </p>
          ) : null}
          {inspectedColumn && inspection?.error ? (
            <div className="mt-2 rounded-lg border border-red-200 bg-red-50 p-3 text-xs text-red-900" role="alert">
              <p>{inspection.error}</p>
              <button
                type="button"
                className="mt-2 font-semibold underline"
                onClick={() => setInspection(undefined)}
              >
                Close
              </button>
            </div>
          ) : null}
          {inspectedColumn && inspection?.descriptor ? (
            <ColumnSourceInspector
              column={inspectedColumn}
              descriptor={inspection.descriptor}
              onClose={() => {
                inspectionGeneration.current += 1;
                setInspection(undefined);
              }}
              onEditInGraph={
                onEditInGraph
                  ? () => onEditInGraph(inspectedColumn)
                  : undefined
              }
            />
          ) : null}
          <div className="mt-2 grid grid-cols-[minmax(0,1fr)_3.5rem_3.5rem_3.5rem_2rem_2rem] gap-2 border-b border-slate-200 bg-slate-50/70 px-2 py-1 text-center text-[10px] font-semibold uppercase tracking-wide text-slate-500">
            <span className="text-left">Display name / source</span>
            <span>Table</span>
            <span>Filter</span>
            <span>Chart</span>
            <span>Order</span>
            <span />
          </div>
          <div
            ref={candidateScrollRef}
            className="min-h-0 flex-1 overflow-y-auto bg-white/80"
          >
            {rows.length ? (
              <div
                className="relative"
                style={{ height: rows.length * CANDIDATE_ROW_HEIGHT }}
              >
                {rows.slice(rowRange.start, rowRange.end).map((row, visibleIndex) => {
                  const order = rowRange.start + visibleIndex;
                  const configuredOccurrence =
                    row.kind === 'configured'
                      ? occurrences.find(
                          ({ id }) => id === row.column.occurrenceId,
                        )
                      : undefined;
                  const configuredResourceType = configuredOccurrence
                    ? catalog.nodes.find(
                        ({ nodeId }) => nodeId === configuredOccurrence.nodeId,
                      )?.resourceType
                    : undefined;
                  return (
                    <div
                      key={row.kind === 'configured' ? `configured:${row.column.column}` : `available:${row.candidate.candidateId}`}
                      className={`absolute inset-x-0 ${row.kind === 'configured' && row.column.column === focusColumn ? 'rounded border-2 border-blue-500 bg-blue-50' : ''}`}
                      data-feature-focus={row.kind === 'configured' && row.column.column === focusColumn ? 'true' : undefined}
                      style={{ top: order * CANDIDATE_ROW_HEIGHT, height: CANDIDATE_ROW_HEIGHT }}
                    >
                      {row.kind === 'configured' ? (
                        <ConfiguredColumnRow
                          column={row.column}
                          order={row.column.table?.order ?? order}
                          disabled={disabled}
                          filterable={
                            configuredCapabilities.get(row.column.column)
                              ?.filterable ?? true
                          }
                          chartable={
                            configuredCapabilities.get(row.column.column)
                              ?.chartable ?? true
                          }
                          candidate={configuredCapabilities.get(row.column.column)}
                          resolution={configuredResolutions.get(row.column.column)}
                          related={row.column.occurrenceId !== 'base'}
                          rowContext={table?.document.rows.kind}
                          candidates={(catalog.candidates ?? []).filter(
                            (candidateOption) =>
                              candidateOption.nodeId ===
                              configuredOccurrence?.nodeId,
                          )}
                          resourceLabel={titleForResource(
                            configuredResourceType ?? resourceType,
                          )}
                          onChange={onChange}
                          onSourceChange={onSourceChange}
                          onContributorChange={onContributorChange}
                          onTransformationChange={onTransformationChange}
                          moveToEndDisabled={
                            !(row.column.table?.visible ?? Boolean(row.column.table)) ||
                            row.column.column === lastVisibleColumn
                          }
                          onMoveToEnd={() => moveColumnToEnd(row.column)}
                          onRemove={() => onRemove(row.column.column)}
                          onInspect={() =>
                            void inspectColumn(row.column)
                          }
                        />
                      ) : (
                        <AvailableColumnRow
                          candidate={row.candidate}
                          displayName={availableDisplayNames[row.candidate.candidateId] ?? row.candidate.label}
                          disabled={disabled}
                          onDisplayNameChange={(value) =>
                            setAvailableDisplayNames((current) => ({
                              ...current,
                              [row.candidate.candidateId]: value,
                            }))
                          }
                          onAdd={(displayName, initialPresentation) =>
                            addCandidate(
                              row.candidate,
                              displayName,
                              initialPresentation,
                            )
                          }
                        />
                      )}
                    </div>
                  );
                })}
              </div>
            ) : (
              <p className="p-4 text-sm text-slate-500">
                {loadingCandidates
                  ? 'Loading available dataset columns…'
                  : normalizedQuery
                    ? 'No columns match this search.'
                    : 'No configured or available columns were found.'}
              </p>
            )}
          </div>
        </>
      )}
    </aside>
  );
};

export const columnFromCandidate = (
  candidate: ExplorerBuilderCandidate,
  occurrenceId: string,
  existing: ReadonlyArray<ExplorerBuilderColumn>,
  displayName = candidate.label,
  resourceType = '',
): ExplorerBuilderColumn => {
  const names = new Set(existing.map((column) => column.column));
  const order =
    Math.max(-1, ...existing.map((column) => column.table?.order ?? -1)) + 1;
  return {
    column: candidateColumnName(candidate, occurrenceId, resourceType, names),
    label: displayName.trim() || candidate.label,
    logicalType: candidate.logicalType,
    occurrenceId,
    source: {
      kind: 'field',
      field: {
        path: candidate.fieldPath,
        projectionMode: candidate.defaultProjectionMode,
      },
    },
    table: { visible: true, order },
  };
};
