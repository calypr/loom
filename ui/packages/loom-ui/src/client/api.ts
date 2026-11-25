import type { ExplorerAuthoringApiError } from '../contracts/errors';
import { sameConstructionRoute, sameSemanticItemIdentity, semanticIdentityForItem, semanticItemMatchesIdentity } from '../contracts/identity';
import { isRecord } from '../contracts/guards';
import { viewBuilderStateSchema, type ProjectViewBuilderState, type SaveProjectViewConfigRequest, type SaveProjectViewDefaultsRequest } from '../contracts/projectViewBuilder';

export interface ProjectViewBuilderArgs { readonly project: string; readonly authResourcePath?: string }
export interface SaveProjectViewConfigArgs extends ProjectViewBuilderArgs { readonly configId: string; readonly request: SaveProjectViewConfigRequest }
export interface DeleteProjectViewConfigArgs extends ProjectViewBuilderArgs { readonly configId: string; readonly expectedRevisionId: string }
export interface SaveProjectViewDefaultsArgs extends ProjectViewBuilderArgs { readonly request: SaveProjectViewDefaultsRequest }

import {
  EXPLORER_AUTHORING_SEMANTICS_VERSION,
  SEMANTIC_FIELD_PRESENTATION_REVISION,
  assertExplorerBuilderCompileResult,
  assertExplorerBuilderPreviewResult,
  assertExplorerBuilderPublishResult,
  assertExplorerBuilderState,
  assertExplorerStateV1,
  explorerAuthoringCapabilitiesSchema,
  explorerErrorResponseSchema,
  explorerBuilderCommandsResultSchema,
  explorerBuilderSuggestionsResultSchema,
  explorerBuilderSchemaFieldsResultSchema,
  type ExplorerBuilderSchemaFieldsResult,
  explorerColumnSourceDescriptorSchema,
  constructionChoiceSearchResponseSchema,
  relatedExpandChoiceSearchResponseSchema,
  relatedExpandContributorSearchResponseSchema,
  relatedFieldChoiceSearchResponseSchema,
  constructionInputsRequestSchema,
  constructionInputsResponseSchema,
  constructionCapabilitiesRequestSchema,
  constructionCapabilitiesResponseSchema,
  constructionColumnPopulationRequestSchema,
  constructionColumnPopulationResponseSchema,
  constructionCategoryDiscoveryRequestSchema,
  constructionCategoryDiscoveryResponseSchema,
  authoringNetworkRequestSchema,
  authoringNetworkResponseSchema,
  type AuthoringNetworkRequest,
  type AuthoringNetworkResponse,
  columnPolicyProposalRequestSchema, columnPolicyProposalSchema, type ColumnPolicyProposalRequest, type ColumnPolicyProposal,
  sourceRelationshipProposalRequestSchema,
  type SourceRelationshipProposalRequest,
  sourceRelationshipProposalWithPreviewSchema,
  type SourceRelationshipProposalWithPreview,
  constructionProposalRequestSchema,
  constructionProposalResponseSchema,
  constructionChoiceProposalRequestSchema,
  constructionChoiceProposalResponseSchema,
  populationRoutesResponseSchema,
  rowDefinitionChoicesResponseSchema,
  rowDefinitionProposalSchema,
  fieldPresentationSchema,
  explicitGroupCreateRequestSchema,
  explicitGroupRevisionSummarySchema,
  explorerBuilderPreviewResultSchema,
  rowChangeAssessmentSchema,
  semanticInventoryBrowseCoreResponseSchema,
  semanticInventoryBrowseRequestSchema,
  frameSourceOptionsResponseSchema,
  explorerBuilderRowLineageResponseSchema,
  type ExplorerBuilderCatalog,
  type ExplorerBuilderCommand,
  type ExplorerBuilderCompileResult,
  type ExplorerBuilderPreviewResult,
  type ExplorerBuilderState,
  type ExplorerBuilderSuggestionsResult,
  type ExplorerBuilderWorkspace,
  type ExplorerColumnSourceDescriptor,
  type ConstructionChoiceSearchResponse,
  type ConstructionChoiceSearchSource,
  type RelatedExpandChoiceSearchResponse,
  type RelatedExpandContributorSearchResponse,
  type RelatedFieldChoiceSearchResponse,
  type ConstructionInputsResponse,
  type ConstructionCapabilitiesResponse,
  type ConstructionColumnPopulationResponse,
  type ConstructionCategoryDiscoveryResponse,
  type ConstructionProposalRequest,
  type ConstructionProposalResponse,
  type ConstructionChoiceProposalRequest,
  type ConstructionChoiceProposalResponse,
  type PopulationRoutesResponse,
  type RowDefinitionChoicesResponse,
  type RowDefinitionProposal,
  type RowDefinitionSelection,
  type ExplicitGroupCreateRequest,
  type ExplicitGroupRevisionSummary,
  type ExplorerRuntimeV1,
  type RowChangeAssessment,
  type RowChangeMode,
  type SemanticInventoryBrowseResponse,
  type SemanticInventoryBrowseRequest,
  type FieldPresentation,
  type FieldPresentationRequest,
  type FieldPresentationContext,
  type FieldPresentationReason,
  type SemanticInventoryItem,
  type SemanticInventoryItemIdentity,
  type FrameSourceOptionsResponse,
  type ExplorerBuilderRowLineageResponse,
} from '../contracts/types';
import type { ExplorerAuthoringDiagnostic } from '../contracts/types';
import {
  configuredColumnContextResponseSchema,
  interpretationLibraryListResponseSchema,
  interpretationPreviewResponseSchema,
  interpretationRevisionSchema,
  type InterpretationApplicability,
  type ConfiguredColumnContextResponse,
  type InterpretationLibraryView,
  type InterpretationPreviewResponse,
  type InterpretationRule,
  type InterpretationRevision,
} from '../contracts/interpretation';
import { z } from 'zod';
import { cellTraceResponseSchema, type CellTraceResponse } from '../contracts/cellTrace';
import {
  selectionPageSchema,
  selectionRevisionSchema,
  resourceRefSchema,
  type LoomOutputFilter,
  type ResourceRef,
  type SelectionPage,
  type SelectionRevision,
  type SelectionSourceIntent,
} from '../contracts/selection';

export interface ExplorerSummary {
  readonly project: string;
  readonly explorerId: string;
  readonly title: string;
  readonly management: string;
  readonly activeRevisionId?: string;
  readonly updatedAt: string;
}

export type { ExplorerAuthoringApiError } from '../contracts/errors';

export interface ExplorerAuthoringStateArgs {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
}

export interface ExplorerAuthoringProjectArgs {
  readonly project: string;
  readonly authResourcePath?: string;
}

interface ApplyExplorerBuilderCommandsArgs extends ExplorerAuthoringStateArgs {
  readonly commandId: string;
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest?: string;
  readonly commands: ReadonlyArray<ExplorerBuilderCommand>;
  readonly requestId?: string;
}

interface AssessExplorerRowChangeArgs extends ExplorerAuthoringStateArgs {
  readonly mode?: RowChangeMode;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly outputId: string;
  readonly rootNodeId: string;
  readonly rootOccurrenceId?: string;
  readonly routeRebase?: ReadonlyArray<{
    readonly occurrenceId: string;
    readonly edgeId: string;
  }>;
  readonly requestId?: string;
}

interface ReconcileExplorerBuilderArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly outputId?: string;
  readonly requestId?: string;
}

export interface PreviewExplorerBuilderArgs extends ExplorerAuthoringStateArgs {
  readonly receiptId: string;
  readonly outputId: string;
  readonly limit?: number;
  readonly requestId?: string;
}

interface PopulationMappingArgs extends ExplorerAuthoringStateArgs {
  readonly receiptId: string;
  readonly outputId: string;
  readonly cursor?: string;
  readonly limit?: number;
}

export interface CellTraceArgs extends ExplorerAuthoringStateArgs {
  readonly receiptId: string;
  readonly outputId: string;
  readonly rowId: string;
  readonly column: string;
  readonly offset?: number;
  readonly limit?: number;
}

interface RowLineageArgs extends ExplorerAuthoringStateArgs {
  readonly receiptId: string;
  readonly outputId: string;
  readonly rowId: string;
  readonly offset?: number;
  readonly limit?: number;
}

interface PrepareArtifactArgs extends ExplorerAuthoringStateArgs {
  readonly revisionId: string;
  readonly outputId: string;
  readonly idempotencyKey: string;
}

interface DownloadArtifactArgs extends ExplorerAuthoringStateArgs {
  readonly artifactId: string;
}

interface CreateInterpretationRevisionArgs extends ExplorerAuthoringProjectArgs {
  readonly libraryId: string;
  readonly parentRevisionId?: string;
  readonly applicability: InterpretationApplicability;
  readonly rules: ReadonlyArray<InterpretationRule>;
  readonly explanation: string;
  readonly requestId?: string;
}

export interface PreviewInterpretationCandidateArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly outputId: string;
  readonly column: string;
  readonly revisionId: string;
  readonly limit?: number;
  readonly requestId?: string;
}

export interface ResolveConfiguredColumnContextsArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly requestId?: string;
}

interface CreateInterpretationRevisionFromColumnArgs extends ResolveConfiguredColumnContextsArgs {
  readonly outputId: string;
  readonly column: string;
  readonly libraryId: string;
  readonly parentRevisionId?: string;
  readonly explanation: string;
}

const populationMappingDiagnosticSchema = z.object({
  severity: z.string(),
  stage: z.string(),
  code: z.string(),
  message: z.string(),
}).strict();
const populationMappingCountsSchema = z.object({
  selected: z.number().int().nonnegative(),
  mapped: z.number().int().nonnegative(),
  unmapped: z.number().int().nonnegative(),
  emittedRows: z.number().int().nonnegative(),
}).strict();
const populationMappingBindingSchema = z.object({
  receiptId: z.string().min(1),
  outputId: z.string().min(1),
  project: z.string().min(1),
  explorerId: z.string().min(1),
  generation: z.string().min(1),
  scopeDigest: z.string().min(1),
  selectionRevisionId: z.string().min(1),
  membershipDigest: z.string().min(1),
  resourceType: z.string().min(1),
}).strict();
const populationMappingResponseSchema = z.object({
  binding: populationMappingBindingSchema,
  status: z.enum(['COMPLETE', 'INCOMPLETE']),
  counts: populationMappingCountsSchema.nullable().optional(),
  unmapped: z.array(resourceRefSchema),
  nextCursor: z.string().min(1).optional(),
  diagnostics: z.array(populationMappingDiagnosticSchema),
}).strict();
export type PopulationMappingResponse = z.infer<typeof populationMappingResponseSchema>;

