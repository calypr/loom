import React, { useEffect, useId, useState } from 'react';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionOperation,
  ConstructionProposalRequest,
  ConstructionStageDescriptor,
  ConstructionStep,
} from '../../../types';
import { constructionInputForStage } from '../constructionWorkspace/constructionStages';
import { classifySourcePickerEmptiness, sourcePickerBoundSet, type SourcePickerEmptiness } from './sourcePickerExplanations';
import { existingColumnForFieldPath, type PublicFieldCarrier } from './existingFieldReuse';
import { SourcePickerExplanation } from '../components/SourcePickerExplanation';

type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'filterSources' | 'deriveSources'
>;

type FilterOperation = Extract<ConstructionOperation, { readonly kind: 'FILTER' }>;
type FilterPayload = FilterOperation['filter'];
type FilterValue = NonNullable<FilterPayload['values']>[number];
type FilterValueKind = FilterValue['kind'];
type FilterOperator = FilterPayload['operator'];
type ScalarFilterOperator = Exclude<FilterOperator, 'EXISTS' | 'MISSING' | 'IN'>;
type DeriveOperation = Extract<ConstructionOperation, { readonly kind: 'DERIVE' }>;
type DerivePayload = DeriveOperation['derive'];
type DerivedOperand = DerivePayload['left'];
type DerivedLiteral = Extract<DerivedOperand, { readonly kind: 'LITERAL' }>['literal'];
type DerivedLiteralKind = DerivedLiteral['kind'];
type DerivedOutput = ConstructionStep['outputs'][number];
type DerivedOperation = DerivePayload['operation'];
type FilterSourceInput = ConstructionCapabilitiesResponse['filterSourceInput'];
type FilterSourceChoice = NonNullable<FilterSourceInput>['choices'][number];
type FilterSourceSelection = NonNullable<ConstructionProposalRequest['filterSources']>[number];
type DeriveSourceInput = ConstructionCapabilitiesResponse['deriveSourceInput'];
type DeriveSourceChoice = NonNullable<DeriveSourceInput>['choices'][number];
type DeriveSourceSelection = NonNullable<ConstructionProposalRequest['deriveSources']>[number];
type SourceChoiceLike = FilterSourceChoice | DeriveSourceChoice;
type SourceSelectionLike = { readonly choiceId: string; readonly columnId: string };
type FilterStageColumn = ConstructionStageDescriptor['columns'][number];

interface ArithmeticExpressionDraft {
  readonly operation: DerivedOperation;
  readonly left: OperandDraft;
  readonly right: OperandDraft;
}

type ArithmeticExpression = Pick<DerivePayload, 'operation' | 'left' | 'right'>;

type ExpressionState =
  | { readonly kind: 'guided'; readonly draft: ArithmeticExpressionDraft }
  | { readonly kind: 'formula-valid'; readonly expression: ArithmeticExpression; readonly source: string }
  | { readonly kind: 'formula-invalid'; readonly lastDraft: ArithmeticExpressionDraft; readonly source: string; readonly error: string };

type FilterForm =
  | { readonly kind: 'exists'; readonly columnId: string; readonly filterSources?: ReadonlyArray<FilterSourceSelection> }
  | { readonly kind: 'missing'; readonly columnId: string; readonly filterSources?: ReadonlyArray<FilterSourceSelection> }
  | {
      readonly kind: 'comparison';
      readonly operator: ScalarFilterOperator;
      readonly columnId: string;
      readonly value: FilterValueDraft;
      readonly filterSources?: ReadonlyArray<FilterSourceSelection>;
    }
  | {
      readonly kind: 'membership';
      readonly operator: 'IN';
      readonly columnId: string;
      readonly values: ReadonlyArray<FilterValueDraft>;
      readonly filterSources?: ReadonlyArray<FilterSourceSelection>;
    }
  | { readonly kind: 'unsupported'; readonly message: string };

type FilterValueDraft =
  | { readonly kind: 'blank' | 'edited'; readonly text: string }
  | { readonly kind: 'saved'; readonly text: string; readonly value: FilterValue };

type OperandDraft =
  | { readonly kind: 'unset' }
  | { readonly kind: 'column'; readonly columnId: string }
  | {
      readonly kind: 'literal';
      readonly literalKind: DerivedLiteralKind;
      readonly text: string;
      readonly edited: boolean;
    };

interface CalculationForm {
  readonly expression: ExpressionState;
  readonly missingInputPolicy: DerivePayload['missingInputPolicy'];
  readonly divisionByZeroPolicy: NonNullable<DerivePayload['divisionByZeroPolicy']>;
  readonly outputName: string;
  readonly outputLabel: string;
  readonly deriveSources?: ReadonlyArray<DeriveSourceSelection>;
}

export interface ConstructionOperationEditorProps {
  readonly family: 'KEEP_ROWS' | 'CALCULATE';
  readonly construction: Construction;
  readonly capabilities: ConstructionCapabilitiesResponse;
  /** The step being changed. When absent, the editor appends one step. */
  readonly editingStep?: ConstructionStep;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly disabled: boolean;
  /** Undefined clears any prior preview while the visible form is incomplete. */
  readonly onCandidateChange: (candidate: CandidateIntent | undefined) => void;
  /** The host loads the predecessor-stage capabilities and reopens this step. */
  readonly onEditStep: (stepId: string) => void;
}

const createOpaqueId = (prefix: string): string => {
  const randomPart = globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2);
  return `${prefix}_${randomPart}`;
};

const normalizedLogicalType = (type: string | undefined): string =>
  (type ?? '').trim().toLowerCase().replace(/[\s-]+/g, '_');

const filterValueKindFor = (type: string | undefined): FilterValueKind | undefined => {
  switch (normalizedLogicalType(type)) {
    case 'string':
    case 'uuid':
      return 'STRING';
    case 'code':
      return 'CODE';
    case 'boolean':
      return 'BOOLEAN';
    case 'integer':
      return 'INTEGER';
    case 'decimal':
    case 'number':
      return 'DECIMAL';
    case 'date':
      return 'DATE';
    case 'date_time':
    case 'datetime':
      return 'DATE_TIME';
    default:
      return undefined;
  }
};

const isNumericColumn = (column: ConstructionStageDescriptor['columns'][number]): boolean => {
  const type = normalizedLogicalType(column.type);
  return type === 'integer' || type === 'decimal' || type === 'number';
};

const normalizedName = (value: string): string => {
  const normalized = value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'column';
  return /^[0-9]/.test(normalized) ? `column_${normalized}` : normalized;
};

const uniqueFilterSourceName = (base: string, used: ReadonlySet<string>): string => {
  const normalized = normalizedName(base);
  if (!used.has(normalized)) return normalized;
  let suffix = 2;
  while (used.has(`${normalized}_${suffix}`)) suffix += 1;
  return `${normalized}_${suffix}`;
};

const filterSourceFieldPathLabel = (path: string): string => {
  const words: string[] = [];
  for (const segment of path.replace(/^root\./, '').split('.')) {
    const readable = segment.replace(/\[\]/g, '').replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/[_-]+/g, ' ').trim();
    if (!readable) continue;
    words.push(readable.toLowerCase() === 'id' ? 'ID' : readable[0].toUpperCase() + readable.slice(1));
  }
  return words.join(' ');
};

const filterSourceLabel = (choice: SourceChoiceLike): string => {
  const rawPath = choice.fieldPath.replace(/^root\./, '');
  const label = choice.label.trim().replace(/^root\./, '');
  const prefixedPath = label.endsWith(`.${rawPath}`) ? label.slice(0, -rawPath.length - 1) : undefined;
  const rawSegments = rawPath.split('.');
  const rawPathResource = rawSegments.length > 1 && /^[A-Z]/.test(rawSegments[0] ?? '') ? rawSegments[0] : undefined;
  const context = prefixedPath ?? (label === rawPath ? rawPathResource : undefined);
  const fieldPath = rawPathResource && context === rawPathResource ? rawSegments.slice(1).join('.') : rawPath;
  const labelIsPath = label === rawPath || Boolean(prefixedPath) || label === choice.fieldPath;
  const field = labelIsPath ? filterSourceFieldPathLabel(fieldPath) : label;
  return context ? `${context} · ${field}` : field;
};

const filterSourceColumn = (
  choice: SourceChoiceLike,
  columnId: string,
  stage: ConstructionStageDescriptor,
): FilterStageColumn => {
  const usedNames = new Set(stage.columns.map((column) => column.name.toLowerCase()));
  return {
    id: columnId,
    name: uniqueFilterSourceName(choice.fieldPath, usedNames),
    label: choice.label,
    type: choice.logicalType,
    cardinality: 'optional_one',
  };
};

const liveFilterSources = (form: FilterForm): ReadonlyArray<FilterSourceSelection> =>
  form.kind === 'unsupported' || !form.filterSources?.length
    ? []
    : form.filterSources.filter((binding) => binding.columnId === form.columnId);

