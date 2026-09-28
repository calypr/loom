import { describe, expect, it } from 'vitest';
import {
  EXPLORER_AUTHORING_SEMANTICS_VERSION,
  assertExplorerBuilderPreviewResult,
  assertExplorerStateV1,
  aggregateTransformationCapabilitySchema,
  aggregateOperationCapabilitySchema,
  columnTransformationChangeSchema,
  columnValueTransformationCapabilitiesSchema,
  columnValueTransformationSchema,
  constructionCapabilitiesResponseSchema,
  constructionInputsRequestSchema,
  constructionInputsResponseSchema,
  constructionSchema,
  explorerBuilderCandidateSchema,
  constructionChoiceSchema,
  explorerBuilderCommandSchema,
  explorerBuilderDocumentSchema,
  explorerColumnSourceSchema,
  rowDefinitionChoicesResponseSchema,
  rowDefinitionProposalSchema,
  explicitGroupCreateRequestSchema,
  explicitGroupRevisionSummarySchema,
  tableShapeComparisonSchema,
  tableShapeProposalRequestSchema,
  tableShapeTaggedScalarSchema,
} from './types';

describe('Explorer authoring contract version', () => {
  it('bumps for the contributor-window aggregate wire contract', () => {
    expect(EXPLORER_AUTHORING_SEMANTICS_VERSION).toBe(9);
  });
});

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
    expect(explorerBuilderDocumentSchema.safeParse(document).success).toBe(false);
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

  it('requires unique stable source column IDs for construction documents', () => {
    const sourceColumn = {
      columnId: 'source_patient_id',
      column: 'patient_id',
      label: 'Patient ID',
      occurrenceId: 'base',
      source: { kind: 'projectId' },
    };
    const stagedDocument = {
      ...document,
      rows: { kind: 'RECORDS', records: {} },
      columns: [sourceColumn],
      construction: { version: 1, steps: [] },
    };

    expect(explorerBuilderDocumentSchema.parse(stagedDocument).columns[0]?.columnId)
      .toBe('source_patient_id');
    expect(explorerBuilderDocumentSchema.safeParse({
      ...stagedDocument,
      columns: [{ ...sourceColumn, columnId: undefined }],
    }).success).toBe(false);
    expect(explorerBuilderDocumentSchema.safeParse({
      ...stagedDocument,
      columns: [sourceColumn, { ...sourceColumn, column: 'other_id' }],
    }).success).toBe(false);
  });

  it('preserves a persisted server table shape as opaque JSON during Builder reload parsing', () => {
    const tableShape = {
      reshape: {
        kind: 'GROUPED_PIVOT',
        pivot: {
          groupKeys: ['subject-column', 'status-column'],
          categories: [{ key: { kind: 'STRING', value: 'alpha' }, output: { column: 'alpha', label: 'Alpha' } }],
          duplicatePolicy: 'SUM',
          missingCellPolicy: 'NULL',
          unlistedCategoryPolicy: 'EXCLUDE',
        },
      },
      derived: [{ output: { column: 'alpha_total', label: 'Alpha total' }, operation: 'ADD' }],
      serverOwnedMetadata: { version: 1, durable: true },
    };
    const parsed = explorerBuilderDocumentSchema.parse({
      ...document,
      rows: { kind: 'RECORDS', records: {} },
      tableShape,
    });

    expect(parsed.tableShape).toEqual(tableShape);
    expect(JSON.parse(JSON.stringify(parsed)).tableShape).toEqual(tableShape);
    expect(explorerBuilderDocumentSchema.safeParse({
      ...document,
      rows: { kind: 'RECORDS', records: {} },
      tableShape,
      unexpectedDocumentField: true,
    }).success).toBe(false);

    for (const invalidTableShape of ['GROUPED_PIVOT', []]) {
      expect(explorerBuilderDocumentSchema.safeParse({
        ...document,
        rows: { kind: 'RECORDS', records: {} },
        tableShape: invalidTableShape,
      }).success).toBe(false);
    }
  });
});