const artifactSchema = z.object({
  id: z.string().regex(/^artifact_[0-9a-f]{64}$/),
  project: z.string().min(1),
  explorerId: z.string().min(1),
  revisionId: z.string().min(1),
  outputId: z.string().min(1),
  receiptId: z.string().min(1),
  executionId: z.string().min(1),
  datasetGeneration: z.string().min(1),
  schemaDigest: z.string().min(1),
  state: z.literal('COMPLETE'),
  format: z.enum(['CSV', 'JSONL']),
  filename: z.string().min(1),
  mediaType: z.literal('application/zip'),
  archiveSha256: z.string().regex(/^[0-9a-f]{64}$/),
  bytes: z.number().int().positive(),
  rows: z.number().int().nonnegative(),
  features: z.number().int().nonnegative(),
  createdAt: z.string().datetime(),
  completedAt: z.string().datetime(),
  expiresAt: z.string().datetime(),
}).strict();
export type Artifact = z.infer<typeof artifactSchema>;

interface PublishExplorerBuilderArgs extends ExplorerAuthoringStateArgs {
  readonly receiptId: string;
  readonly requestId?: string;
}

interface ExplorerCandidateSuggestionsArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly nodeId: string;
  readonly query?: string;
  readonly requestId?: string;
}

interface SearchSchemaFieldsArgs extends ExplorerCandidateSuggestionsArgs {
  readonly cursor?: string;
  readonly limit?: number;
}

export type BrowseSemanticInventoryArgs = ExplorerAuthoringStateArgs &
  SemanticInventoryBrowseRequest & {
  readonly requestId?: string;
  };

interface BrowseFrameSourceOptionsArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly resourceType?: string;
  readonly query?: string;
  readonly cursor?: string;
  readonly limit?: number;
  readonly requestId?: string;
}

interface InspectColumnSourceArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly column: string;
  readonly requestId?: string;
}

interface SearchConstructionChoicesArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly occurrenceId?: string;
  readonly source: ConstructionChoiceSearchSource;
  readonly limit?: number;
  readonly cursor?: string;
  readonly requestId?: string;
}

interface SearchRelatedExpandChoicesArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly outputId: string;
  readonly stageId: string;
  readonly anchorColumnId: string;
  readonly targetResourceType: string;
  readonly limit?: number;
  readonly cursor?: string;
  readonly savedGraphOnly?: boolean;
  readonly requestId?: string;
}

interface SearchRelatedFieldChoicesArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly outputId: string;
  readonly stageId: string;
  readonly query?: string;
  readonly limit?: number;
  readonly cursor?: string;
  readonly requestId?: string;
}

interface SearchRelatedExpandContributorsArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly outputId: string;
  readonly stageId: string;
  readonly routeChoiceId: string;
  readonly query?: string;
  readonly limit?: number;
  readonly cursor?: string;
  readonly requestId?: string;
}

interface SearchPopulationRoutesArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly selectionRevisionId: string;
  readonly limit?: number;
  readonly cursor?: string;
  readonly requestId?: string;
}

interface ListRowDefinitionChoicesArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly outputId: string;
}

interface ProposeRowDefinitionArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly outputId: string;
  readonly selection: RowDefinitionSelection;
  readonly limit?: number;
  readonly requestId?: string;
}

type ConstructionAuthoringStateArgs = ExplorerAuthoringStateArgs & {
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly outputId: string;
  readonly requestId?: string;
};

export interface GetConstructionCapabilitiesArgs extends ConstructionAuthoringStateArgs {
  readonly stageId: string;
  readonly groupStepId?: string;
  readonly includeGroupChoices?: boolean;
}

interface GetConstructionColumnPopulationArgs extends ConstructionAuthoringStateArgs {
  readonly stageId: string;
  readonly columnIds: string[];
}

export interface DiscoverConstructionCategoriesArgs extends ConstructionAuthoringStateArgs {
  readonly groupKeyIds?: string[];
  readonly pivotStepId?: string;
  readonly pivotSources?: ConstructionProposalRequest['pivotSources'];
  readonly candidateConstruction?: ConstructionProposalRequest['candidateConstruction'];
  readonly stageId: string;
  readonly categoryColumnId: string;
  readonly valueColumnId: string;
}

interface GetConstructionInputsArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly query?: string;
  readonly cursor?: string;
  readonly limit?: number;
  readonly requestId?: string;
}

type DescribeAuthoringNetworkArgs = ExplorerAuthoringStateArgs & AuthoringNetworkRequest;

type ProposeColumnPolicyArgs = ExplorerAuthoringStateArgs & ColumnPolicyProposalRequest & { readonly requestId?: string };

type ProposeSourceRelationshipArgs = ExplorerAuthoringStateArgs & SourceRelationshipProposalRequest & { readonly requestId?: string };

export type ProposeConstructionArgs = ExplorerAuthoringStateArgs &
  ConstructionProposalRequest & { readonly requestId?: string };

type ProposeConstructionChoicesArgs = ExplorerAuthoringStateArgs &
  ConstructionChoiceProposalRequest & { readonly requestId?: string };

interface CreateExplicitGroupRevisionArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly selectionRevision: string;
  readonly idempotencyKey: string;
  readonly groups: ExplicitGroupCreateRequest['groups'];
  readonly requestId?: string;
}

interface CreateExplorerArgs extends ExplorerAuthoringProjectArgs {
  readonly name: string;
  readonly title?: string;
  readonly sourceExplorerId?: string;
  readonly requestId?: string;
}

interface DeleteExplorerArgs extends ExplorerAuthoringStateArgs {
  readonly requestId?: string;
}

export interface CreateSelectionArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly idempotencyKey: string;
  readonly source: SelectionSourceIntent;
  readonly exclusions?: ReadonlyArray<ResourceRef>;
  readonly requestId?: string;
}

export interface GetSelectionArgs extends ExplorerAuthoringStateArgs {
  readonly selectionRevision: string;
  readonly cursor?: string;
  readonly limit?: number;
}

interface ProposePopulationMemberRemovalArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly outputId: string;
  readonly baseSelectionRevisionId: string;
  readonly removedMember: ResourceRef;
  readonly limit?: number;
  readonly requestId?: string;
}

const populationMemberRemovalProposalResponseSchema = z.object({
  proposalId: z.string().min(1),
  outputId: z.string().min(1),
  snapshotToken: z.string().min(1),
  draftVersion: z.number().int().positive(),
  draftDigest: z.string().min(1),
  baseDocumentDigest: z.string().min(1),
  candidateWorkspaceDigest: z.string().min(1),
  baseSelection: selectionRevisionSchema,
  candidateSelection: selectionRevisionSchema,
  removedMember: resourceRefSchema,
  previewStatus: z.literal('READY'),
  previewDurationMs: z.number().int().nonnegative(),
  preview: explorerBuilderPreviewResultSchema,
}).strict().superRefine((response, context) => {
  if (response.preview.receiptId !== response.proposalId || response.preview.outputId !== response.outputId) {
    context.addIssue({
      code: 'custom',
      path: ['preview'],
      message: 'Population member preview must belong to its exact proposal and output.',
    });
  }
});
export type PopulationMemberRemovalProposalResponse = z.infer<typeof populationMemberRemovalProposalResponseSchema>;

export interface LoomClientOptions {
  readonly publishTransport?: (args: PublishExplorerBuilderArgs, signal?: AbortSignal) => Promise<unknown>;

  readonly baseUrl?: string;

  readonly fetch?: typeof globalThis.fetch;
  readonly headers?: HeadersInit;
  readonly credentials?: RequestCredentials;
}

export interface LoomRowsOptions {
  readonly project: string;
  readonly first?: number;
  readonly signal?: AbortSignal;
}

export interface LoomOutputSort {
  readonly column: string;
  readonly desc?: boolean;
}

export interface LoomFacetSpec {
  readonly name: string;
  readonly kind: 'TERMS' | 'HISTOGRAM' | 'DATE_HISTOGRAM' | 'STATS' | 'MISSING';
  readonly column: string;
  readonly size?: number;
  readonly interval?: number;
  readonly dateInterval?: number;
  readonly excludeSelfFilter?: boolean;
}

export interface LoomOutputRequest {
  readonly project: string;
  readonly selector: ExplorerRuntimeV1['outputs'][number]['selector'];
  readonly columns?: ReadonlyArray<string>;
  readonly filters?: ReadonlyArray<LoomOutputFilter>;
  readonly sort?: LoomOutputSort;
  readonly first?: number;
  readonly after?: string;
  readonly facets?: ReadonlyArray<LoomFacetSpec>;
  readonly exportHeaders?: Readonly<Record<string, string>>;
}

export interface LoomFacetResult {
  readonly name: string;
  readonly kind: string;
  readonly columns: ReadonlyArray<string>;
  readonly rows: ReadonlyArray<Record<string, unknown>>;
  readonly missingCount?: number;
  readonly truncated?: boolean;
}

export interface LoomOutputResult {
  readonly columns: ReadonlyArray<string>;
  readonly rows: ReadonlyArray<Record<string, unknown>>;
  readonly rowIds: ReadonlyArray<string>;
  readonly totalCount: number | null;
  readonly pageInfo: {
    readonly hasNextPage: boolean;
    readonly endCursor?: string;
  };
  readonly materialization?: Readonly<Record<string, unknown>>;
  readonly facets: ReadonlyArray<LoomFacetResult>;
}

