import { describe, expect, it } from 'vitest';
import {
  aggregateTransformationCapabilitySchema,
  aggregateOperationCapabilitySchema,
  columnTransformationChangeSchema,
  columnValueTransformationCapabilitiesSchema,
  columnValueTransformationSchema,
  explorerBuilderCandidateSchema,
  constructionChoiceSchema,
  explorerBuilderCommandSchema,
  explorerBuilderDocumentSchema,
  explorerColumnSourceSchema,
  rowDefinitionChoicesResponseSchema,
  rowDefinitionProposalSchema,
} from './types';

describe('explorerBuilderDocumentSchema', () => {
  const document = {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'patients', title: 'Patients' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    columns: [],
  };

  it('parses every closed row-definition variant returned by the Builder API', () => {
    const rows = [
      { kind: 'RECORDS', records: {} },
      {
        kind: 'GROUPS',
        groups: {
          source: {
            kind: 'FIELD',
            field: {
              occurrenceId: 'base',
              fieldPath: 'identifier[].system',
              missingKeyPolicy: 'GROUP_AS_MISSING',
            },
          },
        },
      },
      {
        kind: 'GROUPS',
        groups: {
          source: {
            kind: 'EXPLICIT',
            explicit: {
              revisionId: 'group-revision-1',
              unassignedMemberPolicy: 'GROUP_AS_UNASSIGNED',
            },
          },
        },
      },
      {
        kind: 'EXPANDED',
        expanded: {
          occurrenceId: 'base',
          scopePath: 'identifier[]',
          emptyCollectionPolicy: 'PRESERVE_PARENT',
        },
      },
    ] as const;

    for (const rowDefinition of rows) {
      expect(
        explorerBuilderDocumentSchema.parse({
          ...document,
          rows: rowDefinition,
        }).rows,
      ).toEqual(rowDefinition);
    }
  });

  it('rejects a row kind whose payload belongs to another variant', () => {
    expect(
      explorerBuilderDocumentSchema.safeParse({
        ...document,
        rows: {
          kind: 'RECORDS',
          expanded: {
            occurrenceId: 'base',
            scopePath: 'identifier[]',
            emptyCollectionPolicy: 'EXCLUDE',
          },
        },
      }).success,
    ).toBe(false);
  });
});

describe('row-definition contract schemas', () => {
  it('keeps empty descriptions, zero counts, false flags, and empty collections intact', () => {
    const choices = {
      snapshotToken: 'snapshot-token',
      outputId: 'output',
      choices: [{
        choiceId: 'opaque-choice',
        label: 'Grouping key',
        description: '',
        occurrenceSummary: 'Root occurrence',
        routeSummary: 'Root',
        kind: 'FIELD_GROUP',
        valueType: 'STRING',
        policies: [{ name: 'missingKeyPolicy', options: ['ERROR'] }],
      }],
    } as const;
    expect(rowDefinitionChoicesResponseSchema.parse(choices)).toEqual(choices);

    const proposal = {
      proposalId: 'proposal-receipt',
      baseReceiptId: 'base-receipt',
      outputId: 'output',
      snapshotToken: 'snapshot-token',
      draftVersion: 1,
      draftDigest: 'draft-digest',
      baseDocumentDigest: 'document-digest',
      candidateWorkspaceDigest: 'workspace-digest',
      mode: 'EXPANDED',
      comparison: {
        status: 'AVAILABLE',
        base: { rowCount: 0, sampled: false },
        candidate: { rowCount: 0, sampled: false },
        affectedColumns: [],
        notices: [],
        examples: [],
      },
    } as const;
    expect(rowDefinitionProposalSchema.parse(proposal)).toEqual(proposal);
  });

  it('rejects row choices that contain raw schema selectors or unsupported policy fields', () => {
    expect(rowDefinitionChoicesResponseSchema.safeParse({
      snapshotToken: 'snapshot-token',
      outputId: 'output',
      choices: [{
        choiceId: 'opaque-choice',
        label: 'Grouping key',
        description: '',
        occurrenceSummary: 'Root occurrence',
        routeSummary: 'Root',
        kind: 'FIELD_GROUP',
        valueType: 'STRING',
        policies: [{ name: 'missingKeyPolicy', options: ['ERROR'] }],
        fieldPath: 'not-in-the-public-contract',
      }],
    }).success).toBe(false);
  });
});

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

  it('accepts server-supported numeric aggregate operations and rejects unknown ones', () => {
    for (const operation of ['SUM', 'MEAN'] as const) {
      expect(explorerColumnSourceSchema.parse({
        kind: 'aggregate',
        aggregate: { operation, path: 'valueQuantity.value' },
      })).toEqual({
        kind: 'aggregate',
        aggregate: { operation, path: 'valueQuantity.value' },
      });
    }
    expect(explorerColumnSourceSchema.safeParse({
      kind: 'aggregate',
      aggregate: { operation: 'MEDIAN', path: 'valueQuantity.value' },
    }).success).toBe(false);
  });
});

