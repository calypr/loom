// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { z } from 'zod';
import { createLoomClient } from '../../../api';
import { LoomProvider } from '../../../react';
import type {
  ConstructionChoice,
  FeatureCatalogBrowseResponse,
  FeatureCatalogItem,
  FeatureCatalogSection,
} from '../../../types';
import {
  featureCatalogBrowseResponseSchema,
  featureCatalogItemSchema,
  featureCatalogSectionSchema,
} from '../../../types';
import { ConceptCatalog, type CatalogRouteContext } from './ConceptCatalog';

const choiceOption = (
  form: 'VALUE' | 'ALL',
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
  candidateId: string,
  resourceType = 'Patient',
  path = 'id',
): ConstructionChoice => ({
  choiceId: `choice-${candidateId}`,
  route: [],
  presentation: {
    summary: `${resourceType} field`,
    facts: [{ label: 'FHIR path', value: `${resourceType}.${path}` }],
  },
  source: {
    kind: 'FIELD',
    candidateId,
    nodeId: resourceType === 'Patient' ? 'patient-node' : 'observation-node',
    resourceType,
    path,
    cardinality: 'optional_one',
  },
  options: [choiceOption('VALUE', 'DEFAULT')],
});

const semanticChoice = (
  conceptId: string,
  bindingId: string,
  resourceType: string,
  options: ConstructionChoice['options'] = [choiceOption('VALUE', 'DEFAULT')],
): ConstructionChoice => ({
  choiceId: `choice-${conceptId}`,
  route: [],
  presentation: {
    summary: `${resourceType} concept`,
    facts: [{ label: 'Code path', value: `${resourceType}.code.coding[]` }],
  },
  source: {
    kind: 'SEMANTIC',
    conceptId,
    bindingId,
    candidateId: `candidate-${conceptId}`,
    nodeId: resourceType === 'Patient' ? 'patient-node' : 'observation-node',
    resourceType,
    sourcePath: 'code.coding[]',
    fieldPath: `root.code.coding[]`,
    valueSelector: 'valueQuantity.value',
    valueScope: 'KEY_ITEM',
    logicalType: 'decimal',
    ruleVersion: '1',
    schemaVersion: 1,
    cardinality: 'optional_one',
  },
  options,
});

const readiness = (
  status: FeatureCatalogItem['readiness']['status'],
): FeatureCatalogItem['readiness'] => ({
  status,
  code: status,
  message: status === 'READY'
    ? 'This feature is ready to add.'
    : status === 'READY_WITH_WARNING'
      ? 'This feature is ready to add with a warning.'
      : status === 'NEEDS_MAPPING'
        ? 'This feature needs a terminology mapping.'
        : 'This feature is unsupported for the selected output.',
});

const fieldItem = (
  featureId: string,
  title: string,
  candidateId: string,
  options: {
    readonly resourceType?: string;
    readonly readiness?: FeatureCatalogItem['readiness'];
    readonly constructionChoice?: ConstructionChoice;
    readonly sourceEvidence?: string;
  } = {},
): FeatureCatalogItem => featureCatalogItemSchema.parse({
  kind: 'DIRECT_FIELD',
  featureId,
  title,
  description: 'A direct primitive value available for this table.',
  resourceType: options.resourceType ?? 'Patient',
  valueType: 'string',
  cardinality: 'optional_one',
  occurrences: 81,
  readiness: options.readiness ?? readiness('READY'),
  source: { kind: 'FIELD', candidateId },
  sourceDetails: [{
    label: 'FHIR path',
    value: options.sourceEvidence ?? `${options.resourceType ?? 'Patient'}.id`,
  }],
  ...(options.constructionChoice
    ? { constructionChoice: options.constructionChoice }
    : {}),
});

const semanticItem = (
  featureId: string,
  title: string,
  conceptId: string,
  bindingId: string,
  options: {
    readonly resourceType?: string;
    readonly readiness?: FeatureCatalogItem['readiness'];
    readonly constructionChoice?: ConstructionChoice;
    readonly sourceEvidence?: string;
  } = {},
): FeatureCatalogItem => featureCatalogItemSchema.parse({
  kind: 'SEMANTIC_FEATURE',
  featureId,
  title,
  description: 'A concept with a schema-defined value for this table.',
  resourceType: options.resourceType ?? 'Observation',
  valueType: 'decimal',
  cardinality: 'semantic_value',
  occurrences: 91,
  readiness: options.readiness ?? readiness('READY'),
  source: { kind: 'SEMANTIC', conceptId, bindingId },
  sourceDetails: [{
    label: 'FHIR path',
    value: options.sourceEvidence ?? 'Observation.code.coding[]',
  }],
  ...(options.constructionChoice
    ? { constructionChoice: options.constructionChoice }
    : {}),
});

