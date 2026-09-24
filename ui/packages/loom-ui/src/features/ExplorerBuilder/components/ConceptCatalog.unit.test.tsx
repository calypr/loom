// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
import { pivotFamilies, type ConfiguredPivotContext } from '../pivotFamilies';

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
  code?: string,
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
    ...(code ? { system: 'urn:measurements', code, keySelector: 'code.coding[]', ruleHint: 'CODED_VALUE_V1' } : {}),
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
    readonly coverage?: FeatureCatalogItem['coverage'];
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
  coverage: options.coverage ?? { state: 'INDEXED', rowsWithValue: 81 },
  readiness: options.readiness ?? readiness('READY'),
  source: { kind: 'FIELD', candidateId },
    sourceDetails: [{
      label: 'FHIR path',
      value: options.sourceEvidence ?? `${options.resourceType ?? 'Patient'}.id`,
    }],
    constructionChoice: options.constructionChoice ?? fieldChoice(
      candidateId,
      options.resourceType ?? 'Patient',
    ),
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
    readonly occurrences?: number;
    readonly sourceRecords?: number;
    readonly coverage?: FeatureCatalogItem['coverage'];
    readonly pivotFamily?: { readonly id: string; readonly title: string; readonly relationship: string; readonly form: 'VALUE' | 'ALL'; readonly multiCodeRowsPossible: boolean };
    readonly code?: string;
  } = {},
): FeatureCatalogItem => featureCatalogItemSchema.parse({
  kind: 'SEMANTIC_FEATURE',
  featureId,
  title,
  description: 'A concept with a schema-defined value for this table.',
  resourceType: options.resourceType ?? 'Observation',
  valueType: 'decimal',
  cardinality: 'optional_one',
  occurrences: options.occurrences ?? 91,
  coverage: options.coverage ?? { state: 'INDEXED', rowsWithValue: options.sourceRecords ?? 91 },
  ...(options.sourceRecords === undefined ? {} : { sourceRecords: options.sourceRecords }),
  ...(options.pivotFamily ? { pivotFamily: { ...options.pivotFamily, code: options.code ?? 'fixture-code' } } : {}),
  readiness: options.readiness ?? readiness('READY'),
  source: { kind: 'SEMANTIC', conceptId, bindingId },
    sourceDetails: [{
      label: 'FHIR path',
      value: options.sourceEvidence ?? 'Observation.code.coding[]',
    }],
    constructionChoice: options.constructionChoice ?? semanticChoice(
      conceptId,
      bindingId,
      options.resourceType ?? 'Observation',
      undefined,
      options.code,
    ),
});

const browseResponse = (
  section: FeatureCatalogSection,
  entries: ReadonlyArray<FeatureCatalogItem>,
  contextToken: string,
  options: {
    readonly buildId?: string;
    readonly sourceAvailability?: 'unknown' | 'verified' | 'unproven';
    readonly state?: FeatureCatalogBrowseResponse['state'];
    readonly nextCursor?: string;
  } = {},
): FeatureCatalogBrowseResponse => featureCatalogBrowseResponseSchema.parse({
  contextToken,
  buildId: options.buildId ?? `${section.toLowerCase()}-build`,
  state: options.state ?? 'complete',
  sourceAvailability: options.sourceAvailability ?? 'verified',
  section,
  entries,
  ...(options.nextCursor ? { nextCursor: options.nextCursor } : {}),
});

const runningBrowseResponse = (
  section: FeatureCatalogSection,
): FeatureCatalogBrowseResponse => featureCatalogBrowseResponseSchema.parse({
  contextToken: '',
  buildId: '',
  state: 'running',
  sourceAvailability: 'unknown',
  section,
  entries: [],
});

