// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  TableShapeEditor,
  type ChoiceAvailability,
  type ReshapeMode,
  type ServerChoice,
  type TableShapeDefinition,
  type TableShapeEditorChoices,
} from './TableShapeEditor';

const supported: ChoiceAvailability = { kind: 'supported' };

const choice = <Kind extends string>(
  choiceKind: Kind,
  choiceId: string,
  label: string,
): ServerChoice<Kind> => ({ choiceKind, choiceId, label, availability: supported });

const mode = (
  value: ReshapeMode,
  choiceId: string,
  label: string,
  availability: ChoiceAvailability = supported,
) => ({ choiceKind: 'reshapeMode' as const, choiceId, label, availability, mode: value });

const choices = {
  reshapeModes: [
    mode('NONE', 'shape-none', 'Keep current columns'),
    mode('GROUPED_PIVOT', 'shape-pivot', 'Grouped pivot'),
    mode('UNPIVOT', 'shape-unpivot', 'Unpivot rows'),
  ],
  groupColumns: [
    choice('column', 'patient-id', 'Patient ID'),
    choice('column', 'encounter-id', 'Encounter ID'),
  ],
  categoryColumns: [choice('column', 'category-field', 'Measurement type')],
  valueColumns: [choice('column', 'value-field', 'Measurement value')],
  frozenCategories: [
    { ...choice('frozenCategory', 'category-sys', 'Systolic'), outputColumnName: 'systolic_bp' },
    { ...choice('frozenCategory', 'category-dia', 'Diastolic'), outputColumnName: 'diastolic_bp' },
  ],
  duplicatePolicies: [choice('duplicatePolicy', 'duplicate-error', 'Report duplicate cells')],
  missingCellPolicies: [choice('missingCellPolicy', 'missing-null', 'Leave empty cells null')],
  unlistedCategoryPolicies: [choice('unlistedCategoryPolicy', 'unlisted-error', 'Report new categories')],
  unpivotColumns: [
    choice('column', 'systolic-source', 'Systolic value'),
    choice('column', 'diastolic-source', 'Diastolic value'),
  ],
  unpivotKeyOutputs: [{ ...choice('unpivotKeyOutput', 'measure-name', 'Measure name'), resultTypeLabel: 'category' }],
  unpivotValueOutputs: [{ ...choice('unpivotValueOutput', 'measure-value', 'Measure value'), resultTypeLabel: 'number' }],
  unpivotNullRowPolicies: [choice('unpivotNullRowPolicy', 'drop-empty-row', 'Drop rows with no value')],
  derivedAvailability: supported,
  unpivotWithDerivedAvailability: {
    kind: 'unsupported',
    reason: 'Derived columns are not supported after unpivot in this proposal.',
  },
  derivedOutputs: [{ ...choice('derivedOutput', 'body-mass-index', 'Body mass index'), resultTypeLabel: 'decimal number' }],
  binaryOperators: [
    { ...choice('binaryOperator', 'divide', 'Divide'), requiresDivisionByZeroPolicy: true },
    { ...choice('binaryOperator', 'add', 'Add'), requiresDivisionByZeroPolicy: false },
  ],
  operands: [choice('operand', 'weight-kg', 'Weight in kilograms'), choice('operand', 'height-m', 'Height in meters')],
  missingInputPolicies: [choice('missingInputPolicy', 'propagate-null', 'Propagate null')],
  divisionByZeroPolicies: [choice('divisionByZeroPolicy', 'zero-error', 'Report division by zero')],
} satisfies TableShapeEditorChoices;