const browseResponse = (
  section: FeatureCatalogSection,
  entries: ReadonlyArray<FeatureCatalogItem>,
  contextToken: string,
  options: {
    readonly buildId?: string;
    readonly sourceAvailability?: 'unknown' | 'verified' | 'unproven';
    readonly nextCursor?: string;
  } = {},
): FeatureCatalogBrowseResponse => featureCatalogBrowseResponseSchema.parse({
  contextToken,
  buildId: options.buildId ?? `${section.toLowerCase()}-build`,
  state: 'complete',
  sourceAvailability: options.sourceAvailability ?? 'verified',
  section,
  entries,
  ...(options.nextCursor ? { nextCursor: options.nextCursor } : {}),
});

const jsonBodySchema = z.object({}).passthrough();
const browseRequestSchema = z.object({
  section: featureCatalogSectionSchema,
  query: z.string().optional(),
  cursor: z.string().optional(),
  nodeId: z.string().optional(),
  limit: z.number().optional(),
}).passthrough();

const readBody = (init: RequestInit | undefined) =>
  jsonBodySchema.parse(JSON.parse(String(init?.body ?? '{}')));

const readBrowseRequest = (init: RequestInit | undefined) =>
  browseRequestSchema.parse(readBody(init));

const jsonResponse = (value: unknown, status = 200) => new Response(JSON.stringify(value), {
  status,
  headers: { 'content-type': 'application/json' },
});

const createFetch = (
  resolveBrowse: (request: ReturnType<typeof readBrowseRequest>) => FeatureCatalogBrowseResponse,
) => vi.fn<typeof globalThis.fetch>().mockImplementation(async (input, init) => {
  if (String(input).endsWith('/construction-choices')) {
    return jsonResponse({
      snapshotToken: 'snapshot-a',
      outputId: 'patients',
      complete: true,
      truncated: false,
      choices: [],
    });
  }
  return jsonResponse(resolveBrowse(readBrowseRequest(init)));
});

const renderCatalog = (
  fetch: typeof globalThis.fetch,
  options: {
    readonly onAddSelected?: (selections: ReadonlyArray<{ constructionChoice: { choiceId: string; form: string }; title?: string }>) => Promise<void>;
    readonly routeContext?: CatalogRouteContext;
    readonly resourceType?: string;
  } = {},
) => {
  const onAddSelected = options.onAddSelected ?? vi.fn().mockResolvedValue(undefined);
  render(
    <LoomProvider client={createLoomClient({ fetch })}>
      <ConceptCatalog
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-a"
        outputId="patients"
        rowRoot="Patient"
        resourceType={options.resourceType}
        routeContext={options.routeContext}
        onAddSelected={onAddSelected}
      />
    </LoomProvider>,
  );
  return onAddSelected;
};

const featureCatalogCalls = (fetch: ReturnType<typeof createFetch>) =>
  fetch.mock.calls.filter(([input]) => String(input).endsWith('/feature-catalog'));

const browseBody = (call: ReturnType<typeof featureCatalogCalls>[number]) =>
  readBrowseRequest(call[1]);

