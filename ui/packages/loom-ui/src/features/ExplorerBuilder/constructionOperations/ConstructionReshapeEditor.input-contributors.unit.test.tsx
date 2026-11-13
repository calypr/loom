// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ConstructionReshapeEditor, type ConstructionReshapeEditorProps, type ConstructionReshapeStep } from './ConstructionReshapeEditor';
import { constructionSchema } from '../../../types';

type Capabilities = ConstructionReshapeEditorProps['capabilities'];
const sourceStage: Capabilities['selectedStage'] = {
  id: 'source_projection', inputStageId: '', operation: 'SOURCE_PROJECTION', rowIdentityColumn: '_key',
  columns: [
    { id: 'source-status', name: 'status', label: 'Status', type: 'string', cardinality: 'required_one' },
    { id: 'source-subject-id', name: 'subject_id', label: 'Subject ID', type: 'string', cardinality: 'required_one' },
    { id: 'hidden-key', name: '_key', label: 'Private identity', type: 'string', cardinality: 'required_one' },
    { id: 'list-values', name: 'values', label: 'Repeated values', type: 'string', cardinality: 'many' },
    { id: 'object-value', name: 'object', label: 'Object value', type: 'object', cardinality: 'optional_one' },
  ], capabilities: [{ kind: 'GROUP', supported: true }],
};
const savedGroup: ConstructionReshapeStep = {
  id: 'saved-group', inputs: [{ kind: 'SOURCE_PROJECTION' }],
  operation: { kind: 'GROUP', group: { constructionId: 'saved-group', missingKeyPolicy: 'GROUP',
    keys: [{ inputColumnId: 'source-status', outputColumnId: 'group-status' }],
    aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'subject-count' }] } },
  outputs: [{ id: 'group-status', name: 'status', label: 'Status', type: 'string' },
    { id: 'subject-count', name: 'subject_count', label: 'Subject count', type: 'integer' }],
};
const capabilities = (rowChoices: Capabilities['groupRowValueChoices'] = []): Capabilities => ({
  snapshotToken: 'snapshot', draftVersion: 4, draftDigest: 'digest', outputId: 'ResearchSubject',
  stageId: sourceStage.id, selectedStage: sourceStage, stages: [sourceStage],
  baseConstruction: { version: 1, steps: [savedGroup] }, groupFieldChoices: [], groupRowValueChoices: rowChoices, workspaceInputs: [],
});
const propsFor = (caps = capabilities(), onCandidateChange = vi.fn()): ConstructionReshapeEditorProps => ({
  construction: caps.baseConstruction, capabilities: caps, editingStep: savedGroup, rowValuesOnly: true,
  disabled: false, onCandidateChange, onEditStep: vi.fn(),
});
afterEach(cleanup);

it('retains an exact projected input when imported source has no raw-field context', async () => {
  const onCandidateChange = vi.fn();
  render(<ConstructionReshapeEditor {...propsFor(capabilities(), onCandidateChange)} />);
  fireEvent.click(screen.getByText('Add contributor value', { exact: true }));
  const input = screen.getByTestId('construction-group-add-column-input-source-subject-id');
  expect(input).toHaveAttribute('value', 'source-subject-id');
  expect(screen.queryByRole('checkbox', { name: 'Add column Status' })).toBeNull();
  expect(screen.queryByRole('checkbox', { name: 'Add column Private identity' })).toBeNull();
  expect(screen.queryByRole('checkbox', { name: 'Add column Repeated values' })).toBeNull();
  expect(screen.queryByRole('checkbox', { name: 'Add column Object value' })).toBeNull();
  fireEvent.click(input);
  await waitFor(() => expect(onCandidateChange.mock.lastCall?.[0]).toBeDefined());
  const candidate = constructionSchema.parse(onCandidateChange.mock.lastCall?.[0].candidateConstruction);
  expect(candidate.steps[0].inputs).toEqual(savedGroup.inputs);
  expect(candidate.steps[0].operation).toEqual(savedGroup.operation);
  expect(candidate.steps[0].rowValues).toEqual([{ inputColumnId: 'source-subject-id', outputColumnId: expect.any(String), policy: 'ALL' }]);
  expect(candidate.steps[0].outputs).toEqual(expect.arrayContaining(savedGroup.outputs));
  expect(candidate.steps[0].outputs).toContainEqual(expect.objectContaining({ name: 'subject_id', type: 'array' }));
  expect((screen.getByLabelText('Keep values 1') as HTMLSelectElement).value).toBe('ALL');
  expect(screen.queryByTestId('construction-group-add-column-input-source-subject-id')).toBeNull();
});

