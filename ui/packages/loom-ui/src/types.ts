import { z } from 'zod';
/** Selector identity for published dataframe reads. */
export interface DataframeSelector {
  readonly recipe: string;
  readonly translationVersion: string;
  readonly output: string;
}

export const EXPLORER_AUTHORING_API_VERSION =
  'loom.calypr.org/explorer-authoring/v2' as const;
export const EXPLORER_AUTHORING_SEMANTICS_VERSION = 9;

const opaqueIdSchema = z.string().trim().min(1);
const projectionModeSchema = z.enum([
  'VALUE',
  'INDEXED',
  'FIRST',
  'ALL',
  'DISTINCT',
]);
const unknownRecordSchema = z.record(z.string(), z.unknown());

export const correlatedBindingSchema = z.object({
  ownerPath: z.string().optional(),
  keyPath: opaqueIdSchema,
  systemPath: opaqueIdSchema,
  codePath: opaqueIdSchema,
  valuePath: opaqueIdSchema,
  valueFallback: z.array(opaqueIdSchema).optional(),
  choiceArms: z.array(opaqueIdSchema).optional(),
  logicalType: opaqueIdSchema,
  unitPath: z.string().optional(),
}).strict();

export const conceptCandidateSchema = z.object({
  sourceResourceType: opaqueIdSchema,
  sourceCanonical: z.string().optional(),
  sourceProfile: z.string().optional(),
  sourcePath: z.string().optional(),
  owningScope: z.string().optional(),
  extensionUrlPath: z.array(z.string()).optional(),
  keySelector: z.string().optional(),
  system: z.string().optional(),
  code: z.string().optional(),
  display: z.string().optional(),
  valueSelector: z.string().optional(),
  choiceArm: z.string().optional(),
  logicalType: z.string().optional(),
  observedUnits: z.array(z.string()).optional(),
  observedUnitsTruncated: z.boolean().optional(),
  completeness: opaqueIdSchema,
  status: opaqueIdSchema,
  population: z.number().int().nonnegative().safe(),
  examples: z.array(z.string()).optional(),
  examplesTruncated: z.boolean().optional(),
  ruleHint: z.string().optional(),
  ruleVersion: z.string().optional(),
}).strict();

const extensionBindingSchema = z.object({
  ownerPath: opaqueIdSchema,
  urlPath: z.array(opaqueIdSchema).min(1),
  valuePath: opaqueIdSchema,
  logicalType: opaqueIdSchema,
  valueFallback: z.array(opaqueIdSchema).optional(),
  choiceArms: z.array(opaqueIdSchema).optional(),
  unitPath: z.string().optional(),
}).strict();

const identifierBindingSchema = z.object({
  ownerPath: opaqueIdSchema,
  systemPath: opaqueIdSchema,
  valuePath: opaqueIdSchema,
  systemURI: opaqueIdSchema,
  logicalType: opaqueIdSchema,
}).strict();

export const explorerAuthoringDiagnosticSchema = z
  .object({
    severity: z.enum(['error', 'warning', 'info']),
    stage: z.string().optional(),
    code: opaqueIdSchema,
    path: z.string().nullable().optional(),
    fieldPath: z.string().nullable().optional(),
    message: z.string(),
    details: unknownRecordSchema.optional(),
    requestId: z.string().optional(),
  })
  .strict();
export type ExplorerAuthoringDiagnostic = z.infer<
  typeof explorerAuthoringDiagnosticSchema
>;

export const explorerTablePresentationSchema = z
  .object({
    visible: z.boolean().optional(),
    order: z.number().int().nonnegative().optional(),
    pinned: z.boolean().optional(),
    cellRenderer: z.literal('fileActions').optional(),
  })
  .strict();
export const explorerFilterPresentationSchema = z
  .object({
    label: z.string().optional(),
    order: z.number().int().nonnegative().optional(),
  })
  .strict();
export const explorerChartPresentationSchema = z
  .object({
    type: opaqueIdSchema,
    title: z.string().optional(),
    order: z.number().int().nonnegative().optional(),
  })
  .strict();
const fieldColumnSourceSchema = z
  .object({
    kind: z.literal('field'),
    field: z.object({
      path: opaqueIdSchema,
      projectionMode: projectionModeSchema.optional(),
      relatedSelection: z.object({
        kind: z.literal('first-by-resource-key'),
        acknowledged: z.boolean(),
      }).strict().optional(),
    }).strict(),
  }).strict();
const lookupColumnSourceSchema = z.union([
  z.object({
    kind: z.literal('identifierBySystem'),
    lookup: z.object({
      identifier: identifierBindingSchema,
      projectionMode: projectionModeSchema.optional(),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.literal('extensionByUrl'),
    lookup: z.object({
      extension: extensionBindingSchema,
      projectionMode: projectionModeSchema.optional(),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.enum([
      'identifierBySystem',
      'extensionByUrl',
    ]),
    lookup: z.object({
      match: opaqueIdSchema,
      path: opaqueIdSchema.optional(),
      projectionMode: projectionModeSchema.optional(),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.literal('codedValue'),
    lookup: z.object({
      binding: correlatedBindingSchema,
      key: z.object({ system: opaqueIdSchema, code: opaqueIdSchema }).strict(),
      projectionMode: projectionModeSchema.optional(),
    }).strict(),
  }).strict(),
]);
const ownerRecordsColumnSourceSchema = z.object({
  kind: z.literal('ownerRecords'),
  ownerRecords: z.object({
    binding: correlatedBindingSchema,
    key: z.object({ system: opaqueIdSchema, code: opaqueIdSchema }).strict(),
  }).strict(),
}).strict();
export const contributorWindowSchema = z.object({
  timestampPath: opaqueIdSchema,
  anchorPath: opaqueIdSchema,
  lowerOffsetSeconds: z.number().int(),
  upperOffsetSeconds: z.number().int(),
  lowerInclusive: z.boolean(),
  upperInclusive: z.boolean(),
  precision: z.literal('INSTANT'),
}).strict().refine(
  ({ lowerOffsetSeconds, upperOffsetSeconds }) => lowerOffsetSeconds <= upperOffsetSeconds,
  { message: 'lowerOffsetSeconds must not exceed upperOffsetSeconds' },
);
export type ContributorWindow = z.infer<typeof contributorWindowSchema>;
export const aggregateOrderingSchema = z.object({
  timestampPath: opaqueIdSchema,
  direction: z.enum(['ASC', 'DESC']),
  tiePolicy: z.enum(['REQUIRE_UNIQUE', 'RESOURCE_KEY']),
}).strict();
export type AggregateOrdering = z.infer<typeof aggregateOrderingSchema>;
const unitNormalizationSchema = z.object({
  policyId: opaqueIdSchema,
  version: opaqueIdSchema,
}).strict();
export const resultUnitSchema = z.object({
  system: opaqueIdSchema,
  code: opaqueIdSchema,
}).strict();
export type ResultUnit = z.infer<typeof resultUnitSchema>;
const aggregateColumnSourceSchema = z.object({
  kind: z.literal('aggregate'),
  aggregate: z.discriminatedUnion('operation', [
    z.object({
      operation: z.enum(['COUNT', 'EXISTS']),
      path: opaqueIdSchema.optional(),
      contributorWindow: contributorWindowSchema.optional(),
    }).strict(),
    z.object({
      operation: z.enum(['MIN', 'MAX', 'SUM', 'MEAN']),
      path: opaqueIdSchema,
      contributorWindow: contributorWindowSchema.optional(),
      unitNormalization: unitNormalizationSchema.optional(),
    }).strict(),
    z.object({
      operation: z.literal('COUNT_DISTINCT'),
      path: opaqueIdSchema,
    }).strict(),
    z.object({
      operation: z.literal('CONTAINS_ALL'),
      path: opaqueIdSchema,
      requiredValues: z.array(z.string().refine((value) => value.trim().length > 0)).min(1)
        .refine((values) => new Set(values).size === values.length),
    }).strict(),
    z.object({
      operation: z.enum(['DISTINCT_VALUES', 'REQUIRE_ONE', 'COLLECT']),
      path: opaqueIdSchema,
      unitNormalization: unitNormalizationSchema.optional(),
    }).strict(),
    z.object({
      operation: z.literal('FIRST_ORDERED'),
      path: opaqueIdSchema,
      contributorWindow: contributorWindowSchema,
      ordering: aggregateOrderingSchema,
      unitNormalization: unitNormalizationSchema.optional(),
    }).strict(),
  ]),
}).strict();
const contributorValueSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('STRING'), string: z.string() }).strict(),
  z.object({
    kind: z.literal('CODE'),
    code: z.object({ code: z.string().min(1) }).strict(),
  }).strict(),
]);
export const contributorPredicateSchema = z
  .object({
    candidateId: opaqueIdSchema,
    operator: z.enum(['EXISTS', 'EQUALS']),
    quantifier: z.literal('ANY').optional(),
    value: contributorValueSchema.optional(),
  })
  .strict();
export const explorerColumnSourceSchema = z.union([
  fieldColumnSourceSchema,
  lookupColumnSourceSchema,
  ownerRecordsColumnSourceSchema,
  aggregateColumnSourceSchema,
  z.object({ kind: z.literal('projectId') }).strict(),
]);
export type ExplorerColumnSource = z.infer<typeof explorerColumnSourceSchema>;
export const featureInterpretationSchema = z
  .object({
    kind: z.literal('PINNED'),
    pinned: z.object({ revisionId: opaqueIdSchema }).strict(),
  })
  .strict();
export type FeatureInterpretation = z.infer<typeof featureInterpretationSchema>;
export type ExplorerBuilderRouteNode = {
  occurrenceId: string;
  resourceType: string;
  catalogEdgeId?: string;
  relationship?: string;
  matchMode?: 'OPTIONAL' | 'REQUIRED';
  children?: ExplorerBuilderRouteNode[];
};
const explorerPopulationStepSchema = z.object({
  resourceType: opaqueIdSchema,
  relationship: opaqueIdSchema,
  catalogEdgeId: opaqueIdSchema.optional(),
}).strict();
export const explorerPopulationSchema = z.object({
  selectionRevisionId: opaqueIdSchema,
  route: z.array(explorerPopulationStepSchema),
}).strict();
export const explorerBuilderRouteNodeSchema: z.ZodType<ExplorerBuilderRouteNode> =
  z.lazy(() =>
    z
      .object({
        occurrenceId: opaqueIdSchema,
        resourceType: opaqueIdSchema,
        catalogEdgeId: opaqueIdSchema.optional(),
        relationship: z.string().optional(),
        matchMode: z.enum(['OPTIONAL', 'REQUIRED']).optional(),
        children: z.array(explorerBuilderRouteNodeSchema).optional(),
      })
      .strict(),
  );
const recordRowsSchema = z.object({}).strict();
const fieldGroupSourceSchema = z
  .object({
    occurrenceId: opaqueIdSchema,
    fieldPath: opaqueIdSchema,
    missingKeyPolicy: z.enum(['ERROR', 'EXCLUDE', 'GROUP_AS_MISSING']),
  })
  .strict();
const explicitGroupSourceSchema = z
  .object({
    revisionId: opaqueIdSchema,
    unassignedMemberPolicy: z.enum([
      'ERROR',
      'EXCLUDE',
      'GROUP_AS_UNASSIGNED',
    ]),
  })
  .strict();
const groupSourceSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('FIELD'), field: fieldGroupSourceSchema }).strict(),
  z
    .object({ kind: z.literal('EXPLICIT'), explicit: explicitGroupSourceSchema })
    .strict(),
]);
export const explorerRowDefinitionSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('RECORDS'), records: recordRowsSchema }).strict(),
  z
    .object({
      kind: z.literal('GROUPS'),
      groups: z.object({ source: groupSourceSchema }).strict(),
    })
    .strict(),
  z
    .object({
      kind: z.literal('EXPANDED'),
      expanded: z
        .object({
          occurrenceId: opaqueIdSchema,
          scopePath: opaqueIdSchema,
          emptyCollectionPolicy: z.enum([
            'ERROR',
            'EXCLUDE',
            'PRESERVE_PARENT',
          ]),
        })
        .strict(),
    })
    .strict(),
]);
export type ExplorerRowDefinition = z.infer<typeof explorerRowDefinitionSchema>;

const rowDefinitionChoiceFieldPolicySchema = z
  .object({
    name: z.literal('missingKeyPolicy'),
    options: z.array(z.enum(['ERROR', 'EXCLUDE', 'GROUP_AS_MISSING'])).min(1),
  })
  .strict();
const rowDefinitionChoiceExpandedPolicySchema = z
  .object({
    name: z.literal('emptyCollectionPolicy'),
    options: z.array(z.enum(['ERROR', 'EXCLUDE', 'PRESERVE_PARENT'])).min(1),
  })
  .strict();
const rowDefinitionChoiceBaseSchema = z.object({
  choiceId: opaqueIdSchema,
  label: z.string().min(1),
  description: z.string(),
  occurrenceSummary: z.string().min(1),
  routeSummary: z.string().min(1),
});
export const rowDefinitionChoiceSchema = z.discriminatedUnion('kind', [
  rowDefinitionChoiceBaseSchema.extend({
    kind: z.literal('FIELD_GROUP'),
    valueType: z.enum(['STRING', 'NUMBER', 'BOOLEAN']),
    policies: z.array(rowDefinitionChoiceFieldPolicySchema).length(1),
  }).strict(),
  rowDefinitionChoiceBaseSchema.extend({
    kind: z.literal('EXPANDED'),
    valueType: z.literal('ARRAY'),
    policies: z.array(rowDefinitionChoiceExpandedPolicySchema).length(1),
  }).strict(),
]);
export type RowDefinitionChoice = z.infer<typeof rowDefinitionChoiceSchema>;
export const explicitGroupRevisionChoiceSchema = z.object({
  revisionId: opaqueIdSchema,
  groupCount: z.number().int().positive().safe(),
  memberCount: z.number().int().nonnegative().safe(),
  createdAt: z.string().datetime(),
  unassignedMemberPolicies: z.array(z.enum(['ERROR', 'EXCLUDE', 'GROUP_AS_UNASSIGNED'])).min(1),
}).strict();
export type ExplicitGroupRevisionChoice = z.infer<typeof explicitGroupRevisionChoiceSchema>;
export const explicitGroupInputSchema = z.object({
  id: opaqueIdSchema,
  label: z.string().min(1).max(256),
  ordinal: z.number().int().nonnegative().safe(),
  memberIds: z.array(z.string().min(1).max(512)).max(100_000),
}).strict();
export const explicitGroupCreateRequestSchema = z.object({
  snapshotToken: opaqueIdSchema,
  idempotencyKey: z.string().min(1).max(256),
  groups: z.array(explicitGroupInputSchema).min(1).max(1000),
}).strict();
export type ExplicitGroupCreateRequest = z.infer<typeof explicitGroupCreateRequestSchema>;
export const explicitGroupSummarySchema = z.object({
  id: opaqueIdSchema,
  label: z.string().min(1).max(256),
  ordinal: z.number().int().nonnegative().safe(),
  memberCount: z.number().int().nonnegative().safe(),
}).strict();
export const explicitGroupRevisionSummarySchema = z.object({
  revisionId: opaqueIdSchema,
  sourceSelectionRevisionId: opaqueIdSchema,
  groupCount: z.number().int().positive().safe(),
  memberCount: z.number().int().nonnegative().safe(),
  createdAt: z.string().datetime(),
  groups: z.array(explicitGroupSummarySchema),
}).strict();
export type ExplicitGroupRevisionSummary = z.infer<typeof explicitGroupRevisionSummarySchema>;
export const rowDefinitionChoicesResponseSchema = z
  .object({
    snapshotToken: opaqueIdSchema,
    outputId: opaqueIdSchema,
    choices: z.array(rowDefinitionChoiceSchema),
    explicitGroups: z.array(explicitGroupRevisionChoiceSchema),
  })
  .strict();
