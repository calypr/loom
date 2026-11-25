// @vitest-environment jsdom

import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { builderWorkspaceNullMocks } from '../../../testSupport/builderWorkspaceMocks';
import { createLoomClient, type LoomClient, type ApplyExplorerBuilderCommandsArgs } from '../../client/api';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionProposalResponse,
  ConstructionStageDescriptor,
  ExplorerBuilderState,
  ExplorerBuilderWorkspace,
} from '../../contracts/types';
import {
  constructionCapabilitiesResponseSchema,
  constructionProposalRequestSchema,
  explorerBuilderStateSchema,
  explorerBuilderWorkspaceSchema,
} from '../../contracts/types';
import { relationalAdapters } from '../../../testSupport/builderContractAdapters/relational';
import { coreAdapters } from '../../../testSupport/builderContractAdapters/core';
import BuilderWorkspace from './BuilderWorkspace';

type CapturedRequest = {
  readonly url: string;
  readonly method: string;
  readonly body: string | null;
};

type CapturedResponse = {
  readonly requestIndex: number;
  readonly url: string;
  readonly status: number;
  readonly body: string;
};

const harness = vi.hoisted(() => ({
  requests: [] as CapturedRequest[],
  responses: [] as CapturedResponse[],
  builderState: null as ExplorerBuilderState | null,
  capabilities: null as ConstructionCapabilitiesResponse | null,
  proposalStatus: 'NEEDS_REPAIR' as 'READY' | 'NEEDS_REPAIR',
  mismatchProposalSnapshot: false,
  applyCommands: vi.fn(),
  getSuggestions: vi.fn(() => ({
    unwrap: () => Promise.resolve({ snapshotToken: 'snapshot-ui-contract-capture', candidates: [] }),
  })),
}));

let client!: LoomClient;

vi.mock('./session/useBuilderLoad', () => ({
  useGetExplorerBuilderStateV2Query: () => ({
    data: harness.builderState,
    isLoading: false,
    refetch: vi.fn(),
  }),
}));

vi.mock('../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../react')>();
  const mutation = () => [vi.fn(), { isLoading: false }];
  return {
    ...actual,
    useLoomClient: () => client,
    useApplyExplorerBuilderCommandsV2Mutation: () => [harness.applyCommands, { isLoading: false }],
    useAssessExplorerRowChangeMutation: mutation,
    useCreateExplorerAuthoringMutation: mutation,
    useDeleteExplorerAuthoringMutation: mutation,
    useGetExplorerAuthoringCapabilityV2Query: () => ({ data: { features: {} } }),
    useGetExplorerAuthoringExplorersQuery: () => ({
      data: [{ explorerId: 'builder', title: 'UI contract capture' }],
      isLoading: false,
      refetch: vi.fn(),
    }),
    useGetExplorerCandidateSuggestionsV2Mutation: () => [harness.getSuggestions, { isLoading: false }],
    usePreviewExplorerAuthoringV2Mutation: mutation,
    usePublishExplorerAuthoringV2Mutation: mutation,
    useReconcileExplorerBuilderV2Mutation: mutation,
    useResolveConfiguredColumnContextsQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false }),
    useResolvePopulationSelectionQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false, refetch: vi.fn() }),
  };
});

vi.mock('./useAutomaticPreview', () => ({ useAutomaticPreview: () => ({ error: undefined, isLoading: false }) }));
vi.mock('./hooks/useDirtyBeforeUnload', () => ({ useDirtyBeforeUnload: () => undefined }));
vi.mock('./hooks/usePortalHost', () => ({ usePortalHost: () => null }));
vi.mock('./components/DatasetReviewPanel', () => ({ DatasetReviewPanel: () => null }));
vi.mock('./components/PreviewTable', async (importOriginal) => ({
  ...await importOriginal<typeof import('./components/PreviewTable')>(),
  PreviewTable: () => null,
}));
vi.mock('./components/DataframeContractPanel', () => builderWorkspaceNullMocks.DataframeContractPanel);
vi.mock('./components/PopulationPanel', () => builderWorkspaceNullMocks.PopulationPanel);
vi.mock('./components/RowChangePreviewPanel', () => ({ RowChangePreviewPanel: () => null }));
vi.mock('./components/InterpretationPanel', () => builderWorkspaceNullMocks.InterpretationPanel);

const apiVersion = 'loom.calypr.org/explorer-authoring/v2' as const;
const outputId = 'patients';
const groupStepId = 'group-step';
const keyOutputId = 'gender-group';
const aggregateOutputId = 'row-count';
const project = 'BForePC';
const explorerId = 'builder';

