// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createLoomClient } from '../../../api';
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
        onAddSelected={onAddSelected}
      />
    </LoomProvider>,
  );
  return onAddSelected;
};

describe('ConceptCatalog', () => {
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
        title: 'id',
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
    expect(within(resultRow).getByText(/Server-supported forms: One value/)).toBeInTheDocument();
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
      title: 'id',
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
      name: 'amount: Keep all matching values',
    }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 2 columns' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([
      {
        constructionChoice: { choiceId: 'field-choice-id', form: 'VALUE' },
        title: 'id',
      },
      {
        constructionChoice: { choiceId: 'diagnostic-report-amount-choice', form: 'ALL' },
        title: 'amount',
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
      name: 'Hemoglobin: Direct relationship: Patient to Observation via Subject',
    })).toHaveProperty('checked', false);
    expect(within(dialog).getByRole('button', { name: 'Add 1 column' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Load more paths' }));
    expect(await screen.findByRole('radio', {
      name: 'Hemoglobin: 2-relationship path: Patient to DiagnosticReport to Observation via Subject then Result',
    })).toBeInTheDocument();
    fireEvent.click(within(dialog).getByText('Other relationship paths (1)'));
    fireEvent.click(screen.getByRole('radio', {
      name: 'Hemoglobin: 2-relationship path: Patient to DiagnosticReport to Observation via Subject then Result',
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
