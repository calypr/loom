// @vitest-environment jsdom
// Adversarial rev07 probes (read-only review; only this file is new).
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  type Construction,
  type ConstructionCapabilitiesResponse,
  type ConstructionProposalRequest,
  type ConstructionStageDescriptor,
} from '../../../types';
import { ConstructionOperationEditor } from './ConstructionOperationEditor';
import { ConstructionReshapeEditor, type ConstructionReshapeEditorProps } from './ConstructionReshapeEditor';

type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'filterSources' | 'deriveSources'
>;

const projection = (columnId: string, fieldPath: string): NonNullable<Construction['sourceProjections']>[number] => ({
  columnId,
  occurrenceId: `occ-${columnId}`,
  fieldPath,
  fhirType: 'string',
  logicalType: 'string',
  label: fieldPath,
});

const sourceStage = (cols: ConstructionStageDescriptor['columns']): ConstructionStageDescriptor => ({
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  columns: cols,
  capabilities: [
    { kind: 'FILTER', supported: true },
    { kind: 'DERIVE', supported: true },
    { kind: 'PIVOT', supported: false, reason: 'Not needed in this test.' },
    { kind: 'UNPIVOT', supported: false, reason: 'Not needed in this test.' },
  ],
});

const renderEditor = (args: {
  readonly family: 'KEEP_ROWS' | 'CALCULATE';
  readonly construction?: Construction;
  readonly stageColumns: ConstructionStageDescriptor['columns'];
  readonly filterSourceInput?: ConstructionCapabilitiesResponse['filterSourceInput'];
  readonly deriveSourceInput?: ConstructionCapabilitiesResponse['deriveSourceInput'];
  readonly onCandidateChange?: (candidate: CandidateIntent | undefined) => void;
}) => {
  const selectedStage = sourceStage(args.stageColumns);
  const onCandidateChange = args.onCandidateChange ?? vi.fn();
  render(
    <ConstructionOperationEditor
      family={args.family}
      construction={args.construction ?? { version: 1, steps: [] }}
      capabilities={{
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'draft-digest-1',
        outputId: 'table-1',
        stageId: selectedStage.id,
        baseConstruction: { version: 1, steps: [] },
        stages: [selectedStage],
        selectedStage,
        groupFieldChoices: [],
        groupRowValueChoices: [],
        workspaceInputs: [],
        ...(args.filterSourceInput ? { filterSourceInput: args.filterSourceInput } : {}),
        ...(args.deriveSourceInput ? { deriveSourceInput: args.deriveSourceInput } : {}),
      }}
      editingStep={args.editingStep}
      disabled={false}
      onCandidateChange={onCandidateChange}
      onEditStep={vi.fn()}
    />,
  );
  return onCandidateChange;
};

const filterColumnSelect = (): HTMLSelectElement =>
  screen.getByRole('combobox', { name: 'Column' }) as HTMLSelectElement;

const operandColumn = (label: string): HTMLSelectElement =>
  screen.getByRole('combobox', { name: `${label} column` }) as HTMLSelectElement;

const operandKind = (label: string): HTMLSelectElement =>
  screen.getByRole('combobox', { name: `${label} kind` }) as HTMLSelectElement;

const stringCol = (id: string, label: string) => ({ id, name: id, label, type: 'string' });
const intCol = (id: string, label: string) => ({ id, name: id, label, type: 'integer' });
const decCol = (id: string, label: string) => ({ id, name: id, label, type: 'decimal' });

const filterChoice = (choiceId: string, fieldPath: string, label: string, isPopulated = true, columnId?: string) => ({
  choiceId,
  fieldPath,
  label,
  logicalType: 'integer' as const,
  isPopulated,
  ...(columnId ? { columnId } : {}),
});

const deriveChoice = (choiceId: string, fieldPath: string, label: string, isPopulated = true, columnId?: string) => ({
  choiceId,
  fieldPath,
  label,
  logicalType: 'decimal' as const,
  isPopulated,
  ...(columnId ? { columnId } : {}),
});