// A DERIVE step consumes its two operands as a list, so a live binding is one
// whose column is actually referenced by either arithmetic operand.
const liveDeriveSources = (form: CalculationForm): ReadonlyArray<DeriveSourceSelection> => {
  if (!form.deriveSources?.length) return [];
  const draft = expressionDraftFor(form.expression);
  const consumed = new Set(
    [draft.left, draft.right]
      .filter((operand): operand is Extract<OperandDraft, { readonly kind: 'column' }> => operand.kind === 'column')
      .map((operand) => operand.columnId),
  );
  return form.deriveSources.filter((binding) => binding.columnId.trim() !== '' && consumed.has(binding.columnId));
};

const deriveSourceStage = (
  stage: ConstructionStageDescriptor,
  sourceInput: DeriveSourceInput | undefined,
  deriveSources: ReadonlyArray<DeriveSourceSelection>,
): ConstructionStageDescriptor => {
  const choices = sourceInput?.choices ?? [];
  const inputs = deriveSources.flatMap((selection) => {
    if (selection.columnId.trim() === '') return [];
    if (stage.columns.some((column) => column.id === selection.columnId)) return [];
    const choice = choices.find((candidate) => candidate.choiceId === selection.choiceId);
    return choice ? [filterSourceColumn(choice, selection.columnId, stage)] : [];
  });
  return inputs.length ? { ...stage, columns: [...stage.columns, ...inputs] } : stage;
};

const deriveSourceChoiceForColumn = (
  choices: ReadonlyArray<DeriveSourceChoice>,
  columnId: string,
): DeriveSourceChoice | undefined => {
  if (columnId.trim() === '') return undefined;
  return choices.find((choice) => materializedChoiceColumnId(choice) === columnId);
};

// A signed dataset-field source is not a stage column, so the same derived column
// Loom will materialize for it has to be visible in step outputs and lookups.
const filterInputStage = (
  stage: ConstructionStageDescriptor,
  sourceInput: FilterSourceInput,
  filterSources: ReadonlyArray<FilterSourceSelection>,
): ConstructionStageDescriptor => {
  const choices = sourceInput?.choices ?? [];
  const inputs = filterSources.flatMap((selection) => {
    if (selection.columnId.trim() === '') return [];
    if (stage.columns.some((column) => column.id === selection.columnId)) return [];
    const choice = choices.find((candidate) => candidate.choiceId === selection.choiceId);
    return choice ? [filterSourceColumn(choice, selection.columnId, stage)] : [];
  });
  return inputs.length ? { ...stage, columns: [...stage.columns, ...inputs] } : stage;
};

const materializedChoiceColumnId = (choice: SourceChoiceLike): string | undefined => {
  const columnId = choice.columnId ?? '';
  return columnId.trim() === '' ? undefined : choice.columnId;
};

const filterSourceChoiceForColumn = (
  choices: ReadonlyArray<FilterSourceChoice>,
  columnId: string,
): FilterSourceChoice | undefined => {
  if (columnId.trim() === '') return undefined;
  return choices.find((choice) => materializedChoiceColumnId(choice) === columnId);
};

const operationSupport = (
  stage: ConstructionStageDescriptor,
  kind: 'FILTER' | 'DERIVE',
): { readonly supported: boolean; readonly reason?: string } => {
  const choice = stage.capabilities.find((candidate) => candidate.kind === kind);
  if (!choice) return { supported: false, reason: 'Loom did not return support for this operation at the selected stage.' };
  return choice.supported
    ? { supported: true }
    : { supported: false, reason: choice.reason ?? 'This operation is not supported at the selected stage.' };
};

const editorContextKey = (
  capabilities: ConstructionCapabilitiesResponse,
  editingStep: ConstructionStep | undefined,
): string => [
  capabilities.snapshotToken,
  capabilities.draftVersion,
  capabilities.draftDigest,
  capabilities.outputId,
  capabilities.stageId,
  editingStep?.id ?? 'new',
].join(':');

const constructionOutputsForStage = (
  stage: ConstructionStageDescriptor,
): ConstructionStep['outputs'] => stage.columns.map((column) => ({
  id: column.id,
  name: column.name,
  label: column.label,
  ...(column.type === undefined ? {} : { type: column.type }),
}));

const replaceOrAppendStep = (
  construction: Construction,
  editingStep: ConstructionStep | undefined,
  step: ConstructionStep,
): Construction | undefined => {
  if (editingStep && !construction.steps.some((candidate) => candidate.id === editingStep.id)) return undefined;
  return {
    ...construction,
    steps: editingStep
      ? construction.steps.map((candidate) => candidate.id === editingStep.id ? step : candidate)
      : [...construction.steps, step],
  };
};

const filterValueText = (value: FilterValue): string => {
  switch (value.kind) {
    case 'STRING': return value.string;
    case 'CODE': return value.code.code;
    case 'BOOLEAN': return String(value.boolean);
    case 'INTEGER': return String(value.integer);
    case 'DECIMAL': return String(value.decimal);
    case 'DATE': return value.date;
    case 'DATE_TIME': return value.dateTime;
    default: {
      const exhaustive: never = value;
      return exhaustive;
    }
  }
};

