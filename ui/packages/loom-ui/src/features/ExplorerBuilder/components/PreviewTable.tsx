import React, { useRef, useState } from 'react';
import { useEscapeToClose } from '../hooks/useEscapeToClose';
import { useBrowserSubscription } from '../hooks/useBrowserSubscription';
import type {
  ExplorerBuilderColumn,
  ExplorerBuilderEmission,
  ExplorerBuilderPreviewResult,
  ExplorerBuilderPreviewRowSource,
  ExplorerBuilderRowLineageResponse,
  ConstructionStageColumn,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import { effectiveOutputAvailability } from '../constructionWorkspace/outputEligibility';
import { displayValue, losslessText } from '../../../valueDisplay';
import { resultUnitTitle } from '../../../resultUnitDisplay';
import { BoundedCache, useVirtualViewport, virtualRange } from './virtualization';

const PREVIEW_ROW_HEIGHT = 44;
const PREVIEW_HEADER_HEIGHT = 42;
const PREVIEW_COLUMN_WIDTH = 180;
const PREVIEW_ROW_GUTTER_WIDTH = 64;
const PREVIEW_HEADER_CONTROLS_WIDTH = 192;

type OwnerRecordInspectorState = {
  readonly columnLabel: string;
  readonly value: unknown;
};

type RowLineageState = {
  readonly rowId: string;
  readonly contributors: NonNullable<ExplorerBuilderRowLineageResponse['contributors']>;
  readonly nextOffset?: number;
  readonly loading: boolean;
  readonly status?: ExplorerBuilderRowLineageResponse['status'];
  readonly reasonCode?: string;
  readonly error?: string;
};

export type PreviewTablePresentationChange =
  | { readonly kind: 'AUTHORED_COLUMN'; readonly column: ExplorerBuilderColumn }
  | {
      readonly kind: 'CONSTRUCTION_OUTPUT';
      readonly stepId: string;
      readonly column: ConstructionStageColumn;
    };

export const sortByPresentationOrder = <T,>(
  values: ReadonlyArray<T>,
  orderOf: (value: T, index: number) => number,
): T[] => values
  .map((value, index) => ({ value, index, order: orderOf(value, index) }))
  .sort((left, right) => left.order - right.order || left.index - right.index)
  .map(({ value }) => value);

const isStructuredRecord = (value: unknown): value is object =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const ownerRecordEntries = (value: unknown): ReadonlyArray<object> =>
  Array.isArray(value) ? value.filter(isStructuredRecord) : [];

const recordField = (record: object, key: string): unknown =>
  Reflect.get(record, key);

const formatOwnerRecordCodings = (value: unknown): string => {
  if (!Array.isArray(value)) return formatPreviewCell(value);
  const labels = value.map((coding) => {
    if (!isStructuredRecord(coding)) return formatPreviewCell(coding);
    const parts = ['system', 'version', 'code', 'display']
      .map((key) => recordField(coding, key))
      .filter((part): part is string => typeof part === 'string' && part.trim() !== '')
      .map((part) => part.trim());
    return parts.length > 0 ? parts.join(' · ') : formatPreviewCell(coding);
  });
  return labels.length > 0 ? labels.join('; ') : formatPreviewCell(value);
};

export const formatPreviewCell = (value: unknown, depth = 0): string =>
  displayValue(value, { unknownRecord: 'parts', maxDepth: 3 }, depth);

export const previewCellTitle = (value: unknown): string => {
  return losslessText(value);
};

export const PreviewTable = ({
  preview,
  previewErrorMessage,
  table,
  limit,
  onLimitChange,
  onRowLineage,
  disabled = false,
}: {
  readonly preview?: ExplorerBuilderPreviewResult;
  readonly previewErrorMessage?: string;
  readonly table?: DraftTable;
  readonly limit: number;
  readonly onLimitChange: (value: 25 | 50 | 100 | 500 | 1000) => void;
  readonly disabled?: boolean;
  readonly onRowLineage?: (
    rowId: string,
    offset: number,
    signal: AbortSignal,
  ) => Promise<ExplorerBuilderRowLineageResponse>;
}) => {
  const [ownerRecordInspector, setOwnerRecordInspector] =
    useState<OwnerRecordInspectorState>();
  const [inspectedRow, setInspectedRow] = useState<{ number: number; identity: string; source?: ExplorerBuilderPreviewRowSource }>();
  const [rowLineage, setRowLineage] = useState<RowLineageState>();
  const lineageAbortRef = useRef<AbortController | undefined>(undefined);
  useBrowserSubscription((_onStoreChange) => () => lineageAbortRef.current?.abort(), [preview?.receiptId, preview?.outputId]);
  const loadRowLineage = (rowId: string, offset: number) => {
    if (!onRowLineage) return;
    lineageAbortRef.current?.abort();
    const controller = new AbortController();
    lineageAbortRef.current = controller;
    setRowLineage((previous) => ({
      rowId,
      contributors: offset > 0 && previous?.rowId === rowId ? previous.contributors : [],
      loading: true,
    }));
    void onRowLineage(rowId, offset, controller.signal).then((result) => {
      if (controller.signal.aborted) return;
      setRowLineage((previous) => ({
        rowId,
        contributors: [
          ...(offset > 0 && previous?.rowId === rowId ? previous.contributors : []),
          ...(result.contributors ?? []),
        ],
        nextOffset: result.hasMore ? result.nextOffset : undefined,
        loading: false,
        status: result.status,
        reasonCode: result.reasonCode,
      }));
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setRowLineage((previous) => ({
        rowId,
        contributors: offset > 0 && previous?.rowId === rowId ? previous.contributors : [],
        loading: false,
        error: error instanceof Error ? error.message : 'Unable to load source records.',
      }));
    });
  };
  const closeInspectedRow = () => {
    lineageAbortRef.current?.abort();
    setInspectedRow(undefined);
    setRowLineage(undefined);
  };
  useEscapeToClose(Boolean(inspectedRow), closeInspectedRow);
  const { viewport, ref: previewScrollRef } =
    useVirtualViewport<HTMLDivElement>();
  const formattingCacheRef = React.useRef<BoundedCache<string> | null>(null);
  if (!formattingCacheRef.current) formattingCacheRef.current = new BoundedCache(5000);
  const formattingCache = formattingCacheRef.current;
  const previewIdentityRef = React.useRef<ExplorerBuilderPreviewResult | undefined>(undefined);
  if (previewIdentityRef.current !== preview) {
    formattingCache.clear();
    previewIdentityRef.current = preview;
  }
  const authoredByColumn = new Map(
    table?.document.columns.map((column) => [column.column, column]) ?? [],
  );
  const authoredConstructionStep = table?.document.construction?.steps.at(-1);
  const outputAvailability = table
    ? effectiveOutputAvailability(table.document)
    : { hasAnyOutputColumn: false, hasVisibleOutputColumn: false };
  const rowDefinition = table?.document.rows;
  const explicitGroups = rowDefinition?.kind === 'GROUPS' && rowDefinition.groups.source.kind === 'EXPLICIT'
    ? rowDefinition.groups
    : undefined;
  const cohortTerminal = Boolean(explicitGroups && (
    !authoredConstructionStep
      ? (table?.document.construction?.steps.length ?? 0) === 0
      : explicitGroups.afterStepId === authoredConstructionStep.id
  ));
  const constructionStep = cohortTerminal ? undefined : authoredConstructionStep;
  // A row-value output represents the grouped value even when it reuses the
  // authored source's physical name. Keep its stable output ID as the owner
  // of presentation changes while leaving ordinary source passthroughs alone.
  const rowValueOutputIds = new Set(
    (constructionStep?.rowValues ?? []).map((value) => value.outputColumnId),
  );
  const constructionOutputs = constructionStep?.outputs.filter(
    (output) => !authoredByColumn.has(output.name) || rowValueOutputIds.has(output.id),
  ) ?? [];
  const constructionOutputByName = new Map(
    constructionOutputs.map((output) => [output.name, output]),
  );
  const constructionOrderByName = new Map(
    constructionStep?.outputs.map((output, index) => [
      output.name,
      output.table?.order ?? index,
    ] as const) ?? [],
  );
  const cohortOrderByName = new Map<string, number>(
    explicitGroups
      ? [['group_label', 0], ['group_ordinal', 1], ['members', 2]]
      : [],
  );
  (explicitGroups?.rowValues ?? []).forEach((value, index) => {
      const column = table?.document.columns.find((candidate) => candidate.columnId === value.columnId);
      if (!column) return;
      cohortOrderByName.set(column.column, 3 + index);
  });
  const authoredColumnsFor = (column: Pick<ExplorerBuilderEmission, 'authoredColumns' | 'column'>) => {
    const names = column.authoredColumns ?? [column.column];
    return names.flatMap((name) => {
      const authored = authoredByColumn.get(name);
      return authored ? [authored] : [];
    });
  };
  const effectiveConstructionOutputVisibility = (
    columnName: string,
    emission = preview?.columns.find((column) => column.column === columnName),
  ) => {
    const constructionOutput = constructionOutputByName.get(columnName);
    if (constructionOutput?.table?.visible !== undefined) {
      return constructionOutput.table.visible;
    }
    const authoredColumn = authoredByColumn.get(columnName);
    const authoredColumns = emission
      ? authoredColumnsFor(emission)
      : authoredColumn ? [authoredColumn] : [];
    return authoredColumns.length === 0 || authoredColumns.some(
      (authored) => authored.table?.visible ?? Boolean(authored.table),
    );
  };
  const authoredColumnFor = (column: ExplorerBuilderEmission) =>
    authoredColumnsFor(column)[0];
  const orderForEmission = (column: ExplorerBuilderEmission): number => {
    const outputOrder = constructionOrderByName.get(column.column);
    if (outputOrder !== undefined) return outputOrder;
    return Math.min(
      Number.MAX_SAFE_INTEGER,
      ...authoredColumnsFor(column).map(
        (authored) => authored.table?.order ?? Number.MAX_SAFE_INTEGER,
      ),
      cohortOrderByName.get(column.column) ?? Number.MAX_SAFE_INTEGER,
    );
  };
  const unorderedColumns: ExplorerBuilderEmission[] = (preview?.columns ?? [])
    .map((column) => {
      const authored = (column.authoredColumns ?? [column.column])
        .map((name) => authoredByColumn.get(name))
        .find((value) => value !== undefined);
      return {
        ...column,
        label: constructionOutputByName.get(column.column)?.label ?? authoredByColumn.get(column.column)?.label ?? column.label,
        outputId: preview?.outputId ?? table?.outputId ?? '',
        candidateId: column.column,
        occurrenceId: authored?.occurrenceId ?? 'base',
        projectionMode:
          authored?.source.kind === 'field'
            ? (authored.source.field.projectionMode ?? 'FIRST')
            : authored && 'lookup' in authored.source
              ? (authored.source.lookup.projectionMode ?? 'FIRST')
              : 'VALUE',
        emissionId: column.column,
        publicColumn: column.column,
      };
    });
  const hasCohortColumns = cohortTerminal && unorderedColumns.some(
    (column) => cohortOrderByName.has(column.column),
  );
  const orderedColumns = sortByPresentationOrder(
    unorderedColumns,
    (column) => {
      const leftCohortOrder = cohortTerminal ? cohortOrderByName.get(column.column) : undefined;
      return hasCohortColumns
        ? leftCohortOrder ?? Number.MAX_SAFE_INTEGER
        : orderForEmission(column);
    },
  );
  const columns = orderedColumns.filter((column) => {
    const constructionOutput = constructionOutputByName.get(column.column);
    if (constructionOutput) {
      return effectiveConstructionOutputVisibility(column.column, column);
    }
    const authoredColumns = authoredColumnsFor(column);
    if (authoredColumns.length === 0) {
      return true;
    }
    return authoredColumns.some(
      (authored) => authored.table?.visible ?? Boolean(authored.table),
    );
  });
  const rows = preview?.rows ?? [];
  const hasEmptyPreview = preview !== undefined && rows.length === 0;
  const previewContentHeight =
    PREVIEW_HEADER_HEIGHT +
    rows.length * PREVIEW_ROW_HEIGHT +
    (hasEmptyPreview ? 48 : 0);
  const previewTableWidth = Math.max(
    viewport.width,
    PREVIEW_ROW_GUTTER_WIDTH +
      columns.length * PREVIEW_COLUMN_WIDTH +
      PREVIEW_HEADER_CONTROLS_WIDTH,
  );
  const headerColumnViewportWidth = Math.max(
    0,
    Math.min(
      previewTableWidth,
      viewport.scrollLeft + viewport.width - PREVIEW_HEADER_CONTROLS_WIDTH,
    ),
  );
  const columnRange = virtualRange({
    count: columns.length,
    offset: Math.max(0, viewport.scrollLeft - PREVIEW_ROW_GUTTER_WIDTH),
    viewport: Math.max(
      0,
      viewport.width - PREVIEW_HEADER_CONTROLS_WIDTH -
        Math.max(0, PREVIEW_ROW_GUTTER_WIDTH - viewport.scrollLeft),
    ),
    itemSize: PREVIEW_COLUMN_WIDTH,
    overscan: 2,
  });
  const rowRange = virtualRange({
    count: rows.length,
    offset: Math.max(0, viewport.scrollTop - PREVIEW_HEADER_HEIGHT),
    viewport: Math.max(0, viewport.height - PREVIEW_HEADER_HEIGHT),
    itemSize: PREVIEW_ROW_HEIGHT,
    overscan: 3,
  });
  const visibleColumns = columns.slice(columnRange.start, columnRange.end);
  const formattedCell = (rowIndex: number, column: ExplorerBuilderEmission, rawValue: unknown): string =>
    formattingCache.getOrSet(`display:${rowIndex}:${column.emissionId}`, () => formatPreviewCell(rawValue));
  const titledCell = (rowIndex: number, column: ExplorerBuilderEmission, rawValue: unknown): string =>
    formattingCache.getOrSet(`title:${rowIndex}:${column.emissionId}`, () => previewCellTitle(rawValue));
  const tableControls = (
    <div
      data-testid="preview-table-header-controls"
      className="sticky right-0 top-0 z-20 ml-auto flex h-[42px] shrink-0 items-center justify-end gap-2 bg-slate-50 px-2 text-slate-700"
      style={{ width: PREVIEW_HEADER_CONTROLS_WIDTH }}
    >
      <label className="flex items-center gap-1 text-xs font-medium text-slate-600">
        Show
        <select
          aria-label="Preview row limit"
          className="min-w-16 rounded-md border border-slate-300 bg-white py-1.5 pl-2 pr-6 text-right font-normal tabular-nums text-slate-800"
          value={limit}
          onChange={(event) =>
            onLimitChange(
              Number(event.currentTarget.value) as 25 | 50 | 100 | 500 | 1000,
            )
          }
        >
          <option value={25}>25</option>
          <option value={50}>50</option>
          <option value={100}>100</option>
          <option value={500}>500</option>
          <option value={1000}>1,000</option>
        </select>
        rows
      </label>
    </div>
  );
  return (
    <section className="construction-preview-surface overflow-hidden bg-white">
      {preview && !previewErrorMessage ? null : (
        <div className="flex justify-end border-b border-slate-200 bg-white">
          {tableControls}
        </div>
      )}
      <div
        ref={previewScrollRef}
        data-testid="preview-table-scroll"
        className="max-h-[min(65dvh,40rem)] max-w-full overflow-auto overscroll-contain"
      >
        {previewErrorMessage ? (
          <p role="status" className="px-4 py-8 text-sm text-red-800">
            Preview did not complete for this draft: {previewErrorMessage}. Correct the Builder issue above; Preview will run again after the draft changes.
          </p>
        ) : !preview ? (
          <p className="px-4 py-8 text-sm text-slate-500">
            {!table?.document.rootResourceType
              ? 'Choose starting records to preview this table.'
              : !outputAvailability.hasAnyOutputColumn
                ? 'Add a column to see your table.'
                : 'Loading your table…'}
          </p>
        ) : (
          <div
            data-testid="preview-table-content"
            className="relative"
            style={{
              width: previewTableWidth,
              height: previewContentHeight,
            }}
          >
            {tableControls}
            <div
              role="table"
              aria-rowcount={rows.length + 1}
              aria-colcount={columns.length}
              className="absolute left-0 top-0 text-left text-xs"
              style={{
                width: previewTableWidth,
                height: PREVIEW_HEADER_HEIGHT + rows.length * PREVIEW_ROW_HEIGHT,
              }}
            >
            <div
              role="row"
              className="construction-preview-header-row sticky top-0 z-10 bg-slate-50 text-[11px] uppercase tracking-wide text-slate-600"
              style={{ height: PREVIEW_HEADER_HEIGHT }}
            >
              <div
                data-testid="preview-table-header-columns"
                className="absolute inset-y-0 left-0 overflow-hidden"
                style={{ width: headerColumnViewportWidth }}
              >
                <div className="relative h-full" style={{ width: previewTableWidth }}>
                  <div className="absolute left-0 top-0 bg-slate-50 px-2 py-2.5" style={{ width: PREVIEW_ROW_GUTTER_WIDTH, height: PREVIEW_HEADER_HEIGHT }}>
                    Row
                  </div>
                  {visibleColumns.map((column, visibleIndex) => {
                    const columnIndex = columnRange.start + visibleIndex;
                    return (
                      <div
                        role="columnheader"
                        key={column.emissionId}
                        aria-label={column.resultUnit
                          ? `${column.label} (${column.resultUnit.code})`
                          : undefined}
                        className="absolute top-0 overflow-hidden whitespace-nowrap px-4 py-2.5 font-semibold"
                        style={{
                          left: PREVIEW_ROW_GUTTER_WIDTH + columnIndex * PREVIEW_COLUMN_WIDTH,
                          width: PREVIEW_COLUMN_WIDTH,
                          height: PREVIEW_HEADER_HEIGHT,
                        }}
                      >
                        {column.label}
                        {column.resultUnit ? (
                          <span
                            className="font-normal normal-case text-slate-500"
                            title={resultUnitTitle(column.resultUnit)}
                          >
                            {' '}({column.resultUnit.code})
                          </span>
                        ) : null}
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>
            {rows.slice(rowRange.start, rowRange.end).map((row, visibleRowIndex) => {
              const rowIndex = rowRange.start + visibleRowIndex;
              const rawIdentity = row.__loom_row_id;
              const rowIdentity = typeof rawIdentity === 'string'
                ? rawIdentity
                : rawIdentity && typeof rawIdentity === 'object'
                  ? JSON.stringify(rawIdentity)
                  : undefined;
              return (
                <div
                  role="row"
                  key={rowIndex}
                  className="construction-preview-data-row absolute left-0 right-0 bg-white hover:bg-slate-50"
                  style={{
                    top: PREVIEW_HEADER_HEIGHT + rowIndex * PREVIEW_ROW_HEIGHT,
                    height: PREVIEW_ROW_HEIGHT,
                  }}
                >
                  {typeof rowIdentity === 'string' && rowIdentity ? (
                    <button
                      type="button"
                      aria-label={`Inspect row ${rowIndex + 1} identity`}
                      className="absolute left-0 top-0 bg-transparent px-2 py-2.5 text-left font-medium text-blue-700 hover:underline"
                      style={{ width: PREVIEW_ROW_GUTTER_WIDTH, height: PREVIEW_ROW_HEIGHT }}
                      onClick={() => {
                        lineageAbortRef.current?.abort();
                        setRowLineage(undefined);
                        setInspectedRow({
                          number: rowIndex + 1,
                          identity: rowIdentity,
                          source: preview?.rowSources?.[rowIndex],
                        });
                        if (preview?.rowSources?.[rowIndex]?.kind === 'COMPOSITE' && preview.rowLineageCapability.status === 'AVAILABLE') {
                          loadRowLineage(rowIdentity, 0);
                        }
                      }}
                    >
                      {rowIndex + 1}
                    </button>
                  ) : (
                    <span className="absolute left-0 top-0 px-2 py-2.5 text-slate-500" style={{ width: PREVIEW_ROW_GUTTER_WIDTH, height: PREVIEW_ROW_HEIGHT }}>
                      {rowIndex + 1}
                    </span>
                  )}
                  {visibleColumns.map((column, visibleColumnIndex) => {
                    const columnIndex = columnRange.start + visibleColumnIndex;
                    const rawValue = row[column.publicColumn];
                    const arrayValue = Array.isArray(rawValue) ? rawValue : undefined;
                    const isOwnerRecords =
                      authoredColumnFor(column)?.source.kind === 'ownerRecords';
                    const recordCount = ownerRecordEntries(rawValue).length;
                    return (
                      <div
                        role="cell"
                        key={column.emissionId}
                        className="absolute top-0 max-w-56 overflow-hidden px-4 py-2.5 text-slate-700"
                        style={{
                          left: PREVIEW_ROW_GUTTER_WIDTH + columnIndex * PREVIEW_COLUMN_WIDTH,
                          width: PREVIEW_COLUMN_WIDTH,
                          height: PREVIEW_ROW_HEIGHT,
                        }}
                      >
                        {isOwnerRecords ? (
                          <button
                            type="button"
                            aria-label={`Inspect ${column.label} for row ${rowIndex + 1}`}
                            className="flex w-full items-center justify-between gap-2 truncate whitespace-nowrap bg-transparent text-left text-blue-700 hover:text-blue-900"
                            title={titledCell(rowIndex, column, rawValue)}
                            onClick={() => setOwnerRecordInspector({ columnLabel: column.label, value: rawValue })}
                          >
                            <span className="truncate">{recordCount} {recordCount === 1 ? 'record' : 'records'}</span>
                            <span aria-hidden="true" className="text-[10px]">Inspect</span>
                          </button>
                        ) : (
                          <div
                            className={arrayValue
                              ? 'flex min-w-0 items-center gap-1 whitespace-nowrap'
                              : 'truncate whitespace-nowrap'}
                            title={titledCell(rowIndex, column, rawValue)}
                          >
                            {arrayValue?.length === 0 ? (
                              <span
                                data-testid="preview-array-indicator"
                                className="inline-flex shrink-0 rounded bg-slate-100 px-1.5 py-0.5 text-xs leading-4 text-slate-600"
                              >
                                Empty list
                              </span>
                            ) : arrayValue ? (
                              <>
                                <span className="min-w-0 flex-1 truncate">
                                  {formattedCell(rowIndex, column, rawValue)}
                                </span>
                                <span
                                  data-testid="preview-array-indicator"
                                  className="inline-flex shrink-0 rounded bg-slate-100 px-1.5 py-0.5 text-xs leading-4 text-slate-600"
                                >
                                  {arrayValue.length} {arrayValue.length === 1 ? 'value' : 'values'}
                                </span>
                              </>
                            ) : formattedCell(rowIndex, column, rawValue)}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              );
            })}
            </div>
            {hasEmptyPreview ? (
              <p
                role="status"
                data-testid="preview-table-empty-state"
                className="absolute inset-x-0 px-4 py-3 text-sm text-slate-500"
                style={{ top: PREVIEW_HEADER_HEIGHT }}
              >
                No rows in this table.
              </p>
            ) : null}
          </div>
        )}
      </div>
      {inspectedRow ? (
        <div className="fixed inset-0 z-[110] flex items-center justify-center bg-slate-950/45 p-4" role="presentation">
          <section aria-label={`Row ${inspectedRow.number} identity`} aria-modal="true" className="max-h-[calc(100dvh-2rem)] w-full max-w-lg overflow-y-auto rounded-xl bg-white p-5 shadow-2xl" role="dialog">
            <div className="sticky top-0 z-10 flex items-start gap-3 bg-white pb-2">
              <div>
                <h3 className="font-semibold text-slate-950">Row {inspectedRow.number}</h3>
                <p className="mt-1 text-sm text-slate-600">Stable identity for this preview row. Use it to check whether edits preserve the same rows.</p>
              </div>
              <button type="button" className="ml-auto rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700" onClick={closeInspectedRow}>Close</button>
            </div>
            {inspectedRow.source?.kind === 'SINGLE' ? (
              <div className="mt-4 rounded-md border border-slate-200 p-3 text-sm">
                <p className="font-medium text-slate-800">Starting FHIR record</p>
                <p className="mt-1 break-all text-slate-700">{inspectedRow.source.resourceType}/{inspectedRow.source.id}</p>
              </div>
            ) : inspectedRow.source?.kind === 'COMPOSITE' ? (
              <div className="mt-4 text-sm text-slate-700">
                <p className="font-medium text-slate-800">Source records in this row</p>
                {preview?.rowLineageCapability.status === 'AVAILABLE' && onRowLineage ? (
                  <>
                    {rowLineage?.loading && rowLineage.contributors.length === 0 ? <p className="mt-2">Loading source records…</p> : null}
                    {rowLineage?.error ? <p role="alert" className="mt-2 text-red-700">Could not load source records: {rowLineage.error}</p> : null}
                    {rowLineage?.status === 'UNAVAILABLE' || rowLineage?.status === 'INCOMPLETE' ? (
                      <p className="mt-2 text-amber-800">Source records could not be fully listed{rowLineage.reasonCode ? ` (${rowLineage.reasonCode})` : ''}.</p>
                    ) : null}
                    {rowLineage?.status === 'COMPLETE' && rowLineage.contributors.length === 0 ? <p className="mt-2">No source records contributed to this row.</p> : null}
                    {rowLineage?.contributors.length ? (
                      <ul className="mt-2 max-h-64 overflow-y-auto rounded-md border border-slate-200 p-2">
                        {rowLineage.contributors.map((contributor, index) => (
                          <li key={JSON.stringify([
                            preview?.receiptId ?? '',
                            preview?.outputId ?? '',
                            rowLineage.rowId,
                            contributor.resourceType,
                            contributor.resourceId,
                            contributor.occurrenceKey,
                            index,
                          ])} className="break-all py-1">
                            {contributor.resourceType}/{contributor.resourceId}
                          </li>
                        ))}
                      </ul>
                    ) : null}
                    {rowLineage?.nextOffset !== undefined ? (
                      <button type="button" disabled={rowLineage.loading} className="mt-2 rounded-md border border-slate-300 px-3 py-1.5 text-blue-700 disabled:text-slate-400" onClick={() => {
                        const nextOffset = rowLineage.nextOffset;
                        if (nextOffset !== undefined) loadRowLineage(inspectedRow.identity, nextOffset);
                      }}>
                        {rowLineage.loading ? 'Loading…' : 'Show more source records'}
                      </button>
                    ) : null}
                  </>
                ) : (
                  <p className="mt-2 text-slate-600">Source records cannot be listed for this table shape{preview?.rowLineageCapability.status === 'UNAVAILABLE' && preview.rowLineageCapability.operation ? ` after ${preview.rowLineageCapability.operation}` : ''}.</p>
                )}
              </div>
            ) : (
              <p className="mt-4 text-sm text-slate-600">Source record details are unavailable for this row.</p>
            )}
            <details className="mt-4 rounded-md border border-slate-200 p-3 text-sm text-slate-700">
              <summary className="cursor-pointer font-medium">Technical row ID</summary>
              <p className="mt-2 break-all font-mono text-xs">{inspectedRow.identity}</p>
            </details>
          </section>
        </div>
      ) : null}
      {ownerRecordInspector ? (
        <div className="fixed inset-0 z-[110] flex items-center justify-center bg-slate-950/45 p-4" role="presentation">
          <section
            aria-label={`${ownerRecordInspector.columnLabel} record evidence`}
            aria-modal="true"
            className="max-h-[90vh] w-full max-w-3xl overflow-y-auto rounded-xl bg-white shadow-2xl"
            role="dialog"
          >
            <header className="sticky top-0 flex items-center gap-3 border-b border-slate-200 bg-white px-5 py-4">
              <div>
                <h3 className="font-semibold text-slate-950">{ownerRecordInspector.columnLabel}</h3>
                <p className="text-xs text-slate-500">Repeated FHIR records preserved in this cell</p>
              </div>
              <button
                type="button"
                className="ml-auto rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700"
                onClick={() => setOwnerRecordInspector(undefined)}
              >
                Close
              </button>
            </header>
            <div className="space-y-3 p-5">
              {ownerRecordEntries(ownerRecordInspector.value).length === 0 ? (
                <p className="rounded-md bg-slate-50 p-4 text-sm text-slate-600">No matching records were found for this row.</p>
              ) : ownerRecordEntries(ownerRecordInspector.value).map((record, index) => {
                const source = recordField(record, 'source');
                const status = recordField(record, 'status');
                const value = recordField(record, 'value');
                const values = recordField(record, 'values');
                const unit = recordField(record, 'unit');
                const codings = recordField(record, 'codings');
                const choiceArm = recordField(record, 'choiceArm');
                const owner = recordField(record, 'owner');
                return (
                  <article key={index} className="rounded-lg border border-slate-200 p-4">
                    <div className="flex flex-wrap items-center gap-2">
                      <h4 className="font-semibold text-slate-900">Record {index + 1}</h4>
                      <span className="rounded-full bg-slate-100 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-slate-700">
                        {typeof status === 'string' ? status : 'UNKNOWN'}
                      </span>
                    </div>
                    <dl className="mt-3 grid gap-2 text-sm sm:grid-cols-[7rem_1fr]">
                      <dt className="font-medium text-slate-500">Value</dt>
                      <dd className="break-words text-slate-900">{formatPreviewCell(value ?? values)}</dd>
                      <dt className="font-medium text-slate-500">Unit</dt>
                      <dd className="break-words text-slate-900">{formatPreviewCell(unit)}</dd>
                      <dt className="font-medium text-slate-500">Choice arm</dt>
                      <dd className="break-words text-slate-900">{formatPreviewCell(choiceArm)}</dd>
                      <dt className="font-medium text-slate-500">Matching code</dt>
                      <dd className="break-words text-slate-900">{formatOwnerRecordCodings(codings)}</dd>
                      <dt className="font-medium text-slate-500">Source</dt>
                      <dd className="break-all font-mono text-xs text-slate-700">{formatPreviewCell(source)}</dd>
                    </dl>
                    <details className="mt-3">
                      <summary className="cursor-pointer text-xs font-semibold text-blue-700">Raw FHIR owner</summary>
                      <pre className="mt-2 max-h-64 overflow-auto rounded-md bg-slate-950 p-3 text-xs text-slate-100">{losslessText(owner)}</pre>
                    </details>
                  </article>
                );
              })}
            </div>
          </section>
        </div>
      ) : null}
    </section>
  );
};