const withProjections = (projections: Construction['sourceProjections']): Construction => ({
  version: 1,
  steps: [],
  ...(projections ? { sourceProjections: projections } : {}),
});

const optionValues = (select: HTMLSelectElement) => [...select.options].map((option) => option.value);
const explanationNodes = () => screen.queryAllByTestId('source-picker-explanation');
const allBoundText = () =>
  screen.queryByText('Every eligible dataset field that contains values is already used by another source selection here. Remove one to choose a different field.');
const nonePopulatedText = () =>
  screen.queryByText('No eligible dataset field contains values in this dataset yet.');

// --- Reshape harness (unpivot lane) ---

const unpivotStageFor = (cols: ConstructionReshapeEditorProps['capabilities']['selectedStage']['columns']): ConstructionReshapeEditorProps['capabilities']['selectedStage'] => ({
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  columns: cols,
  capabilities: [
    { kind: 'FILTER', supported: false, reason: 'Not needed in this test.' },
    { kind: 'DERIVE', supported: false, reason: 'Not needed in this test.' },
    { kind: 'PIVOT', supported: false, reason: 'Not needed in this test.' },
    { kind: 'UNPIVOT', supported: true },
    { kind: 'GROUP', supported: false, reason: 'Not needed in this test.' },
    { kind: 'EXPAND', supported: false, reason: 'Not needed in this test.' },
  ],
});

const populatedChoice = (choiceId: string, fieldPath: string, columnId: string, isPopulated = true) => ({
  choiceId,
  fieldPath,
  label: fieldPath.split('.').at(-1) ?? fieldPath,
  logicalType: 'decimal' as const,
  isPopulated,
  columnId,
});

const renderUnpivot = (args: {
  readonly construction: Construction;
  readonly choices: ReturnType<typeof populatedChoice>[];
  readonly columns?: ConstructionReshapeEditorProps['capabilities']['selectedStage']['columns'];
  readonly selectedColumns?: ReadonlyArray<string>;
}) => {
  const cols = args.columns ?? [
    { id: 'opaque-a', name: 'col_opaque_a', label: 'Patient reference', type: 'string' as const, cardinality: 'required_one' as const },
    { id: 'opaque-b', name: 'col_opaque_b', label: 'Melt two', type: 'string' as const, cardinality: 'required_one' as const },
  ];
  const stage = unpivotStageFor(cols);
  render(
    <ConstructionReshapeEditor
      construction={args.construction}
      capabilities={{
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'draft-digest-1',
        outputId: 'table-1',
        stageId: stage.id,
        baseConstruction: { version: 1, steps: [] },
        stages: [stage],
        selectedStage: stage,
        groupFieldChoices: [],
        groupRowValueChoices: [],
        workspaceInputs: [],
        unpivotSourceInput: { supported: true, stageId: 'source_projection', choices: args.choices },
      }}
      selectedColumns={args.selectedColumns ?? ['opaque-a']}
      initialKind="unpivot"
      disabled={false}
      onCandidateChange={vi.fn()}
      onEditStep={vi.fn()}
    />,
  );
  const summary = screen.queryByText('Advanced options: field names and missing values');
  if (summary) fireEvent.click(summary);
};

const unpivotSourceGroup = (index: number): HTMLOptGroupElement | null => {
  const select = screen.getByTestId(`construction-reshape-unpivot-input-${index}-column`) as HTMLSelectElement;
  return select.querySelector('optgroup[label="Fields from your data"]');
};

const unpivotTableGroup = (index: number): HTMLOptGroupElement | null => {
  const select = screen.getByTestId(`construction-reshape-unpivot-input-${index}-column`) as HTMLSelectElement;
  return select.querySelector('optgroup[label="Fields in this table"]');
};

afterEach(cleanup);