describe('aggregateOperationCapabilitySchema', () => {
  it('parses strict server capability rows, including support details and required configuration', () => {
    const capability = {
      operation: 'SUM',
      rowContext: 'RECORDS',
      supported: true,
      resultLogicalType: 'decimal',
      resultCardinality: 'OPTIONAL_ONE',
      missingValueSemantics: 'null inputs are ignored; no non-null inputs returns null',
      contributorSemantics: 'every non-null numeric input contributes once, grouped by its source resource',
      requiresConfiguration: ['temporal'],
    };

    expect(aggregateOperationCapabilitySchema.parse(capability)).toEqual(capability);
    expect(aggregateOperationCapabilitySchema.safeParse({
      ...capability,
      unexpected: true,
    }).success).toBe(false);
    expect(aggregateOperationCapabilitySchema.safeParse({
      ...capability,
      rowContext: 'FHIR',
    }).success).toBe(false);
  });
});

describe('aggregateTransformationCapabilitySchema', () => {
  it('parses strict temporal field and unit preset capabilities', () => {
    const transformations = {
      temporalReduction: {
        available: true,
        timestampFields: [{
          candidateId: 'observation-date',
          nodeId: 'observation',
          resourceType: 'Observation',
          fieldPath: 'effectiveDateTime',
          label: 'Observed at',
        }],
        anchorFields: [{
          candidateId: 'patient-updated',
          nodeId: 'patient',
          resourceType: 'Patient',
          fieldPath: 'meta.lastUpdated',
          label: 'Updated at',
        }],
      },
      unitNormalization: {
        available: true,
        presets: [{
          policyId: 'to-centimeters',
          version: '2',
          target: { system: 'http://unitsofmeasure.org', code: 'cm' },
          available: true,
        }, {
          policyId: 'to-kilograms',
          version: '1',
          target: { system: 'http://unitsofmeasure.org', code: 'kg' },
          available: false,
          reasonCode: 'UNIT_PRESET_INCOMPATIBLE',
          reason: 'This preset does not cover all observed source units.',
        }],
      },
    };

    expect(aggregateTransformationCapabilitySchema.parse(transformations)).toEqual(transformations);
    expect(aggregateTransformationCapabilitySchema.safeParse({
      ...transformations,
      unsupported: true,
    }).success).toBe(false);
    const candidate = {
      candidateId: 'height-value',
      nodeId: 'observation',
      fieldPath: 'valueQuantity.value',
      label: 'Height',
      logicalType: 'decimal',
      cardinality: 'optional_one',
      filterable: true,
      chartable: true,
      projectionModes: ['VALUE'],
      defaultProjectionMode: 'VALUE',
      aggregateOperations: [],
      transformations,
      valueTransformations: {
        exactCategoryRecode: {
          available: false,
          reasonCode: 'COLUMN_VALUE_TYPE_UNSUPPORTED',
          reason: 'Exact category recoding requires a scalar string value.',
        },
        codedValueRecoding: {
          available: false,
          reasonCode: 'CODED_VALUE_RECODE_UNAVAILABLE',
          reason: 'Coded value recoding is unavailable because this scalar transformation cannot preserve both Coding.system and Coding.code.',
        },
      },
    } as const;
    expect(explorerBuilderCandidateSchema.parse(candidate)).toEqual(candidate);
    expect(explorerBuilderCandidateSchema.safeParse({
      ...candidate,
      transformations: undefined,
    }).success).toBe(false);
    expect(explorerBuilderCandidateSchema.safeParse({
      ...candidate,
      valueTransformations: undefined,
    }).success).toBe(false);
  });
});

describe('column value transformation boundary schemas', () => {
  const recoding = {
    kind: 'EXACT_CATEGORY_RECODE',
    exactCategoryRecode: {
      mappings: [
        { from: 'recorded-A', to: 'group-1' },
        { from: 'recorded-B', to: 'group-2' },
      ],
      unknownPolicy: 'KEEP_ORIGINAL',
    },
  } as const;

  it('parses persisted exact mappings and server-owned capability reasons', () => {
    expect(columnValueTransformationSchema.parse(recoding)).toEqual(recoding);
    expect(columnValueTransformationSchema.safeParse({
      ...recoding,
      exactCategoryRecode: { ...recoding.exactCategoryRecode, unknownPolicy: 'SET_NULL' },
    }).success).toBe(false);
    expect(columnValueTransformationSchema.safeParse({
      ...recoding,
      extra: true,
    }).success).toBe(false);
    const capabilities = {
      exactCategoryRecode: { available: true },
      codedValueRecoding: {
        available: false,
        reasonCode: 'CODED_VALUE_RECODE_UNAVAILABLE',
        reason: 'Both Coding.system and Coding.code must be preserved.',
      },
    } as const;
    expect(columnValueTransformationCapabilitiesSchema.parse(capabilities)).toEqual(capabilities);
  });

  it('parses generic replace and remove authoring commands', () => {
    expect(columnTransformationChangeSchema.parse({
      kind: 'SET',
      transformation: recoding,
    })).toEqual({ kind: 'SET', transformation: recoding });
    expect(columnTransformationChangeSchema.parse({ kind: 'REMOVE' })).toEqual({ kind: 'REMOVE' });
    expect(columnTransformationChangeSchema.safeParse({
      kind: 'REMOVE',
      transformation: recoding,
    }).success).toBe(false);
    expect(explorerBuilderCommandSchema.parse({
      type: 'UPDATE_COLUMN_TRANSFORMATION',
      outputId: 'patients',
      column: 'status',
      transformationChange: { kind: 'SET', transformation: recoding },
    })).toMatchObject({
      type: 'UPDATE_COLUMN_TRANSFORMATION',
      transformationChange: { kind: 'SET', transformation: recoding },
    });
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
