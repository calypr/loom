import { z } from 'zod';
/** Selector identity for published dataframe reads. */
export interface DataframeSelector {
  readonly recipe: string;
  readonly translationVersion: string;
  readonly output: string;
}

export const EXPLORER_AUTHORING_API_VERSION =
  'loom.calypr.org/explorer-authoring/v2' as const;
export const EXPLORER_AUTHORING_SEMANTICS_VERSION = 4;

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
      'codingBySystem',
      'observationComponentByCode',
    ]),
    lookup: z.object({
      match: opaqueIdSchema,
      path: opaqueIdSchema.optional(),
      projectionMode: projectionModeSchema.optional(),
    }).strict(),
  }).strict(),
  z.object({
    kind: z.enum(['codingBySystem', 'observationComponentByCode']),
    lookup: z.object({
      binding: correlatedBindingSchema,
      key: z.object({ system: opaqueIdSchema, code: opaqueIdSchema }).strict(),
      projectionMode: projectionModeSchema.optional(),
    }).strict(),
  }).strict(),
]);
const aggregateColumnSourceSchema = z
  .object({
    kind: z.literal('aggregate'),
    aggregate: z.object({
      operation: z.enum([
        'COUNT',
        'COUNT_DISTINCT',
        'DISTINCT_VALUES',
        'MIN',
        'MAX',
        'EXISTS',
        'CONTAINS_ALL',
      ]),
      path: opaqueIdSchema.optional(),
      requiredValues: z.array(z.string()).optional(),
    }).strict(),
  })
  .strict();
const contributorValueSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('STRING'), string: z.string() }).strict(),
  z.object({
    kind: z.literal('CODE'),
    code: z.object({ code: z.string().min(1) }).strict(),
  }).strict(),
]);
const contributorPredicateSchema = z
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
  aggregateColumnSourceSchema,
  z.object({ kind: z.literal('projectId') }).strict(),
]);
export type ExplorerColumnSource = z.infer<typeof explorerColumnSourceSchema>;
export type ExplorerBuilderRouteNode = {
  occurrenceId: string;
  resourceType: string;
  relationship?: string;
  matchMode?: 'OPTIONAL' | 'REQUIRED';
  children?: ExplorerBuilderRouteNode[];
};
const explorerPopulationStepSchema = z.object({
  resourceType: opaqueIdSchema,
  relationship: opaqueIdSchema,
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
        relationship: z.string().optional(),
        matchMode: z.enum(['OPTIONAL', 'REQUIRED']).optional(),
        children: z.array(explorerBuilderRouteNodeSchema).optional(),
      })
      .strict(),
  );
export const explorerBuilderColumnSchema = z
  .object({
    column: opaqueIdSchema.regex(/^[A-Za-z_][A-Za-z0-9_]*$/),
    label: z.string().min(1),
    logicalType: z.string().optional(),
    occurrenceId: opaqueIdSchema,
    source: explorerColumnSourceSchema,
    contributor: contributorPredicateSchema.optional(),
    table: explorerTablePresentationSchema.optional(),
    filter: explorerFilterPresentationSchema.optional(),
    chart: explorerChartPresentationSchema.optional(),
  })
  .strict();
export type ExplorerBuilderColumn = z.infer<typeof explorerBuilderColumnSchema>;
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
    columns: z.array(explorerBuilderColumnSchema),
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
  .strict();
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
export const explorerBuilderCandidateSchema = z
  .object({
    candidateId: opaqueIdSchema,
    nodeId: opaqueIdSchema,
    fieldPath: opaqueIdSchema,
    label: z.string(),
    logicalType: opaqueIdSchema,
    repeated: z.boolean().optional(),
    filterable: z.boolean(),
    chartable: z.boolean(),
    projectionModes: z.array(projectionModeSchema).min(1),
    defaultProjectionMode: projectionModeSchema,
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
      'SET_COLUMN_CONTRIBUTOR',
      'CLEAR_COLUMN_CONTRIBUTOR',
      'REMOVE_COLUMN',
    ]),
    outputId: opaqueIdSchema.optional(),
    sourceOutputId: opaqueIdSchema.optional(),
    title: z.string().optional(),
    rootNodeId: opaqueIdSchema.optional(),
    selectionRevisionId: opaqueIdSchema.optional(),
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
    contributor: contributorPredicateSchema.optional(),
    source: explorerColumnSourceSchema.optional(),
    rowChange: rowChangeProposalSchema.optional(),
    outputIds: z.array(opaqueIdSchema).optional(),
  })
  .strict();
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
    ]),
    outputId: opaqueIdSchema.optional(),
    tabId: opaqueIdSchema.optional(),
    occurrenceId: opaqueIdSchema.optional(),
    column: opaqueIdSchema.optional(),
  })
  .strict();
export const explorerBuilderCommandsResultSchema = z
  .object({
    commandId: opaqueIdSchema,
    workspace: explorerBuilderWorkspaceSchema,
    draftVersion: z.number().int().positive(),
    draftDigest: opaqueIdSchema,
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