export type RowDefinitionChoicesResponse = z.infer<typeof rowDefinitionChoicesResponseSchema>;

export const rowDefinitionSelectionSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('RECORDS') }).strict(),
  z.object({
    kind: z.literal('FIELD_GROUP'),
    fieldGroup: z.object({
      rowChoiceId: opaqueIdSchema,
      missingKeyPolicy: z.enum(['ERROR', 'EXCLUDE', 'GROUP_AS_MISSING']),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.literal('EXPLICIT_GROUP'),
    explicitGroup: z.object({
      revisionId: opaqueIdSchema,
      unassignedMemberPolicy: z.enum(['ERROR', 'EXCLUDE', 'GROUP_AS_UNASSIGNED']),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.literal('EXPANDED'),
    expanded: z.object({
      rowChoiceId: opaqueIdSchema,
      emptyCollectionPolicy: z.enum(['ERROR', 'EXCLUDE', 'PRESERVE_PARENT']),
    }).strict(),
  }).strict(),
]);
export type RowDefinitionSelection = z.infer<typeof rowDefinitionSelectionSchema>;
export const rowDefinitionPreviewSummarySchema = z.object({
  rowCount: z.number().int().nonnegative(),
  sampled: z.boolean(),
}).strict();
const rowDefinitionComparisonExampleSchema = z.object({
  rowIdentity: opaqueIdSchema,
  basePresent: z.boolean(),
  candidatePresent: z.boolean(),
}).strict();
const rowDefinitionComparisonCommonSchema = z.object({
  affectedColumns: z.array(opaqueIdSchema),
  notices: z.array(z.string()),
  examples: z.array(rowDefinitionComparisonExampleSchema).max(10),
});
export const rowDefinitionComparisonSchema = z.discriminatedUnion('status', [
  rowDefinitionComparisonCommonSchema.extend({
    status: z.literal('AVAILABLE'),
    base: rowDefinitionPreviewSummarySchema,
    candidate: rowDefinitionPreviewSummarySchema,
  }).strict(),
  rowDefinitionComparisonCommonSchema.extend({
    status: z.literal('UNAVAILABLE'),
    reasonCode: opaqueIdSchema,
    reason: z.string().min(1),
    base: rowDefinitionPreviewSummarySchema.optional(),
    candidate: rowDefinitionPreviewSummarySchema.optional(),
  }).strict(),
]);
export type RowDefinitionComparison = z.infer<typeof rowDefinitionComparisonSchema>;
export const rowDefinitionProposalSchema = z.object({
  proposalId: opaqueIdSchema.optional(),
  baseReceiptId: opaqueIdSchema,
  outputId: opaqueIdSchema,
  snapshotToken: opaqueIdSchema,
  draftVersion: z.number().int().positive(),
  draftDigest: opaqueIdSchema,
  baseDocumentDigest: opaqueIdSchema,
  candidateWorkspaceDigest: opaqueIdSchema,
  mode: z.enum(['RECORDS', 'FIELD_GROUP', 'EXPLICIT_GROUP', 'EXPANDED']),
  comparison: rowDefinitionComparisonSchema,
}).strict();
export type RowDefinitionProposal = z.infer<typeof rowDefinitionProposalSchema>;

const tableShapeChoiceAvailabilitySchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('supported') }).strict(),
  z.object({ kind: z.literal('unsupported'), reason: z.string().min(1) }).strict(),
]);
export type TableShapeChoiceAvailability = z.infer<typeof tableShapeChoiceAvailabilitySchema>;

export type TableShapeJSONValue =
  | null
  | boolean
  | number
  | string
  | ReadonlyArray<TableShapeJSONValue>
  | { readonly [key: string]: TableShapeJSONValue };

const tableShapeJSONValueSchema: z.ZodType<TableShapeJSONValue> = z.lazy(() => z.union([
  z.null(),
  z.boolean(),
  z.number(),
  z.string(),
  z.array(tableShapeJSONValueSchema),
  z.record(z.string(), tableShapeJSONValueSchema),
]));
const persistedTableShapeSchema = z.record(z.string(), tableShapeJSONValueSchema);

export const tableShapeTaggedScalarSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('BOOLEAN'), boolean: z.boolean() }).strict(),
  z.object({ kind: z.literal('DECIMAL'), decimal: z.number() }).strict(),
  z.object({ kind: z.literal('INTEGER'), integer: z.number().int() }).strict(),
  z.object({ kind: z.literal('STRING'), string: z.string() }).strict(),
]);
export type TableShapeTaggedScalar = z.infer<typeof tableShapeTaggedScalarSchema>;

const tableShapeOutputNameSchema = z.object({ column: z.string().min(1), label: z.string().min(1) }).strict();
const tableShapeColumnReferenceSchema = z.object({ kind: z.literal('column'), choiceId: opaqueIdSchema }).strict();
const tableShapeOperandReferenceSchema = z.object({ kind: z.literal('operand'), choiceId: opaqueIdSchema }).strict();
const tableShapePivotCategoryReferenceSchema = z.object({ kind: z.literal('pivotCategory'), choiceId: opaqueIdSchema }).strict();
const tableShapeReshapeModeReferenceSchema = z.object({ kind: z.literal('reshapeMode'), choiceId: opaqueIdSchema }).strict();
const tableShapeBinaryOperatorReferenceSchema = z.object({ kind: z.literal('binaryOperator'), choiceId: opaqueIdSchema }).strict();
const tableShapeDuplicatePolicyReferenceSchema = z.object({ kind: z.literal('duplicatePolicy'), choiceId: opaqueIdSchema }).strict();
const tableShapeMissingCellPolicyReferenceSchema = z.object({ kind: z.literal('missingCellPolicy'), choiceId: opaqueIdSchema }).strict();
const tableShapeUnlistedPolicyReferenceSchema = z.object({ kind: z.literal('unlistedCategoryPolicy'), choiceId: opaqueIdSchema }).strict();
const tableShapeUnpivotPolicyReferenceSchema = z.object({ kind: z.literal('unpivotNullRowPolicy'), choiceId: opaqueIdSchema }).strict();
const tableShapeMissingInputPolicyReferenceSchema = z.object({ kind: z.literal('missingInputPolicy'), choiceId: opaqueIdSchema }).strict();
const tableShapeDivisionByZeroPolicyReferenceSchema = z.object({ kind: z.literal('divisionByZeroPolicy'), choiceId: opaqueIdSchema }).strict();

const tableShapePivotOutputReferenceSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('group'), column: tableShapeColumnReferenceSchema }).strict(),
  z.object({ kind: z.literal('category'), category: tableShapePivotCategoryReferenceSchema }).strict(),
]);

const tableShapeDerivedOperandIntentSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('base'), reference: tableShapeOperandReferenceSchema }).strict(),
  z.object({ kind: z.literal('derived'), localId: opaqueIdSchema }).strict(),
  z.object({ kind: z.literal('pivotOutput'), reference: tableShapePivotOutputReferenceSchema }).strict(),
  z.object({ kind: z.literal('literal'), representation: z.enum(['integer', 'decimal']), text: z.string() }).strict(),
]);

const tableShapeDerivedColumnIntentSchema = z.object({
  divisionByZeroPolicy: tableShapeDivisionByZeroPolicyReferenceSchema.optional(),
  leftOperand: tableShapeDerivedOperandIntentSchema,
  localId: opaqueIdSchema,
  missingInputPolicy: tableShapeMissingInputPolicyReferenceSchema,
  operator: tableShapeBinaryOperatorReferenceSchema,
  output: tableShapeOutputNameSchema,
  rightOperand: tableShapeDerivedOperandIntentSchema,
}).strict();

const tableShapePivotProposalIntentSchema = z.object({
  categoryColumn: tableShapeColumnReferenceSchema,
  categoryDiscoveryIdentity: opaqueIdSchema,
  duplicatePolicy: tableShapeDuplicatePolicyReferenceSchema,
  groupColumns: z.array(tableShapeColumnReferenceSchema).min(1),
  includedCategories: z.array(z.object({
    category: tableShapePivotCategoryReferenceSchema,
    output: tableShapeOutputNameSchema,
  }).strict()).min(1),
  missingCellPolicy: tableShapeMissingCellPolicyReferenceSchema,
  unlistedCategoryPolicy: tableShapeUnlistedPolicyReferenceSchema,
  valueColumn: tableShapeColumnReferenceSchema,
}).strict();

const tableShapeUnpivotProposalIntentSchema = z.object({
  inputColumns: z.array(tableShapeColumnReferenceSchema).min(1),
  keyOutput: tableShapeOutputNameSchema,
  nullRowPolicy: tableShapeUnpivotPolicyReferenceSchema,
  valueOutput: tableShapeOutputNameSchema,
}).strict();

export const tableShapeProposalIntentSchema = z.discriminatedUnion('kind', [
  z.object({
    derivedColumns: z.array(tableShapeDerivedColumnIntentSchema),
    kind: z.literal('NONE'),
    reshapeMode: tableShapeReshapeModeReferenceSchema,
  }).strict(),
  z.object({
    derivedColumns: z.array(tableShapeDerivedColumnIntentSchema),
    kind: z.literal('GROUPED_PIVOT'),
    pivot: tableShapePivotProposalIntentSchema,
    reshapeMode: tableShapeReshapeModeReferenceSchema,
  }).strict(),
  z.object({
    derivedColumns: z.array(tableShapeDerivedColumnIntentSchema),
    kind: z.literal('UNPIVOT'),
    reshapeMode: tableShapeReshapeModeReferenceSchema,
    unpivot: tableShapeUnpivotProposalIntentSchema,
  }).strict(),
]);
export type TableShapeProposalIntent = z.infer<typeof tableShapeProposalIntentSchema>;

const tableShapeEditorChoiceSchema = z.object({
  availability: tableShapeChoiceAvailabilitySchema,
  choiceId: opaqueIdSchema,
  choiceKind: z.string().min(1),
  label: z.string().min(1),
}).strict();

const tableShapeColumnChoiceSchema = tableShapeEditorChoiceSchema.extend({ choiceKind: z.literal('column') }).strict();
const tableShapeOperandChoiceSchema = tableShapeEditorChoiceSchema.extend({ choiceKind: z.literal('operand') }).strict();
const tableShapePolicyChoiceSchema = <Kind extends string>(choiceKind: Kind) => tableShapeEditorChoiceSchema.extend({ choiceKind: z.literal(choiceKind) }).strict();
const tableShapeOutputSuggestionSchema = z.object({
  availability: tableShapeChoiceAvailabilitySchema,
  choiceId: opaqueIdSchema,
  choiceKind: z.enum(['derivedOutput', 'unpivotKeyOutput', 'unpivotValueOutput']),
  label: z.string().min(1),
  resultTypeLabel: z.string().min(1),
  suggestedOutput: tableShapeOutputNameSchema,
}).strict();
const tableShapeOutputSupportSchema = z.discriminatedUnion('kind', [
  z.object({
    kind: z.literal('supported'),
    resultTypeLabel: z.string().min(1),
    suggestions: z.array(tableShapeOutputSuggestionSchema),
  }).strict(),
  z.object({ kind: z.literal('unsupported'), reason: z.string().min(1) }).strict(),
]);
const tableShapeBinaryOperatorChoiceSchema = tableShapeEditorChoiceSchema.extend({
  choiceKind: z.literal('binaryOperator'),
  requiresDivisionByZeroPolicy: z.boolean(),
}).strict();
const tableShapeReshapeModeChoiceSchema = tableShapeEditorChoiceSchema.extend({
  choiceKind: z.literal('reshapeMode'),
  mode: z.enum(['NONE', 'GROUPED_PIVOT', 'UNPIVOT']),
}).strict();
const tableShapePivotCategoryChoiceSchema = tableShapeEditorChoiceSchema.extend({
  choiceKind: z.literal('pivotCategory'),
  suggestedOutput: tableShapeOutputNameSchema,
  value: tableShapeTaggedScalarSchema,
}).strict();
const tableShapePivotCategoryPairSchema = z.object({
  categoryColumn: tableShapeColumnReferenceSchema,
  valueColumn: tableShapeColumnReferenceSchema,
}).strict();

const tableShapePivotCategoryDiscoveryStateSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('not-requested') }).strict(),
  z.object({
    categories: z.array(tableShapePivotCategoryChoiceSchema),
    discoveryIdentity: opaqueIdSchema,
    kind: z.literal('complete'),
    pair: tableShapePivotCategoryPairSchema,
  }).strict(),
]);

