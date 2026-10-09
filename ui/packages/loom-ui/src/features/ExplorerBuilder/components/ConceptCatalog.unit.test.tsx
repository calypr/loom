// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createLoomClient as createFullLoomClient } from '../../../api';

// These existing fixtures contain observed inventory only. Generated-field
// requests have their own route-aware fixture below.
const createLoomClient = (options: Parameters<typeof createFullLoomClient>[0]) => ({
  ...createFullLoomClient(options), searchSchemaFields: undefined,
});
import { LoomProvider } from '../../../react';
import type {
  AggregateTransformationCapability,
  ColumnValueTransformationCapabilities,
  ConstructionChoice,
  ExplorerBuilderCatalog,
  ExplorerBuilderCandidate,
  SemanticInventoryBrowseResponse,
  SemanticInventoryItem,
} from '../../../types';
import type { CatalogInitialSelection } from './CatalogSelectionDialog';
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
  return { ...value, constructionChoice: semanticChoice(`choice-${code}`, value) };
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
    expect(await screen.findByRole('checkbox', { name: 'Select Availability scope result' })).toBeInTheDocument();
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

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Patient.id' }));
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
    expect(screen.getByRole('button', { name: 'Add selected features' })).toBeDisabled();

    pending[1]?.resolve(new Response(JSON.stringify(page([
      item('new-scope', 'New scope result', 3),
    ])), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    }));
    expect(await screen.findByRole('checkbox', { name: 'Select New scope result' })).toBeInTheDocument();

    pending[0]?.resolve(new Response(JSON.stringify(page([
      item('old-scope', 'Old scope result', 1),
    ])), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    }));
    await waitFor(() => expect(screen.queryByRole('checkbox', { name: 'Select Old scope result' })).not.toBeInTheDocument());
    expect(screen.getByRole('checkbox', { name: 'Select New scope result' })).toBeInTheDocument();
  });

  it('aborts both inventory and generated-field reads when their scope owner changes', async () => {
    const pending: Array<{
      readonly url: string;
      readonly resolve: (response: Response) => void;
      readonly signal: AbortSignal | null | undefined;
    }> = [];
    const fetch = vi.fn<typeof globalThis.fetch>((input, init) => new Promise<Response>((resolve) => {
      pending.push({ url: String(input), resolve, signal: init?.signal });
    }));
    const client = createFullLoomClient({ fetch });
    const oldChoice = fieldChoice('old-generated-choice', 'old-generated-field', 'patient-node', 'Patient', 'old_marker');
    const currentChoice = fieldChoice('current-generated-choice', 'current-generated-field', 'patient-node', 'Patient', 'gender');
    const schemaResponse = (snapshotToken: string, path: string, constructionChoice: ConstructionChoice) => ({
      apiVersion: 'loom.calypr.org/explorer-authoring/v2',
      kind: 'ExplorerBuilderGeneratedSchemaFields',
      snapshotToken,
      schemaDigest: 'a'.repeat(64),
      nodeId: 'patient-node',
      resourceType: 'Patient',
      query: '',
      complete: true,
      truncated: false,
      fields: [{
        origin: 'GENERATED_SCHEMA',
        nodeId: 'patient-node',
        resourceType: 'Patient',
        path,
        primitiveType: 'string',
        cardinality: 'optional_one',
        constructionChoice,
      }],
    });
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
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    expect(pending[0]?.url.endsWith('/semantic-inventory')).toBe(true);
    expect(pending[1]?.url.endsWith('/schema-fields')).toBe(true);

    view.rerender(renderAtScope('snapshot-b'));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(4));
    await waitFor(() => {
      expect(pending[0]?.signal?.aborted).toBe(true);
      expect(pending[1]?.signal?.aborted).toBe(true);
    });
    expect(pending[2]?.signal?.aborted).toBe(false);
    expect(pending[3]?.signal?.aborted).toBe(false);

    pending[2]?.resolve(new Response(JSON.stringify(page([
      item('current-scope', 'Current scope result', 3),
    ])), { status: 200, headers: { 'content-type': 'application/json' } }));
    pending[3]?.resolve(new Response(JSON.stringify(schemaResponse('snapshot-b', 'gender', currentChoice)), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    }));
    expect(await screen.findByRole('checkbox', { name: 'Select Current scope result' })).toBeInTheDocument();
    expect(await screen.findByRole('checkbox', { name: 'Select Patient.gender' })).toBeInTheDocument();

    pending[0]?.resolve(new Response(JSON.stringify(page([
      item('old-scope', 'Old scope result', 1),
    ])), { status: 200, headers: { 'content-type': 'application/json' } }));
    pending[1]?.resolve(new Response(JSON.stringify(schemaResponse('snapshot-a', 'old_marker', oldChoice)), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    }));
    await waitFor(() => {
      expect(screen.queryByRole('checkbox', { name: 'Select Old scope result' })).not.toBeInTheDocument();
      expect(screen.queryByRole('checkbox', { name: 'Select Patient.old_marker' })).not.toBeInTheDocument();
    });
    expect(screen.getByRole('checkbox', { name: 'Select Current scope result' })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Select Patient.gender' })).toBeInTheDocument();
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

    const dialog = await screen.findByRole('dialog', { name: 'Choose how to add these fields' });
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

    expect(await screen.findByRole('searchbox', { name: 'Search features by field name, concept, or code' })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Select Patient.id' })).toBeEnabled();
    expect(await screen.findByRole('checkbox', { name: 'Select Hemoglobin A1c' })).toBeEnabled();
    expect(
      screen.getByRole('heading', { name: 'Coded values across the dataset' })
        .compareDocumentPosition(screen.getByRole('heading', { name: 'Fields on Patient' })) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.queryByRole('checkbox', { name: 'Select Observation id' })).not.toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Select Off-root concept' })).toBeEnabled();

    const request = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as { resourceType?: string; rowRoot?: string };
    expect(request).toMatchObject({ rowRoot: 'Patient' });
    expect(request).not.toHaveProperty('resourceType');

    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Patient.id' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Hemoglobin A1c' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 2 selected features' }));

    const dialog = await screen.findByRole('dialog', { name: 'Choose how to add these fields' });
    fireEvent.click(within(dialog).getAllByText('Technical path details')[1]!);
    expect(within(dialog).getByText('valueQuantity.value')).toBeInTheDocument();
    const defaultForm = screen.getByRole('radio', { name: 'Hemoglobin A1c: Use the matching value' });
    const listForm = screen.getByRole('radio', { name: 'Hemoglobin A1c: Keep all matching values' });
    expect(screen.getByRole('radio', { name: 'Hemoglobin A1c: Keep each matching record' })).toBeEnabled();
    expect(defaultForm).toHaveProperty('checked', true);
    expect(listForm).toHaveProperty('checked', false);
    fireEvent.click(listForm);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 2 columns' }));

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

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select All forms' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));
    const dialog = await screen.findByRole('dialog', { name: 'Choose how to add these fields' });
    const countRadio = within(dialog).getByRole('radio', { name: 'All forms: Count matching records' });
    fireEvent.click(countRadio);
    const countCard = countRadio.closest('.rounded-md') as HTMLElement | null;
    expect(countCard).not.toBeNull();
    if (!countCard) throw new Error('The count form card is missing.');
    expect(within(countCard).getByText(/What to expect: Loom supports the COUNT form/)).toBeInTheDocument();
    fireEvent.click(within(countCard).getByText('Technical form details'));
    expect(countRadio).toHaveProperty('checked', true);
    expect(within(countCard).getByText('COUNT')).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 column' }));
    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'choice-all-forms', form: 'COUNT' },
      title: 'All forms',
    }]));
  });

  it('shows unsupported semantic entries with their reason and keeps them non-selectable', async () => {
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

    expect(await screen.findByRole('checkbox', { name: 'Select Case identifier' })).toBeDisabled();
    expect(screen.getByText('Unsupported')).toBeInTheDocument();
    expect(screen.getAllByText(/does not contain the scalar selected by the current projection/).length).toBeGreaterThan(0);
    expect(screen.getByText('Availability')).toBeInTheDocument();
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
    expect(screen.getByText(/does not report a per-code denominator/)).toBeInTheDocument();

    const resultRow = screen.getByRole('checkbox', { name: 'Select Hemoglobin A1c' }).closest('article');
    expect(resultRow).not.toBeNull();
    if (!resultRow) throw new Error('The concept result row is missing.');
    expect(within(resultRow).getByText(/Available results: One value/)).toBeInTheDocument();
    fireEvent.click(within(resultRow).getByText('Inspect meaning, evidence, and construction choices'));
    expect(within(resultRow).getByText('Completeness: partial.')).toBeInTheDocument();
    expect(within(resultRow).getByText(/Observed units: g\/dL · additional units exist/)).toBeInTheDocument();
    expect(within(resultRow).getByText(/Observed examples: 10\.2, 11\.0 · additional examples exist/)).toBeInTheDocument();
    expect(within(resultRow).getByText('Server choice uses the Patient root resource; no route steps')).toBeInTheDocument();
    expect(within(resultRow).getByText(/Loom proves the VALUE output form preserves the row grain\./)).toBeInTheDocument();
  });

  it('keeps discovery evidence inspectable while disabling Add when the selected stage cannot retain source identity', async () => {
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

    const result = await screen.findByRole('checkbox', { name: 'Select Hemoglobin A1c' });
    expect(result).toBeDisabled();
    expect(screen.getByText(/selected stage has a different row identity after pivoting/)).toBeInTheDocument();
    expect(screen.getByText('Add selected features')).toBeDisabled();

    const resultRow = result.closest('article');
    expect(resultRow).not.toBeNull();
    if (!resultRow) throw new Error('The concept result row is missing.');
    fireEvent.click(within(resultRow).getByText('Inspect meaning, evidence, and construction choices'));
    expect(within(resultRow).getByText('91 source occurrences were observed for this code. The inventory response does not provide a denominator or coverage of the current table rows.')).toBeInTheDocument();
    expect(within(resultRow).getByText(/Observed examples: 10\.2, 11\.0 · additional examples exist/)).toBeInTheDocument();

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

    const result = await screen.findByRole('checkbox', { name: 'Select Hemoglobin' });
    const resultRow = result.closest('article');
    expect(resultRow).not.toBeNull();
    if (!resultRow) throw new Error('The related concept result row is missing.');
    fireEvent.click(within(resultRow).getByText('Inspect meaning, evidence, and construction choices'));
    fireEvent.click(within(resultRow).getByRole('button', { name: 'Load choices for these table rows' }));

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

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Patient.id' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'field-choice-id', form: 'VALUE' },
      title: 'Patient ID',
    }]));
    expect(screen.queryByRole('dialog', { name: 'Choose how to add these fields' })).not.toBeInTheDocument();
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

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Patient.id' }));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search features by field name, concept, or code' }), {
      target: { value: 'amount' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select DiagnosticReport.amount' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 2 selected features' }));
    expect(await screen.findByRole('dialog', { name: 'Choose how to add these fields' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('radio', {
      name: 'Amount: Keep all matching values',
    }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 2 columns' }));

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

  it('shows the RELATED_SOURCE reason and disables only related fields when unsupported', async () => {
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

    const rootField = await screen.findByRole('checkbox', { name: 'Select Patient.id' });
    expect(rootField).toBeEnabled();
    expect(screen.getByText(new RegExp(reason))).toBeInTheDocument();

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search features by field name, concept, or code' }), {
      target: { value: 'identifier' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    const relatedField = await screen.findByRole('checkbox', { name: 'Select Observation.identifier' });
    expect(relatedField).toBeDisabled();
    expect(relatedField).toHaveAttribute('aria-describedby');
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

    expect(await screen.findByRole('checkbox', { name: 'Select Patient.id' })).toBeDisabled();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search features by field name, concept, or code' }), {
      target: { value: 'identifier' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    const relatedField = await screen.findByRole('checkbox', { name: 'Select Observation.identifier' });
    expect(relatedField).toBeEnabled();
    fireEvent.click(relatedField);
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

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
      );
    };
    render(<RelatedCatalog />);

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));
    const dialog = await screen.findByRole('dialog', { name: 'Choose how to add these fields' });
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Observation id: Keep all matching values' }));
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Values per grouped row' }), {
      target: { value: 'ONE' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 column' }));

    await waitFor(() => expect(screen.getByText('ONE has multiple distinct values.', { exact: true })).toBeInTheDocument());
    expect(screen.getByRole('dialog', { name: 'Choose how to add these fields' })).toBeInTheDocument();
    expect(within(dialog).getByRole('radio', { name: 'Observation id: Keep all matching values' })).toHaveProperty('checked', true);
    expect(within(dialog).getByRole('combobox', { name: 'Values per grouped row' })).toHaveProperty('value', 'ONE');
    expect(onAddSelected).toHaveBeenNthCalledWith(1, [{
      constructionChoice: { choiceId: relatedChoice.choiceId, form: 'ALL', rowValuePolicy: 'ONE' },
      title: 'Observation id',
      relatedSource: { choice: relatedChoice, candidate: relatedCandidate },
    }]);

    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Values per grouped row' }), {
      target: { value: 'ALL' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 column' }));
    await waitFor(() => expect(onAddSelected).toHaveBeenCalledTimes(2));
    expect(onAddSelected).toHaveBeenNthCalledWith(2, [{
      constructionChoice: { choiceId: relatedChoice.choiceId, form: 'ALL' },
      title: 'Observation id',
      relatedSource: { choice: relatedChoice, candidate: relatedCandidate },
    }]);
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Choose how to add these fields' })).not.toBeInTheDocument());
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

    expect(await screen.findByRole('checkbox', { name: 'Select Patient.id' })).toBeDisabled();
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
    const field = await screen.findByRole('checkbox', { name: 'Select Patient.id' });
    expect(field).toBeDisabled();
    expect(screen.getByText(
      'Loom is finishing the previous table update. Field selection will return when the draft refresh completes.',
    )).toHaveAttribute('role', 'status');
    expect(field).toHaveAttribute('aria-describedby');

    view.rerender(renderPending(false));

    expect(screen.getByRole('checkbox', { name: 'Select Patient.id' })).toBeEnabled();
  });

  it('searches ordinary fields from the active row-root candidates', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () =>
      new Response(JSON.stringify(page([])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    renderCatalog(fetch);

    const search = await screen.findByRole('searchbox', { name: 'Search features by field name, concept, or code' });
    expect(screen.getByRole('checkbox', { name: 'Select Patient.id' })).toBeInTheDocument();
    fireEvent.change(search, { target: { value: 'missing' } });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(screen.queryByRole('checkbox', { name: 'Select Patient.id' })).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Search' })).toBeEnabled());

    fireEvent.change(search, { target: { value: 'id' } });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByRole('checkbox', { name: 'Select Patient.id' })).toBeInTheDocument();
  });

  it('puts row-root fields first and identifies each searched field by resource', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () =>
      new Response(JSON.stringify(page([])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    renderCatalog(fetch);

    const search = await screen.findByRole('searchbox', { name: 'Search features by field name, concept, or code' });
    fireEvent.change(search, { target: { value: 'id' } });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));

    await waitFor(() => {
      const names = screen.getAllByRole('checkbox').map((checkbox) => checkbox.getAttribute('aria-label'));
      expect(names[0]).toBe('Select Patient.id');
      expect(names).toEqual(expect.arrayContaining([
        'Select DocumentReference.id',
        'Select Observation.identifier',
      ]));
      expect(new Set(names).size).toBe(names.length);
    });
    for (const resourceType of ['Patient', 'DocumentReference', 'Observation']) {
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

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Hemoglobin A1c' }));
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Hemoglobin' }));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search features by field name, concept, or code' }), {
      target: { value: 'glucose' },
  });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Glucose' }));
    expect(screen.getByText('3')).toBeInTheDocument();

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

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Hemoglobin' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    const dialog = await screen.findByRole('dialog', { name: 'Choose how to add these fields' });
    expect(screen.getByText(/more relationship paths are available/i)).toBeInTheDocument();
    expect(within(dialog).getByRole('radio', {
      name: 'Hemoglobin: Patient <-[subject]- Observation',
    })).toHaveProperty('checked', false);
    expect(within(dialog).getByRole('button', { name: 'Add 1 column' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Load more paths' }));
    fireEvent.click(await within(dialog).findByText('Other relationship paths (1)'));
    fireEvent.click(screen.getByRole('radio', {
      name: 'Hemoglobin: Patient <-[subject]- DiagnosticReport -[result]-> Observation',
    }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 column' }));

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

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Route count' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));
    const dialog = await screen.findByRole('dialog', { name: 'Choose how to add these fields' });
    const countRadio = within(dialog).getByRole('radio', { name: 'Route count: Count matching records' });
    expect(countRadio).toHaveProperty('checked', true);
    fireEvent.click(within(dialog).getByRole('radio', {
      name: 'Route count: Patient <-[focus]- Observation',
    }));
    expect(countRadio).toHaveProperty('checked', true);
    expect(within(dialog).getByRole('button', { name: 'Add 1 column' })).toBeEnabled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 column' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: focusChoice.choiceId, form: 'COUNT' },
      title: 'Route count',
    }]));
  });

  it('pins node-local code selection to the selected authored occurrence route', async () => {
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

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Hemoglobin' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'route-choice-subject', form: 'VALUE' },
      title: 'Hemoglobin',
    }]));
    expect(screen.queryByRole('dialog', { name: 'Choose how to add these fields' })).not.toBeInTheDocument();
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
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(page([patientConcept, observationConcept, otherObservationNodeConcept, unscopedConcept])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    renderCatalog(fetch, vi.fn().mockResolvedValue(undefined), undefined, {
      resourceType: 'Observation',
      nodeId: 'observation-node',
    });

    expect(await screen.findByRole('checkbox', { name: 'Select Observation.identifier' })).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Patient.id' })).not.toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Select Observation concept' })).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Other observation source' })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Unscoped observation concept' })).not.toBeInTheDocument();
    expect(screen.getByTestId('construction-operation-unscoped-concepts')).toHaveTextContent(
      'Some concepts are hidden because Loom did not identify their source node.',
    );

    const inventoryRequest = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as Record<string, unknown>;
    expect(inventoryRequest).toMatchObject({ rowRoot: 'Patient', resourceType: 'Observation' });
  });

  it('keeps a related field out of Add when Loom returns no valid route', async () => {
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
    renderCatalog(fetch, onAddSelected, undefined, {
      resourceType: 'Observation',
      nodeId: 'observation-node',
    });

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Observation.identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    const dialog = await screen.findByRole('dialog', { name: 'Choose how to add these fields' });
    expect(within(dialog).getByText('No verified path or table value is available for this field.')).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Add 1 column' })).toBeDisabled();
    expect(onAddSelected).not.toHaveBeenCalled();
    const choiceRequest = JSON.parse(String(fetch.mock.calls[1]?.[1]?.body)) as Record<string, unknown>;
    expect(choiceRequest).toMatchObject({
      outputId: 'patients',
      source: { kind: 'FIELD', candidateId: 'candidate-observation-id' },
    });
    expect(choiceRequest).not.toHaveProperty('route');
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
  fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Observation.identifier' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));
  await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
    constructionChoice: { choiceId: choice.choiceId, form }, title: candidate.label,
    relatedSource: { choice, candidate },
  }]));
});


