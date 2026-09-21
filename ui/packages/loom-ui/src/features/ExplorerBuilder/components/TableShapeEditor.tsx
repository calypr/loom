import React, { useEffect, useRef, useState } from 'react';

export type ChoiceAvailability =
  | { readonly kind: 'supported' }
  | { readonly kind: 'unsupported'; readonly reason: string };

export interface ServerChoice<Kind extends string> {
  readonly choiceId: string;
  readonly choiceKind: Kind;
  readonly label: string;
  readonly availability: ChoiceAvailability;
}

export interface ChoiceReference<Kind extends string> {
  readonly kind: Kind;
  readonly choiceId: string;
}

export type ReshapeMode = 'NONE' | 'GROUPED_PIVOT' | 'UNPIVOT';

export interface ReshapeModeChoice extends ServerChoice<'reshapeMode'> {
  readonly mode: ReshapeMode;
}

export type ColumnChoice = ServerChoice<'column'>;

export interface FrozenCategoryChoice extends ServerChoice<'frozenCategory'> {
  readonly outputColumnName: string;
}

export type DuplicatePolicyChoice = ServerChoice<'duplicatePolicy'>;
export type MissingCellPolicyChoice = ServerChoice<'missingCellPolicy'>;
export type UnlistedCategoryPolicyChoice = ServerChoice<'unlistedCategoryPolicy'>;
export type MissingInputPolicyChoice = ServerChoice<'missingInputPolicy'>;
export type DivisionByZeroPolicyChoice = ServerChoice<'divisionByZeroPolicy'>;
export type UnpivotNullRowPolicyChoice = ServerChoice<'unpivotNullRowPolicy'>;

export interface UnpivotKeyOutputChoice extends ServerChoice<'unpivotKeyOutput'> {
  readonly resultTypeLabel: string;
}

export interface UnpivotValueOutputChoice extends ServerChoice<'unpivotValueOutput'> {
  readonly resultTypeLabel: string;
}

export interface DerivedOutputChoice extends ServerChoice<'derivedOutput'> {
  readonly resultTypeLabel: string;
}

export interface BinaryOperatorChoice extends ServerChoice<'binaryOperator'> {
  readonly requiresDivisionByZeroPolicy: boolean;
}

export type OperandChoice = ServerChoice<'operand'>;

export interface TableShapeEditorChoices {
  readonly reshapeModes: ReadonlyArray<ReshapeModeChoice>;
  readonly groupColumns: ReadonlyArray<ColumnChoice>;
  readonly categoryColumns: ReadonlyArray<ColumnChoice>;
  readonly valueColumns: ReadonlyArray<ColumnChoice>;
  readonly frozenCategories: ReadonlyArray<FrozenCategoryChoice>;
  readonly duplicatePolicies: ReadonlyArray<DuplicatePolicyChoice>;
  readonly missingCellPolicies: ReadonlyArray<MissingCellPolicyChoice>;
  readonly unlistedCategoryPolicies: ReadonlyArray<UnlistedCategoryPolicyChoice>;
  readonly unpivotColumns: ReadonlyArray<ColumnChoice>;
  readonly unpivotKeyOutputs: ReadonlyArray<UnpivotKeyOutputChoice>;
  readonly unpivotValueOutputs: ReadonlyArray<UnpivotValueOutputChoice>;
  readonly unpivotNullRowPolicies: ReadonlyArray<UnpivotNullRowPolicyChoice>;
  readonly derivedAvailability: ChoiceAvailability;
  readonly unpivotWithDerivedAvailability: ChoiceAvailability;
  readonly derivedOutputs: ReadonlyArray<DerivedOutputChoice>;
  readonly binaryOperators: ReadonlyArray<BinaryOperatorChoice>;
  readonly operands: ReadonlyArray<OperandChoice>;
  readonly missingInputPolicies: ReadonlyArray<MissingInputPolicyChoice>;
  readonly divisionByZeroPolicies: ReadonlyArray<DivisionByZeroPolicyChoice>;
}

type NonEmptyReadonlyArray<Value> = readonly [Value, ...Value[]];

export interface PivotDefinition {
  readonly groupColumns: NonEmptyReadonlyArray<ChoiceReference<'column'>>;
  readonly categoryColumn: ChoiceReference<'column'>;
  readonly valueColumn: ChoiceReference<'column'>;
  readonly includedCategories: NonEmptyReadonlyArray<ChoiceReference<'frozenCategory'>>;
  readonly duplicatePolicy: ChoiceReference<'duplicatePolicy'>;
  readonly missingCellPolicy: ChoiceReference<'missingCellPolicy'>;
  readonly unlistedCategoryPolicy: ChoiceReference<'unlistedCategoryPolicy'>;
}

export interface UnpivotDefinition {
  readonly inputColumns: NonEmptyReadonlyArray<ChoiceReference<'column'>>;
  readonly keyOutput: ChoiceReference<'unpivotKeyOutput'>;
  readonly valueOutput: ChoiceReference<'unpivotValueOutput'>;
  readonly nullRowPolicy: ChoiceReference<'unpivotNullRowPolicy'>;
}

