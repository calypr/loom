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
