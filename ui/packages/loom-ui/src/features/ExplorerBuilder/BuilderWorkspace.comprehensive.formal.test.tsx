// @vitest-environment jsdom
/// <reference types="node" />
import React from 'react';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient, type LoomClient, type ApplyExplorerBuilderCommandsArgs } from '../../api';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionProposalResponse,
  ConstructionStageDescriptor,
  ExplorerBuilderState,
  ExplorerBuilderWorkspace,
} from '../../types';
import {
  constructionCapabilitiesResponseSchema,
  constructionProposalRequestSchema,
  explorerBuilderStateSchema,
  explorerBuilderWorkspaceSchema,
} from '../../types';
import { peripheralAdapters } from '../../../testSupport/builderContractAdapters/peripheral';
import { relationalAdapters } from '../../../testSupport/builderContractAdapters/relational';
import { shapeCombineAdapters } from '../../../testSupport/builderContractAdapters/shapeCombine';
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

let client: LoomClient;

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
    useGetExplorerBuilderStateV2Query: () => ({
      data: harness.builderState,
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
vi.mock('./components/DataframeContractPanel', () => ({ DataframeContractPanel: () => null }));
vi.mock('./components/PopulationPanel', () => ({ PopulationPanel: () => null }));
vi.mock('./components/RowChangePreviewPanel', () => ({ RowChangePreviewPanel: () => null }));
vi.mock('./components/InterpretationPanel', () => ({ InterpretationPanel: () => null }));

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

export type ContractAdapter = {
  readonly adapterId: string;
  readonly runnerActionId: string;
  readonly sourceRefs: ReadonlyArray<string>;
  readonly drive: (input: ContractAdapterInput) => void | Promise<void>;
};

export const mergeBuilderContractAdapterRegistries = (
  ...registries: ReadonlyArray<Readonly<Record<string, ContractAdapter>>>
): Readonly<Record<string, ContractAdapter>> => {
  const merged: Record<string, ContractAdapter> = {};
  for (const registry of registries) {
    for (const [id, adapter] of Object.entries(registry)) {
      if (merged[id]) throw new Error(`Duplicate Builder UI adapter registration for ${id}.`);
      merged[id] = adapter;
    }
  }
  return Object.freeze(merged);
};

// Inventory IDs never manufacture adapter rows; only callable mounted drivers
// appear in these registries.
const centralContractAdapters: Readonly<Record<string, ContractAdapter>> = Object.freeze({});
export const BUILDER_CONTRACT_ADAPTERS = mergeBuilderContractAdapterRegistries(
  centralContractAdapters,
  coreAdapters,
  relationalAdapters,
  shapeCombineAdapters,
  peripheralAdapters,
);

const contractPhase = process.env.BUILDER_CONTRACT_PHASE ?? 'smoke';

type JsonRecord = Record<string, unknown>;

const asJsonRecord = (value: unknown): JsonRecord | undefined =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as JsonRecord
    : undefined;

const readJsonlRecords = (path: string | undefined): { readonly records: ReadonlyArray<JsonRecord>; readonly error?: string; readonly bytes?: Buffer } => {
  if (!path) return { records: [], error: 'Required JSONL path is not configured.' };
  try {
    const bytes = readFileSync(path);
    const records = bytes.toString('utf8').split(/\r?\n/).filter((line) => line.length > 0)
      .map((line, index) => {
        try {
          const record = asJsonRecord(JSON.parse(line));
          if (!record) throw new Error('JSONL value must be an object.');
          return record;
        } catch (error) {
          throw new Error(`${path}:${index + 1}: ${error instanceof Error ? error.message : String(error)}`);
        }
      });
    return { records, bytes };
  } catch (error) {
    return { records: [], error: error instanceof Error ? error.message : String(error) };
  }
};

const keyForContractRow = (row: JsonRecord): string | undefined => {
  const caseId = row.caseId;
  const entryId = row.entryId;
  const actionIndex = row.actionIndex;
  return typeof caseId === 'string' && typeof entryId === 'string' && Number.isInteger(actionIndex)
    ? `${caseId}\u0000${entryId}\u0000${actionIndex}`
    : undefined;
};

const captureModelFile = contractPhase === 'capture'
  ? readJsonlRecords(process.env.BUILDER_FULL_UI_INVENTORY_JSONL)
  : undefined;
const preparedSeedFile = contractPhase === 'capture'
  ? readJsonlRecords(process.env.BUILDER_FULL_UI_PREPARED_SEEDS_JSONL)
  : undefined;
const captureModelRows = (captureModelFile?.records ?? []).filter((row) => row.recordType === 'case');
const preparedSeedByKey = new Map(
  (preparedSeedFile?.records ?? [])
    .filter((row) => row.recordType === 'prepared-seed')
    .flatMap((row) => {
      const key = keyForContractRow(row);
      return key ? [[key, row] as const] : [];
    }),
);
const captureObservations = new Map<string, JsonRecord>();
const captureOutputPath = process.env.BUILDER_FULL_UI_UI_OBSERVATIONS;

const centralTestSourcePath = 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.comprehensive.formal.test.tsx';
const adapterImplementationPathById = new Map<string, string>([
  ...Object.keys(centralContractAdapters).map((id) => [id, centralTestSourcePath] as const),
  ...Object.keys(coreAdapters).map((id) => [id, 'ui/packages/loom-ui/testSupport/builderContractAdapters/core.ts'] as const),
  ...Object.keys(relationalAdapters).map((id) => [id, 'ui/packages/loom-ui/testSupport/builderContractAdapters/relational.ts'] as const),
  ...Object.keys(shapeCombineAdapters).map((id) => [id, 'ui/packages/loom-ui/testSupport/builderContractAdapters/shapeCombine.ts'] as const),
  ...Object.keys(peripheralAdapters).map((id) => [id, 'ui/packages/loom-ui/testSupport/builderContractAdapters/peripheral.tsx'] as const),
]);
const hashNamedBytes = (files: ReadonlyArray<{ readonly path: string; readonly bytes: Buffer }>): string => {
  const hash = createHash('sha256');
  for (const file of [...files].sort((left, right) => left.path.localeCompare(right.path))) {
    hash.update(file.path).update('\0').update(file.bytes).update('\0');
  }
  return hash.digest('hex');
};
const adapterSourceManifest = (): ReadonlyArray<{ readonly path: string; readonly sha256: string; readonly bytes: Buffer }> => {
  const files = new Map<string, Buffer>();
  files.set(centralTestSourcePath, readFileSync(new URL(import.meta.url)));
  for (const path of new Set(adapterImplementationPathById.values())) {
    if (path === centralTestSourcePath) continue;
    files.set(path, readFileSync(resolve(process.cwd(), '../../..', path)));
  }
  return [...files.entries()].map(([path, bytes]) => ({
    path,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    bytes,
  })).sort((left, right) => left.path.localeCompare(right.path));
};

const requestKindFromUrl = (url: string): string => {
  if (url.includes('/construction-proposals')) return 'constructionProposal';
  if (url.endsWith('/preview')) return 'preview';
  if (url.endsWith('/commands')) return 'commands';
  if (url.endsWith('/construction-capabilities')) return 'constructionCapabilities';
  return 'http';
};

const parseRequestBody = (body: string | null): unknown => {
  if (body === null) return null;
  try { return JSON.parse(body) as unknown; } catch { return null; }
};

const requestObservation = (request: CapturedRequest, requestIndex: number): JsonRecord => ({
  requestIndex,
  kind: requestKindFromUrl(request.url),
  url: request.url,
  method: request.method,
  bodyRaw: request.body,
  bodyParsed: parseRequestBody(request.body),
  bodySha256: request.body === null ? null : createHash('sha256').update(request.body).digest('hex'),
  captureBoundary: 'LoomClient fetch RequestInit',
});

const responseObservation = (response: CapturedResponse): JsonRecord => ({
  requestIndex: response.requestIndex,
  url: response.url,
  status: response.status,
  bodyRaw: response.body,
  bodyParsed: parseRequestBody(response.body),
  source: 'synthetic-ui-transport',
});

const modelEndpointFor = (modelCase: JsonRecord): string | undefined => {
  const action = asJsonRecord(modelCase.action);
  const requestStep = asJsonRecord(modelCase.requestStep) ?? asJsonRecord(action?.requestStep);
  return typeof requestStep?.endpoint === 'string' ? requestStep.endpoint : undefined;
};

const endpointMatches = (actualUrl: string, expectedEndpoint: string): boolean => {
  const expectedPath = expectedEndpoint.split('?')[0]?.replace(/[.,;]$/, '') ?? expectedEndpoint;
  const versionedIndex = expectedPath.indexOf('/authoring/v2/');
  const suffix = versionedIndex >= 0
    ? expectedPath.slice(versionedIndex)
    : expectedPath.split('/').filter(Boolean).slice(-1)[0] ?? expectedPath;
  return actualUrl.split('?')[0]?.endsWith(suffix) ?? false;
};

const actualTargetRequestIndex = (modelCase: JsonRecord): number | undefined => {
  const expectedEndpoint = modelEndpointFor(modelCase);
  if (!expectedEndpoint) return undefined;
  const reversed = harness.requests.map((request, requestIndex) => ({ request, requestIndex })).reverse();
  return reversed.find(({ request }) => endpointMatches(request.url, expectedEndpoint))?.requestIndex;
};

const controlStateSnapshot = (): ReadonlyArray<JsonRecord> =>
  [...document.querySelectorAll<HTMLElement>('button,input,select,textarea,[role="button"],[role="combobox"]')]
    .map((element) => {
      const tag = element.tagName.toLocaleLowerCase();
      const nativeRole = tag === 'button' ? 'button' : tag === 'select' ? 'combobox'
        : tag === 'textarea' ? 'textbox'
          : tag === 'input' ? (element.getAttribute('type') === 'checkbox' ? 'checkbox'
            : element.getAttribute('type') === 'radio' ? 'radio' : 'textbox')
            : null;
      const labels = 'labels' in element ? (element as HTMLInputElement).labels : null;
      const accessibleName = element.getAttribute('aria-label') ?? labels?.[0]?.textContent?.trim()
        ?? element.getAttribute('title') ?? element.textContent?.trim() ?? '';
      const value = element instanceof HTMLInputElement || element instanceof HTMLSelectElement || element instanceof HTMLTextAreaElement
        ? element.value
        : null;
      const checked = element instanceof HTMLInputElement && (element.type === 'checkbox' || element.type === 'radio')
        ? element.checked
        : null;
      const disabled = 'disabled' in element ? Boolean((element as HTMLButtonElement).disabled)
        : element.getAttribute('aria-disabled') === 'true';
      const style = getComputedStyle(element);
      return {
        role: element.getAttribute('role') ?? nativeRole,
        accessibleName,
        testId: element.getAttribute('data-testid'),
        disabled,
        visible: !element.closest('[hidden],[aria-hidden="true"]') && style.display !== 'none' && style.visibility !== 'hidden',
        value,
        checked,
        pressed: element.getAttribute('aria-pressed'),
        expanded: element.getAttribute('aria-expanded'),
      };
    });

const changedControl = (
  before: ReadonlyArray<JsonRecord>,
  after: ReadonlyArray<JsonRecord>,
): JsonRecord | null => {
  const key = (control: JsonRecord) => `${String(control.role)}\u0000${String(control.accessibleName)}\u0000${String(control.testId)}`;
  const beforeByKey = new Map(before.map((control) => [key(control), JSON.stringify(control)]));
  const changed = after.filter((control) => beforeByKey.get(key(control)) !== JSON.stringify(control));
  return changed.at(-1) ?? null;
};

const actualRequestForModelCase = (modelCase: JsonRecord): JsonRecord | null => {
  const requestIndex = actualTargetRequestIndex(modelCase);
  if (requestIndex === undefined) return null;
  const request = harness.requests[requestIndex];
  return request ? requestObservation(request, requestIndex) : null;
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
          commandId: body.commandId,
          workspace,
          draftVersion: 2,
          draftDigest: 'sha256:applied-group-change',
          results: [],
          diagnostics: [],
        });
      }
      if (url.pathname.endsWith('/construction-capabilities')) {
        return captureJsonResponse(request.url, harness.capabilities);
      }
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
              apiVersion,
              kind: 'ExplorerBuilderPreview' as const,
              receiptId: 'proposal-ui-contract-fixture',
              outputId: proposalRequest.outputId,
              columns: [],
              rows: [],
              rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'SYNTHETIC_UI_FIXTURE' },
              rowCount: 0,
              diagnostics: [],
            },
          } : {}),
        };
        return captureJsonResponse(request.url, proposalResponse);
      }
      return captureJsonResponse(request.url, {});
    },
  });
};

