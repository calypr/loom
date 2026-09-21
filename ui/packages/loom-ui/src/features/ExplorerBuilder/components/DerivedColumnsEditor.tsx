import React from 'react';
import { ChoiceSelect } from './ChoiceSelect';
import { OutputNameFields } from './OutputNameFields';
import type {
  DerivedOperandDraft,
  DerivedColumnFormState,
  PivotFormState,
  ReshapeMode,
  TableShapeEditorChoices,
} from './tableShapeModel';
import {
  choiceFor,
  pivotOutputChoicesFor,
  pivotOutputReferenceKey,
  samePivotOutputReference,
} from './tableShapeModel';

const operandDraftForSource = (source: string): DerivedOperandDraft | undefined => {
  switch (source) {
    case 'base':
      return { kind: 'base', reference: null };
    case 'derived':
      return { kind: 'derived', localId: null };
    case 'pivotOutput':
      return { kind: 'pivotOutput', reference: null };
    case 'literal':
      return { kind: 'literal', text: '' };
    default:
      return undefined;
  }
};

interface DerivedOperandEditorProps {
  readonly columns: ReadonlyArray<DerivedColumnFormState>;
  readonly columnIndex: number;
  readonly mode: ReshapeMode | undefined;
  readonly pivot: PivotFormState;
  readonly choices: TableShapeEditorChoices;
  readonly disabled: boolean;
  readonly label: string;
  readonly testId: string;
  readonly value: DerivedOperandDraft;
  readonly onChange: (operand: DerivedOperandDraft) => void;
}