const filterValueInputText = (value: FilterValue): string => {
  if (value.kind !== 'DATE_TIME') return filterValueText(value);
  const instant = new Date(value.dateTime);
  const local = new Date(instant.getTime() - instant.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
};

const filterOperatorFromInput = (value: string): FilterOperator | undefined => {
  switch (value) {
    case 'EQUALS': return 'EQUALS';
    case 'NOT_EQUALS': return 'NOT_EQUALS';
    case 'IN': return 'IN';
    case 'EXISTS': return 'EXISTS';
    case 'MISSING': return 'MISSING';
    case 'CONTAINS_TEXT': return 'CONTAINS_TEXT';
    case 'GT': return 'GT';
    case 'GTE': return 'GTE';
    case 'LT': return 'LT';
    case 'LTE': return 'LTE';
    default: return undefined;
  }
};

const filterOperatorsForColumn = (column: ConstructionStageDescriptor['columns'][number]): ReadonlyArray<FilterOperator> => {
  const kind = filterValueKindFor(column.type);
  const operators: FilterOperator[] = ['EXISTS'];
  if (kind) operators.push('EQUALS', 'NOT_EQUALS', 'IN');
  if (kind === 'STRING') operators.push('CONTAINS_TEXT');
  if (kind === 'INTEGER' || kind === 'DECIMAL' || kind === 'DATE' || kind === 'DATE_TIME') {
    operators.push('GT', 'GTE', 'LT', 'LTE');
  }
  operators.push('MISSING');
  return operators;
};

const filterOperatorLabel = (operator: FilterOperator): string => {
  switch (operator) {
    case 'EXISTS': return 'is present';
    case 'EQUALS': return 'equals';
    case 'NOT_EQUALS': return 'does not equal';
    case 'IN': return 'is one of';
    case 'MISSING': return 'is missing';
    case 'CONTAINS_TEXT': return 'contains text';
    case 'GT': return 'is greater than';
    case 'GTE': return 'is greater than or equal to';
    case 'LT': return 'is less than';
    case 'LTE': return 'is less than or equal to';
    default: {
      const exhaustive: never = operator;
      return exhaustive;
    }
  }
};

const filterValueDraftsFor = (form: FilterForm): ReadonlyArray<FilterValueDraft> => {
  if (form.kind === 'comparison') return [form.value];
  if (form.kind === 'membership') return form.values;
  return [];
};

const filterFormForOperator = (
  operator: FilterOperator,
  columnId: string,
  drafts: ReadonlyArray<FilterValueDraft>,
  filterSources?: ReadonlyArray<FilterSourceSelection>,
): FilterForm => {
  const binding = filterSources?.length ? { filterSources } : {};
  if (operator === 'EXISTS') return { kind: 'exists', columnId, ...binding };
  if (operator === 'MISSING') return { kind: 'missing', columnId, ...binding };
  const blank: FilterValueDraft = { kind: 'blank', text: '' };
  const first = drafts[0] ?? blank;
  const values = drafts.length > 0 ? drafts : [blank];
  if (operator === 'IN') return { kind: 'membership', operator, columnId, values, ...binding };
  return { kind: 'comparison', operator, columnId, value: first, ...binding };
};

const filterValueFromDraft = (kind: FilterValueKind, draft: FilterValueDraft): FilterValue | undefined => {
  switch (draft.kind) {
    case 'saved': return draft.value;
    case 'blank': return undefined;
    case 'edited': return filterValueFor(kind, draft.text, true);
    default: {
      const exhaustive: never = draft;
      return exhaustive;
    }
  }
};

const derivedOperationFromInput = (value: string): DerivePayload['operation'] | undefined => {
  switch (value) {
    case 'ADD':
    case 'SUBTRACT':
    case 'MULTIPLY':
    case 'DIVIDE':
      return value;
    default:
      return undefined;
  }
};

const derivedLiteralKindFromInput = (value: string): DerivedLiteralKind | undefined => {
  switch (value) {
    case 'INTEGER': return 'INTEGER';
    case 'DECIMAL': return 'DECIMAL';
    default: return undefined;
  }
};

const missingInputPolicyFromInput = (value: string): DerivePayload['missingInputPolicy'] | undefined => {
  switch (value) {
    case 'PROPAGATE_NULL': return 'PROPAGATE_NULL';
    case 'ERROR': return 'ERROR';
    default: return undefined;
  }
};

const divisionByZeroPolicyFromInput = (value: string): NonNullable<DerivePayload['divisionByZeroPolicy']> | undefined => {
  switch (value) {
    case 'NULL': return 'NULL';
    case 'ERROR': return 'ERROR';
    default: return undefined;
  }
};

const filterValueFor = (
  kind: FilterValueKind,
  text: string,
  edited: boolean,
): FilterValue | undefined => {
  if (!edited) return undefined;
  switch (kind) {
    case 'STRING':
      return { kind, string: text };
    case 'CODE':
      return text.trim() ? { kind, code: { code: text.trim() } } : undefined;
    case 'BOOLEAN':
      return text === 'true' ? { kind, boolean: true } : text === 'false' ? { kind, boolean: false } : undefined;
    case 'INTEGER': {
      if (text.trim() === '') return undefined;
      const value = Number(text);
      return Number.isSafeInteger(value) ? { kind, integer: value } : undefined;
    }
    case 'DECIMAL': {
      if (text.trim() === '') return undefined;
      const value = Number(text);
      return Number.isFinite(value) ? { kind, decimal: value } : undefined;
    }
    case 'DATE':
      return /^\d{4}-\d{2}-\d{2}$/.test(text) ? { kind, date: text } : undefined;
    case 'DATE_TIME': {
      if (!text.trim()) return undefined;
      const instant = new Date(text);
      return Number.isFinite(instant.getTime()) ? { kind, dateTime: instant.toISOString() } : undefined;
    }
    default: {
      const exhaustive: never = kind;
      return exhaustive;
    }
  }
};

const filterFormFromStep = (
  step: ConstructionStep | undefined,
  stage: ConstructionStageDescriptor,
  selectedColumns: ReadonlyArray<string>,
  filterSourceChoices: ReadonlyArray<FilterSourceChoice> = [],
): FilterForm => {
  const savedSourceBinding = (columnId: string): ReadonlyArray<FilterSourceSelection> | undefined => {
    const choice = filterSourceChoiceForColumn(filterSourceChoices, columnId);
    const signedColumnId = choice ? materializedChoiceColumnId(choice) : undefined;
    return choice && signedColumnId ? [{ choiceId: choice.choiceId, columnId: signedColumnId }] : undefined;
  };
  const columnFor = (columnId: string): FilterStageColumn | undefined => {
    const column = stage.columns.find((candidate) => candidate.id === columnId);
    if (column) return column;
    const choice = filterSourceChoiceForColumn(filterSourceChoices, columnId);
    return choice ? filterSourceColumn(choice, columnId, stage) : undefined;
  };

  if (step) {
    if (step.operation.kind !== 'FILTER') {
      return { kind: 'unsupported', message: 'Only saved row filter steps can be edited here.' };
    }
    const filter = step.operation.filter;
    if (filter.operator === 'EXISTS' || filter.operator === 'MISSING') {
      if (!columnFor(filter.columnId)) {
        return { kind: 'unsupported', message: 'The saved condition column is no longer present in Loom’s predecessor stage.' };
      }
      const filterSources = savedSourceBinding(filter.columnId);
      return { kind: filter.operator === 'EXISTS' ? 'exists' : 'missing', columnId: filter.columnId, ...(filterSources ? { filterSources } : {}) };
    }
    const column = columnFor(filter.columnId);
    if (!column) {
      return { kind: 'unsupported', message: 'The saved condition column is no longer present in Loom’s predecessor stage.' };
    }
    if (!filterOperatorsForColumn(column).includes(filter.operator)) {
      return { kind: 'unsupported', message: 'This saved condition is incompatible with the predecessor column type.' };
    }
    const valueKind = filterValueKindFor(column.type);
    const values = filter.values ?? [];
    const validCount = filter.operator === 'IN' ? values.length > 0 : values.length === 1;
    if (!valueKind || !validCount || values.some((value) => value.kind !== valueKind)) {
      return { kind: 'unsupported', message: 'Loom did not return a matching typed value for this saved condition.' };
    }
    const drafts = values.map((value): FilterValueDraft => ({
      kind: 'saved', text: filterValueInputText(value), value,
    }));
    const filterSources = savedSourceBinding(filter.columnId);
    return filterFormForOperator(filter.operator, filter.columnId, drafts, filterSources);
  }

  const column = stage.columns.find((candidate) => selectedColumns.includes(candidate.id)) ?? stage.columns[0];
  if (!column) return { kind: 'missing', columnId: '' };
  return filterValueKindFor(column.type)
    ? { kind: 'comparison', operator: 'EQUALS', columnId: column.id, value: { kind: 'blank', text: '' } }
    : { kind: 'missing', columnId: column.id };
};

const buildFilterCandidate = (args: {
  readonly construction: Construction;
  readonly stage: ConstructionStageDescriptor;
  readonly filterSourceInput?: FilterSourceInput;
  readonly editingStep?: ConstructionStep;
  readonly stepId: string;
  readonly form: FilterForm;
}): CandidateIntent | undefined => {
  const { construction, stage, filterSourceInput, editingStep, stepId, form } = args;
  if (form.kind === 'unsupported') return undefined;
  const filterSources = liveFilterSources(form);
  const inputStage = filterInputStage(stage, filterSourceInput, filterSources);
  const column = inputStage.columns.find((candidate) => candidate.id === form.columnId);
  if (!column) return undefined;

  let filter: FilterPayload;
  if (form.kind === 'missing') {
    filter = { columnId: column.id, operator: 'MISSING' };
  } else if (form.kind === 'exists') {
    filter = { columnId: column.id, operator: 'EXISTS' };
  } else {
    const valueKind = filterValueKindFor(column.type);
    if (!valueKind) return undefined;
    const values: FilterValue[] = [];
    for (const draft of filterValueDraftsFor(form)) {
      const value = filterValueFromDraft(valueKind, draft);
      if (!value) return undefined;
      values.push(value);
    }
    if (values.length === 0 || (form.kind === 'comparison' && values.length !== 1)) return undefined;
    filter = { columnId: column.id, operator: form.operator, values };
  }

  const step = {
    id: editingStep?.id ?? stepId,
    inputs: [constructionInputForStage(inputStage)],
    operation: { kind: 'FILTER', filter },
    outputs: constructionOutputsForStage(inputStage),
  } satisfies ConstructionStep;
  const candidateConstruction = replaceOrAppendStep(construction, editingStep, step);
  if (!candidateConstruction) return undefined;
  return {
    candidateConstruction,
    changedStepId: step.id,
    ...(filterSources.length ? { filterSources: [...filterSources] } : {}),
  };
};

const calculationOutputName = (columns: ConstructionStageDescriptor['columns']): string => {
  const usedNames = new Set(columns.map((column) => column.name));
  let name = 'calculated_value';
  let suffix = 2;
  while (usedNames.has(name)) name = `calculated_value_${suffix++}`;
  return name;
};

const operandDraftFromWire = (operand: DerivedOperand): OperandDraft => {
  if (operand.kind === 'COLUMN') return { kind: 'column', columnId: operand.columnId };
  return {
    kind: 'literal',
    literalKind: operand.literal.kind,
    text: String(operand.literal.kind === 'INTEGER' ? operand.literal.integer : operand.literal.decimal),
    edited: true,
  };
};

const calculationFormFromStep = (
  step: ConstructionStep | undefined,
  stage: ConstructionStageDescriptor,
  deriveSourceChoices: ReadonlyArray<DeriveSourceChoice> = [],
): CalculationForm => {
  if (step?.operation.kind === 'DERIVE') {
    const derive = step.operation.derive;
    const output = step.outputs.find((column) => column.id === derive.outputColumnId);
    const bindings: DeriveSourceSelection[] = [];
    for (const operand of [derive.left, derive.right]) {
      if (operand.kind !== 'COLUMN') continue;
      const choice = deriveSourceChoiceForColumn(deriveSourceChoices, operand.columnId);
      const signedColumnId = choice ? materializedChoiceColumnId(choice) : undefined;
      if (choice && signedColumnId && !bindings.some((binding) => binding.choiceId === choice.choiceId)) {
        bindings.push({ choiceId: choice.choiceId, columnId: signedColumnId });
      }
    }
    return {
      expression: {
        kind: 'guided',
        draft: {
          operation: derive.operation,
          left: operandDraftFromWire(derive.left),
          right: operandDraftFromWire(derive.right),
        },
      },
      missingInputPolicy: derive.missingInputPolicy,
      divisionByZeroPolicy: derive.divisionByZeroPolicy ?? 'NULL',
      outputName: output?.name ?? calculationOutputName(stage.columns),
      outputLabel: output?.label ?? 'Calculated value',
      ...(bindings.length ? { deriveSources: bindings } : {}),
    };
  }
  const numericColumns = stage.columns.filter(isNumericColumn);
  const selectedNumeric = numericColumns[0];
  return {
    expression: {
      kind: 'guided',
      draft: {
        operation: 'ADD',
        left: selectedNumeric ? { kind: 'column', columnId: selectedNumeric.id } : { kind: 'unset' },
        right: { kind: 'unset' },
      },
    },
    missingInputPolicy: 'PROPAGATE_NULL',
    divisionByZeroPolicy: 'NULL',
    outputName: calculationOutputName(stage.columns),
    outputLabel: 'Calculated value',
  };
};

const operandForWire = (operand: OperandDraft): DerivedOperand | undefined => {
  if (operand.kind === 'unset') return undefined;
  if (operand.kind === 'column') return { kind: 'COLUMN', columnId: operand.columnId };
  if (!operand.edited || operand.text.trim() === '') return undefined;
  const value = Number(operand.text);
  if (!Number.isFinite(value)) return undefined;
  if (operand.literalKind === 'INTEGER') {
    return Number.isSafeInteger(value)
      ? { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: value } }
      : undefined;
  }
  return { kind: 'LITERAL', literal: { kind: 'DECIMAL', decimal: value } };
};