// ---------------------------------------------------------------------------
// ATTACK 1 — label-matching must never drive reuse.
// col-age (label 'root.note' = another choice's fieldPath) carries root.age;
// col-note has label 'Age' = choice A's label. Reuse must follow fieldPath only.
// ---------------------------------------------------------------------------
describe('ATTACK 1 label-matching cannot cross-suppress', () => {
  const choiceA = filterChoice('choice-a', 'root.age', 'Age');
  const choiceB = filterChoice('choice-b', 'root.note', 'Note');

  it('suppression follows projection fieldPath only, never column labels', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('col-age', 'root.age')]),
      // col-age deliberately labelled with choice B's fieldPath; col-note labelled like choice A.
      stageColumns: [intCol('col-age', 'root.note'), intCol('col-note', 'Age')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA, choiceB] },
    });
    const values = optionValues(filterColumnSelect());
    expect(values).not.toContain('source:choice-a'); // its fieldPath is projected → reused
    expect(values).toContain('source:choice-b'); // root.note NOT projected → must stay offered
  });

  it('mirrored: label-equals-fieldPath on an unprojected column does not suppress the projected field choice', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('col-note', 'root.note')]),
      stageColumns: [intCol('col-age', 'root.note'), intCol('col-note', 'Age')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA, choiceB] },
    });
    const values = optionValues(filterColumnSelect());
    expect(values).toContain('source:choice-a'); // root.age never projected
    expect(values).not.toContain('source:choice-b'); // its exact fieldPath IS projected
  });
});

// ---------------------------------------------------------------------------
// ATTACK 2 — projection ambiguity rules.
// ---------------------------------------------------------------------------
describe('ATTACK 2 ambiguity: 3-way and duplicate-columnId payloads', () => {
  const choiceA = filterChoice('choice-a', 'root.age', 'Age');

  it('two live + one stale projection for one fieldPath keeps the signed choice (usable==2)', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([
        projection('col-a', 'root.age'),
        projection('col-b', 'root.age'),
        projection('col-gone', 'root.age'),
      ]),
      stageColumns: [intCol('col-a', 'Age'), intCol('col-b', 'Age copy'), stringCol('other', 'Other')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA] },
    });
    expect(optionValues(filterColumnSelect())).toContain('source:choice-a');
  });

  it('two live projection entries sharing ONE columnId value: falls back to signed (reported behaviour)', () => {
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([
        projection('col-dup', 'root.age'),
        projection('col-dup', 'root.age'),
      ]),
      stageColumns: [intCol('col-dup', 'Age'), stringCol('other', 'Other')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA] },
    });
    // Entry-count ambiguity → usable.length === 2 → no reuse → signed kept, nothing suppressed.
    const values = optionValues(filterColumnSelect());
    expect(values).toContain('source:choice-a');
    expect(values).toContain('col-dup'); // the column is merely an ordinary stage column
  });
});

