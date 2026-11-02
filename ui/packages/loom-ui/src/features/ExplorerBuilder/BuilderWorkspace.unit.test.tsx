import { describe, expect, it } from 'vitest';
import { REASON_DISPLAY_LIMIT, unavailableOperationReason } from './catalogItems';

const RESHAPE_COPY = {
  errorMessage: 'Loom could not check this table’s row operations.',
  checkingMessage: 'Checking this table’s available row operations…',
  label: 'This row operation is unavailable for the current table.',
} as const;

const RELATED_COPY = {
  errorMessage: 'Loom could not check this table’s row operations.',
  checkingMessage: 'Checking available related row paths…',
  label: 'This table has no executable related-record row path at the current stage.',
} as const;

const reshape = (
  stage: 'error' | 'checking' | 'ready',
  capability?: { readonly reasonCode?: string; readonly reason?: string },
) => unavailableOperationReason({ stage, ...RESHAPE_COPY, capability });

const related = (
  stage: 'error' | 'checking' | 'ready',
  capability?: { readonly reasonCode?: string; readonly reason?: string },
) => unavailableOperationReason({ stage, ...RELATED_COPY, capability });

describe('unavailable row operation reasons used by BuilderWorkspace', () => {
  it('shows the server reason verbatim instead of a generic sentence', () => {
    expect(reshape('ready', {
      reasonCode: 'CARRY_FORWARD',
      reason: 'Grouped rows cannot carry the starting record forward.',
    })).toBe('Grouped rows cannot carry the starting record forward.');
    expect(related('ready', {
      reason: 'The related path has no anchor that keeps one row per source record.',
    })).toBe('The related path has no anchor that keeps one row per source record.');
  });

  it('names an unknown reason code without inventing a cause', () => {
    expect(reshape('ready', { reasonCode: 'CARRY_FORWARD' })).toBe(
      'Loom reported that this is unavailable (CARRY_FORWARD) without an explanation.',
    );
    expect(related('ready', { reasonCode: 'STAGE_LOCKED_BY_PUBLISHED_RELEASE' })).toBe(
      'Loom reported that this is unavailable (STAGE_LOCKED_BY_PUBLISHED_RELEASE) without an explanation.',
    );
  });

  it('says nothing was reported when the capability stays silent', () => {
    expect(reshape('ready', {})).toBe(
      'This row operation is unavailable for the current table. Loom did not report a reason.',
    );
    expect(reshape('ready', { reasonCode: '   ', reason: '   ' })).toBe(
      'This row operation is unavailable for the current table. Loom did not report a reason.',
    );
    expect(related('ready', {})).toBe(
      'This table has no executable related-record row path at the current stage. Loom did not report a reason.',
    );
  });

  it('keeps the shipped sentence for a known reason code', () => {
    expect(reshape('ready', { reasonCode: 'INSUFFICIENT_SCALAR_COLUMNS' })).toBe(
      'Pivot needs fields for the row, category, and value. Add or select those fields first.',
    );
    expect(reshape('ready', { reasonCode: 'NO_NUMERIC_COLUMN' })).toBe(
      'Add a numeric field before creating a calculated value.',
    );
    expect(reshape('ready', { reasonCode: 'DIRECT_SOURCE_STAGE_REQUIRED' })).toBe(
      'This coded row operation applies to the starting records before other row changes.',
    );
  });

  it('keeps the plain shipped sentence when no capability row came back at all', () => {
    expect(reshape('ready')).toBe('This row operation is unavailable for the current table.');
    expect(related('ready')).toBe(
      'This table has no executable related-record row path at the current stage.',
    );
  });

  it('clamps and strips control characters from server-supplied text', () => {
    const reason = reshape('ready', { reason: `bad\u0000text ${'z'.repeat(500)}` });
    expect(reason).toContain('badtext');
    expect(reason).not.toContain('\u0000');
    expect(reason.length).toBe(REASON_DISPLAY_LIMIT + 1);
    expect(unavailableOperationReason({
      stage: 'error',
      errorMessage: `Loom failed\u0000 ${'x'.repeat(400)}`,
      checkingMessage: RESHAPE_COPY.checkingMessage,
      label: RESHAPE_COPY.label,
    })).toBe(`Loom failed ${'x'.repeat(REASON_DISPLAY_LIMIT - 12)}\u2026`);
  });

  it('keeps loading and failure states separate from unsupported reasons', () => {
    expect(reshape('checking')).toBe('Checking this table’s available row operations…');
    expect(related('checking')).toBe('Checking available related row paths…');
    expect(reshape('error')).toBe('Loom could not check this table’s row operations.');
    expect(related('error')).toBe('Loom could not check this table’s row operations.');
    expect(reshape('checking', { reasonCode: 'CARRY_FORWARD' })).not.toMatch(/CARRY_FORWARD/);
  });
});
