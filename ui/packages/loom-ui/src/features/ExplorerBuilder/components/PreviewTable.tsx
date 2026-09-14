import React, { useState } from 'react';
import type {
  ExplorerBuilderColumn,
  ExplorerBuilderEmission,
  ExplorerBuilderPreviewResult,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import { useDismissibleLayer } from './useDismissibleLayer';
import { BoundedCache, useVirtualViewport, virtualRange } from './virtualization';

const PREVIEW_ROW_HEIGHT = 44;
const PREVIEW_HEADER_HEIGHT = 42;
const PREVIEW_COLUMN_WIDTH = 180;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const scalarText = (value: unknown): string | undefined => {
  if (typeof value === 'string') return value.trim() || undefined;
  if (
    typeof value === 'number' ||
    typeof value === 'boolean' ||
    typeof value === 'bigint'
  ) {
    return String(value);
  }
  return undefined;
};

export const formatPreviewCell = (value: unknown, depth = 0): string => {
  if (value === undefined || value === null) return '—';
  const scalar = scalarText(value);
  if (scalar !== undefined) return scalar;
  if (Array.isArray(value)) {
    const items = value
      .map((item) => formatPreviewCell(item, depth + 1))
      .filter((item) => item !== '—');
    return items.length > 0 ? items.join('; ') : '—';
  }
  if (isRecord(value)) {
    // Prefer the human-bearing fields used by common FHIR datatypes.
    for (const key of ['text', 'display', 'value', 'code']) {
      const preferred = scalarText(value[key]);
      if (preferred !== undefined) return preferred;
    }
    if (value.reference !== undefined) {
      return formatPreviewCell(value.reference, depth + 1);
    }
    if (value.coding !== undefined) {
      return formatPreviewCell(value.coding, depth + 1);
    }
    if (depth < 3) {
      const parts = Object.entries(value)
        .map(([key, nested]) => {
          const formatted = formatPreviewCell(nested, depth + 1);
          return formatted === '—' ? undefined : `${key}: ${formatted}`;
        })
        .filter((part): part is string => part !== undefined);
      if (parts.length > 0) return parts.join(' · ');
    }
  }
  return previewCellTitle(value);
};

export const previewCellTitle = (value: unknown): string => {
  if (value === undefined || value === null) return '—';
  const scalar = scalarText(value);
  if (scalar !== undefined) return scalar;
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    return String(value);
  }
};

