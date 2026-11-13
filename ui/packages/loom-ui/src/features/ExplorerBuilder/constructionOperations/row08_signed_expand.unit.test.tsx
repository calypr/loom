// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import { ConstructionReshapeEditor, type ConstructionReshapeEditorProps, type ConstructionReshapeStep } from './ConstructionReshapeEditor';
import { constructionCapabilitiesResponseSchema, constructionProposalRequestSchema, constructionSchema } from '../../../types';

const baseColumns: ConstructionReshapeEditorProps['capabilities']['selectedStage']['columns'] = [
  { id: 'site-id', name: 'site', label: 'Site', type: 'string', cardinality: 'required_one' },
  { id: 'tags-id', name: 'tags', label: 'Tags', type: 'string', cardinality: 'many' },
];

const baseStage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  columns: baseColumns,
  capabilities: [
    { kind: 'FILTER', supported: true },
    { kind: 'DERIVE', supported: true },
    { kind: 'PIVOT', supported: false, reason: 'not needed' },
    { kind: 'UNPIVOT', supported: false, reason: 'not needed' },
    { kind: 'GROUP', supported: true },
    { kind: 'EXPAND', supported: false, reason: 'no public array columns' },
  ],
};

const capabilitiesFor = (
  overrides: Partial<ConstructionReshapeEditorProps['capabilities']> = {},
): ConstructionReshapeEditorProps['capabilities'] => ({
  snapshotToken: 'snapshot-1',
  draftVersion: 1,
  draftDigest: 'draft-digest-1',
  outputId: 'table-1',
  stageId: baseStage.id,
  baseConstruction: { version: 1, steps: [] },
  stages: [baseStage],
  selectedStage: baseStage,
  groupFieldChoices: [],
  groupRowValueChoices: [],
  workspaceInputs: [],
  ...overrides,
});

const signedChannel: ConstructionReshapeEditorProps['capabilities']['expandSourceInput'] = {
  supported: true,
  stageId: baseStage.id,
  choices: [
    { choiceId: 'choice-photo', fieldPath: 'photo', label: 'Photo', logicalType: 'string', isPopulated: true },
  ],
};

const renderEditor = (args: {
  readonly construction?: ConstructionReshapeEditorProps['construction'];
  readonly capabilities?: ConstructionReshapeEditorProps['capabilities'];
  readonly editingStep?: ConstructionReshapeStep;
  readonly onCandidateChange?: ConstructionReshapeEditorProps['onCandidateChange'];
}) => {
  const onCandidateChange = (args.onCandidateChange ?? vi.fn()) as Mock<ConstructionReshapeEditorProps['onCandidateChange']>;
  render(
    <ConstructionReshapeEditor
      construction={args.construction ?? { version: 1, steps: [] }}
      capabilities={args.capabilities ?? capabilitiesFor()}
      editingStep={args.editingStep}
      initialKind="expand"
      disabled={false}
      onCandidateChange={onCandidateChange}
      onEditStep={vi.fn()}
    />,
  );
  return { onCandidateChange };
};

const selectRepeatedField = (): HTMLSelectElement => {
  const control = screen.getByLabelText('Repeated field');
  expect(control instanceof HTMLSelectElement).toBe(true);
  return control as HTMLSelectElement;
};

afterEach(cleanup);

