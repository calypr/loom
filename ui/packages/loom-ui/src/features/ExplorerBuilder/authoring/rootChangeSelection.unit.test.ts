import { describe, expect, it } from 'vitest';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from './model';
import {
  parseCatalogRootRouteSelection,
  resolveCatalogRootRouteSelection,
  sameRootChangeRequestIdentity,
  startingRootChoices,
} from './rootChangeSelection';

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot',
  generation: 'generation',
  routePolicy: { allowRepeatedEdges: true, allowSelfLoops: false, maxSteps: 4 },
  nodes: [
    { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 18 },
    { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 18 },
    { nodeId: 'condition', resourceType: 'Condition', rowRootEligible: true, populated: true, documentCount: 8 },
    { nodeId: 'unpopulated', resourceType: 'Medication', rowRootEligible: true, populated: false, documentCount: 0 },
    { nodeId: 'unrelated', resourceType: 'Encounter', rowRootEligible: true, populated: true, documentCount: 3 },
    { nodeId: 'one-way', resourceType: 'AllergyIntolerance', rowRootEligible: true, populated: true, documentCount: 2 },
  ],
  edges: [
    { edgeId: 'patient-observation', fromNodeId: 'patient', toNodeId: 'observation', label: 'subject_Patient' },
    { edgeId: 'observation-patient', fromNodeId: 'observation', toNodeId: 'patient', label: 'subject_Patient' },
    { edgeId: 'observation-condition', fromNodeId: 'observation', toNodeId: 'condition', label: 'focus_Observation' },
    { edgeId: 'condition-observation', fromNodeId: 'condition', toNodeId: 'observation', label: 'focus_Observation' },
    { edgeId: 'condition-patient', fromNodeId: 'condition', toNodeId: 'patient', label: 'subject_Patient' },
    { edgeId: 'patient-medication', fromNodeId: 'patient', toNodeId: 'unpopulated', label: 'medication' },
    { edgeId: 'patient-one-way', fromNodeId: 'patient', toNodeId: 'one-way', label: 'allergy' },
  ],
};

const plainPatient: DraftTable = {
  outputId: 'patients',
  tabId: 'patients',
  title: 'Patients',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'patients', title: 'Patients' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [],
  },
};

const routeToken = (payload: unknown): string => {
  const encoded = btoa(JSON.stringify(payload))
    .replace(/=/g, '')
    .replace(/\+/g, '-')
    .replace(/\//g, '_');
  return `row-change-route:v1:${encoded}`;
};

describe('catalog-root selection contract', () => {
  it('returns only connected, populated, eligible catalog roots within the configured depth', () => {
    const choices = startingRootChoices(plainPatient, catalog);
    expect(choices.map(({ resourceType, key, current }) => ({ resourceType, key, current }))).toEqual([
      { resourceType: 'Patient', key: 'current', current: true },
      { resourceType: 'Observation', key: 'node:observation', current: false },
      { resourceType: 'Condition', key: 'node:condition', current: false },
    ]);
    expect(choices[1].relationship).toBe('Patient → Subject → Observation');
    expect(choices[2].relationship).toBe('Patient → Subject → Observation → Focus → Condition');
    expect(choices.some((choice) => choice.resourceType === 'AllergyIntolerance')).toBe(false);
  });

  it('does not imply one route label when multiple shortest reversible paths reach a root', () => {
    const ambiguousCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      edges: [
        ...catalog.edges,
        { edgeId: 'patient-observation-focus', fromNodeId: 'patient', toNodeId: 'observation', label: 'focus_Patient' },
        { edgeId: 'focus-patient', fromNodeId: 'observation', toNodeId: 'patient', label: 'focus_Patient' },
      ],
    };
    const observation = startingRootChoices(plainPatient, ambiguousCatalog)
      .find((choice) => choice.resourceType === 'Observation');

    expect(observation).toMatchObject({
      key: 'node:observation',
      selection: { kind: 'catalog-node', nodeId: 'observation' },
      current: false,
    });
    expect(observation?.relationship).toBeUndefined();
  });

  it('decodes only the versioned route-selection token shape', () => {
    expect(parseCatalogRootRouteSelection(routeToken({
      edgeIds: ['patient-observation'],
      anchorOccurrenceId: 'base',
    }))).toEqual({ edgeIds: ['patient-observation'], anchorOccurrenceId: 'base' });
    expect(parseCatalogRootRouteSelection('row-change-route:v1:%%%')).toBeUndefined();
    expect(parseCatalogRootRouteSelection(routeToken({
      edgeIds: ['patient-observation'],
      anchorOccurrenceId: 'base',
      label: 'untrusted',
    }))).toBeUndefined();
    expect(parseCatalogRootRouteSelection(routeToken({
      edgeIds: [],
      anchorOccurrenceId: 'base',
    }))).toBeUndefined();
  });

  it('resolves a current catalog path to readable labels and rejects stale or mismatched choices', () => {
    const token = routeToken({ edgeIds: ['patient-observation'], anchorOccurrenceId: 'base' });
    const resolved = resolveCatalogRootRouteSelection(
      token,
      catalog,
      plainPatient,
      'observation',
      true,
    );
    expect(resolved?.pathLabel).toBe('Patient → Subject → Observation');
    expect(resolved?.anchorLabel).toBe('Patient rows');
    expect(resolveCatalogRootRouteSelection(token, catalog, plainPatient, 'observation', false)).toBeUndefined();
    expect(resolveCatalogRootRouteSelection(token, catalog, plainPatient, 'condition', true)).toBeUndefined();
    expect(resolveCatalogRootRouteSelection(
      routeToken({ edgeIds: ['removed-edge'], anchorOccurrenceId: 'base' }),
      catalog,
      plainPatient,
      'observation',
      true,
    )).toBeUndefined();
    expect(resolveCatalogRootRouteSelection(
      routeToken({ edgeIds: ['observation-condition'], anchorOccurrenceId: 'base' }),
      catalog,
      plainPatient,
      'condition',
      true,
    )).toBeUndefined();
  });
});

it('treats a different request epoch as a different root-change identity', () => {
  const identity = {
    epoch: 1,
    project: 'project',
    explorerId: 'explorer',
    outputId: 'patients',
    snapshotToken: 'snapshot',
    draftVersion: 3,
    draftDigest: 'digest',
  };
  expect(sameRootChangeRequestIdentity(identity, { ...identity })).toBe(true);
  expect(sameRootChangeRequestIdentity(identity, { ...identity, epoch: 2 })).toBe(false);
});