interface DerivedColumnDefinitionBase {
  readonly output: ChoiceReference<'derivedOutput'>;
  readonly operator: ChoiceReference<'binaryOperator'>;
  readonly leftOperand: ChoiceReference<'operand'>;
  readonly rightOperand: ChoiceReference<'operand'>;
  readonly missingInputPolicy: ChoiceReference<'missingInputPolicy'>;
}

export type DerivedColumnDefinition =
  | (DerivedColumnDefinitionBase & {
      readonly divisionByZeroPolicy: ChoiceReference<'divisionByZeroPolicy'>;
    })
  | (DerivedColumnDefinitionBase & { readonly divisionByZeroPolicy?: never });

export type TableShapeDefinition =
  | {
      readonly kind: 'NONE';
      readonly reshapeMode: ChoiceReference<'reshapeMode'>;
      readonly derivedColumns: ReadonlyArray<DerivedColumnDefinition>;
    }
  | {
      readonly kind: 'GROUPED_PIVOT';
      readonly reshapeMode: ChoiceReference<'reshapeMode'>;
      readonly pivot: PivotDefinition;
      readonly derivedColumns: ReadonlyArray<DerivedColumnDefinition>;
    }
  | {
      readonly kind: 'UNPIVOT';
      readonly reshapeMode: ChoiceReference<'reshapeMode'>;
      readonly unpivot: UnpivotDefinition;
      readonly derivedColumns: ReadonlyArray<DerivedColumnDefinition>;
    };

interface PivotFormState {
  readonly groupColumns: ReadonlyArray<ChoiceReference<'column'>>;
  readonly categoryColumn: ChoiceReference<'column'> | null;
  readonly valueColumn: ChoiceReference<'column'> | null;
  readonly includedCategories: ReadonlyArray<ChoiceReference<'frozenCategory'>>;
  readonly duplicatePolicy: ChoiceReference<'duplicatePolicy'> | null;
  readonly missingCellPolicy: ChoiceReference<'missingCellPolicy'> | null;
  readonly unlistedCategoryPolicy: ChoiceReference<'unlistedCategoryPolicy'> | null;
}

interface UnpivotFormState {
  readonly inputColumns: ReadonlyArray<ChoiceReference<'column'>>;
  readonly keyOutput: ChoiceReference<'unpivotKeyOutput'> | null;
  readonly valueOutput: ChoiceReference<'unpivotValueOutput'> | null;
  readonly nullRowPolicy: ChoiceReference<'unpivotNullRowPolicy'> | null;
}

interface DerivedColumnFormState {
  readonly localId: string;
  readonly output: ChoiceReference<'derivedOutput'> | null;
  readonly operator: ChoiceReference<'binaryOperator'> | null;
  readonly leftOperand: ChoiceReference<'operand'> | null;
  readonly rightOperand: ChoiceReference<'operand'> | null;
  readonly missingInputPolicy: ChoiceReference<'missingInputPolicy'> | null;
  readonly divisionByZeroPolicy: ChoiceReference<'divisionByZeroPolicy'> | null;
}

interface EditorFormState {
  readonly mode: ChoiceReference<'reshapeMode'> | null;
  readonly pivot: PivotFormState;
  readonly unpivot: UnpivotFormState;
  readonly derivedColumns: ReadonlyArray<DerivedColumnFormState>;
}

const emptyPivot = (): PivotFormState => ({
  groupColumns: [],
  categoryColumn: null,
  valueColumn: null,
  includedCategories: [],
  duplicatePolicy: null,
  missingCellPolicy: null,
  unlistedCategoryPolicy: null,
});

const emptyUnpivot = (): UnpivotFormState => ({
  inputColumns: [],
  keyOutput: null,
  valueOutput: null,
  nullRowPolicy: null,
});

const referenceFor = <Kind extends string>(
  choice: ServerChoice<Kind>,
): ChoiceReference<Kind> => ({ kind: choice.choiceKind, choiceId: choice.choiceId });

const choiceFor = <Choice extends ServerChoice<string>>(
  choices: ReadonlyArray<Choice>,
  reference: ChoiceReference<Choice['choiceKind']> | null,
): Choice | undefined =>
  reference ? choices.find((choice) => choice.choiceId === reference.choiceId) : undefined;

const definitionToForm = (definition: TableShapeDefinition): EditorFormState => {
  const derivedColumns = definition.derivedColumns.map((derivedColumn, index) => ({
    localId: `saved-${index + 1}`,
    output: derivedColumn.output,
    operator: derivedColumn.operator,
    leftOperand: derivedColumn.leftOperand,
    rightOperand: derivedColumn.rightOperand,
    missingInputPolicy: derivedColumn.missingInputPolicy,
    divisionByZeroPolicy: derivedColumn.divisionByZeroPolicy ?? null,
  }));

  if (definition.kind === 'NONE') {
    return {
      mode: definition.reshapeMode,
      pivot: emptyPivot(),
      unpivot: emptyUnpivot(),
      derivedColumns,
    };
  }

  if (definition.kind === 'GROUPED_PIVOT') {
    return {
      mode: definition.reshapeMode,
      pivot: {
        groupColumns: definition.pivot.groupColumns,
        categoryColumn: definition.pivot.categoryColumn,
        valueColumn: definition.pivot.valueColumn,
        includedCategories: definition.pivot.includedCategories,
        duplicatePolicy: definition.pivot.duplicatePolicy,
        missingCellPolicy: definition.pivot.missingCellPolicy,
        unlistedCategoryPolicy: definition.pivot.unlistedCategoryPolicy,
      },
      unpivot: emptyUnpivot(),
      derivedColumns,
    };
  }

  return {
    mode: definition.reshapeMode,
    pivot: emptyPivot(),
    unpivot: {
      inputColumns: definition.unpivot.inputColumns,
      keyOutput: definition.unpivot.keyOutput,
      valueOutput: definition.unpivot.valueOutput,
      nullRowPolicy: definition.unpivot.nullRowPolicy,
    },
    derivedColumns,
  };
};

