// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  TableShapeEditor,
  type ChoiceAvailability,
  type PivotCategoryDiscovery,
  type PivotCategoryPair,
  type ReshapeMode,
  type ServerChoice,
  type TableShapeEditorChoices,
  type TableShapeProposalIntent,
} from './TableShapeEditor';

const supported: ChoiceAvailability = { kind: 'supported' };

const choice = <Kind extends string>(
  choiceKind: Kind,
  choiceId: string,
  label: string,
  availability: ChoiceAvailability = supported,
): ServerChoice<Kind> => ({ choiceKind, choiceId, label, availability });

const mode = (
  value: ReshapeMode,
  choiceId: string,
  label: string,
  availability: ChoiceAvailability = supported,
) => ({ choiceKind: 'reshapeMode' as const, choiceId, label, availability, mode: value });

const categoryPair: PivotCategoryPair = {
  categoryColumn: { kind: 'column', choiceId: 'category-field' },
  valueColumn: { kind: 'column', choiceId: 'value-field' },
};

const category = (
  choiceId: string,
  label: string,
  column: string,
  outputLabel: string,
  availability: ChoiceAvailability = supported,
) => ({
  ...choice('pivotCategory', choiceId, label, availability),
  suggestedOutput: { column, label: outputLabel },
});

const makeDiscovery = (
  pair: PivotCategoryPair = categoryPair,
  discoveryIdentity = 'discovery-snapshot-17',
  categories = [
    category('category-sys', 'Systolic', 'systolic_bp', 'Systolic blood pressure'),
    category('category-dia', 'Diastolic', 'diastolic_bp', 'Diastolic blood pressure'),
  ],
): PivotCategoryDiscovery => ({
  kind: 'complete',
  discoveryIdentity,
  pair,
  categories,
});

const choices: TableShapeEditorChoices = {
  reshapeModes: [
    mode('NONE', 'shape-none', 'Keep current columns'),
    mode('GROUPED_PIVOT', 'shape-pivot', 'Grouped pivot'),
    mode('UNPIVOT', 'shape-unpivot', 'Unpivot rows'),
  ],
  groupColumns: [
    choice('column', 'patient-id', 'Patient ID'),
    choice('column', 'encounter-id', 'Encounter ID'),
  ],
  categoryColumns: [
    choice('column', 'category-field', 'Measurement type'),
    choice('column', 'alternate-category-field', 'Alternate category'),
  ],
  valueColumns: [
    choice('column', 'value-field', 'Measurement value'),
    choice('column', 'alternate-value-field', 'Alternate value'),
  ],
  pivotCategoryDiscovery: { kind: 'not-requested' },
  duplicatePolicies: [choice('duplicatePolicy', 'duplicate-error', 'Report duplicate cells')],
  missingCellPolicies: [choice('missingCellPolicy', 'missing-null', 'Leave empty cells null')],
  unlistedCategoryPolicies: [choice('unlistedCategoryPolicy', 'unlisted-error', 'Report new categories')],
  unpivotColumns: [
    choice('column', 'systolic-source', 'Systolic value'),
    choice('column', 'diastolic-source', 'Diastolic value'),
  ],
  unpivotKeyOutput: {
    kind: 'supported',
    resultTypeLabel: 'category label',
    suggestions: [{
      ...choice('unpivotKeyOutput', 'measure-name', 'Measure name'),
      suggestedOutput: { column: 'measure_name', label: 'Measurement name' },
      resultTypeLabel: 'category label',
    }],
  },
  unpivotValueOutput: {
    kind: 'supported',
    resultTypeLabel: 'number',
    suggestions: [{
      ...choice('unpivotValueOutput', 'measure-value', 'Measure value'),
      suggestedOutput: { column: 'measure_value', label: 'Measurement value' },
      resultTypeLabel: 'number',
    }],
  },
  unpivotNullRowPolicies: [choice('unpivotNullRowPolicy', 'drop-empty-row', 'Drop rows with no value')],
  derivedAvailability: supported,
  unpivotWithDerivedAvailability: {
    kind: 'unsupported',
    reason: 'Derived columns are not supported after unpivot in this proposal.',
  },
  derivedOutputSuggestions: [{
    ...choice('derivedOutput', 'body-mass-index', 'Body mass index'),
    suggestedOutput: { column: 'body_mass_index', label: 'Body mass index' },
    resultTypeLabel: 'decimal number',
  }, {
    ...choice('derivedOutput', 'future-index', 'Future index', {
      kind: 'unsupported',
      reason: 'The server cannot safely suggest this output in the current table.',
    }),
    suggestedOutput: { column: 'future_index', label: 'Future index' },
    resultTypeLabel: 'decimal number',
  }],
  binaryOperators: [
    { ...choice('binaryOperator', 'divide', 'Divide'), requiresDivisionByZeroPolicy: true },
    { ...choice('binaryOperator', 'add', 'Add'), requiresDivisionByZeroPolicy: false },
  ],
  operands: [choice('operand', 'weight-kg', 'Weight in kilograms'), choice('operand', 'height-m', 'Height in meters')],
  missingInputPolicies: [choice('missingInputPolicy', 'propagate-null', 'Propagate null')],
  divisionByZeroPolicies: [choice('divisionByZeroPolicy', 'zero-error', 'Report division by zero')],
};

