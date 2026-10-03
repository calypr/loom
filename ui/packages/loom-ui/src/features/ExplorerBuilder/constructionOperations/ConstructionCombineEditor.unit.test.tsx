// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ConstructionCombineEditor, type ConstructionCombineEditorProps } from './ConstructionCombineEditor';
import type {
  ConstructionCombineCandidateIntent,
  ConstructionCombineCatalog,
  ConstructionCombinePublishedRevision,
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
    column('age', 'age', 'integer', 'Int32'),
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
  columns: [column('third-patient-key', 'patient_id', 'string', 'String'), column('third-age', 'age', 'integer', 'Int32')],
};

const catalog: ConstructionCombineCatalog = {
  kind: 'ready',
  revisions: [leftRevision, rightOldRevision, rightCurrentRevision, thirdRevision],
};

const refKey = (revision: ConstructionCombinePublishedRevision): string =>
  JSON.stringify([revision.tableId, revision.revisionId, revision.outputId]);

const renderEditor = (options: {
  readonly catalog?: ConstructionCombineCatalog;
  readonly editingStep?: ConstructionCombineEditorProps['editingStep'];
  readonly disabled?: boolean;
  readonly onCandidateChange?: CandidateMock;
} = {}) => {
  const onCandidateChange = options.onCandidateChange ?? vi.fn<(intent: ConstructionCombineCandidateIntent | undefined) => void>();
  render(
    <ConstructionCombineEditor
      catalog={options.catalog ?? catalog}
      constructionVersion={7}
      editingStep={options.editingStep}
      disabled={options.disabled ?? false}
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

  it('requires compatible mappings from every input before emitting an APPEND candidate', () => {
    const onCandidateChange = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Stack rows' }));
    choose('Input table 1', refKey(leftRevision));
    choose('Input table 2', refKey(thirdRevision));
    fireEvent.click(screen.getByRole('button', { name: 'Add output field' }));
    choose('Output field 1 matching field in input 1', 'age');
    choose('Output field 1 name', 'age');
    choose('Output field 1 label', 'Age');
    expect(lastCandidate(onCandidateChange)).toBeUndefined();

    choose('Output field 1 matching field in input 2', 'third-age');
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

  it('keeps membership outputs on the first table and does not allow nullable or repeated matching keys', () => {
    const onCandidateChange = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Keep or exclude matches' }));
    choose('Input table 1', refKey(leftRevision));
    choose('Input table 2', refKey(rightOldRevision));
    choose('Matching pair 1 first field', 'patient-key');
    choose('Matching pair 1 second field', 'visit-patient-key');
    choose('Which rows should stay?', 'EXCLUDE');
    fireEvent.click(screen.getByRole('button', { name: 'Add output field' }));
    choose('Output field 1 source field in input 1', 'age');
    choose('Output field 1 name', 'age');
    choose('Output field 1 label', 'Age');
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0].operation).toMatchObject({
      kind: 'COMBINE',
      combine: {
        kind: 'MEMBERSHIP',
        membershipMode: 'EXCLUDE',
        projections: [{ inputIndex: 0, inputColumnId: 'age', outputColumnId: expect.any(String) }],
      },
    });
    expect(screen.queryByLabelText('Output field 1 source field in input 2')).toBeNull();
    expect(screen.getByLabelText('Matching pair 1 second field').querySelector('option[value="visit-tags"]')).toBeNull();
    expect(screen.getByLabelText('Matching pair 1 first field').querySelector('option[value="nullable-code"]')).toBeNull();
  });

  it('withholds candidates until exact catalog metadata is ready and reports a failed catalog', () => {
    const onCandidateChange = vi.fn<(intent: ConstructionCombineCandidateIntent | undefined) => void>();
    renderEditor({ catalog: { kind: 'failed', message: 'Snapshot expired.' }, onCandidateChange });
    expect(screen.getByRole('alert').textContent).toContain('Snapshot expired.');
    expect(onCandidateChange).toHaveBeenCalledWith(undefined);
    expect(screen.getByTestId('construction-combine-editor').querySelector('fieldset')?.disabled).toBe(true);
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
    expect(lastCandidate(onCandidateChange)?.candidateConstruction.steps[0]).toMatchObject({
      id: 'saved-combine',
      operation: {
        combine: {
          kind: 'KEY_JOIN',
          projections: [{ outputColumnId: 'saved-visit-day', inputIndex: 1, inputColumnId: 'visit-day' }],
        },
      },
      outputs: [{ id: 'saved-visit-day', name: 'visit_day', label: 'Visit day' }],
    });
  });
});