const nonEmpty = <Value,>(values: ReadonlyArray<Value>): NonEmptyReadonlyArray<Value> | undefined => {
  const first = values[0];
  return first === undefined ? undefined : [first, ...values.slice(1)];
};

const choiceIsSupported = <Kind extends string>(
  choices: ReadonlyArray<ServerChoice<Kind>>,
  reference: ChoiceReference<Kind> | null,
): boolean => choiceFor(choices, reference)?.availability.kind === 'supported';

const allChoicesSupported = <Kind extends string>(
  choices: ReadonlyArray<ServerChoice<Kind>>,
  references: ReadonlyArray<ChoiceReference<Kind>>,
): boolean => references.every((reference) => choiceIsSupported(choices, reference));

const derivedDefinitionFor = (
  form: DerivedColumnFormState,
  choices: TableShapeEditorChoices,
): DerivedColumnDefinition | undefined => {
  const output = choiceFor(choices.derivedOutputs, form.output);
  const operator = choiceFor(choices.binaryOperators, form.operator);
  const leftOperand = choiceFor(choices.operands, form.leftOperand);
  const rightOperand = choiceFor(choices.operands, form.rightOperand);
  const missingInputPolicy = choiceFor(choices.missingInputPolicies, form.missingInputPolicy);
  if (
    !output || output.availability.kind !== 'supported' ||
    !operator || operator.availability.kind !== 'supported' ||
    !leftOperand || leftOperand.availability.kind !== 'supported' ||
    !rightOperand || rightOperand.availability.kind !== 'supported' ||
    !missingInputPolicy || missingInputPolicy.availability.kind !== 'supported'
  ) {
    return undefined;
  }

  const base = {
    output: referenceFor(output),
    operator: referenceFor(operator),
    leftOperand: referenceFor(leftOperand),
    rightOperand: referenceFor(rightOperand),
    missingInputPolicy: referenceFor(missingInputPolicy),
  };

  if (!operator.requiresDivisionByZeroPolicy) return base;

  const divisionByZeroPolicy = choiceFor(choices.divisionByZeroPolicies, form.divisionByZeroPolicy);
  if (!divisionByZeroPolicy || divisionByZeroPolicy.availability.kind !== 'supported') {
    return undefined;
  }
  return { ...base, divisionByZeroPolicy: referenceFor(divisionByZeroPolicy) };
};

const completeDerivedDefinitions = (
  form: EditorFormState,
  choices: TableShapeEditorChoices,
): ReadonlyArray<DerivedColumnDefinition> | undefined => {
  if (
    form.derivedColumns.length > 0 &&
    choices.derivedAvailability.kind === 'unsupported'
  ) {
    return undefined;
  }

  const result: DerivedColumnDefinition[] = [];
  for (const derivedColumn of form.derivedColumns) {
    const definition = derivedDefinitionFor(derivedColumn, choices);
    if (!definition) return undefined;
    result.push(definition);
  }
  return result;
};