describe('staged construction contract', () => {
  it('parses typed GROUP and EXPAND operations and enforces aggregate inputs', () => {
    const column = { id: 'source', name: 'source', label: 'Source' };
    const construction = {
      version: 1,
      steps: [
        {
          id: 'group-step',
          inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: {
            kind: 'GROUP',
            group: {
              constructionId: 'group-step',
              keys: [{ inputColumnId: 'source', outputColumnId: 'grouped' }],
              aggregates: [
                { operation: 'COUNT_ROWS', outputColumnId: 'row_count' },
                { operation: 'SUM', inputColumnId: 'source', outputColumnId: 'sum' },
              ],
            },
          },
          outputs: [column],
        },
        {
          id: 'expand-step',
          inputs: [{ kind: 'STEP_OUTPUT', stepId: 'group-step' }],
          operation: {
            kind: 'EXPAND',
            expand: {
              constructionId: 'expand-step',
              inputColumnId: 'source',
              outputColumnId: 'item',
              ordinalColumnId: 'position',
              emptyPolicy: 'PRESERVE_PARENT',
            },
          },
          outputs: [column],
        },
      ],
    };

    expect(constructionSchema.parse(construction)).toEqual(construction);
    expect(constructionSchema.safeParse({
      ...construction,
      steps: [{
        ...construction.steps[0],
        operation: {
          kind: 'GROUP',
          group: {
            constructionId: 'group-step',
            aggregates: [{ operation: 'COUNT_ROWS', inputColumnId: 'source', outputColumnId: 'row_count' }],
          },
        },
      }],
    }).success).toBe(false);
    expect(constructionSchema.safeParse({
      ...construction,
      steps: [{
        ...construction.steps[0],
        operation: {
          kind: 'GROUP',
          group: {
            constructionId: 'group-step',
            aggregates: [{ operation: 'SUM', outputColumnId: 'sum' }],
          },
        },
      }],
    }).success).toBe(false);
  });

  it('parses pinned Combine operations and optional output nullability', () => {
    const inputs = [
      { kind: 'TABLE_REVISION', tableId: 'patients', revisionId: 'r1', outputId: 'out1' },
      { kind: 'TABLE_REVISION', tableId: 'labs', revisionId: 'r2', outputId: 'out2' },
    ];
    const outputs = [
      { id: 'patient_id', name: 'patient_id', label: 'Patient ID', nullable: false },
      { id: 'lab_value', name: 'lab_value', label: 'Lab value', nullable: true },
    ];
    const construction = {
      version: 1,
      steps: [
        {
          id: 'join',
          inputs,
          operation: {
            kind: 'COMBINE',
            combine: {
              kind: 'KEY_JOIN',
              keys: [{ leftColumnId: 'patient_id', rightColumnId: 'subject_id' }],
              projections: [
                { outputColumnId: 'patient_id', inputIndex: 0, inputColumnId: 'patient_id' },
                { outputColumnId: 'lab_value', inputIndex: 1, inputColumnId: 'result_value' },
              ],
              joinType: 'LEFT',
              rightMatchPolicy: 'PRESERVE_ALL',
            },
          },
          outputs,
        },
        {
          id: 'append',
          inputs,
          operation: {
            kind: 'COMBINE',
            combine: {
              kind: 'APPEND',
              projections: [
                { outputColumnId: 'patient_id', inputIndex: 0, inputColumnId: 'patient_id' },
                { outputColumnId: 'patient_id', inputIndex: 1, inputColumnId: 'person_id' },
              ],
            },
          },
          outputs: [outputs[0]],
        },
        {
          id: 'membership',
          inputs,
          operation: {
            kind: 'COMBINE',
            combine: {
              kind: 'MEMBERSHIP',
              keys: [{ leftColumnId: 'patient_id', rightColumnId: 'subject_id' }],
              projections: [
                { outputColumnId: 'patient_id', inputIndex: 0, inputColumnId: 'patient_id' },
              ],
              membershipMode: 'EXCLUDE',
            },
          },
          outputs: [outputs[0]],
        },
      ],
    };

    expect(constructionSchema.parse(construction)).toEqual(construction);
    expect(constructionSchema.safeParse({
      ...construction,
      steps: [{
        ...construction.steps[0],
        operation: {
          kind: 'COMBINE',
          combine: {
            ...construction.steps[0].operation.combine,
            projections: [{ ...construction.steps[0].operation.combine.projections[0], inputIndex: -1 }],
          },
        },
      }],
    }).success).toBe(false);
    expect(constructionSchema.safeParse({
      ...construction,
      steps: [{
        ...construction.steps[0],
        operation: {
          kind: 'COMBINE',
          combine: { ...construction.steps[0].operation.combine, keys: [] },
        },
      }],
    }).success).toBe(false);
    expect(constructionSchema.safeParse({
      ...construction,
      steps: [{
        ...construction.steps[0],
        outputs: [{ ...outputs[0], nullable: 'false' }],
      }],
    }).success).toBe(false);
  });

  it('parses exact published table revision inputs for Combine discovery', () => {
    const request = {
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 7,
      expectedDraftDigest: 'draft-7',
      query: 'lab',
      cursor: 'page-2',
      limit: 25,
    };
    expect(constructionInputsRequestSchema.parse(request)).toEqual(request);
    expect(constructionInputsRequestSchema.safeParse({ ...request, limit: 101 }).success).toBe(false);

    const response = {
      snapshotToken: 'snapshot-1',
      draftVersion: 7,
      draftDigest: 'draft-7',
      datasetGeneration: 'generation-a',
      entries: [{
        kind: 'TABLE_REVISION',
        tableId: 'labs',
        revisionId: 'revision-2',
        outputId: 'lab-results',
        tableTitle: 'Laboratory results',
        outputTitle: 'Results',
        rowMeaning: 'Observation',
        isCurrent: false,
        createdAt: '2026-09-20T00:00:00.000Z',
        columns: [{
          id: 'subject-id',
          name: 'subject_id',
          label: 'Subject ID',
          type: 'string',
          clickhouseType: 'String',
          nullable: false,
          repeated: false,
          semanticPath: 'Observation.subject',
        }],
      }],
      nextCursor: 'page-3',
    };
    expect(constructionInputsResponseSchema.parse(response)).toEqual(response);
    expect(constructionInputsResponseSchema.safeParse({
      ...response,
      entries: [{ ...response.entries[0], revisionId: '' }],
    }).success).toBe(false);
  });

  it('parses stage cardinality and all advertised construction capabilities', () => {
    const stage = {
      id: 'source_projection',
      inputStageId: '',
      columns: [{ id: 'codes', name: 'codes', label: 'Codes', cardinality: 'many' }],
      capabilities: [
        { kind: 'GROUP', supported: true },
        { kind: 'EXPAND', supported: true },
        { kind: 'RELATED_ELIGIBILITY', supported: true },
      ],
    };
    const response = {
      snapshotToken: 'snapshot',
      draftVersion: 1,
      draftDigest: 'sha256:draft',
      outputId: 'patients',
      stageId: 'source_projection',
      baseConstruction: { version: 1, steps: [] },
      stages: [stage],
      selectedStage: stage,
    };

    expect(constructionCapabilitiesResponseSchema.parse(response).selectedStage.columns[0]?.cardinality)
      .toBe('many');
    expect(constructionCapabilitiesResponseSchema.parse(response).selectedStage.capabilities)
      .toContainEqual({ kind: 'RELATED_ELIGIBILITY', supported: true });
    expect(constructionCapabilitiesResponseSchema.safeParse({
      ...response,
      selectedStage: {
        ...stage,
        columns: [{ ...stage.columns[0], cardinality: 'MANY' }],
      },
    }).success).toBe(false);
    expect(constructionCapabilitiesResponseSchema.safeParse({
      ...response,
      selectedStage: {
        ...stage,
        columns: [{ ...stage.columns[0], nullable: true }],
      },
    }).success).toBe(false);
  });

  it('retains the exact related record anchor from an expanded stage', () => {
    const stage = {
      id: 'expand-encounters', inputStageId: 'source_projection', operation: 'RELATED_EXPAND',
      columns: [{ id: 'encounter-id', name: 'encounter_id', label: 'Encounter ID', type: 'string' }],
      capabilities: [{ kind: 'RELATED_EXPAND', supported: true }],
      relatedExpand: {
        anchorColumnId: '_key', anchorColumn: '_key', anchorKind: 'root',
        anchorNodeId: 'patient-node', anchorResourceType: 'Patient', relatedRecordColumnId: 'encounter-id',
        parentIdentityColumnId: 'parent-id', parentIdentityColumn: '_key',
        terminalIdentityColumn: 'terminal-id', targetNodeId: 'encounter-node',
        targetResourceType: 'Encounter',
        route: [{
          edgeId: 'patient-encounter', fromNodeId: 'patient-node', toNodeId: 'encounter-node',
          fromResourceType: 'Patient', toResourceType: 'Encounter', relationship: 'subject_Patient',
          storageDirection: 'INBOUND', matchMode: 'OPTIONAL',
        }],
      },
    };
    const response = {
      snapshotToken: 'snapshot', draftVersion: 1, draftDigest: 'sha256:draft',
      outputId: 'patients', stageId: stage.id,
      baseConstruction: { version: 1, steps: [] }, stages: [stage], selectedStage: stage,
    };

    expect(constructionCapabilitiesResponseSchema.parse(response).selectedStage.relatedExpand)
      .toEqual(stage.relatedExpand);
  });
});