describe('signed EXPAND source lane', () => {
  it('offers a populated signed array choice even when the server says EXPAND is unsupported', () => {
    const capabilities = capabilitiesFor({ expandSourceInput: signedChannel });
    constructionCapabilitiesResponseSchema.parse(JSON.parse(JSON.stringify(capabilities)));
    renderEditor({ capabilities });
    const options = [...selectRepeatedField().querySelectorAll('option')].map((option) => option.value);
    expect(options).toContain('source:choice-photo');
    expect(options).toContain('tags-id');
  });

  it('hides signed choices when the channel is unsupported and shows the honest empty state when no list columns either', () => {
    const stage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
      ...baseStage,
      columns: [baseColumns[0]!],
    };
    renderEditor({ capabilities: capabilitiesFor({ expandSourceInput: { supported: false, stageId: baseStage.id, reasonCode: 'NO_ELIGIBLE_POPULATED_EXPAND_SOURCE_FIELDS', reason: 'Nothing populated.', choices: [] }, selectedStage: stage, stages: [stage] }) });
    expect(screen.queryByLabelText('Repeated field')).toBeNull();
    expect(screen.getByText('Loom has not returned any list-valued columns for this stage.')).toBeTruthy();
  });

  it('materializes a fresh signed column id and emits the request-parseable expandSources binding', () => {
    const capabilities = capabilitiesFor({ expandSourceInput: signedChannel });
    const { onCandidateChange } = renderEditor({ capabilities });
    fireEvent.change(selectRepeatedField(), { target: { value: 'source:choice-photo' } });
    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent?.expandSources).toEqual([{ choiceId: 'choice-photo', columnId: expect.stringMatching(/\S/) }]);
    constructionSchema.parse(intent?.candidateConstruction);
    constructionProposalRequestSchema.parse({
      snapshotToken: 'snapshot-1', expectedDraftVersion: 1, expectedDraftDigest: 'draft-digest-1',
      outputId: 'table-1', changedStepId: intent?.changedStepId,
      candidateConstruction: intent?.candidateConstruction, expandSources: intent?.expandSources,
    });
    const step = intent?.candidateConstruction?.steps?.[0];
    expect(step?.operation.kind).toBe('EXPAND');
    if (step?.operation.kind !== 'EXPAND') throw new Error('expected expand step');
    expect(step.operation.expand.inputColumnId).toBe(intent?.expandSources?.[0]?.columnId);
    expect(step.operation.expand.expandedField).toBeUndefined();
  });

  it('reuses the durable server columnId for an already-materialized signed choice', () => {
    const capabilities = capabilitiesFor({
      expandSourceInput: {
        supported: true,
        stageId: baseStage.id,
        choices: [{ choiceId: 'choice-photo', fieldPath: 'photo', label: 'Photo', logicalType: 'string', isPopulated: true, columnId: 'materialized-photo' }],
      },
    });
    const { onCandidateChange } = renderEditor({ capabilities });
    fireEvent.change(selectRepeatedField(), { target: { value: 'source:choice-photo' } });
    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent?.expandSources).toEqual([{ choiceId: 'choice-photo', columnId: 'materialized-photo' }]);
  });

  it('drops the signed binding when the input switches back to a plain list column', () => {
    const capabilities = capabilitiesFor({ expandSourceInput: signedChannel });
    const { onCandidateChange } = renderEditor({ capabilities });
    fireEvent.change(selectRepeatedField(), { target: { value: 'source:choice-photo' } });
    fireEvent.change(selectRepeatedField(), { target: { value: 'tags-id' } });
    const intent = onCandidateChange.mock.lastCall?.[0];
    expect(intent?.expandSources).toBeUndefined();
    const step = intent?.candidateConstruction?.steps?.[0];
    if (step?.operation.kind !== 'EXPAND') throw new Error('expected expand step');
    expect(step.operation.expand.inputColumnId).toBe('tags-id');
  });

  it('suppresses a signed choice whose field is already a list column of this stage (ROW-SOURCE-07 reuse)', () => {
    const capabilities = capabilitiesFor({ expandSourceInput: signedChannel });
    const construction: ConstructionReshapeEditorProps['construction'] = {
      version: 1,
      steps: [],
      sourceProjections: [{
        columnId: 'photo-col', ownerStepId: 'owner-step', occurrenceId: 'root',
        fieldPath: 'photo', fhirType: 'string', logicalType: 'string', label: 'Photo',
      }],
    };
    const stage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
      ...baseStage,
      columns: [...baseColumns, { id: 'photo-col', name: 'photo', label: 'Photo', type: 'string', cardinality: 'many' }],
    };
    renderEditor({
      construction,
      capabilities: capabilitiesFor({ expandSourceInput: signedChannel, selectedStage: stage, stages: [stage] }),
    });
    const options = [...selectRepeatedField().querySelectorAll('option')].map((option) => option.value);
    expect(options).not.toContain('source:choice-photo');
    expect(options).toContain('photo-col');
  });

  it('keeps the signed choice offered when the same-field column exists but is not a list column', () => {
    const capabilities = capabilitiesFor({ expandSourceInput: signedChannel });
    const construction: ConstructionReshapeEditorProps['construction'] = {
      version: 1,
      steps: [],
      sourceProjections: [{
        columnId: 'photo-scalar', ownerStepId: 'owner-step', occurrenceId: 'root',
        fieldPath: 'photo', fhirType: 'string', logicalType: 'string', label: 'Photo',
      }],
    };
    const stage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
      ...baseStage,
      columns: [...baseColumns, { id: 'photo-scalar', name: 'photo_scalar', label: 'Photo scalar', type: 'string', cardinality: 'optional_one' }],
    };
    renderEditor({
      construction,
      capabilities: capabilitiesFor({ expandSourceInput: signedChannel, selectedStage: stage, stages: [stage] }),
    });
    const options = [...selectRepeatedField().querySelectorAll('option')].map((option) => option.value);
    expect(options).toContain('source:choice-photo');
  });

  it('restores the durable binding when editing a saved signed expand step', () => {
    const savedStep: ConstructionReshapeStep = {
      id: 'saved-expand',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'EXPAND', expand: {
        constructionId: 'saved-expand', inputColumnId: 'stored-photo-col', outputColumnId: 'photo_item',
        emptyPolicy: 'EXCLUDE',
        expandedField: { path: 'photo', cardinality: 'many', fhirType: 'string', logicalType: 'string' },
      } },
      outputs: [
        { id: 'site-id', name: 'site', label: 'Site', type: 'string' },
        { id: 'photo_item', name: 'photo_item', label: 'Photo item', type: 'string' },
      ],
    };
    const stage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
      ...baseStage,
      columns: [...baseColumns, { id: 'stored-photo-col', name: 'photo', label: 'Photo', type: 'string', cardinality: 'many' }],
    };
    const construction: ConstructionReshapeEditorProps['construction'] = {
      version: 1,
      steps: [savedStep],
      sourceProjections: [{
        columnId: 'stored-photo-col', ownerStepId: 'saved-expand', occurrenceId: 'root',
        fieldPath: 'photo', fhirType: 'string', logicalType: 'string', label: 'Photo',
      }],
    };
    const capabilities = capabilitiesFor({
      expandSourceInput: {
        supported: true,
        stageId: baseStage.id,
        choices: [{ choiceId: 'choice-photo', fieldPath: 'photo', label: 'Photo', logicalType: 'string', isPopulated: true, columnId: 'stored-photo-col' }],
      },
      selectedStage: stage,
      stages: [stage],
    });
    const { onCandidateChange } = renderEditor({ construction, capabilities, editingStep: savedStep });
    expect(screen.getByLabelText('Repeated field')).toBeTruthy();
    const intent = onCandidateChange.mock.calls.map((call) => call[0]).find((candidate) => candidate?.expandSources !== undefined);
    expect(intent?.expandSources).toEqual([{ choiceId: 'choice-photo', columnId: 'stored-photo-col' }]);
  });
});