export const tableShapeCapabilitiesSchema = z.object({
  binaryOperators: z.array(tableShapeBinaryOperatorChoiceSchema),
  catalogId: opaqueIdSchema,
  categoryColumns: z.array(tableShapeColumnChoiceSchema),
  derivedAvailability: tableShapeChoiceAvailabilitySchema,
  derivedOutputSuggestions: z.array(tableShapeOutputSuggestionSchema.extend({ choiceKind: z.literal('derivedOutput') }).strict()),
  divisionByZeroPolicies: z.array(tableShapePolicyChoiceSchema('divisionByZeroPolicy')),
  duplicatePolicies: z.array(tableShapePolicyChoiceSchema('duplicatePolicy')),
  groupColumns: z.array(tableShapeColumnChoiceSchema),
  missingCellPolicies: z.array(tableShapePolicyChoiceSchema('missingCellPolicy')),
  missingInputPolicies: z.array(tableShapePolicyChoiceSchema('missingInputPolicy')),
  operands: z.array(tableShapeOperandChoiceSchema),
  outputId: opaqueIdSchema,
  pivotCategoryDiscovery: tableShapePivotCategoryDiscoveryStateSchema,
  reshapeModes: z.array(tableShapeReshapeModeChoiceSchema),
  savedProposalAvailability: tableShapeChoiceAvailabilitySchema,
  savedProposalIntent: tableShapeProposalIntentSchema,
  unlistedCategoryPolicies: z.array(tableShapePolicyChoiceSchema('unlistedCategoryPolicy')),
  unpivotColumns: z.array(tableShapeColumnChoiceSchema),
  unpivotKeyOutput: tableShapeOutputSupportSchema,
  unpivotNullRowPolicies: z.array(tableShapePolicyChoiceSchema('unpivotNullRowPolicy')),
  unpivotValueOutput: tableShapeOutputSupportSchema,
  unpivotWithDerivedAvailability: tableShapeChoiceAvailabilitySchema,
  valueColumns: z.array(tableShapeColumnChoiceSchema),
}).strict();
export type TableShapeCapabilities = z.infer<typeof tableShapeCapabilitiesSchema>;

export const tableShapeCapabilitiesRequestSchema = z.object({
  expectedDraftDigest: opaqueIdSchema,
  expectedDraftVersion: z.number().int().positive(),
  outputId: opaqueIdSchema,
  snapshotToken: opaqueIdSchema,
}).strict();
export type TableShapeCapabilitiesRequest = z.infer<typeof tableShapeCapabilitiesRequestSchema>;

export const tableShapeCategoryDiscoverySchema = z.object({
  catalogId: opaqueIdSchema,
  categories: z.array(tableShapePivotCategoryChoiceSchema),
  discoveryIdentity: opaqueIdSchema,
  kind: z.literal('complete'),
  pair: tableShapePivotCategoryPairSchema,
}).strict();
export type TableShapeCategoryDiscovery = z.infer<typeof tableShapeCategoryDiscoverySchema>;

export const tableShapeCategoryDiscoveryRequestSchema = z.object({
  catalogId: opaqueIdSchema,
  categoryColumnChoiceId: opaqueIdSchema,
  expectedDraftDigest: opaqueIdSchema,
  expectedDraftVersion: z.number().int().positive(),
  outputId: opaqueIdSchema,
  snapshotToken: opaqueIdSchema,
  valueColumnChoiceId: opaqueIdSchema,
}).strict();
export type TableShapeCategoryDiscoveryRequest = z.infer<typeof tableShapeCategoryDiscoveryRequestSchema>;

export const tableShapeResolutionRequestSchema = z.discriminatedUnion('kind', [
  z.object({
    catalogId: opaqueIdSchema,
    expectedDraftDigest: opaqueIdSchema,
    expectedDraftVersion: z.number().int().positive(),
    kind: z.literal('PIVOT'),
    outputId: opaqueIdSchema,
    pivot: z.object({
      categories: z.array(z.object({ choiceId: opaqueIdSchema, outputColumn: z.string().min(1), outputLabel: z.string().min(1) }).strict()).min(1),
      categoryColumnChoiceId: opaqueIdSchema,
      categoryDiscoveryId: opaqueIdSchema,
      duplicatePolicyChoiceId: opaqueIdSchema,
      groupColumnChoiceIds: z.array(opaqueIdSchema).min(1),
      missingPolicyChoiceId: opaqueIdSchema,
      unlistedPolicyChoiceId: opaqueIdSchema,
      valueColumnChoiceId: opaqueIdSchema,
    }).strict(),
    snapshotToken: opaqueIdSchema,
  }).strict(),
  z.object({
    catalogId: opaqueIdSchema,
    expectedDraftDigest: opaqueIdSchema,
    expectedDraftVersion: z.number().int().positive(),
    kind: z.literal('UNPIVOT'),
    outputId: opaqueIdSchema,
    snapshotToken: opaqueIdSchema,
    unpivot: z.object({
      inputColumnChoiceIds: z.array(opaqueIdSchema).min(1),
      keyOutputColumn: z.string().min(1),
      keyOutputLabel: z.string().min(1),
      nullPolicyChoiceId: opaqueIdSchema,
      valueOutputColumn: z.string().min(1),
      valueOutputLabel: z.string().min(1),
    }).strict(),
  }).strict(),
  z.object({
    catalogId: opaqueIdSchema,
    derived: z.object({
      divisionByZeroPolicyChoiceId: opaqueIdSchema.optional(),
      left: z.discriminatedUnion('kind', [
        z.object({ kind: z.literal('CATALOG_CHOICE'), choiceId: opaqueIdSchema }).strict(),
        z.object({ kind: z.literal('RESOLUTION_OUTPUT'), resolutionId: opaqueIdSchema }).strict(),
        z.object({ kind: z.literal('LITERAL'), literal: tableShapeTaggedScalarSchema }).strict(),
      ]),
      missingPolicyChoiceId: opaqueIdSchema,
      operatorChoiceId: opaqueIdSchema,
      outputColumn: z.string().min(1),
      outputLabel: z.string().min(1),
      pivotResolutionId: opaqueIdSchema.optional(),
      right: z.discriminatedUnion('kind', [
        z.object({ kind: z.literal('CATALOG_CHOICE'), choiceId: opaqueIdSchema }).strict(),
        z.object({ kind: z.literal('RESOLUTION_OUTPUT'), resolutionId: opaqueIdSchema }).strict(),
        z.object({ kind: z.literal('LITERAL'), literal: tableShapeTaggedScalarSchema }).strict(),
      ]),
    }).strict(),
    expectedDraftDigest: opaqueIdSchema,
    expectedDraftVersion: z.number().int().positive(),
    kind: z.literal('DERIVED'),
    outputId: opaqueIdSchema,
    snapshotToken: opaqueIdSchema,
  }).strict(),
]);
export type TableShapeResolutionRequest = z.infer<typeof tableShapeResolutionRequestSchema>;

const tableShapeTypeFactSchema = z.object({
  logicalType: z.string().min(1),
  nullable: z.boolean(),
  unitIdentity: z.string().optional(),
}).strict();
const tableShapeResolvedCategorySchema = z.object({
  id: opaqueIdSchema,
  outputColumn: z.string().min(1),
  outputLabel: z.string().min(1),
  type: tableShapeTypeFactSchema,
  value: tableShapeTaggedScalarSchema,
}).strict();
const tableShapeResolvedOutputDescriptorSchema = z.discriminatedUnion('kind', [
  z.object({
    groupColumn: tableShapeColumnReferenceSchema,
    kind: z.literal('group'),
    operandChoiceId: opaqueIdSchema.optional(),
    outputColumn: z.string().min(1),
    outputLabel: z.string().min(1),
    type: tableShapeTypeFactSchema,
  }).strict(),
  z.object({
    category: tableShapePivotCategoryReferenceSchema,
    kind: z.literal('category'),
    operandChoiceId: opaqueIdSchema.optional(),
    outputColumn: z.string().min(1),
    outputLabel: z.string().min(1),
    type: tableShapeTypeFactSchema,
  }).strict(),
  z.object({
    kind: z.literal('unpivotKey'),
    outputColumn: z.string().min(1),
    outputLabel: z.string().min(1),
    type: tableShapeTypeFactSchema,
  }).strict(),
  z.object({
    kind: z.literal('unpivotValue'),
    outputColumn: z.string().min(1),
    outputLabel: z.string().min(1),
    type: tableShapeTypeFactSchema,
  }).strict(),
  z.object({
    kind: z.literal('derived'),
    outputColumn: z.string().min(1),
    outputLabel: z.string().min(1),
    type: tableShapeTypeFactSchema,
  }).strict(),
]);

export const tableShapeResolutionSchema = z.object({
  catalogId: opaqueIdSchema,
  categories: z.array(tableShapeResolvedCategorySchema).optional(),
  categoryDiscoveryId: opaqueIdSchema.optional(),
  keyResult: tableShapeTypeFactSchema.optional(),
  kind: z.enum(['PIVOT', 'UNPIVOT', 'DERIVED']),
  outputDescriptors: z.array(tableShapeResolvedOutputDescriptorSchema),
  postPivotOperands: z.array(tableShapeOperandChoiceSchema),
  resolutionId: opaqueIdSchema,
  result: tableShapeTypeFactSchema.optional(),
  valueResult: tableShapeTypeFactSchema.optional(),
}).strict();
export type TableShapeResolution = z.infer<typeof tableShapeResolutionSchema>;

const tableShapeCellContributorSchema = z.object({
  resourceId: opaqueIdSchema,
  resourceType: z.string().min(1),
  value: tableShapeJSONValueSchema,
}).strict();
const tableShapeContributorSchema = z.object({
  resourceId: opaqueIdSchema,
  resourceType: z.string().min(1),
}).strict();
const tableShapeTraceSchema = z.object({
  cellStatus: z.string().optional(),
  complete: z.boolean(),
  contributors: z.array(tableShapeCellContributorSchema),
  failureCode: z.string().optional(),
  omissionCode: z.string().optional(),
  sampled: z.boolean(),
  state: z.enum(['NOT_APPLICABLE', 'NOT_REQUESTED', 'UNAVAILABLE', 'FAILED', 'AVAILABLE']),
}).strict();
const tableShapeCellValueSchema = z.object({
  present: z.boolean(),
  value: tableShapeJSONValueSchema,
}).strict();
const tableShapeChangedCellSchema = z.object({
  after: tableShapeCellValueSchema,
  before: tableShapeCellValueSchema,
  column: opaqueIdSchema,
  trace: tableShapeTraceSchema,
}).strict();
const tableShapeChangedRowSchema = z.object({
  basePresent: z.boolean(),
  candidatePresent: z.boolean(),
  changedCells: z.array(tableShapeChangedCellSchema),
  changedColumns: z.array(opaqueIdSchema),
  rowIdentity: opaqueIdSchema,
}).strict();
const tableShapePreviewSummarySchema = z.object({
  rowCount: z.number().int().nonnegative(),
  sampled: z.boolean(),
}).strict();
const tableShapeExcludedRecordSchema = z.object({
  sourceIdentity: z.object({
    resourceId: z.string(),
    resourceType: z.string(),
  }).strict().optional(),
  category: z.object({
    present: z.boolean(),
    value: tableShapeJSONValueSchema,
  }).strict(),
  categoryType: z.string(),
  outputRowId: z.string(),
  reason: z.string(),
  omissionCode: z.string().optional(),
}).strict();
const tableShapeExclusionsSchema = z.object({
  status: z.enum(['COMPLETE', 'INCOMPLETE', 'UNAVAILABLE']),
  records: z.array(tableShapeExcludedRecordSchema),
  complete: z.boolean(),
  sampled: z.boolean(),
  failureCode: z.string().optional(),
}).strict();
const tableShapeDeclaredInformationLossSchema = z.object({
  status: z.enum(['COMPLETE', 'UNAVAILABLE']),
  items: z.array(z.object({
    code: z.string(),
    label: z.string(),
    detail: z.string(),
    affectedColumns: z.array(z.string()).optional(),
  }).strict()),
  failureCode: z.string().optional(),
}).strict();
const tableShapeEvidenceLimitationSchema = z.object({
  code: z.string(),
  message: z.string(),
}).strict();
const tableShapeComparisonCommonSchema = z.object({
  changedColumns: z.array(opaqueIdSchema),
  changedRowCount: z.number().int().nonnegative(),
  changedRows: z.array(tableShapeChangedRowSchema),
  changedRowsSampled: z.boolean(),
  contributors: z.array(tableShapeContributorSchema),
  contributorsSampled: z.boolean(),
  exclusions: tableShapeExclusionsSchema,
  declaredInformationLoss: tableShapeDeclaredInformationLossSchema,
  evidenceLimitations: z.array(tableShapeEvidenceLimitationSchema),
  notices: z.array(z.string()),
});
export const tableShapeComparisonSchema = z.discriminatedUnion('status', [
  tableShapeComparisonCommonSchema.extend({
    base: tableShapePreviewSummarySchema,
    candidate: tableShapePreviewSummarySchema,
    status: z.literal('AVAILABLE'),
  }).strict(),
  tableShapeComparisonCommonSchema.extend({
    base: tableShapePreviewSummarySchema.optional(),
    candidate: tableShapePreviewSummarySchema.optional(),
    reason: z.string().min(1),
    reasonCode: opaqueIdSchema,
    status: z.literal('UNAVAILABLE'),
  }).strict(),
]);
export type TableShapeComparison = z.infer<typeof tableShapeComparisonSchema>;

export const tableShapeProposalSchema = z.object({
  baseDocumentDigest: opaqueIdSchema,
  baseReceiptId: opaqueIdSchema,
  candidateWorkspaceDigest: opaqueIdSchema,
  comparison: tableShapeComparisonSchema,
  draftDigest: opaqueIdSchema,
  draftVersion: z.number().int().positive(),
  mode: z.enum(['ADD', 'REPLACE', 'REMOVE']),
  outputId: opaqueIdSchema,
  proposalId: opaqueIdSchema.optional(),
  snapshotToken: opaqueIdSchema,
}).strict();
export type TableShapeProposal = z.infer<typeof tableShapeProposalSchema>;

export const tableShapeProposalRequestSchema = z.object({
  catalogId: opaqueIdSchema,
  derivedResolutionIds: z.array(opaqueIdSchema),
  expectedDraftDigest: opaqueIdSchema,
  expectedDraftVersion: z.number().int().positive(),
  limit: z.number().int().nonnegative().optional(),
  mode: z.enum(['ADD', 'REPLACE', 'REMOVE']),
  outputId: opaqueIdSchema,
  reshapeResolutionId: opaqueIdSchema.optional(),
  snapshotToken: opaqueIdSchema,
}).strict();
export type TableShapeProposalRequest = z.infer<typeof tableShapeProposalRequestSchema>;