describe('explorerBuilderCommandSchema', () => {
  it('accepts only the server-issued revision ID for Undo', () => {
    expect(
      explorerBuilderCommandSchema.parse({
        type: 'RESTORE_DRAFT_REVISION',
        draftRevisionId: 'draft_revision_1',
      }),
    ).toEqual({
      type: 'RESTORE_DRAFT_REVISION',
      draftRevisionId: 'draft_revision_1',
    });
    expect(() =>
      explorerBuilderCommandSchema.parse({
        type: 'RESTORE_DRAFT_REVISION',
        draftRevisionId: 'draft_revision_1',
        outputId: 'patients',
      }),
    ).toThrow();
  });
});

describe('row-definition contract schemas', () => {
  it('keeps empty descriptions, zero counts, false flags, and empty collections intact', () => {
    const choices = {
      snapshotToken: 'snapshot-token',
      outputId: 'output',
      choices: [{
        choiceId: 'opaque-choice',
        fieldPath: 'identifier',
        label: 'Grouping key',
        description: '',
        occurrenceSummary: 'Root occurrence',
        routeSummary: 'Root',
        kind: 'FIELD_GROUP',
        valueType: 'STRING',
        policies: [{ name: 'missingKeyPolicy', options: ['ERROR'] }],
      }],
      explicitGroups: [{
        revisionId: 'grouprev_1',
        groupCount: 2,
        memberCount: 4,
        createdAt: '2026-09-20T00:00:00.000Z',
        unassignedMemberPolicies: ['ERROR', 'EXCLUDE', 'GROUP_AS_UNASSIGNED'],
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
        fieldPath: 'identifier',
        label: 'Grouping key',
        description: '',
        occurrenceSummary: 'Root occurrence',
        routeSummary: 'Root',
        kind: 'FIELD_GROUP',
        valueType: 'STRING',
        policies: [{ name: 'missingKeyPolicy', options: ['ERROR'] }],
        rawSchemaSelector: 'not-in-the-public-contract',
      }],
      explicitGroups: [],
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

  it('models contributor windows separately from first-ordered tie breaking', () => {
    const contributorWindow = {
      timestampPath: 'effectiveDateTime',
      anchorPath: 'meta.lastUpdated',
      lowerOffsetSeconds: -172_800,
      upperOffsetSeconds: 0,
      lowerInclusive: true,
      upperInclusive: false,
      precision: 'INSTANT',
    };
    const ordering = {
      timestampPath: 'effectiveDateTime',
      direction: 'DESC',
      tiePolicy: 'RESOURCE_KEY',
    };

    expect(explorerColumnSourceSchema.parse({
      kind: 'aggregate',
      aggregate: {
        operation: 'SUM',
        path: 'valueQuantity.value',
        contributorWindow,
        unitNormalization: { policyId: 'to-centimeters', version: '2' },
      },
    })).toEqual({
      kind: 'aggregate',
      aggregate: {
        operation: 'SUM',
        path: 'valueQuantity.value',
        contributorWindow,
        unitNormalization: { policyId: 'to-centimeters', version: '2' },
      },
    });
    expect(explorerColumnSourceSchema.parse({
      kind: 'aggregate',
      aggregate: { operation: 'COUNT', path: 'valueQuantity.value', contributorWindow },
    })).toEqual({
      kind: 'aggregate',
      aggregate: { operation: 'COUNT', path: 'valueQuantity.value', contributorWindow },
    });
    expect(explorerColumnSourceSchema.parse({
      kind: 'aggregate',
      aggregate: {
        operation: 'FIRST_ORDERED',
        path: 'valueQuantity.value',
        contributorWindow,
        ordering,
      },
    })).toEqual({
      kind: 'aggregate',
      aggregate: {
        operation: 'FIRST_ORDERED',
        path: 'valueQuantity.value',
        contributorWindow,
        ordering,
      },
    });
  });

  it('rejects aggregate fields that the selected operation cannot use', () => {
    const contributorWindow = {
      timestampPath: 'effectiveDateTime',
      anchorPath: 'meta.lastUpdated',
      lowerOffsetSeconds: -86_400,
      upperOffsetSeconds: 0,
      lowerInclusive: true,
      upperInclusive: true,
      precision: 'INSTANT',
    };
    const ordering = {
      timestampPath: 'effectiveDateTime',
      direction: 'DESC',
      tiePolicy: 'REQUIRE_UNIQUE',
    };
    const unitNormalization = { policyId: 'to-centimeters', version: '2' };

    for (const operation of ['COUNT', 'EXISTS'] as const) {
      expect(explorerColumnSourceSchema.safeParse({
        kind: 'aggregate',
        aggregate: { operation, path: 'valueQuantity.value', unitNormalization },
      }).success).toBe(false);
    }
    expect(explorerColumnSourceSchema.safeParse({
      kind: 'aggregate',
      aggregate: { operation: 'SUM', path: 'valueQuantity.value', ordering },
    }).success).toBe(false);
    for (const operation of [
      'MIN',
      'MAX',
      'SUM',
      'MEAN',
      'COUNT_DISTINCT',
      'DISTINCT_VALUES',
      'REQUIRE_ONE',
      'COLLECT',
    ] as const) {
      expect(explorerColumnSourceSchema.safeParse({
        kind: 'aggregate',
        aggregate: { operation },
      }).success).toBe(false);
    }
    for (const requiredValues of [undefined, [], ['  '], ['Tumor', 'Tumor']]) {
      expect(explorerColumnSourceSchema.safeParse({
        kind: 'aggregate',
        aggregate: { operation: 'CONTAINS_ALL', path: 'type.coding[].code', requiredValues },
      }).success).toBe(false);
    }
    for (const operation of [
      'COUNT',
      'EXISTS',
      'MIN',
      'MAX',
      'SUM',
      'MEAN',
      'COUNT_DISTINCT',
      'DISTINCT_VALUES',
      'REQUIRE_ONE',
      'COLLECT',
    ] as const) {
      expect(explorerColumnSourceSchema.safeParse({
        kind: 'aggregate',
        aggregate: { operation, path: 'valueQuantity.value', requiredValues: ['not-valid-here'] },
      }).success).toBe(false);
    }
    expect(explorerColumnSourceSchema.safeParse({
      kind: 'aggregate',
      aggregate: {
        operation: 'FIRST_ORDERED',
        path: 'valueQuantity.value',
        contributorWindow,
      },
    }).success).toBe(false);
    expect(explorerColumnSourceSchema.safeParse({
      kind: 'aggregate',
      aggregate: {
        operation: 'FIRST_ORDERED',
        path: 'valueQuantity.value',
        ordering,
      },
    }).success).toBe(false);
    expect(explorerColumnSourceSchema.safeParse({
      kind: 'aggregate',
      aggregate: {
        operation: 'SUM',
        path: 'valueQuantity.value',
        contributorWindow: { ...contributorWindow, unexpected: true },
      },
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

  it('keeps explicit-group authoring input closed and preserves overlaps and empty groups', () => {
    const request = {
      snapshotToken: 'snapshot-1',
      idempotencyKey: 'groups-1',
      groups: [
        { id: 'group-a', label: 'Alpha', ordinal: 0, memberIds: ['member-a', 'member-b'] },
        { id: 'group-b', label: 'Beta', ordinal: 1, memberIds: ['member-b'] },
        { id: 'group-empty', label: 'Empty', ordinal: 2, memberIds: [] },
      ],
    };
    expect(explicitGroupCreateRequestSchema.parse(request)).toEqual(request);
    expect(explicitGroupCreateRequestSchema.safeParse({
      ...request,
      groups: [{ ...request.groups[0], resourceType: 'Patient' }],
    }).success).toBe(false);
    expect(explicitGroupCreateRequestSchema.safeParse({
      ...request,
      groups: [{ id: 'group-a', label: 'Alpha', ordinal: 0 }],
    }).success).toBe(false);
    const summary = {
      revisionId: 'grouprev-1', sourceSelectionRevisionId: 'selection-1', groupCount: 1, memberCount: 0,
      createdAt: '2026-09-20T00:00:00.000Z',
      groups: [{ id: 'group-empty', label: 'Empty', ordinal: 0, memberCount: 0 }],
    };
    expect(explicitGroupRevisionSummarySchema.parse(summary)).toEqual(summary);
    expect(explicitGroupRevisionSummarySchema.safeParse({ ...summary, debugSeed: true }).success).toBe(false);
  });

  it('keeps table-shape scalars and comparison presence/value evidence lossless and strict', () => {
    const scalars = [
      { kind: 'INTEGER', integer: 0 },
      { kind: 'DECIMAL', decimal: 0 },
      { kind: 'BOOLEAN', boolean: false },
      { kind: 'STRING', string: '' },
    ] as const;
    scalars.forEach((value) => expect(tableShapeTaggedScalarSchema.parse(value)).toEqual(value));
    expect(tableShapeTaggedScalarSchema.safeParse({ kind: 'INTEGER', integer: 0, resourceType: 'Patient' }).success).toBe(false);

    const comparison = {
      status: 'AVAILABLE',
      base: { rowCount: 1, sampled: false },
      candidate: { rowCount: 1, sampled: false },
      changedColumns: ['measurement'],
      changedRowCount: 1,
      changedRowsSampled: false,
      changedRows: [{
        rowIdentity: 'row-1', basePresent: true, candidatePresent: true,
        changedColumns: ['measurement'],
        changedCells: [{
          column: 'measurement', before: { present: false, value: null }, after: { present: true, value: false },
          trace: { state: 'AVAILABLE', contributors: [{ resourceType: 'Observation', resourceId: 'obs-1', value: '' }], complete: true, sampled: false },
        }],
      }],
      contributors: [{ resourceType: 'Observation', resourceId: 'obs-1' }],
      contributorsSampled: false,
      exclusions: {
        status: 'INCOMPLETE',
        records: [
          {
            sourceIdentity: { resourceType: 'Patient', resourceId: 'patient-1' },
            category: { present: false, value: null }, categoryType: 'string',
            outputRowId: 'row-missing', reason: 'The category value was missing.', omissionCode: 'MISSING_CATEGORY',
          },
          {
            category: { present: true, value: null }, categoryType: 'null', outputRowId: 'row-null', reason: 'Null category.',
          },
          {
            category: { present: true, value: false }, categoryType: 'boolean', outputRowId: 'row-false', reason: 'False category.',
          },
          {
            category: { present: true, value: 0 }, categoryType: 'number', outputRowId: 'row-zero', reason: 'Zero category.',
          },
          {
            category: { present: true, value: '' }, categoryType: 'string', outputRowId: 'row-empty', reason: 'Empty category.',
          },
        ],
        complete: false,
        sampled: true,
      },
      declaredInformationLoss: {
        status: 'COMPLETE',
        items: [{
          code: 'DROPPED_COLUMNS', label: 'Dropped columns', detail: 'Columns were removed by the transformation.',
          affectedColumns: ['legacy_measure', 'old_status'],
        }],
      },
      evidenceLimitations: [{ code: 'SAMPLED_ROWS', message: 'Only sampled rows were compared.' }],
      notices: [],
    };
    expect(tableShapeComparisonSchema.parse(comparison)).toEqual(comparison);
    expect(tableShapeComparisonSchema.safeParse({ ...comparison, debug: true }).success).toBe(false);
    expect(tableShapeComparisonSchema.safeParse({
      ...comparison, evidenceLimitations: ['Only sampled rows were compared.'],
    }).success).toBe(false);
    expect(tableShapeComparisonSchema.safeParse({
      ...comparison,
      exclusions: { ...comparison.exclusions, records: [{ ...comparison.exclusions.records[0], debug: true }] },
    }).success).toBe(false);
    expect(tableShapeComparisonSchema.safeParse({
      ...comparison,
      declaredInformationLoss: {
        ...comparison.declaredInformationLoss,
        items: [{ ...comparison.declaredInformationLoss.items[0], affectedColumns: ['legacy_measure'], debug: true }],
      },
    }).success).toBe(false);
    expect(tableShapeComparisonSchema.safeParse({
      ...comparison,
      changedRows: [{ ...comparison.changedRows[0], changedCells: [{
        ...comparison.changedRows[0].changedCells[0], after: { present: true },
      }] }],
    }).success).toBe(false);

    const unavailable = {
      status: 'UNAVAILABLE',
      reason: 'The comparison could not be produced.',
      reasonCode: 'COMPARE_UNAVAILABLE',
      changedColumns: [], changedRowCount: 0, changedRowsSampled: false, changedRows: [],
      contributors: [], contributorsSampled: false,
      exclusions: { status: 'UNAVAILABLE', records: [], complete: false, sampled: false },
      declaredInformationLoss: { status: 'UNAVAILABLE', items: [] },
      evidenceLimitations: [{ code: 'NO_COMPARISON', message: 'No preview was returned.' }],
      notices: [],
    };
    expect(tableShapeComparisonSchema.parse(unavailable)).toEqual(unavailable);
    expect(tableShapeComparisonSchema.safeParse({ ...unavailable, exclusions: undefined }).success).toBe(false);
  });

  it('accepts only the opaque proposal receipt in the atomic Builder apply command', () => {
    expect(explorerBuilderCommandSchema.parse({
      type: 'APPLY_TABLE_SHAPE_PROPOSAL', outputId: 'table-1', proposalId: 'proposal-1',
    })).toEqual({ type: 'APPLY_TABLE_SHAPE_PROPOSAL', outputId: 'table-1', proposalId: 'proposal-1' });
    expect(explorerBuilderCommandSchema.safeParse({
      type: 'APPLY_TABLE_SHAPE_PROPOSAL', outputId: 'table-1', proposalId: 'proposal-1', tableShape: { columns: [] },
    }).success).toBe(false);
  });

  it('requires the exact catalog and ordered resolution receipt IDs for proposals', () => {
    const request = {
      snapshotToken: 'snapshot-1', expectedDraftVersion: 1, expectedDraftDigest: 'digest-1',
      outputId: 'table-1', mode: 'ADD', catalogId: 'catalog-1',
      reshapeResolutionId: 'pivot-resolution', derivedResolutionIds: ['derived-1', 'derived-2'],
    };
    expect(tableShapeProposalRequestSchema.parse(request)).toEqual(request);
    expect(tableShapeProposalRequestSchema.safeParse({ ...request, tableShape: {} }).success).toBe(false);
  });

  it('parses compiler-owned result units on Preview columns and preserves omission', () => {
    const unit = { system: 'http://unitsofmeasure.org', code: 'cm' };
    const unitColumn = {
      column: 'body_height', label: 'Body height', logicalType: 'decimal',
      resultUnit: unit, filterable: false, chartable: false,
    };
    const unitlessColumn = {
      column: 'patient_id', label: 'Patient ID', logicalType: 'string',
      filterable: false, chartable: false,
    };
    const response = {
      apiVersion: 'loom.calypr.org/explorer-authoring/v2',
      kind: 'ExplorerBuilderPreview',
      receiptId: 'receipt-1', outputId: 'patients',
      columns: [unitColumn, unitlessColumn],
      rows: [{ body_height: 180, patient_id: 'patient-1' }], rowCount: 1,
      rowSources: [{ kind: 'SINGLE', resourceType: 'Patient', id: 'patient-1' }],
      diagnostics: [],
    };

    const parsed = assertExplorerBuilderPreviewResult(response);
    expect(parsed.columns[0]?.resultUnit).toEqual(unit);
    expect(parsed.columns[1]).not.toHaveProperty('resultUnit');
    expect(parsed.rowSources?.[0]).toEqual({ kind: 'SINGLE', resourceType: 'Patient', id: 'patient-1' });
    expect(() => assertExplorerBuilderPreviewResult({ ...response, rowSources: [] })).toThrow();
    expect(() => assertExplorerBuilderPreviewResult({ ...response, rowSources: [{ kind: 'SINGLE', resourceType: 'Patient' }] })).toThrow();
    expect(() => assertExplorerBuilderPreviewResult({
      ...response,
      columns: [{ ...unitColumn, resultUnit: { ...unit, debug: true } }],
    })).toThrow();
  });

  it('parses compiler-owned result units on published runtime columns', () => {
    const unit = { system: 'http://unitsofmeasure.org', code: 'cm' };
    const runtimeState = {
      apiVersion: 'loom.calypr.org/explorer-state/v1', kind: 'ExplorerState',
      project: 'project-1', explorerId: 'explorer-1', title: 'Clinical data',
      management: 'interactive', active: {}, generated: {}, activeUrl: '/viewer',
      draft: { version: 1, digest: 'draft-1' },
      runtime: {
        outputs: [{
          outputId: 'patients', name: 'patients', title: 'Patients', rowLabel: 'patient',
          selector: { recipe: 'recipe', translationVersion: 'v1', output: 'patients' },
          columns: [
            { column: 'body_height', label: 'Body height', logicalType: 'decimal', resultUnit: unit, visible: true, order: 0, filterable: false, chartable: false },
            { column: 'patient_id', label: 'Patient ID', logicalType: 'string', visible: true, order: 1, filterable: false, chartable: false },
          ],
          table: { columns: [
            { column: 'body_height', visible: true },
            { column: 'patient_id', visible: true },
          ] },
          filters: [], charts: [], fixedFilters: {},
        }],
        sharedFilters: {}, diagnostics: [],
      },
    };

    const parsed = assertExplorerStateV1(runtimeState);
    const columns = parsed.runtime?.outputs[0]?.columns;
    expect(columns?.[0]?.resultUnit).toEqual(unit);
    expect(columns?.[1]).not.toHaveProperty('resultUnit');
    expect(() => assertExplorerStateV1({
      ...runtimeState,
      runtime: {
        ...runtimeState.runtime,
        outputs: [{
          ...runtimeState.runtime.outputs[0],
          columns: [{
            ...runtimeState.runtime.outputs[0].columns[0],
            resultUnit: { ...unit, debug: true },
          }],
        }],
      },
    })).toThrow();
  });
});