// ---------------------------------------------------------------------------
// ATTACK 3 — suppression must never masquerade as a 06 emptiness class.
// ---------------------------------------------------------------------------
describe('ATTACK 3 banner honesty with reused choices', () => {
  it('filter lane: all reused, none bound → no banner anywhere', () => {
    const choiceA = filterChoice('choice-a', 'root.age', 'Age');
    const choiceB = filterChoice('choice-b', 'root.note', 'Note');
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('col-age', 'root.age'), projection('col-note', 'root.note')]),
      stageColumns: [intCol('col-age', 'Age'), intCol('col-note', 'Note')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA, choiceB] },
    });
    expect(explanationNodes()).toHaveLength(0);
    expect(allBoundText()).toBeNull();
    expect(nonePopulatedText()).toBeNull();
  });

  it('filter lane: all reused + one stale unpopulated choice → still no dishonest banner', () => {
    const choiceA = filterChoice('choice-a', 'root.age', 'Age');
    const choiceB = filterChoice('choice-b', 'root.note', 'Note');
    const stale = filterChoice('choice-c', 'root.dead', 'Dead', false);
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([projection('col-age', 'root.age'), projection('col-note', 'root.note')]),
      stageColumns: [intCol('col-age', 'Age'), intCol('col-note', 'Note')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [choiceA, choiceB, stale] },
    });
    // 'all-bound' would be a lie (nothing bound); 'none-populated' would be a lie (A,B populated).
    expect(allBoundText()).toBeNull();
    expect(nonePopulatedText()).toBeNull();
    expect(explanationNodes()).toHaveLength(0);
  });

  it('derive lane: all reused, column-kind operand, no bindings → no banner', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age');
    const b = deriveChoice('choice-b', 'root.note', 'Note');
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('w-age', 'root.age'), projection('w-note', 'root.note')]),
      stageColumns: [decCol('w-age', 'AgeN'), decCol('w-note', 'NoteN')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a, b] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    expect(explanationNodes()).toHaveLength(0);
    expect(allBoundText()).toBeNull();
    expect(nonePopulatedText()).toBeNull();
  });

  it('unpivot lane: reused column as the only input → no banner, source group empty', () => {
    renderUnpivot({
      construction: withProjections([projection('opaque-a', 'root.value')]),
      choices: [populatedChoice('choice-a', 'root.value', 'unpivot-input')],
      selectedColumns: [],
    });
    fireEvent.click(screen.getByLabelText('Unpivot Patient reference'));
    expect(explanationNodes()).toHaveLength(0);
    expect(allBoundText()).toBeNull();
    expect(unpivotSourceGroup(0)?.querySelectorAll('option').length ?? 0).toBe(0);
    expect([...(unpivotTableGroup(0)?.querySelectorAll('option') ?? [])]
      .some((option) => option.value === 'opaque-a')).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// ATTACK 4 — derive lane paired-operand consistency.
// ---------------------------------------------------------------------------
describe('ATTACK 4 derive operand pair consistency', () => {
  it('both operands open on the SAME reused existing column: no source option anywhere', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age');
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('weight-id', 'root.age')]),
      stageColumns: [intCol('weight-id', 'Weight'), stringCol('status-id', 'Status')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    const left = optionValues(operandColumn('First value'));
    const right = optionValues(operandColumn('Second value'));
    expect(left).toEqual(['weight-id']);
    expect(right).toEqual(['weight-id']);
    expect(left.some((v) => v.startsWith('source:'))).toBe(false);
    expect(right.some((v) => v.startsWith('source:'))).toBe(false);
    expect(allBoundText()).toBeNull();
  });

  it('left reuses column while right binds a DIFFERENT choice: suppression identical, no false all-bound', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age');
    const b = deriveChoice('choice-b', 'root.weight', 'Weight', true, 'derive-input_weight');
    const onCandidateChange = renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('age-col', 'root.age')]),
      stageColumns: [intCol('age-col', 'Age')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a, b] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-b' } });
    const left = optionValues(operandColumn('First value'));
    const right = optionValues(operandColumn('Second value'));
    // Neither operand may present the signed choice for the projected field.
    expect(left).not.toContain('source:choice-a');
    expect(right).not.toContain('source:choice-a');
    expect(right).toContain('derive-input_weight'); // right keeps its materialized input
    expect(allBoundText()).toBeNull();
    const calls = (onCandidateChange as ReturnType<typeof vi.fn>).mock.calls.map((call) => call[0]);
    const candidate = [...calls].reverse().find((entry) => entry !== undefined);
    const bindings = candidate?.deriveSources ?? [];
    expect(bindings.map((binding) => binding.choiceId)).toEqual(['choice-b']);
    expect(bindings.every((binding) => binding.columnId !== 'age-col')).toBe(true);
  });

  it('reverse: bind first via source, then the projection lands — pair stays consistent, selection not rewritten', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age', true, 'derive-input_age');
    const stageColumns = [decCol('plain-n', 'PlainN')];
    // Render #1: no projection yet → signed choice is offered; bind Second value via source.
    const { rerenderPair } = renderPair({ stageColumns, choices: [a], projections: undefined });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-a' } });
    expect((screen.getByRole('combobox', { name: 'Second value column' }) as HTMLSelectElement).value)
      .toBe('derive-input_age');
    // Render #2: same session, capabilities unchanged, server has now recorded the projection
    // onto the operand's own materialized column.
    rerenderPair({ stageColumns, choices: [a], projections: [projection('derive-input_age', 'root.age')] });
    const left = optionValues(operandColumn('First value'));
    const right = optionValues(operandColumn('Second value')) ;
    expect((screen.getByRole('combobox', { name: 'Second value column' }) as HTMLSelectElement).value)
      .toBe('derive-input_age'); // selection never silently rewritten
    expect(left.some((v) => v.startsWith('source:'))).toBe(false);
    expect(right).not.toContain('source:choice-a');
  });

  it('pair symmetry single-open-lane: identical option sets for left and right with mixed choices', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age');
    const b = deriveChoice('choice-b', 'root.weight', 'Weight');
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('age-col', 'root.age')]),
      stageColumns: [intCol('age-col', 'Age')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a, b] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    expect(optionValues(operandColumn('First value'))).toEqual(optionValues(operandColumn('Second value')));
  });

  it('no numeric column AND all choices reused → Numeric-column shortcut cannot materialize, no crash', () => {
    const a = deriveChoice('choice-a', 'root.age', 'Age', true, 'derive-input_age');
    const onCandidateChange = renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('status-id', 'root.age')]),
      stageColumns: [stringCol('status-id', 'Status')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a] },
    });
    // status-id is string → not a usable numeric operand → reuse must NOT fire; signed kept.
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    // With no numeric column, the kind shortcut materializes the still-offered signed choice:
    // the fallback signed flow is intact (not suppressed away) and materializes exactly once.
    const signedFirst = optionValues(operandColumn('First value'));
    expect(signedFirst).toEqual(['derive-input_age']);
    expect(signedFirst.some((v) => v.startsWith('source:'))).toBe(false);
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    const signedCalls = (onCandidateChange as ReturnType<typeof vi.fn>).mock.calls.map((call) => call[0]);
    const signedCandidate = [...signedCalls].reverse().find((entry) => entry !== undefined);
    expect((signedCandidate?.deriveSources ?? []).map((binding) => binding.choiceId)).toEqual(['choice-a']);
    // Now make the column numeric: reuse fires → nothing numeric selectable + nothing offered.
    cleanup();
    const on2 = renderEditor({
      family: 'CALCULATE',
      construction: withProjections([projection('age-num', 'root.age')]),
      stageColumns: [decCol('age-num', 'AgeN')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [a] },
    });
    const kind = operandKind('First value');
    const numericOption = [...kind.options].find((option) => option.value === 'column');
    // firstNumeric exists → shortcut enabled; select it and confirm single reuse column, no source materialization.
    expect(numericOption?.disabled).toBe(false);
    fireEvent.change(kind, { target: { value: 'column' } });
    expect(optionValues(operandColumn('First value'))).toEqual(['age-num']);
    const calls = (on2 as ReturnType<typeof vi.fn>).mock.calls.map((call) => call[0]);
    const candidate = [...calls].reverse().find((entry) => entry !== undefined);
    expect(candidate?.deriveSources ?? []).toEqual([]); // column reuse must not bind a source
    void onCandidateChange;
  });
});

