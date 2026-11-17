// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  type Construction,
  type ConstructionCapabilitiesResponse,
  type ConstructionProposalRequest,
  type ConstructionStageDescriptor,
  type ConstructionStep,
} from '../../../types';
import { ConstructionOperationEditor } from './ConstructionOperationEditor';

type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'filterSources' | 'deriveSources'
>;
type DeriveSourceInput = NonNullable<ConstructionCapabilitiesResponse['deriveSourceInput']>;

const sourceStage = (cols: ConstructionStageDescriptor['columns']): ConstructionStageDescriptor => ({
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  columns: cols,
  capabilities: [
    { kind: 'FILTER', supported: true },
    { kind: 'DERIVE', supported: true },
    { kind: 'PIVOT', supported: false, reason: 'Not needed by this test.' },
    { kind: 'UNPIVOT', supported: false, reason: 'Not needed by this test.' },
  ],
});

const stringCols: ConstructionStageDescriptor['columns'] = [
  { id: 'status-id', name: 'status', label: 'Status', type: 'string' },
];

const numericChoice = (choiceId: string, columnId: string, type: 'string' | 'decimal' = 'decimal', isPopulated = true) => ({
  choiceId,
  fieldPath: `root.${choiceId}`,
  label: choiceId,
  logicalType: type,
  isPopulated,
  columnId,
});

let lastCandidate: CandidateIntent | undefined;

const renderEditor = (args: {
  readonly selectedStage?: ConstructionStageDescriptor;
  readonly deriveSourceInput?: DeriveSourceInput;
  readonly construction?: Construction;
  readonly editingStep?: ConstructionStep;
  readonly rerenderable?: boolean;
}) => {
  const selectedStage = args.selectedStage ?? sourceStage(stringCols);
  const construction = args.construction ?? { version: 1, steps: [] };
  const onCandidateChange = vi.fn((candidate: CandidateIntent | undefined) => {
    lastCandidate = candidate;
  });
  const view = render(
    <ConstructionOperationEditor
      family="CALCULATE"
      construction={construction}
      capabilities={{
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'draft-digest-1',
        outputId: 'table-1',
        stageId: selectedStage.id,
        baseConstruction: construction,
        stages: [selectedStage],
        selectedStage,
        groupFieldChoices: [],
        groupRowValueChoices: [],
        workspaceInputs: [],
        ...(args.deriveSourceInput ? { deriveSourceInput: args.deriveSourceInput } : {}),
      }}
      editingStep={args.editingStep}
      selectedColumns={[]}
      disabled={false}
      onCandidateChange={onCandidateChange}
      onEditStep={vi.fn()}
    />,
  );
  return view;
};

const operandColumn = (label: string): HTMLSelectElement =>
  screen.getByRole('combobox', { name: `${label} column` }) as HTMLSelectElement;

const operandKind = (label: string): HTMLSelectElement =>
  screen.getByRole('combobox', { name: `${label} kind` }) as HTMLSelectElement;

const setKind = (label: string, value: 'column' | 'literal' | 'unset') => {
  fireEvent.change(operandKind(label), { target: { value } });
};

const sourceOptgroup = (select: HTMLSelectElement): HTMLOptGroupElement | null =>
  select.querySelector('optgroup[label="Dataset fields (not in output)"]');

const optValues = (select: HTMLSelectElement): string[] => {
  const group = sourceOptgroup(select);
  return group ? [...group.querySelectorAll('option')].map((o) => o.value) : [];
};

type Frame = {
  readonly label: string;
  readonly explainCount: number;
  readonly explainSlots: string[];
  readonly explainClass: string | null;
  readonly left: { readonly present: boolean; readonly options: string[]; readonly optgroup: string[] };
  readonly right: { readonly present: boolean; readonly options: string[]; readonly optgroup: string[] };
  readonly candidate: string;
};

const sideOf = (el: HTMLElement): string => {
  const fieldset = el.closest('fieldset');
  const legend = fieldset?.querySelector('legend')?.textContent ?? '?';
  return legend;
};

