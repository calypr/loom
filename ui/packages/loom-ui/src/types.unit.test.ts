import { describe, expect, it } from 'vitest';
import {
  explorerBuilderCommandSchema,
  explorerColumnSourceSchema,
} from './types';

describe('explorerColumnSourceSchema', () => {
  it('preserves the selected terminology pair and rejects competing legacy fields', () => {
    const binding = {
      ownerPath: 'component[]', keyPath: 'component[].code.coding[]',
      systemPath: 'system', codePath: 'code', valuePath: 'valueQuantity.value', logicalType: 'decimal',
    };
    const source = {
      kind: 'observationComponentByCode',
      lookup: { binding, key: { system: 'urn:system:B', code: 'shared' }, projectionMode: 'ALL' },
    };
    expect(explorerColumnSourceSchema.parse(source)).toEqual(source);
    expect(explorerColumnSourceSchema.safeParse({ ...source, kind: 'identifierBySystem' }).success).toBe(false);
    expect(explorerColumnSourceSchema.safeParse({ ...source, lookup: { ...source.lookup, match: 'shared' } }).success).toBe(false);
    expect(explorerColumnSourceSchema.safeParse({ ...source, lookup: { binding } }).success).toBe(false);
    expect(explorerColumnSourceSchema.safeParse({ ...source, lookup: { binding, key: { code: 'shared' } } }).success).toBe(false);
  });

  it('rejects legacy flat payloads and fields belonging to another source kind', () => {
    for (const source of [
      { kind: 'field', fieldPath: 'gender', projectionMode: 'VALUE' },
      { kind: 'field', field: { path: 'gender' }, aggregate: { operation: 'COUNT' } },
      { kind: 'projectId', field: { path: 'id' } },
      { kind: 'aggregate', aggregate: { operation: 'COUNT', match: 'code' } },
    ]) {
      expect(explorerColumnSourceSchema.safeParse(source).success).toBe(false);
    }
  });

  it('preserves explicit acknowledgment of lossy related-record selection', () => {
    const source = {
      kind: 'field',
      field: {
        path: 'valueQuantity.value',
        projectionMode: 'VALUE',
        relatedSelection: { kind: 'first-by-resource-key', acknowledged: true },
      },
    };
    expect(explorerColumnSourceSchema.parse(source)).toEqual(source);
  });

  it('accepts the aggregate sources emitted by Loom authoring workspaces', () => {
    expect(
      explorerColumnSourceSchema.parse({
        kind: 'aggregate',
        aggregate: {
          operation: 'CONTAINS_ALL',
          path: 'type.coding[].code',
          requiredValues: ['Tumor', 'Normal'],
        },
      }),
    ).toEqual({
      kind: 'aggregate',
      aggregate: {
        operation: 'CONTAINS_ALL',
        path: 'type.coding[].code',
        requiredValues: ['Tumor', 'Normal'],
      },
    });
  });

  it('rejects aggregate operations Loom does not support', () => {
    expect(() =>
      explorerColumnSourceSchema.parse({
        kind: 'aggregate',
        aggregate: {
          operation: 'SUM',
          path: 'valueQuantity.value',
        },
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
