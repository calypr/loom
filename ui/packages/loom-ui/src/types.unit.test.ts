import { describe, expect, it } from 'vitest';
import {
  explorerBuilderCommandSchema,
  explorerColumnSourceSchema,
} from './types';

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

describe('explorerBuilderCommandSchema', () => {
  it('accepts an in-place route relationship update', () => {
    expect(
      explorerBuilderCommandSchema.parse({
        type: 'UPDATE_ROUTE_EDGE',
        outputId: 'Specimen',
        occurrenceId: 'patient_subject',
        edgeId: 'specimen-patient-participant',
      }),
    ).toEqual({
      type: 'UPDATE_ROUTE_EDGE',
      outputId: 'Specimen',
      occurrenceId: 'patient_subject',
      edgeId: 'specimen-patient-participant',
    });
  });
});