const DerivedOperandEditor = ({
  columns,
  columnIndex,
  mode,
  pivot,
  choices,
  disabled,
  label,
  testId,
  value,
  onChange,
}: DerivedOperandEditorProps) => {
  const earlierColumns = columns.slice(0, columnIndex);
  const pivotOutputs = mode === 'GROUPED_PIVOT' ? pivotOutputChoicesFor(pivot, choices) : [];
  const referencedIndex = value.kind === 'derived' && value.localId !== null
    ? columns.findIndex((column) => column.localId === value.localId)
    : -1;
  const unavailableReference = value.kind === 'derived' && value.localId !== null &&
    (referencedIndex < 0 || referencedIndex >= columnIndex);
  const pivotReference = value.kind === 'pivotOutput' ? value.reference : null;
  const unavailablePivotReference = pivotReference !== null &&
    !pivotOutputs.some((option) => samePivotOutputReference(option.reference, pivotReference));
  const unavailableBaseReference = mode === 'GROUPED_PIVOT' && value.kind === 'base';

  return (
    <div className="grid gap-2">
      <label className="grid gap-1 text-sm font-medium text-slate-800">
        <span>{`${label} source`}</span>
        <select
          aria-label={`${label} source`}
          data-testid={`${testId}-source`}
          className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
          value={value.kind}
          disabled={disabled}
          onChange={(event) => {
            const nextDraft = operandDraftForSource(event.currentTarget.value);
            if (nextDraft) onChange(nextDraft);
          }}
        >
          {mode !== 'GROUPED_PIVOT' ? <option value="base">Base column</option> : null}
          {unavailableBaseReference ? (
            <option value="base" disabled>Base column is unavailable after grouped pivot</option>
          ) : null}
          <option value="derived">Earlier derived output</option>
          {mode === 'GROUPED_PIVOT' || value.kind === 'pivotOutput' ? (
            <option value="pivotOutput">Pivot output</option>
          ) : null}
          <option value="literal">Numeric literal</option>
        </select>
      </label>

      {value.kind === 'base' && mode !== 'GROUPED_PIVOT' ? (
        <ChoiceSelect
          label={label}
          testId={testId}
          choices={choices.operands}
          value={value.reference}
          placeholder="Choose a base column"
          disabled={disabled}
          onChange={(reference) => onChange({ kind: 'base', reference })}
        />
      ) : null}
      {unavailableBaseReference ? (
        <p role="status" data-testid={`${testId}-base-reference-error`} className="text-xs text-amber-900">
          Base columns are removed by grouped pivot. Choose a pivot output, earlier derived output, or numeric literal.
        </p>
      ) : null}

      {value.kind === 'derived' ? (
        <label className="grid gap-1 text-sm font-medium text-slate-800">
          <span>{`${label} earlier derived output`}</span>
          <select
            aria-label={`${label} earlier derived output`}
            aria-invalid={value.localId === null || unavailableReference}
            data-testid={`${testId}-derived`}
            className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
            value={value.localId ?? ''}
            disabled={disabled}
            onChange={(event) => onChange({
              kind: 'derived',
              localId: event.currentTarget.value === '' ? null : event.currentTarget.value,
            })}
          >
            <option value="">Choose an earlier output</option>
            {unavailableReference && value.localId !== null ? (
              <option value={value.localId} disabled>Referenced derived output is unavailable</option>
            ) : null}
            {earlierColumns.map((column, earlierIndex) => (
              <option key={column.localId} value={column.localId}>
                {column.output.column.trim() || column.output.label.trim() || `Derived column ${earlierIndex + 1}`}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      {unavailableReference ? (
        <p role="status" data-testid={`${testId}-reference-error`} className="text-xs text-amber-900">
          The referenced derived output is not an earlier row. Choose another output or operand source.
        </p>
      ) : null}
      {value.kind === 'derived' && earlierColumns.length === 0 && !unavailableReference ? (
        <p role="status" className="text-xs text-slate-600">
          Add an earlier derived column before choosing this source.
        </p>
      ) : null}

      {value.kind === 'pivotOutput' ? (
        <label className="grid gap-1 text-sm font-medium text-slate-800">
          <span>{`${label} pivot output`}</span>
          <select
            aria-label={`${label} pivot output`}
            aria-invalid={value.reference === null || unavailablePivotReference}
            data-testid={`${testId}-pivot-output`}
            className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
            value={value.reference ? pivotOutputReferenceKey(value.reference) : ''}
            disabled={disabled}
            onChange={(event) => {
              const nextValue = event.currentTarget.value;
              if (nextValue === '') {
                onChange({ kind: 'pivotOutput', reference: null });
                return;
              }
              const selected = pivotOutputs.find(
                (option) => pivotOutputReferenceKey(option.reference) === nextValue,
              );
              if (selected) onChange({ kind: 'pivotOutput', reference: selected.reference });
            }}
          >
            <option value="">Choose a selected pivot output</option>
            {unavailablePivotReference && value.reference ? (
              <option value={pivotOutputReferenceKey(value.reference)} disabled>
                Referenced pivot output is unavailable
              </option>
            ) : null}
            {pivotOutputs.map((option) => (
              <option key={pivotOutputReferenceKey(option.reference)} value={pivotOutputReferenceKey(option.reference)}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      {unavailablePivotReference ? (
        <p role="status" data-testid={`${testId}-pivot-reference-error`} className="text-xs text-amber-900">
          The referenced pivot output is no longer selected or the table shape changed. Choose another output or operand source.
        </p>
      ) : null}
      {value.kind === 'pivotOutput' && pivotOutputs.length === 0 && !unavailablePivotReference ? (
        <p role="status" className="text-xs text-slate-600">
          Select a group or category output in the grouped pivot first.
        </p>
      ) : null}

      {value.kind === 'literal' ? (
        <label className="grid gap-1 text-sm font-medium text-slate-800">
          <span>{`${label} numeric literal`}</span>
          <input
            type="text"
            inputMode="decimal"
            aria-label={`${label} numeric literal`}
            aria-describedby={`${testId}-literal-help`}
            data-testid={`${testId}-literal`}
            className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
            placeholder="Enter a finite number"
            value={value.text}
            disabled={disabled}
            onChange={(event) => onChange({ kind: 'literal', text: event.currentTarget.value })}
          />
          <span id={`${testId}-literal-help`} className="text-xs font-normal text-slate-600">
            Whole-number text keeps integer intent. A decimal point keeps decimal intent.
          </span>
        </label>
      ) : null}
    </div>
  );
};

export interface DerivedColumnsEditorProps {
  readonly columns: ReadonlyArray<DerivedColumnFormState>;
  readonly mode: ReshapeMode | undefined;
  readonly pivot: PivotFormState;
  readonly choices: TableShapeEditorChoices;
  readonly disabled: boolean;
  readonly onChange: (columns: ReadonlyArray<DerivedColumnFormState>) => void;
  readonly onAdd: () => void;
}

export const DerivedColumnsEditor = ({
  columns,
  mode,
  pivot,
  choices,
  disabled,
  onChange,
  onAdd,
}: DerivedColumnsEditorProps) => {
  const canAdd = choices.derivedAvailability.kind === 'supported' && (
    mode !== 'UNPIVOT' || choices.unpivotWithDerivedAvailability.kind === 'supported'
  );

  const update = (
    localId: string,
    change: (previous: DerivedColumnFormState) => DerivedColumnFormState,
  ) => onChange(columns.map((column) => column.localId === localId ? change(column) : column));

  return (
    <section aria-label="Derived numeric columns" data-testid="ui04-derived-columns" className="grid gap-3 rounded-lg border border-slate-200 p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h3 className="font-semibold text-slate-900">Derived numeric columns</h3>
          <p className="mt-1 text-xs text-slate-600">Author an output name and choose operands and policies provided by the server.</p>
        </div>
        <button
          type="button"
          data-testid="ui04-add-derived-column"
          className="rounded border border-slate-300 bg-white px-3 py-2 text-sm font-medium hover:bg-slate-50 disabled:opacity-50"
          disabled={disabled || !canAdd}
          onClick={onAdd}
        >
          Add derived column
        </button>
      </div>
      {choices.derivedAvailability.kind === 'unsupported' ? (
        <p role="status" className="text-sm text-amber-900">{choices.derivedAvailability.reason}</p>
      ) : null}
      {mode === 'UNPIVOT' && choices.unpivotWithDerivedAvailability.kind === 'unsupported' ? (
        <p role="status" data-testid="ui04-unpivot-derived-refusal" className="text-sm text-amber-900">
          {choices.unpivotWithDerivedAvailability.reason}
        </p>
      ) : null}
      {columns.map((column, index) => {
        const operatorChoice = choiceFor(choices.binaryOperators, column.operator);
        return (
          <fieldset
            key={column.localId}
            aria-label={`Derived column ${index + 1}`}
            data-testid={`ui04-derived-column-${index + 1}`}
            className="grid gap-3 rounded border border-slate-200 p-3"
          >
            <legend className="px-1 text-sm font-medium text-slate-800">Derived column {index + 1}</legend>
            <OutputNameFields
              label={`Derived column ${index + 1} output`}
              testId={`ui04-derived-output-${index + 1}`}
              value={column.output}
              disabled={disabled}
              suggestions={choices.derivedOutputSuggestions}
              onChange={(output) => update(column.localId, (previous) => ({ ...previous, output }))}
            />
            <ChoiceSelect
              label={`Derived column ${index + 1} operation`}
              testId={`ui04-derived-operator-${index + 1}`}
              choices={choices.binaryOperators}
              value={column.operator}
              placeholder="Choose a server-supported operation"
              disabled={disabled}
              onChange={(operator) => update(column.localId, (previous) => ({ ...previous, operator }))}
            />
            <DerivedOperandEditor
              columns={columns}
              columnIndex={index}
              mode={mode}
              pivot={pivot}
              choices={choices}
              disabled={disabled}
              label={`Derived column ${index + 1} first operand`}
              testId={`ui04-derived-left-operand-${index + 1}`}
              value={column.leftOperand}
              onChange={(leftOperand) => update(column.localId, (previous) => ({ ...previous, leftOperand }))}
            />
            <DerivedOperandEditor
              columns={columns}
              columnIndex={index}
              mode={mode}
              pivot={pivot}
              choices={choices}
              disabled={disabled}
              label={`Derived column ${index + 1} second operand`}
              testId={`ui04-derived-right-operand-${index + 1}`}
              value={column.rightOperand}
              onChange={(rightOperand) => update(column.localId, (previous) => ({ ...previous, rightOperand }))}
            />
            <ChoiceSelect
              label={`Derived column ${index + 1} missing-input policy`}
              testId={`ui04-derived-missing-input-policy-${index + 1}`}
              choices={choices.missingInputPolicies}
              value={column.missingInputPolicy}
              placeholder="Choose a missing-input policy"
              disabled={disabled}
              onChange={(missingInputPolicy) => update(column.localId, (previous) => ({ ...previous, missingInputPolicy }))}
            />
            {operatorChoice?.requiresDivisionByZeroPolicy ? (
              <ChoiceSelect
                label={`Derived column ${index + 1} division-by-zero policy`}
                testId={`ui04-derived-division-by-zero-policy-${index + 1}`}
                choices={choices.divisionByZeroPolicies}
                value={column.divisionByZeroPolicy}
                placeholder="Choose a division-by-zero policy"
                disabled={disabled}
                onChange={(divisionByZeroPolicy) => update(
                  column.localId,
                  (previous) => ({ ...previous, divisionByZeroPolicy }),
                )}
              />
            ) : null}
            <button
              type="button"
              aria-label={`Remove derived column ${index + 1}`}
              data-testid={`ui04-remove-derived-column-${index + 1}`}
              className="justify-self-start rounded border border-red-300 px-3 py-2 text-sm font-medium text-red-800 hover:bg-red-50 disabled:opacity-50"
              disabled={disabled}
              onClick={() => onChange(columns.filter((item) => item.localId !== column.localId))}
            >
              Remove derived column
            </button>
          </fieldset>
        );
      })}
    </section>
  );
};