const definitionFor = (
  form: EditorFormState,
  choices: TableShapeEditorChoices,
): TableShapeDefinition | undefined => {
  const modeChoice = choiceFor(choices.reshapeModes, form.mode);
  if (!modeChoice || modeChoice.availability.kind !== 'supported') return undefined;
  const reshapeMode = form.mode;
  if (!reshapeMode) return undefined;
  const derivedColumns = completeDerivedDefinitions(form, choices);
  if (!derivedColumns) return undefined;

  switch (modeChoice.mode) {
    case 'NONE':
      return { kind: 'NONE', reshapeMode, derivedColumns };
    case 'GROUPED_PIVOT': {
      const groupColumns = nonEmpty(form.pivot.groupColumns);
      const includedCategories = nonEmpty(form.pivot.includedCategories);
      const categoryColumn = choiceFor(choices.categoryColumns, form.pivot.categoryColumn);
      const valueColumn = choiceFor(choices.valueColumns, form.pivot.valueColumn);
      const duplicatePolicy = choiceFor(choices.duplicatePolicies, form.pivot.duplicatePolicy);
      const missingCellPolicy = choiceFor(choices.missingCellPolicies, form.pivot.missingCellPolicy);
      const unlistedCategoryPolicy = choiceFor(
        choices.unlistedCategoryPolicies,
        form.pivot.unlistedCategoryPolicy,
      );
      if (
        !groupColumns ||
        !includedCategories ||
        !allChoicesSupported(choices.groupColumns, form.pivot.groupColumns) ||
        !categoryColumn || categoryColumn.availability.kind !== 'supported' ||
        !valueColumn || valueColumn.availability.kind !== 'supported' ||
        !allChoicesSupported(choices.frozenCategories, form.pivot.includedCategories) ||
        !duplicatePolicy || duplicatePolicy.availability.kind !== 'supported' ||
        !missingCellPolicy || missingCellPolicy.availability.kind !== 'supported' ||
        !unlistedCategoryPolicy || unlistedCategoryPolicy.availability.kind !== 'supported'
      ) {
        return undefined;
      }
      return {
        kind: 'GROUPED_PIVOT',
        reshapeMode,
        pivot: {
          groupColumns,
          categoryColumn: referenceFor(categoryColumn),
          valueColumn: referenceFor(valueColumn),
          includedCategories,
          duplicatePolicy: referenceFor(duplicatePolicy),
          missingCellPolicy: referenceFor(missingCellPolicy),
          unlistedCategoryPolicy: referenceFor(unlistedCategoryPolicy),
        },
        derivedColumns,
      };
    }
    case 'UNPIVOT': {
      if (
        derivedColumns.length > 0 &&
        choices.unpivotWithDerivedAvailability.kind === 'unsupported'
      ) {
        return undefined;
      }
      const inputColumns = nonEmpty(form.unpivot.inputColumns);
      const keyOutput = choiceFor(choices.unpivotKeyOutputs, form.unpivot.keyOutput);
      const valueOutput = choiceFor(choices.unpivotValueOutputs, form.unpivot.valueOutput);
      const nullRowPolicy = choiceFor(
        choices.unpivotNullRowPolicies,
        form.unpivot.nullRowPolicy,
      );
      if (
        !inputColumns ||
        !allChoicesSupported(choices.unpivotColumns, form.unpivot.inputColumns) ||
        !keyOutput || keyOutput.availability.kind !== 'supported' ||
        !valueOutput || valueOutput.availability.kind !== 'supported' ||
        !nullRowPolicy || nullRowPolicy.availability.kind !== 'supported'
      ) {
        return undefined;
      }
      return {
        kind: 'UNPIVOT',
        reshapeMode,
        unpivot: {
          inputColumns,
          keyOutput: referenceFor(keyOutput),
          valueOutput: referenceFor(valueOutput),
          nullRowPolicy: referenceFor(nullRowPolicy),
        },
        derivedColumns,
      };
    }
    default: {
      const exhaustive: never = modeChoice.mode;
      return exhaustive;
    }
  }
};

interface ChoiceSelectProps<Kind extends string> {
  readonly label: string;
  readonly testId: string;
  readonly choices: ReadonlyArray<ServerChoice<Kind>>;
  readonly value: ChoiceReference<Kind> | null;
  readonly placeholder: string;
  readonly disabled?: boolean;
  readonly onChange: (choice: ChoiceReference<Kind> | null) => void;
}

const ChoiceSelect = <Kind extends string,>({
  label,
  testId,
  choices,
  value,
  placeholder,
  disabled = false,
  onChange,
}: ChoiceSelectProps<Kind>) => {
  const selectedChoice = choiceFor(choices, value);
  const missingSavedChoice = value !== null && selectedChoice === undefined;
  const unsupportedChoices = choices.filter(
    (choice) => choice.availability.kind === 'unsupported',
  );

  return (
    <div className="grid gap-1">
      <label className="grid gap-1 text-sm font-medium text-slate-800">
        <span>{label}</span>
        <select
          aria-label={label}
          data-testid={testId}
          className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
          value={value?.choiceId ?? ''}
          disabled={disabled}
          onChange={(event) => {
            const nextId = event.currentTarget.value;
            if (nextId === '') {
              onChange(null);
              return;
            }
            const nextChoice = choices.find((choice) => choice.choiceId === nextId);
            if (nextChoice?.availability.kind === 'supported') {
              onChange(referenceFor(nextChoice));
            }
          }}
        >
          <option value="">{placeholder}</option>
          {missingSavedChoice && value ? (
            <option value={value.choiceId} disabled>Saved selection is not in the current server choices</option>
          ) : null}
          {choices.map((choice) => (
            <option
              key={choice.choiceId}
              value={choice.choiceId}
              disabled={choice.availability.kind === 'unsupported'}
            >
              {choice.availability.kind === 'unsupported'
                ? `${choice.label} — unavailable: ${choice.availability.reason}`
                : choice.label}
            </option>
          ))}
        </select>
      </label>
      {missingSavedChoice ? (
        <p role="status" className="text-xs text-amber-900">
          The saved selection is not present in the current server choices.
        </p>
      ) : null}
      {unsupportedChoices.length > 0 ? (
        <ul aria-label={`${label} unavailable choices`} data-testid={`${testId}-unsupported`} className="space-y-1 text-xs text-amber-900">
          {unsupportedChoices.map((choice) => choice.availability.kind === 'unsupported' ? (
            <li key={choice.choiceId}>
              <span className="font-medium">{choice.label}: </span>
              <span>{choice.availability.reason}</span>
            </li>
          ) : null)}
        </ul>
      ) : null}
    </div>
  );
};