const expressionDraftFor = (state: ExpressionState): ArithmeticExpressionDraft => {
  switch (state.kind) {
    case 'guided':
      return state.draft;
    case 'formula-invalid':
      return state.lastDraft;
    case 'formula-valid':
      return {
        operation: state.expression.operation,
        left: operandDraftFromWire(state.expression.left),
        right: operandDraftFromWire(state.expression.right),
      };
    default: {
      const exhaustive: never = state;
      return exhaustive;
    }
  }
};

const completeExpressionFor = (state: ExpressionState): ArithmeticExpression | undefined => {
  switch (state.kind) {
    case 'guided': {
      const left = operandForWire(state.draft.left);
      const right = operandForWire(state.draft.right);
      return left && right ? { operation: state.draft.operation, left, right } : undefined;
    }
    case 'formula-valid':
      return state.expression;
    case 'formula-invalid':
      return undefined;
    default: {
      const exhaustive: never = state;
      return exhaustive;
    }
  }
};

const formulaSymbolFor = (operation: DerivedOperation): string => {
  switch (operation) {
    case 'ADD': return '+';
    case 'SUBTRACT': return '-';
    case 'MULTIPLY': return '*';
    case 'DIVIDE': return '/';
    default: {
      const exhaustive: never = operation;
      return exhaustive;
    }
  }
};

const formulaOperandText = (
  operand: OperandDraft,
  columns: ConstructionStageDescriptor['columns'],
): string => {
  if (operand.kind === 'unset') return '';
  if (operand.kind === 'column') return columns.find((column) => column.id === operand.columnId)?.name ?? '';
  return operand.edited ? operand.text : '';
};

const formulaTextFor = (
  expression: ArithmeticExpressionDraft,
  columns: ConstructionStageDescriptor['columns'],
): string => `${formulaOperandText(expression.left, columns)} ${formulaSymbolFor(expression.operation)} ${formulaOperandText(expression.right, columns)}`;

type FormulaParseResult =
  | { readonly kind: 'valid'; readonly expression: ArithmeticExpression }
  | { readonly kind: 'invalid'; readonly message: string };

const parseFormulaOperand = (
  text: string,
  columns: ConstructionStageDescriptor['columns'],
): DerivedOperand | undefined => {
  const numericLiteral = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/;
  if (numericLiteral.test(text)) {
    const value = Number(text);
    if (!Number.isFinite(value)) return undefined;
    if (/^[+-]?\d+$/.test(text)) {
      return Number.isSafeInteger(value)
        ? { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: value } }
        : undefined;
    }
    return { kind: 'LITERAL', literal: { kind: 'DECIMAL', decimal: value } };
  }

  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(text)) return undefined;
  const column = columns.find((candidate) => candidate.name === text && isNumericColumn(candidate));
  return column ? { kind: 'COLUMN', columnId: column.id } : undefined;
};

const parseFormula = (
  source: string,
  columns: ConstructionStageDescriptor['columns'],
): FormulaParseResult => {
  const operand = '(?:[A-Za-z_][A-Za-z0-9_]*|[+-]?(?:\\d+(?:\\.\\d*)?|\\.\\d+)(?:[eE][+-]?\\d+)?)';
  const match = new RegExp(`^\\s*(${operand})\\s*([+*/-])\\s*(${operand})\\s*$`).exec(source);
  if (!match) {
    return {
      kind: 'invalid',
      message: 'Enter two numeric operands with one +, -, *, or / operator. Parentheses, functions, and conditional expressions are not supported.',
    };
  }

  const left = parseFormulaOperand(match[1], columns);
  const right = parseFormulaOperand(match[3], columns);
  if (!left || !right) {
    return {
      kind: 'invalid',
      message: 'Use numeric literals or numeric column names returned by Loom for this stage.',
    };
  }
  const operation = derivedOperationFromInput({ '+': 'ADD', '-': 'SUBTRACT', '*': 'MULTIPLY', '/': 'DIVIDE' }[match[2]] ?? '');
  if (!operation) return { kind: 'invalid', message: 'Choose one supported arithmetic operator: +, -, *, or /.' };
  return { kind: 'valid', expression: { operation, left, right } };
};

const buildDeriveCandidate = (args: {
  readonly construction: Construction;
  readonly stage: ConstructionStageDescriptor;
  readonly deriveSourceInput?: DeriveSourceInput;
  readonly editingStep?: ConstructionStep;
  readonly stepId: string;
  readonly outputColumnId: string;
  readonly form: CalculationForm;
  readonly expression: ArithmeticExpression;
}): CandidateIntent | undefined => {
  const { construction, stage, deriveSourceInput, editingStep, stepId, outputColumnId, form, expression } = args;
  if (editingStep && editingStep.operation.kind !== 'DERIVE') return undefined;
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(form.outputName) || !form.outputLabel.trim()) return undefined;
  const deriveSources = liveDeriveSources(form);
  const inputStage = deriveSourceStage(stage, deriveSourceInput, deriveSources);
  const replacedOutputID = editingStep?.operation.kind === 'DERIVE'
    ? editingStep.operation.derive.outputColumnId
    : undefined;
  const nameConflict = inputStage.columns.some((column) =>
    column.name === form.outputName && column.id !== replacedOutputID,
  );
  if (nameConflict) return undefined;

  const numericColumnIDs = new Set(inputStage.columns.filter(isNumericColumn).map((column) => column.id));
  const { left, right } = expression;
  if (left.kind === 'COLUMN' && !numericColumnIDs.has(left.columnId)) return undefined;
  if (right.kind === 'COLUMN' && !numericColumnIDs.has(right.columnId)) return undefined;

  const outputID = replacedOutputID ?? outputColumnId;
  const derive: DerivePayload = expression.operation === 'DIVIDE'
    ? {
        constructionId: editingStep?.id ?? stepId,
        outputColumnId: outputID,
        operation: expression.operation,
        left,
        right,
        missingInputPolicy: form.missingInputPolicy,
        divisionByZeroPolicy: form.divisionByZeroPolicy,
      }
    : {
        constructionId: editingStep?.id ?? stepId,
        outputColumnId: outputID,
        operation: expression.operation,
        left,
        right,
        missingInputPolicy: form.missingInputPolicy,
      };
  const output: DerivedOutput = {
    id: outputID,
    name: form.outputName,
    label: form.outputLabel.trim(),
  };
  const step = {
    id: editingStep?.id ?? stepId,
    inputs: [constructionInputForStage(inputStage)],
    operation: { kind: 'DERIVE', derive },
    outputs: [...constructionOutputsForStage(inputStage), output],
  } satisfies ConstructionStep;
  const candidateConstruction = replaceOrAppendStep(construction, editingStep, step);
  if (!candidateConstruction) return undefined;
  return {
    candidateConstruction,
    changedStepId: step.id,
    ...(deriveSources.length ? { deriveSources: [...deriveSources] } : {}),
  };
};

const filterSteps = (construction: Construction): ReadonlyArray<ConstructionStep> =>
  construction.steps.filter((step) => step.operation.kind === 'FILTER');

