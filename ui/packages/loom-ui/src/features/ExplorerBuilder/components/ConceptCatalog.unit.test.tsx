// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createLoomClient } from '../../../api';
import { LoomProvider } from '../../../react';
import type {
  ConstructionChoice,
  ExplorerBuilderCatalog,
  ExplorerBuilderCandidate,
  SemanticInventoryBrowseResponse,
  SemanticInventoryItem,
} from '../../../types';
import {
  ConceptCatalog,
  type CatalogRouteContext,
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
) => {
    render(
      <LoomProvider client={createLoomClient({ fetch })}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId="patients"
          rowRoot="Patient"
          catalog={catalog}
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
  it('searches root fields and concepts, shows compiler forms, and submits only choice IDs and forms', async () => {
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

    const dialog = await screen.findByRole('dialog', { name: 'Choose output forms' });
    fireEvent.click(within(dialog).getAllByText('Source details')[1]!);
    expect(within(dialog).getByText('valueQuantity.value')).toBeInTheDocument();
    const defaultForm = screen.getByRole('radio', { name: 'Hemoglobin A1c: SCALAR · PRESERVING · VALUE' });
    const listForm = screen.getByRole('radio', { name: 'Hemoglobin A1c: LIST · PRESERVING · ALL' });
    expect(screen.getByRole('radio', { name: 'Hemoglobin A1c: Keep each matching record' })).toBeEnabled();
    expect(defaultForm).toHaveProperty('checked', true);
    expect(listForm).toHaveProperty('checked', false);
    fireEvent.click(listForm);
    fireEvent.click(screen.getByRole('button', { name: 'Add 2 selected features' }));

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
    expect(screen.queryByRole('dialog', { name: 'Choose output forms' })).not.toBeInTheDocument();
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
        relationship: 'subject',
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
        relationship: 'subject',
        storageDirection: 'INBOUND' as const,
        matchMode: 'OPTIONAL' as const,
      }, {
        edgeId: 'diagnostic-report-observation-result',
        fromNodeId: 'diagnostic-report-node',
        toNodeId: 'observation-node',
        fromResourceType: 'DiagnosticReport',
        toResourceType: 'Observation',
        relationship: 'result',
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
        return new Response(JSON.stringify({
          snapshotToken: 'snapshot-a',
          outputId: 'patients',
          complete: false,
          truncated: true,
          nextCursor: 'route-cursor-2',
          choices: [firstRoute, secondRoute],
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

    expect(await screen.findByRole('dialog', { name: 'Choose output forms' })).toBeInTheDocument();
    expect(screen.getByText(/automatic route-search limit/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('radio', {
      name: 'Hemoglobin route 2: Observation through DiagnosticReport result',
    }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

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
      limit: 50,
    });
    expect(JSON.stringify(routeRequest)).not.toContain('edgeId');
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
    expect(screen.queryByRole('dialog', { name: 'Choose output forms' })).not.toBeInTheDocument();
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
});
