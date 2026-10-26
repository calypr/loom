import React from 'react';
import type { ExplorerBuilderPreviewResult } from '../../../types';
import { formatPreviewCell } from '../components/PreviewTable';

const hasValue = (value: unknown): boolean => {
  if (value === null || value === undefined) return false;
  if (typeof value === 'string') return value.trim() !== '';
  if (Array.isArray(value)) return value.length > 0;
  return true;
};

export interface PreviewColumnCoverage {
  readonly columnId: string;
  readonly label: string;
  readonly populatedRows: number;
  readonly displayedRows: number;
  readonly fullOutput: boolean;
  readonly examples: ReadonlyArray<string>;
}

export type PreviewResultMultiplicity =
  | { readonly kind: 'EXACT_COUNT' | 'VALUE_LIST'; readonly zero: number; readonly one: number; readonly many: number }
  | { readonly kind: 'PRESENCE'; readonly zero: number; readonly oneOrMore: number };

export const previewResultMultiplicity = (
  preview: ExplorerBuilderPreviewResult,
  columnId: string,
  form: 'COUNT' | 'PRESENCE' | 'ALL',
): PreviewResultMultiplicity | undefined => {
  if (preview.rows === null) return undefined;
  let zero = 0;
  let one = 0;
  let many = 0;
  for (const row of preview.rows) {
    const value = row[columnId];
    if (form === 'PRESENCE') {
      if (typeof value !== 'boolean') return undefined;
      if (value) one += 1;
      else zero += 1;
      continue;
    }
    const count = form === 'COUNT' ? value : Array.isArray(value) ? value.length : undefined;
    if (typeof count !== 'number' || !Number.isInteger(count) || count < 0) return undefined;
    if (count === 0) zero += 1;
    else if (count === 1) one += 1;
    else many += 1;
  }
  return form === 'PRESENCE'
    ? { kind: 'PRESENCE', zero, oneOrMore: one }
    : { kind: form === 'COUNT' ? 'EXACT_COUNT' : 'VALUE_LIST', zero, one, many };
};

export const previewValueCoverage = (
  preview: ExplorerBuilderPreviewResult,
  columnIds: ReadonlyArray<string>,
): ReadonlyArray<PreviewColumnCoverage> => {
  const rows = preview.rows;
  if (rows === null) return [];
  const requested = new Set(columnIds);
  const fullOutput = preview.sampled === false && preview.partialValidation !== true &&
    rows.length === preview.rowCount;
  return preview.columns.filter((column) => requested.has(column.column)).map((column) => {
    let populatedRows = 0;
    const examples = new Set<string>();
    for (const row of rows) {
      const value = row[column.column];
      if (!hasValue(value)) continue;
      populatedRows += 1;
      if (examples.size < 3) examples.add(formatPreviewCell(value).slice(0, 80));
    }
    return {
      columnId: column.column,
      label: column.label,
      populatedRows,
      displayedRows: rows.length,
      fullOutput,
      examples: [...examples],
    };
  });
};

export const PreviewValueCoverage = ({
  preview,
  columnIds,
  resultForm,
}: {
  readonly preview: ExplorerBuilderPreviewResult;
  readonly columnIds: ReadonlyArray<string>;
  readonly resultForm?: 'COUNT' | 'PRESENCE' | 'ALL';
}) => {
  const coverage = previewValueCoverage(preview, columnIds);
  if (coverage.length === 0) return null;
  return (
    <div data-testid="construction-preview-value-coverage" className="mt-3 rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm text-slate-700">
      <h4 className="font-semibold text-slate-900">Values in the preview</h4>
      <ul className="mt-2 space-y-2">
        {coverage.map((column) => {
          const multiplicity = resultForm ? previewResultMultiplicity(preview, column.columnId, resultForm) : undefined;
          return (
            <li key={column.columnId} data-column-id={column.columnId}>
              <span className="font-medium">{column.label}</span>
              {' · '}{column.populatedRows} of {column.displayedRows} {column.fullOutput ? 'rows' : 'displayed rows'} contain a value.
              {multiplicity?.kind === 'EXACT_COUNT' ? (
                <span className="block text-xs text-slate-700">Matching records in {column.fullOutput ? 'all rows' : 'displayed rows'}: 0 for {multiplicity.zero}, 1 for {multiplicity.one}, 2 or more for {multiplicity.many}.</span>
              ) : multiplicity?.kind === 'PRESENCE' ? (
                <span className="block text-xs text-slate-700">Matching records in {column.fullOutput ? 'all rows' : 'displayed rows'}: none for {multiplicity.zero}, at least one for {multiplicity.oneOrMore}.</span>
              ) : multiplicity?.kind === 'VALUE_LIST' ? (
                <span className="block text-xs text-slate-700">Returned values in {column.fullOutput ? 'all rows' : 'displayed rows'}: 0 for {multiplicity.zero}, 1 for {multiplicity.one}, 2 or more for {multiplicity.many}.</span>
              ) : null}
              {column.examples.length > 0 ? <span className="block text-xs text-slate-600">Examples: {column.examples.join(', ')}</span> : null}
            </li>
          );
        })}
      </ul>
      {!coverage.every((column) => column.fullOutput) ? (
        <p className="mt-2 text-xs text-slate-600">These counts describe the preview rows. Coverage across the full table has not been measured.</p>
      ) : null}
    </div>
  );
};
