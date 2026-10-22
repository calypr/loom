// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient, type LoomClient } from '../../../api';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionProposalResponse,
  ConstructionStageDescriptor,
  ExplorerBuilderState,
  ExplorerBuilderWorkspace,
} from '../../../types';
import { constructionCapabilitiesResponseSchema, constructionProposalRequestSchema } from '../../../types';
import BuilderWorkspace from '../BuilderWorkspace';
import { CoreAdapterGapError, coreAdapters } from './core';

type CapturedRequest = Readonly<{ url: string; method: string; body: string | null }>;

const fixture = vi.hoisted(() => ({
  requests: [] as CapturedRequest[],
  state: null as ExplorerBuilderState | null,
  capabilities: null as ConstructionCapabilitiesResponse | null,
  proposal: null as ConstructionProposalRequest | null,
  resolveProposal: null as ((response: Response) => void) | null,
  getSuggestions: vi.fn(() => ({ unwrap: () => Promise.resolve({ snapshotToken: 'snapshot-core-driver-probe', candidates: [] }) })),
}));

let client: LoomClient;

vi.mock('../../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../react')>();
  const mutation = () => [vi.fn(), { isLoading: false }];
  return {
    ...actual,
    useLoomClient: () => client,
    useApplyExplorerBuilderCommandsV2Mutation: mutation,
    useAssessExplorerRowChangeMutation: mutation,
    useCreateExplorerAuthoringMutation: mutation,
    useDeleteExplorerAuthoringMutation: mutation,
    useGetExplorerAuthoringCapabilityV2Query: () => ({ data: { features: {} } }),
    useGetExplorerAuthoringExplorersQuery: () => ({ data: [{ explorerId: 'builder', title: 'Core driver probe' }], isLoading: false, refetch: vi.fn() }),
    useGetExplorerBuilderStateV2Query: () => ({ data: fixture.state, isLoading: false, refetch: vi.fn() }),
    useGetExplorerCandidateSuggestionsV2Mutation: () => [fixture.getSuggestions, { isLoading: false }],
    usePreviewExplorerAuthoringV2Mutation: mutation,
    usePublishExplorerAuthoringV2Mutation: mutation,
    useReconcileExplorerBuilderV2Mutation: mutation,
    useResolveConfiguredColumnContextsQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false }),
    useResolvePopulationSelectionQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false, refetch: vi.fn() }),
  };
});

vi.mock('../useAutomaticPreview', () => ({ useAutomaticPreview: () => ({ error: undefined, isLoading: false }) }));
vi.mock('../hooks/useDirtyBeforeUnload', () => ({ useDirtyBeforeUnload: () => undefined }));
vi.mock('../hooks/usePortalHost', () => ({ usePortalHost: () => null }));
vi.mock('../components/DatasetReviewPanel', () => ({ DatasetReviewPanel: () => null }));
vi.mock('../components/PreviewTable', async (importOriginal) => ({
  ...await importOriginal<typeof import('../components/PreviewTable')>(),
  PreviewTable: () => null,
}));
vi.mock('../components/DataframeContractPanel', () => ({ DataframeContractPanel: () => null }));
vi.mock('../components/PopulationPanel', () => ({ PopulationPanel: () => null }));
vi.mock('../components/RowChangePreviewPanel', () => ({ RowChangePreviewPanel: () => null }));
vi.mock('../components/InterpretationPanel', () => ({ InterpretationPanel: () => null }));

const apiVersion = 'loom.calypr.org/explorer-authoring/v2' as const;
const outputId = 'patients';
const project = 'BForePC';
const explorerId = 'builder';
const groupStepId = 'group-step';
const aggregateOutputId = 'row-count';
const groupKeyOutputId = 'gender-group';
const baseConstruction: Construction = {
  version: 1,
  steps: [{
    id: groupStepId,
    inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: {
      kind: 'GROUP',
      group: {
        constructionId: groupStepId,
        missingKeyPolicy: 'GROUP',
        keys: [{ inputColumnId: 'gender-id', outputColumnId: groupKeyOutputId }],
        aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: aggregateOutputId }],
      },
    },
    outputs: [
      { id: groupKeyOutputId, name: 'gender', label: 'Gender', type: 'string' },
      { id: aggregateOutputId, name: 'row_count', label: 'Rows', type: 'integer' },
    ],
  }],
};

