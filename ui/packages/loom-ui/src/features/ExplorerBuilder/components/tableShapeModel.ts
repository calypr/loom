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

export interface OutputNameDraft {
  readonly column: string;
  readonly label: string;
}

export interface ReshapeModeChoice extends ServerChoice<'reshapeMode'> {
  readonly mode: ReshapeMode;
}

export type ReshapeMode = 'NONE' | 'GROUPED_PIVOT' | 'UNPIVOT';
export type ColumnChoice = ServerChoice<'column'>;
export type DuplicatePolicyChoice = ServerChoice<'duplicatePolicy'>;
export type MissingCellPolicyChoice = ServerChoice<'missingCellPolicy'>;
export type UnlistedCategoryPolicyChoice = ServerChoice<'unlistedCategoryPolicy'>;
export type MissingInputPolicyChoice = ServerChoice<'missingInputPolicy'>;
export type DivisionByZeroPolicyChoice = ServerChoice<'divisionByZeroPolicy'>;
export type UnpivotNullRowPolicyChoice = ServerChoice<'unpivotNullRowPolicy'>;
export type OperandChoice = ServerChoice<'operand'>;
export type NumericLiteralRepresentation = 'integer' | 'decimal';

export type PivotOutputReference =
  | {
      readonly kind: 'group';
      readonly column: ChoiceReference<'column'>;
    }
  | {
      readonly kind: 'category';
      readonly category: ChoiceReference<'pivotCategory'>;
    };

export interface PivotOutputChoice {
  readonly reference: PivotOutputReference;
  readonly label: string;
}

export type DerivedOperandDraft =
  | {
      readonly kind: 'base';
      readonly reference: ChoiceReference<'operand'> | null;
    }
  | {
      readonly kind: 'derived';
      readonly localId: string | null;
    }
  | {
      readonly kind: 'pivotOutput';
      readonly reference: PivotOutputReference | null;
    }
  | {
      readonly kind: 'literal';
      readonly text: string;
    };

export type DerivedOperandIntent =
  | {
      readonly kind: 'base';
      readonly reference: ChoiceReference<'operand'>;
    }
  | {
      readonly kind: 'derived';
      readonly localId: string;
    }
  | {
      readonly kind: 'pivotOutput';
      readonly reference: PivotOutputReference;
    }
  | {
      readonly kind: 'literal';
      readonly representation: NumericLiteralRepresentation;
      readonly text: string;
    };

export interface OutputNameSuggestion<Kind extends string> extends ServerChoice<Kind> {
  readonly suggestedOutput: OutputNameDraft;
}

export interface TypedOutputNameSuggestion<Kind extends string> extends OutputNameSuggestion<Kind> {
  readonly resultTypeLabel: string;
}

export interface BinaryOperatorChoice extends ServerChoice<'binaryOperator'> {
  readonly requiresDivisionByZeroPolicy: boolean;
}

export interface PivotCategoryChoice extends ServerChoice<'pivotCategory'> {
  readonly suggestedOutput: OutputNameDraft;
}

export interface PivotCategoryPair {
  readonly categoryColumn: ChoiceReference<'column'>;
  readonly valueColumn: ChoiceReference<'column'>;
}

export type PivotCategoryDiscovery =
  | { readonly kind: 'not-requested' }
  | { readonly kind: 'loading'; readonly pair: PivotCategoryPair }
  | { readonly kind: 'unavailable'; readonly pair: PivotCategoryPair; readonly reason: string }
  | {
      readonly kind: 'complete';
      readonly discoveryIdentity: string;
      readonly pair: PivotCategoryPair;
      readonly categories: ReadonlyArray<PivotCategoryChoice>;
    };

export type OutputDescriptorSupport<Kind extends string> =
  | {
      readonly kind: 'supported';
      readonly resultTypeLabel: string;
      readonly suggestions: ReadonlyArray<TypedOutputNameSuggestion<Kind>>;
    }
  | { readonly kind: 'unsupported'; readonly reason: string };

export type UnpivotKeyOutputSupport = OutputDescriptorSupport<'unpivotKeyOutput'>;
export type UnpivotValueOutputSupport = OutputDescriptorSupport<'unpivotValueOutput'>;
export type DerivedOutputSuggestion = TypedOutputNameSuggestion<'derivedOutput'>;