const filterStepDescription = (
  step: ConstructionStep,
  stages: ReadonlyArray<ConstructionStageDescriptor>,
): string => {
  if (step.operation.kind !== 'FILTER') return 'Unsupported row condition';
  const outputStage = stages.find((stage) => stage.id === step.id);
  const inputStage = outputStage
    ? stages.find((stage) => stage.id === outputStage.inputStageId)
    : undefined;
  const filter = step.operation.filter;
  const column = inputStage?.columns.find((candidate) => candidate.id === filter.columnId)
    ?? step.outputs.find((candidate) => candidate.id === filter.columnId);
  const label = column?.label ?? column?.name ?? 'Unknown column';
  if (filter.operator === 'MISSING') return `${label} is missing`;
  const values = filter.values ?? [];
  switch (filter.operator) {
    case 'EQUALS':
      return values[0] ? `${label} equals ${filterValueText(values[0])}` : `${label} equals a value`;
    case 'NOT_EQUALS':
      return values[0] ? `${label} does not equal ${filterValueText(values[0])}` : `${label} does not equal a value`;
    case 'IN':
      return `${label} is one of ${values.map(filterValueText).join(', ') || 'the saved values'}`;
    case 'EXISTS':
      return `${label} is present`;
    case 'CONTAINS_TEXT':
      return `${label} contains ${values[0] ? filterValueText(values[0]) : 'the saved text'}`;
    case 'GT':
    case 'GTE':
    case 'LT':
    case 'LTE': {
      const operator = { GT: '>', GTE: '≥', LT: '<', LTE: '≤' }[filter.operator];
      return `${label} ${operator} ${values[0] ? filterValueText(values[0]) : 'the saved value'}`;
    }
    default: {
      const exhaustive: never = filter.operator;
      return `${label}: ${exhaustive}`;
    }
  }
};

const FilterValueInput = (props: {
  readonly kind: FilterValueKind;
  readonly ariaLabel: string;
  readonly value: string;
  readonly disabled: boolean;
  readonly onChange: (value: string) => void;
}) => {
  switch (props.kind) {
    case 'STRING':
    case 'CODE':
      return <input aria-label={props.ariaLabel} type="text" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5" />;
    case 'BOOLEAN':
      return (
        <select aria-label={props.ariaLabel} value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5">
          <option value="">Choose true or false</option>
          <option value="true">True</option>
          <option value="false">False</option>
        </select>
      );
    case 'INTEGER':
      return <input aria-label={props.ariaLabel} type="number" step="1" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5" />;
    case 'DECIMAL':
      return <input aria-label={props.ariaLabel} type="number" step="any" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5" />;
    case 'DATE':
      return <input aria-label={props.ariaLabel} type="date" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5" />;
    case 'DATE_TIME':
      return (
        <>
          <input aria-label={props.ariaLabel} type="datetime-local" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5" />
          <span className="text-xs text-slate-500">Saved as UTC.</span>
        </>
      );
    default: {
      const exhaustive: never = props.kind;
      return exhaustive;
    }
  }
};

// An ordinary PUBLIC column carries its field as authored authoringv2
// Column.Source.Field identity, compiled separately from construction.sourceProjections,
// so the projection list alone cannot see it. Capabilities sign that same
// identity into the scalar field choices: canonicalPath plus inputColumnId is
// the server's own "this live stage column already carries this root field"
// proof. Root-role scalar entries only, so a nested occurrence can never
// impersonate a root dataset field.
const publicFieldCarriersForStage = (
  capabilities: ConstructionCapabilitiesResponse,
): PublicFieldCarrier[] => {
  const carriers: PublicFieldCarrier[] = [];
  for (const choice of [...capabilities.groupFieldChoices ?? [], ...capabilities.groupRowValueChoices ?? []]) {
    if (choice.kind !== 'FIELD_SOURCE') continue;
    if (choice.valueKind !== 'SCALAR') continue;
    if (choice.sourceRole !== 'ROOT' || choice.boundarySelector !== undefined) continue;
    const columnId = choice.inputColumnId ?? '';
    if (columnId.trim() === '') continue;
    carriers.push({ columnId, fieldPath: choice.canonicalPath });
  }
  return carriers;
};

