import { describe, expect, it } from 'vitest';
import type { Construction, ConstructionStageDescriptor, ExplorerBuilderDocument } from '../../../types';
import {
  constructionAppendStageFor,
  constructionInputForStage,
  constructionInputStageFor,
  isSourceProjectionStage,
  sourceInputMatchesStage,
} from './constructionStages';

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
  it('maps the explicit source stage to source projection and constructed operations to their exact predecessor step', () => {
    const source: ConstructionStageDescriptor = {
      id: 'source_projection', inputStageId: '', operation: 'SOURCE_PROJECTION', columns: [], capabilities: [],
    };
    expect(constructionInputForStage(source)).toEqual({ kind: 'SOURCE_PROJECTION' });
    expect(isSourceProjectionStage(source)).toBe(true);
    expect(sourceInputMatchesStage('source_projection', source)).toBe(true);

    for (const operation of ['FILTER', 'DERIVE', 'GROUP']) {
      const stage: ConstructionStageDescriptor = {
        id: `${operation.toLowerCase()}-stage`, inputStageId: 'source_projection', operation, columns: [], capabilities: [],
      };
      expect(constructionInputForStage(stage)).toEqual({ kind: 'STEP_OUTPUT', stepId: stage.id });
      expect(isSourceProjectionStage(stage)).toBe(false);
      expect(sourceInputMatchesStage('source_projection', stage)).toBe(false);
    }
  });

  it('does not treat a constructed or mismatched stage as source input', () => {
    const source: ConstructionStageDescriptor = {
      id: 'source_projection', inputStageId: '', operation: 'SOURCE_PROJECTION', columns: [], capabilities: [],
    };
    const wrongSourceOperation = { ...source, operation: 'GROUP' };
    const constructedGroup: ConstructionStageDescriptor = {
      id: 'group-status', inputStageId: 'source_projection', operation: 'GROUP', columns: [], capabilities: [],
    };

    expect(sourceInputMatchesStage('group-status', source)).toBe(false);
    expect(sourceInputMatchesStage('source_projection', wrongSourceOperation)).toBe(false);
    expect(sourceInputMatchesStage('source_projection', constructedGroup)).toBe(false);
  });

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

  it('selects the compiled terminal stage when editing a Combine with published inputs', () => {
    const combine: Construction = { version: 1, steps: [{
      id: 'append_sources',
      inputs: [
        { kind: 'TABLE_REVISION', tableId: 'table-a', revisionId: 'revision-a', outputId: 'observations' },
        { kind: 'TABLE_REVISION', tableId: 'table-b', revisionId: 'revision-b', outputId: 'reports' },
      ],
      operation: { kind: 'COMBINE', combine: {
        kind: 'APPEND',
        projections: [{ outputColumnId: 'subject', inputIndex: 0, inputColumnId: 'subject' }],
      } },
      outputs: [{ id: 'subject', name: 'subject', label: 'Subject', type: 'string' }],
    }] };
    const compiledStages: ConstructionStageDescriptor[] = [{
      id: 'append_sources', inputStageId: '', operation: 'APPEND', rowIdentityColumn: 'row_id',
      columns: [{ id: 'subject', name: 'subject', label: 'Subject', type: 'string' }],
      capabilities: [],
    }];

    const selectedStageId = constructionInputStageFor(combine, 'append_sources');
    expect(selectedStageId).toBe('append_sources');
    expect(compiledStages.map((stage) => stage.id)).toContain(selectedStageId);
    expect(compiledStages.map((stage) => stage.id)).not.toContain('source_projection');
  });
});
