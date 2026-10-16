// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createLoomClient as createFullLoomClient, LoomRequestError } from '../../../api';

const createLoomClient = createFullLoomClient;
import { LoomProvider } from '../../../react';
import { semanticInventoryBrowseRequestSchema } from '../../../types';
import type {
  AggregateTransformationCapability,
  ColumnValueTransformationCapabilities,
  ConstructionChoice,
  ExplorerBuilderCatalog,
  ExplorerBuilderCandidate,
  FieldPresentation,
  FieldPresentationRequest,
  SemanticInventoryBrowseResponse,
  SemanticInventoryItem,
} from '../../../types';
import type { CatalogInitialSelection } from './CatalogSelectionOptions';
import type { PairedColumnSuggestion } from '../constructionWorkspace/PairedColumnSuggestions';
import {
  ConceptCatalog,
  type CatalogRelatedSourceAvailability,
  type CatalogRouteContext,
  type CatalogSourceProjectionAvailability,
} from './ConceptCatalog';

const choiceOption = (
  form: 'VALUE' | 'ALL' | 'OWNER_RECORDS',
  decision: 'DEFAULT' | 'REQUIRES_DECISION',
): ConstructionChoice['options'][number] => ({
  form,
  shape: form === 'VALUE' ? 'SCALAR' : 'LIST',
  decision,
  preservation: 'PRESERVING',
  rowEffect: 'PRESERVES_ROW_GRAIN',
  support: 'SUPPORTED',
  reason: `Loom proves the ${form} output form preserves the row grain.`,
});

const fieldChoice = (
  choiceId: string,
  candidateId: string,
  nodeId: string,
  resourceType: string,
  path: string,
): ConstructionChoice => ({
  choiceId,
  route: [],
  presentation: {
    summary: `${resourceType}.${path}`,
    facts: [{ label: 'Field', value: path }],
  },
  source: {
    kind: 'FIELD',
    candidateId,
    nodeId,
    resourceType,
    path,
    cardinality: 'optional_one',
  },
  options: [choiceOption('VALUE', 'DEFAULT')],
});

const unavailableTransformations: AggregateTransformationCapability = {
  temporalReduction: {
    available: false,
    reason: 'No advertised temporal choices are available for this candidate.',
    timestampFields: [],
    anchorFields: [],
  },
  unitNormalization: {
    available: false,
    reason: 'No approved unit preset is available for this candidate.',
    presets: [],
  },
};

const availableStringValueTransformations: ColumnValueTransformationCapabilities = {
  exactCategoryRecode: { available: true },
  codedValueRecoding: {
    available: false,
    reasonCode: 'CODED_VALUE_RECODE_UNAVAILABLE',
    reason: 'Coded value recoding is unavailable because this scalar transformation cannot preserve both Coding.system and Coding.code.',
  },
};

const semanticChoice = (
  choiceId: string,
  item: Pick<SemanticInventoryItem, 'conceptId' | 'bindingId' | 'resourceType' | 'sourcePath' | 'valueSelector' | 'system' | 'code'>,
  options: ConstructionChoice['options'] = [choiceOption('VALUE', 'DEFAULT')],
  ruleHint = 'CODED_VALUE_V1',
): ConstructionChoice => ({
  choiceId,
  route: [],
  presentation: {
    summary: `${item.system} · ${item.code}`,
    facts: [
      { label: 'Source path', value: item.sourcePath },
      { label: 'Value member', value: item.valueSelector },
    ],
  },
  source: {
    kind: 'SEMANTIC',
    conceptId: item.conceptId,
    bindingId: item.bindingId,
    candidateId: `candidate-${choiceId}`,
    nodeId: `${item.resourceType.toLowerCase()}-node`,
    resourceType: item.resourceType,
    sourcePath: item.sourcePath,
    fieldPath: `root.${item.sourcePath}`,
    valueSelector: item.valueSelector,
    logicalType: 'decimal',
    system: item.system,
    code: item.code,
    ruleHint,
    ruleVersion: '1',
    schemaVersion: 1,
    cardinality: 'optional_one',
  },
  options: [...options],
});

const item = (
  code: string,
  display: string,
  occurrences: number,
  resourceType = 'Patient',
  ruleHint = 'CODED_VALUE_V1',
): SemanticInventoryItem => {
  const value = {
  conceptId: `concept-${code}`,
  bindingId: `binding-${code}`,
    resourceType,
    sourcePath: 'extension.valueQuantity',
  system: 'http://loinc.org',
  code,
  codingVersion: '2.77',
  display,
  valueSelector: 'valueQuantity.value',
  valueType: 'decimal',
  owningScope: 'extension[]',
  occurrences,
  examples: ['10.2', '11.0'],
  examplesTruncated: true,
  observedUnits: ['g/dL'],
  observedUnitsTruncated: true,
  completeness: 'partial' as const,
    readiness: { status: 'READY' as const, code: 'READY', message: 'This semantic field is ready to add.' },
  };
  return { ...value, constructionChoice: semanticChoice(`choice-${code}`, value, undefined, ruleHint) };
};

const withSemanticIdentity = (
  entry: SemanticInventoryItem,
  conceptId: string,
  bindingId: string,
): SemanticInventoryItem => {
  const choice = entry.constructionChoice;
  if (!choice || choice.source.kind !== 'SEMANTIC') {
    throw new Error('A semantic construction choice is required for this fixture.');
  }
  return {
    ...entry,
    conceptId,
    bindingId,
    constructionChoice: {
      ...choice,
      source: { ...choice.source, conceptId, bindingId },
    },
  };
};

const page = (
  entries: ReadonlyArray<SemanticInventoryItem>,
  nextCursor?: string,
): SemanticInventoryBrowseResponse => ({
  contextToken: 'context-1',
  buildId: 'build-1',
  state: 'complete',
  sourceAvailability: 'verified',
  entries: [...entries],
  nextCursor,
});