const KeepRowsEditor = (props: ConstructionOperationEditorProps) => {
  const { construction, capabilities, editingStep, selectedColumns = [], disabled } = props;
  const stage = capabilities.selectedStage;
  const sourceInput = capabilities.filterSourceInput;
  const sourceChoices = sourceInput?.choices ?? [];
  const publicCarriers = publicFieldCarriersForStage(capabilities);
  const [stepId, setStepId] = useState(() => createOpaqueId('filter'));
  const [form, setForm] = useState(() => filterFormFromStep(editingStep, stage, selectedColumns, sourceChoices));
  const contextKey = editorContextKey(capabilities, editingStep);
  useEffect(() => {
    setStepId(createOpaqueId('filter'));
    setForm(filterFormFromStep(editingStep, stage, selectedColumns, sourceChoices));
    props.onCandidateChange(undefined);
  }, [contextKey]);

  const support = operationSupport(stage, 'FILTER');
  const supportedSavedSteps = filterSteps(construction);
  const filterSources = liveFilterSources(form);
  const inputStage = filterInputStage(stage, sourceInput, filterSources);
  const boundBinding = filterSources[0];
  const boundChoice = boundBinding
    ? sourceChoices.find((choice) => choice.choiceId === boundBinding.choiceId)
    : undefined;
  const boundSourceVisible = Boolean(boundBinding && boundChoice
    && !stage.columns.some((column) => column.id === boundBinding.columnId));
  const sourceOptions = sourceInput?.supported
    ? sourceChoices.filter((choice) => choice.isPopulated
      && !filterSources.some((binding) => binding.choiceId === choice.choiceId)
      && existingColumnForFieldPath({
        sourceProjections: construction.sourceProjections,
        columns: stage.columns,
        fieldPath: choice.fieldPath,
        publicCarriers,
      }) === undefined)
    : [];
  const columnOptionsAvailable = stage.columns.length > 0 || boundSourceVisible || sourceOptions.length > 0;
  const sourceUnavailableReason = sourceInput && !sourceInput.supported
    ? [sourceInput.reasonCode, sourceInput.reason]
      .map((part) => part?.trim() ?? '')
      .filter((part) => part !== '')
      .join(' · ')
    : undefined;
  const valueKind = form.kind === 'comparison' || form.kind === 'membership'
    ? filterValueKindFor(inputStage.columns.find((column) => column.id === form.columnId)?.type)
    : undefined;
  const conditionColumn = form.kind === 'unsupported'
    ? undefined
    : inputStage.columns.find((column) => column.id === form.columnId);
  const conditionOperators = conditionColumn ? filterOperatorsForColumn(conditionColumn) : [];

  const updateForm = (next: FilterForm) => {
    const live = liveFilterSources(next);
    const normalized: FilterForm = next.kind === 'unsupported' || live.length === 0
      ? (next.kind === 'unsupported' ? next : { ...next, filterSources: undefined })
      : { ...next, filterSources: live };
    setForm(normalized);
    props.onCandidateChange(support.supported
      ? buildFilterCandidate({ construction, stage, filterSourceInput: sourceInput, editingStep, stepId, form: normalized })
      : undefined);
  };

  const selectColumn = (column: FilterStageColumn, nextFilterSources?: ReadonlyArray<FilterSourceSelection>) => {
    if (form.kind === 'unsupported') return;
    const currentOperator = form.kind === 'exists' ? 'EXISTS' : form.kind === 'missing' ? 'MISSING' : form.operator;
    const availableOperators = filterOperatorsForColumn(column);
    const nextOperator = availableOperators.includes(currentOperator)
      ? currentOperator
      : filterValueKindFor(column.type) ? 'EQUALS' : 'MISSING';
    updateForm(filterFormForOperator(nextOperator, column.id, [], nextFilterSources));
  };

  const chooseColumn = (selected: string) => {
    if (form.kind === 'unsupported') return;
    if (!selected.startsWith('source:')) {
      const column = inputStage.columns.find((candidate) => candidate.id === selected);
      if (!column) return;
      selectColumn(column, form.filterSources);
      return;
    }
    const choice = sourceChoices.find((candidate) => `source:${candidate.choiceId}` === selected);
    if (!choice) return;
    const existing = form.filterSources?.find((binding) => binding.choiceId === choice.choiceId);
    const columnId = materializedChoiceColumnId(choice) ?? existing?.columnId ?? createOpaqueId('filter-input');
    const nextFilterSources = [
      ...(form.filterSources ?? []).filter((binding) => binding.choiceId !== choice.choiceId),
      { choiceId: choice.choiceId, columnId },
    ];
    selectColumn(filterSourceColumn(choice, columnId, stage), nextFilterSources);
  };

  if (!support.supported) {
    return <p role="status" data-testid="construction-filter-unavailable" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{support.reason}</p>;
  }

  return (
    <section aria-label="Filter output rows by condition" data-testid="construction-filter-editor" className="grid gap-3">
      <header>
        <h3 className="font-semibold text-slate-900">Filter output rows</h3>
        <p className="mt-1 text-xs text-slate-600">Rows must match every condition. Contributor rules still choose which records supply their values.</p>
      </header>

      {supportedSavedSteps.length > 0 ? (
        <fieldset data-testid="construction-filter-condition-tree" className="rounded-lg border border-slate-200 p-3">
          <legend className="px-1 text-sm font-semibold text-slate-800">Saved conditions</legend>
          <ol className="grid gap-2">
            {supportedSavedSteps.map((step, index) => (
              <li key={step.id} className="flex items-center justify-between gap-3 rounded bg-slate-50 px-3 py-2 text-sm">
                <span><span className="mr-2 text-slate-500">{index + 1}.</span>{filterStepDescription(step, capabilities.stages)}</span>
                <button
                  type="button"
                  data-testid={`construction-filter-edit-${step.id}`}
                  disabled={disabled}
                  onClick={() => props.onEditStep(step.id)}
                  className="shrink-0 rounded px-2 py-1 font-medium text-blue-800 hover:bg-blue-100 disabled:cursor-not-allowed disabled:text-slate-400"
                >
                  Edit
                </button>
              </li>
            ))}
          </ol>
        </fieldset>
      ) : null}

      {form.kind === 'unsupported' ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{form.message}</p>
      ) : (
        <div className="grid gap-3 rounded-lg border border-slate-200 p-3">
          <p className="text-sm font-medium text-slate-800">{editingStep ? 'Edit this saved condition' : 'New condition'}</p>
          {!columnOptionsAvailable ? (
            <>
              <p role="status" className="text-sm text-slate-600">Loom returned no columns for this stage.</p>
              {sourceInput?.supported ? (
                <SourcePickerExplanation
                  emptiness={classifySourcePickerEmptiness(sourceChoices, sourcePickerBoundSet({
                    bindings: filterSources,
                    isOwn: () => false,
                    ownVisible: false,
                  }))}
                />
              ) : null}
            </>
          ) : (
            <>
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Column
                <select
                  aria-label="Column"
                  value={form.columnId}
                  disabled={disabled}
                  onChange={(event) => chooseColumn(event.currentTarget.value)}
                  className="rounded border border-slate-300 bg-white px-2 py-1.5"
                >
                  {stage.columns.map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type ?? 'unknown type'})</option>)}
                  {boundSourceVisible && boundBinding && boundChoice
                    ? <option value={boundBinding.columnId} title={boundChoice.fieldPath}>{filterSourceLabel(boundChoice)} ({boundChoice.logicalType})</option>
                    : null}
                  {sourceOptions.length > 0 ? <optgroup label="Dataset fields (not in output)">
                    {sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{filterSourceLabel(choice)} ({choice.logicalType})</option>)}
                  </optgroup> : null}
                </select>
              </label>

              {sourceInput && !sourceInput.supported ? (
                <p role="status" data-testid="construction-filter-source-unavailable" className="text-sm text-amber-900">
                  {sourceUnavailableReason && sourceUnavailableReason !== ''
                    ? sourceUnavailableReason
                    : 'Loom did not report why filtering on dataset fields is unavailable at this stage.'}
                </p>
              ) : null}

              {sourceInput?.supported && sourceOptions.length === 0 ? (
                <SourcePickerExplanation
                  emptiness={classifySourcePickerEmptiness(sourceChoices, sourcePickerBoundSet({
                    bindings: filterSources,
                    isOwn: (binding) => binding.choiceId === filterSources[filterSources.length - 1]?.choiceId,
                    ownVisible: stage.columns.some((column) => column.id === form.columnId) || boundSourceVisible,
                  }))}
                />
              ) : null}

              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Condition
                <select
                  aria-label="Condition"
                  value={form.kind === 'missing' ? 'MISSING' : form.kind === 'exists' ? 'EXISTS' : form.operator}
                  disabled={disabled}
                  onChange={(event) => {
                    const column = inputStage.columns.find((candidate) => candidate.id === form.columnId);
                    if (!column) return;
                    const operator = filterOperatorFromInput(event.currentTarget.value);
                    if (!operator || !filterOperatorsForColumn(column).includes(operator)) return;
                    updateForm(filterFormForOperator(operator, column.id, filterValueDraftsFor(form), liveFilterSources(form)));
                  }}
                  className="rounded border border-slate-300 bg-white px-2 py-1.5"
                >
                  {conditionOperators.map((operator) => (
                    <option key={operator} value={operator}>{filterOperatorLabel(operator)}</option>
                  ))}
                </select>
              </label>

              {form.kind === 'comparison' && valueKind ? (
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  Value
                  <FilterValueInput
                    kind={valueKind}
                    ariaLabel="Value"
                    value={form.value.text}
                    disabled={disabled}
                    onChange={(text) => updateForm({ ...form, value: { kind: 'edited', text } })}
                  />
                </label>
              ) : null}
              {form.kind === 'comparison' && !valueKind ? (
                <p role="status" className="text-sm text-amber-900">Value comparisons are unavailable for this column type. Choose “is present” or “is missing”.</p>
              ) : null}

              {form.kind === 'membership' && valueKind ? (
                <div className="grid gap-2">
                  {form.values.map((value, index) => (
                    <div key={index} className="flex items-end gap-2">
                      <label className="grid flex-1 gap-1 text-sm font-medium text-slate-700">
                        Value {index + 1}
                        <FilterValueInput
                          kind={valueKind}
                          ariaLabel={`Value ${index + 1}`}
                          value={value.text}
                          disabled={disabled}
                          onChange={(text) => updateForm({
                            ...form,
                            values: form.values.map((candidate, candidateIndex) =>
                              candidateIndex === index ? { kind: 'edited', text } : candidate,
                            ),
                          })}
                        />
                      </label>
                      {form.values.length > 1 ? (
                        <button
                          type="button"
                          aria-label={`Remove value ${index + 1}`}
                          disabled={disabled}
                          onClick={() => updateForm({ ...form, values: form.values.filter((_, candidateIndex) => candidateIndex !== index) })}
                          className="rounded border border-slate-300 px-2 py-1.5 text-sm"
                        >
                          Remove
                        </button>
                      ) : null}
                    </div>
                  ))}
                  <button
                    type="button"
                    aria-label="Add another value"
                    disabled={disabled}
                    onClick={() => updateForm({ ...form, values: [...form.values, { kind: 'blank', text: '' }] })}
                    className="w-fit rounded border border-slate-300 px-2 py-1.5 text-sm"
                  >
                    Add another value
                  </button>
                </div>
              ) : null}
              {form.kind === 'membership' && !valueKind ? (
                <p role="status" className="text-sm text-amber-900">A value list is unavailable for this column type. Choose “is present” or “is missing”.</p>
              ) : null}
            </>
          )}
        </div>
      )}
    </section>
  );
};

