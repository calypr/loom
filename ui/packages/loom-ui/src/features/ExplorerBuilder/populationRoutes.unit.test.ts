import { describe, expect, it } from 'vitest';
import type { ConstructionRouteStep } from '../../types';
import {
  matchesSavedPopulationRoute,
  sameConstructionRoute,
} from './populationRoutes';

const route: ReadonlyArray<ConstructionRouteStep> = [{
  edgeId: 'patient-specimen-subject',
  fromNodeId: 'patient-node',
  toNodeId: 'specimen-node',
  fromResourceType: 'Patient',
  toResourceType: 'Specimen',
  relationship: 'subject',
  storageDirection: 'INBOUND',
  matchMode: 'OPTIONAL',
}];

describe('population route identity', () => {
  it('honors pinned saved edge identity and direction while allowing legacy unpinned routes', () => {
    const saved = [{ resourceType: 'Specimen', relationship: 'subject', catalogEdgeId: route[0]!.edgeId, storageDirection: 'INBOUND' as const }];
    expect(matchesSavedPopulationRoute(route, saved)).toBe(true);
    expect(matchesSavedPopulationRoute([{ ...route[0]!, edgeId: 'parallel-edge' }], saved)).toBe(false);
    expect(matchesSavedPopulationRoute([{ ...route[0]!, storageDirection: 'OUTBOUND' }], saved)).toBe(false);
    expect(matchesSavedPopulationRoute(route, [{ ...saved[0]!, catalogEdgeId: undefined }])).toBe(true);
    expect(matchesSavedPopulationRoute([{ ...route[0]!, storageDirection: 'OUTBOUND' }], [{ ...saved[0]!, storageDirection: undefined }])).toBe(true);
    expect(matchesSavedPopulationRoute(route, [{ ...saved[0]!, catalogEdgeId: undefined, storageDirection: 'OUTBOUND' }])).toBe(false);
    expect(matchesSavedPopulationRoute([{ ...route[0]!, edgeId: 'parallel-edge' }], [{ ...saved[0]!, storageDirection: undefined }])).toBe(false);
    expect(matchesSavedPopulationRoute([], [])).toBe(true);
    expect(matchesSavedPopulationRoute(route, [])).toBe(false);
  });

  it('requires the exact compiler-proved route when preserving a connection', () => {
    expect(sameConstructionRoute(route, route)).toBe(true);
    expect(sameConstructionRoute(route, [{
      ...route[0]!,
      storageDirection: 'OUTBOUND',
    }])).toBe(false);
  });

  it('matches the route projection persisted in an authored table', () => {
    expect(matchesSavedPopulationRoute(route, [{
      resourceType: 'Specimen',
      relationship: 'subject',
    }])).toBe(true);
    expect(matchesSavedPopulationRoute(route, [{
      resourceType: 'Specimen',
      relationship: 'encounter',
    }])).toBe(false);
  });
});