// small helper for the rerender flow in the reverse-order probe
function renderPair(args: {
  readonly stageColumns: ConstructionStageDescriptor['columns'];
  readonly choices: ReturnType<typeof deriveChoice>[];
  readonly projections?: Construction['sourceProjections'];
}) {
  const selectedStage = sourceStage(args.stageColumns);
  const capabilities: ConstructionCapabilitiesResponse = {
    snapshotToken: 'snapshot-1',
    draftVersion: 1,
    draftDigest: 'draft-digest-1',
    outputId: 'table-1',
    stageId: selectedStage.id,
    baseConstruction: { version: 1, steps: [] },
    stages: [selectedStage],
    selectedStage,
    groupFieldChoices: [],
    groupRowValueChoices: [],
    workspaceInputs: [],
    deriveSourceInput: { supported: true, stageId: 'source_projection', choices: args.choices },
  };
  const initial = (
    <ConstructionOperationEditor
      family="CALCULATE"
      construction={withProjections(args.projections)}
      capabilities={capabilities}
      editingStep={undefined}
      disabled={false}
      onCandidateChange={vi.fn()}
      onEditStep={vi.fn()}
    />
  );
  const view = render(initial);
  return {
    rerender: () => undefined,
    rerenderPair: (next: { stageColumns: ConstructionStageDescriptor['columns']; choices: ReturnType<typeof deriveChoice>[]; projections?: Construction['sourceProjections'] }) => {
      const stage = sourceStage(next.stageColumns);
      view.rerender(
        <ConstructionOperationEditor
          family="CALCULATE"
          construction={withProjections(next.projections)}
          capabilities={{ ...capabilities, stages: [stage], selectedStage: stage }}
          editingStep={undefined}
          disabled={false}
          onCandidateChange={vi.fn()}
          onEditStep={vi.fn()}
        />,
      );
    },
  };
}

