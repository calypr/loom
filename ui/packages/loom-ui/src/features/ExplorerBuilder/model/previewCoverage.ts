import type { ExplorerBuilderPreviewResult } from '../../../contracts/types';
import { formatPreviewCell } from './previewCellFormat';

const hasValue = (value: unknown): boolean => {
  if (value === null || value === undefined) return false;
  if (typeof value === 'string') return value.trim() !== '';
  if (Array.isArray(value)) return value.length > 0;
  return true;
};

interface PreviewColumnCoverage {
  readonly columnId: string;
  readonly label: string;
  readonly populatedRows: number;
  readonly displayedRows: number;
  readonly fullOutput: boolean;
  readonly examples: ReadonlyArray<string>;
}

type PreviewResultMultiplicity =
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