interface OrderedChoiceEditorProps<Kind extends string> {
  readonly label: string;
  readonly testId: string;
  readonly choices: ReadonlyArray<ServerChoice<Kind>>;
  readonly selected: ReadonlyArray<ChoiceReference<Kind>>;
  readonly disabled?: boolean;
  readonly onChange: (selected: ReadonlyArray<ChoiceReference<Kind>>) => void;
}

const OrderedChoiceEditor = <Kind extends string,>({
  label,
  testId,
  choices,
  selected,
  disabled = false,
  onChange,
}: OrderedChoiceEditorProps<Kind>) => (
  <fieldset className="grid gap-2 rounded border border-slate-200 p-3">
    <legend className="px-1 text-sm font-medium text-slate-800">{label}</legend>
    <ul className="grid gap-1">
      {choices.map((choice, index) => {
        const reference = referenceFor(choice);
        const checked = selected.some((item) => item.choiceId === choice.choiceId);
        const choiceDisabled = disabled || (
          choice.availability.kind === 'unsupported' && !checked
        );
        return (
          <li key={choice.choiceId}>
            <label className="flex items-start gap-2 rounded px-1 py-1 text-sm text-slate-800">
              <input
                type="checkbox"
                aria-label={`${label}: ${choice.label}`}
                data-testid={`${testId}-choice-${index + 1}`}
                checked={checked}
                disabled={choiceDisabled}
                onChange={(event) => {
                  if (choice.availability.kind === 'unsupported' && event.currentTarget.checked) {
                    return;
                  }
                  if (event.currentTarget.checked) {
                    if (!checked) onChange([...selected, reference]);
                  } else {
                    onChange(selected.filter((item) => item.choiceId !== choice.choiceId));
                  }
                }}
              />
              <span className="grid gap-0.5">
                <span>{choice.label}</span>
                {choice.availability.kind === 'unsupported' ? (
                  <span className="text-xs text-amber-900">{choice.availability.reason}</span>
                ) : null}
              </span>
            </label>
          </li>
        );
      })}
    </ul>
    {selected.length > 0 ? (
      <ol aria-label={`${label} order`} data-testid={`${testId}-order`} className="grid gap-1">
        {selected.map((reference, index) => {
          const choice = choiceFor(choices, reference);
          const choiceLabel = choice?.label ?? 'Saved selection is not in the current server choices';
          return (
            <li key={`${reference.kind}:${reference.choiceId}`} className="flex items-center gap-2 text-sm">
              <span className="min-w-0 flex-1">{index + 1}. {choiceLabel}</span>
              <button
                type="button"
                aria-label={`Move ${choiceLabel} up`}
                className="rounded border border-slate-300 px-2 py-1 disabled:opacity-40"
                disabled={disabled || index === 0}
                onClick={() => {
                  const previous = selected[index - 1];
                  const current = selected[index];
                  if (!previous || !current) return;
                  const reordered = selected.slice();
                  reordered[index - 1] = current;
                  reordered[index] = previous;
                  onChange(reordered);
                }}
              >
                Move up
              </button>
              <button
                type="button"
                aria-label={`Move ${choiceLabel} down`}
                className="rounded border border-slate-300 px-2 py-1 disabled:opacity-40"
                disabled={disabled || index === selected.length - 1}
                onClick={() => {
                  const next = selected[index + 1];
                  const current = selected[index];
                  if (!next || !current) return;
                  const reordered = selected.slice();
                  reordered[index] = next;
                  reordered[index + 1] = current;
                  onChange(reordered);
                }}
              >
                Move down
              </button>
              <button
                type="button"
                aria-label={`Remove ${choiceLabel}`}
                className="rounded border border-slate-300 px-2 py-1 disabled:opacity-40"
                disabled={disabled}
                onClick={() => onChange(selected.filter((item) => item.choiceId !== reference.choiceId))}
              >
                Remove
              </button>
            </li>
          );
        })}
      </ol>
    ) : null}
  </fieldset>
);

interface CategoryChoiceListProps {
  readonly choices: ReadonlyArray<FrozenCategoryChoice>;
  readonly selected: ReadonlyArray<ChoiceReference<'frozenCategory'>>;
  readonly disabled: boolean;
  readonly onChange: (selected: ReadonlyArray<ChoiceReference<'frozenCategory'>>) => void;
}

const CategoryChoiceList = ({ choices, selected, disabled, onChange }: CategoryChoiceListProps) => (
  <fieldset className="grid gap-2 rounded border border-slate-200 p-3">
    <legend className="px-1 text-sm font-medium text-slate-800">Frozen pivot categories and output names</legend>
    <ul className="grid gap-1" data-testid="ui04-pivot-frozen-categories">
      {choices.map((choice, index) => {
        const checked = selected.some((item) => item.choiceId === choice.choiceId);
        return (
          <li key={choice.choiceId}>
            <label className="flex items-start gap-2 rounded px-1 py-1 text-sm text-slate-800">
              <input
                type="checkbox"
                aria-label={`${choice.label}, output column ${choice.outputColumnName}`}
                data-testid={`ui04-pivot-category-${index + 1}`}
                checked={checked}
                disabled={disabled || (choice.availability.kind === 'unsupported' && !checked)}
                onChange={(event) => {
                  if (choice.availability.kind === 'unsupported' && event.currentTarget.checked) {
                    return;
                  }
                  if (event.currentTarget.checked) {
                    if (!checked) onChange([...selected, referenceFor(choice)]);
                  } else {
                    onChange(selected.filter((item) => item.choiceId !== choice.choiceId));
                  }
                }}
              />
              <span className="grid gap-0.5">
                <span>{choice.label}</span>
                <span className="text-xs text-slate-600">Output column: {choice.outputColumnName}</span>
                {choice.availability.kind === 'unsupported' ? (
                  <span className="text-xs text-amber-900">{choice.availability.reason}</span>
                ) : null}
              </span>
            </label>
          </li>
        );
      })}
    </ul>
  </fieldset>
);