it('offers a compiler-issued missing schema field in the existing related-field chooser', async () => {
  const choice = fieldChoice('generated-gender-choice', 'generated-gender', 'patient-node', 'Patient', 'gender');
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (url, init) => {
    if (String(url).endsWith('/schema-fields') && JSON.parse(String(init?.body)).cursor) return new Promise<Response>(() => {});
    if (String(url).endsWith('/schema-fields')) return new Response(JSON.stringify({
      apiVersion: 'loom.calypr.org/explorer-authoring/v2', kind: 'ExplorerBuilderGeneratedSchemaFields',
      snapshotToken: 'snapshot-a', schemaDigest: 'a'.repeat(64), nodeId: 'patient-node',
      resourceType: 'Patient', query: '', complete: false, truncated: true, nextCursor: 'later-fields',
      fields: [{ origin: 'GENERATED_SCHEMA', nodeId: 'patient-node', resourceType: 'Patient',
        path: 'gender', primitiveType: 'string', cardinality: 'optional_one', constructionChoice: choice }],
    }), { status: 200, headers: { 'content-type': 'application/json' } });
    return new Response(JSON.stringify(page([])), { status: 200, headers: { 'content-type': 'application/json' } });
  });
  render(<LoomProvider client={createFullLoomClient({ fetch })}><ConceptCatalog
    project="project-a" explorerId="explorer-a" snapshotToken="snapshot-a" outputId="patients"
    rowRoot="Patient" resourceType="Patient" sourceNodeId="patient-node" catalog={catalog}
    onAddSelected={vi.fn().mockResolvedValue(undefined)}
  /></LoomProvider>);
  const control = await screen.findByRole('checkbox', { name: 'Select Patient.gender' });
  expect(control).not.toBeDisabled();
  fireEvent.click(control);
  expect(control instanceof HTMLInputElement && control.checked).toBe(true);
  const request = fetch.mock.calls.find(([url]) => String(url).endsWith('/schema-fields'));
  expect(JSON.parse(String(request?.[1]?.body))).toMatchObject({ snapshotToken: 'snapshot-a', nodeId: 'patient-node' });
});

