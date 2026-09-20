import { describe, expect, it } from 'vitest';
import {
  constructionChoiceSchema,
  explorerBuilderCommandSchema,
  explorerColumnSourceSchema,
} from './types';

describe('explorerColumnSourceSchema', () => {
  it('preserves a typed Identifier namespace/value binding', () => {
    const source = {
      kind: 'identifierBySystem',
      lookup: {
        identifier: {
          ownerPath: 'identifier[]',
          systemPath: 'system',
          valuePath: 'value',
          systemURI: 'https://proteomic.datacommons.cancer.gov/pdc/case_id',
          logicalType: 'string',
        },
        projectionMode: 'VALUE',
      },
    };
    expect(explorerColumnSourceSchema.parse(source)).toEqual(source);
    expect(explorerColumnSourceSchema.safeParse({
      ...source,
      lookup: { ...source.lookup, match: source.lookup.identifier.systemURI },
    }).success).toBe(false);
    expect(explorerColumnSourceSchema.safeParse({ ...source, kind: 'codedValue' }).success).toBe(false);
  });

  it('preserves the selected terminology pair and rejects competing legacy fields', () => {
    const binding = {
      ownerPath: 'component[]', keyPath: 'component[].code.coding[]',
      systemPath: 'system', codePath: 'code', valuePath: 'valueQuantity.value', logicalType: 'decimal',
    };
    const source = {
      kind: 'codedValue',
      lookup: { binding, key: { system: 'urn:system:B', code: 'shared' }, projectionMode: 'ALL' },
    };
    expect(explorerColumnSourceSchema.parse(source)).toEqual(source);
    expect(explorerColumnSourceSchema.safeParse({ ...source, kind: 'identifierBySystem' }).success).toBe(false);
    for (const kind of ['codingBySystem', 'observationComponentByCode']) {
      expect(explorerColumnSourceSchema.safeParse({ ...source, kind }).success).toBe(false);
    }
    expect(explorerColumnSourceSchema.safeParse({ ...source, lookup: { ...source.lookup, match: 'shared' } }).success).toBe(false);
    expect(explorerColumnSourceSchema.safeParse({ ...source, lookup: { binding } }).success).toBe(false);
    expect(explorerColumnSourceSchema.safeParse({ ...source, lookup: { binding, key: { code: 'shared' } } }).success).toBe(false);
  });

  it('preserves repeated FHIR owners as a distinct closed source', () => {
    const source = {
      kind: 'ownerRecords',
      ownerRecords: {
        binding: {
          ownerPath: 'component[]', keyPath: 'component[].code.coding[]',
          systemPath: 'system', codePath: 'code', valuePath: 'valueQuantity.value',
          choiceArms: ['valueQuantity'], logicalType: 'decimal', unitPath: 'valueQuantity.unit',
        },
        key: { system: 'http://loinc.org', code: '8302-2' },
      },
    };
    expect(explorerColumnSourceSchema.parse(source)).toEqual(source);
    expect(explorerColumnSourceSchema.safeParse({ ...source, lookup: source.ownerRecords }).success).toBe(false);
    expect(explorerColumnSourceSchema.safeParse({ ...source, ownerRecords: { binding: source.ownerRecords.binding } }).success).toBe(false);
    expect(explorerColumnSourceSchema.safeParse({ ...source, kind: 'codedValue' }).success).toBe(false);
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
  it('accepts compiler-issued FIELD and SEMANTIC construction choices and rejects unknown variants', () => {
    const option = {
      form: 'VALUE',
      shape: 'SCALAR',
      decision: 'DEFAULT',
      preservation: 'PRESERVING',
      rowEffect: 'PRESERVES_ROW_GRAIN',
      support: 'SUPPORTED',
      reason: 'The scalar output preserves the row grain.',
    };
    const fieldChoice = {
      choiceId: 'choice-field',
      route: [],
      presentation: {
        summary: 'Patient identifier',
        facts: [{ label: 'Field', value: 'id' }],
      },
      source: {
        kind: 'FIELD',
        candidateId: 'patient-id',
        nodeId: 'patient',
        resourceType: 'Patient',
        path: 'id',
        cardinality: 'required_one',
      },
      options: [option],
    };
    const semanticChoice = {
      choiceId: 'choice-semantic',
      route: [],
      presentation: {
        summary: 'Hemoglobin A1c',
        facts: [{ label: 'Code', value: 'http://loinc.org · 4548-4' }],
      },
      source: {
        kind: 'SEMANTIC',
        conceptId: 'concept-a',
        bindingId: 'binding-a',
        candidateId: 'observation-value',
        nodeId: 'observation',
        resourceType: 'Observation',
        sourcePath: 'valueQuantity',
        fieldPath: 'root.valueQuantity.value',
        valueSelector: 'valueQuantity.value',
        logicalType: 'decimal',
        ruleVersion: 'rule-1',
        schemaVersion: 1,
        cardinality: 'optional_one',
      },
      options: [option],
    };
    expect(constructionChoiceSchema.parse(fieldChoice)).toEqual(fieldChoice);
    expect(constructionChoiceSchema.parse(semanticChoice)).toEqual(semanticChoice);
    expect(constructionChoiceSchema.parse({
      ...semanticChoice,
      options: [{ ...option, form: 'OWNER_RECORDS', shape: 'LIST', decision: 'REQUIRES_DECISION' }],
    }).options[0].form).toBe('OWNER_RECORDS');
    expect(constructionChoiceSchema.safeParse({
      ...fieldChoice,
      source: { ...fieldChoice.source, kind: 'ROUTE' },
    }).success).toBe(false);
    expect(constructionChoiceSchema.safeParse({
      ...fieldChoice,
      options: [{ ...option, form: 'INDEXED' }],
    }).success).toBe(false);
    expect(constructionChoiceSchema.safeParse({ ...fieldChoice, unexpected: true }).success).toBe(false);

    const command = {
      type: 'APPLY_CONSTRUCTION_CHOICE',
      outputId: 'patients',
      constructionChoice: { choiceId: 'choice-field', form: 'VALUE' },
      title: 'Patient ID',
    };
    expect(explorerBuilderCommandSchema.parse(command)).toEqual(command);
    expect(explorerBuilderCommandSchema.safeParse({
      ...command,
      constructionChoice: { choiceId: 'choice-field', form: 'INDEXED' },
    }).success).toBe(false);
    expect(explorerBuilderCommandSchema.safeParse({ ...command, unexpected: 'unknown-field' }).success).toBe(false);
    expect(explorerBuilderCommandSchema.safeParse({ ...command, type: 'APPLY_UNKNOWN' }).success).toBe(false);
  });

  it('preserves the full extension ancestry as a closed source payload', () => {
    const source = {
      kind: 'extensionByUrl',
      lookup: {
        extension: {
          ownerPath: 'extension[].extension[]', urlPath: ['urn:parent:left', 'urn:leaf'],
          valuePath: 'valueString', logicalType: 'string',
        },
        projectionMode: 'ALL',
      },
    };
    expect(explorerColumnSourceSchema.parse(source)).toEqual(source);
    expect(() => explorerColumnSourceSchema.parse({ ...source, lookup: { ...source.lookup, match: 'urn:leaf' } })).toThrow();
    expect(() => explorerColumnSourceSchema.parse({ ...source, lookup: { ...source.lookup, extension: { ...source.lookup.extension, urlPath: [] } } })).toThrow();
    expect(() => explorerColumnSourceSchema.parse({ ...source, kind: 'observationComponentByCode' })).toThrow();
  });

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

  it('accepts explicit contributor set and clear commands', () => {
    const contributor = {
      candidateId: 'observation-status',
      operator: 'EQUALS',
      quantifier: 'ANY',
      value: { kind: 'STRING', string: 'registered' },
    };
    expect(explorerBuilderCommandSchema.parse({
      type: 'SET_COLUMN_CONTRIBUTOR', outputId: 'patients', column: 'registered_count', contributor,
    })).toMatchObject({ type: 'SET_COLUMN_CONTRIBUTOR', contributor });
    expect(explorerBuilderCommandSchema.parse({
      type: 'CLEAR_COLUMN_CONTRIBUTOR', outputId: 'patients', column: 'registered_count',
    })).toEqual({ type: 'CLEAR_COLUMN_CONTRIBUTOR', outputId: 'patients', column: 'registered_count' });
    expect(explorerBuilderCommandSchema.safeParse({
      type: 'SET_COLUMN_CONTRIBUTOR', outputId: 'patients', column: 'registered_count',
      contributor: { ...contributor, value: { kind: 'CODE', code: { system: 'urn:system', code: 'registered' } } },
    }).success).toBe(false);
  });
});
