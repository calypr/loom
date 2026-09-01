import { describe, expect, it } from 'vitest';
import { explorerColumnSourceSchema } from './types';

describe('explorerColumnSourceSchema', () => {
  it('accepts the aggregate sources emitted by Loom authoring workspaces', () => {
    expect(
      explorerColumnSourceSchema.parse({
        kind: 'aggregate',
        operation: 'CONTAINS_ALL',
        fieldPath: 'type.coding[].code',
        requiredValues: ['Tumor', 'Normal'],
      }),
    ).toEqual({
      kind: 'aggregate',
      operation: 'CONTAINS_ALL',
      fieldPath: 'type.coding[].code',
      requiredValues: ['Tumor', 'Normal'],
    });
  });

  it('rejects aggregate operations Loom does not support', () => {
    expect(() =>
      explorerColumnSourceSchema.parse({
        kind: 'aggregate',
        operation: 'SUM',
        fieldPath: 'valueQuantity.value',
      }),
    ).toThrow();
  });
});