const exactCategoryMappingSchema = z
  .object({ from: z.string(), to: z.string() })
  .strict();
export const columnValueTransformationSchema = z.discriminatedUnion('kind', [
  z
    .object({
      kind: z.literal('EXACT_CATEGORY_RECODE'),
      exactCategoryRecode: z
        .object({
          mappings: z.array(exactCategoryMappingSchema).min(1),
          unknownPolicy: z.enum(['ERROR', 'KEEP_ORIGINAL']),
        })
        .strict(),
    })
    .strict(),
]);
export type ColumnValueTransformation = z.infer<
  typeof columnValueTransformationSchema
>;

export const columnTransformationChangeSchema = z.discriminatedUnion('kind', [
  z
    .object({
      kind: z.literal('SET'),
      transformation: columnValueTransformationSchema,
    })
    .strict(),
  z.object({ kind: z.literal('REMOVE') }).strict(),
]);
export type ColumnTransformationChange = z.infer<
  typeof columnTransformationChangeSchema
>;

export const explorerBuilderColumnSchema = z
  .object({
    columnId: opaqueIdSchema.optional(),
    column: opaqueIdSchema.regex(/^[A-Za-z_][A-Za-z0-9_]*$/),
    label: z.string().min(1),
    logicalType: z.string().optional(),
    occurrenceId: opaqueIdSchema,
    source: explorerColumnSourceSchema,
    valueTransformation: columnValueTransformationSchema.optional(),
    contributor: contributorPredicateSchema.optional(),
    interpretation: featureInterpretationSchema.optional(),
    table: explorerTablePresentationSchema.optional(),
    filter: explorerFilterPresentationSchema.optional(),
    chart: explorerChartPresentationSchema.optional(),
  })
  .strict();
export type ExplorerBuilderColumn = z.infer<typeof explorerBuilderColumnSchema>;

export const explorerColumnSourceDescriptorSchema = z
  .object({
    snapshotToken: opaqueIdSchema,
    outputId: opaqueIdSchema,
    column: opaqueIdSchema,
    summary: z.string().min(1),
    facts: z.array(
      z.object({
        label: z.string().min(1),
        value: z.string(),
      }).strict(),
    ),
    route: z.array(
      z.object({
        occurrenceId: opaqueIdSchema,
        resourceType: opaqueIdSchema,
        catalogEdgeId: opaqueIdSchema.optional(),
        relationship: z.string().optional(),
        storageDirection: z.enum(['INBOUND', 'OUTBOUND']).optional(),
        matchMode: z.enum(['OPTIONAL', 'REQUIRED']).optional(),
      }).strict(),
    ).min(1),
  })
  .strict();
export type ExplorerColumnSourceDescriptor = z.infer<
  typeof explorerColumnSourceDescriptorSchema
>;

const constructionTableScalarSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('STRING'), string: z.string() }).strict(),
  z.object({ kind: z.literal('INTEGER'), integer: z.number().int() }).strict(),
  z.object({ kind: z.literal('DECIMAL'), decimal: z.number() }).strict(),
  z.object({ kind: z.literal('BOOLEAN'), boolean: z.boolean() }).strict(),
  z.object({ kind: z.literal('NULL') }).strict(),
  z.object({ kind: z.literal('MISSING') }).strict(),
]);
export type ConstructionTableScalar = z.infer<typeof constructionTableScalarSchema>;

const constructionInputRefSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('SOURCE_PROJECTION') }).strict(),
  z.object({ kind: z.literal('STEP_OUTPUT'), stepId: opaqueIdSchema }).strict(),
  z.object({
    kind: z.literal('TABLE_REVISION'),
    tableId: opaqueIdSchema,
    revisionId: opaqueIdSchema,
    outputId: opaqueIdSchema,
  }).strict(),
]);
export type ConstructionInputRef = z.infer<typeof constructionInputRefSchema>;

const constructionInputColumnSchema = z.object({
  id: opaqueIdSchema,
  name: z.string().min(1),
  label: z.string().min(1),
  type: z.string().min(1),
  clickhouseType: z.string().min(1),
  nullable: z.boolean(),
  repeated: z.boolean(),
  semanticPath: z.string().optional(),
}).strict();
export type ConstructionInputColumn = z.infer<typeof constructionInputColumnSchema>;

const constructionInputRevisionSchema = z.object({
  kind: z.literal('TABLE_REVISION'),
  tableId: opaqueIdSchema,
  revisionId: opaqueIdSchema,
  outputId: opaqueIdSchema,
  tableTitle: z.string().min(1),
  outputTitle: z.string().min(1),
  rowMeaning: z.string().min(1),
  isCurrent: z.boolean(),
  createdAt: z.string().datetime(),
  columns: z.array(constructionInputColumnSchema),
}).strict();
export type ConstructionInputRevision = z.infer<typeof constructionInputRevisionSchema>;

export const constructionInputsRequestSchema = z.object({
  snapshotToken: opaqueIdSchema,
  expectedDraftVersion: z.number().int().positive(),
  expectedDraftDigest: z.string().min(1),
  query: z.string().min(1).optional(),
  cursor: z.string().min(1).optional(),
  limit: z.number().int().min(1).max(100).optional(),
}).strict();
export type ConstructionInputsRequest = z.infer<typeof constructionInputsRequestSchema>;

export const constructionInputsResponseSchema = z.object({
  snapshotToken: opaqueIdSchema,
  draftVersion: z.number().int().positive(),
  draftDigest: z.string().min(1),
  datasetGeneration: z.string().min(1),
  entries: z.array(constructionInputRevisionSchema),
  nextCursor: z.string().min(1).optional(),
}).strict();
export type ConstructionInputsResponse = z.infer<typeof constructionInputsResponseSchema>;

const constructionStageColumnBaseSchema = z.object({
  id: opaqueIdSchema,
  name: z.string().min(1),
  label: z.string().min(1),
  type: z.string().optional(),
}).strict();

const constructionStageColumnSchema = constructionStageColumnBaseSchema.extend({
  nullable: z.boolean().optional(),
}).strict();
export type ConstructionStageColumn = z.infer<typeof constructionStageColumnSchema>;

const constructionStageColumnDescriptorSchema = constructionStageColumnBaseSchema.extend({
  cardinality: z.enum(['required_one', 'optional_one', 'many']).optional(),
}).strict();
export type ConstructionStageColumnDescriptor = z.infer<typeof constructionStageColumnDescriptorSchema>;

const constructionFilterValueSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('STRING'), string: z.string() }).strict(),
  z.object({ kind: z.literal('CODE'), code: z.object({
    system: z.string().optional(), code: z.string().min(1), display: z.string().optional(),
  }).strict() }).strict(),
  z.object({ kind: z.literal('BOOLEAN'), boolean: z.boolean() }).strict(),
  z.object({ kind: z.literal('INTEGER'), integer: z.number().int() }).strict(),
  z.object({ kind: z.literal('DECIMAL'), decimal: z.number() }).strict(),
  z.object({ kind: z.literal('DATE'), date: z.string() }).strict(),
  z.object({ kind: z.literal('DATE_TIME'), dateTime: z.string().datetime() }).strict(),
]);

const constructionOperandSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('COLUMN'), columnId: opaqueIdSchema }).strict(),
  z.object({
    kind: z.literal('LITERAL'),
    literal: z.discriminatedUnion('kind', [
      z.object({ kind: z.literal('INTEGER'), integer: z.number().int() }).strict(),
      z.object({ kind: z.literal('DECIMAL'), decimal: z.number() }).strict(),
    ]),
  }).strict(),
]);

const constructionCombineKeySchema = z.object({
  leftColumnId: opaqueIdSchema,
  rightColumnId: opaqueIdSchema,
}).strict();
export type ConstructionCombineKey = z.infer<typeof constructionCombineKeySchema>;

const constructionCombineProjectionSchema = z.object({
  outputColumnId: opaqueIdSchema,
  inputIndex: z.number().int().nonnegative(),
  inputColumnId: opaqueIdSchema,
}).strict();
export type ConstructionCombineProjection = z.infer<typeof constructionCombineProjectionSchema>;

const constructionCombineSchema = z.object({
  kind: z.enum(['KEY_JOIN', 'APPEND', 'MEMBERSHIP']),
  keys: z.array(constructionCombineKeySchema).min(1).optional(),
  projections: z.array(constructionCombineProjectionSchema).min(1),
  joinType: z.enum(['INNER', 'LEFT']).optional(),
  rightMatchPolicy: z.literal('PRESERVE_ALL').optional(),
  membershipMode: z.enum(['INCLUDE', 'EXCLUDE']).optional(),
}).strict();
export type ConstructionCombine = z.infer<typeof constructionCombineSchema>;

const constructionRouteStepSchema = z.object({
  edgeId: opaqueIdSchema,
  fromNodeId: opaqueIdSchema,
  toNodeId: opaqueIdSchema,
  fromResourceType: opaqueIdSchema,
  toResourceType: opaqueIdSchema,
  relationship: opaqueIdSchema,
  storageDirection: z.enum(['INBOUND', 'OUTBOUND']),
  matchMode: z.enum(['OPTIONAL', 'REQUIRED']),
}).strict();
export type ConstructionRouteStep = z.infer<typeof constructionRouteStepSchema>;

const relatedSourceSchema = z.object({
  anchorColumnId: opaqueIdSchema,
  choiceId: z.string().min(1),
  sourceOccurrenceId: opaqueIdSchema,
  source: z.object({
    kind: z.literal('FIELD'),
    candidateId: opaqueIdSchema,
    nodeId: opaqueIdSchema,
    resourceType: opaqueIdSchema,
    path: opaqueIdSchema,
    cardinality: z.enum(['optional_one', 'required_one']),
    logicalType: opaqueIdSchema,
  }).strict(),
  route: z.array(constructionRouteStepSchema),
  contributorRule: z.object({ policy: z.literal('ALL_MATCHES') }).strict(),
  form: z.literal('ALL'),
  outputColumnId: opaqueIdSchema,
}).strict();