export interface TableShapeEditorChoices {
  readonly reshapeModes: ReadonlyArray<ReshapeModeChoice>;
  readonly groupColumns: ReadonlyArray<ColumnChoice>;
  readonly categoryColumns: ReadonlyArray<ColumnChoice>;
  readonly valueColumns: ReadonlyArray<ColumnChoice>;
  readonly pivotCategoryDiscovery: PivotCategoryDiscovery;
  readonly duplicatePolicies: ReadonlyArray<DuplicatePolicyChoice>;
  readonly missingCellPolicies: ReadonlyArray<MissingCellPolicyChoice>;
  readonly unlistedCategoryPolicies: ReadonlyArray<UnlistedCategoryPolicyChoice>;
  readonly unpivotColumns: ReadonlyArray<ColumnChoice>;
  readonly unpivotKeyOutput: UnpivotKeyOutputSupport;
  readonly unpivotValueOutput: UnpivotValueOutputSupport;
  readonly unpivotNullRowPolicies: ReadonlyArray<UnpivotNullRowPolicyChoice>;
  readonly derivedAvailability: ChoiceAvailability;
  readonly unpivotWithDerivedAvailability: ChoiceAvailability;
  readonly derivedOutputSuggestions: ReadonlyArray<DerivedOutputSuggestion>;
  readonly binaryOperators: ReadonlyArray<BinaryOperatorChoice>;
  readonly operands: ReadonlyArray<OperandChoice>;
  readonly missingInputPolicies: ReadonlyArray<MissingInputPolicyChoice>;
  readonly divisionByZeroPolicies: ReadonlyArray<DivisionByZeroPolicyChoice>;
}

type NonEmptyReadonlyArray<Value> = readonly [Value, ...Value[]];

export interface PivotCategorySelection {
  readonly category: ChoiceReference<'pivotCategory'>;
  readonly output: OutputNameDraft;
}

export interface PivotProposal {
  readonly groupColumns: NonEmptyReadonlyArray<ChoiceReference<'column'>>;
  readonly categoryColumn: ChoiceReference<'column'>;
  readonly valueColumn: ChoiceReference<'column'>;
  readonly categoryDiscoveryIdentity: string;
  readonly includedCategories: NonEmptyReadonlyArray<PivotCategorySelection>;
  readonly duplicatePolicy: ChoiceReference<'duplicatePolicy'>;
  readonly missingCellPolicy: ChoiceReference<'missingCellPolicy'>;
  readonly unlistedCategoryPolicy: ChoiceReference<'unlistedCategoryPolicy'>;
}

export interface UnpivotProposal {
  readonly inputColumns: NonEmptyReadonlyArray<ChoiceReference<'column'>>;
  readonly keyOutput: OutputNameDraft;
  readonly valueOutput: OutputNameDraft;
  readonly nullRowPolicy: ChoiceReference<'unpivotNullRowPolicy'>;
}

interface DerivedColumnProposalBase {
  readonly localId: string;
  readonly output: OutputNameDraft;
  readonly operator: ChoiceReference<'binaryOperator'>;
  readonly leftOperand: DerivedOperandIntent;
  readonly rightOperand: DerivedOperandIntent;
  readonly missingInputPolicy: ChoiceReference<'missingInputPolicy'>;
}

export type DerivedColumnProposal =
  | (DerivedColumnProposalBase & {
      readonly divisionByZeroPolicy: ChoiceReference<'divisionByZeroPolicy'>;
    })
  | (DerivedColumnProposalBase & { readonly divisionByZeroPolicy?: never });

export type TableShapeProposalIntent =
  | {
      readonly kind: 'NONE';
      readonly reshapeMode: ChoiceReference<'reshapeMode'>;
      readonly derivedColumns: ReadonlyArray<DerivedColumnProposal>;
    }
  | {
      readonly kind: 'GROUPED_PIVOT';
      readonly reshapeMode: ChoiceReference<'reshapeMode'>;
      readonly pivot: PivotProposal;
      readonly derivedColumns: ReadonlyArray<DerivedColumnProposal>;
    }
  | {
      readonly kind: 'UNPIVOT';
      readonly reshapeMode: ChoiceReference<'reshapeMode'>;
      readonly unpivot: UnpivotProposal;
      readonly derivedColumns: ReadonlyArray<DerivedColumnProposal>;
    };

