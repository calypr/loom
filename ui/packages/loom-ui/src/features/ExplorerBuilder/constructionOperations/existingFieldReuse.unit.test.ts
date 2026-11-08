import { describe, expect, it } from 'vitest';
import type { Construction, ConstructionStageDescriptor } from '../../../types';
import { existingColumnForFieldPath } from './existingFieldReuse';

const column = (id: string, label: string): ConstructionStageDescriptor['columns'][number] => ({
  id,
  name: id,
  label,
  type: 'string',
});

const projection = (columnId: string, fieldPath: string): NonNullable<Construction['sourceProjections']>[number] => ({
  columnId,
  occurrenceId: `occ-${columnId}`,
  fieldPath,
  fhirType: 'string',
  logicalType: 'string',
  label: fieldPath,
});

describe('existingColumnForFieldPath', () => {
  it('offers the single existing column that already carries the exact field path', () => {
    const reuse = existingColumnForFieldPath({
      sourceProjections: [projection('col-status', 'root.status'), projection('col-age', 'root.age')],
      columns: [column('col-status', 'Status'), column('age-id', 'Age')],
      fieldPath: 'root.status',
    });
    expect(reuse).toEqual({ columnId: 'col-status', label: 'Status', fieldPath: 'root.status' });
  });

  it('offers nothing when the construction never projected this field', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [projection('col-status', 'root.status')],
      columns: [column('col-status', 'Status')],
      fieldPath: 'root.note.text',
    })).toBeUndefined();
  });

  it('offers nothing for a stale projection whose column is no longer on the stage', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [projection('col-status', 'root.status')],
      columns: [column('age-id', 'Age')],
      fieldPath: 'root.status',
    })).toBeUndefined();
  });

  it('refuses to pick between two live columns carrying the same field path', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [projection('col-a', 'root.status'), projection('col-b', 'root.status')],
      columns: [column('col-a', 'Status'), column('col-b', 'Status (copy)')],
      fieldPath: 'root.status',
    })).toBeUndefined();
  });

  it('never matches on label or display text', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [projection('col-note', 'root.comment')],
      columns: [column('col-note', 'root.note.text')],
      fieldPath: 'root.note.text',
    })).toBeUndefined();
  });

  it('treats a missing projections list as no reuse', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: undefined,
      columns: [column('col-status', 'Status')],
      fieldPath: 'root.status',
    })).toBeUndefined();
  });
});

// External audit 2026-11-03 (row-source-07): an ordinary PUBLIC column is authored
// from authoringv2 Column.Source.Field and never appears in sourceProjections, so
// reuse is blind to it unless the caller carries the signed public identity in.
describe('existingColumnForFieldPath public-field identity', () => {
  it('reuses the live public column whose signed identity carries the field path', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [],
      columns: [column('gender-id', 'Gender'), column('status-id', 'Status')],
      fieldPath: 'gender',
      publicCarriers: [{ columnId: 'gender-id', fieldPath: 'gender' }],
    })).toEqual({ columnId: 'gender-id', label: 'Gender', fieldPath: 'gender' });
  });

  it('matches the signed identity across the root. prefix the server canonicalises away', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [],
      columns: [column('gender-id', 'Gender')],
      fieldPath: 'root.gender',
      publicCarriers: [{ columnId: 'gender-id', fieldPath: 'gender' }],
    })?.columnId).toBe('gender-id');
  });

  it('ignores a public identity whose column is no longer on the stage', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [],
      columns: [column('status-id', 'Status')],
      fieldPath: 'gender',
      publicCarriers: [{ columnId: 'gender-id', fieldPath: 'gender' }],
    })).toBeUndefined();
  });

  it('refuses to pick between two public carriers of the same field path', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [],
      columns: [column('gender-id', 'Gender'), column('gender-copy-id', 'Gender (copy)')],
      fieldPath: 'gender',
      publicCarriers: [
        { columnId: 'gender-id', fieldPath: 'gender' },
        { columnId: 'gender-copy-id', fieldPath: 'root.gender' },
      ],
    })).toBeUndefined();
  });

  it('still refuses a silent pick when two private columns carry the same field path', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [projection('col-a', 'root.status'), projection('col-b', 'root.status')],
      columns: [column('col-a', 'Status'), column('col-b', 'Status (copy)')],
      fieldPath: 'root.status',
      publicCarriers: [{ columnId: 'col-a', fieldPath: 'root.status' }],
    })).toBeUndefined();
  });

  it('refuses to pick when a private and a public column both carry the field path', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [projection('private-status', 'root.status')],
      columns: [column('private-status', 'Status'), column('public-status-id', 'Status')],
      fieldPath: 'root.status',
      publicCarriers: [{ columnId: 'public-status-id', fieldPath: 'status' }],
    })).toBeUndefined();
  });

  it('never matches a public carrier on label or display text', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [],
      columns: [column('col-note', 'root.comment')],
      fieldPath: 'root.comment',
      publicCarriers: [],
    })).toBeUndefined();
  });

  it('keeps private reuse working when no public identity is supplied', () => {
    expect(existingColumnForFieldPath({
      sourceProjections: [projection('col-status', 'root.status')],
      columns: [column('col-status', 'Status')],
      fieldPath: 'root.status',
    })?.columnId).toBe('col-status');
  });
});