const FormulaOperand = (props: {
  readonly label: string;
  readonly slot: 'left' | 'right';
  readonly value: OperandDraft;
  readonly columns: ConstructionStageDescriptor['columns'];
  readonly disabled: boolean;
  readonly sourceInput?: DeriveSourceInput;
  readonly boundSources?: ReadonlyArray<DeriveSourceSelection>;
  readonly explain?: SourcePickerEmptiness;
  readonly sourceProjections?: Construction['sourceProjections'];
  readonly onChange: (value: OperandDraft) => void;
  readonly onSourceSelected: (choice: DeriveSourceChoice, columnId: string) => void;
}) => {
  const firstNumeric = props.columns.find(isNumericColumn);
  const boundChoiceIds = new Set((props.boundSources ?? []).map((binding) => binding.choiceId));
  const sourceChoices = props.sourceInput?.choices ?? [];
  const sourceOptions = props.sourceInput?.supported
    ? sourceChoices.filter((choice) => choice.isPopulated && !boundChoiceIds.has(choice.choiceId)
      && existingColumnForFieldPath({
        sourceProjections: props.sourceProjections,
        columns: props.columns.filter(isNumericColumn),
        fieldPath: choice.fieldPath,
      }) === undefined)
    : [];
  const sourceUnavailableReason = props.sourceInput && !props.sourceInput.supported
    ? [props.sourceInput.reasonCode, props.sourceInput.reason]
      .map((part) => part?.trim() ?? '')
      .filter((part) => part !== '')
      .join(' · ')
    : undefined;
  return (
    <fieldset className="grid gap-2 rounded-lg border border-slate-200 p-3">
      <legend className="px-1 text-sm font-semibold text-slate-800">{props.label}</legend>
      <label className="grid gap-1 text-sm font-medium text-slate-700">
        Use
        <select
          aria-label={`${props.label} kind`}
          value={props.value.kind}
          disabled={props.disabled}
          onChange={(event) => {
            switch (event.currentTarget.value) {
              case 'column':
                if (firstNumeric) {
                  props.onChange({ kind: 'column', columnId: firstNumeric.id });
                } else {
                  const choice = sourceOptions[0];
                  if (!choice) {
                    props.onChange({ kind: 'unset' });
                    break;
                  }
                  props.onSourceSelected(choice, materializedChoiceColumnId(choice) ?? createOpaqueId('derive-input'));
                }
                break;
              case 'literal':
                props.onChange({ kind: 'literal', literalKind: 'DECIMAL', text: '', edited: false });
                break;
              default:
                props.onChange({ kind: 'unset' });
            }
          }}
          className="rounded border border-slate-300 bg-white px-2 py-1.5"
        >
          <option value="unset">Choose a value</option>
          <option value="column" disabled={!firstNumeric && sourceOptions.length === 0}>Numeric column</option>
          <option value="literal">Number</option>
        </select>
      </label>

      {props.value.kind === 'column' ? (
        <label className="grid gap-1 text-sm font-medium text-slate-700">
          Column
          <select
            aria-label={`${props.label} column`}
            value={props.value.columnId}
            data-testid={`construction-calculate-${props.slot}-column`}
            disabled={props.disabled}
            onChange={(event) => {
              const selected = event.currentTarget.value;
              if (!selected.startsWith('source:')) {
                props.onChange({ kind: 'column', columnId: selected });
                return;
              }
              const choice = sourceChoices.find((candidate) => `source:${candidate.choiceId}` === selected);
              if (!choice) return;
              const existing = props.boundSources?.find((binding) => binding.choiceId === choice.choiceId);
              const columnId = materializedChoiceColumnId(choice) ?? existing?.columnId ?? createOpaqueId('derive-input');
              props.onSourceSelected(choice, columnId);
            }}
            className="rounded border border-slate-300 bg-white px-2 py-1.5"
          >
            {props.columns.filter(isNumericColumn).map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type})</option>)}
            {sourceOptions.length > 0 ? <optgroup label="Dataset fields (not in output)">
              {sourceOptions.map((choice) => <option key={choice.choiceId} data-testid={`construction-calculate-${props.slot}-source-option`} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{filterSourceLabel(choice)} ({choice.logicalType})</option>)}
            </optgroup> : null}
          </select>
        </label>
      ) : null}

      {props.sourceInput?.supported && props.explain ? (
        <SourcePickerExplanation emptiness={props.explain} />
      ) : null}

      {sourceUnavailableReason && sourceUnavailableReason !== '' ? (
        <p role="status" data-testid={`construction-calculate-${props.slot}-source-unavailable`} className="text-xs text-amber-900">{sourceUnavailableReason}</p>
      ) : null}

      {props.value.kind === 'literal' ? (
        <>
          <label className="grid gap-1 text-sm font-medium text-slate-700">
            Number type
            <select
              aria-label={`${props.label} number type`}
              value={props.value.literalKind}
              disabled={props.disabled}
              onChange={(event) => {
                const literalKind = derivedLiteralKindFromInput(event.currentTarget.value);
                if (!literalKind) return;
                props.onChange(props.value.kind === 'literal'
                  ? { ...props.value, literalKind }
                  : { kind: 'literal', literalKind, text: '', edited: false });
              }}
              className="rounded border border-slate-300 bg-white px-2 py-1.5"
            >
              <option value="INTEGER">Whole number</option>
              <option value="DECIMAL">Decimal</option>
            </select>
          </label>
          <label className="grid gap-1 text-sm font-medium text-slate-700">
            Number
            <input
              aria-label={`${props.label} number`}
              type="number"
              step={props.value.literalKind === 'INTEGER' ? '1' : 'any'}
              value={props.value.text}
              disabled={props.disabled}
              onChange={(event) => props.onChange({
                kind: 'literal',
                literalKind: props.value.kind === 'literal' ? props.value.literalKind : 'DECIMAL',
                text: event.currentTarget.value,
                edited: true,
              })}
              className="rounded border border-slate-300 px-2 py-1.5"
            />
          </label>
        </>
      ) : null}
    </fieldset>
  );
};