export interface PivotFormState {
  readonly groupColumns: ReadonlyArray<ChoiceReference<'column'>>;
  readonly categoryColumn: ChoiceReference<'column'> | null;
  readonly valueColumn: ChoiceReference<'column'> | null;
  readonly includedCategories: ReadonlyArray<PivotCategorySelection>;
  readonly duplicatePolicy: ChoiceReference<'duplicatePolicy'> | null;
  readonly missingCellPolicy: ChoiceReference<'missingCellPolicy'> | null;
  readonly unlistedCategoryPolicy: ChoiceReference<'unlistedCategoryPolicy'> | null;
}

export interface UnpivotFormState {
  readonly inputColumns: ReadonlyArray<ChoiceReference<'column'>>;
  readonly keyOutput: OutputNameDraft;
  readonly valueOutput: OutputNameDraft;
  readonly nullRowPolicy: ChoiceReference<'unpivotNullRowPolicy'> | null;
}

export interface DerivedColumnFormState {
  readonly localId: string;
  readonly output: OutputNameDraft;
  readonly operator: ChoiceReference<'binaryOperator'> | null;
  readonly leftOperand: DerivedOperandDraft;
  readonly rightOperand: DerivedOperandDraft;
  readonly missingInputPolicy: ChoiceReference<'missingInputPolicy'> | null;
  readonly divisionByZeroPolicy: ChoiceReference<'divisionByZeroPolicy'> | null;
}

export interface TableShapeFormState {
  readonly mode: ChoiceReference<'reshapeMode'> | null;
  readonly pivot: PivotFormState;
  readonly unpivot: UnpivotFormState;
  readonly derivedColumns: ReadonlyArray<DerivedColumnFormState>;
}

export const emptyOutputName = (): OutputNameDraft => ({ column: '', label: '' });

export const emptyOperandDraft = (): DerivedOperandDraft => ({ kind: 'base', reference: null });

export const emptyPivot = (): PivotFormState => ({
  groupColumns: [],
  categoryColumn: null,
  valueColumn: null,
  includedCategories: [],
  duplicatePolicy: null,
  missingCellPolicy: null,
  unlistedCategoryPolicy: null,
});

export const emptyUnpivot = (): UnpivotFormState => ({
  inputColumns: [],
  keyOutput: emptyOutputName(),
  valueOutput: emptyOutputName(),
  nullRowPolicy: null,
});

export const referenceFor = <Kind extends string>(
  choice: ServerChoice<Kind>,
): ChoiceReference<Kind> => ({ kind: choice.choiceKind, choiceId: choice.choiceId });

export const choiceFor = <Choice extends ServerChoice<string>>(
  choices: ReadonlyArray<Choice>,
  reference: ChoiceReference<Choice['choiceKind']> | null,
): Choice | undefined =>
  reference ? choices.find((choice) => choice.choiceId === reference.choiceId) : undefined;

export const pivotOutputReferenceKey = (reference: PivotOutputReference): string => {
  switch (reference.kind) {
    case 'group':
      return `group:${reference.column.choiceId}`;
    case 'category':
      return `category:${reference.category.choiceId}`;
    default: {
      const exhaustive: never = reference;
      return exhaustive;
    }
  }
};

export const samePivotOutputReference = (
  left: PivotOutputReference,
  right: PivotOutputReference,
): boolean => {
  if (left.kind !== right.kind) return false;
  if (left.kind === 'group' && right.kind === 'group') {
    return sameChoiceReference(left.column, right.column);
  }
  if (left.kind === 'category' && right.kind === 'category') {
    return sameChoiceReference(left.category, right.category);
  }
  return false;
};