const document: ExplorerBuilderWorkspace['documents'][number] = {
  kind: 'ExplorerBuilderDocument',
  output: { id: outputId, title: 'Patients' },
  rootResourceType: 'Patient',
  route: { occurrenceId: 'base', resourceType: 'Patient' },
  rows: { kind: 'RECORDS', records: {} },
  columns: [
    {
      columnId: 'gender-id', column: 'gender', label: 'Gender', logicalType: 'string', occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'gender', projectionMode: 'VALUE' } },
      table: { visible: true, order: 0 },
    },
    {
      columnId: 'age-id', column: 'multiple_birth_integer', label: 'Multiple birth integer', logicalType: 'integer', occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'multipleBirthInteger', projectionMode: 'VALUE' } },
      table: { visible: true, order: 1 },
    },
  ],
  construction: baseConstruction,
};

const workspace: ExplorerBuilderWorkspace = {
  apiVersion,
  kind: 'ExplorerBuilderWorkspace',
  explorer: { title: 'Core driver probe' },
  documents: [document],
  tabs: [{ id: 'patients_tab', title: 'Patients', outputId, order: 0, visible: true }],
};

const sourceStage: ConstructionStageDescriptor = {
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  rowIdentityColumn: 'patient-row-id',
  columns: [
    { id: 'gender-id', name: 'gender', label: 'Gender', type: 'string', cardinality: 'optional_one' },
    { id: 'age-id', name: 'multiple_birth_integer', label: 'Multiple birth integer', type: 'integer', cardinality: 'optional_one' },
  ],
  capabilities: [{ kind: 'GROUP', supported: true }],
};
const stage: ConstructionStageDescriptor = {
  id: groupStepId,
  inputStageId: sourceStage.id,
  operation: 'GROUP',
  rowIdentityColumn: 'patient-row-id',
  columns: [
    { id: groupKeyOutputId, name: 'gender', label: 'Gender', type: 'string', cardinality: 'optional_one' },
    { id: aggregateOutputId, name: 'row_count', label: 'Rows', type: 'integer', cardinality: 'optional_one' },
  ],
  capabilities: [{ kind: 'DERIVE', supported: true }, { kind: 'FILTER', supported: true }],
};

const capabilities = constructionCapabilitiesResponseSchema.parse({
  snapshotToken: 'snapshot-core-driver-probe',
  draftVersion: 1,
  draftDigest: 'sha256:core-driver-probe',
  outputId,
  stageId: stage.id,
  baseConstruction,
  stages: [sourceStage, stage],
  selectedStage: stage,
  groupFieldChoices: [],
  groupRowValueChoices: [],
  semanticGroupChoices: [],
  workspaceInputs: [],
});

const jsonResponse = (value: unknown): Response => new Response(JSON.stringify(value), {
  status: 200,
  headers: { 'content-type': 'application/json' },
});

const deriveAdapter = coreAdapters['builder.calculate.derive'];

const driveInput = (values: Readonly<Record<string, unknown>>) => ({
  uiAction: {
    affordanceId: 'builder.calculate.derive',
    runnerActionId: deriveAdapter.runnerActionId,
    values,
  },
  requestStep: { actionId: 'proposalEndpoint', kind: 'proposal' },
});

const proposalResponseFor = (request: ConstructionProposalRequest): ConstructionProposalResponse => ({
  outputId: request.outputId,
  snapshotToken: request.snapshotToken,
  draftVersion: request.expectedDraftVersion,
  draftDigest: request.expectedDraftDigest,
  baseDocumentDigest: 'sha256:synthetic-base',
  candidateWorkspaceDigest: 'sha256:synthetic-candidate',
  changedStepId: request.changedStepId ?? '',
  candidateConstruction: request.candidateConstruction,
  dependencyImpact: { affectedStepIds: [] },
  stages: [sourceStage, stage],
  previewStatus: 'NEEDS_REPAIR',
  previewDurationMs: 0,
});

