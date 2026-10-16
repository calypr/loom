// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient } from '../../../api';
import { LoomProvider } from '../../../react';
import type {
  ConstructionChoice,
  ExplorerBuilderCatalog,
  ExplorerBuilderCandidate,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import type { CatalogChoiceIntent } from '../catalogItems';
import { ConstructionOperationsPanel } from './ConstructionOperationsPanel';

afterEach(cleanup);

const option: ConstructionChoice['options'][number] = {
  form: 'VALUE',
  shape: 'SCALAR',
  decision: 'DEFAULT',
  preservation: 'PRESERVING',
  rowEffect: 'PRESERVES_ROW_GRAIN',
  support: 'SUPPORTED',
  reason: 'Loom confirms one value per Patient row.',
};

const fieldChoice = (
  choiceId: string,
  candidateId: string,
  nodeId: string,
  resourceType: string,
): ConstructionChoice => ({
  choiceId,
  source: {
    kind: 'FIELD',
    candidateId,
    nodeId,
    resourceType,
    path: 'id',
    cardinality: 'optional_one',
  },
  route: [],
  presentation: {
    summary: `${resourceType} identifier`,
    facts: [{ label: 'Field', value: 'id' }],
  },
  options: [option],
});

const candidate = (
  candidateId: string,
  nodeId: string,
  resourceType: string,
  label: string,
): ExplorerBuilderCandidate => ({
  candidateId,
  nodeId,
  fieldPath: 'id',
  label,
  logicalType: 'string',
  cardinality: 'optional_one',
  repeated: false,
  filterable: true,
  chartable: false,
  projectionModes: ['VALUE'],
  defaultProjectionMode: 'VALUE',
  aggregateOperations: [],
  transformations: {
    temporalReduction: {
      available: false,
      reason: 'No temporal choice is available for this identifier.',
      timestampFields: [],
      anchorFields: [],
    },
    unitNormalization: {
      available: false,
      reason: 'No unit choice is available for this identifier.',
      presets: [],
    },
  },
  valueTransformations: {
    exactCategoryRecode: { available: false },
    codedValueRecoding: {
      available: false,
      reasonCode: 'NO_CODED_VALUE',
      reason: 'This identifier has no coded value to recode.',
    },
  },
  constructionChoice: fieldChoice(`${candidateId}-choice`, candidateId, nodeId, resourceType),
});

const table: DraftTable = {
  outputId: 'table-1',
  tabId: 'table-tab',
  title: 'Patients',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'table-1', title: 'Patients' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'patient-root', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [],
  },
};

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot-1',
  generation: 'generation-1',
  routePolicy: {},
  nodes: [
    { nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 10 },
    { nodeId: 'observation-node', resourceType: 'Observation', rowRootEligible: false, populated: true, documentCount: 20 },
    { nodeId: 'report-node', resourceType: 'DiagnosticReport', rowRootEligible: false, populated: true, documentCount: 12 },
  ],
  edges: [
    { edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node', label: 'observations' },
    { edgeId: 'patient-subject', fromNodeId: 'patient-node', toNodeId: 'report-node', label: 'subject' },
    { edgeId: 'patient-encounter', fromNodeId: 'patient-node', toNodeId: 'report-node', label: 'encounter' },
  ],
  candidates: [
    candidate('observation-id', 'observation-node', 'Observation', 'Observation identifier'),
    candidate('report-id', 'report-node', 'DiagnosticReport', 'Report identifier'),
  ],
};

const routeChoice = (
  choiceId: string,
  relationship: string,
): ConstructionChoice => ({
  ...fieldChoice(choiceId, 'report-id', 'report-node', 'DiagnosticReport'),
  route: [{
    edgeId: `patient-${relationship}`,
    fromNodeId: 'patient-node',
    toNodeId: 'report-node',
    fromResourceType: 'Patient',
    toResourceType: 'DiagnosticReport',
    relationship,
    storageDirection: 'INBOUND',
    matchMode: 'OPTIONAL',
  }],
  presentation: {
    summary: `Report through ${relationship}`,
    facts: [{ label: 'Relationship', value: relationship }],
  },
});

const browseResponse = {
  contextToken: 'catalog-context',
  buildId: 'catalog-build',
  state: 'complete',
  sourceAvailability: 'verified',
  entries: [],
};

const renderPanel = (
  fetch: typeof globalThis.fetch,
  onAddSelected: (selections: ReadonlyArray<CatalogChoiceIntent>) => Promise<void>,
) => render(
  <LoomProvider client={createLoomClient({ fetch })}>
    <ConstructionOperationsPanel
      family="ADD_COLUMNS"
      project="project-a"
      explorerId="explorer-a"
      snapshotToken="snapshot-1"
      draftVersion={1}
      draftDigest="digest-1"
      table={table}
      catalog={catalog}
      rowRoot="Patient"
      routeContext={{ occurrenceId: 'saved-observation-occurrence', nodeId: 'observation-node' }}
      disabled={false}
      onAddSelected={onAddSelected}
      onApplyProposal={async () => true}
    />
  </LoomProvider>,
);

describe('Add columns source selection', () => {
  it('drops a saved occurrence when switching to a related node and applies only a server route choice', async () => {
    const choices = [
      routeChoice('report-via-subject', 'subject_Patient'),
      routeChoice('report-via-encounter', 'encounter_DiagnosticReport'),
    ];
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => {
      const url = String(input);
      const response = url.endsWith('/semantic-inventory')
        ? browseResponse
        : url.endsWith('/construction-choices')
          ? {
              snapshotToken: 'snapshot-1',
              outputId: 'table-1',
              complete: true,
              truncated: false,
              choices,
            }
          : undefined;
      if (!response) throw new Error(`Unexpected request: ${url}`);
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    renderPanel(fetch, onAddSelected);

    fireEvent.click(screen.getByTestId('construction-operation-intention-add_columns-find_information'));
    const source = screen.getByTestId('construction-operation-source');
    expect(source).toHaveProperty('value', 'node:observation-node');
    fireEvent.change(source, { target: { value: 'node:report-node' } });

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select DiagnosticReport.id' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    const dialog = await screen.findByRole('dialog', { name: 'Choose how to add these fields' });
    const confirm = within(dialog).getByRole('button', { name: 'Add 1 column' });
    expect(confirm).toBeDisabled();
    fireEvent.click(within(dialog).getByRole('radio', {
      name: 'Report identifier: Patient <-[encounter]- DiagnosticReport',
    }));
    const technicalPath = within(dialog).getAllByText('Technical path details')[1]!;
    fireEvent.click(technicalPath);
    expect(within(dialog).getAllByText('This path permits rows with no matching related record.').length).toBeGreaterThan(0);
    fireEvent.click(confirm);

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'report-via-encounter', form: 'VALUE' },
      title: 'Report identifier',
    }]));
    const routeRequest = fetch.mock.calls
      .map(([, init]) => JSON.parse(String(init?.body)) as Record<string, unknown>)
      .find((request) => request.source !== undefined);
    expect(routeRequest).toMatchObject({
      outputId: 'table-1',
      source: { kind: 'FIELD', candidateId: 'report-id' },
    });
    expect(routeRequest).not.toHaveProperty('occurrenceId');
    expect(JSON.stringify(routeRequest)).not.toContain('edgeId');
  });
});