export const pivotOutputChoicesFor = (
  pivot: PivotFormState,
  choices: TableShapeEditorChoices,
): ReadonlyArray<PivotOutputChoice> => {
  const groupOutputs = pivot.groupColumns.map((column, index) => ({
    reference: { kind: 'group', column } satisfies PivotOutputReference,
    label: `Group output: ${choiceFor(choices.groupColumns, column)?.label ?? `Group column ${index + 1}`}`,
  }));
  const categoryOutputs = pivot.includedCategories.map((selection, index) => {
    const serverCategory = choices.pivotCategoryDiscovery.kind === 'complete'
      ? choices.pivotCategoryDiscovery.categories.find(
          (category) => category.choiceId === selection.category.choiceId,
        )
      : undefined;
    const outputLabel = selection.output.column.trim() || selection.output.label.trim() ||
      serverCategory?.label || `Category output ${index + 1}`;
    return {
      reference: { kind: 'category', category: selection.category } satisfies PivotOutputReference,
      label: `Category output: ${outputLabel}`,
    };
  });
  return [...groupOutputs, ...categoryOutputs];
};

export const sameChoiceReference = <Kind extends string>(
  left: ChoiceReference<Kind> | null,
  right: ChoiceReference<Kind> | null,
): boolean => left?.choiceId === right?.choiceId;

export const pairForPivot = (pivot: PivotFormState): PivotCategoryPair | undefined => {
  const categoryColumn = pivot.categoryColumn;
  const valueColumn = pivot.valueColumn;
  return categoryColumn && valueColumn ? { categoryColumn, valueColumn } : undefined;
};

export const samePivotPair = (left: PivotCategoryPair, right: PivotCategoryPair): boolean =>
  sameChoiceReference(left.categoryColumn, right.categoryColumn) &&
  sameChoiceReference(left.valueColumn, right.valueColumn);