if (contractPhase === 'inventory') {
  describe('Builder UI contract adapter inventory', () => {
    it('exports only registered callable handlers and reports uncovered inventory IDs', () => {
      const inventoryPath = process.env.BUILDER_CONTRACT_INVENTORY;
      const outputPath = process.env.BUILDER_CONTRACT_OUTPUT;
      const modelPath = process.env.BUILDER_FULL_UI_INVENTORY_JSONL;
      if (!inventoryPath || !outputPath || !modelPath) {
        throw new Error('Inventory phase requires BUILDER_CONTRACT_INVENTORY, BUILDER_CONTRACT_OUTPUT, and BUILDER_FULL_UI_INVENTORY_JSONL.');
      }
      const inventoryBytes = readFileSync(inventoryPath);
      const inventory = JSON.parse(inventoryBytes.toString('utf8')) as {
        readonly schemaVersion: number;
        readonly sourceCommit: string;
        readonly affordances: ReadonlyArray<{ readonly id: string }>;
      };
      const modelBytes = readFileSync(modelPath);
      const inventoryIds = new Set(inventory.affordances.map((entry) => entry.id));
      const registryIds = Object.keys(BUILDER_CONTRACT_ADAPTERS);
      const duplicateIds = registryIds.filter((id, index) => registryIds.indexOf(id) !== index);
      const unknownIds = registryIds.filter((id) => !inventoryIds.has(id));
      const missingAdapterIds = [...inventoryIds].filter((id) => !registryIds.includes(id)).sort();
      expect(inventory.schemaVersion).toBe(1);
      expect(inventory.sourceCommit).toBe('dc331783dde3f61e764bab3a421b66b28698c9d4');
      expect(duplicateIds).toEqual([]);
      expect(unknownIds).toEqual([]);
      const adapterSources = adapterSourceManifest();
      const header = {
        recordType: 'header',
        schemaVersion: 1,
        phase: 'inventory',
        sourceCommit: inventory.sourceCommit,
        adapterCommit: process.env.BUILDER_CONTRACT_ADAPTER_COMMIT ?? null,
        inventorySha256: createHash('sha256').update(inventoryBytes).digest('hex'),
        modelJsonlSha256: createHash('sha256').update(modelBytes).digest('hex'),
        adapterSourceSha256: hashNamedBytes(adapterSources),
        adapterSources: adapterSources.map(({ path, sha256 }) => ({ path, sha256 })),
        adapters: Object.entries(BUILDER_CONTRACT_ADAPTERS).map(([id, adapter]) => ({
          id,
          adapterId: adapter.adapterId,
          runnerActionId: adapter.runnerActionId,
          status: 'mapped' as const,
          sourceRefs: adapter.sourceRefs,
          implementationSources: adapterSourceManifest().filter(({ path }) =>
            path === adapterImplementationPathById.get(id)).map(({ path, sha256 }) => ({ path, sha256 })),
        })),
        missingAdapterIds,
        totals: {
          inventoryEntries: inventory.affordances.length,
          registeredAdapters: Object.keys(BUILDER_CONTRACT_ADAPTERS).length,
          mappedAdapters: Object.keys(BUILDER_CONTRACT_ADAPTERS).length,
          missingAdapterCount: missingAdapterIds.length,
        },
      };
      mkdirSync(dirname(outputPath), { recursive: true });
      writeFileSync(outputPath, `${JSON.stringify(header)}\n`);
      console.info(`Builder UI adapter inventory: ${header.totals.mappedAdapters}/${header.totals.inventoryEntries} entries mapped; ${header.totals.missingAdapterCount} lack executable handlers.`);
    });
  });
} else if (contractPhase === 'capture') {
  describe('Builder full UI model-driven request capture', () => {
    beforeEach(resetHarness);

    const setupFailureObservation = (modelCase: JsonRecord, error: string): JsonRecord => {
      const key = keyForContractRow(modelCase);
      const prepared = key ? preparedSeedByKey.get(key) : undefined;
      return {
        recordType: 'ui-observation',
        schemaVersion: 1,
        caseId: modelCase.caseId,
        entryId: modelCase.entryId,
        actionIndex: modelCase.actionIndex,
        requestMode: modelCase.requestMode,
        status: 'SETUP_FAILURE',
        model: modelCase,
        capabilityProvenance: prepared?.capabilityProvenance ?? null,
        error,
        actual: {
          acceptedWorkspace: prepared?.acceptedWorkspace ?? null,
          preparedIdentity: prepared?.identity ?? null,
          preparedSeed: prepared ?? null,
          builderState: prepared?.builderState ?? null,
          capabilitiesResponse: prepared?.capabilities ?? null,
          capabilityProvenance: prepared?.capabilityProvenance ?? null,
          uiControl: null,
          controlState: [],
          request: null,
          response: null,
          requests: [],
          responses: [],
        },
      };
    };

    const modelGapObservation = (modelCase: JsonRecord): JsonRecord => {
      const action = asJsonRecord(modelCase.action);
      const expectation = asJsonRecord(action?.expectation);
      const reason = typeof expectation?.reason === 'string'
        ? expectation.reason
        : 'The bounded model marks this transition as an explicit semantic GAP.';
      return {
        recordType: 'ui-observation',
        schemaVersion: 1,
        caseId: modelCase.caseId,
        entryId: modelCase.entryId,
        actionIndex: modelCase.actionIndex,
        requestMode: modelCase.requestMode,
        status: 'GAP',
        model: modelCase,
        error: reason,
      };
    };

    const captureOneModelCase = async (modelCase: JsonRecord): Promise<JsonRecord> => {
      const key = keyForContractRow(modelCase);
      if (!key) throw new Error('Model row lacks exact (caseId, entryId, actionIndex) key.');
      const prepared = preparedSeedByKey.get(key);
      if (!prepared) throw new Error(`Go PREPARE row is missing for ${String(modelCase.caseId)} / ${String(modelCase.entryId)} / ${String(modelCase.actionIndex)}.`);
      if (prepared.status !== 'PREPARED' && prepared.status !== 'PREPARED_UI_ONLY') {
        throw new Error(`Go PREPARE status ${String(prepared.status)} for ${key}: ${String(prepared.error ?? 'no accepted seed was prepared')}`);
      }

      const acceptedWorkspace = explorerBuilderWorkspaceSchema.parse(prepared.acceptedWorkspace);
      const builderState = explorerBuilderStateSchema.parse(prepared.builderState);
      expect(builderState.workspace).toEqual(acceptedWorkspace);
      const preparedIdentity = asJsonRecord(prepared.identity);
      if (!preparedIdentity) throw new Error(`Prepared row ${key} is missing identity.`);
      const project = preparedIdentity.project;
      const explorerId = preparedIdentity.explorerId;
      if (typeof project !== 'string' || typeof explorerId !== 'string') {
        throw new Error(`Prepared row ${key} lacks project/explorer identity.`);
      }
      const projectParts = project.split('/');
      if (projectParts.length !== 2 || !projectParts[0] || !projectParts[1]) {
        throw new Error(`Prepared project identity must be organization/project; got ${JSON.stringify(project)}.`);
      }

      const capabilitiesResponse = prepared.capabilities === undefined || prepared.capabilities === null
        ? null
        : constructionCapabilitiesResponseSchema.parse(prepared.capabilities);
      harness.builderState = builderState;
      harness.capabilities = capabilitiesResponse;
      harness.proposalStatus = 'READY';
      capture.request = undefined;
      capture.requestArgs = undefined;

      const action = asJsonRecord(modelCase.action);
      const uiAction = asJsonRecord(action?.uiAction);
      const entryId = typeof modelCase.entryId === 'string' ? modelCase.entryId : '';
      const adapter = BUILDER_CONTRACT_ADAPTERS[entryId];
      if (!uiAction) throw new Error(`Model action for ${key} has no uiAction.`);
      const actionValues = asJsonRecord(uiAction.values);
      if (!actionValues) throw new Error(`Model action for ${key} has no object uiAction.values.`);
      if (!adapter) throw new Error(`No callable UI adapter is registered for ${entryId}.`);
      if (uiAction.affordanceId !== entryId) throw new Error(`uiAction.affordanceId ${String(uiAction.affordanceId)} does not match ${entryId}.`);
      if (uiAction.adapterId !== adapter.adapterId || uiAction.runnerActionId !== adapter.runnerActionId) {
        throw new Error(`Model adapter binding for ${key} differs from the registered adapter.`);
      }
      const rawRequestStep = asJsonRecord(modelCase.requestStep) ?? asJsonRecord(action?.requestStep);
      const requestStep = rawRequestStep === undefined ? null : (() => {
        const { flowId, stepId, kind, method, endpoint, actionId, order } = rawRequestStep;
        if (typeof flowId !== 'string' || typeof stepId !== 'string' || typeof kind !== 'string' ||
            typeof method !== 'string' || typeof endpoint !== 'string' || typeof actionId !== 'string' ||
            typeof order !== 'number' || !Number.isInteger(order)) {
          throw new Error(`Model requestStep for ${key} does not match the normalized request-step ABI.`);
        }
        return { flowId, stepId, kind, method, endpoint, actionId, order };
      })();

      render(<BuilderWorkspace organization={projectParts[0]} project={projectParts[1]} explorerId={explorerId} />);
      const controlsBefore = controlStateSnapshot();
      await adapter.drive({
        uiAction: {
          affordanceId: entryId,
          runnerActionId: adapter.runnerActionId,
          values: actionValues,
        },
        requestStep,
      });
      const controlsAfter = controlStateSnapshot();
      const request = actualRequestForModelCase(modelCase);
      const targetRequestIndex = actualTargetRequestIndex(modelCase);
      const response = targetRequestIndex === undefined
        ? null
        : harness.responses.find((entry) => entry.requestIndex === targetRequestIndex);
      const observedActual = {
        acceptedWorkspace: prepared.acceptedWorkspace,
        preparedIdentity: prepared.identity,
        preparedSeed: prepared,
        builderState: prepared.builderState,
        capabilitiesResponse: prepared.capabilities ?? null,
        capabilityProvenance: prepared.capabilityProvenance ?? null,
        uiControl: changedControl(controlsBefore, controlsAfter),
        controlState: controlsAfter,
        request,
        response: response ? responseObservation(response) : null,
        requests: harness.requests.map(requestObservation),
        responses: harness.responses.map(responseObservation),
      };
      return {
        recordType: 'ui-observation',
        schemaVersion: 1,
        caseId: modelCase.caseId,
        entryId: modelCase.entryId,
        actionIndex: modelCase.actionIndex,
        requestMode: modelCase.requestMode,
        status: 'OBSERVED',
        model: modelCase,
        capabilityProvenance: prepared.capabilityProvenance ?? null,
        actual: observedActual,
      };
    };

    if (captureModelRows.length === 0) {
      it('reports model input setup failure instead of falling back to smoke', () => {
        expect(captureModelFile?.error ?? 'Model JSONL contains no case rows.').toBe('');
      });
    } else {
      it.each(captureModelRows)('captures $caseId / $entryId / $actionIndex through a registered real UI handler', async (modelCase) => {
        const key = keyForContractRow(modelCase) ?? `${String(modelCase.caseId)}\u0000${String(modelCase.entryId)}\u0000${String(modelCase.actionIndex)}`;
        let observation: JsonRecord;
        if (modelCase.requestMode === 'gap') {
          observation = modelGapObservation(modelCase);
        } else {
          try {
            const inputErrors = [captureModelFile?.error, preparedSeedFile?.error]
              .filter((error): error is string => Boolean(error));
            if (inputErrors.length > 0) throw new Error(inputErrors.join(' | '));
            observation = await captureOneModelCase(modelCase);
          } catch (error) {
            observation = setupFailureObservation(modelCase, error instanceof Error ? error.message : String(error));
          }
        }
        captureObservations.set(key, observation);
        const expectedStatus = modelCase.requestMode === 'gap' ? 'GAP' : 'OBSERVED';
        expect(observation.status, observation.error as string | undefined).toBe(expectedStatus);
      });
    }

    afterAll(() => {
      if (!captureOutputPath) throw new Error('Capture phase requires BUILDER_FULL_UI_UI_OBSERVATIONS.');
      const inventoryPath = process.env.BUILDER_CONTRACT_INVENTORY;
      const inventoryBytes = inventoryPath ? readFileSync(inventoryPath) : undefined;
      const adapterSources = adapterSourceManifest();
      const modelKeys = new Set(captureModelRows.map(keyForContractRow).filter((key): key is string => key !== undefined));
      const preparedKeys = new Set([...preparedSeedByKey.keys()]);
      const missingPreparedKeys = [...modelKeys].filter((key) => !preparedKeys.has(key)).sort();
      const extraPreparedKeys = [...preparedKeys].filter((key) => !modelKeys.has(key)).sort();
      const rows = captureModelRows.map((modelCase) => {
        const key = keyForContractRow(modelCase);
        return key ? captureObservations.get(key) ?? setupFailureObservation(modelCase, 'No test observation was emitted for this model row.')
          : setupFailureObservation(modelCase, 'Model row lacks exact triple key.');
      });
      const requestModeCount = (mode: string) => captureModelRows.filter((row) => row.requestMode === mode).length;
      const header = {
        recordType: 'header',
        schemaVersion: 1,
        phase: 'capture',
        sourceCommit: 'dc331783dde3f61e764bab3a421b66b28698c9d4',
        adapterCommit: process.env.BUILDER_CONTRACT_ADAPTER_COMMIT ?? null,
        inventorySha256: inventoryBytes ? createHash('sha256').update(inventoryBytes).digest('hex') : null,
        modelJsonlSha256: captureModelFile?.bytes ? createHash('sha256').update(captureModelFile.bytes).digest('hex') : null,
        preparedSeedsSha256: preparedSeedFile?.bytes ? createHash('sha256').update(preparedSeedFile.bytes).digest('hex') : null,
        adapterSourceSha256: hashNamedBytes(adapterSources),
        adapterSources: adapterSources.map(({ path, sha256 }) => ({ path, sha256 })),
        missingPreparedKeys,
        extraPreparedKeys,
        inputErrors: [captureModelFile?.error, preparedSeedFile?.error].filter(Boolean),
        totals: {
          caseRowCount: captureModelRows.length,
          observedCount: rows.filter((row) => row.status === 'OBSERVED').length,
          modelGapObservationCount: rows.filter((row) => row.status === 'GAP').length,
          setupFailureCount: rows.filter((row) => row.status === 'SETUP_FAILURE').length,
          emittedCount: requestModeCount('emitted'),
          blockedCount: requestModeCount('blocked'),
          localCount: requestModeCount('local'),
          gapCount: requestModeCount('gap'),
        },
      };
      mkdirSync(dirname(captureOutputPath), { recursive: true });
      writeFileSync(captureOutputPath, `${[header, ...rows].map((record) => JSON.stringify(record)).join('\n')}\n`);
    });
  });
} else describe('mounted BuilderWorkspace UI contract behaviors', () => {
  beforeEach(resetHarness);

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('opens the real Group editor and captures the exact serialized proposal request', async () => {
    const actionSequence = ['open saved Group step', 'select MEAN in Summary 1'];
    render(<BuilderWorkspace organization="HTAN_INT" project={project} explorerId={explorerId} />);
    fireEvent.click(await screen.findByTestId(`construction-history-step-${groupStepId}`));
    await screen.findByTestId('construction-reshape-editor');
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
    await screen.findByTestId(`construction-history-step-${groupStepId}`);
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
    fireEvent.click(screen.getByTestId(`construction-history-step-${groupStepId}`));
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
    fireEvent.click(await screen.findByTestId(`construction-history-step-${groupStepId}`));
    fireEvent.change(await screen.findByRole('combobox', { name: 'Summary 1' }), { target: { value: 'MEAN' } });
    await screen.findByTestId('construction-proposal-error');
    expect(capture.requestArgs?.snapshotToken).toBe('snapshot-ui-contract-capture');
    expect(screen.queryByTestId('construction-proposal-ready')).toBeNull();
    expect(screen.queryByTestId('construction-apply-proposal')).toBeNull();
    expect(harness.applyCommands).not.toHaveBeenCalled();
  });

});