export interface TableShapeEditorProps {
  readonly savedDefinitionKey: string;
  readonly savedDefinition: TableShapeDefinition;
  readonly choices: TableShapeEditorChoices;
  readonly recoverableError?: string;
  readonly disabled?: boolean;
  readonly onApply: (definition: TableShapeDefinition) => void;
  readonly onCancel: () => void;
}

export const TableShapeEditor = ({
  savedDefinitionKey,
  savedDefinition,
  choices,
  recoverableError,
  disabled = false,
  onApply,
  onCancel,
}: TableShapeEditorProps) => {
  const [form, setForm] = useState(() => definitionToForm(savedDefinition));
  const nextLocalId = useRef(0);
  const latestSavedDefinition = useRef(savedDefinition);
  latestSavedDefinition.current = savedDefinition;

  useEffect(() => {
    setForm(definitionToForm(savedDefinition));
  }, [savedDefinitionKey]);

  const modeChoice = choiceFor(choices.reshapeModes, form.mode);
  const canAddDerived = choices.derivedAvailability.kind === 'supported' && (
    modeChoice?.mode !== 'UNPIVOT' ||
    choices.unpivotWithDerivedAvailability.kind === 'supported'
  );
  const definition = definitionFor(form, choices);
  const activeMode = modeChoice?.mode;

  const updateDerivedColumn = (
    localId: string,
    update: (previous: DerivedColumnFormState) => DerivedColumnFormState,
  ) => {
    setForm((previous) => ({
      ...previous,
      derivedColumns: previous.derivedColumns.map((derivedColumn) =>
        derivedColumn.localId === localId ? update(derivedColumn) : derivedColumn,
      ),
    }));
  };

  const cancel = () => {
    setForm(definitionToForm(latestSavedDefinition.current));
    onCancel();
  };

  return (
    <section aria-label="Table shape editor" data-testid="ui04-table-shape-editor" className="grid gap-4 rounded-xl border border-slate-200 bg-white p-4 text-slate-800 shadow-sm">
      <div>
        <h2 className="text-base font-semibold text-slate-900">Table shape</h2>
        <p className="mt-1 text-sm text-slate-600">Choose a server-supported reshape and optional derived columns.</p>
      </div>
      {recoverableError ? (
        <p role="alert" data-testid="ui04-table-shape-error" className="rounded border border-red-200 bg-red-50 p-3 text-sm text-red-900">
          {recoverableError}
        </p>
      ) : null}
      <ChoiceSelect
        label="Table shape"
        testId="ui04-reshape-mode"
        choices={choices.reshapeModes}
        value={form.mode}
        placeholder="Choose a table shape"
        disabled={disabled}
        onChange={(mode) => setForm((previous) => ({ ...previous, mode }))}
      />

      {activeMode === 'GROUPED_PIVOT' ? (
        <section aria-label="Grouped pivot configuration" data-testid="ui04-pivot-configuration" className="grid gap-3 rounded-lg border border-slate-200 p-3">
          <div>
            <h3 className="font-semibold text-slate-900">Grouped pivot</h3>
            <p className="mt-1 text-xs text-slate-600">Select the group keys, category and value columns, then freeze the category outputs.</p>
          </div>
          <OrderedChoiceEditor
            label="Pivot group columns"
            testId="ui04-pivot-group-columns"
            choices={choices.groupColumns}
            selected={form.pivot.groupColumns}
            disabled={disabled}
            onChange={(groupColumns) => setForm((previous) => ({
              ...previous,
              pivot: { ...previous.pivot, groupColumns },
            }))}
          />
          <ChoiceSelect
            label="Pivot category column"
            testId="ui04-pivot-category-column"
            choices={choices.categoryColumns}
            value={form.pivot.categoryColumn}
            placeholder="Choose a category column"
            disabled={disabled}
            onChange={(categoryColumn) => setForm((previous) => ({
              ...previous,
              pivot: { ...previous.pivot, categoryColumn },
            }))}
          />
          <ChoiceSelect
            label="Pivot value column"
            testId="ui04-pivot-value-column"
            choices={choices.valueColumns}
            value={form.pivot.valueColumn}
            placeholder="Choose a value column"
            disabled={disabled}
            onChange={(valueColumn) => setForm((previous) => ({
              ...previous,
              pivot: { ...previous.pivot, valueColumn },
            }))}
          />
          <CategoryChoiceList
            choices={choices.frozenCategories}
            selected={form.pivot.includedCategories}
            disabled={disabled}
            onChange={(includedCategories) => setForm((previous) => ({
              ...previous,
              pivot: { ...previous.pivot, includedCategories },
            }))}
          />
          <ChoiceSelect
            label="Duplicate cell policy"
            testId="ui04-pivot-duplicate-policy"
            choices={choices.duplicatePolicies}
            value={form.pivot.duplicatePolicy}
            placeholder="Choose how duplicate cells are handled"
            disabled={disabled}
            onChange={(duplicatePolicy) => setForm((previous) => ({
              ...previous,
              pivot: { ...previous.pivot, duplicatePolicy },
            }))}
          />
          <ChoiceSelect
            label="Missing cell policy"
            testId="ui04-pivot-missing-policy"
            choices={choices.missingCellPolicies}
            value={form.pivot.missingCellPolicy}
            placeholder="Choose how missing cells are handled"
            disabled={disabled}
            onChange={(missingCellPolicy) => setForm((previous) => ({
              ...previous,
              pivot: { ...previous.pivot, missingCellPolicy },
            }))}
          />
          <ChoiceSelect
            label="Unlisted category policy"
            testId="ui04-pivot-unlisted-policy"
            choices={choices.unlistedCategoryPolicies}
            value={form.pivot.unlistedCategoryPolicy}
            placeholder="Choose how new categories are handled"
            disabled={disabled}
            onChange={(unlistedCategoryPolicy) => setForm((previous) => ({
              ...previous,
              pivot: { ...previous.pivot, unlistedCategoryPolicy },
            }))}
          />
        </section>
      ) : null}

      {activeMode === 'UNPIVOT' ? (
        <section aria-label="Unpivot configuration" data-testid="ui04-unpivot-configuration" className="grid gap-3 rounded-lg border border-slate-200 p-3">
          <div>
            <h3 className="font-semibold text-slate-900">Unpivot</h3>
            <p className="mt-1 text-xs text-slate-600">Select inputs in output order and choose the server-provided key and value descriptors.</p>
          </div>
          <OrderedChoiceEditor
            label="Unpivot input columns"
            testId="ui04-unpivot-input-columns"
            choices={choices.unpivotColumns}
            selected={form.unpivot.inputColumns}
            disabled={disabled}
            onChange={(inputColumns) => setForm((previous) => ({
              ...previous,
              unpivot: { ...previous.unpivot, inputColumns },
            }))}
          />
          <ChoiceSelect
            label="Unpivot key output"
            testId="ui04-unpivot-key-output"
            choices={choices.unpivotKeyOutputs}
            value={form.unpivot.keyOutput}
            placeholder="Choose a key output descriptor"
            disabled={disabled}
            onChange={(keyOutput) => setForm((previous) => ({
              ...previous,
              unpivot: { ...previous.unpivot, keyOutput },
            }))}
          />
          {choiceFor(choices.unpivotKeyOutputs, form.unpivot.keyOutput) ? (
            <p className="text-xs text-slate-600">Key output type: {choiceFor(choices.unpivotKeyOutputs, form.unpivot.keyOutput)?.resultTypeLabel}</p>
          ) : null}
          <ChoiceSelect
            label="Unpivot value output"
            testId="ui04-unpivot-value-output"
            choices={choices.unpivotValueOutputs}
            value={form.unpivot.valueOutput}
            placeholder="Choose a value output descriptor"
            disabled={disabled}
            onChange={(valueOutput) => setForm((previous) => ({
              ...previous,
              unpivot: { ...previous.unpivot, valueOutput },
            }))}
          />
          {choiceFor(choices.unpivotValueOutputs, form.unpivot.valueOutput) ? (
            <p className="text-xs text-slate-600">Value output type: {choiceFor(choices.unpivotValueOutputs, form.unpivot.valueOutput)?.resultTypeLabel}</p>
          ) : null}
          <ChoiceSelect
            label="Unpivot null-row policy"
            testId="ui04-unpivot-null-row-policy"
            choices={choices.unpivotNullRowPolicies}
            value={form.unpivot.nullRowPolicy}
            placeholder="Choose how rows with no value are handled"
            disabled={disabled}
            onChange={(nullRowPolicy) => setForm((previous) => ({
              ...previous,
              unpivot: { ...previous.unpivot, nullRowPolicy },
            }))}
          />
        </section>
      ) : null}

      <section aria-label="Derived numeric columns" data-testid="ui04-derived-columns" className="grid gap-3 rounded-lg border border-slate-200 p-3">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div>
            <h3 className="font-semibold text-slate-900">Derived numeric columns</h3>
            <p className="mt-1 text-xs text-slate-600">Choose an output descriptor, operands and policies provided by the server.</p>
          </div>
          <button
            type="button"
            data-testid="ui04-add-derived-column"
            className="rounded border border-slate-300 bg-white px-3 py-2 text-sm font-medium hover:bg-slate-50 disabled:opacity-50"
            disabled={disabled || !canAddDerived}
            onClick={() => setForm((previous) => ({
              ...previous,
              derivedColumns: [...previous.derivedColumns, {
                localId: `draft-${nextLocalId.current++}`,
                output: null,
                operator: null,
                leftOperand: null,
                rightOperand: null,
                missingInputPolicy: null,
                divisionByZeroPolicy: null,
              }],
            }))}
          >
            Add derived column
          </button>
        </div>
        {choices.derivedAvailability.kind === 'unsupported' ? (
          <p role="status" className="text-sm text-amber-900">{choices.derivedAvailability.reason}</p>
        ) : null}
        {activeMode === 'UNPIVOT' && choices.unpivotWithDerivedAvailability.kind === 'unsupported' ? (
          <p role="status" data-testid="ui04-unpivot-derived-refusal" className="text-sm text-amber-900">
            {choices.unpivotWithDerivedAvailability.reason}
          </p>
        ) : null}
        {form.derivedColumns.map((derivedColumn, index) => {
          const operatorChoice = choiceFor(choices.binaryOperators, derivedColumn.operator);
          const outputChoice = choiceFor(choices.derivedOutputs, derivedColumn.output);
          return (
            <fieldset
              key={derivedColumn.localId}
              aria-label={`Derived column ${index + 1}`}
              data-testid={`ui04-derived-column-${index + 1}`}
              className="grid gap-3 rounded border border-slate-200 p-3"
            >
              <legend className="px-1 text-sm font-medium text-slate-800">Derived column {index + 1}</legend>
              <ChoiceSelect
                label={`Derived column ${index + 1} output`}
                testId={`ui04-derived-output-${index + 1}`}
                choices={choices.derivedOutputs}
                value={derivedColumn.output}
                placeholder="Choose an output descriptor"
                disabled={disabled}
                onChange={(output) => updateDerivedColumn(derivedColumn.localId, (previous) => ({ ...previous, output }))}
              />
              {outputChoice ? (
                <p className="text-xs text-slate-600">Output type: {outputChoice.resultTypeLabel}</p>
              ) : null}
              <ChoiceSelect
                label={`Derived column ${index + 1} operation`}
                testId={`ui04-derived-operator-${index + 1}`}
                choices={choices.binaryOperators}
                value={derivedColumn.operator}
                placeholder="Choose a server-supported operation"
                disabled={disabled}
                onChange={(operator) => updateDerivedColumn(derivedColumn.localId, (previous) => ({ ...previous, operator }))}
              />
              <ChoiceSelect
                label={`Derived column ${index + 1} first operand`}
                testId={`ui04-derived-left-operand-${index + 1}`}
                choices={choices.operands}
                value={derivedColumn.leftOperand}
                placeholder="Choose the first operand"
                disabled={disabled}
                onChange={(leftOperand) => updateDerivedColumn(derivedColumn.localId, (previous) => ({ ...previous, leftOperand }))}
              />
              <ChoiceSelect
                label={`Derived column ${index + 1} second operand`}
                testId={`ui04-derived-right-operand-${index + 1}`}
                choices={choices.operands}
                value={derivedColumn.rightOperand}
                placeholder="Choose the second operand"
                disabled={disabled}
                onChange={(rightOperand) => updateDerivedColumn(derivedColumn.localId, (previous) => ({ ...previous, rightOperand }))}
              />
              <ChoiceSelect
                label={`Derived column ${index + 1} missing-input policy`}
                testId={`ui04-derived-missing-input-policy-${index + 1}`}
                choices={choices.missingInputPolicies}
                value={derivedColumn.missingInputPolicy}
                placeholder="Choose a missing-input policy"
                disabled={disabled}
                onChange={(missingInputPolicy) => updateDerivedColumn(derivedColumn.localId, (previous) => ({ ...previous, missingInputPolicy }))}
              />
              {operatorChoice?.requiresDivisionByZeroPolicy ? (
                <ChoiceSelect
                  label={`Derived column ${index + 1} division-by-zero policy`}
                  testId={`ui04-derived-division-by-zero-policy-${index + 1}`}
                  choices={choices.divisionByZeroPolicies}
                  value={derivedColumn.divisionByZeroPolicy}
                  placeholder="Choose a division-by-zero policy"
                  disabled={disabled}
                  onChange={(divisionByZeroPolicy) => updateDerivedColumn(derivedColumn.localId, (previous) => ({ ...previous, divisionByZeroPolicy }))}
                />
              ) : null}
              <button
                type="button"
                aria-label={`Remove derived column ${index + 1}`}
                data-testid={`ui04-remove-derived-column-${index + 1}`}
                className="justify-self-start rounded border border-red-300 px-3 py-2 text-sm font-medium text-red-800 hover:bg-red-50 disabled:opacity-50"
                disabled={disabled}
                onClick={() => setForm((previous) => ({
                  ...previous,
                  derivedColumns: previous.derivedColumns.filter((item) => item.localId !== derivedColumn.localId),
                }))}
              >
                Remove derived column
              </button>
            </fieldset>
          );
        })}
      </section>

      <div className="flex flex-wrap justify-end gap-2">
        <button
          type="button"
          className="rounded border border-slate-300 bg-white px-3 py-2 text-sm font-medium hover:bg-slate-50"
          data-testid="ui04-cancel-table-shape"
          onClick={cancel}
        >
          Cancel
        </button>
        <button
          type="button"
          className="rounded bg-blue-700 px-3 py-2 text-sm font-semibold text-white hover:bg-blue-800 disabled:opacity-50"
          data-testid="ui04-apply-table-shape"
          disabled={disabled || !definition}
          onClick={() => definition && onApply(definition)}
        >
          Apply table shape
        </button>
      </div>
    </section>
  );
};