const constructionOperationSchema = z.discriminatedUnion('kind', [
  z.object({
    kind: z.literal('PIVOT'),
    pivot: z.object({
      constructionId: opaqueIdSchema,
      groupKeyIds: z.array(opaqueIdSchema),
      categoryColumnId: opaqueIdSchema,
      valueColumnId: opaqueIdSchema,
      categories: z.array(z.object({
        key: constructionTableScalarSchema,
        outputColumnId: opaqueIdSchema,
      }).strict()),
      duplicatePolicy: z.enum(['ERROR', 'SUM', 'MIN', 'MAX']),
      missingCellPolicy: z.enum(['NULL', 'ERROR']),
      unlistedCategoryPolicy: z.enum(['ERROR', 'EXCLUDE_WITH_EVIDENCE']),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.literal('DERIVE'),
    derive: z.object({
      constructionId: opaqueIdSchema,
      outputColumnId: opaqueIdSchema,
      operation: z.enum(['ADD', 'SUBTRACT', 'MULTIPLY', 'DIVIDE']),
      left: constructionOperandSchema,
      right: constructionOperandSchema,
      missingInputPolicy: z.enum(['PROPAGATE_NULL', 'ERROR']),
      divisionByZeroPolicy: z.enum(['NULL', 'ERROR']).optional(),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.literal('FILTER'),
    filter: z.object({
      columnId: opaqueIdSchema,
      operator: z.enum(['EQUALS', 'NOT_EQUALS', 'IN', 'EXISTS', 'MISSING', 'CONTAINS_TEXT', 'GT', 'GTE', 'LT', 'LTE']),
      values: z.array(constructionFilterValueSchema).optional(),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.literal('UNPIVOT'),
    unpivot: z.object({
      constructionId: opaqueIdSchema,
      inputs: z.array(z.object({ columnId: opaqueIdSchema, key: constructionTableScalarSchema }).strict()),
      keyOutputColumnId: opaqueIdSchema,
      valueOutputColumnId: opaqueIdSchema,
      nullRowPolicy: z.enum(['DROP', 'PRESERVE']),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.literal('GROUP'),
    group: z.object({
      constructionId: opaqueIdSchema,
      keys: z.array(z.object({
        inputColumnId: opaqueIdSchema,
        outputColumnId: opaqueIdSchema,
      }).strict()).optional(),
      aggregates: z.array(z.discriminatedUnion('operation', [
        z.object({
          operation: z.literal('COUNT_ROWS'),
          outputColumnId: opaqueIdSchema,
        }).strict(),
        z.object({
          operation: z.literal('COUNT_NON_NULL'),
          inputColumnId: opaqueIdSchema,
          outputColumnId: opaqueIdSchema,
        }).strict(),
        z.object({
          operation: z.literal('COUNT_DISTINCT'),
          inputColumnId: opaqueIdSchema,
          outputColumnId: opaqueIdSchema,
        }).strict(),
        z.object({
          operation: z.literal('SUM'),
          inputColumnId: opaqueIdSchema,
          outputColumnId: opaqueIdSchema,
        }).strict(),
        z.object({
          operation: z.literal('MEAN'),
          inputColumnId: opaqueIdSchema,
          outputColumnId: opaqueIdSchema,
        }).strict(),
      ])).optional(),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.literal('EXPAND'),
    expand: z.object({
      constructionId: opaqueIdSchema,
      inputColumnId: opaqueIdSchema,
      outputColumnId: opaqueIdSchema,
      ordinalColumnId: opaqueIdSchema.optional(),
      emptyPolicy: z.enum(['ERROR', 'EXCLUDE', 'PRESERVE_PARENT']).optional(),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.literal('COMBINE'),
    combine: constructionCombineSchema,
  }).strict(),
  z.object({
    kind: z.literal('RELATED_SOURCE'),
    relatedSource: relatedSourceSchema,
  }).strict(),
]);
export type ConstructionOperation = z.infer<typeof constructionOperationSchema>;

const constructionStepSchema = z.object({
  id: opaqueIdSchema,
  inputs: z.array(constructionInputRefSchema),
  operation: constructionOperationSchema,
  outputs: z.array(constructionStageColumnSchema),
}).strict();
export type ConstructionStep = z.infer<typeof constructionStepSchema>;

export const constructionSchema = z.object({
  version: z.number().int().positive(),
  steps: z.array(constructionStepSchema),
}).strict();
export type Construction = z.infer<typeof constructionSchema>;

const constructionOperationCapabilitySchema = z.object({
  kind: z.enum(['PIVOT', 'DERIVE', 'FILTER', 'UNPIVOT', 'GROUP', 'EXPAND', 'RELATED_SOURCE']),
  supported: z.boolean(),
  reasonCode: z.string().optional(),
  reason: z.string().optional(),
}).strict();
const constructionStageDescriptorSchema = z.object({
  id: opaqueIdSchema,
  inputStageId: z.string(),
  operation: z.string().optional(),
  rowIdentityColumn: z.string().optional(),
  columns: z.array(constructionStageColumnDescriptorSchema),
  capabilities: z.array(constructionOperationCapabilitySchema),
}).strict();
export type ConstructionStageDescriptor = z.infer<typeof constructionStageDescriptorSchema>;

const constructionDependencyIssueSchema = z.object({
  stepId: opaqueIdSchema,
  columnId: opaqueIdSchema,
}).strict();
const constructionDependencyImpactSchema = z.object({
  changedStepId: opaqueIdSchema.optional(),
  removedStepIds: z.array(opaqueIdSchema).optional(),
  affectedStepIds: z.array(opaqueIdSchema),
  missingInputs: z.array(constructionDependencyIssueSchema).optional(),
}).strict();
export type ConstructionDependencyImpact = z.infer<typeof constructionDependencyImpactSchema>;

export const constructionCapabilitiesRequestSchema = z.object({
  snapshotToken: opaqueIdSchema,
  expectedDraftVersion: z.number().int().positive(),
  expectedDraftDigest: z.string().min(1),
  outputId: opaqueIdSchema,
  stageId: opaqueIdSchema,
}).strict();
export type ConstructionCapabilitiesRequest = z.infer<typeof constructionCapabilitiesRequestSchema>;

export const constructionCapabilitiesResponseSchema = z.object({
  snapshotToken: opaqueIdSchema,
  draftVersion: z.number().int().positive(),
  draftDigest: z.string().min(1),
  outputId: opaqueIdSchema,
  stageId: opaqueIdSchema,
  baseConstruction: constructionSchema,
  stages: z.array(constructionStageDescriptorSchema),
  selectedStage: constructionStageDescriptorSchema,
}).strict();
export type ConstructionCapabilitiesResponse = z.infer<typeof constructionCapabilitiesResponseSchema>;

export const constructionProposalRequestSchema = z.object({
  snapshotToken: opaqueIdSchema,
  expectedDraftVersion: z.number().int().positive(),
  expectedDraftDigest: z.string().min(1),
  outputId: opaqueIdSchema,
  changedStepId: z.string().optional(),
  removeStepIds: z.array(opaqueIdSchema).optional(),
  candidateConstruction: constructionSchema,
  limit: z.number().int().min(1).max(1000).optional(),
}).strict();
export type ConstructionProposalRequest = z.infer<typeof constructionProposalRequestSchema>;

export const constructionProposalResponseSchema = z.object({
  proposalId: opaqueIdSchema.optional(),
  baseReceiptId: opaqueIdSchema.optional(),
  outputId: opaqueIdSchema,
  snapshotToken: opaqueIdSchema,
  draftVersion: z.number().int().positive(),
  draftDigest: z.string().min(1),
  baseDocumentDigest: z.string().min(1),
  candidateWorkspaceDigest: z.string().min(1),
  changedStepId: z.string(),
  candidateConstruction: constructionSchema,
  dependencyImpact: constructionDependencyImpactSchema,
  stages: z.array(constructionStageDescriptorSchema),
  previewStatus: z.enum(['READY', 'NEEDS_REPAIR']),
  previewDurationMs: z.number().int().nonnegative(),
  preview: z.lazy(() => explorerBuilderPreviewResultSchema).optional(),
}).strict();
export type ConstructionProposalResponse = z.infer<typeof constructionProposalResponseSchema>;

export const explorerBuilderDocumentSchema = z
  .object({
    kind: z.literal('ExplorerBuilderDocument'),
    output: z
      .object({
        id: opaqueIdSchema.regex(/^[A-Za-z_][A-Za-z0-9_]*$/),
        title: z.string().min(1),
        rowLabel: z.string().optional(),
      })
      .strict(),
    rootResourceType: opaqueIdSchema,
    population: explorerPopulationSchema.optional(),
    route: explorerBuilderRouteNodeSchema,
    rows: explorerRowDefinitionSchema,
    columns: z.array(explorerBuilderColumnSchema),
    tableShape: persistedTableShapeSchema.optional(),
    construction: constructionSchema.optional(),
    fixedFilters: z
      .array(
        z
          .object({
            column: opaqueIdSchema,
            values: z.array(z.string()).min(1),
          })
          .strict(),
      )
      .optional(),
    actions: z
      .array(
        z
          .object({
            type: opaqueIdSchema,
            title: z.string().min(1),
            fileName: z.string().optional(),
            columns: z
              .array(
                z
                  .object({
                    column: opaqueIdSchema,
                    exportHeader: z.string().optional(),
                  })
                  .strict(),
              )
              .optional(),
          })
          .strict(),
      )
      .optional(),
  })
  .strict()
  .superRefine((document, context) => {
    if (document.construction && document.tableShape) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        message: 'Construction documents cannot also carry legacy tableShape.',
        path: ['tableShape'],
      });
    }
    if (document.construction) {
      const sourceColumnIds = new Set<string>();
      document.columns.forEach((column, index) => {
        if (!column.columnId) {
          context.addIssue({
            code: z.ZodIssueCode.custom,
            message: 'Construction documents require a stable columnId on every source column.',
            path: ['columns', index, 'columnId'],
          });
          return;
        }
        if (sourceColumnIds.has(column.columnId)) {
          context.addIssue({
            code: z.ZodIssueCode.custom,
            message: 'Construction source column IDs must be unique.',
            path: ['columns', index, 'columnId'],
          });
        }
        sourceColumnIds.add(column.columnId);
      });
    }
  });
export type ExplorerBuilderDocument = z.infer<
  typeof explorerBuilderDocumentSchema
>;

export const explorerBuilderTabSchema = z
  .object({
    id: opaqueIdSchema,
    title: z.string(),
    outputId: opaqueIdSchema,
    order: z.number().int().nonnegative(),
    visible: z.boolean().optional(),
  })
  .strict();
export const explorerBuilderWorkspaceSchema = z
  .object({
    apiVersion: z.literal(EXPLORER_AUTHORING_API_VERSION),
    kind: z.literal('ExplorerBuilderWorkspace'),
    semanticsVersion: z.number().int().positive().optional(),
    migrationDecisions: z.array(z.string()).optional(),
    explorer: z
      .object({ title: z.string().min(1), description: z.string().optional() })
      .strict(),
    documents: z.array(explorerBuilderDocumentSchema),
    tabs: z.array(explorerBuilderTabSchema),
    sharedFilters: z
      .record(
        z.string(),
        z.array(
          z
            .object({ outputId: opaqueIdSchema, column: opaqueIdSchema })
            .strict(),
        ),
      )
      .optional(),
    fileActions: z
      .object({
        extensions: z.record(z.string(), z.array(z.string())),
        actions: z.record(z.string(), z.string()),
      })
      .strict()
      .optional(),
  })
  .strict()
  .superRefine((workspace, context) => {
    const outputIds = workspace.documents.map((document) => document.output.id);
    const tabIds = workspace.tabs.map((tab) => tab.id);
    const tabOutputIds = workspace.tabs.map((tab) => tab.outputId);
    const duplicate = (values: ReadonlyArray<string>) =>
      values.find((value, index) => values.indexOf(value) !== index);
    if (duplicate(outputIds)) {
      context.addIssue({
        code: 'custom',
        path: ['documents'],
        message: 'Document output IDs must be unique.',
      });
    }
    if (duplicate(tabIds)) {
      context.addIssue({
        code: 'custom',
        path: ['tabs'],
        message: 'Tab IDs must be unique.',
      });
    }
    if (
      duplicate(tabOutputIds) ||
      outputIds.length !== tabOutputIds.length ||
      outputIds.some((outputId) => !tabOutputIds.includes(outputId))
    ) {
      context.addIssue({
        code: 'custom',
        path: ['tabs'],
        message: 'Tabs and document outputs must have a one-to-one mapping.',
      });
    }
  });
export type ExplorerBuilderWorkspace = z.infer<
  typeof explorerBuilderWorkspaceSchema
>;

export const explorerBuilderRoutePolicySchema = z
  .object({
    allowRepeatedEdges: z.boolean().optional(),
    allowSelfLoops: z.boolean().optional(),
    repeatedEdges: z.boolean().optional(),
    selfLoops: z.boolean().optional(),
    maxSteps: z.number().int().positive().nullable().optional(),
  })
  .strict();
export const explorerBuilderCatalogNodeSchema = z
  .object({
    nodeId: opaqueIdSchema,
    resourceType: opaqueIdSchema,
    rowRootEligible: z.boolean(),
    rowGrain: z.string().optional(),
    populated: z.boolean(),
    documentCount: z.number().int().nonnegative(),
  })
  .strict();
export const explorerBuilderCatalogEdgeSchema = z
  .object({
    edgeId: opaqueIdSchema,
    fromNodeId: opaqueIdSchema,
    toNodeId: opaqueIdSchema,
    label: z.string(),
    populated: z.boolean().optional(),
  })
  .strict();

export const constructionChoiceFormSchema = z.enum([
  'VALUE',
  'FIRST',
  'ALL',
  'DISTINCT',
  'OWNER_RECORDS',
]);
export type ConstructionChoiceForm = z.infer<typeof constructionChoiceFormSchema>;

export const constructionChoiceOptionSchema = z
  .object({
    form: constructionChoiceFormSchema,
    shape: z.enum(['SCALAR', 'LIST']),
    decision: z.enum(['DEFAULT', 'REQUIRES_DECISION']),
    preservation: z.enum(['PRESERVING', 'REDUCING']),
    rowEffect: z.literal('PRESERVES_ROW_GRAIN'),
    support: z.literal('SUPPORTED'),
    reason: z.string().min(1),
  })
  .strict();
export type ConstructionChoiceOption = z.infer<
  typeof constructionChoiceOptionSchema
>;

const constructionChoiceRepeatedBoundarySchema = z
  .object({
    path: z.string().min(1),
    maxItems: z.number().int().nonnegative(),
  })
  .strict();

export const fieldChoiceSourceSchema = z
  .object({
    kind: z.literal('FIELD'),
    candidateId: opaqueIdSchema,
    nodeId: opaqueIdSchema,
    resourceType: opaqueIdSchema,
    path: opaqueIdSchema,
    cardinality: z.string(),
    repeatedBoundaries: z
      .array(constructionChoiceRepeatedBoundarySchema)
      .optional(),
  })
  .strict();
export type FieldChoiceSource = z.infer<typeof fieldChoiceSourceSchema>;

export const semanticBindingChoiceSourceSchema = z
  .object({
    kind: z.literal('SEMANTIC'),
    conceptId: opaqueIdSchema,
    bindingId: opaqueIdSchema,
    candidateId: opaqueIdSchema,
    nodeId: opaqueIdSchema,
    resourceType: opaqueIdSchema,
    sourcePath: opaqueIdSchema,
    sourceCanonical: z.string().optional(),
    sourceProfile: z.string().optional(),
    fieldPath: opaqueIdSchema,
    owningScope: z.string().optional(),
    extensionUrlPath: z.array(z.string()).optional(),
    keySelector: z.string().optional(),
    system: z.string().optional(),
    version: z.string().optional(),
    code: z.string().optional(),
    valueSelector: opaqueIdSchema,
    choiceArm: z.string().optional(),
    logicalType: opaqueIdSchema,
    ruleHint: z.string().optional(),
    ruleVersion: opaqueIdSchema,
    schemaVersion: z.number().int().min(1),
    cardinality: z.string(),
    repeatedBoundaries: z
      .array(constructionChoiceRepeatedBoundarySchema)
      .optional(),
  })
  .strict();
export type SemanticBindingChoiceSource = z.infer<
  typeof semanticBindingChoiceSourceSchema
>;

export const constructionChoiceSourceSchema = z.discriminatedUnion('kind', [
  fieldChoiceSourceSchema,
  semanticBindingChoiceSourceSchema,
]);
export type ConstructionChoiceSource = z.infer<
  typeof constructionChoiceSourceSchema
>;

export const sourcePresentationFactSchema = z
  .object({
    label: z.string().min(1),
    value: z.string(),
  })
  .strict();
export const constructionChoicePresentationSchema = z
  .object({
    summary: z.string().min(1),
    facts: z.array(sourcePresentationFactSchema),
  })
  .strict();

export const constructionChoiceSchema = z
  .object({
    choiceId: z.string().min(1).max(16384),
    source: constructionChoiceSourceSchema,
    route: z.array(constructionRouteStepSchema),
    presentation: constructionChoicePresentationSchema,
    options: z.array(constructionChoiceOptionSchema).min(1),
  })
  .strict();
export type ConstructionChoice = z.infer<typeof constructionChoiceSchema>;

export const constructionChoiceSearchSourceSchema = z.discriminatedUnion(
  'kind',
  [
    z.object({
      kind: z.literal('FIELD'),
      candidateId: opaqueIdSchema,
    }).strict(),
    z.object({
      kind: z.literal('SEMANTIC'),
      contextToken: opaqueIdSchema,
      buildId: opaqueIdSchema,
      conceptId: opaqueIdSchema,
      bindingId: opaqueIdSchema,
    }).strict(),
  ],
);
export type ConstructionChoiceSearchSource = z.infer<
  typeof constructionChoiceSearchSourceSchema
>;

export const constructionChoiceSearchResponseSchema = z
  .object({
    snapshotToken: opaqueIdSchema,
    outputId: opaqueIdSchema,
    complete: z.boolean(),
    truncated: z.boolean(),
    nextCursor: opaqueIdSchema.optional(),
    choices: z.array(constructionChoiceSchema).max(50),
  })
  .strict()
  .superRefine((value, context) => {
    if (value.complete === value.truncated) {
      context.addIssue({
        code: 'custom',
        message: 'Construction route search must be either complete or truncated.',
      });
    }
    if (value.nextCursor && !value.truncated) {
      context.addIssue({
        code: 'custom',
        path: ['nextCursor'],
        message: 'A complete route search cannot have a continuation cursor.',
      });
    }
  });
export type ConstructionChoiceSearchResponse = z.infer<
  typeof constructionChoiceSearchResponseSchema
>;

export const populationRouteChoiceSchema = z
  .object({
    routeChoiceId: opaqueIdSchema,
    route: z.array(constructionRouteStepSchema),
    presentation: constructionChoicePresentationSchema,
  })
  .strict();
export type PopulationRouteChoice = z.infer<typeof populationRouteChoiceSchema>;

export const populationRoutesResponseSchema = z
  .object({
    snapshotToken: opaqueIdSchema,
    outputId: opaqueIdSchema,
    selectionRevisionId: opaqueIdSchema,
    complete: z.boolean(),
    truncated: z.boolean(),
    nextCursor: opaqueIdSchema.optional(),
    choices: z.array(populationRouteChoiceSchema).max(50),
  })
  .strict()
  .superRefine((value, context) => {
    if (value.complete === value.truncated) {
      context.addIssue({
        code: 'custom',
        message: 'Population route search must be either complete or truncated.',
      });
    }
    if (value.nextCursor && !value.truncated) {
      context.addIssue({
        code: 'custom',
        path: ['nextCursor'],
        message: 'A complete population route search cannot have a continuation cursor.',
      });
    }
  });
export type PopulationRoutesResponse = z.infer<
  typeof populationRoutesResponseSchema
>;

export const constructionChoiceSelectionSchema = z
  .object({
    choiceId: z.string().min(1),
    form: constructionChoiceFormSchema,
  })
  .strict();
export type ConstructionChoiceSelection = z.infer<
  typeof constructionChoiceSelectionSchema
>;

export const aggregateOperationCapabilitySchema = z
  .object({
    operation: z.enum([
      'COUNT',
      'COUNT_DISTINCT',
      'DISTINCT_VALUES',
      'EXISTS',
      'MIN',
      'MAX',
      'SUM',
      'MEAN',
      'CONTAINS_ALL',
      'REQUIRE_ONE',
      'COLLECT',
      'FIRST_ORDERED',
    ]),
    rowContext: z.enum(['RECORDS', 'GROUPS', 'EXPANDED']),
    supported: z.boolean(),
    reasonCode: z.string().optional(),
    reason: z.string().optional(),
    resultLogicalType: z.string().optional(),
    resultCardinality: z.enum(['ONE', 'OPTIONAL_ONE', 'MANY']).optional(),
    missingValueSemantics: z.string().optional(),
    contributorSemantics: z.string().optional(),
    requiresConfiguration: z.array(z.string()).optional(),
  })
  .strict();
export type AggregateOperationCapability = z.infer<
  typeof aggregateOperationCapabilitySchema
>;

export const temporalFieldChoiceSchema = z
  .object({
    candidateId: opaqueIdSchema,
    nodeId: opaqueIdSchema,
    resourceType: opaqueIdSchema,
    fieldPath: opaqueIdSchema,
    label: z.string(),
  })
  .strict();
export type TemporalFieldChoice = z.infer<typeof temporalFieldChoiceSchema>;

export const temporalReductionCapabilitySchema = z
  .object({
    available: z.boolean(),
    reasonCode: z.string().optional(),
    reason: z.string().optional(),
    timestampFields: z.array(temporalFieldChoiceSchema),
    anchorFields: z.array(temporalFieldChoiceSchema),
  })
  .strict();
export type TemporalReductionCapability = z.infer<
  typeof temporalReductionCapabilitySchema
>;

export const unitNormalizationPresetCapabilitySchema = z
  .object({
    policyId: opaqueIdSchema,
    version: opaqueIdSchema,
    target: z.object({ system: z.string(), code: z.string() }).strict(),
    available: z.boolean(),
    reasonCode: z.string().optional(),
    reason: z.string().optional(),
  })
  .strict();
export type UnitNormalizationPresetCapability = z.infer<
  typeof unitNormalizationPresetCapabilitySchema
>;

export const unitNormalizationCapabilitySchema = z
  .object({
    available: z.boolean(),
    reasonCode: z.string().optional(),
    reason: z.string().optional(),
    presets: z.array(unitNormalizationPresetCapabilitySchema),
  })
  .strict();
export type UnitNormalizationCapability = z.infer<
  typeof unitNormalizationCapabilitySchema
>;

export const aggregateTransformationCapabilitySchema = z
  .object({
    temporalReduction: temporalReductionCapabilitySchema,
    unitNormalization: unitNormalizationCapabilitySchema,
  })
  .strict();
export type AggregateTransformationCapability = z.infer<
  typeof aggregateTransformationCapabilitySchema
>;

export const valueTransformationCapabilitySchema = z
  .object({
    available: z.boolean(),
    reasonCode: z.string().optional(),
    reason: z.string().optional(),
  })
  .strict();
export const columnValueTransformationCapabilitiesSchema = z
  .object({
    exactCategoryRecode: valueTransformationCapabilitySchema,
    codedValueRecoding: valueTransformationCapabilitySchema,
  })
  .strict();
export type ColumnValueTransformationCapabilities = z.infer<
  typeof columnValueTransformationCapabilitiesSchema
>;

export const explorerBuilderCandidateSchema = z
  .object({
    candidateId: opaqueIdSchema,
    nodeId: opaqueIdSchema,
    fieldPath: opaqueIdSchema,
    label: z.string(),
    logicalType: opaqueIdSchema,
    cardinality: opaqueIdSchema,
    repeated: z.boolean().optional(),
    filterable: z.boolean(),
    chartable: z.boolean(),
    projectionModes: z.array(projectionModeSchema).min(1),
    defaultProjectionMode: projectionModeSchema,
    constructionChoice: constructionChoiceSchema.optional(),
    aggregateOperations: z.array(aggregateOperationCapabilitySchema),
    transformations: aggregateTransformationCapabilitySchema,
    valueTransformations: columnValueTransformationCapabilitiesSchema,
    conceptCandidates: z.array(conceptCandidateSchema).optional(),
    repeatedBoundaries: z
      .array(
        z
          .object({
            path: opaqueIdSchema,
            maxItems: z.number().int().nonnegative(),
          })
          .strict(),
      )
      .optional(),
  })
  .strict();
export const explorerBuilderCatalogSchema = z
  .object({
    snapshotToken: opaqueIdSchema,
    generation: opaqueIdSchema,
    resolvedSchemaDigest: z.string().optional(),
    authorizationScopeDigest: z.string().optional(),
    complete: z.boolean().optional(),
    routePolicy: explorerBuilderRoutePolicySchema,
    nodes: z.array(explorerBuilderCatalogNodeSchema),
    edges: z.array(explorerBuilderCatalogEdgeSchema),
    candidates: z.array(explorerBuilderCandidateSchema).optional(),
  })
  .strict();
export type ExplorerBuilderCatalog = z.infer<
  typeof explorerBuilderCatalogSchema
>;
export type ExplorerBuilderCandidate = z.infer<
  typeof explorerBuilderCandidateSchema
>;

export const explorerBuilderStateSchema = z
  .object({
    apiVersion: z.literal(EXPLORER_AUTHORING_API_VERSION),
    kind: z.literal('ExplorerBuilderState'),
    lifecycleState: z.enum(['NEW', 'READY']),
    draftVersion: z.number().int().nonnegative(),
    draftDigest: z.string(),
    previousDraftRevisionId: opaqueIdSchema.optional(),
    workspace: explorerBuilderWorkspaceSchema.nullable(),
    catalog: explorerBuilderCatalogSchema,
  })
  .strict()
  .superRefine((state, context) => {
    if ((state.lifecycleState === 'NEW') !== (state.workspace === null)) {
      context.addIssue({
        code: 'custom',
        path: ['workspace'],
        message: 'NEW requires null workspace and READY requires a workspace.',
      });
    }
  });
export type ExplorerBuilderState = z.infer<typeof explorerBuilderStateSchema>;

export const routeRebaseChoiceSchema = z
  .object({
    occurrenceId: opaqueIdSchema,
    edgeId: opaqueIdSchema,
  })
  .strict();
export const rowChangeProposalSchema = z
  .object({
    outputId: opaqueIdSchema,
    rootNodeId: opaqueIdSchema,
    rootOccurrenceId: opaqueIdSchema,
    sourceDocumentDigest: opaqueIdSchema,
    routeRebase: z.array(routeRebaseChoiceSchema).min(1),
    preservedFeatureKeys: z.array(opaqueIdSchema),
  })
  .strict();
const rowChangeAssessmentBaseSchema = z.object({
  snapshotToken: opaqueIdSchema,
  draftVersion: z.number().int().positive(),
  draftDigest: opaqueIdSchema,
  currentRootResourceType: opaqueIdSchema,
  candidateRootResourceType: opaqueIdSchema,
  preservedFeatureKeys: z.array(opaqueIdSchema),
  diagnostics: z.array(explorerAuthoringDiagnosticSchema),
});
export const rowChangeUnresolvedReferenceSchema = z
  .object({
    kind: z.enum(['route', 'column']),
    id: opaqueIdSchema,
    code: opaqueIdSchema,
    message: z.string().min(1),
    alternatives: z.array(opaqueIdSchema).optional(),
  })
  .strict();
export type RowChangeUnresolvedReference = z.infer<
  typeof rowChangeUnresolvedReferenceSchema
>;
export const rowChangeAssessmentSchema = z.discriminatedUnion('status', [
  rowChangeAssessmentBaseSchema.extend({
    status: z.literal('READY'),
    proposal: rowChangeProposalSchema,
    unresolved: z.array(rowChangeUnresolvedReferenceSchema).length(0),
  }).strict(),
  rowChangeAssessmentBaseSchema.extend({
    status: z.literal('BLOCKED'),
    unresolved: z.array(rowChangeUnresolvedReferenceSchema).min(1),
  }).strict(),
  rowChangeAssessmentBaseSchema.extend({
    status: z.literal('NO_CHANGE'),
    unresolved: z.array(rowChangeUnresolvedReferenceSchema).length(0),
  }).strict(),
]);
export type RowChangeAssessment = z.infer<typeof rowChangeAssessmentSchema>;

export const semanticSelectionIntentSchema = z
  .object({
    conceptId: opaqueIdSchema,
    bindingId: opaqueIdSchema,
    routeEdgeIds: z.array(opaqueIdSchema),
    projectionMode: projectionModeSchema,
    title: z.string().optional(),
  })
  .strict();
export type SemanticSelectionIntent = z.infer<typeof semanticSelectionIntentSchema>;

export const semanticSelectionResultSchema = z
  .object({
    conceptId: opaqueIdSchema,
    bindingId: opaqueIdSchema,
    columnId: opaqueIdSchema,
    status: z.enum(['ADDED', 'ALREADY_PRESENT']),
  })
  .strict();
export type SemanticSelectionResult = z.infer<typeof semanticSelectionResultSchema>;

export const explorerBuilderCommandSchema = z
  .object({
    type: z.enum([
      'CREATE_TABLE',
      'DUPLICATE_TABLE',
      'DELETE_TABLE',
      'RENAME_TABLE',
      'REORDER_TABLES',
      'SET_TABLE_ROOT',
      'APPLY_TABLE_ROOT_REBASE',
      'SET_TABLE_POPULATION',
      'CLEAR_TABLE_POPULATION',
      'ADD_ROUTE',
      'UPDATE_ROUTE_EDGE',
      'SET_ROUTE_MATCH_MODE',
      'REMOVE_ROUTE',
      'ADD_COLUMN',
      'ADD_COLUMN_SOURCE',
      'UPDATE_COLUMN_SOURCE',
      'UPDATE_COLUMN',
      'UPDATE_COLUMN_TRANSFORMATION',
      'SET_COLUMN_CONTRIBUTOR',
      'CLEAR_COLUMN_CONTRIBUTOR',
      'APPLY_INTERPRETATION_CANDIDATE',
      'REMOVE_COLUMN',
      'ADD_SEMANTIC_SELECTIONS',
      'APPLY_CONSTRUCTION_CHOICE',
      'APPLY_ROW_DEFINITION_PROPOSAL',
      'APPLY_CONSTRUCTION_PROPOSAL',
      'APPLY_TABLE_SHAPE_PROPOSAL',
      'RESTORE_DRAFT_REVISION',
    ]),
    outputId: opaqueIdSchema.optional(),
    sourceOutputId: opaqueIdSchema.optional(),
    title: z.string().optional(),
    rootNodeId: opaqueIdSchema.optional(),
    selectionRevisionId: opaqueIdSchema.optional(),
    routeChoiceId: opaqueIdSchema.optional(),
    edgeIds: z.array(opaqueIdSchema).optional(),
    parentOccurrenceId: opaqueIdSchema.optional(),
    occurrenceId: opaqueIdSchema.optional(),
    edgeId: opaqueIdSchema.optional(),
    matchMode: z.enum(['OPTIONAL', 'REQUIRED']).optional(),
    candidateId: opaqueIdSchema.optional(),
    projectionMode: projectionModeSchema.optional(),
    initialPresentation: z.enum(['TABLE', 'FILTER', 'CHART']).optional(),
    column: opaqueIdSchema.optional(),
    columnValue: explorerBuilderColumnSchema.optional(),
    transformationChange: columnTransformationChangeSchema.optional(),
    contributor: contributorPredicateSchema.optional(),
    source: explorerColumnSourceSchema.optional(),
    rowChange: rowChangeProposalSchema.optional(),
    interpretationCandidate: z
      .object({
        candidateReceiptId: opaqueIdSchema,
        revisionId: opaqueIdSchema,
      })
      .strict()
      .optional(),
    proposalId: opaqueIdSchema.optional(),
    draftRevisionId: opaqueIdSchema.optional(),
    contextToken: opaqueIdSchema.optional(),
    semanticSelections: z.array(semanticSelectionIntentSchema).min(1).max(100).optional(),
    constructionChoice: constructionChoiceSelectionSchema.optional(),
    outputIds: z.array(opaqueIdSchema).optional(),
  })
  .strict()
  .superRefine((command, context) => {
    if (command.type !== 'RESTORE_DRAFT_REVISION') return;
    if (!command.draftRevisionId) {
      context.addIssue({
        code: 'custom',
        path: ['draftRevisionId'],
        message: 'RESTORE_DRAFT_REVISION requires a draft revision ID.',
      });
    }
    for (const [key, value] of Object.entries(command)) {
      if (key !== 'type' && key !== 'draftRevisionId' && value !== undefined) {
        context.addIssue({
          code: 'custom',
          path: [key],
          message: 'RESTORE_DRAFT_REVISION accepts only its revision ID.',
        });
      }
    }
  });
export type ExplorerBuilderCommand = z.infer<
  typeof explorerBuilderCommandSchema
>;
export const explorerBuilderCommandResultSchema = z
  .object({
    type: z.enum([
      'TABLE_CREATED',
      'TABLE_CHANGED',
      'ROUTE_ADDED',
      'COLUMN_ADDED',
      'SEMANTIC_SELECTIONS_ADDED',
      'DRAFT_RESTORED',
    ]),
    outputId: opaqueIdSchema.optional(),
    tabId: opaqueIdSchema.optional(),
    occurrenceId: opaqueIdSchema.optional(),
    column: opaqueIdSchema.optional(),
    semanticSelections: z.array(semanticSelectionResultSchema).max(100).optional(),
  })
  .strict();
export const explorerBuilderCommandsResultSchema = z
  .object({
    commandId: opaqueIdSchema,
    workspace: explorerBuilderWorkspaceSchema,
    draftVersion: z.number().int().positive(),
    draftDigest: opaqueIdSchema,
    previousDraftRevisionId: opaqueIdSchema.optional(),
    results: z.array(explorerBuilderCommandResultSchema),
    diagnostics: z.array(explorerAuthoringDiagnosticSchema),
  })
  .strict();
export type ExplorerBuilderCommandsResult = z.infer<
  typeof explorerBuilderCommandsResultSchema
>;

export const explorerBuilderContractColumnSchema = z
  .object({
    column: opaqueIdSchema,
    authoredColumns: z.array(opaqueIdSchema).min(1).optional(),
    label: z.string(),
    logicalType: opaqueIdSchema,
    resultUnit: resultUnitSchema.optional(),
    filterable: z.boolean(),
    chartable: z.boolean(),
    nullable: z.boolean().optional(),
    shape: z.string().optional(),
    sourceResourceType: z.string().optional(),
    sourcePath: z.string().optional(),
    choiceArm: z.string().optional(),
    coordinates: z
      .array(
        z
          .object({
            boundaryPath: opaqueIdSchema,
            index: z.number().int().nonnegative(),
            width: z.number().int().positive(),
          })
          .strict(),
      )
      .optional(),
    lossless: z.boolean().optional(),
    mlReady: z.boolean().optional(),
    structuralSuitability: z.enum(['scalar', 'array', 'requires-review']).optional(),
    lossReasons: z.array(z.string()).optional(),
    unitNormalization: z.object({
      target: resultUnitSchema,
      rules: z.array(z.object({ id: opaqueIdSchema, version: opaqueIdSchema }).strict()).min(1),
    }).strict().optional(),
  })
  .strict();
export type ExplorerBuilderContractColumn = z.infer<
  typeof explorerBuilderContractColumnSchema
>;
// Local Builder view-model identities are not part of Loom's wire contract.
export interface ExplorerBuilderSelection {
  readonly candidateId: string;
  readonly occurrenceId: string;
  readonly projectionMode: string;
}
export interface ExplorerPresentationIntent {
  readonly label?: string;
  readonly visible?: boolean;
  readonly order?: number;
  readonly table?: { readonly pinned?: boolean };
  readonly filter?: { readonly label?: string };
  readonly chart?: { readonly type: string; readonly title?: string };
}
export interface ExplorerBuilderEmission extends ExplorerBuilderContractColumn {
  readonly outputId: string;
  readonly candidateId: string;
  readonly occurrenceId: string;
  readonly projectionMode: string;
  readonly emissionId: string;
  readonly publicColumn: string;
}
export const explorerBuilderReceiptOutputSchema = z
  .object({
    outputId: opaqueIdSchema,
    title: z.string().optional(),
    rowGrain: z.string().optional(),
    rootResourceType: z.string().optional(),
    rowMultiplication: z.enum(['none', 'expand']).optional(),
    lossless: z.boolean().optional(),
    mlReady: z.boolean().optional(),
    structuralSuitability: z.enum(['scalar', 'array', 'requires-review']).optional(),
    lossReasons: z.array(z.string()).optional(),
    columns: z.array(explorerBuilderContractColumnSchema),
  })
  .strict();
export const explorerBuilderCompileResultSchema = z
  .object({
    apiVersion: z.literal(EXPLORER_AUTHORING_API_VERSION),
    kind: z.literal('ExplorerBuilderReceipt'),
    receiptId: opaqueIdSchema,
    snapshotToken: opaqueIdSchema,
    generation: z.string().optional(),
    intentDigest: z.string().optional(),
    resolvedInputsDigest: z.string().optional(),
    compilerVersion: z.string().optional(),
    shapeDigest: z.string().optional(),
    recipeDigest: z.string().optional(),
    resolvedRecipeDigest: z.string().optional(),
    resolvedSchemaDigest: z.string().optional(),
    outputContractDigest: z.string().optional(),
    authorizationScopeDigest: z.string().optional(),
    capabilitySchemaDigest: z.string().optional(),
    builder: explorerBuilderWorkspaceSchema,
    outputs: z.array(explorerBuilderReceiptOutputSchema),
    diagnostics: z.array(explorerAuthoringDiagnosticSchema),
  })
  .strict();
export type ExplorerBuilderCompileResult = z.infer<
  typeof explorerBuilderCompileResultSchema
>;

export const explorerBuilderPreviewColumnSchema =
  explorerBuilderContractColumnSchema;
export const explorerBuilderPreviewResultSchema = z
  .object({
    apiVersion: z.literal(EXPLORER_AUTHORING_API_VERSION),
    kind: z.literal('ExplorerBuilderPreview'),
    receiptId: opaqueIdSchema,
    outputId: opaqueIdSchema,
    columns: z.array(explorerBuilderPreviewColumnSchema),
    rows: z.array(unknownRecordSchema).nullable(),
    rowCount: z.number().int().nonnegative(),
    diagnostics: z.array(explorerAuthoringDiagnosticSchema),
  })
  .strict();
export type ExplorerBuilderPreviewResult = z.infer<
  typeof explorerBuilderPreviewResultSchema
>;

export const explorerBuilderPublishResultSchema = z
  .object({
    apiVersion: z.literal(EXPLORER_AUTHORING_API_VERSION),
    kind: z.literal('ExplorerBuilderPublication'),
    receiptId: opaqueIdSchema,
    revisionId: opaqueIdSchema,
    state: z.string(),
    outputs: z.array(
      z
        .object({
          outputId: opaqueIdSchema,
          state: z.string(),
          materializationId: z.string().optional(),
        })
        .strict(),
    ),
    diagnostics: z.array(explorerAuthoringDiagnosticSchema),
  })
  .strict();
export type ExplorerBuilderPublishResult = z.infer<
  typeof explorerBuilderPublishResultSchema
>;

export const explorerBuilderSuggestionsResultSchema = z
  .object({
    apiVersion: z.literal(EXPLORER_AUTHORING_API_VERSION),
    kind: z.literal('ExplorerBuilderCandidateSuggestions'),
    snapshotToken: opaqueIdSchema,
    nodeId: opaqueIdSchema,
    candidates: z.array(explorerBuilderCandidateSchema),
    diagnostics: z.array(explorerAuthoringDiagnosticSchema),
  })
  .strict();
export type ExplorerBuilderSuggestionsResult = z.infer<
  typeof explorerBuilderSuggestionsResultSchema
>;

export const semanticSelectionReadinessSchema = z
  .object({
    status: z.enum(['READY', 'READY_WITH_WARNING', 'NEEDS_MAPPING', 'UNSUPPORTED']),
    code: z.string().min(1),
    message: z.string().min(1),
  })
  .strict();
export type SemanticSelectionReadiness = z.infer<typeof semanticSelectionReadinessSchema>;

export const semanticInventoryItemSchema = z
  .object({
    conceptId: z.string(),
    bindingId: z.string(),
    resourceType: z.string().min(1),
    sourcePath: z.string(),
    system: z.string(),
    code: z.string(),
    codingVersion: z.string(),
    display: z.string(),
    valueSelector: z.string(),
    valueType: z.string(),
    owningScope: z.string(),
    // Counts observed source occurrences/events; one source record may contribute more than once.
    occurrences: z.number().int().nonnegative(),
    examples: z.array(z.string()).max(32).optional(),
    examplesTruncated: z.boolean(),
    observedUnits: z.array(z.string()).optional(),
    observedUnitsTruncated: z.boolean(),
    completeness: z.enum(['complete', 'partial', 'incomplete']).optional(),
    readiness: semanticSelectionReadinessSchema,
    constructionChoice: constructionChoiceSchema.optional(),
  })
  .strict();
export type SemanticInventoryItem = z.infer<typeof semanticInventoryItemSchema>;

export const semanticInventoryBrowseResponseSchema = z
  .object({
    contextToken: z.string().min(1),
    buildId: z.string(),
    state: z.enum([
      'unknown',
      'not_started',
      'running',
      'complete',
      'failed',
      'invalidated',
    ]),
    sourceAvailability: z.enum(['unknown', 'verified', 'unproven']),
    entries: z.array(semanticInventoryItemSchema).max(50),
    nextCursor: z.string().min(1).optional(),
  })
  .strict();
export type SemanticInventoryBrowseResponse = z.infer<
  typeof semanticInventoryBrowseResponseSchema
>;

export const explorerAuthoringCapabilitiesSchema = z
  .object({
    apiVersion: z.literal(EXPLORER_AUTHORING_API_VERSION),
    kind: z.literal('ExplorerAuthoringCapabilities'),
    operations: z.array(z.string()),
    previewLimits: z.array(z.number().int().positive()).optional(),
    features: z
      .object({
        emissionFilters: z.boolean(),
        emissionCharts: z.boolean(),
        sharedFilters: z.boolean(),
        fixedFilters: z.boolean(),
        fileActions: z.boolean(),
        deleteExplorer: z.boolean(),
      })
      .strict(),
  })
  .strict();
export type ExplorerAuthoringCapabilities = z.infer<
  typeof explorerAuthoringCapabilitiesSchema
>;

export const explorerAuthoringErrorSchema = z
  .object({
    code: opaqueIdSchema,
    message: z.string(),
    diagnostics: z.array(explorerAuthoringDiagnosticSchema).optional(),
    requestId: z.string().optional(),
    details: unknownRecordSchema.optional(),
  })
  .strict();
export type ExplorerAuthoringError = z.infer<
  typeof explorerAuthoringErrorSchema
>;

export const assertExplorerBuilderState = (value: unknown) =>
  explorerBuilderStateSchema.parse(value);
export const assertExplorerBuilderCompileResult = (value: unknown) =>
  explorerBuilderCompileResultSchema.parse(value);
export const assertExplorerBuilderPreviewResult = (value: unknown) =>
  explorerBuilderPreviewResultSchema.parse(value);
export const assertExplorerBuilderPublishResult = (value: unknown) =>
  explorerBuilderPublishResultSchema.parse(value);

/** Server-owned runtime projection retained for viewer/ETL consumers. */
export interface PublicationMetadata {
  readonly state: string;
  readonly generation?: string;
  readonly executionId?: string;
  readonly revisionId?: string;
  readonly updatedAt?: string;
}
export interface ExplorerRuntimeColumnV1 {
  readonly column: string;
  readonly label: string;
  readonly logicalType: string;
  readonly resultUnit?: ResultUnit;
  readonly visible: boolean;
  readonly order: number;
  readonly repeated?: boolean;
  readonly filterable: boolean;
  readonly sortable?: boolean;
  readonly chartable: boolean;
  readonly aggregatable?: boolean;
}
export type ExplorerRuntimeColumnsV1 = ReadonlyArray<ExplorerRuntimeColumnV1>;
export interface ExplorerRuntimeBindingV1 {
  readonly column: string;
  readonly outputId?: string;
  readonly label?: string;
  readonly type?: string;
  readonly title?: string;
}
export interface ExplorerRuntimeOutputV1 {
  readonly outputId: string;
  readonly name: string;
  readonly title: string;
  readonly rowLabel: string;
  readonly selector: DataframeSelector;
  readonly columns: ExplorerRuntimeColumnsV1;
  readonly table: {
    readonly columns: ReadonlyArray<
      ExplorerRuntimeBindingV1 & { readonly visible: boolean } & {
        readonly pinned?: boolean;
        readonly cellRenderer?: 'fileActions';
      }
    >;
  };
  readonly filters: ReadonlyArray<ExplorerRuntimeBindingV1>;
  readonly charts: ReadonlyArray<ExplorerRuntimeBindingV1>;
  readonly fixedFilters: Readonly<Record<string, ReadonlyArray<string>>>;
  readonly actions?: ReadonlyArray<{
    readonly type: string;
    readonly title: string;
    readonly fileName?: string;
    readonly output?: string;
    readonly columns?: ReadonlyArray<string>;
    readonly exportHeaders?: Readonly<Record<string, string>>;
  }>;
  readonly query?: Readonly<Record<string, unknown>>;
  readonly materialization?: Readonly<Record<string, unknown>>;
}
export interface ExplorerRuntimeV1 {
  /** Client-side identity derived from the enclosing response for legacy runtimes. */
  readonly responseIdentity?: string;
  readonly status?: string;
  readonly generation?: string;
  readonly publication?: PublicationMetadata;
  readonly schema?: { readonly digest?: string; readonly version?: string };
  readonly outputs: ReadonlyArray<ExplorerRuntimeOutputV1>;
  readonly sharedFilters: Readonly<
    Record<string, ReadonlyArray<ExplorerRuntimeBindingV1>>
  >;
  readonly fileActions?: {
    readonly extensions?: Readonly<Record<string, ReadonlyArray<string>>>;
    readonly actions?: Readonly<Record<string, string>>;
  };
  readonly qualityReports?: ReadonlyArray<ExplorerQualityReportV1>;
  readonly diagnostics: ReadonlyArray<ExplorerRuntimeDiagnostic>;
}

export interface ExplorerRuntimeDiagnostic {
  readonly severity: string;
  readonly stage?: string;
  readonly code: string;
  readonly fieldPath?: string | null;
  readonly message: string;
  readonly details?: Readonly<Record<string, unknown>>;
  readonly retryable?: boolean;
  readonly requestId?: string;
}

export interface ExplorerColumnQualityV1 {
  readonly column: string;
  readonly present: number;
  readonly missing: number;
  readonly recordedNull: number;
  readonly emptyArray: number;
  readonly relatedSource?: RelatedSourcePopulationV1;
}

export interface RelatedSourcePopulationV1 {
  readonly basis: 'ALL_MATCHES_NO_FILTER_OR_WINDOW';
  readonly outputRows: number;
  readonly nonemptyListRows: number;
  readonly emptyListRows: number;
  readonly totalListEntries: number;
  readonly rowsWithMultipleEntries: number;
  readonly nullOrAbsentFieldValueEntries: number;
  readonly unknownRows: number;
}

export interface ExplorerQualityReportV1 {
  readonly id: string;
  readonly receiptId: string;
  readonly project: string;
  readonly datasetGeneration: string;
  readonly scopeDigest: string;
  readonly output: string;
  readonly policyVersion: string;
  readonly completeness: 'COMPLETE' | 'INCOMPLETE';
  readonly verdict: 'PASSED' | 'FAILED';
  readonly rowCount: number;
  readonly columns: ReadonlyArray<ExplorerColumnQualityV1>;
  readonly keyIntegrity: {
    readonly distinct: number;
    readonly missing: number;
    readonly duplicate: number;
  };
  readonly limits: {
    readonly maxRows: number;
    readonly maxDistinctKeys: number;
  };
  readonly issues: {
    readonly ambiguous: number;
    readonly invalidType: number;
    readonly incompatibleUnit: number;
  };
  readonly omissions?: ReadonlyArray<{ readonly code: string; readonly detail: string }>;
}

/** Opaque generated metadata retained only for the runtime compatibility adapter. */
export type ExplorerStateAuthoringBundleV1 = Readonly<Record<string, unknown>>;
export interface ExplorerStateEmittedColumnV1 {
  readonly emissionId: string;
  readonly outputId: string;
  readonly nodeId?: string;
  readonly selectionId?: string;
  readonly candidateId?: string;
  readonly occurrenceId?: string;
  readonly publicColumn: string;
  readonly logicalType: string;
  readonly filterable: boolean;
  readonly chartable: boolean;
}
export interface ExplorerStatePhysicalColumnV1 {
  readonly name: string;
  readonly semanticPath?: string;
  readonly clickhouseType?: string;
  readonly logicalType?: string;
  readonly nullable?: boolean;
  readonly repeated?: boolean;
  readonly provenance?: string;
  readonly loomOwned?: boolean;
}
export interface ExplorerStateDatasetOutputV1 {
  readonly name: string;
  readonly state: string;
  readonly queryable: boolean;
  readonly fingerprint?: string;
  readonly selector?: DataframeSelector;
  readonly columns?: ReadonlyArray<ExplorerStatePhysicalColumnV1>;
}
export interface ExplorerStateMaterializationV1 {
  readonly outputId: string;
  readonly output: string;
  readonly materializationId: string;
  readonly fingerprint?: string;
  readonly selector?: DataframeSelector;
  readonly columns: ReadonlyArray<ExplorerStatePhysicalColumnV1>;
}

/** Runtime-only selected Explorer response. Editable state is never read here. */
export interface ExplorerStateV1 {
  readonly apiVersion: 'loom.calypr.org/explorer-state/v1';
  readonly kind: 'ExplorerState';
  readonly project: string;
  readonly explorerId: string;
  readonly title: string;
  readonly management:
    | 'repository'
    | 'interactive'
    | 'REPOSITORY'
    | 'INTERACTIVE';
  readonly draft: {
    readonly bundle?: ExplorerStateAuthoringBundleV1;
    readonly receiptId?: string;
    readonly version: number;
    readonly digest: string;
    readonly intentDigest?: string;
  };
  readonly active: {
    readonly bundle?: ExplorerStateAuthoringBundleV1;
    readonly revisionId?: string;
    readonly intentDigest?: string;
    readonly status?: string;
  };
  readonly generated: {
    readonly recipeDigest?: string;
    readonly sourceGeneration?: string;
    readonly resolvedSchemaDigest?: string;
    readonly emittedColumns?: ReadonlyArray<ExplorerStateEmittedColumnV1>;
    readonly materializations?: ReadonlyArray<ExplorerStateMaterializationV1>;
    readonly dataset?: {
      readonly outputs: ReadonlyArray<ExplorerStateDatasetOutputV1>;
    };
    readonly publication?: PublicationMetadata;
    readonly qualityReports?: ReadonlyArray<ExplorerQualityReportV1>;
    readonly diagnostics?: ReadonlyArray<ExplorerRuntimeDiagnostic>;
  };
  readonly activeUrl: string;
  readonly updatedBy?: string;
  readonly updatedAt?: string;
  /** Runtime is null when Loom has no valid published runtime to serve. */
  readonly runtime?: ExplorerRuntimeV1 | null;
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const legacyExplorerStateKeys = new Set(['draftConfig', 'activeConfig']);

const dataframeSelectorSchema = z
  .object({
    recipe: opaqueIdSchema,
    translationVersion: opaqueIdSchema,
    output: opaqueIdSchema,
  })
  .strict();
const publicationMetadataSchema = z
  .object({
    state: z.string(),
    generation: z.string().optional(),
    executionId: z.string().optional(),
    revisionId: z.string().optional(),
    updatedAt: z.string().optional(),
  })
  .strict();
const runtimeBindingSchema = z
  .object({
    column: opaqueIdSchema,
    outputId: z.string().optional(),
    label: z.string().optional(),
    type: z.string().optional(),
    title: z.string().optional(),
  })
  .strict();
const runtimeColumnSchema = z
  .object({
    column: opaqueIdSchema,
    label: z.string(),
    logicalType: z.string(),
    resultUnit: resultUnitSchema.optional(),
    visible: z.boolean(),
    order: z.number().int().nonnegative(),
    repeated: z.boolean().optional(),
    filterable: z.boolean(),
    sortable: z.boolean().optional(),
    chartable: z.boolean(),
    aggregatable: z.boolean().optional(),
  })
  .strict();
const runtimeTableColumnSchema = z
  .object({
    column: opaqueIdSchema,
    visible: z.boolean(),
    pinned: z.boolean().optional(),
    cellRenderer: z.literal('fileActions').optional(),
  })
  .passthrough();
const runtimeActionSchema = z
  .object({
    type: opaqueIdSchema,
    title: z.string(),
    fileName: z.string().optional(),
    output: z.string().optional(),
    columns: z.array(z.string()).optional(),
    exportHeaders: z.record(z.string(), z.string()).optional(),
  })
  .strict();
const runtimeOutputSchema = z
  .object({
    outputId: opaqueIdSchema,
    name: z.string(),
    title: z.string(),
    rowLabel: z.string(),
    selector: dataframeSelectorSchema,
    columns: z.array(runtimeColumnSchema),
    table: z.object({ columns: z.array(runtimeTableColumnSchema) }).strict(),
    filters: z.array(runtimeBindingSchema),
    charts: z.array(runtimeBindingSchema),
    fixedFilters: z.record(z.string(), z.array(z.string())),
    actions: z.array(runtimeActionSchema).optional(),
    query: unknownRecordSchema.optional(),
    materialization: unknownRecordSchema.optional(),
  })
  .strict();
const runtimeDiagnosticSchema = z
  .object({
    severity: z.string(),
    stage: z.string().optional(),
    code: opaqueIdSchema,
    fieldPath: z.string().nullable().optional(),
    message: z.string(),
    details: unknownRecordSchema.optional(),
    retryable: z.boolean().optional(),
    requestId: z.string().optional(),
  })
  .strict();
const qualityReportSchema = z
  .object({
    id: opaqueIdSchema,
    receiptId: opaqueIdSchema,
    project: z.string(),
    datasetGeneration: z.string(),
    scopeDigest: z.string(),
    output: opaqueIdSchema,
    policyVersion: z.string(),
    completeness: z.enum(['COMPLETE', 'INCOMPLETE']),
    verdict: z.enum(['PASSED', 'FAILED']),
    rowCount: z.number().int().nonnegative(),
    columns: z.array(z.object({
      column: opaqueIdSchema,
      present: z.number().int().nonnegative(),
      missing: z.number().int().nonnegative(),
      recordedNull: z.number().int().nonnegative(),
      emptyArray: z.number().int().nonnegative(),
      relatedSource: z.object({
        basis: z.literal('ALL_MATCHES_NO_FILTER_OR_WINDOW'),
        outputRows: z.number().int().nonnegative(),
        nonemptyListRows: z.number().int().nonnegative(),
        emptyListRows: z.number().int().nonnegative(),
        totalListEntries: z.number().int().nonnegative(),
        rowsWithMultipleEntries: z.number().int().nonnegative(),
        nullOrAbsentFieldValueEntries: z.number().int().nonnegative(),
        unknownRows: z.number().int().nonnegative(),
      }).strict().optional(),
    }).strict()),
    keyIntegrity: z.object({
      distinct: z.number().int().nonnegative(),
      missing: z.number().int().nonnegative(),
      duplicate: z.number().int().nonnegative(),
    }).strict(),
    limits: z.object({
      maxRows: z.number().int().nonnegative(),
      maxDistinctKeys: z.number().int().nonnegative(),
    }).strict(),
    issues: z.object({
      ambiguous: z.number().int().nonnegative(),
      invalidType: z.number().int().nonnegative(),
      incompatibleUnit: z.number().int().nonnegative(),
    }).strict(),
    omissions: z.array(z.object({ code: z.string(), detail: z.string() }).strict()).optional(),
  })
  .strict();
const runtimeSchema = z
  .object({
    status: z.string().optional(),
    generation: z.string().optional(),
    publication: publicationMetadataSchema.optional(),
    schema: z.object({ digest: z.string().optional(), version: z.string().optional() }).strict().optional(),
    outputs: z.array(runtimeOutputSchema),
    sharedFilters: z.record(z.string(), z.array(runtimeBindingSchema)),
    fileActions: z.object({
      extensions: z.record(z.string(), z.array(z.string())).optional(),
      actions: z.record(z.string(), z.string()).optional(),
    }).strict().optional(),
    qualityReports: z.array(qualityReportSchema).optional(),
    diagnostics: z.array(runtimeDiagnosticSchema),
  })
  .strict();
const physicalColumnSchema = z
  .object({
    name: opaqueIdSchema,
    semanticPath: z.string().optional(),
    clickhouseType: z.string().optional(),
    logicalType: z.string().optional(),
    nullable: z.boolean().optional(),
    repeated: z.boolean().optional(),
    provenance: z.string().optional(),
    loomOwned: z.boolean().optional(),
  })
  .passthrough();
const selectorMetadataSchema = dataframeSelectorSchema.optional();
const datasetOutputSchema = z
  .object({
    name: opaqueIdSchema,
    state: z.string(),
    queryable: z.boolean(),
    fingerprint: z.string().optional(),
    selector: selectorMetadataSchema,
    columns: z.array(physicalColumnSchema).optional(),
  })
  .strict();
const emittedColumnSchema = z
  .object({
    emissionId: opaqueIdSchema,
    outputId: opaqueIdSchema,
    nodeId: z.string().optional(),
    selectionId: z.string().optional(),
    candidateId: z.string().optional(),
    occurrenceId: z.string().optional(),
    publicColumn: opaqueIdSchema,
    logicalType: z.string(),
    filterable: z.boolean(),
    chartable: z.boolean(),
  })
  .passthrough();
const materializationSchema = z
  .object({
    outputId: opaqueIdSchema,
    output: opaqueIdSchema,
    materializationId: opaqueIdSchema,
    fingerprint: z.string().optional(),
    selector: selectorMetadataSchema,
    columns: z.array(physicalColumnSchema),
  })
  .passthrough();
const generatedSchema = z
  .object({
    recipeDigest: z.string().optional(),
    sourceGeneration: z.string().optional(),
    resolvedSchemaDigest: z.string().optional(),
    emittedColumns: z.array(emittedColumnSchema).optional(),
    materializations: z.array(materializationSchema).optional(),
    dataset: z.object({
      generation: z.string().optional(),
      schemaDigest: z.string().optional(),
      outputs: z.array(datasetOutputSchema).nullable().transform((value) => value ?? []),
    }).strict().optional(),
    publication: publicationMetadataSchema.optional(),
    qualityReports: z.array(qualityReportSchema).optional(),
    diagnostics: z.array(runtimeDiagnosticSchema).optional(),
  })
  .strict();
const bundleSchema = unknownRecordSchema;
const explorerStateV1Schema = z
  .object({
    apiVersion: z.literal('loom.calypr.org/explorer-state/v1'),
    kind: z.literal('ExplorerState'),
    project: opaqueIdSchema,
    explorerId: opaqueIdSchema,
    title: z.string(),
    management: z.enum(['repository', 'interactive', 'REPOSITORY', 'INTERACTIVE']),
    active: z.object({
      bundle: bundleSchema.optional(),
      revisionId: z.string().optional(),
      intentDigest: z.string().optional(),
      status: z.string().optional(),
    }).strict(),
    generated: generatedSchema,
    activeUrl: z.string(),
    updatedBy: z.string().optional(),
    updatedAt: z.string().optional(),
    runtime: runtimeSchema.nullable().optional(),
    draft: z.object({
      bundle: bundleSchema.optional(),
      receiptId: z.string().optional(),
      version: z.number().int().nonnegative(),
      digest: z.string(),
      intentDigest: z.string().optional(),
    }).strict(),
  })
  .strict();

export const isExplorerStateV1 = (value: unknown): value is ExplorerStateV1 =>
  explorerStateV1Schema.safeParse(normalizeExplorerStateV1(value)).success;

const normalizeExplorerStateV1 = (value: unknown): unknown => {
  if (!isRecord(value) || !isRecord(value.runtime)) return value;
  if (value.runtime.diagnostics !== null) return value;

  // Go encodes a nil diagnostics slice as null. Treat that wire-level empty
  // value as the empty collection promised by ExplorerRuntimeV1.
  return {
    ...value,
    runtime: {
      ...value.runtime,
      diagnostics: [],
    },
  };
};

export const assertExplorerStateV1 = (value: unknown): ExplorerStateV1 => {
  const normalized = normalizeExplorerStateV1(value);
  const parsed = explorerStateV1Schema.safeParse(normalized);
  if (parsed.success) return parsed.data;
  const hasLegacyConfiguration =
    isRecord(value) &&
    Object.keys(value).some((key) => legacyExplorerStateKeys.has(key));
  if (hasLegacyConfiguration)
    throw new Error(
      'Loom returned an invalid ExplorerStateV1 response; legacy Explorer configuration fields are not supported.',
    );
  throw new Error('Loom returned an invalid ExplorerStateV1 response.');
};