export interface LoomClient {
  readonly createSelection: (args: CreateSelectionArgs, signal?: AbortSignal) => Promise<SelectionRevision>;
  readonly getSelection: (args: GetSelectionArgs, signal?: AbortSignal) => Promise<SelectionPage>;
  readonly proposePopulationMemberRemoval: (
    args: ProposePopulationMemberRemovalArgs,
    signal?: AbortSignal,
  ) => Promise<PopulationMemberRemovalProposalResponse>;
  readonly createExplicitGroupRevision: (
    args: CreateExplicitGroupRevisionArgs,
    signal?: AbortSignal,
  ) => Promise<ExplicitGroupRevisionSummary>;
  readonly listExplorers: (
    args: ExplorerAuthoringProjectArgs,
    signal?: AbortSignal,
  ) => Promise<ReadonlyArray<ExplorerSummary>>;
  readonly getBuilder: (
    args: ExplorerAuthoringStateArgs,
    options?: { readonly signal?: AbortSignal; readonly reload?: boolean },
  ) => Promise<ExplorerBuilderState>;
  readonly getProjectViewBuilder: (args: ProjectViewBuilderArgs, options?: { readonly signal?: AbortSignal; readonly reload?: boolean }) => Promise<ProjectViewBuilderState>;
  readonly saveProjectViewConfig: (args: SaveProjectViewConfigArgs, signal?: AbortSignal) => Promise<ProjectViewBuilderState>;
  readonly deleteProjectViewConfig: (args: DeleteProjectViewConfigArgs, signal?: AbortSignal) => Promise<ProjectViewBuilderState>;
  readonly saveProjectViewDefaults: (args: SaveProjectViewDefaultsArgs, signal?: AbortSignal) => Promise<ProjectViewBuilderState>;
  readonly adoptProjectViewBuilderCatalogs: (args: ProjectViewBuilderArgs, signal?: AbortSignal) => Promise<ProjectViewBuilderState>;
  readonly getCapability: (
    args: ExplorerAuthoringStateArgs,
    signal?: AbortSignal,
  ) => Promise<ReturnType<typeof explorerAuthoringCapabilitiesSchema.parse>>;
  readonly getExplorer: (
    args: ExplorerAuthoringStateArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerRuntimeV1>;
  readonly applyCommands: (
    args: ApplyExplorerBuilderCommandsArgs,
    signal?: AbortSignal,
  ) => Promise<ReturnType<typeof explorerBuilderCommandsResultSchema.parse>>;
  readonly assessRowChange: (
    args: AssessExplorerRowChangeArgs,
    signal?: AbortSignal,
  ) => Promise<RowChangeAssessment>;
  readonly reconcile: (
    args: ReconcileExplorerBuilderArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerBuilderCompileResult>;
  readonly suggestions: (
    args: ExplorerCandidateSuggestionsArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerBuilderSuggestionsResult>;
  readonly searchSchemaFields?: (
    args: SearchSchemaFieldsArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerBuilderSchemaFieldsResult>;
  readonly browseSemanticInventory: (
    args: BrowseSemanticInventoryArgs,
    signal?: AbortSignal,
  ) => Promise<SemanticInventoryBrowseResponse>;
  readonly browseFrameSourceOptions: (
    args: BrowseFrameSourceOptionsArgs,
    signal?: AbortSignal,
  ) => Promise<FrameSourceOptionsResponse>;
  readonly inspectColumnSource: (
    args: InspectColumnSourceArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerColumnSourceDescriptor>;
  readonly searchConstructionChoices: (
    args: SearchConstructionChoicesArgs,
    signal?: AbortSignal,
  ) => Promise<ConstructionChoiceSearchResponse>;
  readonly searchRelatedExpandChoices: (
    args: SearchRelatedExpandChoicesArgs,
    signal?: AbortSignal,
  ) => Promise<RelatedExpandChoiceSearchResponse>;
  readonly searchRelatedExpandContributors: (
    args: SearchRelatedExpandContributorsArgs,
    signal?: AbortSignal,
  ) => Promise<RelatedExpandContributorSearchResponse>;
  readonly searchRelatedFieldChoices: (
    args: SearchRelatedFieldChoicesArgs,
    signal?: AbortSignal,
  ) => Promise<RelatedFieldChoiceSearchResponse>;
  readonly searchPopulationRoutes: (
    args: SearchPopulationRoutesArgs,
    signal?: AbortSignal,
  ) => Promise<PopulationRoutesResponse>;
  readonly listRowDefinitionChoices: (
    args: ListRowDefinitionChoicesArgs,
    signal?: AbortSignal,
  ) => Promise<RowDefinitionChoicesResponse>;
  readonly proposeRowDefinition: (
    args: ProposeRowDefinitionArgs,
    signal?: AbortSignal,
  ) => Promise<RowDefinitionProposal>;
  readonly getConstructionCapabilities: (
    args: GetConstructionCapabilitiesArgs,
    signal?: AbortSignal,
  ) => Promise<ConstructionCapabilitiesResponse>;
  readonly getConstructionColumnPopulation: (
    args: GetConstructionColumnPopulationArgs,
    signal?: AbortSignal,
  ) => Promise<ConstructionColumnPopulationResponse>;
  readonly discoverConstructionCategories: (
    args: DiscoverConstructionCategoriesArgs,
    signal?: AbortSignal,
  ) => Promise<ConstructionCategoryDiscoveryResponse>;
  readonly getConstructionInputs: (
    args: GetConstructionInputsArgs,
    signal?: AbortSignal,
  ) => Promise<ConstructionInputsResponse>;
  readonly describeAuthoringNetwork: (args: DescribeAuthoringNetworkArgs, signal?: AbortSignal) => Promise<AuthoringNetworkResponse>;
  readonly proposeColumnPolicy: (args: ProposeColumnPolicyArgs, signal?: AbortSignal) => Promise<ColumnPolicyProposal>;
  readonly proposeSourceRelationship: (args: ProposeSourceRelationshipArgs, signal?: AbortSignal) => Promise<SourceRelationshipProposalWithPreview>;
  readonly proposeConstruction: (
    args: ProposeConstructionArgs,
    signal?: AbortSignal,
  ) => Promise<ConstructionProposalResponse>;
  readonly proposeConstructionChoices: (
    args: ProposeConstructionChoicesArgs,
    signal?: AbortSignal,
  ) => Promise<ConstructionChoiceProposalResponse>;
  readonly preview: (
    args: PreviewExplorerBuilderArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerBuilderPreviewResult>;
  readonly populationMapping: (
    args: PopulationMappingArgs,
    signal?: AbortSignal,
  ) => Promise<PopulationMappingResponse>;
  readonly cellTrace: (
    args: CellTraceArgs,
    signal?: AbortSignal,
  ) => Promise<CellTraceResponse>;
  readonly rowLineage: (
    args: RowLineageArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerBuilderRowLineageResponse>;
  readonly prepareArtifact: (
    args: PrepareArtifactArgs,
    signal?: AbortSignal,
  ) => Promise<Artifact>;

  readonly artifactDownloadURL: (args: DownloadArtifactArgs) => string;
  readonly listInterpretationLibraries: (
    args: ExplorerAuthoringProjectArgs,
    signal?: AbortSignal,
  ) => Promise<ReadonlyArray<InterpretationLibraryView>>;
  readonly getInterpretationRevision: (
    args: { readonly project: string; readonly revisionId: string },
    signal?: AbortSignal,
  ) => Promise<InterpretationRevision>;
  readonly createInterpretationRevision: (
    args: CreateInterpretationRevisionArgs,
    signal?: AbortSignal,
  ) => Promise<InterpretationRevision>;
  readonly resolveConfiguredColumnContexts: (
    args: ResolveConfiguredColumnContextsArgs,
    signal?: AbortSignal,
  ) => Promise<ConfiguredColumnContextResponse>;
  readonly createInterpretationRevisionFromColumn: (
    args: CreateInterpretationRevisionFromColumnArgs,
    signal?: AbortSignal,
  ) => Promise<InterpretationRevision>;
  readonly previewInterpretationCandidate: (
    args: PreviewInterpretationCandidateArgs,
    signal?: AbortSignal,
  ) => Promise<InterpretationPreviewResponse>;
  readonly publish: (
    args: PublishExplorerBuilderArgs,
    signal?: AbortSignal,
  ) => Promise<ReturnType<typeof assertExplorerBuilderPublishResult>>;
  readonly createExplorer: (
    args: CreateExplorerArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerSummary>;
  readonly deleteExplorer: (
    args: DeleteExplorerArgs,
    signal?: AbortSignal,
  ) => Promise<null>;
  readonly rows: (
    selector: ExplorerRuntimeV1['outputs'][number]['selector'],
    columns: ReadonlyArray<string>,
    options: LoomRowsOptions,
  ) => Promise<{ readonly columns: ReadonlyArray<string>; readonly rows: ReadonlyArray<Record<string, unknown>>; readonly totalCount?: number | null }>;
  readonly queryOutput: (
    request: LoomOutputRequest,
    signal?: AbortSignal,
  ) => Promise<LoomOutputResult>;
  readonly exportOutput: (
    request: LoomOutputRequest,
    signal?: AbortSignal,
  ) => Promise<Blob>;
  readonly invalidate: (scope?: 'explorers' | 'builder' | 'interpretations' | 'all') => void;
}

export const canonicalProject = (project: string): string => {
  let value = project.trim();
  try {
    value = decodeURIComponent(value);
  } catch {
    // Leave malformed URL input for the server to reject with its contract.
  }
  value = value.replace(/^\/+|\/+$/g, '');
  if (value.includes('/')) {
    const separator = value.indexOf('/');
    const program = value.slice(0, separator);
    const projectName = value.slice(separator + 1);
    return program && projectName ? `${program}/${projectName}` : value;
  }
  const separator = value.indexOf('-');
  if (separator > 0 && separator < value.length - 1) {
    const program = value.slice(0, separator);
    if (program !== program.toLowerCase() || program.includes('_')) {
      return `${program}/${value.slice(separator + 1)}`;
    }
  }
  return value;
};

export const encodeLoomProjectPath = (project: string): string =>
  encodeURIComponent(encodeURIComponent(canonicalProject(project)));
const encodedProject = encodeLoomProjectPath;
export const projectViewBuilderPath = (project: string, authResourcePath?: string): string => {
  const value = authResourcePath?.trim();
  const query = value ? `?${new URLSearchParams({ auth_resource_path: value }).toString()}` : '';
  return `/api/v1/projects/${encodedProject(project)}/view-builder${query}`;
};
export const getProjectViewBuilderState = async (get: (path: string) => Promise<unknown>, project: string, authResourcePath?: string): Promise<ProjectViewBuilderState> => {
  const response = await get(projectViewBuilderPath(project, authResourcePath));
  const payload = typeof response === 'object' && response !== null && 'data' in response
    ? response.data
    : response;
  return viewBuilderStateSchema.parse(payload);
};


const parseJSON = async (response: Response): Promise<unknown> => {
  const text = await response.text();
  if (!text) return {};
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return { message: text };
  }
};

const diagnosticsFrom = (value: unknown): ReadonlyArray<ExplorerAuthoringDiagnostic> =>
  Array.isArray(value) ? value.filter(isRecord).flatMap((item) => {
    if (
      typeof item.code !== 'string' ||
      typeof item.message !== 'string' ||
      (item.severity !== 'error' && item.severity !== 'warning' && item.severity !== 'info')
    ) return [];
    return [{
      severity: item.severity,
      code: item.code,
      message: item.message,
      ...(typeof item.stage === 'string' ? { stage: item.stage } : {}),
      ...(typeof item.path === 'string' || item.path === null
        ? { path: item.path }
        : typeof item.jsonPath === 'string' || item.jsonPath === null
          ? { path: item.jsonPath }
          : {}),
      ...(typeof item.fieldPath === 'string' || item.fieldPath === null ? { fieldPath: item.fieldPath } : {}),
      ...(typeof item.requestId === 'string' ? { requestId: item.requestId } : {}),
    }];
  }) : [];

const requestError = async (
  response: Response,
  callerRequestId?: string,
): Promise<ExplorerAuthoringApiError> => {
  const payload = await parseJSON(response);
  const record = isRecord(payload) ? payload : {};
  const parsedEnvelope = explorerErrorResponseSchema.safeParse(payload);
  const nested: Record<string, unknown> = parsedEnvelope.success
    ? parsedEnvelope.data.error
    : isRecord(record.error) ? record.error : record;
  const code = typeof nested.code === 'string' ? nested.code : undefined;
  const message = typeof nested.message === 'string'
    ? nested.message
    : response.status === 504
      ? 'Loom timed out before returning a result. Retry, or narrow the preview.'
      : response.status >= 500
        ? `Loom could not complete this request (HTTP ${response.status}). Retry and include the request ID if it continues.`
        : `Loom request failed (${response.status}).`;
  const responseRequestId = response.headers.get('x-request-id') ?? response.headers.get('request-id');
  const payloadRequestId = typeof nested.requestId === 'string'
    ? nested.requestId
    : typeof record.requestId === 'string' ? record.requestId : undefined;
  const details = isRecord(nested.details) ? nested.details : undefined;
  const explicitRetryable = typeof details?.retryable === 'boolean' ? details.retryable : undefined;
  return {
    status: response.status,
    code,
    message,
    diagnostics: diagnosticsFrom(parsedEnvelope.success
      ? parsedEnvelope.data.diagnostics ?? parsedEnvelope.data.error.diagnostics ?? (parsedEnvelope.data.error.diagnostic ? [parsedEnvelope.data.error.diagnostic] : undefined)
      : record.diagnostics ?? nested.diagnostics),
    requestId: responseRequestId?.trim() || payloadRequestId?.trim() || callerRequestId,
    details,
    retryable: explicitRetryable ?? (response.status === 408 || response.status === 429 || response.status >= 500),
  };
};

export class LoomRequestError extends Error implements ExplorerAuthoringApiError {
  readonly info: ExplorerAuthoringApiError;
  readonly status: ExplorerAuthoringApiError['status'];
  readonly code?: string;
  readonly diagnostics?: ReadonlyArray<ExplorerAuthoringDiagnostic>;
  readonly requestId?: string;
  readonly details?: Readonly<Record<string, unknown>>;
  readonly retryable?: boolean;
  constructor(info: ExplorerAuthoringApiError) {
    super(info.message);
    this.name = 'LoomRequestError';
    this.info = info;
    this.status = info.status;
    this.code = info.code;
    this.diagnostics = info.diagnostics;
    this.requestId = info.requestId;
    this.details = info.details;
    this.retryable = info.retryable;
  }
}

const dataframeRowSchema = z.union([
  z.array(z.unknown()),
  z.record(z.string(), z.unknown()),
]);
type DataframeRow = z.infer<typeof dataframeRowSchema>;
const dataframeFacetSchema = z.object({
  name: z.string(),
  kind: z.string(),
  columns: z.array(z.string()),
  rows: z.array(dataframeRowSchema),
  missingCount: z.number().finite().nullable().optional(),
  truncated: z.boolean().optional(),
}).passthrough();
type DataframeFacet = z.infer<typeof dataframeFacetSchema>;
const dataframeMaterializationSchema = z.object({
  id: z.string().optional(),
  name: z.string().optional(),
  revision: z.string().optional(),
  projectId: z.string().optional(),
  datasetGeneration: z.string().optional(),
  state: z.string().optional(),
  rowCount: z.number().int().nonnegative().nullable().optional(),
  selector: z.object({
    recipe: z.string(),
    translationVersion: z.string(),
    output: z.string(),
  }).strict().nullable().optional(),
}).passthrough();
const dataframeConnectionSchema = z.object({
  materialization: dataframeMaterializationSchema.optional(),
  columns: z.array(z.string()),
  rows: z.array(dataframeRowSchema),
  rowIds: z.array(z.string()).optional().transform((value) => value ?? []),
  totalCount: z.number().int().nonnegative().nullable(),
  pageInfo: z.object({
    hasNextPage: z.boolean(),
    endCursor: z.string().nullable().optional(),
  }).strict(),
}).passthrough();
const dataframeAggregationsSchema = z.object({
  materialization: dataframeMaterializationSchema,
  aggregations: z.array(dataframeFacetSchema),
}).passthrough();

const shapeRows = (rows: ReadonlyArray<DataframeRow>, columns: ReadonlyArray<string>): Array<Record<string, unknown>> => {
  return rows.map((row) => {
    const source = Array.isArray(row)
      ? Object.fromEntries(columns.map((column, index) => [column, row[index]]))
      : row;
    const result: Record<string, unknown> = {};
    Object.entries(source).forEach(([key, value]) => {
      const parts = key.split('.').filter(Boolean);
      if (parts.length < 2) {
        result[key] = value;
        return;
      }
      let cursor = result;
      parts.slice(0, -1).forEach((part) => {
        const nested = cursor[part];
        if (!isRecord(nested)) {
          const next: Record<string, unknown> = {};
          cursor[part] = next;
          cursor = next;
        } else {
          cursor = nested;
        }
      });
      cursor[parts[parts.length - 1]] = value;
    });
    return result;
  });
};

const numberOrNull = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? value : null;

const normalizedFacet = (value: DataframeFacet): LoomFacetResult => {
  const columns = value.columns;
  const rows = shapeRows(value.rows, columns);
  const missingCount = numberOrNull(value.missingCount);
  return {
    name: value.name,
    kind: value.kind,
    columns,
    rows,
    ...(missingCount === null ? {} : { missingCount }),
    ...(typeof value.truncated === 'boolean' ? { truncated: value.truncated } : {}),
  };
};

const materializationIdentity = (
  value: Readonly<Record<string, unknown>>,
): string | undefined => {
  const selector = isRecord(value.selector)
    ? {
        recipe: value.selector.recipe,
        translationVersion: value.selector.translationVersion,
        output: value.selector.output,
      }
    : undefined;
  const identity = {
    id: value.id,
    revision: value.revision,
    projectId: value.projectId,
    datasetGeneration: value.datasetGeneration,
    selector,
  };
  return Object.values(identity).every((part) => part !== undefined)
    ? JSON.stringify(identity)
    : undefined;
};

const presentationUnavailable = (reason: FieldPresentationReason): FieldPresentation => ({
  kind: 'UNAVAILABLE',
  reason,
});

const replacementIsAddableInContext = (
  item: SemanticInventoryItem,
  identity: SemanticInventoryItemIdentity,
  context: FieldPresentationContext,
): boolean => {
  const choice = item.constructionChoice;
  if (!choice || !['READY', 'READY_WITH_WARNING'].includes(item.readiness.status) ||
    !sameConstructionRoute(choice.route, context.route)) {
    return false;
  }
  const source = choice.source;
  return source.kind === 'SEMANTIC' &&
    source.nodeId === context.sourceNodeId &&
    source.resourceType === item.resourceType &&
    source.conceptId === item.conceptId &&
    source.bindingId === item.bindingId &&
    source.sourcePath === item.sourcePath &&
    source.system === item.system &&
    (source.version ?? '') === item.codingVersion &&
    (source.code ?? '') === item.code &&
    source.valueSelector === item.valueSelector &&
    semanticItemMatchesIdentity(item, identity);
};

const bindPresentationResponseToRequest = (
  response: SemanticInventoryBrowseResponse,
  args: BrowseSemanticInventoryArgs & {
    readonly outputId: string;
    readonly fieldPresentation: FieldPresentationRequest;
  },
): SemanticInventoryBrowseResponse => {
  const presentation = response.fieldPresentation;
  if (!presentation) {
    return { ...response, fieldPresentation: presentationUnavailable('COVERAGE_UNAVAILABLE') };
  }
  if (presentation.kind === 'UNAVAILABLE') return response;

  const context = presentation.context;
  const request = args.fieldPresentation;
  const currentContext =
    canonicalProject(context.project) === canonicalProject(args.project) &&
    context.explorerId === args.explorerId &&
    context.snapshotToken === args.snapshotToken &&
    context.inventoryBuildId === response.buildId &&
    context.outputId === args.outputId &&
    context.draftVersion === request.expectedDraftVersion &&
    context.draftDigest === request.expectedDraftDigest &&
    context.sourceNodeId === request.sourceNodeId &&
    context.occurrenceId === request.occurrenceId &&
    (context.stageId ?? undefined) === request.stageId &&
    context.presentationRevision === SEMANTIC_FIELD_PRESENTATION_REVISION;
  if (!currentContext) {
    return { ...response, fieldPresentation: presentationUnavailable('STALE_CONTEXT') };
  }

  const requested = new Set(request.candidateIds);
  const decisionIds = presentation.decisions.map((decision) => decision.candidateId);
  if (decisionIds.length !== requested.size || new Set(decisionIds).size !== requested.size ||
    decisionIds.some((candidateId) => !requested.has(candidateId))) {
    return { ...response, fieldPresentation: presentationUnavailable('INCOMPLETE_COVERAGE') };
  }

  const referencedReplacementIds: SemanticInventoryItemIdentity[] = [];
  for (const decision of presentation.decisions) {
    if (decision.kind === 'KEEP_RAW') continue;
    for (const identity of decision.replacementIds) {
      const matches = presentation.replacements.filter((item) => semanticItemMatchesIdentity(item, identity));
      const [replacement] = matches;
      if (matches.length !== 1 || !replacement ||
        !replacementIsAddableInContext(replacement, identity, context)) {
        return { ...response, fieldPresentation: presentationUnavailable('REPLACEMENT_UNAVAILABLE') };
      }
      referencedReplacementIds.push(identity);
    }
  }
  const uniqueReferencedReplacementIds = referencedReplacementIds.filter((identity, index) =>
    referencedReplacementIds.findIndex((candidate) => sameSemanticItemIdentity(identity, candidate)) === index,
  );
  const responseReplacementIds = presentation.replacements.map(semanticIdentityForItem);
  if (responseReplacementIds.some((identity) => identity === undefined) ||
    responseReplacementIds.length !== uniqueReferencedReplacementIds.length ||
    responseReplacementIds.some((identity) => identity === undefined ||
      !uniqueReferencedReplacementIds.some((referenced) => sameSemanticItemIdentity(identity, referenced))) ||
    uniqueReferencedReplacementIds.some((referenced) =>
      responseReplacementIds.filter((identity) => identity !== undefined &&
        sameSemanticItemIdentity(identity, referenced)).length !== 1)) {
    return { ...response, fieldPresentation: presentationUnavailable('REPLACEMENT_UNAVAILABLE') };
  }
  return response;
};

const splitSemanticInventoryBrowseResponse = (value: unknown): {
  readonly core: unknown;
  readonly hasFieldPresentation: boolean;
  readonly fieldPresentation: unknown;
} => {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return { core: value, hasFieldPresentation: false, fieldPresentation: undefined };
  }
  const response = value as Record<string, unknown>;
  const { fieldPresentation, ...core } = response;
  return {
    core,
    hasFieldPresentation: Object.prototype.hasOwnProperty.call(response, 'fieldPresentation'),
    fieldPresentation,
  };
};

export const createLoomClient = (options: LoomClientOptions = {}): LoomClient => {
  const fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
  const baseUrl = options.baseUrl ?? '/';
  interface CacheEntry {
    readonly controller: AbortController;
    readonly promise: Promise<unknown>;
    consumers: number;
    settled: boolean;
  }
  const cache = new Map<string, CacheEntry>();

  const urlFor = (path: string): string => {
    if (/^https?:\/\//.test(baseUrl)) return `${baseUrl.replace(/\/$/, '')}${path}`;
    return `${baseUrl.replace(/\/$/, '')}${path}` || '/';
  };
  const request = async (path: string, init: RequestInit = {}): Promise<unknown> => {
    try {
      const response = await fetcher(urlFor(path), {
        ...init,
        credentials: options.credentials ?? 'same-origin',
        headers: {
          Accept: 'application/json',
          ...(init.body ? { 'Content-Type': 'application/json' } : {}),
          ...(options.headers ?? {}),
          ...(init.headers ?? {}),
        },
      });
      if (!response.ok) {
        const callerRequestId = new Headers(init.headers).get('X-Request-ID') ?? undefined;
        throw new LoomRequestError(await requestError(response, callerRequestId));
      }
      return parseJSON(response);
    } catch (error) {
      if (error instanceof LoomRequestError) throw error;
      if (init.signal?.aborted) throw error;
      throw new LoomRequestError({
        status: 'FETCH_ERROR',
        message: error instanceof Error ? error.message : String(error),
        retryable: true,
      });
    }
  };
  const evictCached = (key: string): void => {
    const entry = cache.get(key);
    if (!entry) return;
    cache.delete(key);
    if (!entry.settled) entry.controller.abort();
  };
  const abortError = (): DOMException =>
    new DOMException('The request was aborted.', 'AbortError');
  const getCached = <T>(
    key: string,
    run: (signal: AbortSignal) => Promise<unknown>,
    parse: (value: unknown) => T,
    signal?: AbortSignal,
    reload = false,
  ): Promise<T> => {
    if (reload) evictCached(key);
    let entry = cache.get(key);
    if (!entry) {
      const controller = new AbortController();
      const promise = Promise.resolve().then(() => run(controller.signal));
      const createdEntry: CacheEntry = { controller, promise, consumers: 0, settled: false };
      entry = createdEntry;
      cache.set(key, createdEntry);
      void promise.then(
        () => {
          createdEntry.settled = true;
        },
        () => {
          createdEntry.settled = true;
          if (cache.get(key) === createdEntry) cache.delete(key);
        },
      );
    }
    const current = entry;
    current.consumers += 1;
    const value = new Promise<unknown>((resolve, reject) => {
      let released = false;
      const release = () => {
        if (released) return;
        released = true;
        current.consumers -= 1;
        if (current.consumers === 0 && !current.settled && cache.get(key) === current) {
          cache.delete(key);
          current.controller.abort();
        }
      };
      const onAbort = () => {
        signal?.removeEventListener('abort', onAbort);
        release();
        reject(signal?.reason ?? abortError());
      };
      if (signal?.aborted) {
        onAbort();
        return;
      }
      signal?.addEventListener('abort', onAbort, { once: true });
      void current.promise.then(
        (result) => {
          signal?.removeEventListener('abort', onAbort);
          release();
          resolve(result);
        },
        (error: unknown) => {
          signal?.removeEventListener('abort', onAbort);
          release();
          reject(error);
        },
      );
    });
    return value.then(parse);
  };
  const authoringPath = (args: ExplorerAuthoringStateArgs, suffix: string): string =>
    `/api/v1/projects/${encodedProject(args.project)}/explorers/${encodeURIComponent(args.explorerId)}/authoring/v2${suffix}`;
  const projectPath = (args: ExplorerAuthoringProjectArgs): string =>
    `/api/v1/projects/${encodedProject(args.project)}/explorers`;
  const interpretationLibrariesPath = (project: string): string =>
    `/api/v1/projects/${encodedProject(project)}/interpretation-libraries`;
  const interpretationRevisionPath = (project: string, revisionId: string): string =>
    `/api/v1/projects/${encodedProject(project)}/interpretation-revisions/${encodeURIComponent(revisionId)}`;
  const authResourcePathQuery = (authResourcePath?: string): string => {
    const value = authResourcePath?.trim();
    return value ? `?${new URLSearchParams({ auth_resource_path: value }).toString()}` : '';
  };
  const durableAuthoringPath = (args: ExplorerAuthoringStateArgs, suffix: string): string =>
    `${authoringPath(args, suffix)}${authResourcePathQuery(args.authResourcePath)}`;
  const durableProjectPath = (args: ExplorerAuthoringProjectArgs): string =>
    `${projectPath(args)}${authResourcePathQuery(args.authResourcePath)}`;
  const viewBuilderPath = (args: ProjectViewBuilderArgs): string =>
    projectViewBuilderPath(args.project, args.authResourcePath);
  const parseProjectViewBuilder = (value: unknown): ProjectViewBuilderState => {
    try { return viewBuilderStateSchema.parse(value); }
    catch { throw new LoomRequestError({ status: 502, code: 'INVALID_VIEW_BUILDER_STATE', message: 'Loom returned invalid saved view settings.', retryable: false }); }
  };
  const viewBuilderCacheKey = (args: ProjectViewBuilderArgs): string => `view-builder:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}`;
  const updatedProjectViewBuilder = (args: ProjectViewBuilderArgs, value: unknown): ProjectViewBuilderState => {
    const state = parseProjectViewBuilder(value);
    evictCached(viewBuilderCacheKey(args));
    return state;
  };
  const getProjectViewBuilder = (args: ProjectViewBuilderArgs, queryOptions: { readonly signal?: AbortSignal; readonly reload?: boolean } = {}) =>
    getCached(viewBuilderCacheKey(args),
      (signal) => request(viewBuilderPath(args), { signal }), parseProjectViewBuilder, queryOptions.signal, queryOptions.reload);
  const saveProjectViewConfig = async (args: SaveProjectViewConfigArgs, signal?: AbortSignal) =>
    updatedProjectViewBuilder(args, await request(`${projectViewBuilderPath(args.project)}/configs/${encodeURIComponent(args.configId)}${authResourcePathQuery(args.authResourcePath)}`, { method: 'PUT', body: JSON.stringify(args.request), signal }));
  const deleteProjectViewConfig = async (args: DeleteProjectViewConfigArgs, signal?: AbortSignal) => {
    const query = new URLSearchParams({ expectedRevisionId: args.expectedRevisionId });
    const auth = args.authResourcePath?.trim();
    const path = `${projectViewBuilderPath(args.project)}/configs/${encodeURIComponent(args.configId)}?${query.toString()}${auth ? `&${new URLSearchParams({ auth_resource_path: auth }).toString()}` : ''}`;
    return updatedProjectViewBuilder(args, await request(path, { method: 'DELETE', signal }));
  };
  const saveProjectViewDefaults = async (args: SaveProjectViewDefaultsArgs, signal?: AbortSignal) =>
    updatedProjectViewBuilder(args, await request(`${projectViewBuilderPath(args.project)}/defaults${authResourcePathQuery(args.authResourcePath)}`, { method: 'PUT', body: JSON.stringify(args.request), signal }));
  const adoptProjectViewBuilderCatalogs = async (args: ProjectViewBuilderArgs, signal?: AbortSignal) =>
    updatedProjectViewBuilder(args, await request(`${projectViewBuilderPath(args.project)}/adopt${authResourcePathQuery(args.authResourcePath)}`, { method: 'POST', signal }));
  const withJson = (body: unknown, signal?: AbortSignal, requestId?: string): RequestInit => ({
    method: 'POST',
    body: JSON.stringify(body),
    signal,
    headers: requestId ? { 'X-Request-ID': requestId } : undefined,
  });

  const listExplorers = (args: ExplorerAuthoringProjectArgs, signal?: AbortSignal) =>
    getCached(
      `explorers:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}`,
      (requestSignal) => request(projectPath(args), { signal: requestSignal }),
      (value): ReadonlyArray<ExplorerSummary> => {
        if (!Array.isArray(value)) throw new LoomRequestError({ status: 502, code: 'INVALID_EXPLORER_LIST', message: 'Loom returned an invalid Explorer list.', retryable: false });
        return value.map((item): ExplorerSummary => {
          if (!isRecord(item) || typeof item.project !== 'string' || typeof item.explorerId !== 'string' || typeof item.title !== 'string' || typeof item.management !== 'string' || typeof item.updatedAt !== 'string') throw new LoomRequestError({ status: 502, code: 'INVALID_EXPLORER_LIST', message: 'Loom returned an invalid Explorer list.', retryable: false });
          return {
            project: item.project,
            explorerId: item.explorerId,
            title: item.title,
            management: item.management,
            ...(typeof item.activeRevisionId === 'string' ? { activeRevisionId: item.activeRevisionId } : {}),
            updatedAt: item.updatedAt,
          };
        });
      },
      signal,
    );
  const getBuilder = (args: ExplorerAuthoringStateArgs, queryOptions: { readonly signal?: AbortSignal; readonly reload?: boolean } = {}) =>
    getCached(
      `builder:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`,
      (signal) => request(authoringPath(args, '/builder'), { signal }),
      assertExplorerBuilderState,
      queryOptions.signal,
      queryOptions.reload,
    );
  const getCapability = (args: ExplorerAuthoringStateArgs, signal?: AbortSignal) =>
    getCached(
      `capability:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`,
      (requestSignal) => request(authoringPath(args, '/capability'), { signal: requestSignal }),
      (value) => explorerAuthoringCapabilitiesSchema.parse(value),
      signal,
    );
  const getExplorer = (args: ExplorerAuthoringStateArgs, signal?: AbortSignal) =>
    getCached(
      `viewer:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`,
      (requestSignal) => request(`/api/v1/projects/${encodedProject(args.project)}/explorers/${encodeURIComponent(args.explorerId)}`, { signal: requestSignal }),
      (value) => {
        let state: ReturnType<typeof assertExplorerStateV1>;
        try {
          state = assertExplorerStateV1(value);
        } catch {
          throw new LoomRequestError({ status: 502, code: 'INVALID_EXPLORER_STATE', message: 'Loom returned an invalid Explorer state.', retryable: false });
        }
        if (!state.runtime) throw new LoomRequestError({ status: 422, code: 'EXPLORER_RUNTIME_REQUIRED', message: 'The selected Explorer has no published runtime.', retryable: false });
        const runtimeIdentity = state.runtime.publication?.revisionId
          ?? state.runtime.publication?.generation
          ?? state.runtime.generation
          ?? state.runtime.schema?.digest;
        if (runtimeIdentity) return state.runtime;
        const responseIdentity = state.active.revisionId ?? state.updatedAt;
        if (!responseIdentity) {
          throw new LoomRequestError({ status: 502, code: 'INVALID_EXPLORER_STATE', message: 'Loom returned a published runtime without a session identity.', retryable: false });
        }
        return { ...state.runtime, responseIdentity };
      },
      signal,
    );
  const applyCommands = async (args: ApplyExplorerBuilderCommandsArgs, signal?: AbortSignal) => {
    const commands = args.commands.map((command) => {
      if (command.type !== 'UPDATE_COLUMN' || !command.columnValue) return command;
      const columnValue = { ...command.columnValue };
      delete columnValue.columnId;
      return { ...command, columnValue };
    });
    const value = explorerBuilderCommandsResultSchema.parse(await request(durableAuthoringPath(args, '/commands'), withJson({
      commandId: args.commandId,
      semanticsVersion: EXPLORER_AUTHORING_SEMANTICS_VERSION,
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      ...(args.expectedDraftDigest ? { expectedDraftDigest: args.expectedDraftDigest } : {}),
      commands,
    }, signal, args.requestId)));
    evictCached(`builder:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`);
    return value;
  };
  const assessRowChange = (args: AssessExplorerRowChangeArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/row-change'), withJson({
      ...(args.mode ? { mode: args.mode } : {}),
      snapshotToken: args.snapshotToken,
      draftVersion: args.draftVersion,
      draftDigest: args.draftDigest,
      outputId: args.outputId,
      rootNodeId: args.rootNodeId,
      ...(args.rootOccurrenceId ? { rootOccurrenceId: args.rootOccurrenceId } : {}),
      ...(args.routeRebase ? { routeRebase: args.routeRebase } : {}),
    }, signal, args.requestId)).then((value) => rowChangeAssessmentSchema.parse(value));
  const reconcile = (args: ReconcileExplorerBuilderArgs, signal?: AbortSignal) =>
    request(durableAuthoringPath(args, '/reconcile'), withJson({
      snapshotToken: args.snapshotToken,
      draftVersion: args.draftVersion,
      draftDigest: args.draftDigest,
      ...(args.outputId ? { outputId: args.outputId } : {}),
    }, signal, args.requestId)).then(assertExplorerBuilderCompileResult);
  const suggestions = (args: ExplorerCandidateSuggestionsArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/suggestions'), withJson({ snapshotToken: args.snapshotToken, nodeId: args.nodeId, ...(args.query ? { query: args.query } : {}) }, signal, args.requestId)).then((value) => explorerBuilderSuggestionsResultSchema.parse(value));
  const searchSchemaFields = (args: SearchSchemaFieldsArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/schema-fields'), withJson({
      snapshotToken: args.snapshotToken, nodeId: args.nodeId,
      ...(args.query ? { query: args.query } : {}),
      ...(args.cursor ? { cursor: args.cursor } : {}),
      ...(args.limit === undefined ? {} : { limit: args.limit }),
    }, signal, args.requestId)).then((value) => explorerBuilderSchemaFieldsResultSchema.parse(value));
  const browseSemanticInventory = (args: BrowseSemanticInventoryArgs, signal?: AbortSignal) => {
    const fieldPresentation = args.fieldPresentation;
    const body = semanticInventoryBrowseRequestSchema.parse({
      snapshotToken: args.snapshotToken,
      rowRoot: args.rowRoot,
      ...(args.selectionMode ? { selectionMode: args.selectionMode } : {}),
      ...(args.frameId ? { frameId: args.frameId } : {}),
      ...(args.sourceChoiceId ? { sourceChoiceId: args.sourceChoiceId } : {}),
      ...(args.outputId ? { outputId: args.outputId } : {}),
      ...(args.resourceType ? { resourceType: args.resourceType } : {}),
      ...(args.query ? { query: args.query } : {}),
      ...(args.cursor ? { cursor: args.cursor } : {}),
      ...(args.limit === undefined ? {} : { limit: args.limit }),
      ...(fieldPresentation ? { fieldPresentation } : {}),
    });
    return request(authoringPath(args, '/semantic-inventory'), withJson(
      body,
      signal,
      args.requestId,
    )).then((value) => {
      const split = splitSemanticInventoryBrowseResponse(value);
      const response = semanticInventoryBrowseCoreResponseSchema.parse(split.core);
      const parsedPresentation = fieldPresentationSchema.safeParse(split.fieldPresentation);
      if (!fieldPresentation) {
        return parsedPresentation.success && split.hasFieldPresentation
          ? { ...response, fieldPresentation: parsedPresentation.data }
          : response;
      }
      if (!split.hasFieldPresentation) {
        return { ...response, fieldPresentation: presentationUnavailable('COVERAGE_UNAVAILABLE') };
      }
      if (!parsedPresentation.success) {
        return { ...response, fieldPresentation: presentationUnavailable('INCOMPLETE_COVERAGE') };
      }
      const responseWithPresentation = { ...response, fieldPresentation: parsedPresentation.data };
      if (!args.outputId) {
        return { ...responseWithPresentation, fieldPresentation: presentationUnavailable('STALE_CONTEXT') };
      }
      return bindPresentationResponseToRequest(responseWithPresentation, {
        ...args,
        outputId: args.outputId,
        fieldPresentation,
      });
    });
  };
  const browseFrameSourceOptions = (args: BrowseFrameSourceOptionsArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/frame-source-options'), withJson({
      snapshotToken: args.snapshotToken,
      outputId: args.outputId,
      ...(args.resourceType ? { resourceType: args.resourceType } : {}),
      ...(args.query ? { query: args.query } : {}),
      ...(args.cursor ? { cursor: args.cursor } : {}),
      ...(args.limit === undefined ? {} : { limit: args.limit }),
    }, signal, args.requestId)).then((value) => frameSourceOptionsResponseSchema.parse(value));
  const inspectColumnSource = (args: InspectColumnSourceArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/column-source'), withJson({
      snapshotToken: args.snapshotToken,
      outputId: args.outputId,
      column: args.column,
    }, signal, args.requestId)).then((value) => explorerColumnSourceDescriptorSchema.parse(value));
  const searchConstructionChoices = (args: SearchConstructionChoicesArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/construction-choices'), withJson({
      snapshotToken: args.snapshotToken,
      outputId: args.outputId,
      ...(args.occurrenceId ? { occurrenceId: args.occurrenceId } : {}),
      source: args.source,
      ...(args.limit === undefined ? {} : { limit: args.limit }),
      ...(args.cursor ? { cursor: args.cursor } : {}),
    }, signal, args.requestId)).then((value) => constructionChoiceSearchResponseSchema.parse(value));
  const searchRelatedExpandChoices = (args: SearchRelatedExpandChoicesArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/related-expand-choices'), withJson({
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      expectedDraftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
      anchorColumnId: args.anchorColumnId,
      targetResourceType: args.targetResourceType,
      ...(args.limit === undefined ? {} : { limit: args.limit }),
      ...(args.savedGraphOnly ? { savedGraphOnly: true } : {}),
      ...(args.cursor ? { cursor: args.cursor } : {}),
    }, signal, args.requestId)).then((value) => relatedExpandChoiceSearchResponseSchema.parse(value));
  const searchRelatedFieldChoices = (args: SearchRelatedFieldChoicesArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/related-field-choices'), withJson({
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      expectedDraftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
      ...(args.query ? { query: args.query } : {}),
      ...(args.limit === undefined ? {} : { limit: args.limit }),
      ...(args.cursor ? { cursor: args.cursor } : {}),
    }, signal, args.requestId)).then((value) => relatedFieldChoiceSearchResponseSchema.parse(value));
  const searchRelatedExpandContributors = (args: SearchRelatedExpandContributorsArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/related-expand-contributors'), withJson({
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      expectedDraftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
      routeChoiceId: args.routeChoiceId,
      ...(args.query ? { query: args.query } : {}),
      ...(args.limit === undefined ? {} : { limit: args.limit }),
      ...(args.cursor ? { cursor: args.cursor } : {}),
    }, signal, args.requestId)).then((value) => relatedExpandContributorSearchResponseSchema.parse(value));
  const searchPopulationRoutes = (args: SearchPopulationRoutesArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/population-routes'), withJson({
      snapshotToken: args.snapshotToken,
      outputId: args.outputId,
      selectionRevisionId: args.selectionRevisionId,
      ...(args.limit === undefined ? {} : { limit: args.limit }),
      ...(args.cursor ? { cursor: args.cursor } : {}),
    }, signal, args.requestId)).then((value) => populationRoutesResponseSchema.parse(value));
  const listRowDefinitionChoices = (args: ListRowDefinitionChoicesArgs, signal?: AbortSignal) => {
    const query = new URLSearchParams({ outputId: args.outputId, snapshotToken: args.snapshotToken });
    return request(`${authoringPath(args, '/row-definition-choices')}?${query.toString()}`, { signal })
      .then((value) => rowDefinitionChoicesResponseSchema.parse(value));
  };
  const proposeRowDefinition = (args: ProposeRowDefinitionArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/row-definition-proposals'), withJson({
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      expectedDraftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      selection: args.selection,
      ...(args.limit === undefined ? {} : { limit: args.limit }),
    }, signal, args.requestId)).then((value) => rowDefinitionProposalSchema.parse(value));
  const getConstructionCapabilities = (args: GetConstructionCapabilitiesArgs, signal?: AbortSignal) =>
    request(durableAuthoringPath(args, '/construction-capabilities'), withJson(
      constructionCapabilitiesRequestSchema.parse({
        expectedDraftDigest: args.expectedDraftDigest,
        expectedDraftVersion: args.expectedDraftVersion,
        outputId: args.outputId,
        snapshotToken: args.snapshotToken,
        stageId: args.stageId,
        ...(args.groupStepId ? { groupStepId: args.groupStepId } : {}),
        ...(args.includeGroupChoices ? { includeGroupChoices: true } : {}),
      }), signal, args.requestId ?? `construction-capabilities-${globalThis.crypto.randomUUID()}`,
    )).then((value) => constructionCapabilitiesResponseSchema.parse(value));
  const getConstructionColumnPopulation = (args: GetConstructionColumnPopulationArgs, signal?: AbortSignal) => {
    const body = constructionColumnPopulationRequestSchema.parse({
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      expectedDraftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
      columnIds: args.columnIds,
    });
    return request(durableAuthoringPath(args, '/construction-column-population'), withJson(body, signal, args.requestId))
      .then((value) => constructionColumnPopulationResponseSchema.parse(value));
  };
  const discoverConstructionCategories = (args: DiscoverConstructionCategoriesArgs, signal?: AbortSignal) =>
    request(durableAuthoringPath(args, '/construction-category-discoveries'), withJson(
      constructionCategoryDiscoveryRequestSchema.parse({
        expectedDraftDigest: args.expectedDraftDigest,
        expectedDraftVersion: args.expectedDraftVersion,
        outputId: args.outputId,
        snapshotToken: args.snapshotToken,
        stageId: args.stageId,
        pivotStepId: args.pivotStepId,
        groupKeyIds: args.groupKeyIds,
        pivotSources: args.pivotSources,
        candidateConstruction: args.candidateConstruction,
        categoryColumnId: args.categoryColumnId,
        valueColumnId: args.valueColumnId,
      }), signal, args.requestId,
    )).then((value) => constructionCategoryDiscoveryResponseSchema.parse(value));
  const getConstructionInputs = (args: GetConstructionInputsArgs, signal?: AbortSignal) => {
    const { project: _project, explorerId: _explorerId, authResourcePath: _authResourcePath, requestId: _requestId, ...body } = args;
    return request(durableAuthoringPath(args, '/construction-inputs'), withJson(
      constructionInputsRequestSchema.parse(body), signal, args.requestId,
    )).then((value) => constructionInputsResponseSchema.parse(value));
  };
  const describeAuthoringNetwork = (args: DescribeAuthoringNetworkArgs, signal?: AbortSignal) => {
    const { project: _project, explorerId: _explorerId, authResourcePath: _authResourcePath, ...body } = args;
    return request(durableAuthoringPath(args, '/authoring-network'), withJson(
      authoringNetworkRequestSchema.parse(body), signal,
    )).then((value) => authoringNetworkResponseSchema.parse(value));
  };
  const proposeColumnPolicy = (args: ProposeColumnPolicyArgs, signal?: AbortSignal) => {
    const { project: _project, explorerId: _explorerId, authResourcePath: _authResourcePath, requestId, ...body } = args;
    return request(durableAuthoringPath(args, '/column-policy-proposals'), withJson(
      columnPolicyProposalRequestSchema.parse(body), signal, requestId,
    )).then((value) => columnPolicyProposalSchema.parse(value));
  };
  const proposeSourceRelationship = (args: ProposeSourceRelationshipArgs, signal?: AbortSignal) => {
    const { project: _project, explorerId: _explorerId, authResourcePath: _authResourcePath, requestId, ...body } = args;
    return request(durableAuthoringPath(args, '/source-relationship-proposals'), withJson(
      sourceRelationshipProposalRequestSchema.parse({ ...body, includePreview: true }), signal, requestId,
    )).then((value) => sourceRelationshipProposalWithPreviewSchema.parse(value));
  };
  const proposeConstruction = (args: ProposeConstructionArgs, signal?: AbortSignal) => {
    const { project: _project, explorerId: _explorerId, authResourcePath: _authResourcePath, requestId, ...body } = args;
    return request(durableAuthoringPath(args, '/construction-proposals'), withJson(
      constructionProposalRequestSchema.parse(body), signal, requestId,
    )).then((value) => constructionProposalResponseSchema.parse(value));
  };
  const proposeConstructionChoices = (args: ProposeConstructionChoicesArgs, signal?: AbortSignal) => {
    const { project: _project, explorerId: _explorerId, authResourcePath: _authResourcePath, requestId, ...body } = args;
    return request(durableAuthoringPath(args, '/construction-choice-proposals'), withJson(
      constructionChoiceProposalRequestSchema.parse(body), signal, requestId,
    )).then((value) => constructionChoiceProposalResponseSchema.parse(value));
  };
  const preview = (args: PreviewExplorerBuilderArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/preview'), withJson({ receiptId: args.receiptId, outputId: args.outputId, ...(args.limit === undefined ? {} : { limit: args.limit }) }, signal, args.requestId)).then(assertExplorerBuilderPreviewResult);
  const populationMapping = (args: PopulationMappingArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/population-mapping'), withJson({ receiptId: args.receiptId, outputId: args.outputId, ...(args.cursor === undefined ? {} : { cursor: args.cursor }), ...(args.limit === undefined ? {} : { limit: args.limit }) }, signal)).then((value) => populationMappingResponseSchema.parse(value));
  const cellTrace = (args: CellTraceArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/cell-trace'), withJson({
      receiptId: args.receiptId,
      outputId: args.outputId,
      rowId: args.rowId,
      column: args.column,
      ...(args.offset === undefined ? {} : { offset: args.offset }),
      ...(args.limit === undefined ? {} : { limit: args.limit }),
    }, signal)).then((value) => cellTraceResponseSchema.parse(value));
  const rowLineage = (args: RowLineageArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/row-lineage'), withJson({
      receiptId: args.receiptId,
      outputId: args.outputId,
      rowId: args.rowId,
      ...(args.offset === undefined ? {} : { offset: args.offset }),
      ...(args.limit === undefined ? {} : { limit: args.limit }),
    }, signal)).then((value) => explorerBuilderRowLineageResponseSchema.parse(value));
  const prepareArtifact = (args: PrepareArtifactArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/artifacts'), withJson({
      revisionId: args.revisionId,
      outputId: args.outputId,
      idempotencyKey: args.idempotencyKey,
    }, signal)).then((value) => {
      const artifact = artifactSchema.parse(value);
      if (canonicalProject(artifact.project) !== canonicalProject(args.project) ||
        artifact.explorerId !== args.explorerId || artifact.revisionId !== args.revisionId || artifact.outputId !== args.outputId) {
        throw new Error('Prepared dataset artifact does not match the requested project, Explorer, revision, and output.');
      }
      return artifact;
    });
  const artifactDownloadURL = (args: DownloadArtifactArgs): string =>
    urlFor(`${authoringPath(args, '/artifacts')}/${encodeURIComponent(args.artifactId)}`);
  const listInterpretationLibraries = (args: ExplorerAuthoringProjectArgs, signal?: AbortSignal) =>
    getCached(
      `interpretations:${canonicalProject(args.project)}`,
      (requestSignal) => request(interpretationLibrariesPath(args.project), { signal: requestSignal }),
      (value) => interpretationLibraryListResponseSchema.parse(value).libraries,
      signal,
    );
  const getInterpretationRevision = (args: { readonly project: string; readonly revisionId: string }, signal?: AbortSignal) =>
    request(interpretationRevisionPath(args.project, args.revisionId), { signal }).then((value) => interpretationRevisionSchema.parse(value));
  const createInterpretationRevision = async (args: CreateInterpretationRevisionArgs, signal?: AbortSignal) => {
    const parentRevisionId = args.parentRevisionId?.trim();
    const value = await request(
      `${interpretationLibrariesPath(args.project)}${authResourcePathQuery(args.authResourcePath)}`,
      withJson({
        libraryId: args.libraryId,
        ...(parentRevisionId ? { parentRevisionId } : {}),
        applicability: args.applicability,
        rules: args.rules,
        explanation: args.explanation,
      }, signal, args.requestId),
    );
    evictCached(`interpretations:${canonicalProject(args.project)}`);
    return interpretationRevisionSchema.parse(value);
  };
  const resolveConfiguredColumnContexts = (args: ResolveConfiguredColumnContextsArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/configured-column-context'), withJson({
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      expectedDraftDigest: args.expectedDraftDigest,
    }, signal, args.requestId)).then((value) => {
      const response = configuredColumnContextResponseSchema.parse(value);
      if (
        response.snapshotToken !== args.snapshotToken ||
        response.draftVersion !== args.expectedDraftVersion ||
        response.draftDigest !== args.expectedDraftDigest
      ) {
        throw new Error('Loom returned interpretation context for a different saved draft.');
      }
      return response;
    });
  const createInterpretationRevisionFromColumn = async (args: CreateInterpretationRevisionFromColumnArgs, signal?: AbortSignal) => {
    const parentRevisionId = args.parentRevisionId?.trim();
    const value = await request(durableAuthoringPath(args, '/interpretation-revisions'), withJson({
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      expectedDraftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      column: args.column,
      libraryId: args.libraryId,
      ...(parentRevisionId ? { parentRevisionId } : {}),
      explanation: args.explanation,
    }, signal, args.requestId));
    evictCached(`interpretations:${canonicalProject(args.project)}`);
    return interpretationRevisionSchema.parse(value);
  };
  const previewInterpretationCandidate = (args: PreviewInterpretationCandidateArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/interpretation-preview'), withJson({
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      expectedDraftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      column: args.column,
      revisionId: args.revisionId,
      ...(args.limit === undefined ? {} : { limit: args.limit }),
    }, signal, args.requestId)).then((value) => interpretationPreviewResponseSchema.parse(value));
  const publish = async (args: PublishExplorerBuilderArgs, signal?: AbortSignal) => {
    const result = assertExplorerBuilderPublishResult(
      options.publishTransport
        ? await options.publishTransport(args, signal)
        : await request(
          durableAuthoringPath(args, '/publish'),
          withJson({ receiptId: args.receiptId }, signal, args.requestId),
        ),
    );
    if (options.publishTransport && result.receiptId !== args.receiptId) {
      throw new Error('The publication result belongs to a different compilation receipt.');
    }
    evictCached(`viewer:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`);
    return result;
  };
  const createExplorer = async (args: CreateExplorerArgs, signal?: AbortSignal) => {
    const value = await request(durableProjectPath(args), withJson({ name: args.name, ...(args.title ? { title: args.title } : {}), ...(args.sourceExplorerId ? { sourceExplorerId: args.sourceExplorerId } : {}) }, signal, args.requestId));
    evictCached(`explorers:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}`);
    return value as ExplorerSummary;
  };
  const createSelection = (args: CreateSelectionArgs, signal?: AbortSignal) =>
    request(`${projectPath(args)}/${encodeURIComponent(args.explorerId)}/selections${authResourcePathQuery(args.authResourcePath)}`, withJson({
      snapshotToken: args.snapshotToken,
      idempotencyKey: args.idempotencyKey,
      source: args.source,
      ...(args.exclusions ? { exclusions: args.exclusions } : {}),
    }, signal, args.requestId)).then((value) => selectionRevisionSchema.parse(value));
  const getSelection = (args: GetSelectionArgs, signal?: AbortSignal) => {
    const params = new URLSearchParams();
    if (args.cursor !== undefined) params.set('cursor', args.cursor);
    if (args.limit !== undefined) params.set('limit', String(args.limit));
    const authResourcePath = args.authResourcePath?.trim();
    if (authResourcePath) params.set('auth_resource_path', authResourcePath);
    const query = params.size > 0 ? `?${params}` : '';
    return request(`${projectPath(args)}/${encodeURIComponent(args.explorerId)}/selections/${encodeURIComponent(args.selectionRevision)}${query}`, { signal })
      .then((value) => selectionPageSchema.parse(value));
  };
  const proposePopulationMemberRemoval = (args: ProposePopulationMemberRemovalArgs, signal?: AbortSignal) =>
    request(durableAuthoringPath(args, '/population-member-proposals'), withJson({
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      expectedDraftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      baseSelectionRevisionId: args.baseSelectionRevisionId,
      removedMember: args.removedMember,
      ...(args.limit === undefined ? {} : { limit: args.limit }),
    }, signal, args.requestId)).then((value) => populationMemberRemovalProposalResponseSchema.parse(value));
  const createExplicitGroupRevision = (args: CreateExplicitGroupRevisionArgs, signal?: AbortSignal) => {
    const requestBody = explicitGroupCreateRequestSchema.parse({
      snapshotToken: args.snapshotToken,
      idempotencyKey: args.idempotencyKey,
      groups: args.groups,
    });
    return request(`${projectPath(args)}/${encodeURIComponent(args.explorerId)}/selections/${encodeURIComponent(args.selectionRevision)}/explicit-groups${authResourcePathQuery(args.authResourcePath)}`, withJson(requestBody, signal, args.requestId))
      .then((value) => explicitGroupRevisionSummarySchema.parse(value));
  };
  const deleteExplorer = async (args: DeleteExplorerArgs, signal?: AbortSignal) => {
    await request(`${projectPath(args)}/${encodeURIComponent(args.explorerId)}`, { method: 'DELETE', signal, headers: args.requestId ? { 'X-Request-ID': args.requestId } : undefined });
    evictCached(`explorers:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}`);
    evictCached(`builder:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`);
    return null;
  };
  const queryOutput = async (outputRequest: LoomOutputRequest, signal?: AbortSignal): Promise<LoomOutputResult> => {
    const path = `/api/v1/projects/${encodedProject(outputRequest.project)}/dataframes`;
    const { selector, columns: requestedColumns, filters, sort, first, after, facets: requestedFacets } = outputRequest;
    const selection = {
      selector,
      ...(filters?.length ? { filters: filters.map(({ column, op, value }) => ({ column, op, value })) } : {}),
    };
    const [rowsResponse, facetResponse] = await Promise.all([
      request(`${path}/rows`, withJson({
        ...selection,
        ...(requestedColumns?.length ? { columns: requestedColumns } : {}),
        ...(sort ? { sort: { column: sort.column, desc: sort.desc ?? false } } : {}),
        ...(first === undefined ? {} : { first }),
        ...(after ? { after } : {}),
      }, signal)),
      requestedFacets?.length
        ? request(`${path}/aggregations`, withJson({ ...selection, specs: requestedFacets }, signal)).then(
            (value) => dataframeAggregationsSchema.safeParse(value),
          )
        : undefined,
    ]);
    const rows = dataframeConnectionSchema.safeParse(rowsResponse);
    if (!rows.success || (facetResponse && !facetResponse.success)) {
      throw new LoomRequestError({ status: 502, code: 'INVALID_OUTPUT_RESPONSE', message: 'Loom returned an invalid output response.', retryable: false });
    }
    const connection = rows.data;
    const aggregationResult = facetResponse?.data;
    if (aggregationResult) {
      const rowIdentity = connection.materialization && materializationIdentity(connection.materialization);
      if (!rowIdentity || rowIdentity !== materializationIdentity(aggregationResult.materialization)) {
        throw new LoomRequestError({ status: 409, code: 'STALE_CURSOR', message: 'The published dataframe changed while its facets were loading.', retryable: false });
      }
    }
    const columns = connection.columns;
    const pageInfo = connection.pageInfo;
    const facets = aggregationResult?.aggregations.map(normalizedFacet) ?? [];
    const materialization = connection.materialization;
    const endCursor = pageInfo.endCursor ?? undefined;
    return {
      columns,
      rows: shapeRows(connection.rows, columns),
      rowIds: connection.rowIds,
      totalCount: connection.totalCount,
      pageInfo: { hasNextPage: pageInfo.hasNextPage, ...(endCursor ? { endCursor } : {}) },
      ...(materialization ? { materialization } : {}),
      facets,
    };
  };

  const exportOutput = async (outputRequest: LoomOutputRequest, signal?: AbortSignal): Promise<Blob> => {
    const rows: Array<Record<string, unknown>> = [];
    let columns: ReadonlyArray<string> = outputRequest.columns ?? [];
    let after: string | undefined;
    let pinnedMaterialization: string | undefined;
    const first = Math.max(outputRequest.first ?? 100, 1000);
    while (true) {
      if (signal?.aborted) throw new DOMException('The export was aborted.', 'AbortError');
      const page = await queryOutput({ ...outputRequest, first, after, facets: [] }, signal);
      if (page.materialization) {
        const identity = materializationIdentity(page.materialization);
        if (identity && pinnedMaterialization && identity !== pinnedMaterialization) {
          throw new LoomRequestError({
            status: 409,
            code: 'PUBLICATION_CONFLICT',
            message: 'The published output changed during export; restart from the first page.',
            retryable: false,
          });
        }
        if (identity) pinnedMaterialization = identity;
      }
      if (columns.length === 0) columns = page.columns;
      rows.push(...page.rows);
      if (!page.pageInfo.hasNextPage || !page.pageInfo.endCursor || page.pageInfo.endCursor === after) break;
      after = page.pageInfo.endCursor;
    }
    const csvValue = (value: unknown): string => {
      if (value === undefined || value === null) return '';
      if (typeof value === 'string') return value;
      if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') return String(value);
      try { return JSON.stringify(value); } catch { return String(value); }
    };
    const csvCell = (value: unknown): string => {
      const text = csvValue(value);
      return /[",\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
    };
    const lines = [columns.map((column) => csvCell(outputRequest.exportHeaders?.[column] ?? column)).join(','), ...rows.map((row) => columns.map((column) => csvCell(row[column])).join(','))];
    return new Blob([lines.join('\n') + '\n'], { type: 'text/csv;charset=utf-8' });
  };

  const rows = async (selector: ExplorerRuntimeV1['outputs'][number]['selector'], columns: ReadonlyArray<string>, rowOptions: LoomRowsOptions) => {
    const result = await queryOutput({ project: rowOptions.project, selector, columns, first: rowOptions.first ?? 100 }, rowOptions.signal);
    return { columns: result.columns, rows: result.rows, totalCount: result.totalCount };
  };
  return {
    listExplorers,
    createSelection,
    getSelection,
    proposePopulationMemberRemoval,
    createExplicitGroupRevision,
    getBuilder,
    getProjectViewBuilder,
    saveProjectViewConfig,
    deleteProjectViewConfig,
    saveProjectViewDefaults,
    adoptProjectViewBuilderCatalogs,
    getCapability,
    getExplorer,
    applyCommands,
    assessRowChange,
    reconcile,
    suggestions,
    browseSemanticInventory,
    searchSchemaFields,
    browseFrameSourceOptions,
    inspectColumnSource,
    searchConstructionChoices,
    searchRelatedExpandChoices,
    searchRelatedExpandContributors,
    searchRelatedFieldChoices,
    searchPopulationRoutes,
    listRowDefinitionChoices,
    proposeRowDefinition,
    getConstructionCapabilities,
    getConstructionColumnPopulation,
    discoverConstructionCategories,
    getConstructionInputs,
    describeAuthoringNetwork,
    proposeColumnPolicy,
    proposeSourceRelationship,
    proposeConstruction,
    proposeConstructionChoices,
    preview,
    populationMapping,
    cellTrace,
    rowLineage,
    prepareArtifact,
    artifactDownloadURL,
    listInterpretationLibraries,
    getInterpretationRevision,
    createInterpretationRevision,
    resolveConfiguredColumnContexts,
    createInterpretationRevisionFromColumn,
    previewInterpretationCandidate,
    publish,
    createExplorer,
    deleteExplorer,
    rows,
    queryOutput,
    exportOutput,
    invalidate: (scope = 'all') => {
      if (scope === 'all' || scope === 'explorers') [...cache.keys()].filter((key) => key.startsWith('explorers:')).forEach(evictCached);
      if (scope === 'all' || scope === 'builder') [...cache.keys()].filter((key) => key.startsWith('builder:')).forEach(evictCached);
      if (scope === 'all' || scope === 'interpretations') [...cache.keys()].filter((key) => key.startsWith('interpretations:')).forEach(evictCached);
      if (scope === 'all') [...cache.keys()].filter((key) => key.startsWith('viewer:')).forEach(evictCached);
    },
  };
};

export type { LoomOutputFilter, LoomOutputFilterOperator } from '../contracts/selection';