// ---------------------------------------------------------------------------
// ATTACK 5 — unpivot addSourceField when every choice is suppressed.
// ---------------------------------------------------------------------------
describe('ATTACK 5 unpivot add-source-field under full suppression', () => {
  it('button not rendered at all when every choice is reused; inputs unchanged, no crash', () => {
    renderUnpivot({
      construction: withProjections([projection('opaque-a', 'root.value')]),
      choices: [populatedChoice('choice-a', 'root.value', 'unpivot-input')],
      selectedColumns: [],
    });
    // Pre-toggle: button hidden because availableSourceChoices is empty.
    expect(screen.queryByTestId('construction-reshape-unpivot-add-source-field')).toBeNull();
    fireEvent.click(screen.getByLabelText('Unpivot Patient reference'));
    fireEvent.click(screen.getByLabelText('Unpivot Melt two'));
    expect(screen.queryByTestId('construction-reshape-unpivot-add-source-field')).toBeNull();
    // Both inputs are ordinary table columns; nothing got a second materialization path.
    expect(unpivotSourceGroup(0)?.querySelectorAll('option').length ?? 0).toBe(0);
    expect(unpivotSourceGroup(1)?.querySelectorAll('option').length ?? 0).toBe(0);
    const selects = [0, 1].map((index) => screen.getByTestId(`construction-reshape-unpivot-input-${index}-column`) as HTMLSelectElement);
    expect(selects.map((select) => select.value)).toEqual(['opaque-a', 'opaque-b']);
    expect(screen.queryByText(/has no values in this dataset yet/)).toBeNull();
    // Nothing to click → clicking the (absent) target is provably a no-op.
    const button = screen.queryByTestId('construction-reshape-unpivot-add-source-field');
    expect(button).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// ATTACK 4c — REV4 kept-filter in chooseSourceOperand: sharing one materialized
// column across operands; switching one operand must not drop the other's live
// binding (the `|| binding.columnId === otherColumnId` guard is the REV4 repair).
// ---------------------------------------------------------------------------
describe('ATTACK 4c chooseSourceOperand kept-filter (REV4)', () => {
  it('switching the shared operand keeps BOTH bindings when the other slot still consumes the old column', () => {
    const c1 = deriveChoice('choice-c1', 'root.a', 'Alpha', true, 'x-col');
    const c2 = deriveChoice('choice-c2', 'root.b', 'Beta', true, 'y-col');
    const onCandidateChange = renderEditor({
      family: 'CALCULATE',
      construction: withProjections(undefined),
      stageColumns: [],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [c1, c2] },
    });
    // No numeric columns → kind shortcut materializes the first signed choice.
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } });
    expect(operandColumn('First value').value).toBe('x-col');
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } });
    expect(operandColumn('Second value').value).toBe('x-col'); // shares the one materialized column
    // Re-point the FIRST operand at another signed choice; the second still uses x-col.
    fireEvent.change(operandColumn('First value'), { target: { value: 'source:choice-c2' } });
    const calls = (onCandidateChange as ReturnType<typeof vi.fn>).mock.calls.map((call) => call[0]);
    const candidate = [...calls].reverse().find((entry) => entry !== undefined);
    const bindings = candidate?.deriveSources ?? [];
    expect(bindings.map((binding) => `${binding.choiceId}@${binding.columnId}`).sort())
      .toEqual(['choice-c1@x-col', 'choice-c2@y-col']);
  });
});