const acceptedConstruction: Construction = {
  version: 1,
  steps: [{
    id: groupStepId,
    inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: {
      kind: 'GROUP',
      group: {
        constructionId: groupStepId,
        missingKeyPolicy: 'GROUP',
        keys: [{ inputColumnId: 'gender-id', outputColumnId: keyOutputId }],
        aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: aggregateOutputId }],
      },
    },
    outputs: [
      { id: keyOutputId, name: 'gender', label: 'Gender', type: 'string' },
      { id: aggregateOutputId, name: 'row_count', label: 'Rows', type: 'integer' },
    ],
  }],
};

const acceptedDocument: ExplorerBuilderWorkspace['documents'][number] = {
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
      columnId: 'age-id', column: 'age', label: 'Age', logicalType: 'integer', occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'age', projectionMode: 'VALUE' } },
      table: { visible: true, order: 1 },
    },
  ],
  construction: acceptedConstruction,
};

const acceptedWorkspace: ExplorerBuilderWorkspace = {
  apiVersion,
  kind: 'ExplorerBuilderWorkspace',
  explorer: { title: 'UI contract capture' },
  documents: [acceptedDocument],
  tabs: [{ id: 'patients_tab', title: 'Patients', outputId, order: 0, visible: true }],
};

const sourceStage: ConstructionStageDescriptor = {
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  rowIdentityColumn: 'patient-row-id',
  columns: [
    { id: 'gender-id', name: 'gender', label: 'Gender', type: 'string', cardinality: 'optional_one' },
    { id: 'age-id', name: 'age', label: 'Age', type: 'integer', cardinality: 'optional_one' },
  ],
  capabilities: [{ kind: 'GROUP', supported: true }],
};

const capabilities: ConstructionCapabilitiesResponse = constructionCapabilitiesResponseSchema.parse({
  sourceInput: { supported: false, stageId: sourceStage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
  snapshotToken: 'snapshot-ui-contract-capture',
  draftVersion: 1,
  draftDigest: 'sha256:accepted-group-count',
  outputId,
  stageId: sourceStage.id,
  baseConstruction: acceptedConstruction,
  stages: [sourceStage],
  selectedStage: sourceStage,
  groupFieldChoices: [],
  groupRowValueChoices: [],
  semanticGroupChoices: [],
  workspaceInputs: [],
});

const acceptedState: ExplorerBuilderState = {
  apiVersion,
  kind: 'ExplorerBuilderState',
  lifecycleState: 'READY',
  draftVersion: capabilities.draftVersion,
  draftDigest: capabilities.draftDigest,
  workspace: acceptedWorkspace,
  catalog: {
    snapshotToken: capabilities.snapshotToken,
    generation: 'generation-ui-contract-capture',
    routePolicy: {},
    nodes: [{ nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 }],
    edges: [],
    candidates: [],
  },
};

const captureJsonResponse = (url: string, value: unknown): Response => {
  const body = JSON.stringify(value);
  harness.responses.push({ requestIndex: harness.requests.length - 1, url, status: 200, body });
  return new Response(body, { status: 200, headers: { 'content-type': 'application/json' } });
};

const capture = {
  request: undefined as CapturedRequest | undefined,
  requestArgs: undefined as ConstructionProposalRequest | undefined,
};

export type ContractAdapterInput = {
  readonly uiAction: {
    readonly affordanceId: string;
    readonly runnerActionId: string;
    readonly values: Readonly<Record<string, unknown>>;
  };
  readonly requestStep: Readonly<{
    flowId: string;
    stepId: string;
    kind: string;
    method: string;
    endpoint: string;
    actionId: string;
    order: number;
  }> | null;
};

const resetHarness = (): void => {
  window.sessionStorage.clear();
  harness.requests.length = 0;
  harness.responses.length = 0;
  harness.builderState = acceptedState;
  harness.capabilities = capabilities;
  harness.proposalStatus = 'NEEDS_REPAIR';
  harness.mismatchProposalSnapshot = false;
  harness.getSuggestions.mockClear();
  harness.applyCommands.mockReset();
  harness.applyCommands.mockImplementation((args: ApplyExplorerBuilderCommandsArgs) => ({
    unwrap: () => client.applyCommands(args),
  }));
  capture.request = undefined;
  capture.requestArgs = undefined;
  client = createLoomClient({
    baseUrl: 'https://ui-contract-capture.invalid',
    fetch: async (input, init) => {
      const url = new URL(String(input));
      const request: CapturedRequest = {
        url: `${url.pathname}${url.search}`,
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? init.body : null,
      };
      harness.requests.push(request);
      if (url.pathname.endsWith('/commands')) {
        const body = JSON.parse(request.body ?? 'null') as { commandId: string };
        const workspace: ExplorerBuilderWorkspace = {
          ...acceptedWorkspace,
          documents: [{ ...acceptedDocument, construction: capture.requestArgs?.candidateConstruction ?? acceptedConstruction }],
        };
        return captureJsonResponse(request.url, {
          commandId: body.commandId, workspace, draftVersion: 2,
          draftDigest: 'sha256:applied-group-change', results: [], diagnostics: [],
        });
      }
      if (url.pathname.endsWith('/construction-capabilities')) return captureJsonResponse(request.url, harness.capabilities);
      if (url.pathname.endsWith('/construction-proposals')) {
        if (!request.body) return captureJsonResponse(request.url, { error: 'missing proposal body' });
        const proposalRequest = constructionProposalRequestSchema.parse(JSON.parse(request.body));
        capture.request = request;
        capture.requestArgs = proposalRequest;
        const proposalResponse: ConstructionProposalResponse = {
          ...(harness.proposalStatus === 'READY' ? { proposalId: 'proposal-ui-contract-fixture' } : {}),
          outputId: proposalRequest.outputId,
          snapshotToken: harness.mismatchProposalSnapshot ? 'snapshot-other-owner' : proposalRequest.snapshotToken,
          draftVersion: proposalRequest.expectedDraftVersion,
          draftDigest: proposalRequest.expectedDraftDigest,
          baseDocumentDigest: 'sha256:synthetic-transport-response',
          candidateWorkspaceDigest: 'sha256:synthetic-candidate-response',
          changedStepId: proposalRequest.changedStepId ?? '',
          candidateConstruction: proposalRequest.candidateConstruction,
          dependencyImpact: { affectedStepIds: [] },
          stages: harness.capabilities?.stages ?? [sourceStage],
          previewStatus: harness.proposalStatus,
          previewDurationMs: 0,
          ...(harness.proposalStatus === 'READY' ? {
            preview: {
              apiVersion, kind: 'ExplorerBuilderPreview' as const,
              receiptId: 'proposal-ui-contract-fixture', outputId: proposalRequest.outputId,
              columns: [], rows: [],
              rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'SYNTHETIC_UI_FIXTURE' },
              rowCount: 0, diagnostics: [],
            },
          } : {}),
        };
        return captureJsonResponse(request.url, proposalResponse);
      }
      return captureJsonResponse(request.url, {});
    },
  });
};