describe('core Calculate adapter dimensions', () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    fixture.requests.length = 0;
    fixture.state = {
      apiVersion,
      kind: 'ExplorerBuilderState',
      lifecycleState: 'READY',
      draftVersion: capabilities.draftVersion,
      draftDigest: capabilities.draftDigest,
      workspace,
      catalog: {
        snapshotToken: capabilities.snapshotToken,
        generation: 'generation-core-driver-probe',
        routePolicy: {},
        nodes: [{ nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 1 }],
        edges: [],
        candidates: [],
      },
    };
    fixture.capabilities = capabilities;
    fixture.proposal = null;
    fixture.resolveProposal = null;
    fixture.getSuggestions.mockClear();
    client = createLoomClient({
      baseUrl: 'https://core-driver-probe.invalid',
      fetch: async (requestInfo, init) => {
        const url = new URL(String(requestInfo));
        const request: CapturedRequest = {
          url: `${url.pathname}${url.search}`,
          method: init?.method ?? 'GET',
          body: typeof init?.body === 'string' ? init.body : null,
        };
        fixture.requests.push(request);
        if (url.pathname.endsWith('/construction-capabilities')) return jsonResponse(fixture.capabilities);
        if (url.pathname.endsWith('/construction-proposals')) {
          if (request.body === null) return jsonResponse({ error: 'missing proposal request body' });
          fixture.proposal = constructionProposalRequestSchema.parse(JSON.parse(request.body));
          return await new Promise<Response>((resolve) => { fixture.resolveProposal = resolve; });
        }
        return jsonResponse({});
      },
    });
  });

  afterEach(() => {
    fixture.resolveProposal?.(jsonResponse(fixture.proposal ? proposalResponseFor(fixture.proposal) : {}));
  });

  it.each([
    ['legacy camel-case operands', { expression_view: 'guided', operation: 'ADD', leftColumnId: 'age-id', rightColumnId: 'age-id' }, 'left_operand'],
    ['missing right operand', { expression_view: 'guided', operation: 'ADD', left_operand: { kind: 'COLUMN', columnId: aggregateOutputId } }, 'right_operand'],
    ['legacy formula alias', { expression_view: 'formula', formula: 'multiple_birth_integer + 1' }, 'formula_text'],
  ])('reports %s as an explicit adapter gap', async (_caseName, values, missingDimension) => {
    render(<BuilderWorkspace organization="HTAN_INT" project={project} explorerId={explorerId} />);

    const result = await deriveAdapter.drive(driveInput(values)).then(
      () => null,
      (error: unknown) => error,
    );
    expect(result).toBeInstanceOf(CoreAdapterGapError);
    if (!(result instanceof CoreAdapterGapError)) throw new Error('The malformed action did not produce an explicit adapter gap.');
    expect(result.kind).toBe('ADAPTER_GAP');
    expect(result.message).toContain(missingDimension);
    expect(fixture.proposal).toBeNull();
  });

  it('uses exact snake-case dimensions to emit the mounted Builder proposal request', async () => {
    render(<BuilderWorkspace organization="HTAN_INT" project={project} explorerId={explorerId} />);

    const driving = deriveAdapter.drive(driveInput({
      expression_view: 'guided',
      operation: 'ADD',
      left_operand: { kind: 'COLUMN', columnId: aggregateOutputId },
      right_operand: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 1 } },
      missing_input_policy: 'PROPAGATE_NULL',
      output_name: 'birth_count_plus_one',
      output_label: 'Birth count plus one',
    }));

    await waitFor(() => expect(fixture.proposal).not.toBeNull());
    await driving;
    const request = fixture.proposal;
    expect(request).not.toBeNull();
    if (!request) throw new Error('The real mounted Builder did not emit a proposal request.');
    expect(fixture.requests.filter((candidate) => candidate.url.includes('/construction-proposals'))).toHaveLength(1);
    expect(request.candidateConstruction.steps).toHaveLength(2);
    const derived = request.candidateConstruction.steps.at(-1);
    expect(derived?.operation).toMatchObject({
      kind: 'DERIVE',
      derive: {
        operation: 'ADD',
        left: { kind: 'COLUMN', columnId: aggregateOutputId },
        right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 1 } },
        missingInputPolicy: 'PROPAGATE_NULL',
      },
    });
    expect(derived?.outputs.at(-1)).toMatchObject({ name: 'birth_count_plus_one', label: 'Birth count plus one' });
  });
});

