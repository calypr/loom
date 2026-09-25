import React, { useEffect, useId, useState } from 'react';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionOperation,
  ConstructionProposalRequest,
  ConstructionStageDescriptor,
  ConstructionStep,
} from '../../../types';

type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds'
>;

type FilterOperation = Extract<ConstructionOperation, { readonly kind: 'FILTER' }>;
type FilterPayload = FilterOperation['filter'];
type FilterValue = NonNullable<FilterPayload['values']>[number];
type FilterValueKind = FilterValue['kind'];
type FilterOperator = Extract<FilterPayload['operator'], 'EQUALS' | 'MISSING'>;
type DeriveOperation = Extract<ConstructionOperation, { readonly kind: 'DERIVE' }>;
type DerivePayload = DeriveOperation['derive'];
type DerivedOperand = DerivePayload['left'];
type DerivedLiteral = Extract<DerivedOperand, { readonly kind: 'LITERAL' }>['literal'];
type DerivedLiteralKind = DerivedLiteral['kind'];
type DerivedOutput = ConstructionStep['outputs'][number];
type DerivedOperation = DerivePayload['operation'];

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
  | { readonly kind: 'missing'; readonly columnId: string }
  | {
      readonly kind: 'equals';
      readonly columnId: string;
      readonly valueText: string;
      readonly valueEdited: boolean;
    }
  | { readonly kind: 'unsupported'; readonly message: string };

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

const stepInputFor = (stage: ConstructionStageDescriptor): ConstructionStep['inputs'][number] =>
  !stage.operation
    ? { kind: 'SOURCE_PROJECTION' }
    : { kind: 'STEP_OUTPUT', stepId: stage.id };

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
    case 'MISSING': return 'MISSING';
    default: return undefined;
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
): FilterForm => {
  if (step) {
    if (step.operation.kind !== 'FILTER') {
      return { kind: 'unsupported', message: 'Only saved row filter steps can be edited here.' };
    }
    const filter = step.operation.filter;
    if (filter.operator === 'MISSING') {
      return stage.columns.some((candidate) => candidate.id === filter.columnId)
        ? { kind: 'missing', columnId: filter.columnId }
        : { kind: 'unsupported', message: 'The saved condition column is no longer present in Loom’s predecessor stage.' };
    }
    if (filter.operator !== 'EQUALS') {
      return { kind: 'unsupported', message: 'This saved filter uses an operator that this editor does not change yet.' };
    }
    const column = stage.columns.find((candidate) => candidate.id === filter.columnId);
    const value = filter.values?.[0];
    if (!column || !value || filterValueKindFor(column.type) !== value.kind) {
      return { kind: 'unsupported', message: 'Loom did not return a matching typed value for this saved condition.' };
    }
    return {
      kind: 'equals',
      columnId: filter.columnId,
      valueText: filterValueInputText(value),
      valueEdited: true,
    };
  }

  const column = stage.columns.find((candidate) => selectedColumns.includes(candidate.id)) ?? stage.columns[0];
  if (!column) return { kind: 'missing', columnId: '' };
  return filterValueKindFor(column.type)
    ? { kind: 'equals', columnId: column.id, valueText: '', valueEdited: false }
    : { kind: 'missing', columnId: column.id };
};