const jsonBodySchema = z.object({}).passthrough();
const browseRequestSchema = z.object({
  section: featureCatalogSectionSchema,
  outputId: z.string().optional(),
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
    readonly onAnalyzePivot?: (familyId: string, selections: ReadonlyArray<{ constructionChoice: { choiceId: string; form: string }; title?: string }>) => Promise<{ commandId: string; draftDigest: string; sampledRows: number; rowsWithMultipleCodes: number; columns: ReadonlyArray<{ code: string; label: string; rowsWithValue: number }> }>;
    readonly routeContext?: CatalogRouteContext;
    readonly resourceType?: string;
    readonly outputId?: string;
    readonly configuredPivot?: ConfiguredPivotContext;
  } = {},
) => {
  const onAddSelected = options.onAddSelected ?? vi.fn().mockResolvedValue(undefined);
  render(
    <LoomProvider client={createLoomClient({ fetch })}>
      <ConceptCatalog
        project="project-a"
        explorerId="explorer-a"
        snapshotToken="snapshot-a"
        outputId={options.outputId ?? 'patients'}
        rowRoot="Patient"
        resourceType={options.resourceType}
        routeContext={options.routeContext}
        configuredPivot={options.configuredPivot}
        onAnalyzePivot={options.onAnalyzePivot}
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
  it('marks only the exact saved code and route as added, then selects the remaining code', async () => {
    const family = {
      id: 'pivot-family-measurement', title: 'Observation code values',
      relationship: 'From related Observation records (1 connection)',
      form: 'ALL' as const, multiCodeRowsPossible: true,
    };
    const route = [{
      edgeId: 'patient-observation-edge', fromNodeId: 'patient-node', toNodeId: 'observation-node',
      fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient',
      storageDirection: 'INBOUND' as const, matchMode: 'OPTIONAL' as const,
    }];
    const items = ['height', 'weight'].map((code) => semanticItem(`semantic:${code}`, code, code, 'measurement-binding', {
      code, pivotFamily: family, sourceRecords: 5,
      constructionChoice: { ...semanticChoice(code, 'measurement-binding', 'Observation', [choiceOption('ALL', 'DEFAULT')], code), route },
    }));
    const configuredPivot: ConfiguredPivotContext = {
      route: { occurrenceId: 'base', resourceType: 'Patient', children: [{
        occurrenceId: 'observation-occurrence', resourceType: 'Observation',
        catalogEdgeId: 'patient-observation-edge', relationship: 'subject_Patient',
      }] },
      columns: [{
        column: 'height_column', label: 'Height', occurrenceId: 'observation-occurrence',
        source: { kind: 'codedValue', lookup: {
          projectionMode: 'ALL', key: { system: 'urn:measurements', code: 'height' },
          binding: {
            keyPath: 'code.coding[]', systemPath: 'system', codePath: 'code',
            valuePath: 'valueQuantity.value', choiceArms: ['valueQuantity'], logicalType: 'decimal',
          },
        } },
      }],
    };
    expect(pivotFamilies(items, {
      ...configuredPivot,
      route: { ...configuredPivot.route, children: [{
        occurrenceId: 'observation-occurrence', resourceType: 'Observation',
        catalogEdgeId: 'another-edge', relationship: 'subject_Patient',
      }] },
    })[0]?.codes.every((code) => !code.configured)).toBe(true);
    const fetch = createFetch((request) => request.section === 'CONCEPTS'
      ? browseResponse('CONCEPTS', items, 'concepts-context')
      : browseResponse(request.section, [], `${request.section}-context`));
    const onAnalyzePivot = vi.fn().mockResolvedValue({
      commandId: 'remaining-code', draftDigest: 'draft-a', sampledRows: 2,
      rowsWithMultipleCodes: 0, columns: [{ code: 'weight', label: 'weight', rowsWithValue: 1 }],
    });
    renderCatalog(fetch, { configuredPivot, onAnalyzePivot });

    const group = await screen.findByRole('group', { name: /^Code set Observation code values:/ });
    expect(within(group).getByText(/1 already added/)).toBeInTheDocument();
    fireEvent.click(within(group).getByRole('button', { name: 'Choose codes' }));
    expect(within(group).getByRole('checkbox', { name: 'Select height as a column' })).toBeDisabled();
    expect(within(group).getByRole('checkbox', { name: 'Select weight as a column' })).toHaveProperty('checked', false);
    fireEvent.click(within(group).getByRole('checkbox', { name: 'Select weight as a column' }));
    fireEvent.click(within(group).getByRole('button', { name: 'Check row coverage' }));
    await waitFor(() => expect(onAnalyzePivot).toHaveBeenCalledWith('pivot-family-measurement', [
      { constructionChoice: { choiceId: 'choice-weight', form: 'ALL' }, title: 'weight' },
    ]));
  });

  it('adds an observed code family as an atomic list-preserving pivot selection', async () => {
    const family = {
      id: 'pivot-family-measurement',
      title: 'Observation code values',
      relationship: 'From related Observation records (1 connection)',
      form: 'ALL' as const,
      multiCodeRowsPossible: true,
    };
    const all = [choiceOption('VALUE', 'REQUIRES_DECISION'), choiceOption('ALL', 'DEFAULT')];
    const height = semanticItem('semantic:height', 'Height', 'height', 'height-binding', {
      code: 'height', pivotFamily: family, sourceRecords: 12,
      constructionChoice: semanticChoice('height', 'height-binding', 'Observation', all, 'height'),
    });
    const weight = semanticItem('semantic:weight', 'Weight', 'weight', 'weight-binding', {
      code: 'weight', pivotFamily: family, sourceRecords: 9,
      constructionChoice: semanticChoice('weight', 'weight-binding', 'Observation', all, 'weight'),
    });
    const fetch = createFetch((request) => request.section === 'CONCEPTS'
      ? browseResponse('CONCEPTS', [height, weight], 'concepts-context')
      : browseResponse(request.section, [], `${request.section}-context`));
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    const onAnalyzePivot = vi.fn().mockResolvedValue({
      commandId: 'proposal-command', draftDigest: 'draft-a', sampledRows: 2,
      rowsWithMultipleCodes: 1,
      columns: [
        { code: 'height', label: 'Height', rowsWithValue: 1 },
        { code: 'weight', label: 'Weight', rowsWithValue: 2 },
      ],
    });
    renderCatalog(fetch, { onAddSelected, onAnalyzePivot });

    const group = await screen.findByRole('group', { name: /^Code set Observation code values:/ });
    expect(within(group).getByText('Observation code values · 2 code values')).toBeInTheDocument();
    fireEvent.click(within(group).getByText('Technical source and route'));
    expect(within(group).getByText('Observation.code.coding[]')).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Height' })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Weight' })).not.toBeInTheDocument();
    fireEvent.click(within(group).getByRole('button', { name: 'Choose codes' }));
    expect(within(group).getByRole('checkbox', { name: 'Select Height as a column' })).toHaveProperty('checked', false);
    expect(within(group).getByRole('checkbox', { name: 'Select Weight as a column' })).toHaveProperty('checked', false);
    expect(within(group).getByRole('button', { name: 'Add 0 columns' })).toBeDisabled();
    fireEvent.click(within(group).getByRole('checkbox', { name: 'Select Height as a column' }));
    fireEvent.click(within(group).getByRole('checkbox', { name: 'Select Weight as a column' }));
    fireEvent.click(within(group).getByRole('button', { name: 'Check row coverage' }));
    await waitFor(() => expect(within(group).getByRole('button', { name: 'Add 2 columns' })).toBeEnabled());
    expect(within(group).getByText('2 preview rows checked')).toBeInTheDocument();
    expect(within(group).getByText('Coverage reflects this preview sample, not the whole table.')).toBeInTheDocument();
    expect(within(group).getByText('1 of 2 checked rows have more than one selected value.')).toBeInTheDocument();
    expect(onAnalyzePivot).toHaveBeenCalledWith('pivot-family-measurement', [
      { constructionChoice: { choiceId: 'choice-height', form: 'ALL' }, title: 'Height' },
      { constructionChoice: { choiceId: 'choice-weight', form: 'ALL' }, title: 'Weight' },
    ]);
    fireEvent.click(within(group).getByRole('checkbox', { name: 'Select Weight as a column' }));
    expect(within(group).getByRole('button', { name: 'Add 1 column' })).toBeDisabled();
    fireEvent.click(within(group).getByRole('checkbox', { name: 'Select Weight as a column' }));
    fireEvent.click(within(group).getByRole('button', { name: 'Check row coverage' }));
    await waitFor(() => expect(within(group).getByRole('button', { name: 'Add 2 columns' })).toBeEnabled());
    fireEvent.click(within(group).getByRole('button', { name: 'Add 2 columns' }));
    await waitFor(() => expect(onAddSelected).toHaveBeenCalledTimes(1));
    expect(onAddSelected).toHaveBeenCalledWith([
      { constructionChoice: { choiceId: 'choice-height', form: 'ALL' }, title: 'Height' },
      { constructionChoice: { choiceId: 'choice-weight', form: 'ALL' }, title: 'Weight' },
    ], 'proposal-command', 'draft-a');
  });

  it('shows every code in a multi-code family in one expandable catalog result', async () => {
    const family = {
      id: 'pivot-family-rare',
      title: 'Specimen type values',
      relationship: 'On each Specimen row',
      form: 'VALUE' as const,
      multiCodeRowsPossible: false,
    };
    const tissue = semanticItem('semantic:tissue', 'Tissue', 'tissue', 'type-binding', {
      code: 'tissue', pivotFamily: family, sourceRecords: 12,
    });
    const fluid = semanticItem('semantic:fluid', 'Fluid', 'fluid', 'type-binding', {
      code: 'fluid', pivotFamily: family, sourceRecords: 11,
    });
    const fetch = createFetch((request) => request.section === 'CONCEPTS'
      ? browseResponse('CONCEPTS', [tissue, fluid], 'concepts-context')
      : browseResponse(request.section, [], `${request.section}-context`));
    renderCatalog(fetch, { onAddSelected: vi.fn().mockResolvedValue(undefined), onAnalyzePivot: vi.fn().mockResolvedValue(undefined) });

    const group = await screen.findByRole('group', { name: 'Code set Specimen type values: Tissue + Fluid' });
    expect(within(group).getByText('Tissue + Fluid')).toBeInTheDocument();
    expect(within(group).getByText('Specimen type values · 2 code values')).toBeInTheDocument();
    expect(within(group).getByRole('button', { name: 'Choose codes' })).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(within(group).getByRole('button', { name: 'Choose codes' }));
    expect(within(group).getByRole('checkbox', { name: 'Select Tissue as a column' })).toHaveProperty('checked', false);
    expect(within(group).getByRole('checkbox', { name: 'Select Fluid as a column' })).toHaveProperty('checked', false);
    expect(screen.queryByRole('checkbox', { name: 'Select Tissue' })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Fluid' })).not.toBeInTheDocument();
  });

  it('offers a one-code family as an ordinary selectable concept', async () => {
    const item = semanticItem('semantic:single', 'Specimen category', 'single', 'single-binding', {
      code: 'specimen-category', sourceRecords: 12,
      pivotFamily: { id: 'single-set', title: 'Specimen category values', relationship: 'On this row', form: 'VALUE', multiCodeRowsPossible: false },
    });
    const fetch = createFetch((request) => request.section === 'CONCEPTS'
      ? browseResponse('CONCEPTS', [item], 'concepts-context')
      : browseResponse(request.section, [], `${request.section}-context`));
    const onAnalyzePivot = vi.fn();
    renderCatalog(fetch, { onAnalyzePivot });

    const catalogItem = await screen.findByRole('checkbox', { name: 'Select Specimen category' });
    expect(catalogItem).toBeInTheDocument();
    expect(screen.queryByRole('group', { name: 'Code set Specimen category values' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Choose codes' })).not.toBeInTheDocument();
    fireEvent.click(catalogItem);
    expect(within(screen.getByRole('complementary')).getByText('Specimen category')).toBeInTheDocument();
    expect(onAnalyzePivot).not.toHaveBeenCalled();
  });

  it('does not offer an individually queued code again inside its code set', async () => {
    const family = {
      id: 'measurement-set', title: 'Measurements', relationship: 'On this row',
      form: 'VALUE' as const, multiCodeRowsPossible: true,
    };
    const height = semanticItem('semantic:height', 'Height', 'height', 'height-binding', {
      code: 'height', pivotFamily: family, sourceRecords: 3,
    });
    const weight = semanticItem('semantic:weight', 'Weight', 'weight', 'weight-binding', {
      code: 'weight', pivotFamily: family, sourceRecords: 3,
    });
    const fetch = createFetch((request) => request.section === 'CONCEPTS'
      ? browseResponse('CONCEPTS', request.query?.toLowerCase() === 'height' ? [height] : [height, weight], 'concepts-context')
      : browseResponse(request.section, [], `${request.section}-context`));
    renderCatalog(fetch, {
      onAnalyzePivot: vi.fn().mockResolvedValue({
        commandId: 'weight-only', draftDigest: 'draft-a', sampledRows: 1,
        rowsWithMultipleCodes: 0,
        columns: [{ code: 'weight', label: 'Weight', rowsWithValue: 1 }],
      }),
    });

    await screen.findByRole('tab', { name: /^Concepts\s*2$/ });
    const search = screen.getByRole('searchbox', { name: 'Search features by field name, concept, or code' });
    fireEvent.change(search, { target: { value: 'Height' } });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await screen.findByRole('tab', { name: /^Concepts\s*1$/ });
    const heightChoice = await screen.findByRole('checkbox', { name: 'Select Height' });
    fireEvent.click(heightChoice);
    expect(within(screen.getByRole('complementary')).getByText('Height')).toBeInTheDocument();

    fireEvent.change(search, { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    const codeSetsTab = await screen.findByRole('tab', { name: /^Code sets\s*1$/ });
    fireEvent.click(codeSetsTab);
    const group = within(screen.getByRole('tabpanel', { name: /^Code sets\s*1$/ }))
      .getByRole('group', { name: /^Code set Measurements:/ });
    fireEvent.click(within(group).getByRole('button', { name: 'Choose codes' }));
    expect(within(group).getByRole('checkbox', { name: 'Select Height as a column' })).toBeDisabled();
    expect(within(group).getByText('Selected')).toBeInTheDocument();
    fireEvent.click(within(group).getByRole('checkbox', { name: 'Select Weight as a column' }));
    expect(within(group).getByRole('button', { name: 'Add 1 column' })).toBeDisabled();
  });

  it('shows every available code set in its tab without hiding or duplicating concepts', async () => {
    const individualConcepts = Array.from({ length: 49 }, (_, index) =>
      semanticItem(
        `semantic:individual-${index}`,
        `Individual concept ${index}`,
        `individual-${index}`,
        `individual-binding-${index}`,
      ));
    const familySizes = [5, 4, 2, 1, 1, 1];
    const codeSetItems = familySizes.flatMap((size, familyIndex) => {
      const family = {
        id: `code-set-${familyIndex}`,
        title: `Code set ${familyIndex}`,
        relationship: 'On this table row',
        form: 'VALUE' as const,
        multiCodeRowsPossible: familyIndex < 3,
      };
      return Array.from({ length: size }, (_, codeIndex) => {
        const title = `Code set ${familyIndex} value ${codeIndex}`;
        return semanticItem(
          `semantic:code-set-${familyIndex}-${codeIndex}`,
          title,
          `code-set-${familyIndex}-${codeIndex}`,
          `code-set-binding-${familyIndex}-${codeIndex}`,
          {
            code: `code-${familyIndex}-${codeIndex}`,
            pivotFamily: family,
            sourceRecords: 3,
          },
        );
      });
    });
    expect(individualConcepts.length + codeSetItems.length).toBe(63);
    const fetch = createFetch((request) => request.section === 'CONCEPTS'
      ? browseResponse('CONCEPTS', [...individualConcepts, ...codeSetItems], 'concepts-context')
      : browseResponse(request.section, [], `${request.section}-context`));
    renderCatalog(fetch, {
      onAddSelected: vi.fn().mockResolvedValue(undefined),
      onAnalyzePivot: vi.fn().mockResolvedValue({
        commandId: 'proposal-command',
        draftDigest: 'draft-a',
        sampledRows: 1,
        rowsWithMultipleCodes: 0,
        columns: [],
      }),
    });

    const codeSetsTab = await screen.findByRole('tab', { name: /^Code sets\s*6$/ });
    expect(screen.getByRole('tab', { name: /^All\s*63$/ })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: /^Fields\s*0$/ })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: /^Concepts\s*63$/ })).toBeInTheDocument();
    fireEvent.click(codeSetsTab);

    const codeSetsPanel = screen.getByRole('tabpanel', { name: /^Code sets\s*6$/ });
    expect(codeSetsTab).toHaveAttribute('aria-selected', 'true');
    expect(within(codeSetsPanel).getByRole('heading', { name: 'Code sets for the current table rows' })).toBeInTheDocument();
    expect(within(codeSetsPanel).getAllByTestId('pivot-family-catalog-row')).toHaveLength(3);
    expect(within(codeSetsPanel).getByRole('group', { name: /^Code set Code set 0:/ })).toBeInTheDocument();
    expect(within(codeSetsPanel).getByRole('group', { name: /^Code set Code set 2:/ })).toBeInTheDocument();
    expect(within(codeSetsPanel).getByRole('checkbox', { name: 'Select Code set 3 value 0' })).toBeInTheDocument();
    expect(within(codeSetsPanel).getByRole('checkbox', { name: 'Select Code set 5 value 0' })).toBeInTheDocument();
    expect(within(codeSetsPanel).queryByRole('checkbox', { name: 'Select Individual concept 0' })).not.toBeInTheDocument();
    expect(within(codeSetsPanel).queryByRole('checkbox', { name: 'Select Code set 0 value 0' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: /^Concepts\s*63$/ }));
    const conceptsPanel = screen.getByRole('tabpanel', { name: /^Concepts\s*63$/ });
    expect(within(conceptsPanel).getByRole('checkbox', { name: 'Select Individual concept 0' })).toBeInTheDocument();
    expect(within(conceptsPanel).getAllByTestId('pivot-family-catalog-row')).toHaveLength(3);
    expect(within(conceptsPanel).getByRole('checkbox', { name: 'Select Code set 3 value 0' })).toBeInTheDocument();
    expect(within(conceptsPanel).queryByRole('checkbox', { name: 'Select Code set 0 value 0' })).not.toBeInTheDocument();
  });

  it('shows all code sets inline and keeps grouped codes out of select all', async () => {
    const items = Array.from({ length: 51 }, (_, index) => [
      semanticItem(`semantic:group-${index}-a`, `Group ${index} first value`, `group-${index}-a`, `binding-${index}-a`, {
        code: `first-${index}`, sourceRecords: 3,
        pivotFamily: { id: `group-${index}`, title: `Group ${index}`, relationship: 'On this row', form: 'VALUE', multiCodeRowsPossible: true },
      }),
      semanticItem(`semantic:group-${index}-b`, `Group ${index} second value`, `group-${index}-b`, `binding-${index}-b`, {
        code: `second-${index}`, sourceRecords: 2,
        pivotFamily: { id: `group-${index}`, title: `Group ${index}`, relationship: 'On this row', form: 'VALUE', multiCodeRowsPossible: true },
      }),
    ]).flat();
    const field = fieldItem('field:select-all', 'Select all fixture field', 'candidate-select-all');
    const fetch = createFetch((request) => request.section === 'CONCEPTS'
      ? browseResponse('CONCEPTS', items, 'concepts-context')
      : request.section === 'FIELDS'
        ? browseResponse('FIELDS', [field], 'fields-context')
        : browseResponse('NEEDS_REVIEW', [], 'review-context'));
    renderCatalog(fetch, { onAddSelected: vi.fn().mockResolvedValue(undefined), onAnalyzePivot: vi.fn().mockResolvedValue(undefined) });

    expect(await screen.findByRole('group', { name: /^Code set Group 0:/ })).toBeInTheDocument();
    expect(screen.getByRole('group', { name: /^Code set Group 50:/ })).toBeInTheDocument();
    expect(screen.getAllByTestId('pivot-family-catalog-row')).toHaveLength(51);
    expect(screen.getByText('Choose code values inside each code set.')).toBeInTheDocument();
    const selectAll = screen.getByRole('checkbox', { name: 'Select all individual columns' });
    expect(selectAll).toHaveProperty('checked', false);
    fireEvent.click(selectAll);
    expect(selectAll).toHaveProperty('checked', true);
    expect(within(screen.getByRole('complementary')).getByText('Select all fixture field')).toBeInTheDocument();
    const firstFamily = screen.getByRole('group', { name: /^Code set Group 0:/ });
    fireEvent.click(within(firstFamily).getByRole('button', { name: 'Choose codes' }));
    expect(within(firstFamily).getByRole('checkbox', { name: 'Select Group 0 first value as a column' })).toHaveProperty('checked', false);
    fireEvent.click(screen.getByRole('tab', { name: /Concepts/ }));
    expect(screen.getByRole('tab', { name: /Concepts\s*102/ })).toHaveAttribute('aria-selected', 'true');
  });

  it('switches the visible catalog section without dropping selections', async () => {
    const field = fieldItem('field:tab-patient-id', 'Tab patient identifier', 'candidate-tab-patient-id');
    const concept = semanticItem('semantic:tab-code', 'Tab diagnosis code', 'tab-diagnosis', 'tab-diagnosis-binding');
    const fetch = createFetch((request) => request.section === 'FIELDS'
      ? browseResponse('FIELDS', [field], 'fields-context')
      : request.section === 'CONCEPTS'
        ? browseResponse('CONCEPTS', [concept], 'concepts-context')
        : browseResponse('NEEDS_REVIEW', [], 'review-context'));
    renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Tab patient identifier' }));
    fireEvent.click(screen.getByRole('tab', { name: /Concepts/ }));
    expect(await screen.findByRole('checkbox', { name: 'Select Tab diagnosis code' })).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Tab patient identifier' })).not.toBeInTheDocument();
    expect(within(screen.getByRole('complementary')).getByText('Tab patient identifier')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: /All/ }));
    expect(screen.getByRole('checkbox', { name: 'Select Tab patient identifier' })).toHaveProperty('checked', true);
    expect(screen.getByRole('checkbox', { name: 'Select Tab diagnosis code' })).toBeInTheDocument();
  });

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
    expect(screen.queryByRole('region', { name: 'Needs review' })).not.toBeInTheDocument();

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
    expect(featureCatalogCalls(fetch).every((call) => browseBody(call).limit === 500)).toBe(true);
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

  it('keeps the normal catalog unscoped across dataset resources', async () => {
    const fetch = createFetch((request) => browseResponse(request.section, [], `${request.section.toLowerCase()}-context`));
    renderCatalog(fetch);

    await waitFor(() => expect(featureCatalogCalls(fetch)).toHaveLength(3));
    expect(featureCatalogCalls(fetch).map(browseBody).every((body) => !('resourceType' in body))).toBe(true);
    expect(screen.getByRole('region', { name: 'Concepts across the dataset' })).toBeInTheDocument();
    expect(screen.getByText('Search available fields and concepts for Patient rows.')).toBeInTheDocument();
  });

  it('labels occurrences as matches and shows source records only when available', async () => {
    const legacy = semanticItem('semantic:legacy', 'Legacy concept', 'legacy-concept', 'legacy-binding', { occurrences: 7 });
    const counted = semanticItem('semantic:counted', 'Counted concept', 'counted-concept', 'counted-binding', { occurrences: 8, sourceRecords: 3 });
    const fetch = createFetch((request) => request.section === 'CONCEPTS'
      ? browseResponse('CONCEPTS', [legacy, counted], 'concepts-context')
      : browseResponse(request.section, [], `${request.section.toLowerCase()}-context`));
    renderCatalog(fetch);

    const concepts = await screen.findByRole('region', { name: 'Concepts across the dataset' });
    expect(within(concepts).getByText('7 matches')).toBeInTheDocument();
    expect(within(concepts).getByText('8 matches · 3 records')).toBeInTheDocument();
    expect(within(concepts).queryByText('7 records')).not.toBeInTheDocument();
  });

  it('scopes graph catalog sections to the selected resource node', async () => {
    const fetch = createFetch((request) => browseResponse(request.section, [], `${request.section.toLowerCase()}-context`));
    renderCatalog(fetch, {
      resourceType: 'Observation',
      routeContext: { occurrenceId: 'occurrence-observations', nodeId: 'observation-node' },
    });

    await waitFor(() => expect(featureCatalogCalls(fetch)).toHaveLength(3));
    const requests = featureCatalogCalls(fetch).map(browseBody);
    expect(requests.every((body) => body.resourceType === 'Observation')).toBe(true);
    expect(requests.find((body) => body.section === 'FIELDS')).toEqual(expect.objectContaining({
      nodeId: 'observation-node',
    }));
    expect(screen.getByRole('region', { name: 'Concepts on Observation' })).toBeInTheDocument();
    expect(screen.getByText('Search available fields and concepts on the selected Observation graph node.')).toBeInTheDocument();
  });

  it('shows one preparing state, polls a running catalog, and withholds every candidate until all sections finish', async () => {
    const field = fieldItem('field:ready-after-build', 'Ready after build', 'candidate-ready-after-build');
    let fieldRequests = 0;
    let releaseReview: (response: Response) => void = () => undefined;
    const reviewPending = new Promise<Response>((resolve) => {
      releaseReview = resolve;
    });
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      const request = readBrowseRequest(init);
      if (request.section === 'FIELDS') {
        fieldRequests += 1;
        return jsonResponse(fieldRequests === 1
          ? runningBrowseResponse('FIELDS')
          : browseResponse('FIELDS', [field], 'fields-context'));
      }
      if (request.section === 'NEEDS_REVIEW') return reviewPending;
      return jsonResponse(browseResponse('CONCEPTS', [], 'concepts-context'));
    });
    renderCatalog(fetch);

    expect(await screen.findByRole('status')).toHaveTextContent('Preparing available columns…');
    expect(screen.getAllByRole('status')).toHaveLength(1);
    expect(screen.queryByRole('checkbox', { name: 'Select Ready after build' })).not.toBeInTheDocument();
    expect(screen.queryByText(/populated columns available/)).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select all individual columns' })).not.toBeInTheDocument();
    await waitFor(() => expect(fieldRequests).toBe(2), { timeout: 5_000 });
    expect(screen.queryByRole('checkbox', { name: 'Select Ready after build' })).not.toBeInTheDocument();
    expect(featureCatalogCalls(fetch).map(browseBody).filter((request) => request.section === 'FIELDS'))
      .toHaveLength(2);

    releaseReview(jsonResponse(browseResponse('NEEDS_REVIEW', [], 'review-context')));
    expect(await screen.findByRole('checkbox', { name: 'Select Ready after build' })).toBeEnabled();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('backs off repeated running responses from one to two, four, then five seconds', async () => {
    vi.useFakeTimers();
    try {
      let fieldRequests = 0;
      const fetch = createFetch((request) => {
        if (request.section === 'FIELDS') {
          fieldRequests += 1;
          return runningBrowseResponse('FIELDS');
        }
        return browseResponse(request.section, [], `${request.section.toLowerCase()}-context`);
      });
      renderCatalog(fetch);
      const advance = async (milliseconds: number) => {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(milliseconds);
        });
      };

      await advance(0);
      expect(fieldRequests).toBe(1);
      await advance(999);
      expect(fieldRequests).toBe(1);
      await advance(1);
      expect(fieldRequests).toBe(2);
      await advance(1_999);
      expect(fieldRequests).toBe(2);
      await advance(1);
      expect(fieldRequests).toBe(3);
      await advance(3_999);
      expect(fieldRequests).toBe(3);
      await advance(1);
      expect(fieldRequests).toBe(4);
      await advance(4_999);
      expect(fieldRequests).toBe(4);
      await advance(1);
      expect(fieldRequests).toBe(5);
      await advance(4_999);
      expect(fieldRequests).toBe(5);
      await advance(1);
      expect(fieldRequests).toBe(6);
    } finally {
      vi.clearAllTimers();
      vi.useRealTimers();
    }
  });

  it('cancels a scheduled poll and restarts the delay when the catalog context changes', async () => {
    vi.useFakeTimers();
    try {
      const fieldRequests: Record<string, number> = {};
      const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
        const request = readBrowseRequest(init);
        if (request.section === 'FIELDS') {
          const outputId = request.outputId ?? 'missing';
          const requestCount = (fieldRequests[outputId] ?? 0) + 1;
          fieldRequests[outputId] = requestCount;
          return jsonResponse(request.outputId === 'encounters' && requestCount > 1
            ? browseResponse('FIELDS', [fieldItem('field:encounter', 'Encounter field', 'candidate-encounter')], 'encounter-fields')
            : runningBrowseResponse('FIELDS'));
        }
        return jsonResponse(browseResponse(request.section, [], `${request.section.toLowerCase()}-${request.outputId}`));
      });
      const client = createLoomClient({ fetch });
      const viewForOutput = (outputId: string) => (
        <LoomProvider client={client}>
          <ConceptCatalog
            project="project-a"
            explorerId="explorer-a"
            snapshotToken="snapshot-a"
            outputId={outputId}
            rowRoot="Patient"
            onAddSelected={vi.fn().mockResolvedValue(undefined)}
          />
        </LoomProvider>
      );
      const view = render(viewForOutput('patients'));
      const advance = async (milliseconds: number) => {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(milliseconds);
        });
      };

      await advance(0);
      expect(fieldRequests.patients).toBe(1);
      view.rerender(viewForOutput('encounters'));
      await advance(0);
      expect(fieldRequests.encounters).toBe(1);
      await advance(999);
      expect(fieldRequests.patients).toBe(1);
      expect(fieldRequests.encounters).toBe(1);
      await advance(1);
      expect(fieldRequests.patients).toBe(1);
      expect(fieldRequests.encounters).toBe(2);
      expect(screen.getByRole('checkbox', { name: 'Select Encounter field' })).toBeEnabled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('aborts in-flight catalog requests and reloads when the output changes', async () => {
    const oldSignals: Array<AbortSignal | null | undefined> = [];
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (_input, init) => {
      const request = readBrowseRequest(init);
      if (request.outputId === 'patients') {
        oldSignals.push(init?.signal);
        return jsonResponse(request.section === 'FIELDS'
          ? runningBrowseResponse('FIELDS')
          : browseResponse(request.section, [], `${request.section.toLowerCase()}-old`));
      }
      return jsonResponse(browseResponse(request.section, [], `${request.section.toLowerCase()}-new`));
    });
    const client = createLoomClient({ fetch });
    const viewForOutput = (outputId: string) => (
      <LoomProvider client={client}>
        <ConceptCatalog
          project="project-a"
          explorerId="explorer-a"
          snapshotToken="snapshot-a"
          outputId={outputId}
          rowRoot="Patient"
          onAddSelected={vi.fn().mockResolvedValue(undefined)}
        />
      </LoomProvider>
    );
    const view = render(viewForOutput('patients'));
    await waitFor(() => expect(oldSignals).toHaveLength(3));
    expect(await screen.findByRole('status')).toHaveTextContent('Preparing available columns…');

    view.rerender(viewForOutput('encounters'));
    await waitFor(() => expect(featureCatalogCalls(fetch)).toHaveLength(6));
    expect(oldSignals.every((signal) => signal?.aborted)).toBe(true);
    expect(featureCatalogCalls(fetch).map(browseBody).map((request) => request.outputId)).toEqual([
      'patients', 'patients', 'patients', 'encounters', 'encounters', 'encounters',
    ]);
    expect(featureCatalogCalls(fetch).map(browseBody).filter((request) =>
      request.outputId === 'patients' && request.section === 'FIELDS',
    )).toHaveLength(1);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('shows failed and unverified catalog responses with a retry and no candidates', async () => {
    const field = fieldItem('field:must-not-show', 'Questionable field', 'candidate-questionable');
    const fieldWithoutDefault = featureCatalogItemSchema.parse({
      ...field,
      constructionChoice: undefined,
    });
    let fieldAttempts = 0;
    const fetch = createFetch((request) => {
      if (request.section !== 'FIELDS') {
        return browseResponse(request.section, [], `${request.section.toLowerCase()}-context`);
      }
      fieldAttempts += 1;
      if (fieldAttempts === 1) {
        return browseResponse('FIELDS', [field], 'fields-failed', { state: 'failed' });
      }
      if (fieldAttempts === 2) {
        return browseResponse('FIELDS', [field], 'fields-unverified', { sourceAvailability: 'unproven' });
      }
      if (fieldAttempts === 3) {
        return browseResponse('FIELDS', [fieldWithoutDefault], 'fields-without-default');
      }
      return browseResponse('FIELDS', [field], 'fields-verified');
    });
    renderCatalog(fetch);

    expect(await screen.findByRole('alert')).toHaveTextContent('feature catalog is failed');
    expect(screen.queryByRole('checkbox', { name: 'Select Questionable field' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry catalog load' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('could not verify all source data');
    expect(screen.queryByRole('checkbox', { name: 'Select Questionable field' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry catalog load' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('without its compiler default');
    expect(screen.queryByRole('checkbox', { name: 'Select Questionable field' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry catalog load' }));
    expect(await screen.findByRole('checkbox', { name: 'Select Questionable field' })).toBeEnabled();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('adds the exact attached graph occurrence choices without searching routes again', async () => {
    const route = {
      edgeId: 'patient-observation',
      fromNodeId: 'patient-node',
      toNodeId: 'observation-node',
      fromResourceType: 'Patient',
      toResourceType: 'Observation',
      relationship: 'subject_Patient',
      storageDirection: 'INBOUND' as const,
      matchMode: 'OPTIONAL' as const,
    };
    const field = fieldItem('field:observation-id', 'Observation identifier', 'returned-candidate-id', {
      resourceType: 'Observation',
      constructionChoice: {
        ...fieldChoice('returned-candidate-id', 'Observation'),
        choiceId: 'choice-field-on-occurrence',
        route: [route],
      },
    });
    const concept = semanticItem(
      'semantic:glucose:returned-binding',
      'Blood glucose',
      'returned-concept-id',
      'returned-binding-id',
      {
        constructionChoice: {
          ...semanticChoice('returned-concept-id', 'returned-binding-id', 'Observation'),
          choiceId: 'choice-semantic-on-occurrence',
          route: [route],
        },
      },
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
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    renderCatalog(fetch, {
      onAddSelected,
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

    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([
      {
        constructionChoice: { choiceId: 'choice-field-on-occurrence', form: 'VALUE' },
        title: 'Observation identifier',
      },
      {
        constructionChoice: { choiceId: 'choice-semantic-on-occurrence', form: 'VALUE' },
        title: 'Blood glucose',
      },
    ]));
    expect(fetch.mock.calls.every(([input]) => !String(input).endsWith('/construction-choices'))).toBe(true);
  });

  it('hides unpopulated, unsupported, and review-only catalog entries', async () => {
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
    const unpopulated = semanticItem(
      'semantic:pending:binding',
      'Not yet populated',
      'pending-concept',
      'pending-binding',
      { coverage: { state: 'PENDING' } },
    );
    const fetch = createFetch((request) => {
      if (request.section === 'CONCEPTS') {
        return browseResponse('CONCEPTS', [unmapped, unpopulated], 'concept-context');
      }
      if (request.section === 'NEEDS_REVIEW') {
        return browseResponse('NEEDS_REVIEW', [reviewOnly], 'review-context');
      }
      return browseResponse('FIELDS', [], 'field-context');
    });
    renderCatalog(fetch);

    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument());
    expect(screen.queryByRole('checkbox', { name: 'Select Unmapped diagnosis' })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Not yet populated' })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Source requiring review' })).not.toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Needs review' })).not.toBeInTheDocument();
    expect(screen.queryByText('This feature needs a terminology mapping.')).not.toBeInTheDocument();
    expect(within(screen.getByRole('complementary')).getByText('0')).toBeInTheDocument();
  });

  it('selects every loaded available column and excludes entries without populated choices', async () => {
    const field = fieldItem('field:all-id', 'All fields id', 'candidate-all-id');
    const concept = semanticItem('semantic:all-concept', 'All concepts code', 'all-concept', 'all-binding');
    const pending = semanticItem('semantic:pending-route', 'Unpopulated concept', 'pending-concept', 'pending-binding', {
      coverage: { state: 'PENDING' },
    });
    const reviewOnly = semanticItem(
      'semantic:all-review',
      'Review-only concept',
      'all-review',
      'all-review-binding',
      { readiness: readiness('READY') },
    );
    const fetch = createFetch((request) => request.section === 'FIELDS'
      ? browseResponse('FIELDS', [field], 'fields-context')
      : request.section === 'CONCEPTS'
        ? browseResponse('CONCEPTS', [concept, pending], 'concepts-context')
        : browseResponse('NEEDS_REVIEW', [reviewOnly], 'review-context'));
    renderCatalog(fetch);

    const selectAll = await screen.findByRole('checkbox', { name: 'Select all individual columns' });
    expect(selectAll).toBeEnabled();
    fireEvent.click(selectAll);

    const selectedFeatures = within(screen.getByRole('complementary'));
    expect(selectedFeatures.getByText('All fields id')).toBeInTheDocument();
    expect(selectedFeatures.getByText('All concepts code')).toBeInTheDocument();
    expect(selectedFeatures.queryByText('Unpopulated concept')).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Unpopulated concept' })).not.toBeInTheDocument();
    expect(selectedFeatures.queryByText('Review-only concept')).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select Review-only concept' })).not.toBeInTheDocument();
    expect(selectAll).toHaveProperty('checked', true);

    fireEvent.click(selectAll);
    expect(selectedFeatures.queryByText('All fields id')).not.toBeInTheDocument();
    expect(selectedFeatures.queryByText('All concepts code')).not.toBeInTheDocument();
    expect(selectedFeatures.getByText('0')).toBeInTheDocument();
  });

  it('shows an indeterminate select-all state and fills the remaining loaded columns', async () => {
    const first = fieldItem('field:partial-first', 'Partial first', 'candidate-partial-first');
    const second = fieldItem('field:partial-second', 'Partial second', 'candidate-partial-second');
    const fetch = createFetch((request) => request.section === 'FIELDS'
      ? browseResponse('FIELDS', [first, second], 'fields-context')
      : browseResponse(request.section, [], `${request.section.toLowerCase()}-context`));
    renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Partial first' }));
    const selectAll = screen.getByRole('checkbox', { name: 'Select all individual columns' });
    expect(selectAll).toHaveAttribute('aria-checked', 'mixed');
    expect(selectAll).toHaveProperty('indeterminate', true);

    fireEvent.click(selectAll);
    expect(screen.getByRole('checkbox', { name: 'Select Partial first' })).toHaveProperty('checked', true);
    expect(screen.getByRole('checkbox', { name: 'Select Partial second' })).toHaveProperty('checked', true);
    expect(selectAll).toHaveProperty('checked', true);
  });

  it('clears only the current filtered results and preserves unrelated selections', async () => {
    const original = fieldItem('field:original', 'Original column', 'candidate-original');
    const filtered = fieldItem('field:filtered', 'Filtered column', 'candidate-filtered');
    const fetch = createFetch((request) => request.section === 'FIELDS'
      ? browseResponse('FIELDS', [request.query ? filtered : original], 'fields-context')
      : browseResponse(request.section, [], `${request.section.toLowerCase()}-context`));
    renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Original column' }));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search features by field name, concept, or code' }), {
      target: { value: 'filtered' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByRole('checkbox', { name: 'Select Filtered column' })).toBeEnabled();

    const selectAll = screen.getByRole('checkbox', { name: 'Select all individual columns' });
    fireEvent.click(selectAll);
    expect(within(screen.getByRole('complementary')).getByText('Original column')).toBeInTheDocument();
    expect(within(screen.getByRole('complementary')).getByText('Filtered column')).toBeInTheDocument();

    fireEvent.click(selectAll);
    const selectedFeatures = within(screen.getByRole('complementary'));
    expect(selectedFeatures.getByText('Original column')).toBeInTheDocument();
    expect(selectedFeatures.queryByText('Filtered column')).not.toBeInTheDocument();
  });

  it('reports the selection limit and selects loaded columns in visible order', async () => {
    const fields = Array.from({ length: 101 }, (_, index) => fieldItem(
      `field:capacity-${index}`,
      `Capacity column ${String(index + 1).padStart(3, '0')}`,
      `candidate-capacity-${index}`,
    ));
    const fetch = createFetch((request) => request.section === 'FIELDS'
      ? browseResponse('FIELDS', fields, 'fields-context')
      : browseResponse(request.section, [], `${request.section.toLowerCase()}-context`));
    renderCatalog(fetch);

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select all individual columns' }));
    const selectedFeatures = within(screen.getByRole('complementary'));
    expect(selectedFeatures.getByText('100')).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Select Capacity column 001' })).toHaveProperty('checked', true);
    expect(screen.getByRole('checkbox', { name: 'Select Capacity column 100' })).toHaveProperty('checked', true);
    expect(screen.getByRole('checkbox', { name: 'Select Capacity column 101' })).toHaveProperty('checked', false);
    expect(screen.getByRole('status')).toHaveTextContent(
      'The 100-column selection limit was reached. Select fewer columns before adding more.',
    );
  });

  it('loads every cursor page into one list without exposing pagination controls', async () => {
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

    const fields = await screen.findByRole('region', { name: 'Fields on Patient' });
    const concepts = await screen.findByRole('region', { name: 'Concepts across the dataset' });
    expect(await within(fields).findByText('Field page one')).toBeInTheDocument();
    expect(await within(fields).findByText('Field page two')).toBeInTheDocument();
    expect(within(concepts).getByText('Concept page one')).toBeInTheDocument();
    expect(await within(concepts).findByText('Concept page two')).toBeInTheDocument();
    expect(within(fields).getByText('2 available')).toBeInTheDocument();
    expect(within(concepts).getByText('2 available')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Previous' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Next' })).not.toBeInTheDocument();

    fireEvent.click(within(fields).getByRole('checkbox', { name: 'Select Field page one' }));
    expect(within(screen.getByRole('complementary')).getByText('Field page one')).toBeInTheDocument();

    const requests = featureCatalogCalls(fetch).map(browseBody);
    expect(requests).toEqual(expect.arrayContaining([
      expect.objectContaining({ section: 'FIELDS', cursor: 'field-cursor-two' }),
      expect.objectContaining({ section: 'CONCEPTS', cursor: 'concept-cursor-two' }),
    ]));
  });

  it('restarts automatic loading once when a catalog cursor becomes stale', async () => {
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

    const fields = await screen.findByRole('region', { name: 'Fields on Patient' });
    expect(await within(fields).findByText('Refreshed field page one')).toBeInTheDocument();
    expect(within(fields).queryByText('Original field page one')).not.toBeInTheDocument();
    expect(within(fields).queryByRole('alert')).not.toBeInTheDocument();
    const fieldRequests = featureCatalogCalls(fetch)
      .map(browseBody)
      .filter((request) => request.section === 'FIELDS');
    expect(fieldRequests).toEqual([
      expect.not.objectContaining({ cursor: expect.anything() }),
      expect.objectContaining({ cursor: 'stale-field-cursor' }),
      expect.not.objectContaining({ cursor: expect.anything() }),
    ]);
  });

  it('withholds fast sections until the slowest section has completed', async () => {
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

    expect(await screen.findByRole('status')).toHaveTextContent('Preparing available columns…');
    expect(screen.queryByRole('checkbox', { name: 'Select Patient identifier' })).not.toBeInTheDocument();
    releaseConcepts(jsonResponse(browseResponse('CONCEPTS', [concept], 'concepts-token')));
    await waitFor(() => expect(screen.queryByRole('checkbox', { name: 'Select Patient identifier' })).not.toBeInTheDocument());
    releaseReview(jsonResponse(browseResponse('NEEDS_REVIEW', [], 'review-token')));
    expect(await screen.findByRole('checkbox', { name: 'Select Patient identifier' })).toBeEnabled();
    expect(await screen.findByRole('checkbox', { name: 'Select Blood glucose' })).toBeEnabled();
  });

  it('uses an attached multi-hop choice and its default form for normal add', async () => {
    const route = {
      edgeId: 'patient-observation',
      fromNodeId: 'patient-node',
      toNodeId: 'observation-node',
      fromResourceType: 'Patient',
      toResourceType: 'Observation',
      relationship: 'subject_Patient',
      storageDirection: 'INBOUND' as const,
      matchMode: 'OPTIONAL' as const,
    };
    const secondRoute = {
      ...route,
      edgeId: 'observation-encounter',
      fromNodeId: 'observation-node',
      toNodeId: 'encounter-node',
      fromResourceType: 'Observation',
      toResourceType: 'Encounter',
      relationship: 'encounter',
      storageDirection: 'OUTBOUND' as const,
    };
    const direct = fieldItem('field:patient-id', 'Patient identifier', 'candidate-patient-id', {
      resourceType: 'Encounter',
      coverage: { state: 'VERIFIED' },
      constructionChoice: {
        ...fieldChoice('candidate-patient-id', 'Encounter'),
        choiceId: 'choice-multi-hop-default',
        route: [route, secondRoute],
        options: [choiceOption('VALUE', 'DEFAULT'), choiceOption('ALL', 'REQUIRES_DECISION')],
      },
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
      constructionChoice: { choiceId: 'choice-multi-hop-default', form: 'VALUE' },
      title: 'Patient identifier',
    }]));
    expect(screen.queryByRole('dialog', { name: 'Choose column sources and output forms' })).not.toBeInTheDocument();
    expect(directFetch.mock.calls.every(([input]) => !String(input).endsWith('/construction-choices'))).toBe(true);
  });

  it('uses select-all defaults and avoids per-item route searches', async () => {
    const field = fieldItem('field:bulk-field', 'Bulk field', 'candidate-bulk-field', {
      constructionChoice: {
        ...fieldChoice('candidate-bulk-field'),
        choiceId: 'choice-bulk-field',
      },
    });
    const concept = semanticItem('semantic:bulk-concept', 'Bulk concept', 'bulk-concept', 'bulk-binding', {
      constructionChoice: {
        ...semanticChoice('bulk-concept', 'bulk-binding', 'Observation'),
        choiceId: 'choice-bulk-concept',
        options: [choiceOption('VALUE', 'DEFAULT'), choiceOption('ALL', 'REQUIRES_DECISION')],
      },
    });
    const fetch = createFetch((request) => request.section === 'FIELDS'
      ? browseResponse('FIELDS', [field], 'fields-context')
      : request.section === 'CONCEPTS'
        ? browseResponse('CONCEPTS', [concept], 'concepts-context')
        : browseResponse('NEEDS_REVIEW', [], 'review-context'));
    const onAddSelected = vi.fn().mockResolvedValue(undefined);
    renderCatalog(fetch, { onAddSelected });

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select all individual columns' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 2 selected features' }));
    await waitFor(() => expect(onAddSelected).toHaveBeenCalledWith([
      { constructionChoice: { choiceId: 'choice-bulk-field', form: 'VALUE' }, title: 'Bulk field' },
      { constructionChoice: { choiceId: 'choice-bulk-concept', form: 'VALUE' }, title: 'Bulk concept' },
    ]));
    expect(fetch.mock.calls.every(([input]) => !String(input).endsWith('/construction-choices'))).toBe(true);
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
    fireEvent.click(screen.getByRole('button', { name: 'Advanced choices' }));
    const dialog = await screen.findByRole('dialog', { name: 'Choose column sources and output forms' });
    expect(within(dialog).getByText(/Choose where each column gets its values/)).toBeInTheDocument();
    expect(within(dialog).queryByText('Route:')).not.toBeInTheDocument();
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
