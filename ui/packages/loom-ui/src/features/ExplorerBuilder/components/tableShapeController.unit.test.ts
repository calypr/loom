import { describe, expect, it } from 'vitest';
import type { TableShapeResolution } from '../../../types';
import { tableShapeOperandSelectionFor } from './tableShapeController';

const pivotResolution: TableShapeResolution = {
  catalogId: 'catalog-1',
  resolutionId: 'pivot-1',
  kind: 'PIVOT',
  outputDescriptors: [
    {
      kind: 'group',
      groupColumn: { kind: 'column', choiceId: 'patient' },
      operandChoiceId: 'pivot-group-operand',
      outputColumn: 'patient_id',
      outputLabel: 'Patient ID',
      type: { logicalType: 'string', nullable: false },
    },
    {
      kind: 'category',
      category: { kind: 'pivotCategory', choiceId: 'systolic' },
      operandChoiceId: 'pivot-category-operand',
      outputColumn: 'systolic',
      outputLabel: 'Systolic',
      type: { logicalType: 'number', nullable: true },
    },
  ],
  postPivotOperands: [],
};

describe('table-shape controller operand adapters', () => {
  it('maps exact pivot output descriptors, ordered derived references, and tagged literals', () => {
    expect(tableShapeOperandSelectionFor({
      kind: 'pivotOutput',
      reference: { kind: 'group', column: { kind: 'column', choiceId: 'patient' } },
    }, new Map(), pivotResolution)).toEqual({ kind: 'CATALOG_CHOICE', choiceId: 'pivot-group-operand' });

    expect(tableShapeOperandSelectionFor({
      kind: 'pivotOutput',
      reference: { kind: 'category', category: { kind: 'pivotCategory', choiceId: 'systolic' } },
    }, new Map(), pivotResolution)).toEqual({ kind: 'CATALOG_CHOICE', choiceId: 'pivot-category-operand' });

    expect(tableShapeOperandSelectionFor({ kind: 'derived', localId: 'earlier' }, new Map([['earlier', 'derived-1']]), pivotResolution))
      .toEqual({ kind: 'RESOLUTION_OUTPUT', resolutionId: 'derived-1' });
    expect(tableShapeOperandSelectionFor({ kind: 'literal', representation: 'integer', text: '0' }, new Map(), pivotResolution))
      .toEqual({ kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 0 } });
    expect(tableShapeOperandSelectionFor({ kind: 'literal', representation: 'decimal', text: '1.25' }, new Map(), pivotResolution))
      .toEqual({ kind: 'LITERAL', literal: { kind: 'DECIMAL', decimal: 1.25 } });
  });

  it('refuses a selected pivot output without its exact server descriptor', () => {
    expect(() => tableShapeOperandSelectionFor({
      kind: 'pivotOutput',
      reference: { kind: 'group', column: { kind: 'column', choiceId: 'other' } },
    }, new Map(), pivotResolution)).toThrow('no matching server operand choice');
  });
});