const buildFilterCandidate = (args: {
  readonly construction: Construction;
  readonly stage: ConstructionStageDescriptor;
  readonly editingStep?: ConstructionStep;
  readonly stepId: string;
  readonly form: FilterForm;
}): CandidateIntent | undefined => {
  const { construction, stage, editingStep, stepId, form } = args;
  if (form.kind === 'unsupported') return undefined;
  const column = stage.columns.find((candidate) => candidate.id === form.columnId);
  if (!column) return undefined;

  let filter: FilterPayload;
  if (form.kind === 'missing') {
    filter = { columnId: column.id, operator: 'MISSING' };
  } else {
    const valueKind = filterValueKindFor(column.type);
    if (!valueKind) return undefined;
    const value = filterValueFor(valueKind, form.valueText, form.valueEdited);
    if (!value) return undefined;
    filter = { columnId: column.id, operator: 'EQUALS', values: [value] };
  }

  const step = {
    id: editingStep?.id ?? stepId,
    inputs: [stepInputFor(stage)],
    operation: { kind: 'FILTER', filter },
    outputs: constructionOutputsForStage(stage),
  } satisfies ConstructionStep;
  const candidateConstruction = replaceOrAppendStep(construction, editingStep, step);
  if (!candidateConstruction) return undefined;
  return {
    candidateConstruction,
    changedStepId: step.id,
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
): CalculationForm => {
  if (step?.operation.kind === 'DERIVE') {
    const derive = step.operation.derive;
    const output = step.outputs.find((column) => column.id === derive.outputColumnId);
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
  readonly editingStep?: ConstructionStep;
  readonly stepId: string;
  readonly outputColumnId: string;
  readonly form: CalculationForm;
  readonly expression: ArithmeticExpression;
}): CandidateIntent | undefined => {
  const { construction, stage, editingStep, stepId, outputColumnId, form, expression } = args;
  if (editingStep && editingStep.operation.kind !== 'DERIVE') return undefined;
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(form.outputName) || !form.outputLabel.trim()) return undefined;
  const replacedOutputID = editingStep?.operation.kind === 'DERIVE'
    ? editingStep.operation.derive.outputColumnId
    : undefined;
  const nameConflict = stage.columns.some((column) =>
    column.name === form.outputName && column.id !== replacedOutputID,
  );
  if (nameConflict) return undefined;

  const numericColumnIDs = new Set(stage.columns.filter(isNumericColumn).map((column) => column.id));
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
    inputs: [stepInputFor(stage)],
    operation: { kind: 'DERIVE', derive },
    outputs: [...constructionOutputsForStage(stage), output],
  } satisfies ConstructionStep;
  const candidateConstruction = replaceOrAppendStep(construction, editingStep, step);
  if (!candidateConstruction) return undefined;
  return {
    candidateConstruction,
    changedStepId: step.id,
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

const filterStepEditorSupported = (step: ConstructionStep): boolean =>
  step.operation.kind === 'FILTER' &&
  (step.operation.filter.operator === 'EQUALS' || step.operation.filter.operator === 'MISSING');

const FilterValueInput = (props: {
  readonly kind: FilterValueKind;
  readonly value: string;
  readonly disabled: boolean;
  readonly onChange: (value: string) => void;
}) => {
  switch (props.kind) {
    case 'STRING':
    case 'CODE':
      return <input aria-label="Value" type="text" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5" />;
    case 'BOOLEAN':
      return (
        <select aria-label="Value" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5">
          <option value="">Choose true or false</option>
          <option value="true">True</option>
          <option value="false">False</option>
        </select>
      );
    case 'INTEGER':
      return <input aria-label="Value" type="number" step="1" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5" />;
    case 'DECIMAL':
      return <input aria-label="Value" type="number" step="any" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5" />;
    case 'DATE':
      return <input aria-label="Value" type="date" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5" />;
    case 'DATE_TIME':
      return (
        <>
          <input aria-label="Value" type="datetime-local" value={props.value} disabled={props.disabled} onChange={(event) => props.onChange(event.currentTarget.value)} className="rounded border border-slate-300 px-2 py-1.5" />
          <span className="text-xs text-slate-500">Saved as UTC.</span>
        </>
      );
    default: {
      const exhaustive: never = props.kind;
      return exhaustive;
    }
  }
};

const KeepRowsEditor = (props: ConstructionOperationEditorProps) => {
  const { construction, capabilities, editingStep, selectedColumns = [], disabled } = props;
  const stage = capabilities.selectedStage;
  const [stepId, setStepId] = useState(() => createOpaqueId('filter'));
  const [form, setForm] = useState(() => filterFormFromStep(editingStep, stage, selectedColumns));
  const contextKey = editorContextKey(capabilities, editingStep);
  useEffect(() => {
    setStepId(createOpaqueId('filter'));
    setForm(filterFormFromStep(editingStep, stage, selectedColumns));
    props.onCandidateChange(undefined);
  }, [contextKey]);

  const support = operationSupport(stage, 'FILTER');
  const supportedSavedSteps = filterSteps(construction);
  const valueKind = form.kind === 'equals'
    ? filterValueKindFor(stage.columns.find((column) => column.id === form.columnId)?.type)
    : undefined;

  const updateForm = (next: FilterForm) => {
    setForm(next);
    props.onCandidateChange(support.supported
      ? buildFilterCandidate({ construction, stage, editingStep, stepId, form: next })
      : undefined);
  };

  if (!support.supported) {
    return <p role="status" data-testid="construction-filter-unavailable" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{support.reason}</p>;
  }

  return (
    <section aria-label="Keep rows by condition" data-testid="construction-filter-editor" className="grid gap-4">
      <header>
        <h3 className="font-semibold text-slate-900">Keep rows that match</h3>
        <p className="mt-1 text-sm text-slate-600">Add one typed condition at a time. Saved conditions are applied in order, so every condition must match.</p>
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
                  disabled={disabled || !filterStepEditorSupported(step)}
                  onClick={() => props.onEditStep(step.id)}
                  className="shrink-0 rounded px-2 py-1 font-medium text-blue-800 hover:bg-blue-100 disabled:cursor-not-allowed disabled:text-slate-400"
                >
                  Edit
                </button>
              </li>
            ))}
          </ol>
          {supportedSavedSteps.some((step) => !filterStepEditorSupported(step)) ? (
            <p className="mt-2 text-xs text-slate-600">Some saved filters use operators this editor cannot reopen yet. They remain unchanged while you add another condition.</p>
          ) : null}
        </fieldset>
      ) : null}

      {form.kind === 'unsupported' ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{form.message}</p>
      ) : (
        <div className="grid gap-3 rounded-lg border border-slate-200 p-3">
          <p className="text-sm font-medium text-slate-800">{editingStep ? 'Edit this saved condition' : 'New condition'}</p>
          {stage.columns.length === 0 ? (
            <p role="status" className="text-sm text-slate-600">Loom returned no columns for this stage.</p>
          ) : (
            <>
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Column
                <select
                  aria-label="Column"
                  value={form.columnId}
                  disabled={disabled}
                  onChange={(event) => {
                    const column = stage.columns.find((candidate) => candidate.id === event.currentTarget.value);
                    if (!column) return;
                    const next: FilterForm = form.kind === 'missing' || !filterValueKindFor(column.type)
                      ? { kind: 'missing', columnId: column.id }
                      : { kind: 'equals', columnId: column.id, valueText: '', valueEdited: false };
                    updateForm(next);
                  }}
                  className="rounded border border-slate-300 bg-white px-2 py-1.5"
                >
                  {stage.columns.map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type ?? 'unknown type'})</option>)}
                </select>
              </label>

              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Condition
                <select
                  aria-label="Condition"
                  value={form.kind === 'missing' ? 'MISSING' : 'EQUALS'}
                  disabled={disabled}
                  onChange={(event) => {
                    const column = stage.columns.find((candidate) => candidate.id === form.columnId);
                    if (!column) return;
                    const operator = filterOperatorFromInput(event.currentTarget.value);
                    if (!operator) return;
                    updateForm(operator === 'MISSING'
                      ? { kind: 'missing', columnId: column.id }
                      : { kind: 'equals', columnId: column.id, valueText: '', valueEdited: false });
                  }}
                  className="rounded border border-slate-300 bg-white px-2 py-1.5"
                >
                  {filterValueKindFor(stage.columns.find((column) => column.id === form.columnId)?.type)
                    ? <option value="EQUALS">equals</option>
                    : null}
                  <option value="MISSING">is missing</option>
                </select>
              </label>

              {form.kind === 'equals' && valueKind ? (
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  Value
                  <FilterValueInput
                    kind={valueKind}
                    value={form.valueText}
                    disabled={disabled}
                    onChange={(valueText) => updateForm({ ...form, valueText, valueEdited: true })}
                  />
                </label>
              ) : null}
              {form.kind === 'equals' && !valueKind ? (
                <p role="status" className="text-sm text-amber-900">Equality is unavailable for this column type. Choose “is missing”.</p>
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
  readonly value: OperandDraft;
  readonly columns: ConstructionStageDescriptor['columns'];
  readonly disabled: boolean;
  readonly onChange: (value: OperandDraft) => void;
}) => {
  const firstNumeric = props.columns.find(isNumericColumn);
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
                props.onChange(firstNumeric ? { kind: 'column', columnId: firstNumeric.id } : { kind: 'unset' });
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
          <option value="column" disabled={!firstNumeric}>Numeric column</option>
          <option value="literal">Number</option>
        </select>
      </label>

      {props.value.kind === 'column' ? (
        <label className="grid gap-1 text-sm font-medium text-slate-700">
          Column
          <select
            aria-label={`${props.label} column`}
            value={props.value.columnId}
            disabled={props.disabled}
            onChange={(event) => props.onChange({ kind: 'column', columnId: event.currentTarget.value })}
            className="rounded border border-slate-300 bg-white px-2 py-1.5"
          >
            {props.columns.filter(isNumericColumn).map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type})</option>)}
          </select>
        </label>
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
  const [stepId, setStepId] = useState(() => createOpaqueId('derive'));
  const [outputColumnId, setOutputColumnId] = useState(() => createOpaqueId('column'));
  const [form, setForm] = useState(() => calculationFormFromStep(editingStep, stage));
  const contextKey = editorContextKey(capabilities, editingStep);
  useEffect(() => {
    const numericSelected = stage.columns.find((column) => selectedColumns.includes(column.id) && isNumericColumn(column));
    const initial = calculationFormFromStep(editingStep, stage);
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
    setForm(next);
    const expression = completeExpressionFor(next.expression);
    props.onCandidateChange(support.supported && expression
      ? buildDeriveCandidate({ construction, stage, editingStep, stepId, outputColumnId, form: next, expression })
      : undefined);
  };

  const expressionDraft = expressionDraftFor(form.expression);
  const formulaText = form.expression.kind === 'formula-invalid' || form.expression.kind === 'formula-valid'
    ? form.expression.source
    : formulaTextFor(expressionDraft, stage.columns);

  const switchExpressionView = (view: 'guided' | 'formula') => {
    if (view === 'guided') {
      updateForm({ ...form, expression: { kind: 'guided', draft: expressionDraft } });
      return;
    }
    const source = formulaTextFor(expressionDraft, stage.columns);
    const parsed = parseFormula(source, stage.columns);
    updateForm(parsed.kind === 'valid'
      ? { ...form, expression: { kind: 'formula-valid', expression: parsed.expression, source } }
      : { ...form, expression: { kind: 'formula-invalid', lastDraft: expressionDraft, source, error: parsed.message } });
  };

  const changeFormula = (source: string) => {
    const parsed = parseFormula(source, stage.columns);
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
              value={expressionDraft.left}
              columns={stage.columns}
              disabled={disabled}
              onChange={(left) => updateForm({
                ...form,
                expression: { kind: 'guided', draft: { ...expressionDraft, left } },
              })}
            />
            <FormulaOperand
              label="Second value"
              value={expressionDraft.right}
              columns={stage.columns}
              disabled={disabled}
              onChange={(right) => updateForm({
                ...form,
                expression: { kind: 'guided', draft: { ...expressionDraft, right } },
              })}
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
