import { describe, expect, it } from 'vitest';
import {
  EXPLORER_AUTHORING_API_VERSION,
  type ExplorerBuilderPreviewResult,
} from '../../../types';
import { previewResultMultiplicity, previewValueCoverage } from './PreviewValueCoverage';

const preview = (
  values: ReadonlyArray<unknown>,
  sampled: boolean,
): ExplorerBuilderPreviewResult => ({
  apiVersion: EXPLORER_AUTHORING_API_VERSION,
  kind: 'ExplorerBuilderPreview',
  receiptId: 'candidate',
  outputId: 'table',
  columns: [{ column: 'new_value', label: 'New value', logicalType: 'string', filterable: false, chartable: false }],
  rows: values.map((value) => ({ new_value: value })),
  rowCount: values.length,
  sampled,
  diagnostics: [],
});

describe('previewValueCoverage', () => {
  it('counts zero and false as values, but excludes null, blank strings, and empty lists', () => {
    expect(previewValueCoverage(preview([0, false, null, '', [], 366], true), ['new_value'])).toEqual([{
      columnId: 'new_value',
      label: 'New value',
      populatedRows: 3,
      displayedRows: 6,
      fullOutput: false,
      examples: ['0', 'false', '366'],
    }]);
  });

  it('calls coverage complete only when the preview explicitly includes every row', () => {
    expect(previewValueCoverage(preview(['A', null], false), ['new_value'])[0]?.fullOutput).toBe(true);
    expect(previewValueCoverage(preview(['A', null], true), ['new_value'])[0]?.fullOutput).toBe(false);
  });

  it('distinguishes zero, one, and many related records without treating false as missing', () => {
    expect(previewResultMultiplicity(preview([0, 1, 38], true), 'new_value', 'COUNT')).toEqual({
      kind: 'EXACT_COUNT', zero: 1, one: 1, many: 1,
    });
    expect(previewResultMultiplicity(preview([false, true], true), 'new_value', 'PRESENCE')).toEqual({
      kind: 'PRESENCE', zero: 1, oneOrMore: 1,
    });
    expect(previewResultMultiplicity(preview([[], ['a'], ['a', 'b']], true), 'new_value', 'ALL')).toEqual({
      kind: 'VALUE_LIST', zero: 1, one: 1, many: 1,
    });
  });
});