const CalculationEditor = (props: ConstructionOperationEditorProps) => {
  const { construction, capabilities, editingStep, selectedColumns = [], disabled } = props;
  const stage = capabilities.selectedStage;
  const deriveSourceInput = capabilities.deriveSourceInput;
  const deriveSourceChoices = deriveSourceInput?.choices ?? [];
  const [stepId, setStepId] = useState(() => createOpaqueId('derive'));
  const [outputColumnId, setOutputColumnId] = useState(() => createOpaqueId('column'));
  const [form, setForm] = useState(() => calculationFormFromStep(editingStep, stage, deriveSourceChoices));
  const contextKey = editorContextKey(capabilities, editingStep);
  useEffect(() => {
    const numericSelected = stage.columns.find((column) => selectedColumns.includes(column.id) && isNumericColumn(column));
    const initial = calculationFormFromStep(editingStep, stage, deriveSourceChoices);
    setForm(numericSelected && !editingStep
      ? {
          ...initial,
          expression: {
            kind: 'guided',
            draft: { ...expressionDraftFor(initial.expression), left: { kind: 'column', columnId: numericSelected.id } },
          },
        }
      : initial);
    setStepId(createOpaqueId('derive'));
    setOutputColumnId(createOpaqueId('column'));
    props.onCandidateChange(undefined);
  }, [contextKey]);

  const support = operationSupport(stage, 'DERIVE');
  const expressionStep = editingStep?.operation.kind === 'DERIVE' ? editingStep : undefined;
  const savedDerive = expressionStep?.operation.kind === 'DERIVE' ? expressionStep.operation.derive : undefined;
  const outputColumnID = savedDerive?.outputColumnId ?? outputColumnId;
  const output = expressionStep?.outputs.find((column) => column.id === outputColumnID);

  const updateForm = (next: CalculationForm) => {
    const live = liveDeriveSources(next);
    const normalized: CalculationForm = { ...next, deriveSources: live.length ? live : undefined };
    setForm(normalized);
    const expression = completeExpressionFor(normalized.expression);
    props.onCandidateChange(support.supported && expression
      ? buildDeriveCandidate({ construction, stage, deriveSourceInput, editingStep, stepId, outputColumnId, form: normalized, expression })
      : undefined);
  };

  const expressionDraft = expressionDraftFor(form.expression);
  const inputStage = deriveSourceStage(stage, deriveSourceInput, liveDeriveSources(form));

  // One explanation per operand pair: the first operand that renders a
  // column-kind source picker and classifies as empty hosts it. A literal
  // operand has no source section, so a zero-field bound operand still gets
  // its explanation instead of silently rendering an empty picker.
  const pickerEmptinessHost = (() => {
    if (!deriveSourceInput?.supported) return undefined;
    const choices = deriveSourceInput.choices ?? [];
    const bindings = form.deriveSources ?? [];
    const operands = [
      { slot: 'left' as const, draft: expressionDraft.left },
      { slot: 'right' as const, draft: expressionDraft.right },
    ];
    for (const operand of operands) {
      if (operand.draft.kind !== 'column') continue;
      const ownColumnId = operand.draft.columnId;
      const ownBinding = bindings.find((binding) => binding.columnId === ownColumnId);
      const ownChoice = ownBinding !== undefined
        ? choices.find((choice) => choice.choiceId === ownBinding.choiceId)
        : choices.find((choice) => choice.columnId === ownColumnId);
      const list = [...bindings] as Array<{ readonly choiceId: string }>;
      if (ownChoice && !list.some((binding) => binding.choiceId === ownChoice.choiceId)) {
        list.push({ choiceId: ownChoice.choiceId });
      }
      const emptiness = classifySourcePickerEmptiness(choices, sourcePickerBoundSet({
        bindings: list,
        isOwn: (binding) => choices.some((choice) => choice.choiceId === binding.choiceId && choice.columnId === ownColumnId),
        ownVisible: ownBinding !== undefined
          // Provenance: only deriveSourceStage's virtual materialization (never
          // present on the raw stage) is own's own column; a foreign server
          // column reusing the id must not silence the explanation.
          && !stage.columns.some((column) => column.id === ownColumnId)
          && inputStage.columns.some((column) => column.id === ownColumnId && isNumericColumn(column)),
      }));
      if (emptiness.emptyClass !== 'available') {
        return { slot: operand.slot, emptiness };
      }
    }
    return undefined;
  })();
  const formulaText = form.expression.kind === 'formula-invalid' || form.expression.kind === 'formula-valid'
    ? form.expression.source
    : formulaTextFor(expressionDraft, inputStage.columns);

  const updateOperandDraft = (slot: 'left' | 'right', draft: OperandDraft) => {
    updateForm({
      ...form,
      expression: { kind: 'guided', draft: { ...expressionDraft, [slot]: draft } },
    });
  };

  const chooseSourceOperand = (slot: 'left' | 'right', choice: DeriveSourceChoice, columnId: string) => {
    const previousId = expressionDraft[slot].kind === 'column' ? expressionDraft[slot].columnId : undefined;
    const otherSlot = slot === 'left' ? 'right' : 'left';
    const otherColumnId = expressionDraft[otherSlot].kind === 'column' ? expressionDraft[otherSlot].columnId : undefined;
    // The kind shortcut can make both operands share one materialized column;
    // prune only bindings this operand actually releases, never the other
    // operand's live binding (liveDeriveSources prunes what truly goes unused).
    const kept = (form.deriveSources ?? []).filter((binding) =>
      ((binding.columnId !== previousId && binding.columnId !== columnId) || binding.columnId === otherColumnId) || binding.columnId === otherColumnId);
    updateForm({
      ...form,
      expression: { kind: 'guided', draft: { ...expressionDraft, [slot]: { kind: 'column', columnId } } },
      deriveSources: [...kept, { choiceId: choice.choiceId, columnId }],
    });
  };

  const switchExpressionView = (view: 'guided' | 'formula') => {
    if (view === 'guided') {
      updateForm({ ...form, expression: { kind: 'guided', draft: expressionDraft } });
      return;
    }
    const source = formulaTextFor(expressionDraft, inputStage.columns);
    const parsed = parseFormula(source, inputStage.columns);
    updateForm(parsed.kind === 'valid'
      ? { ...form, expression: { kind: 'formula-valid', expression: parsed.expression, source } }
      : { ...form, expression: { kind: 'formula-invalid', lastDraft: expressionDraft, source, error: parsed.message } });
  };

  const changeFormula = (source: string) => {
    const parsed = parseFormula(source, inputStage.columns);
    updateForm(parsed.kind === 'valid'
      ? { ...form, expression: { kind: 'formula-valid', expression: parsed.expression, source } }
      : {
          ...form,
          expression: {
            kind: 'formula-invalid',
            lastDraft: expressionDraft,
            source,
            error: parsed.message,
          },
        });
  };

  if (!support.supported) {
    return <p role="status" data-testid="construction-calculate-unavailable" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{support.reason}</p>;
  }
  if (editingStep && editingStep.operation.kind !== 'DERIVE') {
    return <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Only a saved calculation can be edited in this panel.</p>;
  }
  if (expressionStep && !output) {
    return <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Loom did not return the saved calculation’s output column in its step schema, so this calculation cannot be edited safely.</p>;
  }

  const isFormulaView = form.expression.kind !== 'guided';

  return (
    <section aria-label="Calculate a value" data-testid="construction-calculate-editor" className="grid gap-4">
      <header>
        <h3 className="font-semibold text-slate-900">Calculate a value</h3>
        <p className="mt-1 text-sm text-slate-600">Build one arithmetic expression from numeric columns and numbers. Loom checks this candidate and returns the preview that determines whether it can be applied.</p>
      </header>

      {editingStep && output ? (
        <p role="status" data-testid="construction-calculate-replacement" className="rounded bg-blue-50 px-3 py-2 text-sm text-blue-950">
          Editing saved calculation “{output.label}”. Applying this proposal replaces that calculation while keeping its column identity.
        </p>
      ) : (
        <p role="status" data-testid="construction-calculate-new-output" className="rounded bg-slate-50 px-3 py-2 text-sm text-slate-700">
          This creates a new column by default.
        </p>
      )}

      <div role="group" aria-label="Expression editor" className="flex gap-2">
        <button
          type="button"
          aria-pressed={!isFormulaView}
          disabled={disabled}
          onClick={() => switchExpressionView('guided')}
          className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium aria-pressed:bg-blue-50 aria-pressed:text-blue-900"
        >
          Guided controls
        </button>
        <button
          type="button"
          aria-pressed={isFormulaView}
          disabled={disabled}
          onClick={() => switchExpressionView('formula')}
          className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium aria-pressed:bg-blue-50 aria-pressed:text-blue-900"
        >
          Formula editor
        </button>
      </div>

      {isFormulaView ? (
        <div data-testid="construction-calculate-formula-view" className="grid gap-2 rounded-lg border border-slate-200 p-3">
          <p className="text-sm text-slate-600">Use one binary operation with numeric column names or numeric literals. Functions, nested expressions, parentheses, and conditional expressions are not supported.</p>
          <label className="grid gap-1 text-sm font-medium text-slate-700">
            Formula
            <textarea
              aria-label="Formula"
              value={formulaText}
              disabled={disabled}
              rows={2}
              onChange={(event) => changeFormula(event.currentTarget.value)}
              className="rounded border border-slate-300 px-2 py-1.5 font-mono"
              placeholder="weight * 2"
            />
          </label>
          {form.expression.kind === 'formula-invalid' ? (
            <p role="alert" data-testid="construction-calculate-formula-error" className="text-sm text-red-800">{form.expression.error}</p>
          ) : null}
        </div>
      ) : (
        <>
          <label className="grid gap-1 text-sm font-medium text-slate-700">
            Operation
            <select
              aria-label="Operation"
              value={expressionDraft.operation}
              disabled={disabled}
              onChange={(event) => {
                const operation = derivedOperationFromInput(event.currentTarget.value);
                if (operation) updateForm({
                  ...form,
                  expression: { kind: 'guided', draft: { ...expressionDraft, operation } },
                });
              }}
              className="rounded border border-slate-300 bg-white px-2 py-1.5"
            >
              <option value="ADD">Add</option>
              <option value="SUBTRACT">Subtract</option>
              <option value="MULTIPLY">Multiply</option>
              <option value="DIVIDE">Divide</option>
            </select>
          </label>

          <div className="grid gap-3 md:grid-cols-2">
            <FormulaOperand
              label="First value"
              slot="left"
              value={expressionDraft.left}
              columns={inputStage.columns}
              disabled={disabled}
              sourceInput={deriveSourceInput}
              boundSources={form.deriveSources}
              sourceProjections={construction.sourceProjections}
              explain={pickerEmptinessHost?.slot === 'left' ? pickerEmptinessHost.emptiness : undefined}
              onChange={(left) => updateOperandDraft('left', left)}
              onSourceSelected={(choice, columnId) => chooseSourceOperand('left', choice, columnId)}
            />
            <FormulaOperand
              label="Second value"
              slot="right"
              value={expressionDraft.right}
              columns={inputStage.columns}
              disabled={disabled}
              sourceInput={deriveSourceInput}
              boundSources={form.deriveSources}
              sourceProjections={construction.sourceProjections}
              explain={pickerEmptinessHost?.slot === 'right' ? pickerEmptinessHost.emptiness : undefined}
              onChange={(right) => updateOperandDraft('right', right)}
              onSourceSelected={(choice, columnId) => chooseSourceOperand('right', choice, columnId)}
            />
          </div>
        </>
      )}

      <div className="grid gap-3 sm:grid-cols-2">
        <label className="grid gap-1 text-sm font-medium text-slate-700">
          Missing input
          <select
            aria-label="Missing input policy"
            value={form.missingInputPolicy}
            disabled={disabled}
            onChange={(event) => {
              const missingInputPolicy = missingInputPolicyFromInput(event.currentTarget.value);
              if (missingInputPolicy) updateForm({ ...form, missingInputPolicy });
            }}
            className="rounded border border-slate-300 bg-white px-2 py-1.5"
          >
            <option value="PROPAGATE_NULL">Leave the result missing</option>
            <option value="ERROR">Treat missing input as an error</option>
          </select>
        </label>
        {expressionDraft.operation === 'DIVIDE' ? (
          <label className="grid gap-1 text-sm font-medium text-slate-700">
            Divide by zero
            <select
              aria-label="Division by zero policy"
              value={form.divisionByZeroPolicy}
              disabled={disabled}
              onChange={(event) => {
                const divisionByZeroPolicy = divisionByZeroPolicyFromInput(event.currentTarget.value);
                if (divisionByZeroPolicy) updateForm({ ...form, divisionByZeroPolicy });
              }}
              className="rounded border border-slate-300 bg-white px-2 py-1.5"
            >
              <option value="NULL">Leave the result missing</option>
              <option value="ERROR">Treat it as an error</option>
            </select>
          </label>
        ) : null}
      </div>

      <div className="grid gap-3 rounded-lg border border-slate-200 p-3 sm:grid-cols-2">
        <label className="grid gap-1 text-sm font-medium text-slate-700">
          {editingStep ? 'Column name' : 'New column name'}
          <input aria-label="Output column name" value={form.outputName} disabled={disabled} onChange={(event) => updateForm({ ...form, outputName: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
        </label>
        <label className="grid gap-1 text-sm font-medium text-slate-700">
          Column label
          <input aria-label="Output column label" value={form.outputLabel} disabled={disabled} onChange={(event) => updateForm({ ...form, outputLabel: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
        </label>
      </div>
      {!isPhysicalColumnName(form.outputName) ? (
        <p role="status" className="text-sm text-amber-900">Use a name that starts with a letter or underscore and contains only letters, numbers, and underscores.</p>
      ) : null}
      {output ? <p className="text-xs text-slate-500">The saved column keeps its stable ID: {output.id}</p> : null}
    </section>
  );
};

const isPhysicalColumnName = (value: string): boolean => /^[A-Za-z_][A-Za-z0-9_]*$/.test(value);

export const ConstructionOperationEditor = (props: ConstructionOperationEditorProps) => {
  const id = useId();
  return props.family === 'KEEP_ROWS'
    ? <KeepRowsEditor key={`${id}:filter:${editorContextKey(props.capabilities, props.editingStep)}`} {...props} />
    : <CalculationEditor key={`${id}:derive:${editorContextKey(props.capabilities, props.editingStep)}`} {...props} />;
};
