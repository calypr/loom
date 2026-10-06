// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ConstructionCombineEditor, type ConstructionCombineEditorProps } from './ConstructionCombineEditor';
import type {
  ConstructionCombineCandidateIntent,
  ConstructionCombineCatalog,
  ConstructionCombinePublishedRevision,
  ConstructionCombineWorkspaceOutput,
} from './combineEditorTypes';

type CandidateMock = ReturnType<typeof vi.fn<(intent: ConstructionCombineCandidateIntent | undefined) => void>>;

const column = (id: string, name: string, type: string, clickhouseType: string, options: { nullable?: boolean; repeated?: boolean } = {}) => ({
  id,
  name,
  label: name.replaceAll('_', ' '),
  type,
  clickhouseType,
  nullable: options.nullable ?? false,
  repeated: options.repeated ?? false,
});

const leftRevision: ConstructionCombinePublishedRevision = {
  kind: 'TABLE_REVISION',
  tableId: 'table-left',
  revisionId: 'left-rev-1',
  outputId: 'left-output',
  tableTitle: 'Patients',
  outputTitle: 'Patient records',
  rowMeaning: 'one row per patient',
  isCurrent: true,
  columns: [
    column('patient-key', 'patient_id', 'string', 'String'),
    column('age', 'age', 'integer', 'Int64'),
    column('nullable-code', 'nullable_code', 'string', 'Nullable(String)', { nullable: true }),
  ],
};

const rightOldRevision: ConstructionCombinePublishedRevision = {
  kind: 'TABLE_REVISION',
  tableId: 'table-right',
  revisionId: 'right-rev-1',
  outputId: 'right-output',
  tableTitle: 'Visits',
  outputTitle: 'Visit records',
  rowMeaning: 'one row per visit',
  isCurrent: false,
  columns: [
    column('visit-patient-key', 'patient_id', 'string', 'String'),
    column('visit-day', 'visit_day', 'date', 'Date'),
    column('visit-tags', 'visit_tags', 'string', 'Array(String)', { repeated: true }),
  ],
};

const rightCurrentRevision: ConstructionCombinePublishedRevision = {
  ...rightOldRevision,
  revisionId: 'right-rev-2',
  isCurrent: true,
};

const thirdRevision: ConstructionCombinePublishedRevision = {
  kind: 'TABLE_REVISION',
  tableId: 'table-third',
  revisionId: 'third-rev-1',
  outputId: 'third-output',
  tableTitle: 'More patients',
  outputTitle: 'Additional patients',
  rowMeaning: 'one row per patient',
  isCurrent: true,
  columns: [column('third-patient-key', 'patient_id', 'string', 'String'), column('third-age', 'age', 'integer', 'Int64')],
};

const catalog: ConstructionCombineCatalog = {
  kind: 'ready',
  revisions: [leftRevision, rightOldRevision, rightCurrentRevision, thirdRevision],
};

const refKey = (revision: ConstructionCombinePublishedRevision): string =>
  JSON.stringify([revision.tableId, revision.revisionId, revision.outputId]);


const workspaceOutput = (outputId: string, title: string, columnId: string, name = 'patient_id'): ConstructionCombineWorkspaceOutput => ({
  kind: 'WORKSPACE_OUTPUT',
  outputId,
  title,
  columns: [{
    id: columnId,
    name,
    label: name.replaceAll('_', ' '),
    type: 'string',
    nullable: false,
    repeated: false,
    cardinality: 'required_one',
    joinCompatibilityKey: 'String',
    appendCompatibilityKey: 'string:String',
  }],
});

const renderEditor = (options: {
  readonly catalog?: ConstructionCombineCatalog;
  readonly editingStep?: ConstructionCombineEditorProps['editingStep'];
  readonly disabled?: boolean;
  readonly onCandidateChange?: CandidateMock;
  readonly onLoadMore?: () => void;
  readonly workspaceInputs?: ReadonlyArray<ConstructionCombineWorkspaceOutput>;
} = {}) => {
  const onCandidateChange = options.onCandidateChange ?? vi.fn<(intent: ConstructionCombineCandidateIntent | undefined) => void>();
  render(
    <ConstructionCombineEditor
      catalog={options.catalog ?? catalog}
      workspaceInputs={options.workspaceInputs}
      constructionVersion={7}
      editingStep={options.editingStep}
      disabled={options.disabled ?? false}
      onLoadMore={options.onLoadMore}
      onCandidateChange={onCandidateChange}
    />,
  );
  return onCandidateChange;
};