const unsupportedUnpivotChoices: TableShapeEditorChoices = {
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

const savedDefinition: TableShapeDefinition = {
  kind: 'NONE',
  reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-none' },
  derivedColumns: [],
};

const renderEditor = (options: {
  readonly availableChoices?: TableShapeEditorChoices;
  readonly definition?: TableShapeDefinition;
  readonly definitionKey?: string;
  readonly recoverableError?: string;
} = {}) => {
  const onApply = vi.fn();
  const onCancel = vi.fn();
  const view = render(
    <TableShapeEditor
      savedDefinitionKey={options.definitionKey ?? 'saved-1'}
      savedDefinition={options.definition ?? savedDefinition}
      choices={options.availableChoices ?? choices}
      recoverableError={options.recoverableError}
      onApply={onApply}
      onCancel={onCancel}
    />,
  );
  return { ...view, onApply, onCancel };
};

const selectOption = (label: string, value: string) => {
  fireEvent.change(screen.getByRole('combobox', { name: label }), { target: { value } });
};

const selectedValue = (label: string): string | undefined => {
  const element = screen.getByRole('combobox', { name: label });
  return element instanceof HTMLSelectElement ? element.value : undefined;
};

describe('TableShapeEditor', () => {
  afterEach(cleanup);

  it('keeps unsupported server options visible with the exact refusal reason', () => {
    renderEditor({ availableChoices: unsupportedUnpivotChoices });

    const unavailable = screen.getByRole('option', { name: /Unpivot rows/ });
    expect(unavailable.getAttribute('disabled')).not.toBeNull();
    expect(screen.getByTestId('ui04-reshape-mode-unsupported').textContent)
      .toContain('No unpivot inputs are available for this table.');
  });

  it('retains an omitted saved reshape-mode reference and disables Apply until it is available', () => {
    const definition: TableShapeDefinition = {
      ...savedDefinition,
      reshapeMode: { kind: 'reshapeMode', choiceId: 'saved-mode-no-longer-offered' },
    };
    const { onCancel } = renderEditor({ definition });

    expect(selectedValue('Table shape')).toBe('saved-mode-no-longer-offered');
    expect(screen.getByRole('option', { name: 'Saved selection is not in the current server choices' }))
      .toBeTruthy();
    expect(screen.getByRole('status').textContent)
      .toBe('The saved selection is not present in the current server choices.');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(selectedValue('Table shape')).toBe('saved-mode-no-longer-offered');
    expect(screen.getByRole('button', { name: 'Apply table shape' }).hasAttribute('disabled')).toBe(true);
  });

  it('configures a grouped pivot with ordered keys, frozen outputs, and server policies', () => {
    const { onApply } = renderEditor();
    selectOption('Table shape', 'shape-pivot');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Pivot group columns: Patient ID' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Pivot group columns: Encounter ID' }));
    fireEvent.click(screen.getByRole('button', { name: 'Move Encounter ID up' }));
    selectOption('Pivot category column', 'category-field');
    selectOption('Pivot value column', 'value-field');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Systolic, output column systolic_bp' }));
    selectOption('Duplicate cell policy', 'duplicate-error');
    selectOption('Missing cell policy', 'missing-null');
    selectOption('Unlisted category policy', 'unlisted-error');

    const apply = screen.getByRole('button', { name: 'Apply table shape' });
    expect(apply.getAttribute('disabled')).toBeNull();
    fireEvent.click(apply);

    expect(onApply).toHaveBeenCalledWith({
      kind: 'GROUPED_PIVOT',
      reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-pivot' },
      pivot: {
        groupColumns: [
          { kind: 'column', choiceId: 'encounter-id' },
          { kind: 'column', choiceId: 'patient-id' },
        ],
        categoryColumn: { kind: 'column', choiceId: 'category-field' },
        valueColumn: { kind: 'column', choiceId: 'value-field' },
        includedCategories: [{ kind: 'frozenCategory', choiceId: 'category-sys' }],
        duplicatePolicy: { kind: 'duplicatePolicy', choiceId: 'duplicate-error' },
        missingCellPolicy: { kind: 'missingCellPolicy', choiceId: 'missing-null' },
        unlistedCategoryPolicy: { kind: 'unlistedCategoryPolicy', choiceId: 'unlisted-error' },
      },
      derivedColumns: [],
    });
  });

  it('configures a derived numeric column only from server-supplied descriptors and policies', () => {
    const { onApply } = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    selectOption('Derived column 1 output', 'body-mass-index');
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
        output: { kind: 'derivedOutput', choiceId: 'body-mass-index' },
        operator: { kind: 'binaryOperator', choiceId: 'divide' },
        leftOperand: { kind: 'operand', choiceId: 'weight-kg' },
        rightOperand: { kind: 'operand', choiceId: 'height-m' },
        missingInputPolicy: { kind: 'missingInputPolicy', choiceId: 'propagate-null' },
        divisionByZeroPolicy: { kind: 'divisionByZeroPolicy', choiceId: 'zero-error' },
      }],
    });
  });

  it('preserves edits after a recoverable error and Cancel restores the saved definition', () => {
    const { onCancel, rerender } = renderEditor();
    selectOption('Table shape', 'shape-pivot');
    selectOption('Pivot category column', 'category-field');

    rerender(
      <TableShapeEditor
        savedDefinitionKey="saved-1"
        savedDefinition={savedDefinition}
        choices={choices}
        recoverableError="The server could not preview this table shape."
        onApply={vi.fn()}
        onCancel={onCancel}
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

  it('keeps ordered unpivot inputs and surfaces the server refusal for derived columns', () => {
    const { onApply } = renderEditor();
    selectOption('Table shape', 'shape-unpivot');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Unpivot input columns: Systolic value' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Unpivot input columns: Diastolic value' }));
    fireEvent.click(screen.getByRole('button', { name: 'Move Diastolic value up' }));
    selectOption('Unpivot key output', 'measure-name');
    selectOption('Unpivot value output', 'measure-value');
    selectOption('Unpivot null-row policy', 'drop-empty-row');

    expect(screen.getByTestId('ui04-unpivot-derived-refusal').textContent)
      .toBe('Derived columns are not supported after unpivot in this proposal.');
    expect(screen.getByRole('button', { name: 'Add derived column' }).getAttribute('disabled')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Apply table shape' }));

    expect(onApply).toHaveBeenCalledWith({
      kind: 'UNPIVOT',
      reshapeMode: { kind: 'reshapeMode', choiceId: 'shape-unpivot' },
      unpivot: {
        inputColumns: [
          { kind: 'column', choiceId: 'diastolic-source' },
          { kind: 'column', choiceId: 'systolic-source' },
        ],
        keyOutput: { kind: 'unpivotKeyOutput', choiceId: 'measure-name' },
        valueOutput: { kind: 'unpivotValueOutput', choiceId: 'measure-value' },
        nullRowPolicy: { kind: 'unpivotNullRowPolicy', choiceId: 'drop-empty-row' },
      },
      derivedColumns: [],
    });
  });
});