export const proposalIntentToForm = (intent: TableShapeProposalIntent): TableShapeFormState => {
  const derivedColumns = intent.derivedColumns.map((derivedColumn) => ({
    localId: derivedColumn.localId,
    output: derivedColumn.output,
    operator: derivedColumn.operator,
    leftOperand: operandDraftFor(derivedColumn.leftOperand),
    rightOperand: operandDraftFor(derivedColumn.rightOperand),
    missingInputPolicy: derivedColumn.missingInputPolicy,
    divisionByZeroPolicy: derivedColumn.divisionByZeroPolicy ?? null,
  }));

  if (intent.kind === 'NONE') {
    return {
      mode: intent.reshapeMode,
      pivot: emptyPivot(),
      unpivot: emptyUnpivot(),
      derivedColumns,
    };
  }

  if (intent.kind === 'GROUPED_PIVOT') {
    return {
      mode: intent.reshapeMode,
      pivot: {
        groupColumns: intent.pivot.groupColumns,
        categoryColumn: intent.pivot.categoryColumn,
        valueColumn: intent.pivot.valueColumn,
        includedCategories: intent.pivot.includedCategories,
        duplicatePolicy: intent.pivot.duplicatePolicy,
        missingCellPolicy: intent.pivot.missingCellPolicy,
        unlistedCategoryPolicy: intent.pivot.unlistedCategoryPolicy,
      },
      unpivot: emptyUnpivot(),
      derivedColumns,
    };
  }

  return {
    mode: intent.reshapeMode,
    pivot: emptyPivot(),
    unpivot: {
      inputColumns: intent.unpivot.inputColumns,
      keyOutput: intent.unpivot.keyOutput,
      valueOutput: intent.unpivot.valueOutput,
      nullRowPolicy: intent.unpivot.nullRowPolicy,
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

const outputNamesAreCompleteAndUnique = (
  outputs: ReadonlyArray<OutputNameDraft>,
): boolean => {
  const normalizedColumns = outputs.map((output) => output.column.trim());
  return outputs.every((output) => output.column.trim() !== '' && output.label.trim() !== '') &&
    new Set(normalizedColumns).size === normalizedColumns.length;
};

const operandDraftFor = (operand: DerivedOperandIntent): DerivedOperandDraft => {
  switch (operand.kind) {
    case 'base':
      return { kind: 'base', reference: operand.reference };
    case 'derived':
      return { kind: 'derived', localId: operand.localId };
    case 'pivotOutput':
      return { kind: 'pivotOutput', reference: operand.reference };
    case 'literal':
      return { kind: 'literal', text: operand.text };
    default: {
      const exhaustive: never = operand;
      return exhaustive;
    }
  }
};

const numericLiteralFor = (draft: Extract<DerivedOperandDraft, { readonly kind: 'literal' }>): DerivedOperandIntent | undefined => {
  const text = draft.text.trim();
  if (text === '' || !/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(text)) {
    return undefined;
  }
  const value = Number(text);
  if (!Number.isFinite(value)) return undefined;

  const representation: NumericLiteralRepresentation = /[.eE]/.test(text) ? 'decimal' : 'integer';
  if (representation === 'integer' && !Number.isInteger(value)) return undefined;
  return { kind: 'literal', representation, text };
};

const derivedColumnIdsAreUnique = (columns: ReadonlyArray<DerivedColumnFormState>): boolean => {
  const localIds = columns.map((column) => column.localId.trim());
  return localIds.every((localId) => localId !== '') &&
    new Set(localIds).size === localIds.length;
};

const operandIntentFor = ({
  operand,
  columnIndex,
  columns,
  mode,
  pivot,
  choices,
}: {
  readonly operand: DerivedOperandDraft;
  readonly columnIndex: number;
  readonly columns: ReadonlyArray<DerivedColumnFormState>;
  readonly mode: ReshapeMode;
  readonly pivot: PivotFormState;
  readonly choices: TableShapeEditorChoices;
}): DerivedOperandIntent | undefined => {
  switch (operand.kind) {
    case 'base': {
      if (mode === 'GROUPED_PIVOT') return undefined;
      const choice = choiceFor(choices.operands, operand.reference);
      return choice?.availability.kind === 'supported'
        ? { kind: 'base', reference: referenceFor(choice) }
        : undefined;
    }
    case 'derived': {
      if (operand.localId === null) return undefined;
      const referencedIndex = columns.findIndex((column) => column.localId === operand.localId);
      return referencedIndex >= 0 && referencedIndex < columnIndex
        ? { kind: 'derived', localId: operand.localId }
        : undefined;
    }
    case 'pivotOutput': {
      const reference = operand.reference;
      if (reference === null || mode !== 'GROUPED_PIVOT') return undefined;
      const isSelected = reference.kind === 'group'
        ? pivot.groupColumns.some((column) => sameChoiceReference(column, reference.column))
        : pivot.includedCategories.some((selection) =>
            sameChoiceReference(selection.category, reference.category),
          );
      return isSelected ? { kind: 'pivotOutput', reference } : undefined;
    }
    case 'literal':
      return numericLiteralFor(operand);
    default: {
      const exhaustive: never = operand;
      return exhaustive;
    }
  }
};

const derivedProposalsFor = (
  form: TableShapeFormState,
  choices: TableShapeEditorChoices,
  mode: ReshapeMode,
): ReadonlyArray<DerivedColumnProposal> | undefined => {
  if (form.derivedColumns.length > 0 && choices.derivedAvailability.kind === 'unsupported') {
    return undefined;
  }
  if (!derivedColumnIdsAreUnique(form.derivedColumns)) return undefined;

  const result: DerivedColumnProposal[] = [];
  for (const [columnIndex, derivedColumn] of form.derivedColumns.entries()) {
    const operator = choiceFor(choices.binaryOperators, derivedColumn.operator);
    const leftOperand = operandIntentFor({
      operand: derivedColumn.leftOperand,
      columnIndex,
      columns: form.derivedColumns,
      mode,
      pivot: form.pivot,
      choices,
    });
    const rightOperand = operandIntentFor({
      operand: derivedColumn.rightOperand,
      columnIndex,
      columns: form.derivedColumns,
      mode,
      pivot: form.pivot,
      choices,
    });
    const missingInputPolicy = choiceFor(choices.missingInputPolicies, derivedColumn.missingInputPolicy);
    if (
      !operator || operator.availability.kind !== 'supported' ||
      !leftOperand ||
      !rightOperand ||
      !missingInputPolicy || missingInputPolicy.availability.kind !== 'supported'
    ) {
      return undefined;
    }

    const base = {
      localId: derivedColumn.localId,
      output: derivedColumn.output,
      operator: referenceFor(operator),
      leftOperand,
      rightOperand,
      missingInputPolicy: referenceFor(missingInputPolicy),
    };

    if (!operator.requiresDivisionByZeroPolicy) {
      result.push(base);
      continue;
    }

    const divisionByZeroPolicy = choiceFor(
      choices.divisionByZeroPolicies,
      derivedColumn.divisionByZeroPolicy,
    );
    if (!divisionByZeroPolicy || divisionByZeroPolicy.availability.kind !== 'supported') {
      return undefined;
    }
    result.push({ ...base, divisionByZeroPolicy: referenceFor(divisionByZeroPolicy) });
  }

  return result;
};

export const proposalIntentFor = ({
  form,
  choices,
  categoryDiscovery,
}: {
  readonly form: TableShapeFormState;
  readonly choices: TableShapeEditorChoices;
  readonly categoryDiscovery: PivotCategoryDiscovery;
}): TableShapeProposalIntent | undefined => {
  const modeChoice = choiceFor(choices.reshapeModes, form.mode);
  if (!modeChoice || modeChoice.availability.kind !== 'supported' || !form.mode) return undefined;
  const derivedColumns = derivedProposalsFor(form, choices, modeChoice.mode);
  if (!derivedColumns) return undefined;

  switch (modeChoice.mode) {
    case 'NONE': {
      if (!outputNamesAreCompleteAndUnique(derivedColumns.map((column) => column.output))) {
        return undefined;
      }
      return { kind: 'NONE', reshapeMode: form.mode, derivedColumns };
    }
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
      const pair = pairForPivot(form.pivot);
      const currentDiscovery = categoryDiscovery.kind === 'complete' && pair &&
        samePivotPair(categoryDiscovery.pair, pair)
        ? categoryDiscovery
        : undefined;
      if (
        !groupColumns ||
        !includedCategories ||
        !allChoicesSupported(choices.groupColumns, form.pivot.groupColumns) ||
        !categoryColumn || categoryColumn.availability.kind !== 'supported' ||
        !valueColumn || valueColumn.availability.kind !== 'supported' ||
        !currentDiscovery ||
        !includedCategories.every((selection) =>
          currentDiscovery.categories.some((category) =>
            category.choiceId === selection.category.choiceId &&
            category.availability.kind === 'supported',
          ),
        ) ||
        !duplicatePolicy || duplicatePolicy.availability.kind !== 'supported' ||
        !missingCellPolicy || missingCellPolicy.availability.kind !== 'supported' ||
        !unlistedCategoryPolicy || unlistedCategoryPolicy.availability.kind !== 'supported'
      ) {
        return undefined;
      }

      const outputs = [
        ...includedCategories.map((selection) => selection.output),
        ...derivedColumns.map((column) => column.output),
      ];
      if (!outputNamesAreCompleteAndUnique(outputs)) return undefined;

      return {
        kind: 'GROUPED_PIVOT',
        reshapeMode: form.mode,
        pivot: {
          groupColumns,
          categoryColumn: referenceFor(categoryColumn),
          valueColumn: referenceFor(valueColumn),
          categoryDiscoveryIdentity: currentDiscovery.discoveryIdentity,
          includedCategories,
          duplicatePolicy: referenceFor(duplicatePolicy),
          missingCellPolicy: referenceFor(missingCellPolicy),
          unlistedCategoryPolicy: referenceFor(unlistedCategoryPolicy),
        },
        derivedColumns,
      };
    }
    case 'UNPIVOT': {
      const inputColumns = nonEmpty(form.unpivot.inputColumns);
      const nullRowPolicy = choiceFor(
        choices.unpivotNullRowPolicies,
        form.unpivot.nullRowPolicy,
      );
      if (
        (derivedColumns.length > 0 && choices.unpivotWithDerivedAvailability.kind === 'unsupported') ||
        !inputColumns ||
        !allChoicesSupported(choices.unpivotColumns, form.unpivot.inputColumns) ||
        choices.unpivotKeyOutput.kind !== 'supported' ||
        choices.unpivotValueOutput.kind !== 'supported' ||
        !nullRowPolicy || nullRowPolicy.availability.kind !== 'supported'
      ) {
        return undefined;
      }

      const outputs = [form.unpivot.keyOutput, form.unpivot.valueOutput, ...derivedColumns.map((column) => column.output)];
      if (!outputNamesAreCompleteAndUnique(outputs)) return undefined;

      return {
        kind: 'UNPIVOT',
        reshapeMode: form.mode,
        unpivot: {
          inputColumns,
          keyOutput: form.unpivot.keyOutput,
          valueOutput: form.unpivot.valueOutput,
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
