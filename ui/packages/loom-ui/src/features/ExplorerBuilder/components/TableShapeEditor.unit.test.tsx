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
import { proposalIntentFor, proposalIntentToForm } from './tableShapeModel';
import type {
  DerivedColumnFormState,
  PivotCategorySelection,
  TableShapeFormState,
} from './tableShapeModel';

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
  operands: [
    choice('operand', 'weight-kg', 'Weight in kilograms'),
    choice('operand', 'height-m', 'Height in meters'),
    choice('operand', 'count-rows', 'Count of rows'),
  ],
  missingInputPolicies: [choice('missingInputPolicy', 'propagate-null', 'Propagate null')],
  divisionByZeroPolicies: [choice('divisionByZeroPolicy', 'zero-error', 'Report division by zero')],
};

const savedProposalIntent: TableShapeProposalIntent = {
  kind: 'NONE',
  reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-none' },
  derivedColumns: [],
};

const savedDerivedIntent = (): TableShapeProposalIntent => ({
  kind: 'NONE',
  reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-none' },
  derivedColumns: [{
    localId: 'saved-sum-plus-count',
    output: { column: 'sum_plus_count', label: 'Sum plus count' },
    operator: { kind: 'binaryOperator', choiceId: 'add' },
    leftOperand: { kind: 'base', reference: { kind: 'operand', choiceId: 'weight-kg' } },
    rightOperand: { kind: 'literal', representation: 'decimal', text: '0.50' },
    missingInputPolicy: { kind: 'missingInputPolicy', choiceId: 'propagate-null' },
  }, {
    localId: 'saved-plus-one',
    output: { column: 'sum_plus_count_plus_one', label: 'Sum plus count plus one' },
    operator: { kind: 'binaryOperator', choiceId: 'add' },
    leftOperand: { kind: 'derived', localId: 'saved-sum-plus-count' },
    rightOperand: { kind: 'literal', representation: 'integer', text: '1' },
    missingInputPolicy: { kind: 'missingInputPolicy', choiceId: 'propagate-null' },
  }],
});