describe('ConceptCatalog', () => {
  it('loads each section from the feature catalog, ignores Builder candidates, and keeps selections across search', async () => {
    const serverField = fieldItem('field:patient-id', 'Server-owned patient id', 'candidate-from-server', {
      sourceEvidence: 'Patient.id',
    });
    const serverConcept = semanticItem(
      'semantic:glucose:binding-1',
      'Server-owned glucose concept',
      'glucose-concept',
      'glucose-binding',
    );
    const fetch = createFetch((request) => {
      if (request.section === 'FIELDS') {
        return browseResponse('FIELDS', [serverField], 'fields-context');
      }
      if (request.section === 'CONCEPTS') {
        return browseResponse('CONCEPTS', [serverConcept], 'concepts-context');
      }
      return browseResponse('NEEDS_REVIEW', [], 'review-context');
    });
    renderCatalog(fetch);

    expect(await screen.findByRole('checkbox', { name: 'Select Server-owned patient id' })).toBeEnabled();
    expect(await screen.findByRole('checkbox', { name: 'Select Server-owned glucose concept' })).toBeEnabled();
    expect(screen.queryByText('Raw coding candidate')).not.toBeInTheDocument();
    expect(screen.queryByText('Patient.code.coding[]')).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Needs review' })).toBeInTheDocument();

    await waitFor(() => expect(featureCatalogCalls(fetch)).toHaveLength(3));
    expect(featureCatalogCalls(fetch).map(([input]) => String(input))).toEqual([
      expect.stringContaining('/feature-catalog'),
      expect.stringContaining('/feature-catalog'),
      expect.stringContaining('/feature-catalog'),
    ]);
    expect(featureCatalogCalls(fetch).map(browseBody).map((body) => body.section).sort()).toEqual([
      'CONCEPTS',
      'FIELDS',
      'NEEDS_REVIEW',
    ]);
    expect(featureCatalogCalls(fetch).every((call) => browseBody(call).limit === 50)).toBe(true);
    expect(fetch.mock.calls.every(([input]) => !String(input).endsWith('/semantic-inventory'))).toBe(true);

    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Server-owned patient id' }));
    const selected = within(screen.getByRole('complementary')).getByText('Server-owned patient id');
    expect(selected).toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search features by field name, concept, or code' }), {
      target: { value: 'serum' },
    });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Search' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));

    await waitFor(() => expect(featureCatalogCalls(fetch)).toHaveLength(6));
    expect(featureCatalogCalls(fetch).slice(3).map(browseBody).every((body) => body.query === 'serum')).toBe(true);
    expect(within(screen.getByRole('complementary')).getByText('Server-owned patient id')).toBeInTheDocument();
  });

  it('passes returned field and semantic identities into route resolution', async () => {
    const field = fieldItem('field:observation-id', 'Observation identifier', 'returned-candidate-id', {
      resourceType: 'Observation',
    });
    const concept = semanticItem(
      'semantic:glucose:returned-binding',
      'Blood glucose',
      'returned-concept-id',
      'returned-binding-id',
    );
    const fetch = createFetch((request) => {
      if (request.section === 'FIELDS') {
        return browseResponse('FIELDS', [field], 'field-route-context');
      }
      if (request.section === 'CONCEPTS') {
        return browseResponse('CONCEPTS', [concept], 'semantic-route-context', {
          buildId: 'semantic-route-build',
        });
      }
      return browseResponse('NEEDS_REVIEW', [], 'review-route-context');
    });
    renderCatalog(fetch, {
      resourceType: 'Observation',
      routeContext: { occurrenceId: 'occurrence-observations', nodeId: 'observation-node' },
    });

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Observation identifier' }));
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Blood glucose' }));
    expect(browseBody(featureCatalogCalls(fetch).find((call) => browseBody(call).section === 'FIELDS')!).nodeId)
      .toBe('observation-node');
    expect(browseBody(featureCatalogCalls(fetch).find((call) => browseBody(call).section === 'CONCEPTS')!))
      .not.toHaveProperty('nodeId');
    fireEvent.click(screen.getByRole('button', { name: 'Add 2 selected features' }));

    await waitFor(() => expect(fetch.mock.calls.filter(([input]) => String(input).endsWith('/construction-choices'))).toHaveLength(2));
    const routeCalls = fetch.mock.calls.filter(([input]) => String(input).endsWith('/construction-choices'));
    const bodies = routeCalls.map((call) => readBody(call[1]));
    expect(bodies.map((body) => body.occurrenceId)).toEqual([
      'occurrence-observations',
      'occurrence-observations',
    ]);
    expect(bodies.map((body) => body.source)).toEqual(expect.arrayContaining([
      { kind: 'FIELD', candidateId: 'returned-candidate-id' },
      {
        kind: 'SEMANTIC',
        contextToken: 'semantic-route-context',
        buildId: 'semantic-route-build',
        conceptId: 'returned-concept-id',
        bindingId: 'returned-binding-id',
      },
    ]));
    expect(await screen.findByRole('dialog', { name: 'Choose output forms' })).toBeInTheDocument();
  });

  it('keeps unavailable concepts and every Needs review item non-selectable', async () => {
    const unmapped = semanticItem(
      'semantic:unmapped:binding',
      'Unmapped diagnosis',
      'unmapped-concept',
      'unmapped-binding',
      { readiness: readiness('NEEDS_MAPPING') },
    );
    const reviewOnly = semanticItem(
      'semantic:review:binding',
      'Source requiring review',
      'review-concept',
      'review-binding',
      { readiness: readiness('READY') },
    );
    const fetch = createFetch((request) => {
      if (request.section === 'CONCEPTS') {
        return browseResponse('CONCEPTS', [unmapped], 'concept-context');
      }
      if (request.section === 'NEEDS_REVIEW') {
        return browseResponse('NEEDS_REVIEW', [reviewOnly], 'review-context', {
          sourceAvailability: 'unproven',
        });
      }
      return browseResponse('FIELDS', [], 'field-context');
    });
    renderCatalog(fetch);

    expect(await screen.findByRole('checkbox', { name: 'Select Unmapped diagnosis' })).toBeDisabled();
    expect(await screen.findByRole('checkbox', { name: 'Select Source requiring review' })).toBeDisabled();
    expect(screen.getByRole('region', { name: 'Needs review' })).toHaveTextContent('could not verify every retained source collection');
    expect(screen.getAllByText('This feature needs a terminology mapping.')).toHaveLength(2);
    expect(within(screen.getByRole('complementary')).getByText('0')).toBeInTheDocument();
  });

  it('pages fields and concepts independently without dropping selections', async () => {
    const fieldOne = fieldItem('field:one', 'Field page one', 'candidate-one');
    const fieldTwo = fieldItem('field:two', 'Field page two', 'candidate-two');
    const conceptOne = semanticItem('semantic:one', 'Concept page one', 'concept-one', 'binding-one');
    const conceptTwo = semanticItem('semantic:two', 'Concept page two', 'concept-two', 'binding-two');
    const fetch = createFetch((request) => {
      if (request.section === 'FIELDS') {
        return request.cursor
          ? browseResponse('FIELDS', [fieldTwo], 'fields-context')
          : browseResponse('FIELDS', [fieldOne], 'fields-context', { nextCursor: 'field-cursor-two' });
      }
      if (request.section === 'CONCEPTS') {
        return request.cursor
          ? browseResponse('CONCEPTS', [conceptTwo], 'concepts-context')
          : browseResponse('CONCEPTS', [conceptOne], 'concepts-context', { nextCursor: 'concept-cursor-two' });
      }
      return browseResponse('NEEDS_REVIEW', [], 'review-context');
    });
    renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Field page one' }));
    const fields = screen.getByRole('region', { name: 'Fields on Patient' });
    const concepts = screen.getByRole('region', { name: 'Concepts across the dataset' });
    fireEvent.click(within(fields).getByRole('button', { name: 'Next' }));
    expect(await within(fields).findByText('Field page two')).toBeInTheDocument();
    expect(within(concepts).getByText('Concept page one')).toBeInTheDocument();

    fireEvent.click(within(concepts).getByRole('button', { name: 'Next' }));
    expect(await within(concepts).findByText('Concept page two')).toBeInTheDocument();
    expect(within(fields).getByText('Field page two')).toBeInTheDocument();
    expect(within(screen.getByRole('complementary')).getByText('Field page one')).toBeInTheDocument();

    const requests = featureCatalogCalls(fetch).map(browseBody);
    expect(requests).toEqual(expect.arrayContaining([
      expect.objectContaining({ section: 'FIELDS', cursor: 'field-cursor-two' }),
      expect.objectContaining({ section: 'CONCEPTS', cursor: 'concept-cursor-two' }),
    ]));
  });

  it('restarts a section from page one when its catalog cursor becomes stale', async () => {
    const originalField = fieldItem('field:original', 'Original field page one', 'candidate-original');
    const refreshedField = fieldItem('field:refreshed', 'Refreshed field page one', 'candidate-refreshed');
    let fieldFirstPageRequests = 0;
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      const request = readBrowseRequest(init);
      if (request.section !== 'FIELDS') {
        return jsonResponse(browseResponse(request.section, [], `${request.section.toLowerCase()}-context`));
      }
      if (request.cursor) {
        return jsonResponse({
          error: {
            code: 'STALE_CATALOG_CURSOR',
            message: 'restart catalog search with an empty cursor',
          },
        }, 409);
      }
      fieldFirstPageRequests += 1;
      return jsonResponse(fieldFirstPageRequests === 1
        ? browseResponse('FIELDS', [originalField], 'fields-context-a', {
            buildId: 'fields-build-a',
            nextCursor: 'stale-field-cursor',
          })
        : browseResponse('FIELDS', [refreshedField], 'fields-context-b', {
            buildId: 'fields-build-b',
          }));
    });
    renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Original field page one' }));
    const fields = screen.getByRole('region', { name: 'Fields on Patient' });
    fireEvent.click(within(fields).getByRole('button', { name: 'Next' }));

    expect(await within(fields).findByText('Refreshed field page one')).toBeInTheDocument();
    expect(within(fields).queryByRole('alert')).not.toBeInTheDocument();
    expect(within(screen.getByRole('complementary')).queryByText('Original field page one')).not.toBeInTheDocument();
    expect(screen.getByText('The feature catalog changed. Review and select the features again.')).toBeInTheDocument();
    const fieldRequests = featureCatalogCalls(fetch)
      .map(browseBody)
      .filter((request) => request.section === 'FIELDS');
    expect(fieldRequests).toEqual([
      expect.not.objectContaining({ cursor: expect.anything() }),
      expect.objectContaining({ cursor: 'stale-field-cursor' }),
      expect.not.objectContaining({ cursor: expect.anything() }),
    ]);
  });

  it('does not clear selections when independent sections return different context tokens', async () => {
    let releaseConcepts: (response: Response) => void = () => undefined;
    let releaseReview: (response: Response) => void = () => undefined;
    const conceptsPending = new Promise<Response>((resolve) => {
      releaseConcepts = resolve;
    });
    const reviewPending = new Promise<Response>((resolve) => {
      releaseReview = resolve;
    });
    const field = fieldItem('field:patient-id', 'Patient identifier', 'candidate-patient-id');
    const concept = semanticItem('semantic:glucose', 'Blood glucose', 'glucose', 'binding-glucose');
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input, init) => {
      const request = readBrowseRequest(init);
      if (request.section === 'FIELDS') {
        return jsonResponse(browseResponse('FIELDS', [field], 'fields-token'));
      }
      if (request.section === 'CONCEPTS') return conceptsPending;
      if (request.section === 'NEEDS_REVIEW') return reviewPending;
      throw new Error(`Unexpected section ${request.section}`);
    });
    renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Patient identifier' }));
    releaseConcepts(jsonResponse(browseResponse('CONCEPTS', [concept], 'concepts-token')));
    releaseReview(jsonResponse(browseResponse('NEEDS_REVIEW', [], 'review-token')));
    expect(await screen.findByRole('checkbox', { name: 'Select Blood glucose' })).toBeEnabled();
    await waitFor(() => expect(within(screen.getByRole('complementary')).getByText('Patient identifier')).toBeInTheDocument());
  });

  it('keeps the direct-add path and the output-form selection dialog', async () => {
    const direct = fieldItem('field:patient-id', 'Patient identifier', 'candidate-patient-id', {
      constructionChoice: fieldChoice('candidate-patient-id'),
    });
    const directFetch = createFetch((request) => request.section === 'FIELDS'
      ? browseResponse('FIELDS', [direct], 'fields-context')
      : request.section === 'CONCEPTS'
        ? browseResponse('CONCEPTS', [], 'concepts-context')
        : browseResponse('NEEDS_REVIEW', [], 'review-context'));
    const directAdd = vi.fn().mockResolvedValue(undefined);
    renderCatalog(directFetch, { onAddSelected: directAdd });

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Patient identifier' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));
    await waitFor(() => expect(directAdd).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'choice-candidate-patient-id', form: 'VALUE' },
      title: 'Patient identifier',
    }]));
    expect(directFetch.mock.calls.every(([input]) => !String(input).endsWith('/construction-choices'))).toBe(true);
  });

  it('shows backend source evidence only inside Source details and confirms a selected form', async () => {
    const concept = semanticItem(
      'semantic:hemoglobin:binding',
      'Hemoglobin A1c',
      'hemoglobin-concept',
      'hemoglobin-binding',
      {
        resourceType: 'Patient',
        constructionChoice: semanticChoice(
          'hemoglobin-concept',
          'hemoglobin-binding',
          'Patient',
          [choiceOption('VALUE', 'DEFAULT'), choiceOption('ALL', 'REQUIRES_DECISION')],
        ),
        sourceEvidence: 'Observation.code.coding[]',
      },
    );
    const fetch = createFetch((request) => request.section === 'CONCEPTS'
      ? browseResponse('CONCEPTS', [concept], 'concepts-context')
      : request.section === 'FIELDS'
        ? browseResponse('FIELDS', [], 'fields-context')
        : browseResponse('NEEDS_REVIEW', [], 'review-context'));
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    renderCatalog(fetch, { onAddSelected });

    expect(await screen.findByRole('checkbox', { name: 'Select Hemoglobin A1c' })).toBeEnabled();
    const sourceEvidence = screen.getByText('Observation.code.coding[]');
    expect(sourceEvidence.closest('details')).not.toHaveAttribute('open');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Hemoglobin A1c' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));
    const dialog = await screen.findByRole('dialog', { name: 'Choose output forms' });
    fireEvent.click(within(dialog).getByText('Source details'));
    expect(within(dialog).getByText('Patient.code.coding[]')).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Hemoglobin A1c: LIST · PRESERVING · ALL' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'choice-hemoglobin-concept', form: 'ALL' },
      title: 'Hemoglobin A1c',
    }]));
  });
});
