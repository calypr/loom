import React from 'react';
import { ChoiceSelect } from './ChoiceSelect';
import { OutputNameFields } from './OutputNameFields';
import type {
  DerivedColumnFormState,
  ReshapeMode,
  TableShapeEditorChoices,
} from './tableShapeModel';
import { choiceFor } from './tableShapeModel';

export interface DerivedColumnsEditorProps {
  readonly columns: ReadonlyArray<DerivedColumnFormState>;
  readonly mode: ReshapeMode | undefined;
  readonly choices: TableShapeEditorChoices;
  readonly disabled: boolean;
  readonly onChange: (columns: ReadonlyArray<DerivedColumnFormState>) => void;
  readonly onAdd: () => void;
}

export const DerivedColumnsEditor = ({
  columns,
  mode,
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
            <ChoiceSelect
              label={`Derived column ${index + 1} first operand`}
              testId={`ui04-derived-left-operand-${index + 1}`}
              choices={choices.operands}
              value={column.leftOperand}
              placeholder="Choose the first operand"
              disabled={disabled}
              onChange={(leftOperand) => update(column.localId, (previous) => ({ ...previous, leftOperand }))}
            />
            <ChoiceSelect
              label={`Derived column ${index + 1} second operand`}
              testId={`ui04-derived-right-operand-${index + 1}`}
              choices={choices.operands}
              value={column.rightOperand}
              placeholder="Choose the second operand"
              disabled={disabled}
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