export const PreviewTable = ({
  preview,
  table,
  limit,
  onLimitChange,
  onColumnChange,
  onColumnsChange,
}: {
  readonly preview?: ExplorerBuilderPreviewResult;
  readonly table?: DraftTable;
  readonly limit: number;
  readonly onLimitChange: (value: 25 | 50 | 100 | 500 | 1000) => void;
  readonly onColumnChange: (column: ExplorerBuilderColumn) => void;
  readonly onColumnsChange: (
    columns: ReadonlyArray<ExplorerBuilderColumn>,
  ) => void;
}) => {
  const [columnsOpen, setColumnsOpen] = useState(false);
  const columnsMenuRef = useDismissibleLayer<HTMLDivElement>(
    columnsOpen,
    setColumnsOpen,
  );
  const [draggedColumn, setDraggedColumn] = useState<string>();
  const [dropIndex, setDropIndex] = useState<number>();
  const draggedColumnRef = React.useRef<string | undefined>(undefined);
  const previewScrollRef = React.useRef<HTMLDivElement>(null);
  const viewport = useVirtualViewport(previewScrollRef);
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
  const authoredColumnsFor = (column: ExplorerBuilderEmission) => {
    const names = column.authoredColumns ?? [column.column];
    return names.flatMap((name) => {
      const authored = authoredByColumn.get(name);
      return authored ? [authored] : [];
    });
  };
  const authoredColumnFor = (column: ExplorerBuilderEmission) =>
    authoredColumnsFor(column)[0];
  const configuredColumns = [...authoredByColumn.values()].sort(
    (left, right) =>
      (left.table?.order ?? Number.MAX_SAFE_INTEGER) -
      (right.table?.order ?? Number.MAX_SAFE_INTEGER),
    );
  const orderedColumns: ExplorerBuilderEmission[] = (preview?.columns ?? [])
    .map((column) => {
      const authored = (column.authoredColumns ?? [column.column])
        .map((name) => authoredByColumn.get(name))
        .find((value) => value !== undefined);
      return {
        ...column,
        outputId: preview?.outputId ?? table?.outputId ?? '',
        candidateId: column.column,
        occurrenceId: authored?.occurrenceId ?? 'base',
        projectionMode:
          authored?.source.kind !== 'aggregate'
            ? (authored?.source.projectionMode ?? 'FIRST')
            : 'FIRST',
        emissionId: column.column,
        publicColumn: column.column,
      };
    })
    .sort(
      (left, right) =>
        Math.min(
          Number.MAX_SAFE_INTEGER,
          ...authoredColumnsFor(left).map(
            (column) => column.table?.order ?? Number.MAX_SAFE_INTEGER,
          ),
        ) -
        Math.min(
          Number.MAX_SAFE_INTEGER,
          ...authoredColumnsFor(right).map(
            (column) => column.table?.order ?? Number.MAX_SAFE_INTEGER,
          ),
        ),
    );
  const columns = orderedColumns.filter((column) => {
    return authoredColumnsFor(column).some(
      (authored) => authored.table?.visible ?? Boolean(authored.table),
    );
  });
  const rows = preview?.rows ?? [];
  const columnRange = virtualRange({
    count: columns.length,
    offset: viewport.scrollLeft,
    viewport: viewport.width,
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
  const resetDrag = () => {
    draggedColumnRef.current = undefined;
    setDraggedColumn(undefined);
    setDropIndex(undefined);
  };
  const reorderColumns = (columnName: string, insertionIndex: number) => {
    const fromIndex = configuredColumns.findIndex(
      (column) => column.column === columnName,
    );
    if (fromIndex < 0) return;
    const reordered = [...configuredColumns];
    const [moved] = reordered.splice(fromIndex, 1);
    const adjustedIndex = Math.max(
      0,
      Math.min(
        reordered.length,
        insertionIndex - (fromIndex < insertionIndex ? 1 : 0),
      ),
    );
    reordered.splice(adjustedIndex, 0, moved);
    const updates = reordered.flatMap((column, order) => {
      return column.table?.order === order
        ? []
        : [
            {
              ...column,
              table: { ...(column.table ?? {}), order },
            },
          ];
    });
    if (updates.length > 0) onColumnsChange(updates);
  };
  return (
    <section className="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
      <div className="flex flex-wrap items-center gap-2 border-b border-slate-200 bg-slate-50/80 px-4 py-2">
        <h3 className="text-sm font-semibold text-slate-900">
          Preview and configure
        </h3>
        <div ref={columnsMenuRef} className="relative ml-auto">
          <button
            type="button"
            className="rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-xs font-medium text-slate-700 shadow-sm hover:bg-slate-50"
            onClick={() => setColumnsOpen((value) => !value)}
          >
            Columns
          </button>
          {columnsOpen && (
            <div className="absolute right-0 z-20 mt-1 w-[min(32rem,calc(100vw-2rem))] rounded-lg border border-slate-200 bg-white p-2 shadow-lg">
              <p className="border-b border-slate-100 px-2 pb-2 text-[11px] text-slate-500">
                Check columns to show them. Drag rows to change table order.
              </p>
              <div
                role="list"
                aria-label="Table columns"
                className="max-h-[min(60dvh,28rem)] overflow-y-auto overflow-x-hidden py-1 pr-1"
              >
                {configuredColumns.map((column, index) => {
                  const visible = column.table?.visible ?? Boolean(column.table);
                  return (
                    <div
                      key={column.column}
                      role="listitem"
                      onDragOver={(event) => {
                        if (!draggedColumnRef.current) return;
                        event.preventDefault();
                        const bounds =
                          event.currentTarget.getBoundingClientRect();
                        setDropIndex(
                          index +
                            (event.clientY > bounds.top + bounds.height / 2
                              ? 1
                              : 0),
                        );
                      }}
                      onDrop={(event) => {
                        event.preventDefault();
                        const columnName =
                          draggedColumnRef.current ??
                          event.dataTransfer.getData('text/plain');
                        if (columnName) {
                          const bounds =
                            event.currentTarget.getBoundingClientRect();
                          const insertionIndex =
                            index +
                            (event.clientY > bounds.top + bounds.height / 2
                              ? 1
                              : 0);
                          reorderColumns(columnName, insertionIndex);
                        }
                        resetDrag();
                      }}
                      className={`relative flex items-center gap-2 rounded px-2 py-2 text-xs hover:bg-slate-50 ${draggedColumn === column.column ? 'opacity-50' : ''}`}
                    >
                      {draggedColumn && dropIndex === index && (
                        <span className="pointer-events-none absolute inset-x-1 -top-px h-0.5 rounded bg-blue-500" />
                      )}
                      <span
                        aria-label={`Drag ${column.label}`}
                        draggable
                        onDragStart={(event) => {
                          event.dataTransfer.effectAllowed = 'move';
                          event.dataTransfer.setData(
                            'text/plain',
                            column.column,
                          );
                          draggedColumnRef.current = column.column;
                          setDraggedColumn(column.column);
                          setDropIndex(index);
                        }}
                        onDragEnd={resetDrag}
                        className="cursor-grab select-none text-base leading-none text-slate-400 active:cursor-grabbing"
                      >
                        ⋮⋮
                      </span>
                      <label className="flex min-w-0 flex-1 items-start gap-2 leading-5">
                        <input
                          type="checkbox"
                          className="mt-0.5 shrink-0"
                          checked={visible}
                          onChange={(event) =>
                            onColumnChange({
                              ...column,
                              table: {
                                ...(column.table ?? {}),
                                visible: event.currentTarget.checked,
                                order: column.table?.order ?? index,
                              },
                            })
                          }
                        />
                        <span className="min-w-0 break-words">
                          {column.label}
                        </span>
                      </label>
                    </div>
                  );
                })}
                {draggedColumn && dropIndex === configuredColumns.length && (
                  <div className="mx-1 h-0.5 rounded bg-blue-500" />
                )}
              </div>
            </div>
          )}
        </div>
        <label className="text-xs font-medium text-slate-600">
          Rows{' '}
          <select
            aria-label="Preview row limit"
            className="min-w-20 rounded-md border border-slate-300 bg-white py-1.5 pl-2 pr-8 text-right font-normal tabular-nums text-slate-800"
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
        </label>
      </div>
      <div
        ref={previewScrollRef}
        data-testid="preview-table-scroll"
        className="max-h-[min(65dvh,40rem)] max-w-full overflow-auto overscroll-contain"
      >
        {!preview ? (
          <p className="px-4 py-8 text-sm text-slate-500">
            Choose a row resource and at least one visible column, then preview.
          </p>
        ) : (
          <div
            role="table"
            aria-rowcount={rows.length + 1}
            aria-colcount={columns.length}
            className="relative text-left text-xs"
            style={{
              width: Math.max(viewport.width, columns.length * PREVIEW_COLUMN_WIDTH),
              height: PREVIEW_HEADER_HEIGHT + rows.length * PREVIEW_ROW_HEIGHT,
            }}
          >
            <div
              role="row"
              className="sticky top-0 z-10 bg-slate-100 text-[11px] uppercase tracking-wide text-slate-600"
              style={{ height: PREVIEW_HEADER_HEIGHT }}
            >
              {visibleColumns.map((column, visibleIndex) => {
                const columnIndex = columnRange.start + visibleIndex;
                return (
                  <div
                    role="columnheader"
                    key={column.emissionId}
                    className="absolute top-0 overflow-hidden whitespace-nowrap border-b border-slate-200 px-4 py-2.5 font-semibold"
                    style={{
                      left: columnIndex * PREVIEW_COLUMN_WIDTH,
                      width: PREVIEW_COLUMN_WIDTH,
                      height: PREVIEW_HEADER_HEIGHT,
                    }}
                  >
                    {column.label}
                  </div>
                );
              })}
            </div>
            {rows.slice(rowRange.start, rowRange.end).map((row, visibleRowIndex) => {
              const rowIndex = rowRange.start + visibleRowIndex;
              return (
                <div
                  role="row"
                  key={rowIndex}
                  className="absolute left-0 right-0 odd:bg-white even:bg-slate-50/70 hover:bg-blue-50/60"
                  style={{
                    top: PREVIEW_HEADER_HEIGHT + rowIndex * PREVIEW_ROW_HEIGHT,
                    height: PREVIEW_ROW_HEIGHT,
                  }}
                >
                  {visibleColumns.map((column, visibleColumnIndex) => {
                    const columnIndex = columnRange.start + visibleColumnIndex;
                    const rawValue = row[column.publicColumn];
                    return (
                      <div
                        role="cell"
                        key={column.emissionId}
                        className="absolute top-0 max-w-56 overflow-hidden border-b border-slate-100 px-4 py-2.5 text-slate-700"
                        style={{
                          left: columnIndex * PREVIEW_COLUMN_WIDTH,
                          width: PREVIEW_COLUMN_WIDTH,
                          height: PREVIEW_ROW_HEIGHT,
                        }}
                      >
                        <div
                          className="truncate whitespace-nowrap"
                          title={titledCell(rowIndex, column, rawValue)}
                        >
                          {formattedCell(rowIndex, column, rawValue)}
                        </div>
                      </div>
                    );
                  })}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </section>
  );
};