const rootId: ExplorerBuilderCandidate = {
  candidateId: 'candidate-id',
  nodeId: 'patient-node',
  populated: true,
  fieldPath: 'id',
  label: 'id',
  logicalType: 'string',
  cardinality: 'optional_one',
  repeated: false,
  filterable: true,
  chartable: false,
  projectionModes: ['VALUE'],
  defaultProjectionMode: 'VALUE',
  aggregateOperations: [],
  transformations: unavailableTransformations,
  valueTransformations: availableStringValueTransformations,
  constructionChoice: fieldChoice('field-choice-id', 'candidate-id', 'patient-node', 'Patient', 'id'),
};

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot-a',
  generation: 'generation-a',
  routePolicy: {},
  nodes: [
    { nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 1 },
    { nodeId: 'observation-node', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 1 },
    { nodeId: 'document-reference-node', resourceType: 'DocumentReference', rowRootEligible: true, populated: true, documentCount: 1 },
  ],
  edges: [{ edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node', label: 'observations' }],
  candidates: [
    {
      ...rootId,
      candidateId: 'candidate-document-reference-id',
      nodeId: 'document-reference-node',
      constructionChoice: fieldChoice('field-choice-document-reference-id', 'candidate-document-reference-id', 'document-reference-node', 'DocumentReference', 'id'),
    },
    rootId,
    {
      ...rootId,
      candidateId: 'candidate-observation-id',
      nodeId: 'observation-node',
      fieldPath: 'identifier',
      label: 'Observation id',
      constructionChoice: fieldChoice('field-choice-observation-id', 'candidate-observation-id', 'observation-node', 'Observation', 'identifier'),
    },
  ],
};

const fieldPresentationCodeCandidate: ExplorerBuilderCandidate = {
  ...rootId,
  candidateId: 'candidate-condition-code',
  fieldPath: 'condition.code',
  label: 'Condition code',
  logicalType: 'code',
  constructionChoice: fieldChoice(
    'field-choice-condition-code',
    'candidate-condition-code',
    'patient-node',
    'Patient',
    'condition.code',
  ),
};

const fieldPresentationTextCandidate: ExplorerBuilderCandidate = {
  ...rootId,
  candidateId: 'candidate-clinical-note',
  fieldPath: 'clinicalNote',
  label: 'Clinical note',
  logicalType: 'string',
  constructionChoice: fieldChoice(
    'field-choice-clinical-note',
    'candidate-clinical-note',
    'patient-node',
    'Patient',
    'clinicalNote',
  ),
};

const fieldPresentationCatalog: ExplorerBuilderCatalog = {
  ...catalog,
  candidates: [fieldPresentationCodeCandidate, fieldPresentationTextCandidate],
};

const fieldPresentationContext = (
  draftVersion = 3,
  draftDigest = `draft-digest-${draftVersion}`,
): Omit<FieldPresentationRequest, 'candidateIds'> => ({
  expectedDraftVersion: draftVersion,
  expectedDraftDigest: draftDigest,
  sourceNodeId: 'patient-node',
  occurrenceId: 'base',
});

const fieldPresentationReplacement = (
  ruleHint = 'CODED_VALUE_V1',
): SemanticInventoryItem => {
  const base = item('tumor', 'Tumor', 5, 'Patient', ruleHint);
  const choice = base.constructionChoice;
  if (!choice || choice.source.kind !== 'SEMANTIC') {
    throw new Error('The field-presentation fixture requires a compiler-issued semantic choice.');
  }
  const identity = { conceptId: 'concept-tumor', bindingId: 'binding-tumor' };
  return {
    ...base,
    ...identity,
    sourcePath: 'condition.code',
    system: 'urn:condition-codes',
    code: 'tumor',
    codingVersion: '',
    display: 'Tumor',
    valueSelector: 'coding.code',
    valueType: 'code',
    constructionChoice: {
      ...choice,
      source: {
        ...choice.source,
        ...identity,
        nodeId: 'patient-node',
        resourceType: 'Patient',
        sourcePath: 'condition.code',
        fieldPath: 'condition.code',
        valueSelector: 'coding.code',
        system: 'urn:condition-codes',
        code: 'tumor',
        version: undefined,
        logicalType: 'code',
        ruleHint,
      },
    },
  };
};

const readyFieldPresentation = (
  request: FieldPresentationRequest,
  options: {
    readonly replacement?: SemanticInventoryItem;
    readonly stale?: boolean;
    readonly partial?: boolean;
    readonly missingReplacement?: boolean;
  readonly buildId?: string;
  } = {},
): FieldPresentation => {
  const replacement = options.replacement ?? fieldPresentationReplacement();
  const identity = {
    kind: 'EXACT_CONCEPT' as const,
    conceptId: replacement.conceptId,
    bindingId: replacement.bindingId,
  };
  const decisions: Extract<FieldPresentation, { readonly kind: 'READY' }>['decisions'] = [];
  for (const candidateId of request.candidateIds) {
    if (candidateId === fieldPresentationCodeCandidate.candidateId) {
      decisions.push({
        kind: 'GROUP_UNDER_SEMANTIC' as const,
        candidateId,
        coverageId: 'coverage-condition-code',
        replacementIds: [identity],
        access: 'SOURCE_FIELDS' as const,
      });
      continue;
    }
    if (options.partial) continue;
    decisions.push({
      kind: 'KEEP_RAW' as const,
      candidateId,
      reason: 'TEXT_ONLY' as const,
    });
  }
  return {
    kind: 'READY',
    context: {
      project: 'project-a',
      authorizationScopeDigest: 'scope-a',
      snapshotToken: 'snapshot-a',
      datasetGeneration: 'generation-a',
      inventoryBuildId: options.buildId ?? 'build-1',
      coverageBuildId: 'coverage-build-1',
      explorerId: 'explorer-a',
      outputId: 'patients',
      draftVersion: options.stale ? request.expectedDraftVersion - 1 : request.expectedDraftVersion,
      draftDigest: options.stale ? 'stale-draft-digest' : request.expectedDraftDigest,
      sourceNodeId: request.sourceNodeId,
      occurrenceId: request.occurrenceId,
      route: [],
      ...(request.stageId ? { stageId: request.stageId } : {}),
      presentationRevision: 'semantic-field-presentation/v1',
      candidateBatchDigest: 'candidate-batch-digest',
    },
    decisions,
    replacements: options.missingReplacement ? [] : [replacement],
  };
};

const fieldPresentationRequestFromInit = (init: RequestInit | undefined): FieldPresentationRequest => {
  const browseRequest = semanticInventoryBrowseRequestSchema.parse(JSON.parse(String(init?.body)));
  if (!('fieldPresentation' in browseRequest) || !browseRequest.fieldPresentation) {
    throw new Error('The semantic browse request did not include field-presentation context.');
  }
  return browseRequest.fieldPresentation;
};

const semanticBrowseResponse = (
  fieldPresentation?: FieldPresentation,
  entries: ReadonlyArray<SemanticInventoryItem> = [],
  nextCursor?: string,
  buildId = 'build-1',
): Response =>
  new Response(JSON.stringify({
    ...page(entries, nextCursor),
    buildId,
    ...(fieldPresentation ? { fieldPresentation } : {}),
  }), { status: 200, headers: { 'content-type': 'application/json' } });

const renderCatalog = (
  fetch: typeof globalThis.fetch,
  onAddSelected = vi.fn().mockResolvedValue(undefined),
  sourceProjectionAvailability?: CatalogSourceProjectionAvailability,
  source: {
    readonly resourceType?: string;
    readonly nodeId?: string;
    readonly routeContext?: CatalogRouteContext;
  } = {},
  catalogOverride: ExplorerBuilderCatalog = catalog,
  relatedSourceAvailability?: CatalogRelatedSourceAvailability,
  suppressUnavailableNotices = false,
  initialSelection?: CatalogInitialSelection,
) => {
    render(
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          resourceType={source.resourceType}
          sourceNodeId={source.nodeId}
          routeContext={source.routeContext}
          catalog={catalogOverride}
          sourceProjectionAvailability={sourceProjectionAvailability}
          relatedSourceAvailability={relatedSourceAvailability}
          suppressUnavailableNotices={suppressUnavailableNotices}
          initialSelection={initialSelection}
          onAddSelected={onAddSelected}
        />
      </LoomProvider>,
    );
  return onAddSelected;
};

const renderCatalogAtRoute = (
  fetch: typeof globalThis.fetch,
  routeContext: CatalogRouteContext,
  onAddSelected = vi.fn().mockResolvedValue(undefined),
  initialSelection?: CatalogInitialSelection,
) => {
  render(
    <LoomProvider client={createLoomClient({ fetch })}>
      <ConceptCatalog
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-a"
        outputId="patients"
        rowRoot="Patient"
        resourceType="Observation"
        routeContext={routeContext}
        layout="panel"
        catalog={catalog}
        initialSelection={initialSelection}
        onAddSelected={onAddSelected}
      />
    </LoomProvider>,
  );
  return onAddSelected;
};

describe('ConceptCatalog', () => {
  it('uses indexed catalog fields without requesting generated schema fields', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([])), { status: 200, headers: { 'content-type': 'application/json' } }),
    );
    render(
      <LoomProvider client={createFullLoomClient({ fetch })}>
        <ConceptCatalog project="project-a" explorerId="explorer-a" snapshotToken="snapshot-a"
          outputId="patients" rowRoot="Patient" sourceNodeId="patient-node" catalog={catalog} layout="panel" />
      </LoomProvider>,
    );

    await screen.findByRole('searchbox', { name: 'Search coded values by code or label' });
    fireEvent.click(within(screen.getByTestId('feature-catalog-raw-fields')).getByText('Fields'));
    expect(await screen.findByRole('button', { name: 'Select Patient.id' })).toBeEnabled();
    expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/schema-fields'))).toBe(false);
  });

  it('keeps failed submissions in the choice dialog and clears the duplicate aside', async () => {
    const slowConcept = item('ajcc-m', 'AJCC pathological M category', 1);
    const multiFormChoice = {
      ...slowConcept,
      constructionChoice: semanticChoice('ajcc-m-choice', slowConcept, [
        choiceOption('VALUE', 'DEFAULT'),
        choiceOption('ALL', 'REQUIRES_DECISION'),
      ]),
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([multiFormChoice])), { status: 200, headers: { 'content-type': 'application/json' } }),
    );
    const onAddSelected = vi.fn().mockRejectedValue(new Error('preview exceeded its execution deadline'));
    renderCatalog(fetch, onAddSelected);

    fireEvent.click(await screen.findByRole('button', { name: 'Select AJCC pathological M category' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    expect(screen.queryByRole('heading', { name: 'Selected features' })).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 selected feature' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('preview exceeded its execution deadline');
    expect(screen.getByRole('region', { name: 'Advanced route and form choices' })).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Add 1 selected feature' })).toBeEnabled();
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeEnabled();
    expect(onAddSelected).toHaveBeenCalledTimes(1);

    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('region', { name: 'Advanced route and form choices' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add 1 selected feature' })).toBeEnabled();
  });

  it('releases the picker when selection changes during Advanced route lookup', async () => {
    const first = item('first-route', 'First route concept', 1, 'Observation');
    const second = item('second-route', 'Second route concept', 1, 'Observation');
    let routeSignal: AbortSignal | null | undefined;
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input, init) => {
      if (String(input).endsWith('/construction-choices')) {
        return new Promise<Response>((_resolve, reject) => {
          routeSignal = init?.signal;
          routeSignal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
        });
      }
      return new Response(JSON.stringify(page([first, second])), { status: 200, headers: { 'content-type': 'application/json' } });
    });
    renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('button', { name: 'Select First route concept' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    await waitFor(() => expect(routeSignal).toBeDefined());
    expect(screen.getByText('Loading route and result form choices…')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Select Second route concept' }));
    await waitFor(() => expect(routeSignal?.aborted).toBe(true));
    expect(screen.queryByRole('region', { name: 'Advanced route and form choices' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add 2 selected features' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Advanced options' })).toBeEnabled();
  });

  it('keeps a pending inventory read when only source-availability presentation changes', async () => {
    let resolveFetch: ((response: Response) => void) | undefined;
    const fetch = vi.fn<typeof globalThis.fetch>(() => new Promise<Response>((resolve) => {
      resolveFetch = resolve;
    }));
    const client = createLoomClient({ fetch });
    const renderWithAvailability = (available: boolean, reason: string) => (
      <LoomProvider client={client}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          catalog={catalog}
          sourceProjectionAvailability={{ available, reason }}
        />
      </LoomProvider>
    );
    const view = render(renderWithAvailability(true, 'Ready.'));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));

    view.rerender(renderWithAvailability(false, 'The stage uses a related source.'));
    expect(fetch).toHaveBeenCalledTimes(1);
    const request = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as {
      snapshotToken?: string;
      rowRoot?: string;
    };
    expect(request).toMatchObject({
      snapshotToken: 'snapshot-a',
      rowRoot: 'Patient',
    });

    resolveFetch?.(new Response(JSON.stringify(page([
      item('availability-scope', 'Availability scope result', 2),
    ])), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    }));
    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Availability scope result' })).not.toBeInTheDocument();
    view.rerender(renderWithAvailability(true, 'Ready again.'));
    expect(await screen.findByRole('button', { name: 'Select Availability scope result' })).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('clears prior selection on actual scope change and ignores the late prior inventory response', async () => {
    const pending: Array<{ resolve: (response: Response) => void; signal: AbortSignal | null | undefined }> = [];
    const fetch = vi.fn<typeof globalThis.fetch>((_input, init) => new Promise<Response>((resolve) => {
      pending.push({ resolve, signal: init?.signal });
    }));
    const client = createLoomClient({ fetch });
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    const renderAtScope = (snapshotToken: string, rowRoot: string) => (
      <LoomProvider client={client}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken={snapshotToken}
          outputId="patients"
          rowRoot={rowRoot}
          catalog={catalog}
          onAddSelected={onAddSelected}
        />
      </LoomProvider>
    );
    const view = render(renderAtScope('snapshot-a', 'Patient'));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));

    const patientId = await screen.findByRole('button', { name: 'Select Patient.id' });
    fireEvent.click(patientId);
    expect(patientId).toHaveAttribute('aria-pressed', 'true');
    const patientIdCard = patientId.closest('article');
    expect(patientIdCard).not.toBeNull();
    if (!patientIdCard) throw new Error('The selectable field card is missing.');
    fireEvent.focus(within(patientIdCard).getByRole('button', { name: 'Details for Patient ID' }));
    expect(patientId).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Add 1 selected feature' })).toBeEnabled();
    view.rerender(renderAtScope('snapshot-b', 'Observation'));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(pending[0]?.signal?.aborted).toBe(true));

    const firstRequest = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as {
      snapshotToken?: string;
      rowRoot?: string;
    };
    const secondRequest = JSON.parse(String(fetch.mock.calls[1]?.[1]?.body)) as {
      snapshotToken?: string;
      rowRoot?: string;
    };
    expect(firstRequest).toMatchObject({ snapshotToken: 'snapshot-a', rowRoot: 'Patient' });
    expect(secondRequest).toMatchObject({ snapshotToken: 'snapshot-b', rowRoot: 'Observation' });
    expect(screen.queryByRole('button', { name: 'Add selected features' })).not.toBeInTheDocument();

    pending[1]?.resolve(new Response(JSON.stringify(page([
      item('new-scope', 'New scope result', 3),
    ])), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    }));
    expect(await screen.findByRole('button', { name: 'Select New scope result' })).toBeInTheDocument();

    pending[0]?.resolve(new Response(JSON.stringify(page([
      item('old-scope', 'Old scope result', 1),
    ])), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Select Old scope result' })).not.toBeInTheDocument());
    expect(screen.getByRole('button', { name: 'Select New scope result' })).toBeInTheDocument();
  });

  it('aborts inventory reads when their scope owner changes', async () => {
    const pending: Array<{
      readonly url: string;
      readonly resolve: (response: Response) => void;
      readonly signal: AbortSignal | null | undefined;
    }> = [];
    const fetch = vi.fn<typeof globalThis.fetch>((input, init) => new Promise<Response>((resolve) => {
      pending.push({ url: String(input), resolve, signal: init?.signal });
    }));
    const client = createFullLoomClient({ fetch });
    const renderAtScope = (snapshotToken: string) => (
      <LoomProvider client={client}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken={snapshotToken}
          outputId="patients"
          rowRoot="Patient"
          resourceType="Patient"
          sourceNodeId="patient-node"
          catalog={catalog}
        />
      </LoomProvider>
    );
    const view = render(renderAtScope('snapshot-a'));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    expect(pending[0]?.url.endsWith('/semantic-inventory')).toBe(true);

    view.rerender(renderAtScope('snapshot-b'));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    await waitFor(() => {
      expect(pending[0]?.signal?.aborted).toBe(true);
    });
    expect(pending[1]?.signal?.aborted).toBe(false);

    pending[1]?.resolve(new Response(JSON.stringify(page([
      item('current-scope', 'Current scope result', 3),
    ])), { status: 200, headers: { 'content-type': 'application/json' } }));
    expect(await screen.findByRole('button', { name: 'Select Current scope result' })).toBeInTheDocument();

    pending[0]?.resolve(new Response(JSON.stringify(page([
      item('old-scope', 'Old scope result', 1),
    ])), { status: 200, headers: { 'content-type': 'application/json' } }));
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: 'Select Old scope result' })).not.toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: 'Select Current scope result' })).toBeInTheDocument();
  });

  it('opens the existing route and result-form dialog for a ready-to-add paired concept without adding it', async () => {
    const daysToCollection = item('days_to_collection', 'Days to collection', 202195, 'Specimen');
    const resolvedChoice = semanticChoice('days-to-collection-route', daysToCollection, [
      choiceOption('VALUE', 'REQUIRES_DECISION'),
      choiceOption('ALL', 'REQUIRES_DECISION'),
    ]);
    const pairedSuggestion: PairedColumnSuggestion = {
      requestId: 'paired-selection-1',
      snapshotToken: 'snapshot-a',
      outputId: 'specimens',
      contextToken: 'context-1',
      buildId: 'build-1',
      item: daysToCollection,
      choices: {
        snapshotToken: 'snapshot-a',
        outputId: 'specimens',
        complete: true,
        truncated: false,
        choices: [resolvedChoice],
      },
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([daysToCollection])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    render(
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="specimens"
          rowRoot="Specimen"
          catalog={catalog}
          pairedColumnSuggestion={pairedSuggestion}
          onAddSelected={onAddSelected}
        />
      </LoomProvider>,
    );

    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    expect(within(dialog).getByRole('heading', { name: /Days to collection/ })).toBeInTheDocument();
    expect(within(dialog).getByRole('radio', { name: 'Days to collection: Use the matching value' })).toBeInTheDocument();
    expect(within(dialog).getByRole('radio', { name: 'Days to collection: Keep all matching values' })).toBeInTheDocument();
    expect(onAddSelected).not.toHaveBeenCalled();
  });

  it('searches root fields and concepts, shows available forms, and submits only choice IDs and forms', async () => {
    const hemoglobin = item('4548-4', 'Hemoglobin A1c', 91);
    const offRoot = item('718-7', 'Off-root concept', 64, 'Observation');
    const multiOptionChoice = semanticChoice(
      'choice-4548-4',
      hemoglobin,
      [
        choiceOption('VALUE', 'DEFAULT'),
        choiceOption('ALL', 'REQUIRES_DECISION'),
        choiceOption('OWNER_RECORDS', 'REQUIRES_DECISION'),
      ],
    );
    const semantic = { ...hemoglobin, constructionChoice: multiOptionChoice };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([semantic, offRoot])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const onAddSelected = renderCatalog(fetch);

    expect(await screen.findByRole('searchbox', { name: 'Search coded values by code or label' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Select Patient.id' })).toBeEnabled();
    expect(await screen.findByRole('button', { name: 'Select Hemoglobin A1c' })).toBeEnabled();
    expect(
      screen.getByRole('region', { name: 'Coded values' })
        .compareDocumentPosition(screen.getByRole('heading', { name: 'Fields on Patient' })) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Coded values for this table' })).not.toBeInTheDocument();
    expect(screen.queryByText('How coded columns work')).not.toBeInTheDocument();
    expect(screen.queryByText('Availability and coverage notes')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Observation id' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Select Off-root concept' })).toBeEnabled();

    const request = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as { resourceType?: string; rowRoot?: string; outputId?: string };
    expect(request).toMatchObject({ rowRoot: 'Patient', outputId: 'patients' });
    expect(request).not.toHaveProperty('resourceType');

    fireEvent.click(screen.getByRole('button', { name: 'Select Patient.id' }));
    fireEvent.click(screen.getByRole('button', { name: 'Select Hemoglobin A1c' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));

    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    fireEvent.click(within(dialog).getAllByText('Technical path details')[1]!);
    expect(within(dialog).getByText('valueQuantity.value')).toBeInTheDocument();
    const defaultForm = screen.getByRole('radio', { name: 'Hemoglobin A1c: Use the matching value' });
    const listForm = screen.getByRole('radio', { name: 'Hemoglobin A1c: Keep all matching values' });
    expect(screen.getByRole('radio', { name: 'Hemoglobin A1c: Keep each matching record' })).toBeEnabled();
    expect(defaultForm).toHaveProperty('checked', true);
    expect(listForm).toHaveProperty('checked', false);
    fireEvent.click(listForm);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 2 selected features' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([
      {
        constructionChoice: { choiceId: 'field-choice-id', form: 'VALUE' },
        title: 'Patient ID',
      },
      {
        constructionChoice: { choiceId: 'choice-4548-4', form: 'ALL' },
        title: 'Hemoglobin A1c',
      },
    ]));
    expect(onAddSelected).toHaveBeenCalledTimes(1);
  });

  it('uses plain form names and keeps technical details outside the radio label', async () => {
    const entry = item('all-forms', 'All forms', 1);
    const forms: ReadonlyArray<ConstructionChoice['options'][number]['form']> = [
      'VALUE', 'FIRST', 'ALL', 'DISTINCT', 'COUNT', 'PRESENCE', 'OWNER_RECORDS',
    ];
    const options = forms.map((form, index): ConstructionChoice['options'][number] => ({
      form,
      shape: form === 'ALL' || form === 'DISTINCT' || form === 'OWNER_RECORDS' ? 'LIST' : 'SCALAR',
      decision: index === 0 ? 'DEFAULT' : 'REQUIRES_DECISION',
      preservation: form === 'COUNT' || form === 'PRESENCE' ? 'REDUCING' : 'PRESERVING',
      rowEffect: 'PRESERVES_ROW_GRAIN',
      support: 'SUPPORTED',
      reason: `Loom supports the ${form} form for this field.`,
    }));
    const allForms = {
      ...entry,
      constructionChoice: semanticChoice('choice-all-forms', entry, options),
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([allForms])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const onAddSelected = renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('button', { name: 'Select All forms' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    const countRadio = within(dialog).getByRole('radio', { name: 'All forms: Count matching records' });
    fireEvent.click(countRadio);
    const countCard = countRadio.closest('.rounded-md') as HTMLElement | null;
    expect(countCard).not.toBeNull();
    if (!countCard) throw new Error('The count form card is missing.');
    expect(within(countCard).getByText(/What to expect: Loom supports the COUNT form/)).toBeInTheDocument();
    fireEvent.click(within(countCard).getByText('Technical form details'));
    expect(countRadio).toHaveProperty('checked', true);
    expect(within(countCard).getByText('COUNT')).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 selected feature' }));
    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'choice-all-forms', form: 'COUNT' },
      title: 'All forms',
    }]));
  });

  it('omits semantic entries without a supported compiler-proven form', async () => {
    const unsupported = {
      ...item('case-id', 'Case identifier', 12),
      constructionChoice: undefined,
      readiness: {
        status: 'UNSUPPORTED' as const,
        code: 'VALUE_PROJECTION_UNSUPPORTED',
        message: 'The observed value does not contain the scalar selected by the current projection.',
      },
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([unsupported])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    renderCatalog(fetch);

    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Case identifier' })).not.toBeInTheDocument();
  });

  it('keeps only table-applicable semantic identifiers and sends the current output scope', async () => {
    const medicationIdentifier = item('medication-id', 'Medication identifier', 8, 'Medication');
    const researchIdentifier = item('study-id', 'ResearchStudy identifier', 6, 'ResearchStudy');
    const unavailableSubstanceIdentifier = {
      ...item('substance-id', 'Substance identifier', 4, 'Substance'),
      constructionChoice: undefined,
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([
        medicationIdentifier,
        researchIdentifier,
        unavailableSubstanceIdentifier,
      ])), { status: 200, headers: { 'content-type': 'application/json' } }),
    );

    renderCatalog(fetch);

    expect(await screen.findByRole('button', { name: 'Select Medication identifier' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Select ResearchStudy identifier' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Select Substance identifier' })).not.toBeInTheDocument();
    const request = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as { outputId?: string };
    expect(request.outputId).toBe('patients');
  });

  it('shows compiler-proven coded concepts and paired values while excluding other semantic families', async () => {
    const identifier = withSemanticIdentity(
      item('patient-identifier', 'Patient identifier', 8, 'Patient', 'IDENTIFIER_SYSTEM_VALUE'),
      'shared-concept',
      'identifier-binding',
    );
    const extension = item(
      'extension-url',
      'http://hl7.org/fhir/us/core/StructureDefinition/us-core-race',
      6,
      'Observation',
      'EXTENSION_URL_VALUE',
    );
    const codedWithoutChoice = {
      ...item('unproven-code', 'Unproven code', 2),
      constructionChoice: undefined,
    };
    const mismatchedChoiceItem = item('mismatched-code', 'Mismatched code', 1);
    const mismatchedChoice = {
      ...mismatchedChoiceItem,
      constructionChoice: {
        ...mismatchedChoiceItem.constructionChoice!,
        source: {
          ...mismatchedChoiceItem.constructionChoice!.source,
          bindingId: 'another-binding',
        },
      },
    };
    const codedValue = withSemanticIdentity(
      item('method-code', 'Diagnostic Method', 4, 'Observation'),
      'shared-concept',
      'coded-value-binding',
    );
    const standaloneCodeBase = item('condition-code', 'Condition Alpha', 3, 'Condition', 'CODED_CONCEPT_V1');
    const standaloneChoice = standaloneCodeBase.constructionChoice;
    if (!standaloneChoice || standaloneChoice.source.kind !== 'SEMANTIC') {
      throw new Error('The standalone coded-field fixture requires a compiler-issued semantic choice.');
    }
    const standaloneConcept: SemanticInventoryItem = {
      ...standaloneCodeBase,
      conceptId: 'shared-concept',
      bindingId: 'condition-code-binding',
      sourcePath: 'code',
      valueSelector: 'coding.code',
      system: 'urn:condition-codes',
      code: 'condition-alpha',
      display: 'Condition Alpha',
      constructionChoice: {
        ...standaloneChoice,
        route: [{
          edgeId: 'patient-condition',
          fromNodeId: 'patient-node',
          toNodeId: 'condition-node',
          fromResourceType: 'Patient',
          toResourceType: 'Condition',
          relationship: 'subject_Patient',
          storageDirection: 'INBOUND',
          matchMode: 'OPTIONAL',
        }],
        source: {
          ...standaloneChoice.source,
          conceptId: 'shared-concept',
          bindingId: 'condition-code-binding',
          nodeId: 'condition-node',
          sourcePath: 'code',
          fieldPath: 'code',
          valueSelector: 'coding.code',
          logicalType: 'code',
        },
      },
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input, init) => {
      if (String(input).endsWith('/construction-choices')) {
        return new Response(JSON.stringify({
          snapshotToken: 'snapshot-a',
          outputId: 'patients',
          complete: true,
          truncated: false,
          choices: [codedValue.constructionChoice],
        }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      const request = JSON.parse(String(init?.body)) as { readonly cursor?: string };
      const response = request.cursor === 'coded-page-2'
        ? page([codedValue, standaloneConcept])
        : page([identifier, extension, codedWithoutChoice, mismatchedChoice], 'coded-page-2');
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });

    const client = createLoomClient({ fetch });
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    let renderedView: 'coded' | 'fields' = 'coded';
    let viewRevision = 0;
    const renderView = (view: 'coded' | 'fields') => {
      if (view !== renderedView) viewRevision += 1;
      renderedView = view;
      return <LoomProvider client={client}>
      <ConceptCatalog
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-a"
        outputId="patients"
        rowRoot="Patient"
        catalog={catalog}
        view={view}
        viewRevision={viewRevision}
        onAddSelected={onAddSelected}
      />
      </LoomProvider>;
    };
    const { rerender } = render(renderView('coded'));

    expect(await screen.findByText('No coded values on this page. Use Next to continue through the catalog.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Next' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Select Patient identifier' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /us-core-race/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Unproven code' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Mismatched code' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('feature-catalog-raw-fields')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByRole('button', { name: 'Select Diagnostic Method' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Select Condition Alpha' })).toBeEnabled();
    expect(screen.getByRole('heading', { name: 'Coded values from Condition' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Coded values from Observation' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Patient identifier' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /us-core-race/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Unproven code' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Mismatched code' })).not.toBeInTheDocument();
    expect(JSON.parse(String(fetch.mock.calls[1]?.[1]?.body))).toMatchObject({ cursor: 'coded-page-2' });
    fireEvent.click(screen.getByRole('button', { name: 'Select Diagnostic Method' }));
    expect(screen.getByText('1 feature selected')).toBeInTheDocument();
    const requestCountBeforeFields = fetch.mock.calls.length;
    rerender(renderView('fields'));
    expect(await screen.findByTestId('feature-catalog-raw-fields')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Available dataset fields' })).not.toBeInTheDocument();
    expect(screen.getByText(/^\d+ available$/)).toBeInTheDocument();
    expect(screen.getByText('1 feature selected')).toBeInTheDocument();
    const selectedItems = screen.getByRole('list', { name: 'Selected fields and coded values' });
    expect(within(selectedItems).getByText('Coded value')).toBeInTheDocument();
    expect(within(selectedItems).getByText('Diagnostic Method')).toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search dataset fields by name or type' }), {
      target: { value: 'id' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    const fieldCheckbox = await screen.findByRole('button', { name: 'Select Patient.id' });
    expect(fieldCheckbox).toBeEnabled();
    fireEvent.click(fieldCheckbox);
    expect(screen.getByText('2 features selected')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add 2 selected features' })).toBeEnabled();
    expect(fetch).toHaveBeenCalledTimes(requestCountBeforeFields);
    rerender(renderView('coded'));
    await waitFor(() => expect((screen.getByRole('searchbox', {
      name: 'Search coded values by code or label',
    }) as HTMLInputElement).value).toBe(''));
    const preservedItems = screen.getByRole('list', { name: 'Selected fields and coded values' });
    expect(within(preservedItems).getByText('Diagnostic Method')).toBeInTheDocument();
    expect(within(preservedItems).getByText('Patient ID')).toBeInTheDocument();
    rerender(renderView('fields'));
    await waitFor(() => expect((screen.getByRole('searchbox', {
      name: 'Search dataset fields by name or type',
    }) as HTMLInputElement).value).toBe(''));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    const advanced = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    fireEvent.click(await within(advanced).findByRole('button', { name: 'Add 2 selected features' }));
    await waitFor(() => expect(onAddSelected).toHaveBeenCalledTimes(1));
    const routeCall = fetch.mock.calls.find(([input]) => String(input).endsWith('/construction-choices'));
    if (!routeCall) throw new Error('The selected non-root coded value was not resolved against the table.');
    expect(JSON.parse(String(routeCall[1]?.body))).toMatchObject({
      source: {
        kind: 'SEMANTIC',
        contextToken: 'context-1',
        buildId: 'build-1',
        conceptId: 'shared-concept',
        bindingId: 'coded-value-binding',
      },
    });
  });

  it('browses coded system/version families and routes them with the signed family choice', async () => {
    const base = item('', 'Compound name', 18, 'Observation', 'CODED_CATEGORY_V1');
    const baseChoice = base.constructionChoice;
    if (!baseChoice || baseChoice.source.kind !== 'SEMANTIC') {
      throw new Error('The category fixture requires a compiler-issued semantic choice.');
    }
    const route = {
      edgeId: 'patient-observation',
      fromNodeId: 'patient-node',
      toNodeId: 'observation-node',
      fromResourceType: 'Patient',
      toResourceType: 'Observation',
      relationship: 'observations',
      storageDirection: 'OUTBOUND',
      matchMode: 'OPTIONAL',
    } as const;
    const familyChoice: ConstructionChoice = {
      ...baseChoice,
      choiceId: 'signed-compound-family-choice',
      route: [route],
      options: [
        choiceOption('VALUE', 'REQUIRES_DECISION'),
        choiceOption('ALL', 'REQUIRES_DECISION'),
      ],
      source: {
        ...baseChoice.source,
        sourceKind: 'file',
        conceptId: 'family-compound-name',
        bindingId: 'binding-compound-name',
        nodeId: 'observation-node',
        resourceType: 'Observation',
        sourcePath: 'code',
        fieldPath: 'code',
        system: 'urn:compound-codes',
        version: 'v1',
        code: undefined,
        valueSelector: 'coding[].display',
        logicalType: 'string',
        ruleHint: 'CODED_CATEGORY_V1',
      },
    };
    const familySemanticSource = familyChoice.source;
    if (familySemanticSource.kind !== 'SEMANTIC') {
      throw new Error('The category fixture requires a signed semantic choice source.');
    }
    const family: SemanticInventoryItem = {
      ...base,
      conceptId: 'family-compound-name',
      familyId: 'family-compound-name',
      selectionMode: 'SYSTEM_VERSION_CATEGORY',
      bindingId: 'binding-compound-name',
      resourceType: 'Observation',
      sourcePath: 'code',
      system: 'urn:compound-codes',
      code: '',
      codingVersion: 'v1',
      display: 'Compound name',
      valueSelector: 'coding[].display',
      valueType: 'string',
      owningScope: 'Observation',
      constructionChoice: familyChoice,
    };
    const familyVersion = (
      version: string,
      bindingId: string,
      choiceId: string,
    ): SemanticInventoryItem => ({
      ...family,
      bindingId,
      codingVersion: version,
      constructionChoice: {
        ...familyChoice,
        choiceId,
        source: {
          ...familySemanticSource,
          bindingId,
          version: version || undefined,
        },
      },
    });
    const familyV2 = familyVersion('v2', 'binding-compound-name-v2', 'signed-compound-family-choice-v2');
    const familyUnversioned = familyVersion('', 'binding-compound-name-unversioned', 'signed-compound-family-choice-unversioned');
    const familyMethod: SemanticInventoryItem = {
      ...family,
      conceptId: 'family-method-category',
      familyId: 'family-method-category',
      bindingId: 'binding-method-category',
      sourcePath: 'method',
      display: 'Method category',
      constructionChoice: {
        ...familyChoice,
        choiceId: 'signed-method-category-choice',
        source: {
          ...familySemanticSource,
          conceptId: 'family-method-category',
          bindingId: 'binding-method-category',
          sourcePath: 'method',
          fieldPath: 'method',
        },
      },
    };
    const resolvedChoice: ConstructionChoice = {
      ...familyChoice,
      choiceId: 'route-compound-family-choice',
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async input => {
      if (String(input).endsWith('/construction-choices')) {
        return new Response(JSON.stringify({
          snapshotToken: 'snapshot-a',
          outputId: 'patients',
          complete: true,
          truncated: false,
          choices: [resolvedChoice],
        }), { status: 200, headers: { 'content-type': 'application/json' } });
      }
      return new Response(JSON.stringify(page([family, familyV2, familyUnversioned, familyMethod])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    const client = createLoomClient({ fetch });
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    render(
      <LoomProvider client={client}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          catalog={catalog}
          view="coded"
          semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
          onAddSelected={onAddSelected}
        />
      </LoomProvider>,
    );

    const familyLabel = 'Compound name · category · Observation Code · system urn:compound-codes · version v1';
    expect(await screen.findByRole('button', { name: `Select ${familyLabel}` })).toBeEnabled();
    expect(screen.getByRole('button', {
      name: 'Select Compound name · category · Observation Code · system urn:compound-codes · version v2',
    })).toBeEnabled();
    expect(screen.getByRole('button', {
      name: 'Select Compound name · category · Observation Code · system urn:compound-codes · version unversioned',
    })).toBeEnabled();
    expect(screen.getByRole('button', {
      name: 'Select Method category · category · Observation Method · system urn:compound-codes · version v1',
    })).toBeEnabled();
    expect(screen.getAllByRole('heading', { name: 'Coded values from Observation' })).toHaveLength(1);
    const familyCard = screen.getByRole('button', { name: `Select ${familyLabel}` }).closest('article');
    if (!familyCard) throw new Error('The family selection control must be inside its visible card.');
    expect(within(familyCard).getByText('Compound name')).toBeInTheDocument();
    const inventoryRequest = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body));
    expect(inventoryRequest).toMatchObject({
      rowRoot: 'Patient',
      outputId: 'patients',
      selectionMode: 'SYSTEM_VERSION_CATEGORY',
    });

    fireEvent.click(screen.getByRole('button', { name: `Select ${familyLabel}` }));
    const selectedList = screen.getByRole('list', { name: 'Selected fields and coded values' });
    const selectedRow = within(selectedList).getByRole('listitem', { name: `Selected ${familyLabel}` });
    expect(selectedRow).toHaveTextContent('Compound name');
    expect(selectedRow).toHaveAttribute('data-semantic-identity');
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    expect(within(dialog).getByRole('heading', { name: `Observation · ${familyLabel}` })).toBeInTheDocument();
    expect(within(dialog).getByRole('radio', { name: `${familyLabel}: Use the matching value` })).toBeInTheDocument();

    const routeRequest = fetch.mock.calls.find(([input]) => String(input).endsWith('/construction-choices'));
    expect(routeRequest).toBeDefined();
    expect(JSON.parse(String(routeRequest?.[1]?.body))).toMatchObject({
      source: {
        kind: 'SEMANTIC',
        conceptId: 'family-compound-name',
        bindingId: 'binding-compound-name',
        selectionMode: 'SYSTEM_VERSION_CATEGORY',
        choiceId: 'signed-compound-family-choice',
      },
    });
    fireEvent.click(within(dialog).getByRole('radio', { name: `${familyLabel}: Use the matching value` }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 selected feature' }));
    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([expect.objectContaining({
      title: 'Compound name',
    })]));
  });

  it('keeps exact concept meaning in accessible names when system and version are shared', async () => {
    const height = item('height', 'Height', 3, 'Observation');
    const diagnosticMethod = item('diagnostic-method', 'Diagnostic Method', 2, 'Observation');
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([height, diagnosticMethod])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    render(
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          catalog={catalog}
          view="coded"
          semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
          onAddSelected={vi.fn().mockResolvedValue(undefined)}
        />
      </LoomProvider>,
    );

    const heightSelection = 'Height · Observation Extension Value Quantity · system http://loinc.org · version 2.77 · code height';
    const methodSelection = 'Diagnostic Method · Observation Extension Value Quantity · system http://loinc.org · version 2.77 · code diagnostic-method';
    const heightButton = await screen.findByRole('button', { name: `Select ${heightSelection}` });
    const methodButton = screen.getByRole('button', { name: `Select ${methodSelection}` });
    expect(heightButton).toBeEnabled();
    expect(methodButton).toBeEnabled();
    const heightCard = heightButton.closest('article');
    const methodCard = methodButton.closest('article');
    if (!heightCard || !methodCard) throw new Error('Each exact semantic selection control must have a card.');
    expect(within(heightCard).getByText('Height')).toBeInTheDocument();
    expect(within(methodCard).getByText('Diagnostic Method')).toBeInTheDocument();
  });

  it('groups a fully covered raw field under its semantic card and keeps the exact FIELD choice reachable', async () => {
    const replacement = fieldPresentationReplacement();
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      const request = fieldPresentationRequestFromInit(init);
      return semanticBrowseResponse(readyFieldPresentation(request, { replacement }), [replacement]);
    });
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    render(
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          sourceNodeId="patient-node"
          catalog={fieldPresentationCatalog}
          view="coded"
          semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
          presentationContext={fieldPresentationContext()}
          onAddSelected={onAddSelected}
        />
      </LoomProvider>,
    );

    const replacementSelectionLabel = 'Tumor · Patient Condition Code · system urn:condition-codes · version unversioned · code tumor';
    expect(await screen.findByRole('button', { name: `Select ${replacementSelectionLabel}` })).toBeEnabled();
    expect(screen.getByText('Tumor')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Source fields'));
    const rawField = await screen.findByRole('button', { name: 'Select Patient.condition.code' });
    expect(screen.getAllByRole('button', { name: 'Select Patient.condition.code' })).toHaveLength(1);
    expect(rawField).toBeEnabled();
    fireEvent.click(rawField);

    const selectedList = screen.getByRole('list', { name: 'Selected fields and coded values' });
    const selectedRow = within(selectedList).getByRole('listitem', { name: 'Selected Condition code' });
    expect(selectedRow).toHaveAttribute('data-semantic-identity', JSON.stringify(['FIELD', 'candidate-condition-code']));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));
    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([expect.objectContaining({
      constructionChoice: { choiceId: 'field-choice-condition-code', form: 'VALUE' },
      title: 'Condition code',
    })]));
  });

  it('searches a raw path, follows Included in across tabs, and focuses the exact semantic identity', async () => {
    const replacement = fieldPresentationReplacement();
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      const browseRequest = semanticInventoryBrowseRequestSchema.parse(JSON.parse(String(init?.body)));
      if ('fieldPresentation' in browseRequest && browseRequest.fieldPresentation) {
        return semanticBrowseResponse(readyFieldPresentation(browseRequest.fieldPresentation, { replacement }));
      }
      return semanticBrowseResponse();
    });
    const client = createLoomClient({ fetch });
    const Picker = () => {
      const [view, setView] = React.useState<'coded' | 'fields'>('fields');
      const [viewRevision, setViewRevision] = React.useState(0);
      const switchView = (nextView: 'coded') => {
        setView(nextView);
        setViewRevision((revision) => revision + 1);
      };
      return (
        <LoomProvider client={client}>
          <ConceptCatalog
            project="project-a"
            explorerId="explorer-a"
            snapshotToken="snapshot-a"
            outputId="patients"
            rowRoot="Patient"
            sourceNodeId="patient-node"
            catalog={fieldPresentationCatalog}
            view={view}
            viewRevision={viewRevision}
            semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
            presentationContext={fieldPresentationContext()}
            onSwitchView={switchView}
            onAddSelected={vi.fn().mockResolvedValue(undefined)}
          />
        </LoomProvider>
      );
    };
    render(<Picker />);

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: 'Select Patient.condition.code' })).not.toBeInTheDocument();
    });
    expect(screen.queryByRole('button', { name: /Included in/ })).not.toBeInTheDocument();

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search dataset fields by name or type' }), {
      target: { value: 'condition.code' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    const includedInLink = await screen.findByRole('button', {
      name: 'Included in Tumor · Patient Condition Code · system urn:condition-codes · version unversioned · code tumor',
    });
    expect(includedInLink).toHaveAttribute('data-semantic-identity', JSON.stringify([
      'EXACT_CONCEPT', 'concept-tumor', 'binding-tumor',
    ]));
    fireEvent.click(includedInLink);

    const replacementCardButton = await screen.findByRole('button', {
      name: 'Select Tumor · Patient Condition Code · system urn:condition-codes · version unversioned · code tumor',
    });
    const replacementCard = replacementCardButton.closest('article');
    expect(replacementCard).toHaveAttribute('data-semantic-identity', JSON.stringify([
      'EXACT_CONCEPT', 'concept-tumor', 'binding-tumor',
    ]));
    await waitFor(() => expect(document.activeElement).toBe(replacementCard));

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'no-match' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    expect(screen.queryByRole('button', {
      name: 'Select Tumor · Patient Condition Code · system urn:condition-codes · version unversioned · code tumor',
    })).not.toBeInTheDocument();
  });

  it('keeps an existing raw field visible and selected during its initial edit flow', async () => {
    const replacement = fieldPresentationReplacement();
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      const request = fieldPresentationRequestFromInit(init);
      return semanticBrowseResponse(readyFieldPresentation(request, { replacement }));
    });
    render(
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          sourceNodeId="patient-node"
          catalog={fieldPresentationCatalog}
          view="fields"
          semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
          presentationContext={fieldPresentationContext()}
          initialFieldSource={{
            candidateId: fieldPresentationCodeCandidate.candidateId,
            nodeId: fieldPresentationCodeCandidate.nodeId,
            path: fieldPresentationCodeCandidate.fieldPath,
          }}
          onAddSelected={vi.fn().mockResolvedValue(undefined)}
        />
      </LoomProvider>,
    );

    const rawField = await screen.findByRole('button', { name: 'Select Patient.condition.code' });
    expect(rawField).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('listitem', { name: 'Selected Condition code' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Included in/ })).not.toBeInTheDocument();
  });

  it('keeps field presentation scoped to the active source and omits it from cross-source coded search', async () => {
    const replacement = fieldPresentationReplacement();
    const medicationReason: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-medication-reason',
      nodeId: 'medication-node',
      fieldPath: 'reasonCode',
      label: 'Medication reason code',
      logicalType: 'code',
      constructionChoice: fieldChoice(
        'field-choice-medication-reason',
        'candidate-medication-reason',
        'medication-node',
        'Medication',
        'reasonCode',
      ),
    };
    const scopedCatalog: ExplorerBuilderCatalog = {
      ...fieldPresentationCatalog,
      nodes: [
        ...fieldPresentationCatalog.nodes,
        { nodeId: 'medication-node', resourceType: 'Medication', rowRootEligible: true, populated: true, documentCount: 1 },
      ],
      edges: [
        ...(fieldPresentationCatalog.edges ?? []),
        { edgeId: 'patient-medication', fromNodeId: 'patient-node', toNodeId: 'medication-node', label: 'medications' },
      ],
      candidates: [...(fieldPresentationCatalog.candidates ?? []), medicationReason],
    };
    const requests: Array<ReturnType<typeof semanticInventoryBrowseRequestSchema.parse>> = [];
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      const request = semanticInventoryBrowseRequestSchema.parse(JSON.parse(String(init?.body)));
      requests.push(request);
      if (request.resourceType === 'Medication') return semanticBrowseResponse();
      if (request.query === 'no-match') return semanticBrowseResponse();
      if ('fieldPresentation' in request && request.fieldPresentation) {
        return semanticBrowseResponse(
          readyFieldPresentation(request.fieldPresentation, { replacement }),
          [replacement],
        );
      }
      return semanticBrowseResponse(undefined, [replacement]);
    });
    const client = createLoomClient({ fetch });
    const Picker = () => {
      const [resourceType, setResourceType] = React.useState('Patient');
      const sourceNodeId = resourceType === 'Patient' ? 'patient-node' : 'medication-node';
      return (
        <LoomProvider client={client}>
          <button type="button" onClick={() => setResourceType('Medication')}>Medication source</button>
          <button type="button" onClick={() => setResourceType('Patient')}>Patient source</button>
          <ConceptCatalog
            project="project-a"
            explorerId="explorer-a"
            snapshotToken="snapshot-a"
            outputId="patients"
            rowRoot="Patient"
            resourceType={resourceType}
            sourceNodeId={sourceNodeId}
            catalog={scopedCatalog}
            view="all"
            semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
            presentationContext={fieldPresentationContext()}
            onAddSelected={vi.fn().mockResolvedValue(undefined)}
          />
        </LoomProvider>
      );
    };
    render(<Picker />);

    const patientSemanticLabel = 'Tumor · Patient Condition Code · system urn:condition-codes · version unversioned · code tumor';
    expect(await screen.findByRole('button', { name: `Select ${patientSemanticLabel}` })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'Medication source' }));
    expect(await screen.findByRole('button', { name: 'Select Medication.reasonCode' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: `Select ${patientSemanticLabel}` })).not.toBeInTheDocument();
    const medicationRequest = requests.find((request) => request.resourceType === 'Medication');
    expect(medicationRequest && 'fieldPresentation' in medicationRequest && medicationRequest.fieldPresentation).toBeFalsy();

    fireEvent.click(screen.getByRole('button', { name: 'Patient source' }));
    expect(await screen.findByRole('button', { name: `Select ${patientSemanticLabel}` })).toBeEnabled();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'no-match' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: `Select ${patientSemanticLabel}` })).not.toBeInTheDocument();
    const crossSourceSearch = requests.at(-1);
    expect(crossSourceSearch?.query).toBe('no-match');
    expect(crossSourceSearch && 'fieldPresentation' in crossSourceSearch && crossSourceSearch.fieldPresentation).toBeFalsy();
  });

  it('keeps proof replacements out of ordinary pages and coded no-match searches', async () => {
    const replacement = fieldPresentationReplacement();
    const firstPageItem = item('other-page-one', 'Other page one', 4);
    const secondPageItem = item('other-page-two', 'Other page two', 3);
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      const request = semanticInventoryBrowseRequestSchema.parse(JSON.parse(String(init?.body)));
      if ('cursor' in request && request.cursor === 'coded-page-two') {
        return semanticBrowseResponse(undefined, [secondPageItem]);
      }
      if (request.query === 'no-match') return semanticBrowseResponse();
      if ('fieldPresentation' in request && request.fieldPresentation) {
        return semanticBrowseResponse(
          readyFieldPresentation(request.fieldPresentation, { replacement }),
          [firstPageItem],
          'coded-page-two',
        );
      }
      return semanticBrowseResponse(undefined, [firstPageItem]);
    });
    render(
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          sourceNodeId="patient-node"
          catalog={fieldPresentationCatalog}
          view="all"
          semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
          presentationContext={fieldPresentationContext()}
          onAddSelected={vi.fn().mockResolvedValue(undefined)}
        />
      </LoomProvider>,
    );

    expect(await screen.findByText('Other page one')).toBeInTheDocument();
    expect(screen.queryByText('Tumor')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('Other page two')).toBeInTheDocument();
    expect(screen.queryByText('Tumor')).not.toBeInTheDocument();

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'no-match' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    expect(screen.queryByText('Tumor')).not.toBeInTheDocument();
    const browseRequests = fetch.mock.calls.map(([, init]) =>
      semanticInventoryBrowseRequestSchema.parse(JSON.parse(String(init?.body))),
    );
    expect(browseRequests.filter((request) => 'fieldPresentation' in request && request.fieldPresentation)).toHaveLength(1);
  });

  it('refreshes cached proof once when a later browse observes a new build and on explicit refresh', async () => {
    const replacement = fieldPresentationReplacement();
    const firstPageItem = item('old-build-item', 'Old build item', 2);
    const secondPageItem = item('new-build-item', 'New build item', 2);
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      const request = semanticInventoryBrowseRequestSchema.parse(JSON.parse(String(init?.body)));
      if ('cursor' in request && request.cursor === 'new-build-page' && request.fieldPresentation) {
        return semanticBrowseResponse({ kind: 'UNAVAILABLE', reason: 'STALE_CONTEXT' }, [secondPageItem], undefined, 'build-2');
      }
      if ('cursor' in request && request.cursor === 'new-build-page') {
        return semanticBrowseResponse(undefined, [secondPageItem], undefined, 'build-2');
      }
      if ('fieldPresentation' in request && request.fieldPresentation) {
        const responseBuild = fetch.mock.calls.length > 1 ? 'build-2' : 'build-1';
        return semanticBrowseResponse(
          readyFieldPresentation(request.fieldPresentation, { replacement, buildId: responseBuild }),
          responseBuild === 'build-1' ? [replacement, firstPageItem] : [secondPageItem],
          responseBuild === 'build-1' ? 'new-build-page' : undefined,
          responseBuild,
        );
      }
      return semanticBrowseResponse(undefined, [firstPageItem], 'new-build-page', 'build-1');
    });
    render(
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          sourceNodeId="patient-node"
          catalog={fieldPresentationCatalog}
          view="all"
          semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
          presentationContext={fieldPresentationContext()}
          onAddSelected={vi.fn().mockResolvedValue(undefined)}
        />
      </LoomProvider>,
    );

    expect(await screen.findByText('Tumor')).toBeInTheDocument();
    expect(await screen.findByText('Old build item')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('New build item')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Select Patient.condition.code' })).toBeEnabled();
    expect(fetch).toHaveBeenCalledTimes(3);

    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(4));
    const explicitRefresh = semanticInventoryBrowseRequestSchema.parse(
      JSON.parse(String(fetch.mock.calls[3]?.[1]?.body)),
    );
    expect('fieldPresentation' in explicitRefresh && explicitRefresh.fieldPresentation).toBeTruthy();
  });

  it.each([
    'unavailable',
    'omitted',
    'partial',
    'stale',
    'missing-replacement',
    'filtered-extension',
  ] as const)('keeps eligible raw fields available when field proof is %s', async (scenario) => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      if (scenario === 'omitted') return semanticBrowseResponse();
      const request = fieldPresentationRequestFromInit(init);
      if (scenario === 'unavailable') {
        return semanticBrowseResponse({ kind: 'UNAVAILABLE', reason: 'COVERAGE_UNAVAILABLE' });
      }
      const replacement = scenario === 'filtered-extension'
        ? fieldPresentationReplacement('EXTENSION_URL_VALUE')
        : fieldPresentationReplacement();
      const ready = readyFieldPresentation(request, {
        replacement,
        stale: scenario === 'stale',
        partial: scenario === 'partial',
        missingReplacement: scenario === 'missing-replacement',
      });
      if (scenario === 'partial' && ready.kind === 'READY') {
        return semanticBrowseResponse({
          ...ready,
          decisions: ready.decisions.filter((decision) =>
            decision.candidateId === fieldPresentationCodeCandidate.candidateId,
          ),
        });
      }
      return semanticBrowseResponse(ready);
    });
    render(
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          sourceNodeId="patient-node"
          catalog={fieldPresentationCatalog}
          view="fields"
          semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
          presentationContext={fieldPresentationContext()}
          onAddSelected={vi.fn().mockResolvedValue(undefined)}
        />
      </LoomProvider>,
    );

    expect(await screen.findByRole('button', { name: 'Select Patient.condition.code' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Select Patient.clinicalNote' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: /Included in/ })).not.toBeInTheDocument();
  });

  it('does not apply a late field proof after the draft context changes', async () => {
    const replacement = fieldPresentationReplacement();
    let resolveFirst: ((response: Response) => void) | undefined;
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      const request = fieldPresentationRequestFromInit(init);
      if (request.expectedDraftVersion === 3) {
        return new Promise<Response>((resolve) => { resolveFirst = resolve; });
      }
      return semanticBrowseResponse({ kind: 'UNAVAILABLE', reason: 'STALE_CONTEXT' });
    });
    const client = createLoomClient({ fetch });
    const Picker = () => {
      const [draftVersion, setDraftVersion] = React.useState(3);
      return (
        <LoomProvider client={client}>
          <button type="button" onClick={() => setDraftVersion(4)}>Change draft</button>
          <ConceptCatalog
            project="project-a"
            explorerId="explorer-a"
            snapshotToken="snapshot-a"
            outputId="patients"
            tableContextKey={`${draftVersion}:draft-digest-${draftVersion}`}
            rowRoot="Patient"
            sourceNodeId="patient-node"
            catalog={fieldPresentationCatalog}
            view="fields"
            semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
            presentationContext={fieldPresentationContext(draftVersion)}
            onAddSelected={vi.fn().mockResolvedValue(undefined)}
          />
        </LoomProvider>
      );
    };
    render(<Picker />);
    await waitFor(() => expect(resolveFirst).toBeDefined());

    fireEvent.click(screen.getByRole('button', { name: 'Change draft' }));
    expect(await screen.findByRole('button', { name: 'Select Patient.condition.code' })).toBeEnabled();
    await act(async () => {
      resolveFirst?.(semanticBrowseResponse(readyFieldPresentation({
        ...fieldPresentationContext(),
        candidateIds: [
          fieldPresentationCodeCandidate.candidateId,
          fieldPresentationTextCandidate.candidateId,
        ],
      }, { replacement })));
    });
    expect(screen.getByRole('button', { name: 'Select Patient.condition.code' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: /Included in/ })).not.toBeInTheDocument();
  });

  it('clears selected and advanced action state immediately when the table context changes', async () => {
    const concept = item('draft-selection', 'Draft selection', 3);
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([concept])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const client = createLoomClient({ fetch });
    const Picker = () => {
      const [tableContextKey, setTableContextKey] = React.useState('3:draft-digest-3');
      return (
        <LoomProvider client={client}>
          <button type="button" onClick={() => setTableContextKey('4:draft-digest-4')}>Change table</button>
          <ConceptCatalog
            project="project-a"
            explorerId="explorer-a"
            snapshotToken="snapshot-a"
            outputId="patients"
            tableContextKey={tableContextKey}
            rowRoot="Patient"
            catalog={catalog}
            view="coded"
            onAddSelected={vi.fn().mockResolvedValue(undefined)}
          />
        </LoomProvider>
      );
    };
    render(<Picker />);
    fireEvent.click(await screen.findByRole('button', { name: 'Select Draft selection' }));
    expect(screen.getByLabelText('Selected feature action')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Advanced options' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Change table' }));
    expect(screen.queryByLabelText('Selected feature action')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Advanced options' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add selected features' })).not.toBeInTheDocument();
  });

  it('groups populated compiler fields by source and skips generated schema-only fields', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([item('hidden-code', 'Diagnostic Method', 4)])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const observationStatus: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-observation-status',
      nodeId: 'observation-node',
      fieldPath: 'status',
      label: 'Observation status',
      constructionChoice: fieldChoice(
        'field-choice-observation-status',
        'candidate-observation-status',
        'observation-node',
        'Observation',
        'status',
      ),
    };
    const unpopulatedGender: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-patient-gender',
      fieldPath: 'gender',
      label: 'Patient gender',
      populated: false,
      constructionChoice: fieldChoice('field-choice-patient-gender', 'candidate-patient-gender', 'patient-node', 'Patient', 'gender'),
    };
    const unknownPopulationField: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-patient-city',
      fieldPath: 'address.city',
      label: 'Patient city',
      populated: undefined,
      constructionChoice: fieldChoice('field-choice-patient-city', 'candidate-patient-city', 'patient-node', 'Patient', 'address.city'),
    };
    const catalogWithFields = {
      ...catalog,
      candidates: [...(catalog.candidates ?? []), observationStatus, unpopulatedGender, unknownPopulationField],
    } satisfies ExplorerBuilderCatalog;
    render(<LoomProvider client={createFullLoomClient({ fetch })}>
      <ConceptCatalog
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-a"
        outputId="patients"
        rowRoot="Patient"
        catalog={catalogWithFields}
        view="fields"
      />
    </LoomProvider>);

    const fields = await screen.findByTestId('feature-catalog-raw-fields');
    expect(fields.tagName).toBe('DIV');
    expect(screen.queryByRole('heading', { name: 'Available dataset fields' })).not.toBeInTheDocument();
    expect(screen.getByText(/^\d+ available$/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Fields from Patient' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Fields from Observation' })).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Select Patient.id' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Select Observation.status' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Select Patient.gender' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Patient city' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Patient.birthDate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select DocumentReference.id' })).not.toBeInTheDocument();
    expect(screen.getByRole('searchbox', { name: 'Search dataset fields by name or type' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Coded values for this table' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Diagnostic Method' })).not.toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it('hides repeated related fields while keeping root repeated and related scalar fields selectable', async () => {
    const repeatedBoundaries = [{ path: 'identifier[]', maxItems: 9 }];
    const repeatedChoice = (
      choiceId: string,
      candidateId: string,
      nodeId: string,
      resourceType: string,
      path: string,
      repeatedBoundary: { path: string; maxItems: number },
    ): ConstructionChoice => {
      const choice = fieldChoice(choiceId, candidateId, nodeId, resourceType, path);
      return {
        ...choice,
        source: {
          ...choice.source,
          cardinality: 'many',
          repeatedBoundaries: [repeatedBoundary],
        },
        options: [choiceOption('ALL', 'DEFAULT')],
      };
    };
    const identifierValue: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-patient-identifier-value',
      nodeId: 'patient-node',
      fieldPath: 'identifier[].value',
      label: 'Identifier Value',
      cardinality: 'many',
      repeated: true,
      repeatedBoundaries,
      constructionChoice: repeatedChoice(
        'field-choice-patient-identifier-value',
        'candidate-patient-identifier-value',
        'patient-node',
        'Patient',
        'identifier[].value',
        repeatedBoundaries[0]!,
      ),
    };
    const patientBirthDate: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-patient-birth-date',
      nodeId: 'patient-node',
      fieldPath: 'birthDate',
      label: 'Patient birth date',
      constructionChoice: fieldChoice(
        'field-choice-patient-birth-date',
        'candidate-patient-birth-date',
        'patient-node',
        'Patient',
        'birthDate',
      ),
    };
    const observationComponentValue: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-observation-component-value',
      nodeId: 'observation-node',
      fieldPath: 'component[].value',
      label: 'Observation component value',
      cardinality: 'many',
      repeated: true,
      repeatedBoundaries: [{ path: 'component[]', maxItems: 3 }],
      constructionChoice: repeatedChoice(
        'field-choice-observation-component-value',
        'candidate-observation-component-value',
        'observation-node',
        'Observation',
        'component[].value',
        { path: 'component[]', maxItems: 3 },
      ),
    };
    const researchSubjectReferenceBoundary = { path: 'extension[]', maxItems: 3 };
    const researchSubjectReferenceChoice = fieldChoice(
      'field-choice-research-subject-reference',
      'candidate-research-subject-reference',
      'research-subject-node',
      'ResearchSubject',
      'extension[].valueReference.reference',
    );
    const researchSubjectReference: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-research-subject-reference',
      nodeId: 'research-subject-node',
      fieldPath: 'extension[].valueReference.reference',
      label: 'Extension Value Reference Reference',
      cardinality: 'optional_one',
      repeated: true,
      repeatedBoundaries: [researchSubjectReferenceBoundary],
      constructionChoice: {
        ...researchSubjectReferenceChoice,
        source: {
          ...researchSubjectReferenceChoice.source,
          repeatedBoundaries: [researchSubjectReferenceBoundary],
        },
        options: [choiceOption('ALL', 'DEFAULT')],
      },
    };
    const catalogWithFields = {
      ...catalog,
      nodes: [
        ...catalog.nodes,
        { nodeId: 'research-subject-node', resourceType: 'ResearchSubject', rowRootEligible: false, populated: true, documentCount: 1 },
      ],
      edges: [
        ...catalog.edges,
        { edgeId: 'observation-research-subject', fromNodeId: 'observation-node', toNodeId: 'research-subject-node', label: 'researchSubject' },
      ],
      candidates: [
        ...(catalog.candidates ?? []),
        identifierValue,
        patientBirthDate,
        observationComponentValue,
        researchSubjectReference,
      ],
    } satisfies ExplorerBuilderCatalog;
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([])), { status: 200, headers: { 'content-type': 'application/json' } }),
    );

    render(<LoomProvider client={createFullLoomClient({ fetch })}>
      <ConceptCatalog
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-a"
        outputId="observations"
        rowRoot="Observation"
        catalog={catalogWithFields}
        view="fields"
      />
    </LoomProvider>);

    expect(await screen.findByRole('button', { name: 'Select Patient.birthDate' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Select Patient.identifier[].value' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Select Observation.component[].value' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Select ResearchSubject.extension[].valueReference.reference' }))
      .not.toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it('omits raw fields outside the catalog reachable set', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([])), { status: 200, headers: { 'content-type': 'application/json' } }),
    );
    renderCatalog(fetch);

    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Search' })).toBeEnabled());
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'id' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByRole('button', { name: 'Select Observation.identifier' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select DocumentReference.id' })).not.toBeInTheDocument();
  });

  it('keeps all-view field search on populated index entries and preserves related-source search', async () => {
    const observationStatus: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-observation-status',
      nodeId: 'observation-node',
      fieldPath: 'status',
      label: 'Observation status',
      populated: true,
      constructionChoice: fieldChoice(
        'field-choice-observation-status', 'candidate-observation-status', 'observation-node', 'Observation', 'status',
      ),
    };
    const unpopulatedGender: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-patient-gender',
      fieldPath: 'gender',
      label: 'Patient gender',
      populated: false,
      constructionChoice: fieldChoice('field-choice-patient-gender', 'candidate-patient-gender', 'patient-node', 'Patient', 'gender'),
    };
    const unknownPopulationField: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'candidate-patient-city',
      fieldPath: 'address.city',
      label: 'Patient city',
      populated: undefined,
      constructionChoice: fieldChoice('field-choice-patient-city', 'candidate-patient-city', 'patient-node', 'Patient', 'address.city'),
    };
    const catalogWithFields = {
      ...catalog,
      candidates: [...(catalog.candidates ?? []), observationStatus, unpopulatedGender, unknownPopulationField],
    } satisfies ExplorerBuilderCatalog;
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([])), { status: 200, headers: { 'content-type': 'application/json' } }),
    );
    renderCatalog(fetch, vi.fn().mockResolvedValue(undefined), undefined, {}, catalogWithFields);

    expect(await screen.findByRole('button', { name: 'Select Patient.id' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Patient.gender' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Patient city' })).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'status' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByRole('button', { name: 'Select Observation.status' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Patient.gender' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Patient city' })).not.toBeInTheDocument();
    expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/schema-fields'))).toBe(false);
  });

  it('omits raw and semantic fields rejected by the current stage', async () => {
    const stageFetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([item('current', 'Current concept', 1)])), {
        status: 200, headers: { 'content-type': 'application/json' },
      }),
    );
    render(<LoomProvider client={createLoomClient({ fetch: stageFetch })}>
      <ConceptCatalog
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-a"
        outputId="patients"
        rowRoot="Patient"
        catalog={catalog}
        sourceProjectionAvailability={{ available: false, reason: 'The current stage cannot add columns.' }}
      />
    </LoomProvider>);
    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Patient.id' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Current concept' })).not.toBeInTheDocument();
  });

  it('shows observed source scope and server-supported forms before adding a root concept', async () => {
    const observed = item('4548-4', 'Hemoglobin A1c', 91);
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([observed])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    renderCatalog(fetch);

    expect(await screen.findByText('91 observed source occurrences')).toBeInTheDocument();
    const resultRow = screen.getByRole('button', { name: 'Select Hemoglobin A1c' }).closest('article');
    expect(resultRow).not.toBeNull();
    if (!resultRow) throw new Error('The concept result row is missing.');
    const selectionButton = within(resultRow).getByRole('button', { name: 'Select Hemoglobin A1c' });
    const detailsButton = within(resultRow).getByRole('button', { name: 'Details for Hemoglobin A1c' });
    expect(within(resultRow).getByTestId('catalog-item-metadata').contains(detailsButton)).toBe(true);
    expect(selectionButton.contains(detailsButton)).toBe(false);
    detailsButton.focus();
    fireEvent.focus(detailsButton);
    expect(detailsButton).toHaveAttribute('aria-expanded', 'true');
    expect(within(resultRow).getByText(/does not provide a denominator or coverage of the current table rows/)).toBeInTheDocument();
    expect(within(resultRow).getByText(/Available results: One value/)).toBeInTheDocument();
    expect(within(resultRow).getByText('Completeness: partial.')).toBeInTheDocument();
    expect(within(resultRow).getByText(/Observed units: g\/dL · additional units exist/)).toBeInTheDocument();
    expect(within(resultRow).getByText(/Observed examples: 10\.2, 11\.0 · additional examples exist/)).toBeInTheDocument();
    expect(within(resultRow).getByText('Server choice uses the Patient root resource; no route steps')).toBeInTheDocument();
    expect(within(resultRow).getByText(/Loom proves the VALUE output form preserves the row grain\./)).toBeInTheDocument();
    fireEvent.keyDown(detailsButton, { key: 'Escape' });
    expect(detailsButton).toHaveAttribute('aria-expanded', 'false');
    expect(document.activeElement).toBe(detailsButton);
  });

  it('omits semantic fields when the selected stage cannot retain source identity', async () => {
    const observed = item('4548-4', 'Hemoglobin A1c', 91);
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([observed])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    renderCatalog(fetch, onAddSelected, {
      available: false,
      reason: 'The selected stage has a different row identity after pivoting.',
    });

    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Hemoglobin A1c' })).not.toBeInTheDocument();
    expect(screen.getByText(/selected stage has a different row identity after pivoting/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add selected features' })).not.toBeInTheDocument();

    expect(onAddSelected).not.toHaveBeenCalled();
  });

  it('loads table-specific route and output alternatives before the final Add action', async () => {
    const related = item('718-7', 'Hemoglobin', 64, 'Observation');
    const routeChoice: ConstructionChoice = {
      ...semanticChoice('route-choice-subject', related, [
        choiceOption('VALUE', 'DEFAULT'),
        choiceOption('ALL', 'REQUIRES_DECISION'),
      ]),
      route: [{
        edgeId: 'patient-observation-subject',
        fromNodeId: 'patient-node',
        toNodeId: 'observation-node',
        fromResourceType: 'Patient',
        toResourceType: 'Observation',
        relationship: 'subject',
        storageDirection: 'INBOUND',
        matchMode: 'OPTIONAL',
      }],
      presentation: {
        summary: 'Observation through subject',
        facts: [{ label: 'Relationship', value: 'subject' }],
      },
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const url = String(input);
      const response = url.endsWith('/semantic-inventory')
        ? page([related])
        : url.endsWith('/construction-choices')
          ? {
              snapshotToken: 'snapshot-a',
              outputId: 'patients',
              complete: true,
              truncated: false,
              choices: [routeChoice],
            }
          : undefined;
      if (!response) throw new Error(`Unexpected request: ${url}`);
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    const onAddSelected = renderCatalogAtRoute(fetch, {
      occurrenceId: 'observations',
      nodeId: 'observation-node',
    });

    const result = await screen.findByRole('button', { name: 'Select Hemoglobin' });
    const resultRow = result.closest('article');
    expect(resultRow).not.toBeNull();
    if (!resultRow) throw new Error('The related concept result row is missing.');
    fireEvent.focus(within(resultRow).getByRole('button', { name: 'Details for Hemoglobin' }));
    fireEvent.click(within(resultRow).getByRole('button', { name: 'Load choices for these table rows' }));
    expect(result).toHaveAttribute('aria-pressed', 'false');

    expect(await within(resultRow).findByText('Observation through subject')).toBeInTheDocument();
    expect(within(resultRow).getByText(/via subject \(optional, inbound\)/)).toBeInTheDocument();
    expect(within(resultRow).getByText(/Server default/)).toBeInTheDocument();
    expect(within(resultRow).getByText(/Requires a choice/)).toBeInTheDocument();
    expect(onAddSelected).not.toHaveBeenCalled();
  });

  it('applies one compiler DEFAULT field form after one Add click', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () =>
      new Response(JSON.stringify(page([])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const onAddSelected = renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('button', { name: 'Select Patient.id' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'field-choice-id', form: 'VALUE' },
      title: 'Patient ID',
    }]));
    expect(screen.queryByRole('region', { name: 'Advanced route and form choices' })).not.toBeInTheDocument();
  });

  it('sends the exact related field choice and candidate while keeping root projection simple', async () => {
    const relatedChoice: ConstructionChoice = {
      ...fieldChoice(
        'diagnostic-report-amount-choice',
        'diagnostic-report-amount-candidate',
        'diagnostic-report-node',
        'DiagnosticReport',
        'amount',
      ),
      route: [{
        edgeId: 'patient-diagnostic-report',
        fromNodeId: 'patient-node',
        toNodeId: 'diagnostic-report-node',
        fromResourceType: 'Patient',
        toResourceType: 'DiagnosticReport',
        relationship: 'reports',
        storageDirection: 'OUTBOUND',
        matchMode: 'OPTIONAL',
      }],
      options: [
        choiceOption('VALUE', 'DEFAULT'),
        choiceOption('ALL', 'REQUIRES_DECISION'),
      ],
    };
    const relatedCandidate: ExplorerBuilderCandidate = {
      ...rootId,
      candidateId: 'diagnostic-report-amount-candidate',
      nodeId: 'diagnostic-report-node',
      fieldPath: 'amount',
      label: 'amount',
      projectionModes: ['VALUE', 'ALL'],
      defaultProjectionMode: 'VALUE',
      constructionChoice: relatedChoice,
    };
    const relatedCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      nodes: [
        ...catalog.nodes,
        {
          nodeId: 'diagnostic-report-node',
          resourceType: 'DiagnosticReport',
          rowRootEligible: false,
          populated: true,
          documentCount: 1,
        },
      ],
      edges: [
        ...catalog.edges,
        {
          edgeId: 'patient-diagnostic-report',
          fromNodeId: 'patient-node',
          toNodeId: 'diagnostic-report-node',
          label: 'reports',
        },
      ],
      candidates: [...(catalog.candidates ?? []), relatedCandidate],
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const url = String(input);
      const response = url.endsWith('/construction-choices')
        ? {
            snapshotToken: 'snapshot-a',
            outputId: 'patients',
            complete: true,
            truncated: false,
            choices: [relatedChoice],
          }
        : page([]);
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    render(
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          catalog={relatedCatalog}
          relatedSourceAvailability={{ supported: true }}
          onAddSelected={onAddSelected}
        />
      </LoomProvider>,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'Select Patient.id' }));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'amount' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Select DiagnosticReport.amount' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    expect(await screen.findByRole('region', { name: 'Advanced route and form choices' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('radio', {
      name: 'Amount: Keep all matching values',
    }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 2 selected features' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([
      {
        constructionChoice: { choiceId: 'field-choice-id', form: 'VALUE' },
        title: 'Patient ID',
      },
      {
        constructionChoice: { choiceId: 'diagnostic-report-amount-choice', form: 'ALL' },
        title: 'Amount',
        relatedSource: { choice: relatedChoice, candidate: relatedCandidate },
      },
    ]));
  });

  it('shows the RELATED_SOURCE reason and omits related fields when unsupported', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    const reason = 'Loom does not advertise RELATED_SOURCE for this stage.';
    renderCatalog(fetch, onAddSelected, {
      available: true,
      reason: 'The selected stage retains the source row identity.',
    }, {}, catalog, { supported: false, reason });

    const rootField = await screen.findByRole('button', { name: 'Select Patient.id' });
    expect(rootField).toBeEnabled();
    expect(screen.getByText(new RegExp(reason))).toBeInTheDocument();

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'identifier' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Select Observation.identifier' })).not.toBeInTheDocument());
    expect(onAddSelected).not.toHaveBeenCalled();
  });

  it('allows supported related fields when source projection is unavailable and reports preview submission', async () => {
    const relatedChoice: ConstructionChoice = {
      ...fieldChoice(
        'observation-identifier-related-choice',
        'candidate-observation-id',
        'observation-node',
        'Observation',
        'identifier',
      ),
      route: [{
        edgeId: 'patient-observation',
        fromNodeId: 'patient-node',
        toNodeId: 'observation-node',
        fromResourceType: 'Patient',
        toResourceType: 'Observation',
        relationship: 'observations',
        storageDirection: 'OUTBOUND',
        matchMode: 'OPTIONAL',
      }],
      options: [choiceOption('ALL', 'DEFAULT')],
    };
    const sourceCandidate = catalog.candidates?.find(
      (candidate) => candidate.candidateId === 'candidate-observation-id',
    );
    if (!sourceCandidate) throw new Error('The related field candidate is missing.');
    const relatedCandidate: ExplorerBuilderCandidate = {
      ...sourceCandidate,
      constructionChoice: relatedChoice,
    };
    const relatedCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      candidates: (catalog.candidates ?? []).map((candidate) =>
        candidate.candidateId === relatedCandidate.candidateId
          ? relatedCandidate
          : candidate,
      ),
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const response = String(input).endsWith('/construction-choices')
        ? {
            snapshotToken: 'snapshot-a',
            outputId: 'patients',
            complete: true,
            truncated: false,
            choices: [relatedChoice],
          }
        : page([]);
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    renderCatalog(fetch, onAddSelected, {
      available: false,
      reason: 'The selected stage does not retain source projection identity.',
    }, {}, relatedCatalog, { supported: true });

    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Search' })).toBeEnabled());
    expect(screen.queryByRole('button', { name: 'Select Patient.id' })).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'identifier' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    const relatedField = await screen.findByRole('button', { name: 'Select Observation.identifier' });
    expect(relatedField).toBeEnabled();
    fireEvent.click(relatedField);
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    const advanced = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    fireEvent.click(within(advanced).getByRole('radio', { name: 'Observation id: Keep all matching values' }));
    fireEvent.click(within(advanced).getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'observation-identifier-related-choice', form: 'ALL' },
      title: 'Observation id',
      relatedSource: { choice: relatedChoice, candidate: relatedCandidate },
    }]));
    expect(screen.getByText('Related-source proposal submitted. Review the preview before applying.')).toBeInTheDocument();
  });

  it('keeps the same related-field choice open after a failed ONE proposal so ALL can be retried directly', async () => {
    const sourceCandidate = catalog.candidates?.find(
      (candidate) => candidate.candidateId === 'candidate-observation-id',
    );
    if (!sourceCandidate) throw new Error('The related field candidate is missing.');
    const relatedChoice: ConstructionChoice = {
      ...fieldChoice(
        'observation-identifier-related-choice',
        sourceCandidate.candidateId,
        sourceCandidate.nodeId,
        'Observation',
        sourceCandidate.fieldPath,
      ),
      route: [{
        edgeId: 'patient-observation',
        fromNodeId: 'patient-node',
        toNodeId: 'observation-node',
        fromResourceType: 'Patient',
        toResourceType: 'Observation',
        relationship: 'observations',
        storageDirection: 'OUTBOUND',
        matchMode: 'OPTIONAL',
      }],
      options: [choiceOption('VALUE', 'DEFAULT'), choiceOption('ALL', 'REQUIRES_DECISION')],
    };
    const relatedCandidate: ExplorerBuilderCandidate = {
      ...sourceCandidate,
      constructionChoice: relatedChoice,
    };
    const relatedCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      candidates: (catalog.candidates ?? []).map((candidate) =>
        candidate.candidateId === relatedCandidate.candidateId ? relatedCandidate : candidate,
      ),
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const response = String(input).endsWith('/construction-choices')
        ? {
            snapshotToken: 'snapshot-a',
            outputId: 'patients',
            complete: true,
            truncated: false,
            choices: [relatedChoice],
          }
        : page([]);
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    let attempts = 0;
    const onAddSelected = vi.fn(async () => {
      attempts += 1;
      if (attempts === 1) throw new Error('ONE has multiple distinct values.');
      return 'preview-ready' as const;
    });
    const loomClient = createLoomClient({ fetch });
    const RelatedCatalog = () => {
      const [policy, setPolicy] = React.useState<'ALL' | 'ONE'>('ALL');
      return (
        <>
          <label>
            <span>Values from each row&apos;s contributing records</span>
            <select
              aria-label="Values from each row&apos;s contributing records"
              value={policy}
              onChange={(event) => setPolicy(event.currentTarget.value as 'ALL' | 'ONE')}
            >
              <option value="ALL">Keep all distinct values</option>
              <option value="ONE">Require one distinct value</option>
            </select>
          </label>
          <LoomProvider client={loomClient}>
            <ConceptCatalog
              project="project-a"
              explorerId="explorer-a"
              snapshotToken="snapshot-a"
              outputId="patients"
              rowRoot="Patient"
              resourceType="Observation"
              catalog={relatedCatalog}
              sourceProjectionAvailability={{ available: false, reason: 'The stage uses a related source.' }}
              relatedSourceAvailability={{ supported: true }}
              groupedRowValuePolicy={{ value: policy, onChange: setPolicy }}
              onAddSelected={onAddSelected}
            />
          </LoomProvider>
        </>
      );
    };
    render(<RelatedCatalog />);

    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Observation id: Keep all matching values' }));
    fireEvent.change(screen.getByRole('combobox', { name: "Values from each row's contributing records" }), {
      target: { value: 'ONE' },
    });
    expect(screen.queryByRole('combobox', { name: 'Values per grouped row' })).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(screen.getByText('ONE has multiple distinct values.', { exact: true })).toBeInTheDocument());
    expect(screen.getByRole('region', { name: 'Advanced route and form choices' })).toBeInTheDocument();
    expect(within(dialog).getByRole('radio', { name: 'Observation id: Keep all matching values' })).toHaveProperty('checked', true);
    expect(screen.getByRole('combobox', { name: "Values from each row's contributing records" })).toHaveProperty('value', 'ONE');
    expect(onAddSelected).toHaveBeenNthCalledWith(1, [{
      constructionChoice: { choiceId: relatedChoice.choiceId, form: 'ALL', rowValuePolicy: 'ONE' },
      title: 'Observation id',
      relatedSource: { choice: relatedChoice, candidate: relatedCandidate },
    }]);

    fireEvent.change(screen.getByRole('combobox', { name: "Values from each row's contributing records" }), {
      target: { value: 'ALL' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 selected feature' }));
    await waitFor(() => expect(onAddSelected).toHaveBeenCalledTimes(2));
    expect(onAddSelected).toHaveBeenNthCalledWith(2, [{
      constructionChoice: { choiceId: relatedChoice.choiceId, form: 'ALL' },
      title: 'Observation id',
      relatedSource: { choice: relatedChoice, candidate: relatedCandidate },
    }]);
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Advanced route and form choices' })).not.toBeInTheDocument());
  });

  it('suppresses repeated capability errors while the Builder repairs saved source fields', async () => {
    const rawCompileError = 'INVALID_RECIPE at $.recipe: invalid_construction at $.outputs[0].construction: source projection[2].type "unknown" is unsupported';
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );

    renderCatalog(
      fetch,
      vi.fn().mockResolvedValue(undefined),
      { available: false, reason: rawCompileError },
      {},
      catalog,
      { supported: false, reason: rawCompileError },
      true,
    );

    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Patient.id' })).not.toBeInTheDocument();
    expect(screen.queryByText(/Add from source is unavailable here:/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Adding fields from related resources is unavailable here:/)).not.toBeInTheDocument();
    expect(screen.queryByText(/INVALID_RECIPE/)).not.toBeInTheDocument();
  });

  it('explains pending source selection and re-enables fields when the draft settles', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const renderPending = (disabled: boolean) => (
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          catalog={catalog}
          sourceProjectionAvailability={{
            available: true,
            reason: 'The compiler confirms this stage retains the source row identity.',
          }}
          disabled={disabled}
          disabledReason="Loom is finishing the previous table update. Field selection will return when the draft refresh completes."
        />
      </LoomProvider>
    );
    const view = render(renderPending(true));
    const field = await screen.findByRole('button', { name: 'Select Patient.id' });
    expect(field).toBeDisabled();
    expect(screen.getByText(
      'Loom is finishing the previous table update. Field selection will return when the draft refresh completes.',
    )).toHaveAttribute('role', 'status');
    expect(field).toHaveAttribute('aria-describedby');

    view.rerender(renderPending(false));

    expect(screen.getByRole('button', { name: 'Select Patient.id' })).toBeEnabled();
  });

  it('searches ordinary fields from the active row-root candidates', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () =>
      new Response(JSON.stringify(page([])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    renderCatalog(fetch);

    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    const search = await screen.findByRole('searchbox', { name: 'Search coded values by code or label' });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Search' })).toBeEnabled());
    expect(screen.getByRole('button', { name: 'Select Patient.id' })).toBeInTheDocument();
    fireEvent.change(search, { target: { value: 'missing' } });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(screen.queryByRole('button', { name: 'Select Patient.id' })).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Search' })).toBeEnabled());

    fireEvent.change(search, { target: { value: 'id' } });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByRole('button', { name: 'Select Patient.id' })).toBeInTheDocument();
  });

  it('puts row-root fields first and identifies each searched field by resource', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () =>
      new Response(JSON.stringify(page([])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    renderCatalog(fetch);

    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    const search = await screen.findByRole('searchbox', { name: 'Search coded values by code or label' });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Search' })).toBeEnabled());
    fireEvent.change(search, { target: { value: 'id' } });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));

    await waitFor(() => {
      const names = screen.getAllByRole('button')
        .map((button) => button.getAttribute('aria-label'))
        .filter((name): name is string => Boolean(name?.startsWith('Select ')));
      expect(names[0]).toBe('Select Patient.id');
      expect(names).toEqual(expect.arrayContaining([
        'Select Observation.identifier',
      ]));
      expect(names).not.toContain('Select DocumentReference.id');
      expect(new Set(names).size).toBe(names.length);
    });
    for (const resourceType of ['Patient', 'Observation']) {
      expect(screen.getByText(resourceType, { exact: true })).toBeInTheDocument();
    }
  });

  it('keeps selections across semantic pages and searches without limiting simple search to the row-root resource', async () => {
    const first = item('4548-4', 'Hemoglobin A1c', 91);
    const second = item('718-7', 'Hemoglobin', 64);
    const third = item('2345-7', 'Glucose', 37);
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_url, init) => {
      const body = JSON.parse(String(init?.body)) as { query?: string; cursor?: string; resourceType?: string };
      expect(body.resourceType).toBeUndefined();
      const response = body.query === 'glucose'
        ? page([third])
        : body.cursor === 'cursor-2'
          ? page([second])
          : page([first], 'cursor-2');
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'content-type': 'application/json' },
    });
    });
    const onAddSelected = renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('button', { name: 'Select Hemoglobin A1c' }));
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Select Hemoglobin' }));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'glucose' },
  });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Select Glucose' }));
    expect(screen.getByLabelText('Selected feature action')).toHaveTextContent('3 features selected');

    fireEvent.click(screen.getByRole('button', { name: 'Add 3 selected features' }));
    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([
      { constructionChoice: { choiceId: 'choice-4548-4', form: 'VALUE' }, title: 'Hemoglobin A1c' },
      { constructionChoice: { choiceId: 'choice-718-7', form: 'VALUE' }, title: 'Hemoglobin' },
      { constructionChoice: { choiceId: 'choice-2345-7', form: 'VALUE' }, title: 'Glucose' },
    ]));
  });

  it('uses server-issued route choices for a related concept without sending browser-selected edges', async () => {
    const related = item('718-7', 'Hemoglobin', 64, 'Observation');
    const firstRoute = {
      ...semanticChoice('route-choice-a', related),
      route: [{
        edgeId: 'patient-observation-subject',
        fromNodeId: 'patient-node',
        toNodeId: 'observation-node',
        fromResourceType: 'Patient',
        toResourceType: 'Observation',
        relationship: 'subject_Patient',
        storageDirection: 'INBOUND' as const,
        matchMode: 'OPTIONAL' as const,
      }],
      presentation: {
        summary: 'Observation through subject',
        facts: [{ label: 'Concept', value: 'http://loinc.org · 718-7' }],
      },
    };
    const secondRoute = {
      ...semanticChoice('route-choice-b', related),
      route: [{
        edgeId: 'patient-diagnostic-report-result',
        fromNodeId: 'patient-node',
        toNodeId: 'diagnostic-report-node',
        fromResourceType: 'Patient',
        toResourceType: 'DiagnosticReport',
        relationship: 'subject_Patient',
        storageDirection: 'INBOUND' as const,
        matchMode: 'OPTIONAL' as const,
      }, {
        edgeId: 'diagnostic-report-observation-result',
        fromNodeId: 'diagnostic-report-node',
        toNodeId: 'observation-node',
        fromResourceType: 'DiagnosticReport',
        toResourceType: 'Observation',
        relationship: 'result_Observation',
        storageDirection: 'OUTBOUND' as const,
        matchMode: 'OPTIONAL' as const,
      }],
      presentation: {
        summary: 'Observation through DiagnosticReport result',
        facts: [{ label: 'Concept', value: 'http://loinc.org · 718-7' }],
      },
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input, init) => {
      const url = String(input);
      if (url.endsWith('/semantic-inventory')) {
        return new Response(JSON.stringify(page([related])), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      if (url.endsWith('/construction-choices')) {
        const request = JSON.parse(String(init?.body)) as { cursor?: string };
        return new Response(JSON.stringify({
          snapshotToken: 'snapshot-a',
          outputId: 'patients',
          complete: request.cursor === 'route-cursor-2',
          truncated: request.cursor !== 'route-cursor-2',
          ...(request.cursor ? {} : { nextCursor: 'route-cursor-2' }),
          choices: request.cursor ? [secondRoute] : [firstRoute],
        }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      throw new Error(`Unexpected request: ${url} ${String(init?.body)}`);
    });
    const onAddSelected = renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('button', { name: 'Select Hemoglobin' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));

    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    expect(screen.getByText(/more relationship paths are available/i)).toBeInTheDocument();
    expect(within(dialog).getByRole('radio', {
      name: 'Hemoglobin: Patient <-[subject]- Observation',
    })).toHaveProperty('checked', false);
    expect(within(dialog).getByRole('button', { name: 'Add 1 selected feature' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Load more paths' }));
    fireEvent.click(await within(dialog).findByText('Other relationship paths (1)'));
    fireEvent.click(screen.getByRole('radio', {
      name: 'Hemoglobin: Patient <-[subject]- DiagnosticReport -[result]-> Observation',
    }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'route-choice-b', form: 'VALUE' },
      title: 'Hemoglobin',
    }]));
    const routeRequest = JSON.parse(String(fetch.mock.calls[1]?.[1]?.body)) as Record<string, unknown>;
    expect(routeRequest).toEqual({
      snapshotToken: 'snapshot-a',
      outputId: 'patients',
      source: {
        kind: 'SEMANTIC',
        contextToken: 'context-1',
        buildId: 'build-1',
        conceptId: 'concept-718-7',
        bindingId: 'binding-718-7',
      },
      limit: 10,
    });
    expect(JSON.stringify(routeRequest)).not.toContain('edgeId');
    const nextRouteRequest = JSON.parse(String(fetch.mock.calls[2]?.[1]?.body)) as Record<string, unknown>;
    expect(nextRouteRequest).toEqual({ ...routeRequest, cursor: 'route-cursor-2' });
  });

  it('preserves a saved result form when switching to another route that supports it', async () => {
    const related = item('route-count', 'Route count', 8, 'Observation');
    const countOption: ConstructionChoice['options'][number] = {
      form: 'COUNT',
      shape: 'SCALAR',
      decision: 'REQUIRES_DECISION',
      preservation: 'REDUCING',
      rowEffect: 'PRESERVES_ROW_GRAIN',
      support: 'SUPPORTED',
      reason: 'Loom proves this route supports distinct related record counts.',
    };
    const subjectChoice: ConstructionChoice = {
      ...semanticChoice('saved-subject-route', related, [countOption]),
      route: [{
        edgeId: 'patient-observation-subject',
        fromNodeId: 'patient-node',
        toNodeId: 'observation-node',
        fromResourceType: 'Patient',
        toResourceType: 'Observation',
        relationship: 'subject_Patient',
        storageDirection: 'INBOUND',
        matchMode: 'OPTIONAL',
      }],
    };
    const focusChoice: ConstructionChoice = {
      ...subjectChoice,
      choiceId: 'saved-focus-route',
      route: [{
        ...subjectChoice.route[0]!,
        edgeId: 'patient-observation-focus',
        relationship: 'focus_Patient',
      }],
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const url = String(input);
      const response = url.endsWith('/semantic-inventory')
        ? page([related])
        : url.endsWith('/construction-choices')
          ? {
              snapshotToken: 'snapshot-a',
              outputId: 'patients',
              complete: true,
              truncated: false,
              choices: [subjectChoice, focusChoice],
            }
          : undefined;
      if (!response) throw new Error(`Unexpected request: ${url}`);
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    const onAddSelected = renderCatalogAtRoute(
      fetch,
      { occurrenceId: 'observations', nodeId: 'observation-node' },
      vi.fn().mockResolvedValue(undefined),
      { choiceId: subjectChoice.choiceId, form: 'COUNT' },
    );

    fireEvent.click(await screen.findByRole('button', { name: 'Select Route count' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    const countRadio = within(dialog).getByRole('radio', { name: 'Route count: Count matching records' });
    expect(countRadio).toHaveProperty('checked', true);
    fireEvent.click(within(dialog).getByRole('radio', {
      name: 'Route count: Patient <-[focus]- Observation',
    }));
    expect(countRadio).toHaveProperty('checked', true);
    expect(within(dialog).getByRole('button', { name: 'Add 1 selected feature' })).toBeEnabled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: focusChoice.choiceId, form: 'COUNT' },
      title: 'Route count',
    }]));
  });

  it('uses the resolved compiler default for the selected authored occurrence route on one Add click', async () => {
    const related = item('718-7', 'Hemoglobin', 64, 'Observation');
    const routeChoice = (
      choiceId: string,
      edgeId: string,
      summary: string,
    ): ConstructionChoice => ({
      ...semanticChoice(choiceId, related),
      route: [{
        edgeId,
        fromNodeId: 'patient-node',
        toNodeId: 'observation-node',
        fromResourceType: 'Patient',
        toResourceType: 'Observation',
        relationship: edgeId === 'patient-observation-subject' ? 'subject' : 'encounter',
        storageDirection: 'INBOUND',
        matchMode: 'OPTIONAL',
      }],
      presentation: {
        summary,
        facts: [{ label: 'Concept', value: 'http://loinc.org · 718-7' }],
      },
    });
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith('/semantic-inventory')) {
        return new Response(JSON.stringify(page([related])), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      if (url.endsWith('/construction-choices')) {
        return new Response(JSON.stringify({
          snapshotToken: 'snapshot-a',
          outputId: 'patients',
          complete: true,
          truncated: false,
          choices: [routeChoice(
            'route-choice-subject',
            'patient-observation-subject',
            'Observation through subject',
          )],
        }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const onAddSelected = renderCatalogAtRoute(fetch, {
      occurrenceId: 'observation-through-subject',
      nodeId: 'observation-node',
    });

    fireEvent.click(await screen.findByRole('button', { name: 'Select Hemoglobin' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'route-choice-subject', form: 'VALUE' },
      title: 'Hemoglobin',
    }]));
    expect(screen.queryByRole('region', { name: 'Advanced route and form choices' })).not.toBeInTheDocument();
    const inventoryRequest = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as Record<string, unknown>;
    expect(inventoryRequest).toMatchObject({ resourceType: 'Observation', rowRoot: 'Patient' });
    const constructionRequest = JSON.parse(String(fetch.mock.calls[1]?.[1]?.body)) as Record<string, unknown>;
    expect(constructionRequest).toMatchObject({
      occurrenceId: 'observation-through-subject',
      source: {
        kind: 'SEMANTIC',
        conceptId: 'concept-718-7',
        bindingId: 'binding-718-7',
      },
    });
    expect(JSON.stringify(constructionRequest)).not.toContain('edgeId');
  });

  it('announces a direct preview timeout beside the selected-feature action', async () => {
    const hemoglobin = item('718-7', 'Hemoglobin', 64);
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([hemoglobin])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const onAddSelected = vi.fn().mockRejectedValue(new LoomRequestError({
      status: 504,
      code: 'PREVIEW_TIMEOUT',
      message: 'preview exceeded its execution deadline',
      requestId: '18af4cde-7754-4203-93ae-86cd03c66df7',
      retryable: true,
    }));
    renderCatalog(fetch, onAddSelected);

    fireEvent.click(await screen.findByRole('button', { name: 'Select Hemoglobin' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    const action = screen.getByLabelText('Selected feature action');
    expect(await within(action).findByRole('alert')).toHaveTextContent('preview exceeded its execution deadline');
    expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'choice-718-7', form: 'VALUE' },
      title: 'Hemoglobin',
    }]);
  });

  it('keeps a successful add notice as status after clearing the selection', async () => {
    const hemoglobin = item('718-7', 'Hemoglobin', 64);
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([hemoglobin])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    renderCatalog(fetch, onAddSelected);

    fireEvent.click(await screen.findByRole('button', { name: 'Select Hemoglobin' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    expect(await screen.findByRole('status')).toHaveTextContent('1 feature was added to your table.');
    expect(screen.queryByLabelText('Selected feature action')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('filters field and concept discovery to the selected catalog node', async () => {
    const patientConcept = item('patient-code', 'Patient concept', 8);
    const observationConcept = item('observation-code', 'Observation concept', 12, 'Observation');
    const unscopedConcept = {
      ...item('unscoped-code', 'Unscoped observation concept', 4, 'Observation'),
      constructionChoice: undefined,
    };
    const otherObservationSource = item('other-code', 'Other observation source', 3, 'Observation');
    const otherObservationChoice = semanticChoice(
      'other-observation-choice',
      otherObservationSource,
    );
    const otherObservationNodeConcept = {
      ...otherObservationSource,
      constructionChoice: {
        ...otherObservationChoice,
        source: {
          ...otherObservationChoice.source,
          nodeId: 'other-observation-node',
        },
      },
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () =>
      new Response(JSON.stringify(page([patientConcept, observationConcept, otherObservationNodeConcept, unscopedConcept])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    renderCatalog(fetch, vi.fn().mockResolvedValue(undefined), undefined, {
      resourceType: 'Observation',
      nodeId: 'observation-node',
    });

    expect(await screen.findByRole('button', { name: 'Select Observation.identifier' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Patient.id' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Select Observation concept' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Other observation source' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Unscoped observation concept' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('construction-operation-unscoped-concepts')).not.toBeInTheDocument();

    const inventoryRequest = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as Record<string, unknown>;
    expect(inventoryRequest).toMatchObject({ rowRoot: 'Patient', resourceType: 'Observation' });

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'all resources' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByRole('button', { name: 'Select Patient concept' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Select Other observation source' })).toBeInTheDocument();
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    const globalSearchRequest = JSON.parse(String(fetch.mock.calls[1]?.[1]?.body)) as Record<string, unknown>;
    expect(globalSearchRequest).toMatchObject({ rowRoot: 'Patient', query: 'all resources' });
    expect(globalSearchRequest.resourceType).toBeUndefined();

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search coded values by code or label' }), {
      target: { value: 'id' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByRole('button', { name: 'Select Patient.id' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Select Observation.identifier' })).toBeInTheDocument();
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
    const globalFieldSearchRequest = JSON.parse(String(fetch.mock.calls[2]?.[1]?.body)) as Record<string, unknown>;
    expect(globalFieldSearchRequest).toMatchObject({ rowRoot: 'Patient', query: 'id' });
    expect(globalFieldSearchRequest.resourceType).toBeUndefined();
  });

  it('removes a field after route search exhausts without a current-table choice', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const url = String(input);
      const response = url.endsWith('/semantic-inventory')
        ? page([])
        : url.endsWith('/construction-choices')
          ? {
              snapshotToken: 'snapshot-a',
              outputId: 'patients',
              complete: true,
              truncated: false,
              choices: [],
            }
          : undefined;
      if (!response) throw new Error(`Unexpected request: ${url}`);
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    const renderAtTableContext = (tableContextKey: string) => (
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a" explorerId="explorer-a" snapshotToken="snapshot-a"
          outputId="patients" tableContextKey={tableContextKey} rowRoot="Patient"
          resourceType="Observation" sourceNodeId="observation-node" catalog={catalog}
          relatedSourceAvailability={{ supported: true }} onAddSelected={onAddSelected}
        />
      </LoomProvider>
    );
    const view = render(renderAtTableContext('draft-1:digest-1'));

    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));

    expect(await screen.findByText(/is not available for this table and was removed from your selection/)).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Advanced route and form choices' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Observation.identifier' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add 1 selected feature' })).not.toBeInTheDocument();
    expect(screen.queryByText(/No verified path or table value is available/)).not.toBeInTheDocument();
    expect(onAddSelected).not.toHaveBeenCalled();
    const choiceRequestCall = fetch.mock.calls.find(([input]) => String(input).endsWith('/construction-choices'));
    const choiceRequest = JSON.parse(String(choiceRequestCall?.[1]?.body)) as Record<string, unknown>;
    expect(choiceRequest).toMatchObject({
      outputId: 'patients',
      source: { kind: 'FIELD', candidateId: 'candidate-observation-id' },
    });
    expect(choiceRequest).not.toHaveProperty('route');

    view.rerender(renderAtTableContext('draft-2:digest-2'));
    expect(await screen.findByRole('button', { name: 'Select Observation.identifier' })).toBeInTheDocument();
  });

  it('keeps a field exclusion when semantic inventory finishes after the route search', async () => {
    let resolveInventory: ((response: Response) => void) | undefined;
    const fetch = vi.fn<typeof globalThis.fetch>((input) => {
      const url = String(input);
      if (url.endsWith('/semantic-inventory')) {
        return new Promise<Response>((resolve) => { resolveInventory = resolve; });
      }
      if (url.endsWith('/construction-choices')) {
        return Promise.resolve(new Response(JSON.stringify({
          snapshotToken: 'snapshot-a', outputId: 'patients', complete: true, truncated: false, choices: [],
        }), { status: 200, headers: { 'content-type': 'application/json' } }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    renderCatalog(fetch, vi.fn().mockResolvedValue(undefined), undefined, {
      resourceType: 'Observation', nodeId: 'observation-node',
    }, catalog, { supported: true });

    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    expect(await screen.findByText(/is not available for this table and was removed from your selection/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Observation.identifier' })).not.toBeInTheDocument();

    resolveInventory?.(new Response(JSON.stringify(page([])), { status: 200, headers: { 'content-type': 'application/json' } }));
    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Select Observation.identifier' })).not.toBeInTheDocument();
  });

  it('continues an empty field-choice page until a usable current-table route is found', async () => {
    const candidate = catalog.candidates?.find((entry) => entry.candidateId === 'candidate-observation-id');
    if (!candidate) throw new Error('Missing related field fixture.');
    const choice: ConstructionChoice = {
      ...fieldChoice('observation-identifier-related-choice', candidate.candidateId, candidate.nodeId, 'Observation', 'identifier'),
      route: [{
        edgeId: 'patient-observation',
        fromNodeId: 'patient-node',
        toNodeId: 'observation-node',
        fromResourceType: 'Patient',
        toResourceType: 'Observation',
        relationship: 'observations',
        storageDirection: 'OUTBOUND',
        matchMode: 'OPTIONAL',
      }],
      options: [{
        form: 'COUNT', shape: 'SCALAR', decision: 'DEFAULT', preservation: 'REDUCING',
        rowEffect: 'PRESERVES_ROW_GRAIN', support: 'SUPPORTED',
        reason: 'Counts matching related values for each table row.',
      }],
    };
    let routePageCount = 0;
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const url = String(input);
      const response = url.endsWith('/semantic-inventory')
        ? page([])
        : url.endsWith('/construction-choices')
          ? (++routePageCount === 1
            ? { snapshotToken: 'snapshot-a', outputId: 'patients', complete: false, truncated: true, nextCursor: 'route-page-2', choices: [] }
            : { snapshotToken: 'snapshot-a', outputId: 'patients', complete: true, truncated: false, choices: [choice] })
          : undefined;
      if (!response) throw new Error(`Unexpected request: ${url}`);
      return new Response(JSON.stringify(response), { status: 200, headers: { 'content-type': 'application/json' } });
    });
    renderCatalog(fetch, vi.fn().mockResolvedValue(undefined), undefined, {
      resourceType: 'Observation', nodeId: 'observation-node',
    }, catalog, { supported: true });

    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));

    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    expect(within(dialog).getByRole('heading', { name: /Observation · Observation id/ })).toBeInTheDocument();
    expect(within(dialog).queryByText('No verified path or table value is available for this field.')).not.toBeInTheDocument();
    const choiceRequests = fetch.mock.calls.filter(([input]) => String(input).endsWith('/construction-choices'));
    expect(choiceRequests).toHaveLength(2);
    expect(JSON.parse(String(choiceRequests[0]?.[1]?.body))).not.toHaveProperty('cursor');
    expect(JSON.parse(String(choiceRequests[1]?.[1]?.body))).toMatchObject({ cursor: 'route-page-2' });
    expect(choiceRequests[1]?.[1]?.signal).toBe(choiceRequests[0]?.[1]?.signal);
  });

  it('keeps supported choices from a truncated page even when no cursor remains', async () => {
    const candidate = catalog.candidates?.find((entry) => entry.candidateId === 'candidate-observation-id');
    if (!candidate) throw new Error('Missing related field fixture.');
    const choice: ConstructionChoice = {
      ...fieldChoice('observation-identifier-limited-choice', candidate.candidateId, candidate.nodeId, 'Observation', 'identifier'),
      route: [{
        edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node',
        fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'observations',
        storageDirection: 'OUTBOUND', matchMode: 'OPTIONAL',
      }],
      options: [{
        form: 'COUNT', shape: 'SCALAR', decision: 'DEFAULT', preservation: 'REDUCING',
        rowEffect: 'PRESERVES_ROW_GRAIN', support: 'SUPPORTED', reason: 'Counts matching related values.',
      }],
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const url = String(input);
      const response = url.endsWith('/semantic-inventory')
        ? page([])
        : url.endsWith('/construction-choices')
          ? { snapshotToken: 'snapshot-a', outputId: 'patients', complete: false, truncated: true, choices: [choice] }
          : undefined;
      if (!response) throw new Error(`Unexpected request: ${url}`);
      return new Response(JSON.stringify(response), { status: 200, headers: { 'content-type': 'application/json' } });
    });
    renderCatalog(fetch, vi.fn().mockResolvedValue(undefined), undefined, {
      resourceType: 'Observation', nodeId: 'observation-node',
    }, catalog, { supported: true });

    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    expect(within(dialog).getByRole('heading', { name: /Observation · Observation id/ })).toBeInTheDocument();
    expect(within(dialog).getByText('Path search reached its limit. Additional relationship paths may exist.')).toBeInTheDocument();
    expect(screen.queryByText(/is not available for this table and was removed/)).not.toBeInTheDocument();
  });

  it('keeps a supported choice when its page repeats a cursor and stops further paging', async () => {
    const candidate = catalog.candidates?.find((entry) => entry.candidateId === 'candidate-observation-id');
    if (!candidate) throw new Error('Missing related field fixture.');
    const choice: ConstructionChoice = {
      ...fieldChoice('observation-identifier-repeated-cursor-choice', candidate.candidateId, candidate.nodeId, 'Observation', 'identifier'),
      route: [{
        edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node',
        fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'observations',
        storageDirection: 'OUTBOUND', matchMode: 'OPTIONAL',
      }],
      options: [{
        form: 'COUNT', shape: 'SCALAR', decision: 'DEFAULT', preservation: 'REDUCING',
        rowEffect: 'PRESERVES_ROW_GRAIN', support: 'SUPPORTED', reason: 'Counts matching related values.',
      }],
    };
    let routePageCount = 0;
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const url = String(input);
      const response = url.endsWith('/semantic-inventory')
        ? page([])
        : url.endsWith('/construction-choices')
          ? (++routePageCount === 1
            ? { snapshotToken: 'snapshot-a', outputId: 'patients', complete: false, truncated: true, nextCursor: 'repeated-page', choices: [] }
            : { snapshotToken: 'snapshot-a', outputId: 'patients', complete: false, truncated: true, nextCursor: 'repeated-page', choices: [choice] })
          : undefined;
      if (!response) throw new Error(`Unexpected request: ${url}`);
      return new Response(JSON.stringify(response), { status: 200, headers: { 'content-type': 'application/json' } });
    });
    renderCatalog(fetch, vi.fn().mockResolvedValue(undefined), undefined, {
      resourceType: 'Observation', nodeId: 'observation-node',
    }, catalog, { supported: true });

    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    expect(within(dialog).getByRole('heading', { name: /Observation · Observation id/ })).toBeInTheDocument();
    expect(within(dialog).queryByRole('button', { name: 'Load more paths' })).not.toBeInTheDocument();
    expect(within(dialog).queryByText('No verified path or table value is available for this field.')).not.toBeInTheDocument();
    expect(screen.queryByText(/is not available for this table and was removed/)).not.toBeInTheDocument();
    expect(routePageCount).toBe(2);
  });

  it.each(['repeated cursor', 'incomplete page'] as const)('keeps a field selectable after a %s response', async (failure) => {
    const candidate = catalog.candidates?.find((entry) => entry.candidateId === 'candidate-observation-id');
    if (!candidate) throw new Error('Missing related field fixture.');
    const choice: ConstructionChoice = {
      ...fieldChoice('observation-identifier-retry-choice', candidate.candidateId, candidate.nodeId, 'Observation', 'identifier'),
      route: [{
        edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node',
        fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'observations',
        storageDirection: 'OUTBOUND', matchMode: 'OPTIONAL',
      }],
      options: [{
        form: 'COUNT', shape: 'SCALAR', decision: 'DEFAULT', preservation: 'REDUCING',
        rowEffect: 'PRESERVES_ROW_GRAIN', support: 'SUPPORTED', reason: 'Counts matching related values.',
      }],
    };
    let routePageCount = 0;
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith('/semantic-inventory')) {
        return new Response(JSON.stringify(page([])), { status: 200, headers: { 'content-type': 'application/json' } });
      }
      if (!url.endsWith('/construction-choices')) throw new Error(`Unexpected request: ${url}`);
      routePageCount += 1;
      const response = routePageCount === 1
        ? {
            snapshotToken: 'snapshot-a', outputId: 'patients', complete: false, truncated: true,
            ...(failure === 'repeated cursor' ? { nextCursor: 'repeated-page' } : {}), choices: [],
          }
        : failure === 'repeated cursor' && routePageCount === 2
          ? { snapshotToken: 'snapshot-a', outputId: 'patients', complete: false, truncated: true, nextCursor: 'repeated-page', choices: [] }
          : { snapshotToken: 'snapshot-a', outputId: 'patients', complete: true, truncated: false, choices: [choice] };
      return new Response(JSON.stringify(response), { status: 200, headers: { 'content-type': 'application/json' } });
    });
    renderCatalog(fetch, vi.fn().mockResolvedValue(undefined), undefined, {
      resourceType: 'Observation', nodeId: 'observation-node',
    }, catalog, { supported: true });

    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Select Observation.identifier' })).toHaveAttribute('aria-pressed', 'true');

    fireEvent.click(screen.getByRole('button', { name: 'Close advanced options' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    expect(await screen.findByRole('region', { name: 'Advanced route and form choices' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Select Observation.identifier' })).toBeInTheDocument();
    expect(screen.queryByText(/is not available for this table and was removed/)).not.toBeInTheDocument();
    expect(routePageCount).toBe(failure === 'repeated cursor' ? 3 : 2);
  });

  it('discards a delayed alternative page after the table context changes', async () => {
    const candidate = catalog.candidates?.find((entry) => entry.candidateId === 'candidate-observation-id');
    if (!candidate) throw new Error('Missing related field fixture.');
    const choice: ConstructionChoice = {
      ...fieldChoice('observation-identifier-choice', candidate.candidateId, candidate.nodeId, 'Observation', 'identifier'),
      route: [{
        edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node',
        fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'observations',
        storageDirection: 'OUTBOUND', matchMode: 'OPTIONAL',
      }],
      options: [{
        form: 'COUNT', shape: 'SCALAR', decision: 'DEFAULT', preservation: 'REDUCING',
        rowEffect: 'PRESERVES_ROW_GRAIN', support: 'SUPPORTED', reason: 'Counts matching related values.',
      }],
    };
    let routePageCount = 0;
    let delayedRoutePage: { readonly signal: AbortSignal | null | undefined; readonly resolve: (response: Response) => void } | undefined;
    const fetch = vi.fn<typeof globalThis.fetch>((input, init) => {
      const url = String(input);
      if (url.endsWith('/semantic-inventory')) {
        return Promise.resolve(new Response(JSON.stringify(page([])), { status: 200, headers: { 'content-type': 'application/json' } }));
      }
      if (!url.endsWith('/construction-choices')) throw new Error(`Unexpected request: ${url}`);
      routePageCount += 1;
      if (routePageCount === 1) {
        return Promise.resolve(new Response(JSON.stringify({
          snapshotToken: 'snapshot-a', outputId: 'patients', complete: false, truncated: true,
          nextCursor: 'route-page-2', choices: [choice],
        }), { status: 200, headers: { 'content-type': 'application/json' } }));
      }
      if (routePageCount === 2) {
        return new Promise<Response>((resolve) => {
          delayedRoutePage = { signal: init?.signal, resolve };
        });
      }
      return Promise.resolve(new Response(JSON.stringify({
        snapshotToken: 'snapshot-a', outputId: 'patients', complete: true, truncated: false, choices: [choice],
      }), { status: 200, headers: { 'content-type': 'application/json' } }));
    });
    const renderAtTableContext = (tableContextKey: string) => (
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a" explorerId="explorer-a" snapshotToken="snapshot-a"
          outputId="patients" tableContextKey={tableContextKey} rowRoot="Patient"
          resourceType="Observation" sourceNodeId="observation-node" catalog={catalog}
          relatedSourceAvailability={{ supported: true }} onAddSelected={vi.fn().mockResolvedValue(undefined)}
        />
      </LoomProvider>
    );
    const view = render(renderAtTableContext('draft-1:digest-1'));
    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    const dialog = await screen.findByRole('region', { name: 'Advanced route and form choices' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Load more paths' }));
    await waitFor(() => expect(delayedRoutePage).toBeDefined());

    view.rerender(renderAtTableContext('draft-2:digest-2'));
    expect(delayedRoutePage?.signal?.aborted).toBe(true);
    delayedRoutePage?.resolve(new Response(JSON.stringify({
      snapshotToken: 'snapshot-a', outputId: 'patients', complete: true, truncated: false, choices: [],
    }), { status: 200, headers: { 'content-type': 'application/json' } }));
    expect(await screen.findByRole('button', { name: 'Select Observation.identifier' })).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Advanced route and form choices' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced options' }));
    expect(await screen.findByRole('region', { name: 'Advanced route and form choices' })).toBeInTheDocument();
    expect(routePageCount).toBe(3);
  });
});


it.each(['COUNT', 'PRESENCE'] as const)('keeps signed related %s ownership when grouped source projection is unavailable', async (form) => {
  const sourceCandidate = catalog.candidates?.find(candidate => candidate.candidateId === 'candidate-observation-id');
  if (!sourceCandidate) throw new Error('Missing related fixture candidate');
  const choice: ConstructionChoice = {
    ...fieldChoice(`related-${form}`, sourceCandidate.candidateId, sourceCandidate.nodeId, 'Observation', sourceCandidate.fieldPath),
    route: [{ edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node', fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'observations', storageDirection: 'OUTBOUND', matchMode: 'OPTIONAL' }],
    options: [{ form, shape: 'SCALAR', decision: 'DEFAULT', preservation: 'REDUCING', rowEffect: 'PRESERVES_ROW_GRAIN', support: 'SUPPORTED', reason: 'Summarize distinct related records across grouped contributors.' }],
  };
  const candidate = { ...sourceCandidate, constructionChoice: choice };
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async input => new Response(JSON.stringify(String(input).endsWith('/construction-choices')
    ? { snapshotToken: 'snapshot-a', outputId: 'patients', complete: true, truncated: false, choices: [choice] }
    : page([])), { status: 200, headers: { 'content-type': 'application/json' } }));
  const onAddSelected = vi.fn().mockResolvedValue('preview-pending');
  render(<LoomProvider client={createLoomClient({ fetch })}><ConceptCatalog
    project="project-a" explorerId="explorer-a" snapshotToken="snapshot-a" outputId="patients" rowRoot="Patient" resourceType="Observation"
    catalog={{ ...catalog, candidates: [candidate] }} sourceProjectionAvailability={{ available: false, reason: 'Grouped rows' }}
    relatedSourceAvailability={{ supported: true }} groupedRowValuePolicy={{ value: 'ALL', onChange: vi.fn() }} onAddSelected={onAddSelected}
  /></LoomProvider>);
  fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.identifier' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));
  await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
    constructionChoice: { choiceId: choice.choiceId, form }, title: candidate.label,
    relatedSource: { choice, candidate },
  }]));
});