const frame = (label: string): Frame => {
  const banners = [...document.querySelectorAll('[data-testid="source-picker-explanation"]')];
  const text = banners[0]?.textContent ?? null;
  const emptyList = document.querySelector('[data-testid="source-picker-empty-list"]') !== null;
  const cls = text === null ? null
    : emptyList ? 'none-populated'
    : text.includes('already used') ? 'all-bound'
    : text.includes('contains values') ? 'none-populated'
    : text.includes('has not listed') ? 'none-listed'
    : '?';
  const side = (name: string) => {
    let present = false;
    let options: string[] = [];
    let optgroup: string[] = [];
    try {
      const select = operandColumn(name);
      present = true;
      options = [...select.options].map((o) => o.value);
      optgroup = optValues(select);
    } catch {
      present = false;
    }
    return { present, options, optgroup };
  };
  return {
    label,
    explainCount: banners.length,
    explainSlots: banners.map((b) => sideOf(b as HTMLElement)),
    explainClass: cls,
    left: side('First value'),
    right: side('Second value'),
    candidate: lastCandidate === undefined ? 'none' : JSON.stringify(lastCandidate.deriveSources ?? null),
  };
};

const DUMPS: Frame[] = [];
const snap = (label: string): Frame => {
  const f = frame(label);
  DUMPS.push(f);
  return f;
};
const boom = (message: string): never => {
  throw new Error(`${message}\nFRAMES:\n${DUMPS.map((f) => JSON.stringify(f)).join('\n')}`);
};

afterEach(cleanup);

