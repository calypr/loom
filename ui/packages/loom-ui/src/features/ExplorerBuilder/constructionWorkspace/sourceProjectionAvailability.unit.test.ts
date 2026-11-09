import { describe, expect, it } from 'vitest';
import type { ConstructionCapabilitiesResponse, ConstructionStageDescriptor } from '../../../types';
import { sourceProjectionAvailability } from './sourceProjectionAvailability';

const groupedStages = [
  { id: 'source_projection', capabilities: [] },
  { id: 'grouped', capabilities: [] },
] as unknown as ReadonlyArray<ConstructionStageDescriptor>;

const summary = (
  overrides: Partial<NonNullable<ConstructionCapabilitiesResponse['sourceProjectionAvailability']>>,
): NonNullable<ConstructionCapabilitiesResponse['sourceProjectionAvailability']> => ({
  stageId: 'grouped',
  supported: false,
  ...overrides,
});

describe('sourceProjectionAvailability', () => {
  it('shows the server reason when starting-record fields are unavailable', () => {
    const availability = sourceProjectionAvailability(
      groupedStages,
      summary({ reasonCode: 'GROUPED_ROWS_DROP_SOURCE', reason: 'Grouped rows no longer carry their starting record.' }),
      'grouped',
    );
    expect(availability.available).toBe(false);
    expect(availability.reason).toBe('Grouped rows no longer carry their starting record.');
  });

  it('names an unknown server reason code rather than hiding it', () => {
    const availability = sourceProjectionAvailability(
      groupedStages,
      summary({ reasonCode: 'SOURCE_PROJECTION_ROUTE_UNPROVEN' }),
      'grouped',
    );
    expect(availability.available).toBe(false);
    expect(availability.reason).toBe(
      'Loom reported that this is unavailable (SOURCE_PROJECTION_ROUTE_UNPROVEN) without an explanation.',
    );
  });

  it('keeps the shipped wording when Loom reports neither a code nor a reason', () => {
    const availability = sourceProjectionAvailability(groupedStages, summary({}), 'grouped');
    expect(availability.available).toBe(false);
    expect(availability.reason).toBe(
      'Starting-record fields are not available for the current row shape.',
    );
  });

  it('still reports the stale-stage mismatch separately from an unsupported check', () => {
    const availability = sourceProjectionAvailability(
      groupedStages,
      summary({ stageId: 'source_projection', reason: 'Grouped rows no longer carry their starting record.' }),
      'grouped',
    );
    expect(availability.available).toBe(false);
    expect(availability.reason).toBe(
      'The starting-record field check does not match the current table stage. Reload the table and try again.',
    );
  });
});
