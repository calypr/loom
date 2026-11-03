// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { type ConstructionReshapeEditorProps } from './ConstructionReshapeEditor';
import { ConstructionReshapeEditor } from './ConstructionReshapeEditor';

const unpivotColumns: ConstructionReshapeEditorProps['capabilities']['selectedStage']['columns'] = [
  { id: 'opaque-a', name: 'col_opaque_a', label: 'Patient reference', type: 'string', cardinality: 'required_one' },
  { id: 'opaque-b', name: 'col_opaque_b', label: 'Melt two', type: 'string', cardinality: 'required_one' },
];

const unpivotStage: ConstructionReshapeEditorProps['capabilities']['selectedStage'] = {
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  columns: unpivotColumns,
  capabilities: [
    { kind: 'FILTER', supported: false, reason: 'Not needed in this test.' },
    { kind: 'DERIVE', supported: false, reason: 'Not needed in this test.' },
    { kind: 'PIVOT', supported: false, reason: 'Not needed in this test.' },
    { kind: 'UNPIVOT', supported: true },
    { kind: 'GROUP', supported: false, reason: 'Not needed in this test.' },
    { kind: 'EXPAND', supported: false, reason: 'Not needed in this test.' },
  ],
};

const capabilitiesFor = (
  unpivotSourceInput: ConstructionReshapeEditorProps['capabilities']['unpivotSourceInput'],
): ConstructionReshapeEditorProps['capabilities'] => ({
  snapshotToken: 'snapshot-1',
  draftVersion: 1,
  draftDigest: 'draft-digest-1',
  outputId: 'table-1',
  stageId: unpivotStage.id,
  baseConstruction: { version: 1, steps: [] },
  stages: [unpivotStage],
  selectedStage: unpivotStage,
  groupFieldChoices: [],
  groupRowValueChoices: [],
  workspaceInputs: [],
  ...(unpivotSourceInput ? { unpivotSourceInput } : {}),
});

const populated = (choiceId: string, fieldPath: string, columnId: string) => ({
  choiceId,
  fieldPath,
  label: fieldPath,
  logicalType: 'decimal' as const,
  isPopulated: true,
  columnId,
});

const renderUnpivot = (args: {
  readonly choices: ReturnType<typeof populated>[];
  readonly selectedColumns?: ReadonlyArray<string>;
}) => {
  render(
    <ConstructionReshapeEditor
      construction={{ version: 1, steps: [] }}
      capabilities={capabilitiesFor({ supported: true, stageId: 'source_projection', choices: args.choices })}
      selectedColumns={args.selectedColumns}
      initialKind="unpivot"
      disabled={false}
      onCandidateChange={vi.fn()}
      onEditStep={vi.fn()}
    />,
  );
  const details = screen.queryByTestId('construction-unpivot-advanced');
  if (details && !details.querySelector('[open]') && !(details instanceof HTMLDetailsElement && details.open)) {
    fireEvent.click(screen.getByText('Advanced options: field names and missing values'));
  }
};

const openAdvanced = () => {
  const summary = screen.queryByText('Advanced options: field names and missing values');
  if (summary) fireEvent.click(summary);
};

const addSourceField = () => {
  fireEvent.click(screen.getByTestId('construction-reshape-unpivot-add-source-field'));
};

const sourceGroup = (index: number): HTMLOptGroupElement | null => {
  const select = screen.getByTestId(`construction-reshape-unpivot-input-${index}-column`) as HTMLSelectElement;
  return select.querySelector('optgroup[label="Fields from your data"]');
};

afterEach(cleanup);

describe('ROW-SOURCE-06 round-2 attacks (ConstructionReshapeEditor unpivot)', () => {
  it('B1 all-bound melt rows still explain once (shared across rows)', () => {
    renderUnpivot({
      choices: [populated('choice-a', 'root.a', 'unpivot-a'), populated('choice-b', 'root.b', 'unpivot-b')],
      selectedColumns: ['opaque-a'],
    });
    openAdvanced();
    addSourceField();
    addSourceField();

    expect(screen.getByTestId('construction-reshape-unpivot-input-2-column')).toBeTruthy();
    expect(sourceGroup(0)?.querySelectorAll('option').length ?? 0).toBe(0);
    expect(screen.getAllByTestId('source-picker-explanation')).toHaveLength(1);
    expect(screen.getByTestId('source-picker-explanation').textContent)
      .toContain('already used by another source selection');
  });

  it('B2 CONTRACT: when the first melt row itself holds one of the two bound fields, the empty picker still explains', () => {
    renderUnpivot({
      choices: [populated('choice-a', 'root.a', 'unpivot-a'), populated('choice-b', 'root.b', 'unpivot-b')],
      selectedColumns: [],
    });
    addSourceField();
    addSourceField();

    expect(screen.getByTestId('construction-reshape-unpivot-input-1-column')).toBeTruthy();
    expect(sourceGroup(0)?.querySelectorAll('option').length ?? 0).toBe(0);
    expect(sourceGroup(1)?.querySelectorAll('option').length ?? 0).toBe(0);
    expect(screen.queryByTestId('construction-reshape-unpivot-add-source-field')).toBeNull();
    expect(screen.getByTestId('source-picker-explanation').textContent)
      .toContain('already used by another source selection');
  });

  it('B3 a single melt row holding the only populated field stays silent but keeps that field selectable', () => {
    renderUnpivot({
      choices: [populated('choice-a', 'root.a', 'unpivot-a')],
      selectedColumns: [],
    });
    addSourceField();

    const select = screen.getByTestId('construction-reshape-unpivot-input-0-column') as HTMLSelectElement;
    expect([...select.options].map((option) => option.value)).toContain('unpivot-a');
    expect(screen.queryByTestId('source-picker-explanation')).toBeNull();
  });
});
