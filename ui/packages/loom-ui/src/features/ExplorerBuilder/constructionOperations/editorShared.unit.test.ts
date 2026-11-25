import { describe, expect, it } from 'vitest';
import type { Construction, ConstructionStep, ConstructionStageDescriptor } from '../../../contracts/types';
import { constructionWithStep, stageWithSourceInputs } from '../model/editorShared';

const stage: ConstructionStageDescriptor = {
  id: 'source_projection', inputStageId: '', capabilities: [],
  columns: [{ id: 'saved', name: 'value', label: 'Value', type: 'string', cardinality: 'optional_one' }],
};
const choices = [
  { choiceId: 'one', fieldPath: 'value', label: 'First value', logicalType: 'string' },
  { choiceId: 'two', fieldPath: 'value', label: 'Second value', logicalType: 'decimal' },
];
const selections = [{ choiceId: 'one', columnId: 'first' }, { choiceId: 'two', columnId: 'second' }];

describe('signed source inputs in editor stages', () => {
  it.each(['FILTER', 'DERIVE'] as const)('preserves %s naming against the original input stage', (kind) => {
    const result = stageWithSourceInputs({ stage, selections, choices, kind });
    expect(result.columns).toEqual([
      stage.columns[0],
      { id: 'first', name: 'value_2', label: 'First value', type: 'string', cardinality: 'optional_one' },
      { id: 'second', name: 'value_2', label: 'Second value', type: 'decimal', cardinality: 'optional_one' },
    ]);
  });

  it.each(['PIVOT', 'UNPIVOT'] as const)('preserves %s name reservations between signed inputs', (kind) => {
    expect(stageWithSourceInputs({ stage, selections, choices, kind }).columns.map(({ name }) => name))
      .toEqual(['value', 'value_2', 'value_3']);
  });

  it('keeps EXPAND inputs repeated and ignores missing or already materialized choices', () => {
    expect(stageWithSourceInputs({ stage, selections: [selections[0]], choices, kind: 'EXPAND' }).columns[1])
      .toEqual({ id: 'first', name: 'value_2', label: 'First value', type: 'string', cardinality: 'many' });
    expect(stageWithSourceInputs({
      stage, choices, kind: 'DERIVE',
      selections: [{ choiceId: 'missing', columnId: 'unknown' }, { choiceId: 'one', columnId: 'saved' }, { choiceId: 'one', columnId: '' }],
    }).columns).toEqual(stage.columns);
    expect(stage.columns).toHaveLength(1);
  });
});

describe('construction candidates', () => {
  it('retains signed source projections while replacing their owning step', () => {
    const step: ConstructionStep = {
      id: 'filter', inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'FILTER', filter: { columnId: 'projected', operator: 'EXISTS' } },
      outputs: [{ id: 'projected', name: 'value', label: 'Value', type: 'string' }],
    };
    const construction: Construction = {
      version: 1, steps: [step],
      sourceProjections: [{ columnId: 'projected', ownerStepId: 'filter', occurrenceId: 'base',
        fieldPath: 'Observation.value', fhirType: 'string', logicalType: 'string', label: 'Value' }],
    };
    const result = constructionWithStep({ construction, editingStep: step, step });
    expect(result).toBeDefined();
    expect(result?.sourceProjections).toEqual(construction.sourceProjections);
  });
});