// ---------------------------------------------------------------------------
// ATTACK 4d — REV4 pickerEmptinessHost ownVisible semantics.
// A true virtual materialization must NOT raise the all-bound banner; a foreign
// server column that merely reuses the materialization id MUST keep explaining.
// ---------------------------------------------------------------------------
describe('ATTACK 4d REV4 pickerEmptinessHost ownVisible', () => {
  it('sole source choice bound as a virtual materialization → no false all-bound banner', () => {
    const c1 = deriveChoice('choice-c1', 'root.a', 'Alpha', true, 'x-col');
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections(undefined),
      stageColumns: [],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [c1] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } }); // materializes x-col
    fireEvent.change(operandKind('Second value'), { target: { value: 'column' } }); // shares x-col
    expect(explanationNodes()).toHaveLength(0);
    expect(allBoundText()).toBeNull();
  });

  it('foreign stage column reusing the materialization id cannot silence the explanation', () => {
    const c1 = deriveChoice('choice-c1', 'root.a', 'Alpha', true, 'x-col');
    renderEditor({
      family: 'CALCULATE',
      construction: withProjections(undefined),
      stageColumns: [decCol('x-col', 'Foreign numeric with same id')],
      deriveSourceInput: { supported: true, stageId: 'source_projection', choices: [c1] },
    });
    fireEvent.change(operandKind('First value'), { target: { value: 'column' } }); // picks foreign x-col
    fireEvent.change(operandColumn('First value'), { target: { value: 'source:choice-c1' } }); // binds C1@x-col
    // All populated choices are now bound and the picker's own entry is a FOREIGN column,
    // so the honest all-bound explanation must stay visible (REV4: id collision must not silence it).
    expect(allBoundText()).not.toBeNull();
  });
});

// ---------------------------------------------------------------------------
// ATTACK 2c — REPORTED behaviour probe: one columnId pinned to TWO fieldPaths
// (contradictory projection payload). The usable==1 rule counts entries for
// ONE fieldPath, so each field independently "reuses" the same column.
// ---------------------------------------------------------------------------
describe('ATTACK 2c contradictory same-column dual-field projection', () => {
  it('documents: both signed choices are suppressed and one column carries both', () => {
    const a = filterChoice('choice-a', 'root.age', 'Age');
    const b = filterChoice('choice-b', 'root.note', 'Note');
    renderEditor({
      family: 'KEEP_ROWS',
      construction: withProjections([
        projection('col-x', 'root.age'),
        projection('col-x', 'root.note'),
      ]),
      stageColumns: [intCol('col-x', 'X'), stringCol('other', 'Other')],
      filterSourceInput: { supported: true, stageId: 'source_projection', choices: [a, b] },
    });
    const values = optionValues(filterColumnSelect());
    // Reported (not asserted-good): per-fieldPath uniqueness lets BOTH fields reuse col-x.
    expect(values).not.toContain('source:choice-a');
    expect(values).not.toContain('source:choice-b');
    expect(values).toContain('col-x');
  });
});