const savedProposalIntent: TableShapeProposalIntent = {
  kind: 'NONE',
  reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-none' },
  derivedColumns: [],
};

const renderEditor = (options: {
  readonly availableChoices?: TableShapeEditorChoices;
  readonly proposalIntent?: TableShapeProposalIntent;
  readonly proposalKey?: string;
  readonly recoverableError?: string;
} = {}) => {
  const onApply = vi.fn();
  const onCancel = vi.fn();
  const onRequestCategoryDiscovery = vi.fn();
  const view = render(
    <TableShapeEditor
      savedProposalKey={options.proposalKey ?? 'saved-1'}
      savedProposalIntent={options.proposalIntent ?? savedProposalIntent}
      choices={options.availableChoices ?? choices}
      recoverableError={options.recoverableError}
      onApply={onApply}
      onCancel={onCancel}
      onRequestCategoryDiscovery={onRequestCategoryDiscovery}
    />,
  );
  return { ...view, onApply, onCancel, onRequestCategoryDiscovery };
};

const selectOption = (label: string, value: string) => {
  fireEvent.change(screen.getByRole('combobox', { name: label }), { target: { value } });
};

const enterText = (label: string, value: string) => {
  fireEvent.change(screen.getByRole('textbox', { name: label }), { target: { value } });
};

const selectedValue = (label: string): string | undefined => {
  const element = screen.getByRole('combobox', { name: label });
  return element instanceof HTMLSelectElement ? element.value : undefined;
};

const inputValue = (label: string): string | undefined => {
  const element = screen.getByRole('textbox', { name: label });
  return element instanceof HTMLInputElement ? element.value : undefined;
};

const selectPivotPolicies = () => {
  selectOption('Duplicate cell policy', 'duplicate-error');
  selectOption('Missing cell policy', 'missing-null');
  selectOption('Unlisted category policy', 'unlisted-error');
};

const selectPivotColumns = () => {
  selectOption('Table shape', 'shape-pivot');
  fireEvent.click(screen.getByRole('checkbox', { name: 'Pivot group columns: Patient ID' }));
  selectOption('Pivot category column', 'category-field');
  selectOption('Pivot value column', 'value-field');
  selectPivotPolicies();
};

const chooseSystolicAfterDiscovery = () => {
  fireEvent.click(screen.getByRole('checkbox', { name: 'Include Systolic category' }));
};

const completeChoices = (
  discovery: PivotCategoryDiscovery = makeDiscovery(),
): TableShapeEditorChoices => ({ ...choices, pivotCategoryDiscovery: discovery });

const savedPivotIntent = (): TableShapeProposalIntent => ({
  kind: 'GROUPED_PIVOT',
  reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-pivot' },
  pivot: {
    groupColumns: [{ kind: 'column', choiceId: 'patient-id' }],
    categoryColumn: categoryPair.categoryColumn,
    valueColumn: categoryPair.valueColumn,
    categoryDiscoveryIdentity: 'saved-discovery',
    includedCategories: [{
      category: { kind: 'pivotCategory', choiceId: 'category-sys' },
      output: { column: 'saved_systolic', label: 'Saved systolic name' },
    }],
    duplicatePolicy: { kind: 'duplicatePolicy', choiceId: 'duplicate-error' },
    missingCellPolicy: { kind: 'missingCellPolicy', choiceId: 'missing-null' },
    unlistedCategoryPolicy: { kind: 'unlistedCategoryPolicy', choiceId: 'unlisted-error' },
  },
  derivedColumns: [],
});