const renderEditor = (options: {
  readonly availableChoices?: TableShapeEditorChoices;
  readonly proposalIntent?: TableShapeProposalIntent;
  readonly proposalKey?: string;
  readonly recoverableError?: string;
} = {}) => {
  const onApply = vi.fn<(proposalIntent: TableShapeProposalIntent) => void>();
  const onCancel = vi.fn<() => void>();
  const onRequestCategoryDiscovery = vi.fn<(pair: PivotCategoryPair) => void>();
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

const selectByTestId = (testId: string, value: string) => {
  fireEvent.change(screen.getByTestId(testId), { target: { value } });
};

const selectedTestValue = (testId: string): string | undefined => {
  const element = screen.getByTestId(testId);
  return element instanceof HTMLSelectElement ? element.value : undefined;
};

const testInputValue = (testId: string): string | undefined => {
  const element = screen.getByTestId(testId);
  return element instanceof HTMLInputElement ? element.value : undefined;
};

const valueForOption = (testId: string, label: string): string => {
  const element = screen.getByTestId(testId);
  if (!(element instanceof HTMLSelectElement)) throw new Error(`${testId} is not a select`);
  const option = Array.from(element.options).find((item) => item.textContent === label);
  if (!option) throw new Error(`${label} is not an option in ${testId}`);
  return option.value;
};

const selectOptionByLabel = (testId: string, label: string) => {
  const value = valueForOption(testId, label);
  selectByTestId(testId, value);
  return value;
};

const completeBaseDerivedColumn = (
  index: number,
  outputColumn: string,
  outputLabel: string,
  leftOperand = 'weight-kg',
  rightOperand = 'height-m',
) => {
  enterText(`Derived column ${index} output column`, outputColumn);
  enterText(`Derived column ${index} output label`, outputLabel);
  selectOption(`Derived column ${index} operation`, 'add');
  selectOption(`Derived column ${index} first operand`, leftOperand);
  selectOption(`Derived column ${index} second operand`, rightOperand);
  selectOption(`Derived column ${index} missing-input policy`, 'propagate-null');
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

const savedPivotIntentWithTwoCategories = (): Extract<
  TableShapeProposalIntent,
  { readonly kind: 'GROUPED_PIVOT' }
> => {
  const intent = savedPivotIntent();
  if (intent.kind !== 'GROUPED_PIVOT') throw new Error('Expected a grouped pivot intent');
  const includedCategories: readonly [PivotCategorySelection, ...PivotCategorySelection[]] = [{
    category: { kind: 'pivotCategory', choiceId: 'category-sys' },
    output: { column: 'alpha', label: 'Alpha output' },
  }, {
    category: { kind: 'pivotCategory', choiceId: 'category-dia' },
    output: { column: 'zero', label: 'Zero output' },
  }];
  return {
    ...intent,
    pivot: {
      ...intent.pivot,
      includedCategories,
    },
  };
};

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
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(selectedValue('Table shape')).toBe('saved-mode-no-longer-offered');
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);
  });

  it('requires discovery, lets users rename frozen category outputs, and emits the opaque discovery identity', () => {
    const { onApply, onRequestCategoryDiscovery, rerender } = renderEditor();
    selectPivotColumns();

    expect(screen.queryByRole('checkbox', { name: 'Include Systolic category' })).toBeNull();
    expect(screen.getByTestId('ui04-pivot-category-discovery-state').textContent)
      .toContain('not discovered');
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);

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

    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

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
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);

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
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);

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
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

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
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);

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
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);
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
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);
    selectOption('Derived column 1 division-by-zero policy', 'zero-error');
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(false);

    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

    expect(onApply).toHaveBeenCalledWith({
      kind: 'NONE',
      reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-none' },
      derivedColumns: [{
        localId: 'draft-0',
        output: { column: 'bmi_calculated', label: 'BMI calculated' },
        operator: { kind: 'binaryOperator', choiceId: 'divide' },
        leftOperand: { kind: 'base', reference: { kind: 'operand', choiceId: 'weight-kg' } },
        rightOperand: { kind: 'base', reference: { kind: 'operand', choiceId: 'height-m' } },
        missingInputPolicy: { kind: 'missingInputPolicy', choiceId: 'propagate-null' },
        divisionByZeroPolicy: { kind: 'divisionByZeroPolicy', choiceId: 'zero-error' },
      }],
    });
  });

  it('lets a later derived column use sum_plus_count plus the integer literal 1', () => {
    const { onApply } = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    completeBaseDerivedColumn(1, 'sum_plus_count', 'Sum plus count', 'weight-kg', 'count-rows');

    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enterText('Derived column 2 output column', 'sum_plus_count_plus_one');
    enterText('Derived column 2 output label', 'Sum plus count plus one');
    selectOption('Derived column 2 operation', 'add');
    selectOption('Derived column 2 first operand source', 'derived');
    selectOptionByLabel('ui04-derived-left-operand-2-derived', 'sum_plus_count');
    selectOption('Derived column 2 second operand source', 'literal');
    enterText('Derived column 2 second operand numeric literal', '1');
    selectOption('Derived column 2 missing-input policy', 'propagate-null');

    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

    const intent = onApply.mock.calls[0]?.[0];
    if (!intent || intent.kind !== 'NONE') throw new Error('Expected a table shape proposal');
    const [sumPlusCount, sumPlusOne] = intent.derivedColumns;
    expect(sumPlusCount?.output.column).toBe('sum_plus_count');
    expect(sumPlusOne?.output.column).toBe('sum_plus_count_plus_one');
    expect(sumPlusOne?.leftOperand).toEqual({ kind: 'derived', localId: sumPlusCount?.localId });
    expect(sumPlusOne?.rightOperand).toEqual({
      kind: 'literal',
      representation: 'integer',
      text: '1',
    });
  });

  it('preserves integer zero, decimal 0.5, and exact large-integer literal text', () => {
    const { onApply } = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enterText('Derived column 1 output column', 'zero_plus_fraction');
    enterText('Derived column 1 output label', 'Zero plus fraction');
    selectOption('Derived column 1 operation', 'add');
    selectOption('Derived column 1 first operand source', 'literal');
    enterText('Derived column 1 first operand numeric literal', '0');
    selectOption('Derived column 1 second operand source', 'literal');
    enterText('Derived column 1 second operand numeric literal', '0.5');
    selectOption('Derived column 1 missing-input policy', 'propagate-null');

    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    completeBaseDerivedColumn(2, 'large_integer', 'Large integer');
    selectOption('Derived column 2 first operand source', 'literal');
    enterText('Derived column 2 first operand numeric literal', '9007199254740993');

    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

    const intent = onApply.mock.calls[0]?.[0];
    if (!intent || intent.kind !== 'NONE') throw new Error('Expected a table shape proposal');
    expect(intent.derivedColumns[0]?.leftOperand).toEqual({
      kind: 'literal',
      representation: 'integer',
      text: '0',
    });
    expect(intent.derivedColumns[0]?.rightOperand).toEqual({
      kind: 'literal',
      representation: 'decimal',
      text: '0.5',
    });
    expect(intent.derivedColumns[1]?.leftOperand).toEqual({
      kind: 'literal',
      representation: 'integer',
      text: '9007199254740993',
    });
  });

  it('rejects a non-finite numeric literal until it is repaired', () => {
    renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enterText('Derived column 1 output column', 'finite_check');
    enterText('Derived column 1 output label', 'Finite check');
    selectOption('Derived column 1 operation', 'add');
    selectOption('Derived column 1 first operand source', 'literal');
    enterText('Derived column 1 first operand numeric literal', '1e999');
    selectOption('Derived column 1 second operand', 'weight-kg');
    selectOption('Derived column 1 missing-input policy', 'propagate-null');

    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);

    enterText('Derived column 1 first operand numeric literal', '1e2');
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(false);
  });

  it('rejects a forward derived-column reference in proposal intent construction', () => {
    const initialForm = proposalIntentToForm(savedProposalIntent);
    const derivedColumns: ReadonlyArray<DerivedColumnFormState> = [{
      localId: 'first-row',
      output: { column: 'first_result', label: 'First result' },
      operator: { kind: 'binaryOperator', choiceId: 'add' },
      leftOperand: { kind: 'derived', localId: 'later-row' },
      rightOperand: { kind: 'base', reference: { kind: 'operand', choiceId: 'weight-kg' } },
      missingInputPolicy: { kind: 'missingInputPolicy', choiceId: 'propagate-null' },
      divisionByZeroPolicy: null,
    }, {
      localId: 'later-row',
      output: { column: 'later_result', label: 'Later result' },
      operator: { kind: 'binaryOperator', choiceId: 'add' },
      leftOperand: { kind: 'base', reference: { kind: 'operand', choiceId: 'weight-kg' } },
      rightOperand: { kind: 'base', reference: { kind: 'operand', choiceId: 'height-m' } },
      missingInputPolicy: { kind: 'missingInputPolicy', choiceId: 'propagate-null' },
      divisionByZeroPolicy: null,
    }];
    const form: TableShapeFormState = { ...initialForm, derivedColumns };

    expect(proposalIntentFor({
      form,
      choices,
      categoryDiscovery: { kind: 'not-requested' },
    })).toBeUndefined();
  });

  it('lets derived columns choose two current pivot category outputs by their server references', () => {
    const savedIntent = savedPivotIntentWithTwoCategories();
    const { onApply } = renderEditor({
      proposalIntent: savedIntent,
      availableChoices: completeChoices(),
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enterText('Derived column 1 output column', 'alpha_plus_zero');
    enterText('Derived column 1 output label', 'Alpha plus zero');
    selectOption('Derived column 1 operation', 'add');
    selectOption('Derived column 1 first operand source', 'pivotOutput');
    selectOptionByLabel('ui04-derived-left-operand-1-pivot-output', 'Category output: alpha');
    selectOption('Derived column 1 second operand source', 'pivotOutput');
    selectOptionByLabel('ui04-derived-right-operand-1-pivot-output', 'Category output: zero');
    selectOption('Derived column 1 missing-input policy', 'propagate-null');

    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

    const intent = onApply.mock.calls[0]?.[0];
    if (!intent || intent.kind !== 'GROUPED_PIVOT') throw new Error('Expected a grouped pivot proposal');
    expect(intent.pivot.includedCategories.map((selection) => selection.output.column))
      .toEqual(['alpha', 'zero']);
    expect(intent.derivedColumns[0]?.output.column).toBe('alpha_plus_zero');
    expect(intent.derivedColumns[0]?.leftOperand).toEqual({
      kind: 'pivotOutput',
      reference: {
        kind: 'category',
        category: { kind: 'pivotCategory', choiceId: 'category-sys' },
      },
    });
    expect(intent.derivedColumns[0]?.rightOperand).toEqual({
      kind: 'pivotOutput',
      reference: {
        kind: 'category',
        category: { kind: 'pivotCategory', choiceId: 'category-dia' },
      },
    });
  });

  it('supports pivot outputs, earlier derived outputs, and numeric literals after grouped pivot', () => {
    const { onApply } = renderEditor({
      proposalIntent: savedPivotIntentWithTwoCategories(),
      availableChoices: completeChoices(),
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enterText('Derived column 1 output column', 'alpha_plus_one');
    enterText('Derived column 1 output label', 'Alpha plus one');
    selectOption('Derived column 1 operation', 'add');
    selectOption('Derived column 1 first operand source', 'pivotOutput');
    selectOptionByLabel('ui04-derived-left-operand-1-pivot-output', 'Category output: alpha');
    selectOption('Derived column 1 second operand source', 'literal');
    enterText('Derived column 1 second operand numeric literal', '1');
    selectOption('Derived column 1 missing-input policy', 'propagate-null');

    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enterText('Derived column 2 output column', 'alpha_plus_one_plus_zero');
    enterText('Derived column 2 output label', 'Alpha plus one plus zero');
    selectOption('Derived column 2 operation', 'add');
    selectOption('Derived column 2 first operand source', 'derived');
    selectOptionByLabel('ui04-derived-left-operand-2-derived', 'alpha_plus_one');
    selectOption('Derived column 2 second operand source', 'pivotOutput');
    selectOptionByLabel('ui04-derived-right-operand-2-pivot-output', 'Category output: zero');
    selectOption('Derived column 2 missing-input policy', 'propagate-null');

    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

    const intent = onApply.mock.calls[0]?.[0];
    if (!intent || intent.kind !== 'GROUPED_PIVOT') throw new Error('Expected a grouped pivot proposal');
    const [alphaPlusOne, alphaPlusOnePlusZero] = intent.derivedColumns;
    expect(alphaPlusOne?.leftOperand).toEqual({
      kind: 'pivotOutput',
      reference: { kind: 'category', category: { kind: 'pivotCategory', choiceId: 'category-sys' } },
    });
    expect(alphaPlusOne?.rightOperand).toEqual({ kind: 'literal', representation: 'integer', text: '1' });
    expect(alphaPlusOnePlusZero?.leftOperand).toEqual({
      kind: 'derived',
      localId: alphaPlusOne?.localId,
    });
    expect(alphaPlusOnePlusZero?.rightOperand).toEqual({
      kind: 'pivotOutput',
      reference: { kind: 'category', category: { kind: 'pivotCategory', choiceId: 'category-dia' } },
    });
  });

  it('restores saved pivot-output references after Cancel', () => {
    const pivotIntent = savedPivotIntentWithTwoCategories();
    const savedIntent: TableShapeProposalIntent = {
      ...pivotIntent,
      derivedColumns: [{
        localId: 'saved-pivot-derived',
        output: { column: 'alpha_plus_zero', label: 'Alpha plus zero' },
        operator: { kind: 'binaryOperator', choiceId: 'add' },
        leftOperand: {
          kind: 'pivotOutput',
          reference: {
            kind: 'category',
            category: { kind: 'pivotCategory', choiceId: 'category-sys' },
          },
        },
        rightOperand: {
          kind: 'pivotOutput',
          reference: {
            kind: 'category',
            category: { kind: 'pivotCategory', choiceId: 'category-dia' },
          },
        },
        missingInputPolicy: { kind: 'missingInputPolicy', choiceId: 'propagate-null' },
      }],
    };
    const { onApply, onCancel } = renderEditor({
      proposalIntent: savedIntent,
      availableChoices: completeChoices(),
    });

    expect(selectedValue('Derived column 1 first operand source')).toBe('pivotOutput');
    expect(selectedTestValue('ui04-derived-left-operand-1-pivot-output')).toBe('category:category-sys');
    expect(selectedValue('Derived column 1 second operand source')).toBe('pivotOutput');
    expect(selectedTestValue('ui04-derived-right-operand-1-pivot-output')).toBe('category:category-dia');

    selectOption('Derived column 1 second operand source', 'literal');
    enterText('Derived column 1 second operand numeric literal', '1');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(selectedValue('Derived column 1 second operand source')).toBe('pivotOutput');
    expect(selectedTestValue('ui04-derived-right-operand-1-pivot-output')).toBe('category:category-dia');
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));
    const restoredIntent = onApply.mock.calls[0]?.[0];
    if (!restoredIntent || restoredIntent.kind !== 'GROUPED_PIVOT') {
      throw new Error('Expected a restored grouped pivot proposal');
    }
    expect(restoredIntent.pivot.includedCategories).toEqual(savedIntent.pivot.includedCategories);
    expect(restoredIntent.derivedColumns).toEqual(savedIntent.derivedColumns);
  });

  it('keeps saved base operands invalid after grouped pivot until repaired', () => {
    const pivotIntent = savedPivotIntentWithTwoCategories();
    const savedIntent: TableShapeProposalIntent = {
      ...pivotIntent,
      derivedColumns: [{
        localId: 'saved-base-derived',
        output: { column: 'sum_after_pivot', label: 'Sum after pivot' },
        operator: { kind: 'binaryOperator', choiceId: 'add' },
        leftOperand: { kind: 'base', reference: { kind: 'operand', choiceId: 'weight-kg' } },
        rightOperand: { kind: 'base', reference: { kind: 'operand', choiceId: 'height-m' } },
        missingInputPolicy: { kind: 'missingInputPolicy', choiceId: 'propagate-null' },
      }],
    };
    renderEditor({ proposalIntent: savedIntent, availableChoices: completeChoices() });

    expect(selectedValue('Derived column 1 first operand source')).toBe('base');
    expect(screen.getByTestId('ui04-derived-left-operand-1-base-reference-error').textContent)
      .toContain('removed by grouped pivot');
    const unavailableBaseOptions = screen.getAllByRole('option', {
      name: 'Base column is unavailable after grouped pivot',
    });
    expect(unavailableBaseOptions).toHaveLength(2);
    expect(unavailableBaseOptions.every((option) => option.hasAttribute('disabled'))).toBe(true);
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);
  });

  it('rejects pivot outputs outside grouped-pivot mode and after their outputs are removed', () => {
    const initialForm = proposalIntentToForm(savedPivotIntentWithTwoCategories());
    const derivedColumns: ReadonlyArray<DerivedColumnFormState> = [{
      localId: 'after-pivot',
      output: { column: 'alpha_plus_zero', label: 'Alpha plus zero' },
      operator: { kind: 'binaryOperator', choiceId: 'add' },
      leftOperand: {
        kind: 'pivotOutput',
        reference: {
          kind: 'category',
          category: { kind: 'pivotCategory', choiceId: 'category-dia' },
        },
      },
      rightOperand: {
        kind: 'pivotOutput',
        reference: {
          kind: 'category',
          category: { kind: 'pivotCategory', choiceId: 'category-sys' },
        },
      },
      missingInputPolicy: { kind: 'missingInputPolicy', choiceId: 'propagate-null' },
      divisionByZeroPolicy: null,
    }];
    const form: TableShapeFormState = { ...initialForm, derivedColumns };
    const categoryDiscovery = makeDiscovery();
    const removedOutputForm: TableShapeFormState = {
      ...form,
      pivot: {
        ...form.pivot,
        includedCategories: form.pivot.includedCategories.slice(0, 1),
      },
    };
    const nonPivotForm: TableShapeFormState = {
      ...form,
      mode: { kind: 'reshapeMode', choiceId: 'shape-none' },
    };

    expect(proposalIntentFor({ form: removedOutputForm, choices: completeChoices(), categoryDiscovery }))
      .toBeUndefined();
    expect(proposalIntentFor({ form: nonPivotForm, choices, categoryDiscovery }))
      .toBeUndefined();
  });

  it('keeps deleted derived references invalid until the user repairs them', () => {
    renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    completeBaseDerivedColumn(1, 'sum_plus_count', 'Sum plus count');
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    completeBaseDerivedColumn(2, 'sum_plus_value', 'Sum plus value');
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enterText('Derived column 3 output column', 'sum_plus_value_plus_weight');
    enterText('Derived column 3 output label', 'Sum plus value plus weight');
    selectOption('Derived column 3 operation', 'add');
    selectOption('Derived column 3 first operand source', 'derived');
    const removedLocalId = selectOptionByLabel('ui04-derived-left-operand-3-derived', 'sum_plus_value');
    selectOption('Derived column 3 second operand', 'weight-kg');
    selectOption('Derived column 3 missing-input policy', 'propagate-null');
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(false);

    fireEvent.click(screen.getByRole('button', { name: 'Remove derived column 2' }));

    const remainingEarlierId = valueForOption('ui04-derived-left-operand-2-derived', 'sum_plus_count');
    expect(remainingEarlierId).not.toBe(removedLocalId);
    expect(selectedTestValue('ui04-derived-left-operand-2-derived')).toBe(removedLocalId);
    expect(screen.getByTestId('ui04-derived-left-operand-2-reference-error').textContent)
      .toContain('not an earlier row');
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);

    selectOptionByLabel('ui04-derived-left-operand-2-derived', 'sum_plus_count');
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(false);
  });

  it('restores all saved operand variants and their authored text on Cancel', () => {
    const savedIntent = savedDerivedIntent();
    const { onApply, onCancel } = renderEditor({ proposalIntent: savedIntent });

    expect(selectedValue('Derived column 1 first operand source')).toBe('base');
    expect(selectedValue('Derived column 1 first operand')).toBe('weight-kg');
    expect(selectedValue('Derived column 1 second operand source')).toBe('literal');
    expect(testInputValue('ui04-derived-right-operand-1-literal')).toBe('0.50');
    expect(selectedValue('Derived column 2 first operand source')).toBe('derived');
    expect(selectedTestValue('ui04-derived-left-operand-2-derived')).toBe('saved-sum-plus-count');
    expect(testInputValue('ui04-derived-right-operand-2-literal')).toBe('1');

    enterText('Derived column 1 second operand numeric literal', '2.50');
    selectOption('Derived column 2 first operand source', 'base');
    selectOption('Derived column 2 first operand', 'count-rows');
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('group', { name: 'Derived column 3' })).toBeNull();
    expect(selectedValue('Derived column 1 first operand')).toBe('weight-kg');
    expect(testInputValue('ui04-derived-right-operand-1-literal')).toBe('0.50');
    expect(selectedValue('Derived column 2 first operand source')).toBe('derived');
    expect(selectedTestValue('ui04-derived-left-operand-2-derived')).toBe('saved-sum-plus-count');
    expect(testInputValue('ui04-derived-right-operand-2-literal')).toBe('1');

    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));
    expect(onApply).toHaveBeenCalledWith(savedIntent);
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
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(false);

    enterText('Derived column 1 output label', '');
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);
    enterText('Derived column 1 output label', 'New field');
    enterText('Derived column 1 output column', '');
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);
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
    selectOption('Derived column 1 first operand source', 'pivotOutput');
    selectOptionByLabel('ui04-derived-left-operand-1-pivot-output', 'Group output: Patient ID');
    selectOption('Derived column 1 second operand source', 'literal');
    enterText('Derived column 1 second operand numeric literal', '1');
    selectOption('Derived column 1 missing-input policy', 'propagate-null');

    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(true);

    enterText('Derived column 1 output column', 'combined_systolic');
    expect(screen.getByRole('button', { name: 'Preview table shape' }).hasAttribute('disabled')).toBe(false);
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
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

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
