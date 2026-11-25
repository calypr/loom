import { describe, expect, it } from 'vitest';
import { copyForReasonCode, operationSummary, REASON_DISPLAY_LIMIT, sanitizeReasonText } from './catalogItems';

describe('copyForReasonCode', () => {
  it('shows the server reason verbatim when Loom reports one', () => {
    expect(copyForReasonCode(
      'FILTER_SOURCE_AFTER_GROUP',
      'Loom cannot filter on unselected fields after rows are grouped.',
    )).toBe('Loom cannot filter on unselected fields after rows are grouped.');
  });

  it('uses the shipped copy for a known reason code', () => {
    expect(copyForReasonCode('NO_NUMERIC_COLUMN')).toBe(
      'Add a numeric field before creating a calculated value.',
    );
    expect(copyForReasonCode('INSUFFICIENT_SCALAR_COLUMNS')).toBe(
      'Pivot needs fields for the row, category, and value. Add or select those fields first.',
    );
    expect(copyForReasonCode('DIRECT_SOURCE_STAGE_REQUIRED')).toBe(
      'This coded row operation applies to the starting records before other row changes.',
    );
    expect(copyForReasonCode('NO_ROOT_ANCHOR_FIELDS')).toBe(
      'Loom reported that this is unavailable (NO_ROOT_ANCHOR_FIELDS) without an explanation.',
    );
  });

  it('names an unknown reason code instead of inventing a cause', () => {
    expect(copyForReasonCode('STAGE_LOCKED_BY_PUBLISHED_RELEASE')).toBe(
      'Loom reported that this is unavailable (STAGE_LOCKED_BY_PUBLISHED_RELEASE) without an explanation.',
    );
  });

  it('says nothing was reported when both fields are missing', () => {
    expect(copyForReasonCode('   ', '   ')).toBe('Loom did not report why this is unavailable.');
    expect(copyForReasonCode()).toBe('Loom did not report why this is unavailable.');
  });
});

describe('server-supplied reason display', () => {
  it('drops control characters before showing server text', () => {
    expect(copyForReasonCode('X', 'bad\u0000text\u0007 here')).toBe('badtext here');
    expect(copyForReasonCode('A\u0000B\u001f')).toBe(
      'Loom reported that this is unavailable (AB) without an explanation.',
    );
    expect(sanitizeReasonText('\t\n\r')).toBe('');
  });

  it('clamps oversized server text to the display limit', () => {
    const copy = copyForReasonCode(undefined, 'y'.repeat(100_000));
    expect(copy).toBe(`${'y'.repeat(REASON_DISPLAY_LIMIT)}\u2026`);
    expect(copyForReasonCode('y'.repeat(100_000))).toBe(
      `Loom reported that this is unavailable (${'y'.repeat(REASON_DISPLAY_LIMIT)}\u2026) without an explanation.`,
    );
  });
});

describe('operationSummary', () => {
  it('reports a cause when the server supplies either field', () => {
    const summary = operationSummary('Grouping is unavailable.', {
      reasonCode: 'INSUFFICIENT_SCALAR_COLUMNS',
    });
    expect(summary.causeReported).toBe(true);
    expect(summary.reasonCode).toBe('INSUFFICIENT_SCALAR_COLUMNS');
    expect(summary.detail).toBe(
      'Pivot needs fields for the row, category, and value. Add or select those fields first.',
    );
  });

  it('keeps the label available for a silent server response', () => {
    const summary = operationSummary('Grouping is unavailable.', {});
    expect(summary.causeReported).toBe(false);
    expect(summary.reasonCode).toBeUndefined();
    expect(summary.label).toBe('Grouping is unavailable.');
  });
});