it('does not duplicate a projected input already offered as an exact signed field choice', () => {
  const choice: Capabilities['groupRowValueChoices'][number] = {
    kind: 'FIELD_SOURCE', valueKind: 'SCALAR', fieldSource: { kind: 'CHOICE', choiceId: 'signed-subject-id' },
    inputColumnId: 'source-subject-id', label: 'Subject ID', valueType: 'string', sourceStageId: sourceStage.id,
    sourceShapeId: 'shape', candidateId: 'candidate-id', sourceRole: 'ROOT', occurrenceId: 'base', resourceType: 'ResearchSubject',
    ownerOccurrenceId: 'base', ownerResourceType: 'ResearchSubject', canonicalPath: 'id',
  };
  render(<ConstructionReshapeEditor {...propsFor(capabilities([choice]))} />);
  fireEvent.click(screen.getByText('Add contributor value', { exact: true }));
  expect(screen.getAllByRole('checkbox', { name: 'Add column Subject ID' })).toHaveLength(1);
  expect(screen.getByTestId('construction-group-add-column-choice-signed-subject-id')).toBeEnabled();
  expect(screen.queryByTestId('construction-group-add-column-input-source-subject-id')).toBeNull();
});

it('does not offer replacement-stage inputs to a saved Group while its input capabilities are stale', () => {
  const otherStage: Capabilities['selectedStage'] = { ...sourceStage, id: 'other-stage', inputStageId: sourceStage.id,
    operation: 'DERIVE', columns: [{ id: 'other-column', name: 'other', label: 'Other stage value', type: 'string', cardinality: 'required_one' }] };
  const caps: Capabilities = { ...capabilities(), stageId: otherStage.id, selectedStage: otherStage, stages: [sourceStage, otherStage] };
  render(<ConstructionReshapeEditor {...propsFor(caps)} />);
  fireEvent.click(screen.getByText('Add contributor value', { exact: true }));
  expect(screen.queryByTestId('construction-group-add-column-input-source-subject-id')).toBeNull();
  expect(screen.queryByRole('checkbox', { name: 'Add column Other stage value' })).toBeNull();
});

const derivedGroupFixture = () => {
  const root: Capabilities['selectedStage'] = { ...sourceStage,
    columns: sourceStage.columns.filter((column) => ['source-status', 'source-subject-id'].includes(column.id)),
    capabilities: [{ kind: 'GROUP', supported: true }, { kind: 'CODED_PIVOT', supported: true }],
  };
  const derive: ConstructionReshapeStep = {
    id: 'saved-derive', inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: { kind: 'DERIVE', derive: { constructionId: 'saved-derive', outputColumnId: 'derived-value', operation: 'ADD',
      left: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 1 } },
      right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 2 } }, missingInputPolicy: 'ERROR' } },
    outputs: [...root.columns.map((column) => ({ id: column.id, name: column.name, label: column.label, type: column.type })),
      { id: 'derived-value', name: 'derived', label: 'Derived value', type: 'integer' }],
  };
  const group: ConstructionReshapeStep = { ...savedGroup, inputs: [{ kind: 'STEP_OUTPUT', stepId: derive.id }] };
  const derivedStage: Capabilities['selectedStage'] = { id: derive.id, inputStageId: root.id, operation: 'DERIVE',
    columns: derive.outputs.map((column) => ({ ...column, cardinality: 'required_one' })),
    capabilities: [{ kind: 'GROUP', supported: true }],
  };
  return { root, derive, group, derivedStage };
};

it('blocks a coded-capable source from retargeting an existing downstream Group', () => {
  const { root, derive, group } = derivedGroupFixture();
  const caps: Capabilities = { ...capabilities(), selectedStage: root, stages: [root],
    baseConstruction: { version: 1, steps: [derive, group] } };
  const onCandidateChange = vi.fn();
  render(<ConstructionReshapeEditor {...propsFor(caps, onCandidateChange)} editingStep={group} />);
  fireEvent.click(screen.getByText('Add contributor value', { exact: true }));
  expect(screen.queryByTestId('construction-group-add-column-input-source-subject-id')).toBeNull();
  expect(onCandidateChange.mock.calls.every(([intent]) => intent === undefined)).toBe(true);
});

it('edits contributors on the correct derived input without changing the Group anchor or prior work', async () => {
  const { root, derive, group, derivedStage } = derivedGroupFixture();
  const caps: Capabilities = { ...capabilities(), stageId: derivedStage.id, selectedStage: derivedStage,
    stages: [root, derivedStage], baseConstruction: { version: 1, steps: [derive, group] } };
  const onCandidateChange = vi.fn();
  render(<ConstructionReshapeEditor {...propsFor(caps, onCandidateChange)} editingStep={group} />);
  fireEvent.click(screen.getByText('Add contributor value', { exact: true }));
  fireEvent.click(screen.getByTestId('construction-group-add-column-input-source-subject-id'));
  await waitFor(() => expect(onCandidateChange.mock.lastCall?.[0]).toBeDefined());
  const candidate = constructionSchema.parse(onCandidateChange.mock.lastCall?.[0].candidateConstruction);
  expect(candidate.steps[0]).toEqual(derive);
  expect(candidate.steps[1].inputs).toEqual(group.inputs);
  expect(candidate.steps[1].operation).toEqual(group.operation);
  expect(candidate.steps[1].rowValues).toEqual([{ inputColumnId: 'source-subject-id', outputColumnId: expect.any(String), policy: 'ALL' }]);
});