describe('REV4 adversarial probes (derive pickerEmptinessHost)', () => {
  it('attack a: kind-shortcut column sharing then dataset switch -> prune clobbers the other operand binding, false all-bound banner while a populated field stays listable', () => {
    lastCandidate = undefined;
    DUMPS.length = 0;
    renderEditor({
      selectedStage: sourceStage(stringCols),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-a', 'derive-input_a'), numericChoice('choice-b', 'derive-input_b')],
      },
    });
    snap('f0 initial');
    setKind('Second value', 'column');
    snap('f1 right bound to choice-a (kind shortcut)');
    setKind('First value', 'column');
    const f2 = snap('f2 left shares derive-input_a');
    expect(f2.explainCount).toBe(0);
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-b' } });
    const f3 = snap('f3 right switched to choice-b (kept-filter clobber)');

    expect(f3.explainCount).toBeLessThanOrEqual(1);
    const optgroupSize = Math.max(f3.left.optgroup.length, f3.right.optgroup.length);
    if (f3.explainClass === 'all-bound' && optgroupSize > 0) {
      boom(`FALSE BANNER: class=all-bound while optgroup lists ${optgroupSize} field(s); left.optgroup=${JSON.stringify(f3.left.optgroup)} right.optgroup=${JSON.stringify(f3.right.optgroup)} candidate=${f3.candidate}`);
    }
    if (f3.candidate !== 'none' && !f3.candidate.includes('choice-a')) {
      boom(`BINDING LOST: left operand still kind=column columnId=derive-input_a but candidate deriveSources=${f3.candidate}`);
    }
  });

  it('attack b: two choices share one materialized columnId, single active binding stays consistent (host must not double or vanish)', () => {
    lastCandidate = undefined;
    DUMPS.length = 0;
    renderEditor({
      selectedStage: sourceStage(stringCols),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-a', 'shared-col'), numericChoice('choice-a2', 'shared-col'), numericChoice('choice-b', 'derive-input_b')],
      },
    });
    setKind('First value', 'column');
    setKind('Second value', 'column');
    snap('g1 both drafts shared-col, binding choice-a');
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-a2' } });
    const g2 = snap('g2 right re-picks choice-a2 (same columnId)');
    expect(g2.explainCount).toBeLessThanOrEqual(1);
    const leftListsA = g2.left.optgroup.includes('source:choice-a');
    if (g2.explainClass === 'all-bound' && leftListsA) {
      boom(`FALSE BANNER (shared columnId): all-bound while left lists ${JSON.stringify(g2.left.optgroup)} candidate=${g2.candidate}`);
    }
    if (g2.candidate !== 'none' && !g2.candidate.includes('choice-a"') && !g2.candidate.includes('choice-a2')) {
      boom(`WIRE: ${g2.candidate}`);
    }
  });

  it('attack c: live stage change keeps ownVisible true via foreign numeric column id => zero-field picker loses its explanation', () => {
    lastCandidate = undefined;
    DUMPS.length = 0;
    const input: DeriveSourceInput = {
      supported: true,
      stageId: 'source_projection',
      choices: [numericChoice('choice-note', 'clash-col', 'string')],
    };
    const view = renderEditor({
      selectedStage: sourceStage(stringCols),
      deriveSourceInput: input,
    });
    setKind('First value', 'column');
    const h1 = snap('h1 sole string-typed binding, zero dataset fields');
    expect(h1.explainCount).toBe(1);
    expect(h1.explainClass).toBe('all-bound');

    const laterStage = sourceStage([
      ...stringCols,
      { id: 'clash-col', name: 'clash', label: 'Clash', type: 'integer' },
    ]);
    const construction = { version: 1, steps: [] };
    view.rerender(
      <ConstructionOperationEditor
        family="CALCULATE"
        construction={construction}
        capabilities={{
          snapshotToken: 'snapshot-1',
          draftVersion: 1,
          draftDigest: 'draft-digest-1',
          outputId: 'table-1',
          stageId: laterStage.id,
          baseConstruction: construction,
          stages: [laterStage],
          selectedStage: laterStage,
          groupFieldChoices: [],
          groupRowValueChoices: [],
          workspaceInputs: [],
          deriveSourceInput: input,
        }}
        editingStep={undefined}
        selectedColumns={[]}
        disabled={false}
        onCandidateChange={(candidate) => { lastCandidate = candidate; }}
        onEditStep={vi.fn()}
      />,
    );
    const h2 = snap('h2 after stage gains numeric column id=clash-col');
    const leftFields = h2.left.optgroup.length;
    if (leftFields === 0 && h2.explainCount === 0) {
      boom(`FALSE AVAILABLE: zero dataset fields, no explanation; left.options=${JSON.stringify(h2.left.options)} candidate=${h2.candidate}`);
    }
  });

  it('attack e: released (literal) operand prunes its binding; no stale-binding frame may raise a false banner', () => {
    lastCandidate = undefined;
    DUMPS.length = 0;
    renderEditor({
      selectedStage: sourceStage([...stringCols, { id: 'n1', name: 'n1', label: 'N1', type: 'integer' }]),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-note', 'derive-input_note', 'string')],
      },
    });
    setKind('Second value', 'column');
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-note' } });
    setKind('Second value', 'literal');
    const m1 = snap('m1 right released to literal, left still column n1');
    if (m1.explainCount !== 0
      || (m1.candidate !== 'none' && m1.candidate !== 'null' && m1.candidate.includes('choice-note'))) {
      boom(`STALE BINDING: right kind=literal but explainCount=${m1.explainCount} class=${m1.explainClass} candidate=${m1.candidate}`);
    }
  });

  it('attack d: right-hosted banner migrates when left switches to column and back; exactly one element per frame', () => {
    lastCandidate = undefined;
    DUMPS.length = 0;
    renderEditor({
      selectedStage: sourceStage([...stringCols, { id: 'n1', name: 'n1', label: 'N1', type: 'integer' }]),
      deriveSourceInput: {
        supported: true,
        stageId: 'source_projection',
        choices: [numericChoice('choice-note', 'derive-input_note', 'string')],
      },
    });
    setKind('First value', 'literal');
    setKind('Second value', 'column');
    const k0 = snap('k1 right drafts n1, note listable, no banner');
    expect(k0.explainCount).toBe(0);
    fireEvent.change(operandColumn('Second value'), { target: { value: 'source:choice-note' } });
    const k1 = snap('k2 right bound to string note -> zero fields, right hosts');
    expect(k1.explainCount).toBe(1);
    expect(k1.explainSlots).toEqual(['Second value']);
    expect(k1.explainClass).toBe('all-bound');

    setKind('First value', 'column');
    const k2 = snap('k3 left switched to column n1');
    expect(k2.explainCount).toBe(1);
    if (k2.explainSlots.length !== 1) boom('two hosts');
    if (k2.left.optgroup.length === 0 && !k2.explainSlots.includes('First value') && !k2.explainSlots.includes('Second value')) {
      boom('banner lost in migration');
    }
    setKind('First value', 'literal');
    const k3 = snap('k4 left literal again');
    expect(k3.explainCount).toBe(1);
    expect(k3.explainSlots).toEqual(['Second value']);
  });
});