describe('mounted BuilderWorkspace UI contract behaviors', () => {
  beforeEach(resetHarness);

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('opens the real Group editor and captures the exact serialized proposal request', async () => {
    const actionSequence = ['open saved Group step', 'select MEAN in Summary 1'];
    render(<BuilderWorkspace organization="HTAN_INT" project={project} explorerId={explorerId} />);
    await openSavedGroupStep();
    fireEvent.change(screen.getByRole('combobox', { name: 'Summary 1' }), { target: { value: 'MEAN' } });

    await waitFor(() => expect(capture.request).toBeDefined(), { timeout: 5000 });
    const request = capture.request;
    const body = capture.requestArgs;
    expect(request, actionSequence.join(' → ')).toBeDefined();
    expect(body, actionSequence.join(' → ')).toBeDefined();
    if (!request || !body) throw new Error(`No actual proposal capture after ${actionSequence.join(' → ')}`);
    expect(request.method).toBe('POST');
    expect(request.url).toContain(`/explorers/${explorerId}/authoring/v2/construction-proposals`);
    expect(constructionProposalRequestSchema.parse(JSON.parse(request.body ?? 'null'))).toEqual(body);
    expect(harness.requests.filter((entry) => entry.url.includes('/construction-proposals'))).toHaveLength(1);
    await screen.findByTestId('construction-proposal-needs-repair');
    expect(screen.queryByTestId('construction-apply-proposal')).toBeNull();
    expect(harness.applyCommands).not.toHaveBeenCalled();
  });

  const commandRequests = () => harness.requests.filter(request =>
    new URL(request.url, 'https://ui-contract-capture.invalid').pathname.endsWith('/commands'));

  const savedGroupStepControl = () => waitFor(() => {
    const item = [...document.querySelectorAll<HTMLElement>('[data-operation-id]')]
      .find((candidate) => candidate.getAttribute('data-operation-id') === groupStepId);
    const button = item?.querySelector('button');
    if (!(button instanceof HTMLButtonElement)) {
      throw new Error(`The mounted recipe navigator does not offer saved construction step ${groupStepId}.`);
    }
    return button;
  });

  const openSavedGroupStep = async () => {
    fireEvent.click(await savedGroupStepControl());
    await screen.findByTestId('construction-reshape-editor');
  };

  const editGroup = async (operation: 'SUM' | 'MEAN' = 'MEAN') => {
    harness.proposalStatus = 'READY';
    render(<BuilderWorkspace organization="HTAN_INT" project={project} explorerId={explorerId} />);
    await relationalAdapters['builder.reshape.group_field'].drive({
      uiAction: {
        affordanceId: 'builder.reshape.group_field',
        runnerActionId: 'builder.group-field.edit-aggregate',
        values: { groupStepId, operation, inputColumnId: 'age-id' },
      },
      requestStep: null,
    });
    if (!capture.requestArgs) throw new Error('Group edit emitted no proposal request.');
    return capture.requestArgs;
  };

  it.each(['SUM', 'MEAN'] as const)('serializes the %s editor change with its numeric input and preserved saved identifiers', async (operation) => {
    const request = await editGroup(operation);
    expect(request).toMatchObject({
      outputId: 'patients', snapshotToken: 'snapshot-ui-contract-capture',
      expectedDraftVersion: 1, expectedDraftDigest: 'sha256:accepted-group-count',
      changedStepId: 'group-step',
    });
    expect(request.candidateConstruction.steps).toHaveLength(1);
    expect(request.candidateConstruction.steps[0]).toMatchObject({
      id: 'group-step', inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'GROUP', group: {
        constructionId: 'group-step', missingKeyPolicy: 'GROUP',
        keys: [{ inputColumnId: 'gender-id', outputColumnId: 'gender-group' }],
        aggregates: [{ operation, inputColumnId: 'age-id', outputColumnId: 'row-count' }],
      } },
    });
    expect(commandRequests()).toHaveLength(0);
  });

  it('rejects an unsupported aggregate before the adapter changes the editor or sends a request', async () => {
    render(<BuilderWorkspace organization="HTAN_INT" project={project} explorerId={explorerId} />);
    await savedGroupStepControl();
    await expect(relationalAdapters['builder.reshape.group_field'].drive({
      uiAction: { affordanceId: 'builder.reshape.group_field', runnerActionId: 'builder.group-field.edit-aggregate',
        values: { groupStepId, operation: 'NOT_AN_AGGREGATE' } }, requestStep: null,
    })).rejects.toThrow('supported aggregate operation');
    expect(screen.queryByTestId('construction-reshape-editor')).toBeNull();
    expect(harness.requests.filter(request => request.url.endsWith('/construction-proposals'))).toHaveLength(0);
    expect(harness.applyCommands).not.toHaveBeenCalled();
  });

  it('cancels a reviewed change without submitting a save command', async () => {
    await editGroup();
    await coreAdapters['builder.construction_proposal_apply_cancel_retry'].drive({
      uiAction: { affordanceId: 'builder.construction_proposal_apply_cancel_retry', runnerActionId: 'core.driveConstructionProposalLifecycle', values: { localAction: 'cancel_construction_proposal' } },
      requestStep: null,
    });
    await waitFor(() => expect(screen.queryByTestId('construction-proposal-ready')).toBeNull());
    expect(harness.applyCommands).not.toHaveBeenCalled();
    expect(commandRequests()).toHaveLength(0);
    await openSavedGroupStep();
    expect((await screen.findByRole('combobox', { name: 'Summary 1' }) as HTMLSelectElement).value).toBe('COUNT_ROWS');
  });

  it('applies only the reviewed receipt through the real client with the current CAS identity', async () => {
    await editGroup();
    await coreAdapters['builder.construction_proposal_apply_cancel_retry'].drive({
      uiAction: { affordanceId: 'builder.construction_proposal_apply_cancel_retry', runnerActionId: 'core.driveConstructionProposalLifecycle', values: {} },
      requestStep: { actionId: 'accept_construction_proposal' },
    });
    await waitFor(() => expect(commandRequests()).toHaveLength(1));
    const command = commandRequests()[0];
    expect(command?.method).toBe('POST');
    expect(JSON.parse(command?.body ?? 'null')).toMatchObject({
      snapshotToken: 'snapshot-ui-contract-capture', expectedDraftVersion: 1,
      expectedDraftDigest: 'sha256:accepted-group-count',
      commands: [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId: 'patients', proposalId: 'proposal-ui-contract-fixture' }],
    });
    expect(harness.applyCommands).toHaveBeenCalledOnce();
    await waitFor(() => expect(screen.queryByTestId('construction-reshape-editor')).toBeNull());
  });

  it('rejects a ready response for a different snapshot before exposing Apply', async () => {
    harness.proposalStatus = 'READY';
    harness.mismatchProposalSnapshot = true;
    render(<BuilderWorkspace organization="HTAN_INT" project={project} explorerId={explorerId} />);
    await openSavedGroupStep();
    fireEvent.change(await screen.findByRole('combobox', { name: 'Summary 1' }), { target: { value: 'MEAN' } });
    await screen.findByTestId('construction-proposal-error');
    expect(capture.requestArgs?.snapshotToken).toBe('snapshot-ui-contract-capture');
    expect(screen.queryByTestId('construction-proposal-ready')).toBeNull();
    expect(screen.queryByTestId('construction-apply-proposal')).toBeNull();
    expect(harness.applyCommands).not.toHaveBeenCalled();
  });

});