const choose = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } });
const lastCandidate = (callback: CandidateMock): ConstructionCombineCandidateIntent | undefined => callback.mock.lastCall?.[0];

afterEach(cleanup);

describe('ConstructionCombineEditor', () => {
  it('emits an exact-pinned LEFT join, preserves right-side nullability, and requires explicit revision updates', () => {
    const onCandidateChange = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Match rows' }));
    choose('Input table 1', refKey(leftRevision));
    choose('Input table 2', refKey(rightOldRevision));
    expect(screen.getByText(/one row per visit · pinned revision right-rev-1/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Update input table 2 to revision right-rev-2' })).toBeInTheDocument();

    choose('Matching pair 1 first field', 'patient-key');
    choose('Matching pair 1 second field', 'visit-patient-key');
    choose('If a row in the first table has no match', 'LEFT');
    fireEvent.click(screen.getByRole('button', { name: 'Add output field' }));
    choose('Output field 1 source field in input 2', 'visit-day');
    choose('Output field 1 name', 'visit_day');
    choose('Output field 1 label', 'Visit day');

    let intent = lastCandidate(onCandidateChange);
    expect(intent?.candidateConstruction.steps[0]).toMatchObject({
      inputs: [
        { kind: 'TABLE_REVISION', tableId: 'table-left', revisionId: 'left-rev-1', outputId: 'left-output' },
        { kind: 'TABLE_REVISION', tableId: 'table-right', revisionId: 'right-rev-1', outputId: 'right-output' },
      ],
      operation: {
        kind: 'COMBINE',
        combine: {
          kind: 'KEY_JOIN',
          joinType: 'LEFT',
          rightMatchPolicy: 'PRESERVE_ALL',
          keys: [{ leftColumnId: 'patient-key', rightColumnId: 'visit-patient-key' }],
        },
      },
      outputs: [{ name: 'visit_day', label: 'Visit day', type: 'date', nullable: true }],
    });

    fireEvent.click(screen.getByRole('button', { name: 'Update input table 2 to revision right-rev-2' }));
    expect(screen.getByText(/one row per visit · pinned revision right-rev-2/)).toBeInTheDocument();
    intent = lastCandidate(onCandidateChange);
    expect(intent?.candidateConstruction.steps[0].inputs[1]).toEqual({
      kind: 'TABLE_REVISION', tableId: 'table-right', revisionId: 'right-rev-2', outputId: 'right-output',
    });
  });

  it('requires each APPEND slot to be mapped before emitting a candidate', () => {
    const onCandidateChange = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Stack rows' }));
    choose('Input table 1', refKey(leftRevision));
    choose('Input table 2', refKey(thirdRevision));
    fireEvent.click(screen.getByRole('button', { name: 'Add output field' }));
    choose('Output field 1 matching field in input 1', 'column:age');
    choose('Output field 1 name', 'age');
    choose('Output field 1 label', 'Age');
    expect(lastCandidate(onCandidateChange)).toBeUndefined();

    choose('Output field 1 matching field in input 2', 'column:third-age');
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0]).toMatchObject({
      inputs: [
        { revisionId: 'left-rev-1', tableId: 'table-left', outputId: 'left-output' },
        { revisionId: 'third-rev-1', tableId: 'table-third', outputId: 'third-output' },
      ],
      operation: {
        kind: 'COMBINE',
        combine: {
          kind: 'APPEND',
          projections: [
            { outputColumnId: expect.any(String), inputIndex: 0, inputColumnId: 'age' },
            { outputColumnId: expect.any(String), inputIndex: 1, inputColumnId: 'third-age' },
          ],
        },
      },
      outputs: [{ name: 'age', label: 'Age', type: 'integer', nullable: false }],
    });
  });

  it('emits an automatic nullable APPEND candidate only after an explicit empty-table choice', () => {
    const onCandidateChange = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Stack rows' }));
    choose('Input table 1', refKey(leftRevision));
    choose('Input table 2', refKey(rightOldRevision));
    fireEvent.click(screen.getByRole('button', { name: 'Add output field' }));
    choose('Output field 1 matching field in input 1', 'column:patient-key');
    choose('Output field 1 name', 'patient_id');
    choose('Output field 1 label', 'Patient ID');
    expect(lastCandidate(onCandidateChange)).toBeUndefined();
    expect((screen.getByLabelText('Output field 1 matching field in input 2') as HTMLSelectElement).querySelector('option[value="column:visit-tags"]')).toBeNull();

    choose('Output field 1 matching field in input 2', 'empty-for-this-table');
    const intent = lastCandidate(onCandidateChange);
    expect(intent?.candidateConstruction.steps[0].operation.combine).toEqual({
      kind: 'APPEND',
      projections: [{ outputColumnId: expect.any(String), inputIndex: 0, inputColumnId: 'patient-key' }],
    });
    expect(intent?.candidateConstruction.steps[0].outputs).toEqual([
      { id: expect.any(String), name: 'patient_id', label: 'Patient ID', type: 'string', nullable: true },
    ]);
    expect((screen.getByLabelText('Output field 1 matching field in input 2') as HTMLSelectElement).value).toBe('empty-for-this-table');
    expect(screen.getByText(/Repeated fields are not supported/)).toBeInTheDocument();
  });

  it('reconstructs a saved APPEND omission as an explicit empty-table selection', () => {
    const savedStep: NonNullable<ConstructionCombineEditorProps['editingStep']> = {
      id: 'saved-append',
      inputs: [
        { kind: 'TABLE_REVISION', tableId: 'table-left', revisionId: 'left-rev-1', outputId: 'left-output' },
        { kind: 'TABLE_REVISION', tableId: 'table-third', revisionId: 'third-rev-1', outputId: 'third-output' },
      ],
      operation: {
        kind: 'COMBINE',
        combine: {
          kind: 'APPEND',
          projections: [{ outputColumnId: 'saved-age', inputIndex: 0, inputColumnId: 'age' }],
        },
      },
      outputs: [{ id: 'saved-age', name: 'age', label: 'Age', type: 'integer', nullable: true }],
    };
    const onCandidateChange = renderEditor({ editingStep: savedStep });

    expect((screen.getByLabelText('Output field 1 matching field in input 2') as HTMLSelectElement).value).toBe('empty-for-this-table');
    expect(onCandidateChange).not.toHaveBeenCalled();
    choose('Output field 1 label', 'Updated age');
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0].operation.combine).toEqual({
      kind: 'APPEND',
      projections: [{ outputColumnId: 'saved-age', inputIndex: 0, inputColumnId: 'age' }],
    });
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0].outputs[0]).toMatchObject({
      id: 'saved-age', type: 'integer', nullable: true,
    });
  });

  it('offers each combine action and exposes catalog continuation instead of implying the first page is complete', () => {
    const onLoadMore = vi.fn();
    renderEditor({
      catalog: { kind: 'ready', revisions: catalog.revisions, nextCursor: 'next-page' },
      onLoadMore,
    });
    expect(screen.getByRole('button', { name: 'Match rows' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Stack rows' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Keep or exclude matches' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Load more published versions' }));
    expect(onLoadMore).toHaveBeenCalledOnce();
  });

  it('withholds candidates until exact catalog metadata is ready and reports a failed catalog', () => {
    const onCandidateChange = vi.fn<(intent: ConstructionCombineCandidateIntent | undefined) => void>();
    renderEditor({ catalog: { kind: 'failed', message: 'Snapshot expired.' }, onCandidateChange });
    expect(screen.getByRole('alert').textContent).toContain('Snapshot expired.');
    expect(onCandidateChange).not.toHaveBeenCalled();
    expect(screen.getByTestId('construction-combine-editor').querySelector('fieldset')?.disabled).toBe(true);
  });



  it('joins two current draft outputs using compiler-provided schema identities', () => {
    const onCandidateChange = renderEditor({
      catalog: { kind: 'ready', revisions: [] },
      workspaceInputs: [workspaceOutput('grouped-output', 'Grouped visits', 'group-patient-id'), workspaceOutput('demographics-output', 'Demographics', 'demographic-patient-id')],
    });
    fireEvent.click(screen.getByRole('button', { name: 'Match rows' }));
    const groupedKey = JSON.stringify(['WORKSPACE_OUTPUT', 'grouped-output']);
    const demographicsKey = JSON.stringify(['WORKSPACE_OUTPUT', 'demographics-output']);
    choose('Input table 1', groupedKey);
    choose('Input table 2', demographicsKey);
    choose('Matching pair 1 first field', 'group-patient-id');
    choose('Matching pair 1 second field', 'demographic-patient-id');
    fireEvent.click(screen.getByRole('button', { name: 'Add output field' }));
    choose('Output field 1 source field in input 1', 'group-patient-id');
    choose('Output field 1 name', 'patient_id');
    choose('Output field 1 label', 'Patient ID');

    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0]).toMatchObject({
      inputs: [
        { kind: 'WORKSPACE_OUTPUT', outputId: 'grouped-output' },
        { kind: 'WORKSPACE_OUTPUT', outputId: 'demographics-output' },
      ],
      operation: { combine: { kind: 'KEY_JOIN', keys: [{ leftColumnId: 'group-patient-id', rightColumnId: 'demographic-patient-id' }] } },
      outputs: [{ name: 'patient_id', type: 'string', nullable: false }],
    });
  });

  it('creates a membership filter over exact current-draft outputs with the selected mode', () => {
    const first = workspaceOutput('grouped-output', 'Grouped patients', 'group-patient-id');
    const second = workspaceOutput('reference-output', 'Reference patients', 'reference-patient-id');
    const onCandidateChange = renderEditor({ catalog: { kind: 'ready', revisions: [] }, workspaceInputs: [first, second] });

    fireEvent.click(screen.getByRole('button', { name: 'Keep or exclude matches' }));
    choose('Input table 1', JSON.stringify(['WORKSPACE_OUTPUT', 'grouped-output']));
    choose('Input table 2', JSON.stringify(['WORKSPACE_OUTPUT', 'reference-output']));
    choose('Matching pair 1 first field', 'group-patient-id');
    choose('Matching pair 1 second field', 'reference-patient-id');
    fireEvent.click(screen.getByRole('button', { name: 'Add output field' }));
    choose('Output field 1 source field in input 1', 'group-patient-id');
    choose('Output field 1 name', 'patient_id');
    choose('Output field 1 label', 'Patient ID');
    choose('Which rows should stay?', 'EXCLUDE');

    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0]).toMatchObject({
      inputs: [
        { kind: 'WORKSPACE_OUTPUT', outputId: 'grouped-output' },
        { kind: 'WORKSPACE_OUTPUT', outputId: 'reference-output' },
      ],
      operation: {
        kind: 'COMBINE',
        combine: {
          kind: 'MEMBERSHIP',
          membershipMode: 'EXCLUDE',
          keys: [{ leftColumnId: 'group-patient-id', rightColumnId: 'reference-patient-id' }],
          projections: [{ outputColumnId: expect.any(String), inputIndex: 0, inputColumnId: 'group-patient-id' }],
        },
      },
      outputs: [{ name: 'patient_id', label: 'Patient ID', type: 'string', nullable: false }],
    });
  });

  it('changes a saved membership mode without changing its exact inputs, keys, or output identity', () => {
    const draftInput = workspaceOutput('grouped-output', 'Grouped patients', 'group-patient-id');
    const savedStep: NonNullable<ConstructionCombineEditorProps['editingStep']> = {
      id: 'saved-membership',
      inputs: [
        { kind: 'WORKSPACE_OUTPUT', outputId: 'grouped-output' },
        { kind: 'TABLE_REVISION', tableId: 'table-right', revisionId: 'right-rev-1', outputId: 'right-output' },
      ],
      operation: {
        kind: 'COMBINE',
        combine: {
          kind: 'MEMBERSHIP',
          membershipMode: 'INCLUDE',
          keys: [{ leftColumnId: 'group-patient-id', rightColumnId: 'visit-patient-key' }],
          projections: [{ outputColumnId: 'saved-patient-id', inputIndex: 0, inputColumnId: 'group-patient-id' }],
        },
      },
      outputs: [{ id: 'saved-patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', nullable: false }],
    };
    const onCandidateChange = renderEditor({ editingStep: savedStep, workspaceInputs: [draftInput] });

    expect((screen.getByLabelText('Input table 1') as HTMLSelectElement).value).toBe(JSON.stringify(['WORKSPACE_OUTPUT', 'grouped-output']));
    expect((screen.getByLabelText('Input table 2') as HTMLSelectElement).value).toBe(refKey(rightOldRevision));
    expect((screen.getByLabelText('Which rows should stay?') as HTMLSelectElement).value).toBe('INCLUDE');
    expect(onCandidateChange).not.toHaveBeenCalled();

    choose('Which rows should stay?', 'EXCLUDE');
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0]).toMatchObject({
      id: 'saved-membership',
      inputs: [
        { kind: 'WORKSPACE_OUTPUT', outputId: 'grouped-output' },
        { kind: 'TABLE_REVISION', tableId: 'table-right', revisionId: 'right-rev-1', outputId: 'right-output' },
      ],
      operation: {
        combine: {
          kind: 'MEMBERSHIP',
          membershipMode: 'EXCLUDE',
          keys: [{ leftColumnId: 'group-patient-id', rightColumnId: 'visit-patient-key' }],
          projections: [{ outputColumnId: 'saved-patient-id', inputIndex: 0, inputColumnId: 'group-patient-id' }],
        },
      },
      outputs: [{ id: 'saved-patient-id', name: 'patient_id', type: 'string' }],
    });
  });

  it('keeps nullable and repeated fields unavailable as membership keys', () => {
    const left = {
      ...workspaceOutput('membership-left', 'Membership left', 'left-key'),
      columns: [
        { id: 'left-key', name: 'patient_id', label: 'Patient ID', type: 'string', nullable: false, repeated: false, cardinality: 'required_one' as const, joinCompatibilityKey: 'String' },
        { id: 'left-nullable', name: 'nullable_id', label: 'Nullable ID', type: 'string', nullable: true, repeated: false, cardinality: 'optional_one' as const, joinCompatibilityKey: 'String' },
        { id: 'left-repeated', name: 'repeated_id', label: 'Repeated ID', type: 'string', nullable: false, repeated: true, cardinality: 'many' as const, joinCompatibilityKey: 'String' },
      ],
    } satisfies ConstructionCombineWorkspaceOutput;
    const right = {
      ...workspaceOutput('membership-right', 'Membership right', 'right-key'),
      columns: [
        { id: 'right-key', name: 'patient_id', label: 'Patient ID', type: 'string', nullable: false, repeated: false, cardinality: 'required_one' as const, joinCompatibilityKey: 'String' },
        { id: 'right-nullable', name: 'nullable_id', label: 'Nullable ID', type: 'string', nullable: true, repeated: false, cardinality: 'optional_one' as const, joinCompatibilityKey: 'String' },
        { id: 'right-repeated', name: 'repeated_id', label: 'Repeated ID', type: 'string', nullable: false, repeated: true, cardinality: 'many' as const, joinCompatibilityKey: 'String' },
      ],
    } satisfies ConstructionCombineWorkspaceOutput;
    renderEditor({ catalog: { kind: 'ready', revisions: [] }, workspaceInputs: [left, right] });

    fireEvent.click(screen.getByRole('button', { name: 'Keep or exclude matches' }));
    choose('Input table 1', JSON.stringify(['WORKSPACE_OUTPUT', 'membership-left']));
    choose('Input table 2', JSON.stringify(['WORKSPACE_OUTPUT', 'membership-right']));

    for (const fieldId of ['left-nullable', 'left-repeated']) {
      expect(screen.getByLabelText('Matching pair 1 first field').querySelector(`option[value="${fieldId}"]`)).toBeNull();
    }
    for (const fieldId of ['right-nullable', 'right-repeated']) {
      expect(screen.getByLabelText('Matching pair 1 second field').querySelector(`option[value="${fieldId}"]`)).toBeNull();
    }
  });



  it('allows nullable scalar KEY_JOIN fields by base type without changing MEMBERSHIP rules', () => {
    const nullableLeft = { ...workspaceOutput('nullable-left', 'Nullable left', 'left-nullable-id'), columns: [{ id: 'left-nullable-id', name: 'code', label: 'Code', type: 'string', nullable: true, repeated: false, cardinality: 'optional_one' as const, joinCompatibilityKey: 'String' }] };
    const requiredRight = workspaceOutput('required-right', 'Required right', 'right-required-id');
    const onCandidateChange = renderEditor({ catalog: { kind: 'ready', revisions: [] }, workspaceInputs: [nullableLeft, requiredRight] });
    fireEvent.click(screen.getByRole('button', { name: 'Match rows' }));
    choose('Input table 1', JSON.stringify(['WORKSPACE_OUTPUT', 'nullable-left']));
    choose('Input table 2', JSON.stringify(['WORKSPACE_OUTPUT', 'required-right']));
    expect(screen.getByText(/A NULL key never matches another NULL key/)).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /Code · String/ })).toBeInTheDocument();
    choose('Matching pair 1 first field', 'left-nullable-id');
    choose('Matching pair 1 second field', 'right-required-id');
    fireEvent.click(screen.getByRole('button', { name: 'Add output field' }));
    choose('Output field 1 source field in input 1', 'left-nullable-id');
    choose('Output field 1 name', 'code');
    choose('Output field 1 label', 'Code');
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0].operation.combine).toMatchObject({
      kind: 'KEY_JOIN',
      keys: [{ leftColumnId: 'left-nullable-id', rightColumnId: 'right-required-id' }],
    });
  });

  it('matches nullable and non-null published key fields by their supported base type', () => {
    const nullableLeft: ConstructionCombinePublishedRevision = {
      ...leftRevision,
      columns: [column('nullable-left-key', 'external_id', 'string', 'Nullable(String)', { nullable: true })],
    };
    const requiredRight: ConstructionCombinePublishedRevision = {
      ...rightOldRevision,
      columns: [column('required-right-key', 'patient_id', 'string', 'String')],
    };
    const onCandidateChange = renderEditor({ catalog: { kind: 'ready', revisions: [nullableLeft, requiredRight] } });
    fireEvent.click(screen.getByRole('button', { name: 'Match rows' }));
    choose('Input table 1', refKey(nullableLeft));
    choose('Input table 2', refKey(requiredRight));
    choose('Matching pair 1 first field', 'nullable-left-key');
    choose('Matching pair 1 second field', 'required-right-key');
    fireEvent.click(screen.getByRole('button', { name: 'Add output field' }));
    choose('Output field 1 source field in input 1', 'nullable-left-key');
    choose('Output field 1 name', 'external_id');
    choose('Output field 1 label', 'External ID');
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0].operation.combine).toMatchObject({
      kind: 'KEY_JOIN',
      keys: [{ leftColumnId: 'nullable-left-key', rightColumnId: 'required-right-key' }],
    });
  });

  it('stacks three current draft outputs and restores saved workspace bindings while published tables load', () => {
    const inputs = [
      { ...workspaceOutput('cohort-a', 'Cohort A', 'age-a', 'age'), columns: [{ id: 'age-a', name: 'age', label: 'Age', type: 'integer', nullable: false, repeated: false, cardinality: 'required_one', appendCompatibilityKey: 'integer:Int64' }] },
      { ...workspaceOutput('cohort-b', 'Cohort B', 'age-b', 'age'), columns: [{ id: 'age-b', name: 'age', label: 'Age', type: 'integer', nullable: false, repeated: false, cardinality: 'required_one', appendCompatibilityKey: 'integer:Int64' }] },
      { ...workspaceOutput('cohort-c', 'Cohort C', 'age-c', 'age'), columns: [{ id: 'age-c', name: 'age', label: 'Age', type: 'integer', nullable: false, repeated: false, cardinality: 'required_one', appendCompatibilityKey: 'integer:Int64' }] },
    ] satisfies ConstructionCombineWorkspaceOutput[];
    const savedStep: NonNullable<ConstructionCombineEditorProps['editingStep']> = {
      id: 'saved-workspace-append',
      inputs: [{ kind: 'WORKSPACE_OUTPUT', outputId: 'cohort-a' }, { kind: 'WORKSPACE_OUTPUT', outputId: 'cohort-b' }],
      operation: { kind: 'COMBINE', combine: { kind: 'APPEND', projections: [{ outputColumnId: 'saved-age', inputIndex: 0, inputColumnId: 'age-a' }, { outputColumnId: 'saved-age', inputIndex: 1, inputColumnId: 'age-b' }] } },
      outputs: [{ id: 'saved-age', name: 'age', label: 'Age', type: 'integer', nullable: false }],
    };
    const onCandidateChange = renderEditor({ catalog: { kind: 'loading' }, workspaceInputs: inputs, editingStep: savedStep });
    expect(screen.getByTestId('construction-combine-editor').querySelector('fieldset')).not.toBeDisabled();
    expect((screen.getByLabelText('Input table 1') as HTMLSelectElement).value).toBe(JSON.stringify(['WORKSPACE_OUTPUT', 'cohort-a']));
    expect((screen.getByLabelText('Input table 2') as HTMLSelectElement).value).toBe(JSON.stringify(['WORKSPACE_OUTPUT', 'cohort-b']));
    expect(onCandidateChange).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Add another table' }));
    choose('Input table 3', JSON.stringify(['WORKSPACE_OUTPUT', 'cohort-c']));
    choose('Output field 1 matching field in input 3', 'column:age-c');
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0]).toMatchObject({
      id: 'saved-workspace-append',
      inputs: [
        { kind: 'WORKSPACE_OUTPUT', outputId: 'cohort-a' },
        { kind: 'WORKSPACE_OUTPUT', outputId: 'cohort-b' },
        { kind: 'WORKSPACE_OUTPUT', outputId: 'cohort-c' },
      ],
      operation: { combine: { kind: 'APPEND' } },
      outputs: [{ id: 'saved-age', name: 'age', type: 'integer' }],
    });
  });

  it('allows draft-only Combine while published catalog is still loading', () => {
    renderEditor({
      catalog: { kind: 'loading' },
      workspaceInputs: [workspaceOutput('draft-only', 'Current draft table', 'draft-id')],
    });
    expect(screen.getByRole('button', { name: 'Match rows' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'Match rows' }));
    choose('Input table 1', JSON.stringify(['WORKSPACE_OUTPUT', 'draft-only']));
    expect((screen.getByLabelText('Input table 1') as HTMLSelectElement).value).toBe(JSON.stringify(['WORKSPACE_OUTPUT', 'draft-only']));
  });

  it('waits for exact published pins before enabling a saved APPEND despite unrelated workspace outputs', () => {
    const savedStep: NonNullable<ConstructionCombineEditorProps['editingStep']> = {
      id: 'saved-append-three-pins',
      inputs: [
        { kind: 'TABLE_REVISION', tableId: 'table-left', revisionId: 'left-rev-1', outputId: 'left-output' },
        { kind: 'TABLE_REVISION', tableId: 'table-right', revisionId: 'right-rev-1', outputId: 'right-output' },
        { kind: 'TABLE_REVISION', tableId: 'table-third', revisionId: 'third-rev-1', outputId: 'third-output' },
      ],
      operation: {
        kind: 'COMBINE',
        combine: {
          kind: 'APPEND',
          projections: [{ outputColumnId: 'saved-age-three', inputIndex: 0, inputColumnId: 'age' }],
        },
      },
      outputs: [{ id: 'saved-age-three', name: 'age', label: 'Age', type: 'integer', nullable: true }],
    };
    const workspaceInputs = [workspaceOutput('unrelated-draft', 'Unrelated draft output', 'draft-age', 'age')];
    const onCandidateChange = vi.fn<(intent: ConstructionCombineCandidateIntent | undefined) => void>();
    const { rerender } = render(
      <ConstructionCombineEditor
        catalog={{ kind: 'loading' }}
        workspaceInputs={workspaceInputs}
        constructionVersion={7}
        editingStep={savedStep}
        disabled={false}
        onCandidateChange={onCandidateChange}
      />,
    );

    expect(screen.getByTestId('construction-combine-editor').querySelector('fieldset')).toBeDisabled();
    expect(onCandidateChange).not.toHaveBeenCalled();

    rerender(
      <ConstructionCombineEditor
        catalog={{ kind: 'ready', revisions: [leftRevision, rightOldRevision, thirdRevision] }}
        workspaceInputs={workspaceInputs}
        constructionVersion={7}
        editingStep={savedStep}
        disabled={false}
        onCandidateChange={onCandidateChange}
      />,
    );

    expect(screen.getByLabelText('Input table 1')).toHaveProperty('value', refKey(leftRevision));
    expect(screen.getByLabelText('Input table 2')).toHaveProperty('value', refKey(rightOldRevision));
    expect(screen.getByLabelText('Input table 3')).toHaveProperty('value', refKey(thirdRevision));
    expect(screen.getByLabelText('Output field 1 matching field in input 2')).toHaveProperty('value', 'empty-for-this-table');
    expect(screen.getByLabelText('Output field 1 matching field in input 3')).toHaveProperty('value', 'empty-for-this-table');
    expect(onCandidateChange).not.toHaveBeenCalled();

    choose('Output field 1 label', 'Updated age');
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0]).toMatchObject({
      id: 'saved-append-three-pins',
      inputs: [
        { kind: 'TABLE_REVISION', tableId: 'table-left', revisionId: 'left-rev-1', outputId: 'left-output' },
        { kind: 'TABLE_REVISION', tableId: 'table-right', revisionId: 'right-rev-1', outputId: 'right-output' },
        { kind: 'TABLE_REVISION', tableId: 'table-third', revisionId: 'third-rev-1', outputId: 'third-output' },
      ],
      operation: { combine: { kind: 'APPEND', projections: [{ outputColumnId: 'saved-age-three', inputIndex: 0, inputColumnId: 'age' }] } },
      outputs: [{ id: 'saved-age-three', name: 'age', label: 'Updated age', nullable: true }],
    });
  });

  it('reopens a saved combine step with its exact pins and stable output IDs', () => {
    const savedStep: NonNullable<ConstructionCombineEditorProps['editingStep']> = {
      id: 'saved-combine',
      inputs: [
        { kind: 'TABLE_REVISION', tableId: 'table-left', revisionId: 'left-rev-1', outputId: 'left-output' },
        { kind: 'TABLE_REVISION', tableId: 'table-right', revisionId: 'right-rev-1', outputId: 'right-output' },
      ],
      operation: {
        kind: 'COMBINE',
        combine: {
          kind: 'KEY_JOIN',
          keys: [{ leftColumnId: 'patient-key', rightColumnId: 'visit-patient-key' }],
          projections: [{ outputColumnId: 'saved-visit-day', inputIndex: 1, inputColumnId: 'visit-day' }],
          joinType: 'INNER',
          rightMatchPolicy: 'PRESERVE_ALL',
        },
      },
      outputs: [{ id: 'saved-visit-day', name: 'visit_day', label: 'Visit day', type: 'date', nullable: false }],
    };
    const onCandidateChange = renderEditor({ editingStep: savedStep });

    expect((screen.getByLabelText('Input table 1') as HTMLSelectElement).value).toBe(refKey(leftRevision));
    expect((screen.getByLabelText('Input table 2') as HTMLSelectElement).value).toBe(refKey(rightOldRevision));
    expect((screen.getByLabelText('Output field 1 name') as HTMLInputElement).value).toBe('visit_day');
    expect(onCandidateChange).not.toHaveBeenCalled();
    choose('Output field 1 label', 'Updated visit day');
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0]).toMatchObject({
      id: 'saved-combine',
      operation: {
        combine: {
          kind: 'KEY_JOIN',
          projections: [{ outputColumnId: 'saved-visit-day', inputIndex: 1, inputColumnId: 'visit-day' }],
        },
      },
      outputs: [{ id: 'saved-visit-day', name: 'visit_day', label: 'Updated visit day' }],
    });
  });
});
