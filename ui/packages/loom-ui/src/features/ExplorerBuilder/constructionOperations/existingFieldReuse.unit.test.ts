import { describe, expect, it } from 'vitest';
import type { Construction, ConstructionStageDescriptor } from '../../../types';
import { existingColumnForFieldPath, partitionChoicesForReuse } from './existingFieldReuse';

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

describe('partitionChoicesForReuse', () => {
  it('marks each choice with its reuse target while preserving order', () => {
    const parts = partitionChoicesForReuse({
      choices: [{ fieldPath: 'root.note.text' }, { fieldPath: 'root.status' }],
      sourceProjections: [projection('col-status', 'root.status')],
      columns: [column('col-status', 'Status')],
    });
    expect(parts.map((part) => part.choice.fieldPath)).toEqual(['root.note.text', 'root.status']);
    expect(parts[0]?.reuse).toBeUndefined();
    expect(parts[1]?.reuse?.columnId).toBe('col-status');
  });
});