describe('TableShapeEditor', () => {
  afterEach(cleanup);

  it('keeps unsupported server options visible with the exact refusal reason', () => {
    const unsupportedChoices: TableShapeEditorChoices = {
      ...choices,
      reshapeModes: [
        choices.reshapeModes[0],
        choices.reshapeModes[1],
        mode('UNPIVOT', 'shape-unpivot', 'Unpivot rows', {
          kind: 'unsupported',
          reason: 'No unpivot inputs are available for this table.',
        }),
      ],
    };
    renderEditor({ availableChoices: unsupportedChoices });

    expect(screen.getByRole('option', { name: /Unpivot rows/ }).getAttribute('disabled')).not.toBeNull();
    expect(screen.getByTestId('ui04-reshape-mode-unsupported').textContent)
      .toContain('No unpivot inputs are available for this table.');
  });

  it('retains an omitted saved reshape-mode reference and disables Apply until it is available', () => {
    const proposalIntent: TableShapeProposalIntent = {
      ...savedProposalIntent,
      reshapeMode: { kind: 'reshapeMode', choiceId: 'saved-mode-no-longer-offered' },
    };
    const { onCancel } = renderEditor({ proposalIntent });

    expect(selectedValue('Table shape')).toBe('saved-mode-no-longer-offered');
    expect(screen.getByRole('option', { name: 'Saved selection is not in the current server choices' })).toBeTruthy();
    expect(screen.getByRole('status').textContent)
      .toBe('The saved selection is not present in the current server choices.');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(selectedValue('Table shape')).toBe('saved-mode-no-longer-offered');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);
  });

  it('requires discovery, lets users rename frozen category outputs, and emits the opaque discovery identity', () => {
    const { onApply, onRequestCategoryDiscovery, rerender } = renderEditor();
    selectPivotColumns();

    expect(screen.queryByRole('checkbox', { name: 'Include Systolic category' })).toBeNull();
    expect(screen.getByTestId('ui04-pivot-category-discovery-state').textContent)
      .toContain('not discovered');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Discover categories' }));
    expect(onRequestCategoryDiscovery).toHaveBeenCalledWith(categoryPair);
    expect(screen.getByTestId('ui04-pivot-category-discovery-state').textContent)
      .toContain('Loading categories');

    rerender(
      <TableShapeEditor
        savedProposalKey="saved-1"
        savedProposalIntent={savedProposalIntent}
        choices={completeChoices(makeDiscovery(categoryPair, 'scope-opaque/discovery-rev-44'))}
        onApply={onApply}
        onCancel={vi.fn()}
        onRequestCategoryDiscovery={onRequestCategoryDiscovery}
      />,
    );
    chooseSystolicAfterDiscovery();
    expect(screen.getByTestId('ui04-pivot-category-output-1-column').getAttribute('value'))
      .toBe('systolic_bp');
    enterText('Systolic output column', 'sbp_result');
    enterText('Systolic output label', 'Systolic result');

    fireEvent.click(screen.getByRole('button', { name: 'Apply table shape' }));

    expect(onApply).toHaveBeenCalledWith({
      kind: 'GROUPED_PIVOT',
      reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-pivot' },
      pivot: {
        groupColumns: [{ kind: 'column', choiceId: 'patient-id' }],
        categoryColumn: categoryPair.categoryColumn,
        valueColumn: categoryPair.valueColumn,
        categoryDiscoveryIdentity: 'scope-opaque/discovery-rev-44',
        includedCategories: [{
          category: { kind: 'pivotCategory', choiceId: 'category-sys' },
          output: { column: 'sbp_result', label: 'Systolic result' },
        }],
        duplicatePolicy: { kind: 'duplicatePolicy', choiceId: 'duplicate-error' },
        missingCellPolicy: { kind: 'missingCellPolicy', choiceId: 'missing-null' },
        unlistedCategoryPolicy: { kind: 'unlistedCategoryPolicy', choiceId: 'unlisted-error' },
      },
      derivedColumns: [],
    });
  });

  it('rejects replayed discoveries after pair changes and accepts a requested matching identity', () => {
    const previousDiscovery = makeDiscovery(categoryPair, 'old-discovery-identity');
    const previousChoices = completeChoices(previousDiscovery);
    const { onApply, onRequestCategoryDiscovery, rerender } = renderEditor({
      proposalIntent: savedPivotIntent(),
      availableChoices: previousChoices,
    });

    selectOption('Pivot value column', 'alternate-value-field');
    selectOption('Pivot value column', 'value-field');
    selectOption('Pivot category column', 'alternate-category-field');
    selectOption('Pivot category column', 'category-field');
    expect(screen.getByTestId('ui04-pivot-category-discovery-state').textContent)
      .toContain('not discovered');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);

    const replayedChoices = completeChoices({ ...previousDiscovery });
    rerender(
      <TableShapeEditor
        savedProposalKey="saved-1"
        savedProposalIntent={savedPivotIntent()}
        choices={replayedChoices}
        onApply={onApply}
        onCancel={vi.fn()}
        onRequestCategoryDiscovery={onRequestCategoryDiscovery}
      />,
    );
    expect(screen.getByTestId('ui04-pivot-category-discovery-state').textContent)
      .toContain('not discovered');
    expect(screen.queryByRole('checkbox', { name: 'Include Systolic category' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Discover categories' }));
    expect(onRequestCategoryDiscovery).toHaveBeenCalledWith(categoryPair);
    rerender(
      <TableShapeEditor
        savedProposalKey="saved-1"
        savedProposalIntent={savedPivotIntent()}
        choices={completeChoices(makeDiscovery(categoryPair, 'old-discovery-identity'))}
        onApply={onApply}
        onCancel={vi.fn()}
        onRequestCategoryDiscovery={onRequestCategoryDiscovery}
      />,
    );
    chooseSystolicAfterDiscovery();
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Apply table shape' }));

    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'GROUPED_PIVOT',
      pivot: expect.objectContaining({ categoryDiscoveryIdentity: 'old-discovery-identity' }),
    }));
  });

  it('shows exact discovery refusal and keeps loading or unavailable results unappliable', () => {
    const { onRequestCategoryDiscovery, rerender } = renderEditor();
    selectPivotColumns();
    fireEvent.click(screen.getByRole('button', { name: 'Discover categories' }));
    expect(screen.getByTestId('ui04-pivot-category-discovery-state').textContent)
      .toContain('Loading categories');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);

    rerender(
      <TableShapeEditor
        savedProposalKey="saved-1"
        savedProposalIntent={savedProposalIntent}
        choices={completeChoices({
          kind: 'unavailable',
          pair: categoryPair,
          reason: 'Category scan exceeded the project result budget; select a narrower source.',
        })}
        onApply={vi.fn()}
        onCancel={vi.fn()}
        onRequestCategoryDiscovery={onRequestCategoryDiscovery}
      />,
    );

    expect(screen.getByTestId('ui04-pivot-category-discovery-state').textContent)
      .toBe('Category scan exceeded the project result budget; select a narrower source.');
    expect(screen.getByRole('button', { name: 'Try category discovery again' }).hasAttribute('disabled')).toBe(false);
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);
  });

  it('bounds long category discoveries and reports the exact visible overflow count', () => {
    const categories = Array.from({ length: 51 }, (_, index) =>
      category(`category-${index + 1}`, `Category ${index + 1}`, `category_${index + 1}`, `Category label ${index + 1}`),
    );
    const discovery = makeDiscovery(categoryPair, 'large-snapshot', categories);
    const intent = savedPivotIntent();
    const savedCategoryIntent: TableShapeProposalIntent = intent.kind === 'GROUPED_PIVOT'
      ? {
          ...intent,
          pivot: {
            ...intent.pivot,
            includedCategories: [{
              category: { kind: 'pivotCategory', choiceId: 'category-1' },
              output: { column: 'category_1', label: 'Category label 1' },
            }],
          },
        }
      : intent;
    renderEditor({
      proposalIntent: savedCategoryIntent,
      availableChoices: completeChoices(discovery),
    });

    expect(screen.getByRole('checkbox', { name: 'Include Category 1 category' })).toBeTruthy();
    expect(screen.queryByRole('checkbox', { name: 'Include Category 51 category' })).toBeNull();
    expect(screen.getByTestId('ui04-pivot-category-overflow').textContent)
      .toBe('Showing the first 50 of 51 matching discovered categories.');
  });

  it('restores saved category output names on Cancel', () => {
    const { onCancel } = renderEditor({
      proposalIntent: savedPivotIntent(),
      availableChoices: completeChoices(makeDiscovery(categoryPair, 'saved-discovery')),
    });
    expect(inputValue('Systolic output column')).toBe('saved_systolic');
    expect(inputValue('Systolic output label')).toBe('Saved systolic name');

    enterText('Systolic output column', 'draft_systolic');
    enterText('Systolic output label', 'Draft name');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(inputValue('Systolic output column')).toBe('saved_systolic');
    expect(inputValue('Systolic output label')).toBe('Saved systolic name');
  });

  it('supports derived output naming, server suggestions, and division policies', () => {
    const { onApply } = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    expect(inputValue('Derived column 1 output column')).toBe('body_mass_index');
    expect(inputValue('Derived column 1 output label')).toBe('Body mass index');
    expect(screen.getByRole('button', { name: 'Use Body mass index output suggestion for Derived column 1 output' }))
      .toBeTruthy();
    expect(screen.getByTestId('ui04-derived-column-1').textContent)
      .toContain('Server result type: decimal number');
    expect(screen.getByTestId('ui04-derived-column-1').textContent)
      .toContain('The server cannot safely suggest this output in the current table.');
    fireEvent.click(screen.getByRole('button', { name: 'Use Body mass index output suggestion for Derived column 1 output' }));
    enterText('Derived column 1 output column', 'bmi_calculated');
    enterText('Derived column 1 output label', 'BMI calculated');
    selectOption('Derived column 1 operation', 'divide');
    selectOption('Derived column 1 first operand', 'weight-kg');
    selectOption('Derived column 1 second operand', 'height-m');
    selectOption('Derived column 1 missing-input policy', 'propagate-null');
    selectOption('Derived column 1 division-by-zero policy', 'zero-error');

    fireEvent.click(screen.getByRole('button', { name: 'Apply table shape' }));

    expect(onApply).toHaveBeenCalledWith({
      kind: 'NONE',
      reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-none' },
      derivedColumns: [{
        output: { column: 'bmi_calculated', label: 'BMI calculated' },
        operator: { kind: 'binaryOperator', choiceId: 'divide' },
        leftOperand: { kind: 'operand', choiceId: 'weight-kg' },
        rightOperand: { kind: 'operand', choiceId: 'height-m' },
        missingInputPolicy: { kind: 'missingInputPolicy', choiceId: 'propagate-null' },
        divisionByZeroPolicy: { kind: 'divisionByZeroPolicy', choiceId: 'zero-error' },
      }],
    });
  });

  it('disables Apply for blank output columns or labels after the other choices are complete', () => {
    renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enterText('Derived column 1 output column', 'new_field');
    enterText('Derived column 1 output label', 'New field');
    selectOption('Derived column 1 operation', 'add');
    selectOption('Derived column 1 first operand', 'weight-kg');
    selectOption('Derived column 1 second operand', 'height-m');
    selectOption('Derived column 1 missing-input policy', 'propagate-null');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(false);

    enterText('Derived column 1 output label', '');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);
    enterText('Derived column 1 output label', 'New field');
    enterText('Derived column 1 output column', '');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);
  });

  it('leaves a derived output blank when the server provides no supported suggestion', () => {
    const unavailableSuggestion: ChoiceAvailability = {
      kind: 'unsupported',
      reason: 'No safe default output is available.',
    };
    const noSupportedSuggestions: TableShapeEditorChoices = {
      ...choices,
      derivedOutputSuggestions: choices.derivedOutputSuggestions.map((suggestion) => ({
        ...suggestion,
        availability: unavailableSuggestion,
      })),
    };
    renderEditor({ availableChoices: noSupportedSuggestions });
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));

    expect(inputValue('Derived column 1 output column')).toBe('');
    expect(inputValue('Derived column 1 output label')).toBe('');
  });

  it('rejects duplicate output columns across frozen categories and derived columns', () => {
    const { rerender } = renderEditor();
    selectPivotColumns();
    fireEvent.click(screen.getByRole('button', { name: 'Discover categories' }));
    rerender(
      <TableShapeEditor
        savedProposalKey="saved-1"
        savedProposalIntent={savedProposalIntent}
        choices={completeChoices()}
        onApply={vi.fn()}
        onCancel={vi.fn()}
        onRequestCategoryDiscovery={vi.fn()}
      />,
    );
    chooseSystolicAfterDiscovery();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enterText('Derived column 1 output column', 'systolic_bp');
    enterText('Derived column 1 output label', 'Another systolic value');
    selectOption('Derived column 1 operation', 'add');
    selectOption('Derived column 1 first operand', 'weight-kg');
    selectOption('Derived column 1 second operand', 'height-m');
    selectOption('Derived column 1 missing-input policy', 'propagate-null');

    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);

    enterText('Derived column 1 output column', 'combined_systolic');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(false);
  });

  it('preserves edits after recoverable errors and Cancel restores the saved reshape mode', () => {
    const { onCancel, rerender } = renderEditor();
    selectOption('Table shape', 'shape-pivot');
    selectOption('Pivot category column', 'category-field');

    rerender(
      <TableShapeEditor
        savedProposalKey="saved-1"
        savedProposalIntent={savedProposalIntent}
        choices={choices}
        recoverableError="The server could not preview this table shape."
        onApply={vi.fn()}
        onCancel={onCancel}
        onRequestCategoryDiscovery={vi.fn()}
      />,
    );
    expect(screen.getByTestId('ui04-table-shape-error').textContent)
      .toBe('The server could not preview this table shape.');
    expect(selectedValue('Table shape')).toBe('shape-pivot');
    expect(selectedValue('Pivot category column')).toBe('category-field');

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('region', { name: 'Grouped pivot configuration' })).toBeNull();
    expect(selectedValue('Table shape')).toBe('shape-none');
  });

  it('keeps ordered unpivot inputs, editable names, server result types, and refusal reasons', () => {
    const { onApply } = renderEditor();
    selectOption('Table shape', 'shape-unpivot');
    expect(inputValue('Unpivot key output column')).toBe('measure_name');
    expect(inputValue('Unpivot key output label')).toBe('Measurement name');
    expect(inputValue('Unpivot value output column')).toBe('measure_value');
    expect(inputValue('Unpivot value output label')).toBe('Measurement value');
    enterText('Unpivot key output column', 'draft_key');
    enterText('Unpivot key output label', 'Draft key');
    enterText('Unpivot value output column', 'draft_value');
    enterText('Unpivot value output label', 'Draft value');
    selectOption('Table shape', 'shape-none');
    selectOption('Table shape', 'shape-unpivot');
    expect(inputValue('Unpivot key output column')).toBe('draft_key');
    expect(inputValue('Unpivot key output label')).toBe('Draft key');
    expect(inputValue('Unpivot value output column')).toBe('draft_value');
    expect(inputValue('Unpivot value output label')).toBe('Draft value');

    fireEvent.click(screen.getByRole('checkbox', { name: 'Unpivot input columns: Systolic value' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Unpivot input columns: Diastolic value' }));
    fireEvent.click(screen.getByRole('button', { name: 'Move Diastolic value up' }));
    fireEvent.click(screen.getByRole('button', {
      name: 'Use Measure name output suggestion for Unpivot key output',
    }));
    enterText('Unpivot key output column', 'measure_key');
    enterText('Unpivot key output label', 'Measure key');
    fireEvent.click(screen.getByRole('button', {
      name: 'Use Measure value output suggestion for Unpivot value output',
    }));
    enterText('Unpivot value output column', 'measure_amount');
    enterText('Unpivot value output label', 'Measure amount');
    selectOption('Unpivot null-row policy', 'drop-empty-row');

    expect(screen.getByTestId('ui04-unpivot-configuration').textContent)
      .toContain('Server result type: category label');
    expect(screen.getByTestId('ui04-unpivot-configuration').textContent)
      .toContain('Server result type: number');
    expect(screen.getByTestId('ui04-unpivot-derived-refusal').textContent)
      .toBe('Derived columns are not supported after unpivot in this proposal.');
    expect(screen.getByRole('button', { name: 'Add derived column' }).hasAttribute('disabled')).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Apply table shape' }));

    expect(onApply).toHaveBeenCalledWith({
      kind: 'UNPIVOT',
      reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-unpivot' },
      unpivot: {
        inputColumns: [
          { kind: 'column', choiceId: 'diastolic-source' },
          { kind: 'column', choiceId: 'systolic-source' },
        ],
        keyOutput: { column: 'measure_key', label: 'Measure key' },
        valueOutput: { column: 'measure_amount', label: 'Measure amount' },
        nullRowPolicy: { kind: 'unpivotNullRowPolicy', choiceId: 'drop-empty-row' },
      },
      derivedColumns: [],
    });
  });
});