it('starts generated-field discovery only when its containing panel is opened', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (url) =>
    new Response(JSON.stringify(String(url).endsWith('/schema-fields') ? {
      apiVersion: 'loom.calypr.org/explorer-authoring/v2', kind: 'ExplorerBuilderGeneratedSchemaFields',
      snapshotToken: 'snapshot-a', schemaDigest: 'a'.repeat(64), nodeId: 'patient-node',
      resourceType: 'Patient', query: '', fields: [], complete: true, truncated: false,
    } : page([])), { status: 200, headers: { 'content-type': 'application/json' } }));
  const client = createFullLoomClient({ fetch });
  const view = (enabled: boolean) => <LoomProvider client={client}><ConceptCatalog
    project="project-a" explorerId="explorer-a" snapshotToken="snapshot-a" outputId="patients"
    rowRoot="Patient" sourceNodeId="patient-node" resourceType="Patient" catalog={catalog}
    schemaDiscoveryEnabled={enabled} onAddSelected={vi.fn()}
  /></LoomProvider>;
  const { rerender } = render(view(false));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
  expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/schema-fields'))).toBe(false);
  rerender(view(true));
  await waitFor(() => expect(fetch.mock.calls.filter(([url]) => String(url).endsWith('/schema-fields'))).toHaveLength(1));
});