describe('core proposal lifecycle action IDs', () => {
  const lifecycleAdapter = coreAdapters['builder.construction_proposal_apply_cancel_retry'];
  const lifecycleInput = (actionId: string) => ({
    uiAction: {
      affordanceId: 'builder.construction_proposal_apply_cancel_retry',
      runnerActionId: lifecycleAdapter.runnerActionId,
      values: {},
    },
    requestStep: { actionId, kind: 'command' },
  });

  it('accepts the inventory command action ID and clicks Apply', async () => {
    const applied = vi.fn();
    render(<button type="button" data-testid="construction-apply-proposal" onClick={applied}>Apply</button>);

    await lifecycleAdapter.drive(lifecycleInput('accept_construction_proposal'));

    expect(applied).toHaveBeenCalledOnce();
  });

  it('rejects a generic endpoint alias instead of treating it as Apply', async () => {
    const applied = vi.fn();
    render(<button type="button" data-testid="construction-apply-proposal" onClick={applied}>Apply</button>);

    await expect(lifecycleAdapter.drive(lifecycleInput('applyEndpoint'))).rejects.toThrow('proposal lifecycle actionId applyEndpoint is not supported');
    expect(applied).not.toHaveBeenCalled();
  });

  it('clicks Cancel proposal only for the local cancel action', async () => {
    const cancelled = vi.fn();
    render(<button type="button" data-testid="construction-cancel-proposal" onClick={cancelled}>Cancel proposal</button>);

    await lifecycleAdapter.drive({
      uiAction: {
        affordanceId: 'builder.construction_proposal_apply_cancel_retry',
        runnerActionId: lifecycleAdapter.runnerActionId,
        values: { localAction: 'cancel_construction_proposal' },
      },
      requestStep: null,
      values: { localAction: 'cancel_construction_proposal' },
    });

    expect(cancelled).toHaveBeenCalledOnce();
  });

  it('clicks the mounted preview refresh control for refresh_preview_catalog', async () => {
    const refresh = vi.fn();
    render(<><button type="button" onClick={refresh}>Refresh preview</button><div role="region" aria-label="Preview" /></>);

    await coreAdapters['builder.automatic_preview_refresh_and_recovery'].drive({
      uiAction: {
        affordanceId: 'builder.automatic_preview_refresh_and_recovery',
        runnerActionId: coreAdapters['builder.automatic_preview_refresh_and_recovery'].runnerActionId,
        values: {},
      },
      requestStep: { actionId: 'refresh_preview_catalog', kind: 'command' },
    });

    expect(refresh).toHaveBeenCalledOnce();
  });

  it('does not treat another local action as Cancel proposal', async () => {
    const cancelled = vi.fn();
    render(<button type="button" data-testid="construction-cancel-proposal" onClick={cancelled}>Cancel proposal</button>);

    let caught: unknown;
    try {
      await lifecycleAdapter.drive({
        uiAction: {
          affordanceId: 'builder.construction_proposal_apply_cancel_retry',
          runnerActionId: lifecycleAdapter.runnerActionId,
          values: { localAction: 'accept_construction_proposal' },
        },
        requestStep: null,
        values: { localAction: 'accept_construction_proposal' },
      });
    } catch (error) {
      caught = error;
    }

    expect(cancelled).not.toHaveBeenCalled();
    expect(caught).toBeInstanceOf(CoreAdapterGapError);
    expect((caught as Error).message).toContain('proposal lifecycle actionId (missing) is not supported');
  });
});
