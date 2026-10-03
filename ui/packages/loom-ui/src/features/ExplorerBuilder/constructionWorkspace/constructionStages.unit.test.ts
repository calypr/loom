import { describe, expect, it } from 'vitest';
import type { Construction, ExplorerBuilderDocument } from '../../../types';
import { constructionAppendStageFor, constructionInputStageFor } from './constructionStages';

const prefix: Construction = { version: 1, steps: [{
  id: 'source_filter', inputs: [{ kind: 'SOURCE_PROJECTION' }],
  operation: { kind: 'FILTER', filter: { columnId: 'id', operator: 'EXISTS' } },
  outputs: [{ id: 'id', name: 'id', label: 'ID', type: 'string' }],
}] };
const cohort: ExplorerBuilderDocument['rows'] = { kind: 'GROUPS', groups: {
  afterStepId: 'source_filter',
  source: { kind: 'EXPLICIT', explicit: { revisionId: 'revision', unassignedMemberPolicy: 'EXCLUDE' } },
} };

describe('construction operation stages', () => {
  it('appends after a cohort inserted after the last authored step', () => {
    expect(constructionAppendStageFor(prefix, cohort)).toBe('group_rows');
  });
  it('appends after a source-only cohort', () => {
    expect(constructionAppendStageFor(undefined, { kind: 'GROUPS', groups: { source: cohort.groups.source } })).toBe('group_rows');
  });
  it('keeps later authored operations after the cohort and edits their exact input', () => {
    const suffix: Construction = { version: 1, steps: [...prefix.steps, {
      id: 'cohort_filter', inputs: [{ kind: 'STEP_OUTPUT', stepId: 'group_rows' }],
      operation: { kind: 'FILTER', filter: { columnId: 'group_label', operator: 'EXISTS' } },
      outputs: [{ id: 'group_label', name: 'group_label', label: 'Group label', type: 'string' }],
    }] };
    expect(constructionAppendStageFor(suffix, cohort)).toBe('cohort_filter');
    expect(constructionInputStageFor(suffix, 'cohort_filter')).toBe('group_rows');
    expect(constructionInputStageFor(suffix, 'source_filter')).toBe('source_projection');
  });
  it('keeps the ordinary final stage for record rows', () => {
    expect(constructionAppendStageFor(prefix, undefined)).toBe('source_filter');
    expect(constructionAppendStageFor(undefined, undefined)).toBe('source_projection');
  });
});
