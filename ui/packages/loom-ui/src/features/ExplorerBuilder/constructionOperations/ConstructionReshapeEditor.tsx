import React, { useEffect, useId, useState } from 'react';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionStageDescriptor,
  ConstructionTableScalar,
  ConstructionStep,
} from '../../../types';
import { constructionSchema } from '../../../types';

type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds'
>;

type ReshapeOperationKind = 'PIVOT' | 'UNPIVOT' | 'GROUP' | 'EXPAND';
type GroupAggregateKind = 'COUNT_ROWS' | 'COUNT_NON_NULL' | 'COUNT_DISTINCT' | 'SUM' | 'MEAN';
type EmptyListPolicy = 'ERROR' | 'EXCLUDE' | 'PRESERVE_PARENT';

export interface ConstructionReshapePivotCategory {
  readonly key: ConstructionTableScalar;
  readonly label: string;
  readonly suggestedName?: string;
  readonly suggestedLabel?: string;
}

export interface ConstructionReshapePivotDiscoveryRequest {
  readonly stageId: string;
  readonly categoryColumnId: string;
  readonly valueColumnId: string;
}

export type ConstructionReshapePivotDiscovery = ConstructionReshapePivotDiscoveryRequest & (
  | { readonly status: 'loading' }
  | { readonly status: 'failed'; readonly reason: string }
  | { readonly status: 'complete'; readonly categories: ReadonlyArray<ConstructionReshapePivotCategory> }
);

type PivotOperation = Extract<ConstructionStep['operation'], { readonly kind: 'PIVOT' }>;
type UnpivotOperation = Extract<ConstructionStep['operation'], { readonly kind: 'UNPIVOT' }>;

type ReshapeColumn = ConstructionStageDescriptor['columns'][number];
type ReshapeStage = ConstructionStageDescriptor;
type ReshapeCapabilities = ConstructionCapabilitiesResponse;

type GroupKey = {
  readonly inputColumnId: string;
  readonly outputColumnId: string;
  readonly name: string;
  readonly label: string;
};

type GroupAggregate =
  | {
      readonly operation: 'COUNT_ROWS';
      readonly outputColumnId: string;
      readonly name: string;
      readonly label: string;
    }
  | {
      readonly operation: Exclude<GroupAggregateKind, 'COUNT_ROWS'>;
      readonly inputColumnId: string;
      readonly outputColumnId: string;
      readonly name: string;
      readonly label: string;
    };

type PivotCategoryForm = {
  readonly key: ConstructionTableScalar;
  readonly outputColumnId: string;
  readonly name: string;
  readonly label: string;
};

type PivotForm = {
  readonly kind: 'pivot';
  readonly stepId: string;
  readonly groupKeyIds: ReadonlyArray<string>;
  readonly categoryColumnId: string;
  readonly valueColumnId: string;
  readonly categories: ReadonlyArray<PivotCategoryForm>;
  readonly duplicatePolicy: 'ERROR' | 'SUM' | 'MIN' | 'MAX';
  readonly missingCellPolicy: 'NULL' | 'ERROR';
  readonly unlistedCategoryPolicy: 'ERROR' | 'EXCLUDE_WITH_EVIDENCE';
  readonly originalPair?: { readonly categoryColumnId: string; readonly valueColumnId: string };
  readonly categoriesPair?: { readonly categoryColumnId: string; readonly valueColumnId: string };
  readonly savedOutputs?: ConstructionStep['outputs'];
};

type UnpivotInputForm = {
  readonly columnId: string;
  readonly key: ConstructionTableScalar;
};

type UnpivotForm = {
  readonly kind: 'unpivot';
  readonly stepId: string;
  readonly inputs: ReadonlyArray<UnpivotInputForm>;
  readonly keyOutputColumnId: string;
  readonly keyOutputName: string;
  readonly keyOutputLabel: string;
  readonly valueOutputColumnId: string;
  readonly valueOutputName: string;
  readonly valueOutputLabel: string;
  readonly nullRowPolicy: 'DROP' | 'PRESERVE';
  readonly savedOutputs?: ConstructionStep['outputs'];
};

export type ConstructionReshapeStep = ConstructionStep;

type GroupForm = {
  readonly kind: 'group';
  readonly stepId: string;
  readonly keys: ReadonlyArray<GroupKey>;
  readonly aggregates: ReadonlyArray<GroupAggregate>;
};

type ExpandForm = {
  readonly kind: 'expand';
  readonly stepId: string;
  readonly inputColumnId: string;
  readonly outputColumnId: string;
  readonly outputName: string;
  readonly outputLabel: string;
  readonly ordinal: {
    readonly kind: 'none';
  } | {
    readonly kind: 'include';
    readonly outputColumnId: string;
    readonly outputName: string;
    readonly outputLabel: string;
  };
  readonly emptyPolicy: EmptyListPolicy | undefined;
  readonly savedOutputs?: ConstructionStep['outputs'];
};

type ReshapeForm =
  | { readonly kind: 'choose' }
  | GroupForm
  | ExpandForm
  | PivotForm
  | UnpivotForm
  | { readonly kind: 'unsupported'; readonly message: string };

export interface ConstructionReshapeEditorProps {
  readonly construction: Construction;
  readonly capabilities: ReshapeCapabilities;
  readonly editingStep?: ConstructionReshapeStep;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly pivotDiscovery?: ConstructionReshapePivotDiscovery;
  readonly onDiscoverCategories?: (request: ConstructionReshapePivotDiscoveryRequest) => void;
  readonly disabled: boolean;
  readonly onCandidateChange: (intent: CandidateIntent | undefined) => void;
  readonly onEditStep: (stepId: string) => void;
}

export const CONSTRUCTION_RESHAPE_EDITABLE_KINDS = ['PIVOT', 'UNPIVOT', 'GROUP', 'EXPAND'] as const;

const createOpaqueId = (prefix: string): string => {
  const randomPart = globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2);
  return `${prefix}_${randomPart}`;
};

const capabilityFor = (
  stage: ReshapeStage,
  kind: ReshapeOperationKind,
): { readonly supported: boolean; readonly reason: string } => {
  const capability = stage.capabilities.find((candidate) => candidate.kind === kind);
  if (!capability) {
    return {
      supported: false,
      reason: `Loom has not returned ${reshapeKindLabel(kind)} support for this stage.`,
    };
  }
  return capability.supported
    ? { supported: true, reason: '' }
    : { supported: false, reason: capability.reason ?? 'Loom does not support this operation at the selected stage.' };
};

const isReshapeOperationKind = (kind: string): kind is ReshapeOperationKind =>
  kind === 'PIVOT' || kind === 'UNPIVOT' || kind === 'GROUP' || kind === 'EXPAND';

const capabilityForStep = (stage: ReshapeStage, kind: string) =>
  isReshapeOperationKind(kind)
    ? capabilityFor(stage, kind)
    : { supported: false, reason: 'This operation is not edited in the reshape panel.' };

const reshapeKindLabel = (kind: ReshapeOperationKind): string => {
  switch (kind) {
    case 'PIVOT': return 'pivot';
    case 'UNPIVOT': return 'unpivot';
    case 'GROUP': return 'group summary';
    case 'EXPAND': return 'list expansion';
    default: {
      const exhaustive: never = kind;
      return exhaustive;
    }
  }
};

const isScalarColumn = (column: ReshapeColumn): boolean =>
  column.cardinality === 'required_one' || column.cardinality === 'optional_one';

const isListColumn = (column: ReshapeColumn): boolean => column.cardinality === 'many';

const isNumericColumn = (column: ReshapeColumn): boolean => {
  const type = (column.type ?? '').trim().toLowerCase();
  return type === 'integer' || type === 'decimal' || type === 'number';
};

const scalarColumnsFor = (stage: ReshapeStage): ReadonlyArray<ReshapeColumn> =>
  stage.columns.filter(isScalarColumn);

const numericColumnsFor = (stage: ReshapeStage): ReadonlyArray<ReshapeColumn> =>
  scalarColumnsFor(stage).filter(isNumericColumn);

const listColumnsFor = (stage: ReshapeStage): ReadonlyArray<ReshapeColumn> =>
  stage.columns.filter(isListColumn);

const normalizedName = (value: string): string =>
  value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'column';

const uniqueName = (base: string, used: ReadonlySet<string>): string => {
  const normalized = normalizedName(base);
  if (!used.has(normalized)) return normalized;
  let suffix = 2;
  while (used.has(`${normalized}_${suffix}`)) suffix += 1;
  return `${normalized}_${suffix}`;
};

const groupOutputColumns = (form: GroupForm, stage: ReshapeStage): ConstructionStep['outputs'] => {
  const outputs = form.keys.map((key) => {
    const input = stage.columns.find((column) => column.id === key.inputColumnId);
    return {
      id: key.outputColumnId,
      name: key.name,
      label: key.label.trim(),
      ...(input?.type ? { type: input.type } : {}),
    };
  });
  for (const aggregate of form.aggregates) {
    const input = aggregate.operation === 'COUNT_ROWS'
      ? undefined
      : stage.columns.find((column) => column.id === aggregate.inputColumnId);
    const type = aggregate.operation === 'MEAN' ? 'decimal'
      : aggregate.operation === 'COUNT_ROWS' || aggregate.operation === 'COUNT_NON_NULL' || aggregate.operation === 'COUNT_DISTINCT'
        ? 'integer'
        : input?.type;
    outputs.push({
      id: aggregate.outputColumnId,
      name: aggregate.name,
      label: aggregate.label.trim(),
      ...(type ? { type } : {}),
    });
  }
  return outputs;
};

const groupOperationFor = (form: GroupForm): Extract<ConstructionStep['operation'], { readonly kind: 'GROUP' }> => ({
  kind: 'GROUP',
  group: {
    constructionId: form.stepId,
    keys: form.keys.map(({ inputColumnId, outputColumnId }) => ({ inputColumnId, outputColumnId })),
    aggregates: form.aggregates.map((aggregate) => aggregate.operation === 'COUNT_ROWS'
      ? { operation: aggregate.operation, outputColumnId: aggregate.outputColumnId }
      : {
          operation: aggregate.operation,
          inputColumnId: aggregate.inputColumnId,
          outputColumnId: aggregate.outputColumnId,
        }),
  },
});

const expandOutputsFor = (form: ExpandForm, stage: ReshapeStage): ConstructionStep['outputs'] => {
  const input = stage.columns.find((column) => column.id === form.inputColumnId);
  if (!input) return [];
  const savedById = new Map(form.savedOutputs?.map((column) => [column.id, column]));
  const output: ConstructionStep['outputs'][number][] = [];
  for (const column of stage.columns) {
    if (column.id !== input.id) {
      output.push(savedById.get(column.id) ?? {
        id: column.id,
        name: column.name,
        label: column.label,
        ...(column.type ? { type: column.type } : {}),
      });
      continue;
    }
    output.push({
      id: form.outputColumnId,
      name: form.outputName,
      label: form.outputLabel.trim(),
      ...(input.type ? { type: input.type } : {}),
    });
    if (form.ordinal.kind === 'include') {
      output.push({
        id: form.ordinal.outputColumnId,
        name: form.ordinal.outputName,
        label: form.ordinal.outputLabel.trim(),
        type: 'integer',
      });
    }
  }
  return output;
};

const expandOperationFor = (form: ExpandForm): Extract<ConstructionStep['operation'], { readonly kind: 'EXPAND' }> | undefined => {
  if (!form.emptyPolicy) return undefined;
  return {
    kind: 'EXPAND',
    expand: {
      constructionId: form.stepId,
      inputColumnId: form.inputColumnId,
      outputColumnId: form.outputColumnId,
      ...(form.ordinal.kind === 'include' ? { ordinalColumnId: form.ordinal.outputColumnId } : {}),
      emptyPolicy: form.emptyPolicy,
    },
  };
};

const scalarIdentity = (scalar: ConstructionTableScalar): string => JSON.stringify(scalar);

const scalarLabel = (scalar: ConstructionTableScalar): string => {
  switch (scalar.kind) {
    case 'BOOLEAN': return String(scalar.boolean);
    case 'DECIMAL': return String(scalar.decimal);
    case 'INTEGER': return String(scalar.integer);
    case 'STRING': return scalar.string;
    case 'NULL': return 'Null value';
    case 'MISSING': return 'Missing value';
    default: {
      const exhaustive: never = scalar;
      return exhaustive;
    }
  }
};

const scalarFromText = (kind: ConstructionTableScalar['kind'], value: string): ConstructionTableScalar | undefined => {
  switch (kind) {
    case 'STRING': return { kind, string: value };
    case 'INTEGER': {
      if (!/^-?\d+$/.test(value.trim())) return undefined;
      const integer = Number(value);
      return Number.isSafeInteger(integer) ? { kind, integer } : undefined;
    }
    case 'DECIMAL': {
      if (value.trim() === '') return undefined;
      const decimal = Number(value);
      return Number.isFinite(decimal) ? { kind, decimal } : undefined;
    }
    case 'BOOLEAN': return value === 'true' || value === 'false' ? { kind, boolean: value === 'true' } : undefined;
    case 'NULL': return { kind };
    case 'MISSING': return { kind };
    default: {
      const exhaustive: never = kind;
      return exhaustive;
    }
  }
};

const scalarKindFromInput = (value: string): ConstructionTableScalar['kind'] | undefined => {
  switch (value) {
    case 'STRING': return 'STRING';
    case 'INTEGER': return 'INTEGER';
    case 'DECIMAL': return 'DECIMAL';
    case 'BOOLEAN': return 'BOOLEAN';
    case 'NULL': return 'NULL';
    case 'MISSING': return 'MISSING';
    default: return undefined;
  }
};

const convertScalarKind = (kind: ConstructionTableScalar['kind'], current: ConstructionTableScalar): ConstructionTableScalar => {
  const value = scalarLabel(current);
  const parsed = scalarFromText(kind, value);
  if (parsed) return parsed;
  switch (kind) {
    case 'STRING': return { kind, string: value };
    case 'INTEGER': return { kind, integer: 0 };
    case 'DECIMAL': return { kind, decimal: 0 };
    case 'BOOLEAN': return { kind, boolean: false };
    case 'NULL': return { kind };
    case 'MISSING': return { kind };
    default: {
      const exhaustive: never = kind;
      return exhaustive;
    }
  }
};

const pivotOutputsFor = (form: PivotForm, stage: ReshapeStage): ConstructionStep['outputs'] => {
  const savedById = new Map(form.savedOutputs?.map((column) => [column.id, column]));
  const groupOutputs = form.groupKeyIds.flatMap((id) => {
    const column = stage.columns.find((candidate) => candidate.id === id);
    if (!column) return [];
    const saved = savedById.get(column.id);
    return [saved ?? { id: column.id, name: column.name, label: column.label, ...(column.type ? { type: column.type } : {}) }];
  });
  const valueType = stage.columns.find((column) => column.id === form.valueColumnId)?.type;
  const categoryOutputs = form.categories.map((category) => ({
    id: category.outputColumnId,
    name: category.name,
    label: category.label.trim(),
    ...((savedById.get(category.outputColumnId)?.type ?? valueType) ? { type: savedById.get(category.outputColumnId)?.type ?? valueType } : {}),
  }));
  return [...groupOutputs, ...categoryOutputs];
};

const pivotOperationFor = (form: PivotForm): PivotOperation => ({
  kind: 'PIVOT',
  pivot: {
    constructionId: form.stepId,
    groupKeyIds: [...form.groupKeyIds],
    categoryColumnId: form.categoryColumnId,
    valueColumnId: form.valueColumnId,
    categories: form.categories.map(({ key, outputColumnId }) => ({ key, outputColumnId })),
    duplicatePolicy: form.duplicatePolicy,
    missingCellPolicy: form.missingCellPolicy,
    unlistedCategoryPolicy: form.unlistedCategoryPolicy,
  },
});

const unpivotOutputsFor = (form: UnpivotForm, stage: ReshapeStage): ConstructionStep['outputs'] => {
  const savedById = new Map(form.savedOutputs?.map((column) => [column.id, column]));
  const inputIDs = new Set(form.inputs.map((input) => input.columnId));
  const inputTypes = form.inputs.map((input) => stage.columns.find((column) => column.id === input.columnId)?.type);
  const valueType = inputTypes.length > 0 && inputTypes.every((type) => type === inputTypes[0]) ? inputTypes[0] : undefined;
  return [
    ...stage.columns.filter((column) => !inputIDs.has(column.id)).map((column) => savedById.get(column.id) ?? ({
      id: column.id,
      name: column.name,
      label: column.label,
      ...(column.type ? { type: column.type } : {}),
    })),
    { id: form.keyOutputColumnId, name: form.keyOutputName, label: form.keyOutputLabel.trim(), type: 'string' },
    { id: form.valueOutputColumnId, name: form.valueOutputName, label: form.valueOutputLabel.trim(), ...(valueType ? { type: valueType } : {}) },
  ];
};

const unpivotOperationFor = (form: UnpivotForm): UnpivotOperation => ({
  kind: 'UNPIVOT',
  unpivot: {
    constructionId: form.stepId,
    inputs: form.inputs.map(({ columnId, key }) => ({ columnId, key })),
    keyOutputColumnId: form.keyOutputColumnId,
    valueOutputColumnId: form.valueOutputColumnId,
    nullRowPolicy: form.nullRowPolicy,
  },
});

const stepInputFor = (stage: ReshapeStage): ConstructionStep['inputs'][number] =>
  !stage.operation
    ? { kind: 'SOURCE_PROJECTION' }
    : { kind: 'STEP_OUTPUT', stepId: stage.id };

type CandidateEvaluation =
  | { readonly kind: 'ready'; readonly intent: CandidateIntent }
  | { readonly kind: 'incomplete' }
  | { readonly kind: 'schema-pending' };

const candidateIntentFor = (args: {
  readonly construction: Construction;
  readonly editingStep?: ConstructionReshapeStep;
  readonly step: ConstructionReshapeStep;
}): CandidateEvaluation => {
  const { construction, editingStep, step } = args;
  if (editingStep && !construction.steps.some((candidate) => candidate.id === editingStep.id)) return { kind: 'incomplete' };
  const draft: unknown = {
    version: construction.version,
    steps: editingStep
      ? construction.steps.map((candidate) => candidate.id === editingStep.id ? step : candidate)
      : [...construction.steps, step],
  };
  const parsed = constructionSchema.safeParse(draft);
  if (!parsed.success) return { kind: 'schema-pending' };
  return {
    kind: 'ready',
    intent: {
      candidateConstruction: parsed.data,
      changedStepId: step.id,
    },
  };
};

const outputNamesAreValid = (columns: ReadonlyArray<{ readonly name: string; readonly label: string }>): boolean => {
  const names = columns.map((column) => column.name.trim().toLowerCase());
  return names.every((name) => /^[A-Za-z_][A-Za-z0-9_]*$/.test(name))
    && names.length === new Set(names).size
    && columns.every((column) => column.label.trim() !== '');
};

const aggregateInputColumnsFor = (
  stage: ReshapeStage,
  operation: GroupAggregateKind,
): ReadonlyArray<ReshapeColumn> => {
  if (operation === 'COUNT_ROWS') return [];
  return operation === 'SUM' || operation === 'MEAN'
    ? numericColumnsFor(stage)
    : scalarColumnsFor(stage);
};

const aggregateNameFor = (operation: GroupAggregateKind, column?: ReshapeColumn): string => {
  switch (operation) {
    case 'COUNT_ROWS': return 'row_count';
    case 'COUNT_NON_NULL': return `${normalizedName(column?.name ?? 'value')}_count`;
    case 'COUNT_DISTINCT': return `${normalizedName(column?.name ?? 'value')}_distinct_count`;
    case 'SUM': return `${normalizedName(column?.name ?? 'value')}_sum`;
    case 'MEAN': return `${normalizedName(column?.name ?? 'value')}_mean`;
    default: {
      const exhaustive: never = operation;
      return exhaustive;
    }
  }
};

const makeAggregate = (
  operation: GroupAggregateKind,
  stage: ReshapeStage,
  usedNames: ReadonlySet<string>,
  inputColumnId?: string,
): GroupAggregate => {
  const inputOptions = aggregateInputColumnsFor(stage, operation);
  const input = inputOptions.find((column) => column.id === inputColumnId) ?? inputOptions[0];
  const base = aggregateNameFor(operation, input);
  const common = {
    outputColumnId: createOpaqueId('group-column'),
    name: uniqueName(base, usedNames),
    label: operation === 'COUNT_ROWS'
      ? 'Row count'
      : `${operationLabel(operation)} of ${input?.label ?? input?.name ?? 'value'}`,
  };
  return operation === 'COUNT_ROWS'
    ? { operation, ...common }
    : { operation, inputColumnId: input?.id ?? '', ...common };
};

const operationLabel = (operation: GroupAggregateKind): string => {
  switch (operation) {
    case 'COUNT_ROWS': return 'Count rows';
    case 'COUNT_NON_NULL': return 'Count present values';
    case 'COUNT_DISTINCT': return 'Count distinct values';
    case 'SUM': return 'Sum';
    case 'MEAN': return 'Mean';
    default: {
      const exhaustive: never = operation;
      return exhaustive;
    }
  }
};

const groupStepFromForm = (args: {
  readonly stage: ReshapeStage;
  readonly editingStep?: ConstructionReshapeStep;
  readonly form: GroupForm;
}): ConstructionReshapeStep | undefined => {
  const { stage, editingStep, form } = args;
  const outputs = groupOutputColumns(form, stage);
  if (form.aggregates.length === 0 || !outputNamesAreValid(outputs)) return undefined;
  if (form.keys.some((key) => !scalarColumnsFor(stage).some((column) => column.id === key.inputColumnId))) return undefined;
  if (new Set(form.keys.map((key) => key.inputColumnId)).size !== form.keys.length) return undefined;
  if (form.aggregates.some((aggregate) => aggregate.operation !== 'COUNT_ROWS'
      && !aggregateInputColumnsFor(stage, aggregate.operation).some((column) => column.id === aggregate.inputColumnId))) return undefined;
  const step: ConstructionReshapeStep = {
    id: editingStep?.id ?? form.stepId,
    inputs: [stepInputFor(stage)],
    operation: groupOperationFor(form),
    outputs,
  };
  return step;
};

const expandStepFromForm = (args: {
  readonly stage: ReshapeStage;
  readonly editingStep?: ConstructionReshapeStep;
  readonly form: ExpandForm;
}): ConstructionReshapeStep | undefined => {
  const { stage, editingStep, form } = args;
  const operation = expandOperationFor(form);
  const input = listColumnsFor(stage).find((column) => column.id === form.inputColumnId);
  const outputs = expandOutputsFor(form, stage);
  if (!operation || !input || outputs.length === 0 || !outputNamesAreValid(outputs)) return undefined;
  const step: ConstructionReshapeStep = {
    id: editingStep?.id ?? form.stepId,
    inputs: [stepInputFor(stage)],
    operation,
    outputs,
  };
  return step;
};

const pivotStepFromForm = (args: {
  readonly stage: ReshapeStage;
  readonly editingStep?: ConstructionReshapeStep;
  readonly form: PivotForm;
}): ConstructionReshapeStep | undefined => {
  const { stage, editingStep, form } = args;
  const categoryColumn = scalarColumnsFor(stage).find((column) => column.id === form.categoryColumnId);
  const valueColumn = scalarColumnsFor(stage).find((column) => column.id === form.valueColumnId);
  const groupIDs = new Set(form.groupKeyIds);
  const outputIDs = [...form.groupKeyIds, ...form.categories.map((category) => category.outputColumnId)];
  const keyIDs = form.categories.map((category) => scalarIdentity(category.key));
  const outputs = pivotOutputsFor(form, stage);
  if (form.groupKeyIds.length === 0 || form.categories.length === 0 || !categoryColumn || !valueColumn) return undefined;
  if (groupIDs.size !== form.groupKeyIds.length || groupIDs.has(form.categoryColumnId) || groupIDs.has(form.valueColumnId)) return undefined;
  if (form.groupKeyIds.some((id) => !scalarColumnsFor(stage).some((column) => column.id === id))) return undefined;
  if (form.categoryColumnId === form.valueColumnId || new Set(keyIDs).size !== keyIDs.length) return undefined;
  if (new Set(outputIDs).size !== outputIDs.length || !outputNamesAreValid(outputs)) return undefined;
  const step: ConstructionReshapeStep = {
    id: editingStep?.id ?? form.stepId,
    inputs: [stepInputFor(stage)],
    operation: pivotOperationFor(form),
    outputs,
  };
  return step;
};

const unpivotStepFromForm = (args: {
  readonly stage: ReshapeStage;
  readonly editingStep?: ConstructionReshapeStep;
  readonly form: UnpivotForm;
}): ConstructionReshapeStep | undefined => {
  const { stage, editingStep, form } = args;
  const inputIDs = form.inputs.map((input) => input.columnId);
  const keys = form.inputs.map((input) => scalarIdentity(input.key));
  const outputs = unpivotOutputsFor(form, stage);
  if (form.inputs.length === 0 || form.inputs.some((input) => !stage.columns.some((column) => column.id === input.columnId))) return undefined;
  if (form.inputs.some((input) => input.key.kind === 'NULL' || input.key.kind === 'MISSING')) return undefined;
  if (new Set(inputIDs).size !== inputIDs.length || new Set(keys).size !== keys.length) return undefined;
  if (inputIDs.includes(form.keyOutputColumnId) || inputIDs.includes(form.valueOutputColumnId)) return undefined;
  if (form.keyOutputColumnId === form.valueOutputColumnId || !outputNamesAreValid(outputs)) return undefined;
  const step: ConstructionReshapeStep = {
    id: editingStep?.id ?? form.stepId,
    inputs: [stepInputFor(stage)],
    operation: unpivotOperationFor(form),
    outputs,
  };
  return step;
};

const initialGroupForm = (
  stage: ReshapeStage,
  selectedColumns: ReadonlyArray<string>,
  editingStep?: ConstructionReshapeStep,
): GroupForm => {
  if (editingStep?.operation.kind === 'GROUP') {
    const operation = editingStep.operation.group;
    const outputById = new Map(editingStep.outputs.map((column) => [column.id, column]));
    const keys = (operation.keys ?? []).map((key) => {
      const output = outputById.get(key.outputColumnId);
      const input = stage.columns.find((column) => column.id === key.inputColumnId);
      return {
        inputColumnId: key.inputColumnId,
        outputColumnId: key.outputColumnId,
        name: output?.name ?? input?.name ?? 'group_key',
        label: output?.label ?? input?.label ?? 'Group',
      };
    });
    const aggregates: GroupAggregate[] = (operation.aggregates ?? []).map((aggregate) => {
      const output = outputById.get(aggregate.outputColumnId);
      const inputColumnId = 'inputColumnId' in aggregate ? aggregate.inputColumnId : undefined;
      const common = {
        outputColumnId: aggregate.outputColumnId,
        name: output?.name ?? aggregateNameFor(aggregate.operation, stage.columns.find((column) => column.id === inputColumnId)),
        label: output?.label ?? operationLabel(aggregate.operation),
      };
      return aggregate.operation === 'COUNT_ROWS'
        ? { operation: aggregate.operation, ...common }
        : { operation: aggregate.operation, inputColumnId: inputColumnId ?? '', ...common };
    });
    return { kind: 'group', stepId: editingStep.id, keys, aggregates };
  }

  const scalarIDs = new Set(scalarColumnsFor(stage).map((column) => column.id));
  const keys = selectedColumns
    .filter((columnId) => scalarIDs.has(columnId))
    .map((inputColumnId) => {
      const input = stage.columns.find((column) => column.id === inputColumnId);
      return {
        inputColumnId,
        outputColumnId: createOpaqueId('group-key'),
        name: input?.name ?? 'group_key',
        label: input?.label ?? 'Group',
      };
    });
  const aggregate = makeAggregate('COUNT_ROWS', stage, new Set());
  return { kind: 'group', stepId: createOpaqueId('group'), keys, aggregates: [aggregate] };
};

const initialExpandForm = (
  stage: ReshapeStage,
  editingStep?: ConstructionReshapeStep,
): ExpandForm => {
  if (editingStep?.operation.kind === 'EXPAND') {
    const operation = editingStep.operation.expand;
    const outputById = new Map(editingStep.outputs.map((column) => [column.id, column]));
    const output = outputById.get(operation.outputColumnId);
    const ordinal = operation.ordinalColumnId ? outputById.get(operation.ordinalColumnId) : undefined;
    return {
      kind: 'expand',
      stepId: editingStep.id,
      inputColumnId: operation.inputColumnId,
      outputColumnId: operation.outputColumnId,
      outputName: output?.name ?? 'item',
      outputLabel: output?.label ?? 'Item',
      ordinal: ordinal
        ? {
            kind: 'include',
            outputColumnId: operation.ordinalColumnId ?? ordinal.id,
            outputName: ordinal.name,
            outputLabel: ordinal.label,
          }
        : { kind: 'none' },
      emptyPolicy: operation.emptyPolicy,
      savedOutputs: editingStep.outputs,
    };
  }
  const firstList = listColumnsFor(stage)[0];
  return {
    kind: 'expand',
    stepId: createOpaqueId('expand'),
    inputColumnId: firstList?.id ?? '',
    outputColumnId: createOpaqueId('expanded-column'),
    outputName: firstList ? `${normalizedName(firstList.name)}_item` : 'item',
    outputLabel: firstList ? `${firstList.label} item` : 'Item',
    ordinal: { kind: 'none' },
    emptyPolicy: undefined,
  };
};

const initialPivotForm = (
  stage: ReshapeStage,
  selectedColumns: ReadonlyArray<string>,
  editingStep?: ConstructionReshapeStep,
): PivotForm => {
  if (editingStep?.operation.kind === 'PIVOT') {
    const operation = editingStep.operation.pivot;
    const outputById = new Map(editingStep.outputs.map((column) => [column.id, column]));
    return {
      kind: 'pivot',
      stepId: editingStep.id,
      groupKeyIds: [...operation.groupKeyIds],
      categoryColumnId: operation.categoryColumnId,
      valueColumnId: operation.valueColumnId,
      categories: operation.categories.map((category) => {
        const output = outputById.get(category.outputColumnId);
        return {
          key: category.key,
          outputColumnId: category.outputColumnId,
          name: output?.name ?? `category_${normalizedName(scalarLabel(category.key))}`,
          label: output?.label ?? scalarLabel(category.key),
        };
      }),
      duplicatePolicy: operation.duplicatePolicy,
      missingCellPolicy: operation.missingCellPolicy,
      unlistedCategoryPolicy: operation.unlistedCategoryPolicy,
      originalPair: { categoryColumnId: operation.categoryColumnId, valueColumnId: operation.valueColumnId },
      categoriesPair: { categoryColumnId: operation.categoryColumnId, valueColumnId: operation.valueColumnId },
      savedOutputs: editingStep.outputs,
    };
  }

  const scalar = scalarColumnsFor(stage);
  const groups = selectedColumns.filter((columnId) => scalar.some((column) => column.id === columnId));
  const category = scalar.find((column) => !groups.includes(column.id));
  const value = numericColumnsFor(stage).find((column) => column.id !== category?.id && !groups.includes(column.id))
    ?? scalar.find((column) => column.id !== category?.id && !groups.includes(column.id));
  return {
    kind: 'pivot',
    stepId: createOpaqueId('pivot'),
    groupKeyIds: groups,
    categoryColumnId: category?.id ?? '',
    valueColumnId: value?.id ?? '',
    categories: [],
    duplicatePolicy: 'ERROR',
    missingCellPolicy: 'NULL',
    unlistedCategoryPolicy: 'ERROR',
  };
};

const defaultKeyForColumn = (column: ReshapeColumn | undefined): ConstructionTableScalar => ({
  kind: 'STRING',
  string: column?.name ?? 'value',
});

const initialUnpivotForm = (
  stage: ReshapeStage,
  selectedColumns: ReadonlyArray<string>,
  editingStep?: ConstructionReshapeStep,
): UnpivotForm => {
  if (editingStep?.operation.kind === 'UNPIVOT') {
    const operation = editingStep.operation.unpivot;
    const outputById = new Map(editingStep.outputs.map((column) => [column.id, column]));
    const keyOutput = outputById.get(operation.keyOutputColumnId);
    const valueOutput = outputById.get(operation.valueOutputColumnId);
    return {
      kind: 'unpivot',
      stepId: editingStep.id,
      inputs: operation.inputs.map((input) => ({ columnId: input.columnId, key: input.key })),
      keyOutputColumnId: operation.keyOutputColumnId,
      keyOutputName: keyOutput?.name ?? 'variable',
      keyOutputLabel: keyOutput?.label ?? 'Variable',
      valueOutputColumnId: operation.valueOutputColumnId,
      valueOutputName: valueOutput?.name ?? 'value',
      valueOutputLabel: valueOutput?.label ?? 'Value',
      nullRowPolicy: operation.nullRowPolicy,
      savedOutputs: editingStep.outputs,
    };
  }
  const initialInputs = selectedColumns.flatMap((columnId) => {
    const column = stage.columns.find((candidate) => candidate.id === columnId);
    return column ? [{ columnId, key: defaultKeyForColumn(column) }] : [];
  });
  return {
    kind: 'unpivot',
    stepId: createOpaqueId('unpivot'),
    inputs: initialInputs,
    keyOutputColumnId: createOpaqueId('unpivot-key'),
    keyOutputName: uniqueName('variable', new Set(stage.columns.map((column) => column.name.toLowerCase()))),
    keyOutputLabel: 'Variable',
    valueOutputColumnId: createOpaqueId('unpivot-value'),
    valueOutputName: uniqueName('value', new Set(stage.columns.map((column) => column.name.toLowerCase()))),
    valueOutputLabel: 'Value',
    nullRowPolicy: 'DROP',
  };
};

const stageForStep = (
  step: ConstructionReshapeStep,
  capabilities: ReshapeCapabilities,
): ReshapeStage | undefined => {
  const outputStage = capabilities.stages.find((stage) => stage.id === step.id);
  return outputStage
    ? capabilities.stages.find((stage) => stage.id === outputStage.inputStageId)
    : undefined;
};

const stepDescription = (step: ConstructionReshapeStep, capabilities: ReshapeCapabilities): string => {
  const stage = stageForStep(step, capabilities);
  switch (step.operation.kind) {
    case 'GROUP': {
      const group = step.operation.group;
      const keys = (group.keys ?? []).map((key) => stage?.columns.find((column) => column.id === key.inputColumnId)?.label ?? 'a column');
      const measures = (group.aggregates ?? []).length;
      return keys.length > 0
        ? `One row per ${keys.join(', ')} with ${measures} ${measures === 1 ? 'summary' : 'summaries'}.`
        : `One summary row for the whole table with ${measures} ${measures === 1 ? 'summary' : 'summaries'}.`;
    }
    case 'EXPAND': {
      const expand = step.operation.expand;
      const input = stage?.columns.find((column) => column.id === expand.inputColumnId);
      const policy = expand.emptyPolicy ?? 'EXCLUDE';
      return `Make one row per item in ${input?.label ?? input?.name ?? 'the selected list'}. Empty lists: ${emptyPolicyLabel(policy).toLowerCase()}.`;
    }
    case 'PIVOT': {
      const pivot = step.operation.pivot;
      const category = stage?.columns.find((column) => column.id === pivot.categoryColumnId);
      const values = pivot.categories.length;
      return `Turn ${category?.label ?? 'category values'} into ${values} columns, grouped by ${pivot.groupKeyIds.length} ${pivot.groupKeyIds.length === 1 ? 'field' : 'fields'}.`;
    }
    case 'UNPIVOT': {
      const unpivot = step.operation.unpivot;
      const action = unpivot.nullRowPolicy === 'PRESERVE' ? 'keep' : 'drop';
      return `Turn ${unpivot.inputs.length} columns into rows and ${action} rows with missing values.`;
    }
    default:
      return 'A saved operation this editor does not reopen.';
  }
};

const groupKeyName = (stage: ReshapeStage, key: GroupKey): string =>
  stage.columns.find((column) => column.id === key.inputColumnId)?.label ?? 'selected column';

const emptyPolicyLabel = (policy: EmptyListPolicy): string => {
  switch (policy) {
    case 'ERROR': return 'Stop with an error';
    case 'EXCLUDE': return 'Drop the original row';
    case 'PRESERVE_PARENT': return 'Keep the row with a missing item';
    default: {
      const exhaustive: never = policy;
      return exhaustive;
    }
  }
};

const editorContextKey = (
  capabilities: ReshapeCapabilities,
  editingStep: ConstructionReshapeStep | undefined,
): string => [
  capabilities.snapshotToken,
  capabilities.draftVersion,
  capabilities.draftDigest,
  capabilities.outputId,
  capabilities.stageId,
  editingStep?.id ?? 'new',
].join(':');

const formForStep = (
  editingStep: ConstructionReshapeStep | undefined,
  stage: ReshapeStage,
): ReshapeForm => {
  if (!editingStep) return { kind: 'choose' };
  if (editingStep.operation.kind === 'GROUP') return initialGroupForm(stage, [], editingStep);
  if (editingStep.operation.kind === 'EXPAND') return initialExpandForm(stage, editingStep);
  if (editingStep.operation.kind === 'PIVOT') return initialPivotForm(stage, [], editingStep);
  if (editingStep.operation.kind === 'UNPIVOT') return initialUnpivotForm(stage, [], editingStep);
  return { kind: 'unsupported', message: 'This saved reshape is not supported by this editor.' };
};

const pivotDiscoveryMatches = (
  form: PivotForm,
  stageId: string,
  discovery: ConstructionReshapePivotDiscovery | undefined,
): discovery is ConstructionReshapePivotDiscovery => Boolean(
  discovery
  && discovery.stageId === stageId
  && discovery.categoryColumnId === form.categoryColumnId
  && discovery.valueColumnId === form.valueColumnId,
);

const pivotCategoriesAreKnown = (
  form: PivotForm,
  stageId: string,
  discovery: ConstructionReshapePivotDiscovery | undefined,
): boolean => {
  const categoriesBelongToPair = form.categoriesPair?.categoryColumnId === form.categoryColumnId
    && form.categoriesPair.valueColumnId === form.valueColumnId;
  const resultBelongsToPair = pivotDiscoveryMatches(form, stageId, discovery)
    && discovery.status === 'complete';
  return categoriesBelongToPair && (resultBelongsToPair || form.originalPair?.categoryColumnId === form.categoryColumnId
    && form.originalPair.valueColumnId === form.valueColumnId);
};

const getUsedGroupNames = (form: GroupForm, excludingColumnId?: string): ReadonlySet<string> =>
  new Set([
    ...form.keys.filter((key) => key.outputColumnId !== excludingColumnId).map((key) => key.name.trim().toLowerCase()),
    ...form.aggregates.filter((aggregate) => aggregate.outputColumnId !== excludingColumnId).map((aggregate) => aggregate.name.trim().toLowerCase()),
  ]);

const addGroupKey = (form: GroupForm, stage: ReshapeStage, inputColumnId: string): GroupForm => {
  const input = scalarColumnsFor(stage).find((column) => column.id === inputColumnId);
  if (!input || form.keys.some((key) => key.inputColumnId === inputColumnId)) return form;
  const usedNames = getUsedGroupNames(form);
  return {
    ...form,
    keys: [...form.keys, {
      inputColumnId,
      outputColumnId: createOpaqueId('group-key'),
      name: uniqueName(input.name, usedNames),
      label: input.label,
    }],
  };
};

const replaceGroupAggregate = (
  aggregate: GroupAggregate,
  operation: GroupAggregateKind,
  stage: ReshapeStage,
  usedNames: ReadonlySet<string>,
): GroupAggregate => {
  const inputOptions = aggregateInputColumnsFor(stage, operation);
  const priorInputId = aggregate.operation === 'COUNT_ROWS' ? undefined : aggregate.inputColumnId;
  const input = inputOptions.find((column) => column.id === priorInputId) ?? inputOptions[0];
  const common = {
    outputColumnId: aggregate.outputColumnId,
    name: uniqueName(aggregateNameFor(operation, input), usedNames),
    label: operation === 'COUNT_ROWS' ? 'Row count' : `${operationLabel(operation)} of ${input?.label ?? input?.name ?? 'value'}`,
  };
  return operation === 'COUNT_ROWS'
    ? { operation, ...common }
    : { operation, inputColumnId: input?.id ?? '', ...common };
};

const candidateFor = (args: {
  readonly form: ReshapeForm;
  readonly construction: Construction;
  readonly stage: ReshapeStage;
  readonly editingStep?: ConstructionReshapeStep;
  readonly pivotCategoriesKnown: boolean;
}): CandidateEvaluation => {
  const { form, construction, stage, editingStep, pivotCategoriesKnown } = args;
  if (form.kind === 'group') {
    const step = groupStepFromForm({ stage, editingStep, form });
    return step ? candidateIntentFor({ construction, editingStep, step }) : { kind: 'incomplete' };
  }
  if (form.kind === 'expand') {
    const step = expandStepFromForm({ stage, editingStep, form });
    return step ? candidateIntentFor({ construction, editingStep, step }) : { kind: 'incomplete' };
  }
  if (form.kind === 'pivot') {
    if (!pivotCategoriesKnown) return { kind: 'incomplete' };
    const step = pivotStepFromForm({ stage, editingStep, form });
    return step ? candidateIntentFor({ construction, editingStep, step }) : { kind: 'incomplete' };
  }
  if (form.kind === 'unpivot') {
    const step = unpivotStepFromForm({ stage, editingStep, form });
    return step ? candidateIntentFor({ construction, editingStep, step }) : { kind: 'incomplete' };
  }
  return { kind: 'incomplete' };
};

export const ConstructionReshapeEditor = (props: ConstructionReshapeEditorProps) => {
  const id = useId();
  const { construction, capabilities, editingStep, disabled, onCandidateChange, onEditStep } = props;
  const stage = capabilities.selectedStage;
  const contextKey = editorContextKey(capabilities, editingStep);
  const [form, setForm] = useState<ReshapeForm>(() => formForStep(editingStep, stage));
  const [contractUnavailable, setContractUnavailable] = useState(false);
  const groupSupport = capabilityFor(stage, 'GROUP');
  const expandSupport = capabilityFor(stage, 'EXPAND');
  const pivotSupport = capabilityFor(stage, 'PIVOT');
  const unpivotSupport = capabilityFor(stage, 'UNPIVOT');
  const newPivotSupport = props.onDiscoverCategories
    ? pivotSupport
    : { supported: false, reason: pivotSupport.supported ? 'Stage-scoped category discovery is not available yet.' : pivotSupport.reason };
  const expandColumns = listColumnsFor(stage);
  const pivotDiscovery = props.pivotDiscovery;

  useEffect(() => {
    setForm(formForStep(editingStep, capabilities.selectedStage));
    setContractUnavailable(false);
    onCandidateChange(undefined);
  }, [contextKey]);

  const capabilityForForm = (next: ReshapeForm) => {
    switch (next.kind) {
      case 'group': return groupSupport;
      case 'expand': return expandSupport;
      case 'pivot': return pivotSupport;
      case 'unpivot': return unpivotSupport;
      default: return undefined;
    }
  };

  const evaluate = (next: ReshapeForm): CandidateEvaluation => {
    const support = capabilityForForm(next);
    if (!support?.supported) return { kind: 'incomplete' };
    return candidateFor({
      form: next,
      construction,
      stage,
      editingStep,
      pivotCategoriesKnown: next.kind === 'pivot' && pivotCategoriesAreKnown(next, stage.id, pivotDiscovery),
    });
  };

  const updateForm = (next: ReshapeForm) => {
    setForm(next);
    const evaluation = evaluate(next);
    setContractUnavailable(evaluation.kind === 'schema-pending');
    onCandidateChange(evaluation.kind === 'ready' ? evaluation.intent : undefined);
  };

  const savedSteps = construction.steps.filter((step) =>
    isReshapeOperationKind(step.operation.kind),
  );

  const requestPivotCategories = (pivot: PivotForm) => {
    if (!props.onDiscoverCategories || pivot.categoryColumnId === '' || pivot.valueColumnId === '') return;
    props.onDiscoverCategories({
      stageId: stage.id,
      categoryColumnId: pivot.categoryColumnId,
      valueColumnId: pivot.valueColumnId,
    });
  };

  return (
    <section aria-label="Reshape editor" data-testid="construction-reshape-editor" className="grid gap-4">
      <header>
        <h3 className="font-semibold text-slate-900">Change the table shape</h3>
        <p className="mt-1 text-sm text-slate-600">Choose what each new row represents, then set the columns to keep.</p>
      </header>

      {savedSteps.length > 0 ? (
        <fieldset className="grid gap-2 rounded-lg border border-slate-200 p-3" data-testid="construction-reshape-history">
          <legend className="px-1 text-sm font-semibold text-slate-800">Saved reshape steps</legend>
          <ol className="grid gap-2">
            {savedSteps.map((step, index) => (
              <li key={step.id} className="flex items-center justify-between gap-3 rounded bg-slate-50 px-3 py-2 text-sm">
                <span><span className="mr-2 text-slate-500">{index + 1}.</span>{stepDescription(step, capabilities)}</span>
                <button
                  type="button"
                  data-testid={`construction-reshape-edit-${step.id}`}
                  disabled={disabled || !capabilityForStep(stageForStep(step, capabilities) ?? stage, step.operation.kind).supported}
                  onClick={() => onEditStep(step.id)}
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
        <p role="status" data-testid="construction-reshape-edit-unavailable" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{form.message}</p>
      ) : null}
      {contractUnavailable ? (
        <p role="status" data-testid="construction-reshape-schema-unavailable" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">The construction schema did not accept this reshape, so Loom has not previewed it.</p>
      ) : null}

      {!editingStep && form.kind !== 'unsupported' ? (
        <fieldset className="grid gap-2">
          <legend className="mb-1 text-sm font-semibold text-slate-800">Choose a reshape</legend>
          <ReshapeChoice
            testId="construction-reshape-choice-group"
            title="Summarize into groups"
            description="Make one row for each selected group and calculate summaries. With no group fields, make one summary row for the whole table."
            supported={groupSupport.supported}
            reason={groupSupport.reason}
            selected={form.kind === 'group'}
            disabled={disabled}
            onChoose={() => updateForm(initialGroupForm(stage, props.selectedColumns ?? []))}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-expand"
            title="Expand a repeated value"
            description="Make a separate row for each item in a list. Choose what happens to rows with empty lists."
            supported={expandSupport.supported}
            reason={expandSupport.reason || (expandColumns.length === 0 ? 'Loom has not identified a list-valued column at this stage.' : '')}
            selected={form.kind === 'expand'}
            disabled={disabled || expandColumns.length === 0}
            onChoose={() => updateForm(initialExpandForm(stage))}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-pivot"
            title="Turn categories into columns"
            description="Choose group fields, a category field, and values to fill one column per category."
            supported={newPivotSupport.supported}
            reason={newPivotSupport.reason}
            selected={form.kind === 'pivot'}
            disabled={disabled}
            onChoose={() => updateForm(initialPivotForm(stage, props.selectedColumns ?? []))}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-unpivot"
            title="Turn columns into rows"
            description="Choose columns with the same meaning and give each column a key in the new rows."
            supported={unpivotSupport.supported}
            reason={unpivotSupport.reason}
            selected={form.kind === 'unpivot'}
            disabled={disabled}
            onChoose={() => updateForm(initialUnpivotForm(stage, props.selectedColumns ?? []))}
          />
          {form.kind === 'choose' && !groupSupport.supported && !expandSupport.supported && !pivotSupport.supported && !unpivotSupport.supported ? (
            <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">
              Loom has not confirmed a reshape this stage can run. The operation choices stay unavailable until it returns support.
            </p>
          ) : null}
        </fieldset>
      ) : null}

      {form.kind === 'group' ? (
        <GroupEditor
          key={`${id}:group:${contextKey}`}
          form={form}
          stage={stage}
          supported={groupSupport.supported}
          reason={groupSupport.reason}
          disabled={disabled}
          onChange={(next) => updateForm(next)}
        />
      ) : null}

      {form.kind === 'expand' ? (
        <ExpandEditor
          key={`${id}:expand:${contextKey}`}
          form={form}
          stage={stage}
          supported={expandSupport.supported}
          reason={expandSupport.reason}
          disabled={disabled}
          onChange={(next) => updateForm(next)}
        />
      ) : null}
      {form.kind === 'pivot' ? (
        <PivotEditor
          key={`${id}:pivot:${contextKey}`}
          form={form}
          stage={stage}
          supported={pivotSupport.supported}
          reason={pivotSupport.reason}
          disabled={disabled}
          discovery={pivotDiscovery}
          canDiscover={Boolean(props.onDiscoverCategories)}
          onDiscover={() => requestPivotCategories(form)}
          onChange={updateForm}
        />
      ) : null}
      {form.kind === 'unpivot' ? (
        <UnpivotEditor
          key={`${id}:unpivot:${contextKey}`}
          form={form}
          stage={stage}
          supported={unpivotSupport.supported}
          reason={unpivotSupport.reason}
          disabled={disabled}
          onChange={updateForm}
        />
      ) : null}
    </section>
  );
};

const ReshapeChoice = (props: {
  readonly testId: string;
  readonly title: string;
  readonly description: string;
  readonly supported: boolean;
  readonly reason: string;
  readonly selected: boolean;
  readonly disabled: boolean;
  readonly onChoose: () => void;
}) => (
  <button
    type="button"
    data-testid={props.testId}
    aria-pressed={props.selected}
    disabled={props.disabled || !props.supported}
    onClick={props.onChoose}
    className="grid gap-1 rounded-lg border border-slate-200 p-3 text-left enabled:hover:border-blue-400 enabled:hover:bg-blue-50 disabled:cursor-not-allowed disabled:bg-slate-50"
  >
    <span className="text-sm font-semibold text-slate-900">{props.title}</span>
    <span className="text-sm text-slate-600">{props.description}</span>
    {!props.supported || props.reason ? <span className="text-xs text-amber-900">{props.reason}</span> : null}
  </button>
);

const GroupEditor = (props: {
  readonly form: GroupForm;
  readonly stage: ReshapeStage;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly onChange: (form: GroupForm) => void;
}) => {
  const scalarColumns = scalarColumnsFor(props.stage);
  const usedNames = getUsedGroupNames(props.form);
  const changeKey = (index: number, update: Partial<GroupKey>) => {
    props.onChange({
      ...props.form,
      keys: props.form.keys.map((key, keyIndex) => keyIndex === index ? { ...key, ...update } : key),
    });
  };
  const changeAggregate = (index: number, aggregate: GroupAggregate) => {
    props.onChange({
      ...props.form,
      aggregates: props.form.aggregates.map((current, aggregateIndex) => aggregateIndex === index ? aggregate : current),
    });
  };

  return (
    <section aria-label="Summarize into groups" data-testid="construction-reshape-group" className="grid gap-4 rounded-lg border border-slate-200 p-3">
      <header>
        <h4 className="text-sm font-semibold text-slate-900">Summarize rows</h4>
        <p className="mt-1 text-sm text-slate-600">Group fields become the new row labels. Each summary becomes another column.</p>
      </header>
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}

      <fieldset className="grid gap-2" disabled={props.disabled || !props.supported}>
        <legend className="text-sm font-semibold text-slate-800">One row per</legend>
        {scalarColumns.length === 0 ? (
          <p role="status" className="text-sm text-slate-600">Loom has not returned scalar columns for this stage. A whole-table summary is still available.</p>
        ) : (
          <div className="grid gap-2 sm:grid-cols-2">
            {scalarColumns.map((column) => {
              const checked = props.form.keys.some((key) => key.inputColumnId === column.id);
              return (
                <label key={column.id} className="flex items-start gap-2 rounded border border-slate-200 px-3 py-2 text-sm">
                  <input
                    type="checkbox"
                    checked={checked}
                    aria-label={`Group by ${column.label}`}
                    onChange={() => props.onChange(checked
                      ? { ...props.form, keys: props.form.keys.filter((key) => key.inputColumnId !== column.id) }
                      : addGroupKey(props.form, props.stage, column.id))}
                    className="mt-0.5"
                  />
                  <span><span className="block font-medium text-slate-800">{column.label}</span><span className="block text-xs text-slate-500">{column.type ?? 'Unknown type'}</span></span>
                </label>
              );
            })}
          </div>
        )}
      </fieldset>

      {props.form.keys.length > 0 ? (
        <fieldset className="grid gap-3 rounded-lg border border-slate-200 p-3">
          <legend className="px-1 text-sm font-semibold text-slate-800">Group column names</legend>
          {props.form.keys.map((key, index) => (
            <div key={key.outputColumnId} className="grid gap-2 sm:grid-cols-2">
              <p className="sm:col-span-2 text-xs text-slate-500">Values from {groupKeyName(props.stage, key)} identify each group.</p>
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Column name
                <input aria-label={`Group output name ${index + 1}`} value={key.name} disabled={props.disabled || !props.supported} onChange={(event) => changeKey(index, { name: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
              </label>
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Column label
                <input aria-label={`Group output label ${index + 1}`} value={key.label} disabled={props.disabled || !props.supported} onChange={(event) => changeKey(index, { label: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
              </label>
            </div>
          ))}
        </fieldset>
      ) : null}

      <fieldset className="grid gap-3 rounded-lg border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
        <legend className="px-1 text-sm font-semibold text-slate-800">Summaries</legend>
        <p className="text-sm text-slate-600">Choose the value to calculate for each group. Count rows works without a selected field.</p>
        {props.form.aggregates.map((aggregate, index) => {
          const inputColumns = aggregateInputColumnsFor(props.stage, aggregate.operation);
          const operation: GroupAggregateKind = aggregate.operation;
          return (
            <div key={aggregate.outputColumnId} data-testid={`construction-reshape-aggregate-${index}`} className="grid gap-2 rounded bg-slate-50 p-3 sm:grid-cols-2">
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Summary
                <select
                  aria-label={`Summary ${index + 1}`}
                  value={operation}
                  onChange={(event) => {
                    const selected = groupAggregateKindFromInput(event.currentTarget.value);
                    if (!selected) return;
                    const replacement = replaceGroupAggregate(aggregate, selected, props.stage, getUsedGroupNames(props.form, aggregate.outputColumnId));
                    changeAggregate(index, replacement);
                  }}
                  className="rounded border border-slate-300 bg-white px-2 py-1.5"
                >
                  <option value="COUNT_ROWS">Count rows</option>
                  <option value="COUNT_NON_NULL">Count present values</option>
                  <option value="COUNT_DISTINCT">Count distinct values</option>
                  <option value="SUM" disabled={numericColumnsFor(props.stage).length === 0}>Sum numeric values</option>
                  <option value="MEAN" disabled={numericColumnsFor(props.stage).length === 0}>Mean numeric values</option>
                </select>
              </label>
              {aggregate.operation !== 'COUNT_ROWS' ? (
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  Field
                  <select
                    aria-label={`Summary field ${index + 1}`}
                    value={aggregate.inputColumnId}
                    onChange={(event) => {
                      const input = inputColumns.find((column) => column.id === event.currentTarget.value);
                      if (!input) return;
                      changeAggregate(index, {
                        ...aggregate,
                        inputColumnId: input.id,
                        name: uniqueName(aggregateNameFor(aggregate.operation, input), getUsedGroupNames(props.form, aggregate.outputColumnId)),
                        label: `${operationLabel(aggregate.operation)} of ${input.label}`,
                      });
                    }}
                    className="rounded border border-slate-300 bg-white px-2 py-1.5"
                  >
                    {inputColumns.map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type ?? 'unknown type'})</option>)}
                  </select>
                  {inputColumns.length === 0 ? <span role="status" className="text-xs text-amber-900">No compatible columns were returned for this summary.</span> : null}
                </label>
              ) : <span className="self-end text-xs text-slate-500">Counts every input row in the group.</span>}
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Output column name
                <input aria-label={`Summary output name ${index + 1}`} value={aggregate.name} onChange={(event) => changeAggregate(index, { ...aggregate, name: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
              </label>
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Output column label
                <input aria-label={`Summary output label ${index + 1}`} value={aggregate.label} onChange={(event) => changeAggregate(index, { ...aggregate, label: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
              </label>
              <button type="button" aria-label={`Remove summary ${index + 1}`} onClick={() => props.onChange({ ...props.form, aggregates: props.form.aggregates.filter((_, itemIndex) => itemIndex !== index) })} className="justify-self-start rounded px-2 py-1 text-sm font-medium text-red-700 hover:bg-red-50">Remove summary</button>
            </div>
          );
        })}
        <button
          type="button"
          data-testid="construction-reshape-add-summary"
          onClick={() => {
            const aggregate = makeAggregate('COUNT_ROWS', props.stage, usedNames);
            props.onChange({ ...props.form, aggregates: [...props.form.aggregates, aggregate] });
          }}
          className="justify-self-start rounded border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-white"
        >
          Add summary
        </button>
      </fieldset>
      {!outputNamesAreValid(groupOutputColumns(props.form, props.stage)) ? (
        <p role="status" className="text-sm text-amber-900">Give every output a unique column name using letters, numbers, or underscores, and add a label.</p>
      ) : null}
    </section>
  );
};

const groupAggregateKindFromInput = (value: string): GroupAggregateKind | undefined => {
  switch (value) {
    case 'COUNT_ROWS': return 'COUNT_ROWS';
    case 'COUNT_NON_NULL': return 'COUNT_NON_NULL';
    case 'COUNT_DISTINCT': return 'COUNT_DISTINCT';
    case 'SUM': return 'SUM';
    case 'MEAN': return 'MEAN';
    default: return undefined;
  }
};

const ExpandEditor = (props: {
  readonly form: ExpandForm;
  readonly stage: ReshapeStage;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly onChange: (form: ExpandForm) => void;
}) => {
  const columns = listColumnsFor(props.stage);
  const nameConflict = props.form.ordinal.kind === 'include'
    && props.form.outputName.trim().toLowerCase() === props.form.ordinal.outputName.trim().toLowerCase();
  const outputColumns = expandOutputsFor(props.form, props.stage);
  const outputNamesValid = outputColumns.length > 0 && outputNamesAreValid(outputColumns);
  const input = columns.find((column) => column.id === props.form.inputColumnId);
  const updateInput = (columnId: string) => {
    const column = columns.find((candidate) => candidate.id === columnId);
    if (!column) return;
    props.onChange({
      ...props.form,
      inputColumnId: column.id,
      outputName: `${normalizedName(column.name)}_item`,
      outputLabel: `${column.label} item`,
    });
  };

  return (
    <section aria-label="Expand repeated values" data-testid="construction-reshape-expand" className="grid gap-4 rounded-lg border border-slate-200 p-3">
      <header>
        <h4 className="text-sm font-semibold text-slate-900">Expand a list into rows</h4>
        <p className="mt-1 text-sm text-slate-600">Each list item becomes a row. Loom keeps the other columns from its parent row.</p>
      </header>
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      {columns.length === 0 ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Loom has not returned any list-valued columns for this stage.</p>
      ) : (
        <>
          <label className="grid gap-1 text-sm font-medium text-slate-700">
            Repeated field
            <select aria-label="Repeated field" value={props.form.inputColumnId} disabled={props.disabled || !props.supported} onChange={(event) => updateInput(event.currentTarget.value)} className="rounded border border-slate-300 bg-white px-2 py-1.5">
              {columns.map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type ?? 'unknown item type'})</option>)}
            </select>
          </label>
          <div className="grid gap-3 rounded border border-slate-200 p-3 sm:grid-cols-2">
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              Item column name
              <input aria-label="Expanded item name" value={props.form.outputName} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, outputName: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
            </label>
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              Item column label
              <input aria-label="Expanded item label" value={props.form.outputLabel} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, outputLabel: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
            </label>
          </div>
          <label className="flex items-start gap-2 rounded border border-slate-200 px-3 py-2 text-sm">
            <input
              type="checkbox"
              aria-label="Include item position"
              checked={props.form.ordinal.kind === 'include'}
              disabled={props.disabled || !props.supported}
              onChange={(event) => props.onChange({
                ...props.form,
                ordinal: event.currentTarget.checked
                  ? { kind: 'include', outputColumnId: createOpaqueId('expand-ordinal'), outputName: uniqueName(`${normalizedName(input?.name ?? 'item')}_position`, new Set(outputColumns.map((column) => column.name.toLowerCase()))), outputLabel: 'Item position, zero-based' }
                  : { kind: 'none' },
              })}
              className="mt-0.5"
            />
            <span><span className="block font-medium text-slate-800">Include item position</span><span className="block text-xs text-slate-500">Add a zero-based position column for each item.</span></span>
          </label>
          {props.form.ordinal.kind === 'include' ? (
            <div className="grid gap-3 rounded border border-slate-200 p-3 sm:grid-cols-2">
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Position column name
                <input aria-label="Position column name" value={props.form.ordinal.outputName} disabled={props.disabled || !props.supported} onChange={(event) => {
                  const ordinal = props.form.ordinal;
                  if (ordinal.kind === 'include') props.onChange({ ...props.form, ordinal: { ...ordinal, outputName: event.currentTarget.value } });
                }} className="rounded border border-slate-300 px-2 py-1.5" />
              </label>
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Position column label
                <input aria-label="Position column label" value={props.form.ordinal.outputLabel} disabled={props.disabled || !props.supported} onChange={(event) => {
                  const ordinal = props.form.ordinal;
                  if (ordinal.kind === 'include') props.onChange({ ...props.form, ordinal: { ...ordinal, outputLabel: event.currentTarget.value } });
                }} className="rounded border border-slate-300 px-2 py-1.5" />
              </label>
            </div>
          ) : null}
          <label className="grid gap-1 text-sm font-medium text-slate-700">
            When a list is empty
            <select aria-label="Empty list policy" value={props.form.emptyPolicy ?? ''} disabled={props.disabled || !props.supported} onChange={(event) => {
              const policy = emptyListPolicyFromInput(event.currentTarget.value);
              if (policy) props.onChange({ ...props.form, emptyPolicy: policy });
            }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
              <option value="">Choose what happens</option>
              <option value="ERROR">Stop with an error</option>
              <option value="EXCLUDE">Drop the original row</option>
              <option value="PRESERVE_PARENT">Keep the row with a missing item</option>
            </select>
            <span className="text-xs font-normal text-slate-500">This also applies when the repeated value is missing.</span>
          </label>
        </>
      )}
      {nameConflict || !outputNamesValid ? (
        <p role="status" className="text-sm text-amber-900">Choose unique output names and add a label for every new column.</p>
      ) : null}
    </section>
  );
};

const emptyListPolicyFromInput = (value: string): EmptyListPolicy | undefined => {
  switch (value) {
    case 'ERROR': return 'ERROR';
    case 'EXCLUDE': return 'EXCLUDE';
    case 'PRESERVE_PARENT': return 'PRESERVE_PARENT';
    default: return undefined;
  }
};

const pivotDuplicatePolicyFromInput = (value: string): PivotForm['duplicatePolicy'] | undefined => {
  switch (value) {
    case 'ERROR': return 'ERROR';
    case 'SUM': return 'SUM';
    case 'MIN': return 'MIN';
    case 'MAX': return 'MAX';
    default: return undefined;
  }
};

const pivotMissingPolicyFromInput = (value: string): PivotForm['missingCellPolicy'] | undefined => {
  switch (value) {
    case 'NULL': return 'NULL';
    case 'ERROR': return 'ERROR';
    default: return undefined;
  }
};

const pivotUnlistedPolicyFromInput = (value: string): PivotForm['unlistedCategoryPolicy'] | undefined => {
  switch (value) {
    case 'ERROR': return 'ERROR';
    case 'EXCLUDE_WITH_EVIDENCE': return 'EXCLUDE_WITH_EVIDENCE';
    default: return undefined;
  }
};

const PivotEditor = (props: {
  readonly form: PivotForm;
  readonly stage: ReshapeStage;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly discovery?: ConstructionReshapePivotDiscovery;
  readonly canDiscover: boolean;
  readonly onDiscover: () => void;
  readonly onChange: (form: PivotForm) => void;
}) => {
  const columns = scalarColumnsFor(props.stage);
  const discoveryMatches = pivotDiscoveryMatches(props.form, props.stage.id, props.discovery);
  const discoveryComplete = discoveryMatches && props.discovery.status === 'complete';
  const pairUnchanged = props.form.originalPair?.categoryColumnId === props.form.categoryColumnId
    && props.form.originalPair.valueColumnId === props.form.valueColumnId;
  const categoriesKnown = pairUnchanged || discoveryComplete;
  const categoriesAvailable = discoveryComplete ? props.discovery.categories : [];
  const listedKeys = new Set(categoriesAvailable.map((category) => scalarIdentity(category.key)));
  const existingNotListed = pairUnchanged
    ? props.form.categories.filter((category) => !listedKeys.has(scalarIdentity(category.key)))
    : [];
  const groupColumns = props.form.groupKeyIds.flatMap((id) => {
    const column = columns.find((candidate) => candidate.id === id);
    return column ? [column] : [];
  });
  const outputNamesValid = outputNamesAreValid(pivotOutputsFor(props.form, props.stage));
  const numericValue = isNumericColumn(columns.find((column) => column.id === props.form.valueColumnId) ?? { id: '', name: '', label: '', cardinality: 'optional_one' });

  const toggleGroup = (columnId: string, checked: boolean) => {
    const groupKeyIds = checked
      ? [...props.form.groupKeyIds, columnId]
      : props.form.groupKeyIds.filter((id) => id !== columnId);
    props.onChange({ ...props.form, groupKeyIds });
  };

  const updateCategory = (identity: string, update: Partial<PivotCategoryForm>) => {
    props.onChange({
      ...props.form,
      categories: props.form.categories.map((category) => scalarIdentity(category.key) === identity ? { ...category, ...update } : category),
    });
  };

  const toggleCategory = (available: ConstructionReshapePivotCategory, checked: boolean) => {
    const identity = scalarIdentity(available.key);
    const existing = props.form.categories.find((category) => scalarIdentity(category.key) === identity);
    const categories = checked
      ? existing
        ? props.form.categories.map((category) => scalarIdentity(category.key) === identity ? { ...category } : category)
        : [...props.form.categories, {
            key: available.key,
            outputColumnId: createOpaqueId('pivot-column'),
            name: uniqueName(available.suggestedName ?? normalizedName(scalarLabel(available.key)), new Set([
              ...props.stage.columns.map((column) => column.name.toLowerCase()),
              ...props.form.categories.map((category) => category.name.toLowerCase()),
            ])),
            label: available.suggestedLabel ?? available.label,
          }]
      : props.form.categories.filter((category) => scalarIdentity(category.key) !== identity);
    props.onChange({
      ...props.form,
      categories,
      categoriesPair: { categoryColumnId: props.form.categoryColumnId, valueColumnId: props.form.valueColumnId },
    });
  };

  return (
    <section aria-label="Pivot categories into columns" data-testid="construction-reshape-pivot" className="grid gap-4 rounded-lg border border-slate-200 p-3">
      <header>
        <h4 className="text-sm font-semibold text-slate-900">Turn categories into columns</h4>
        <p className="mt-1 text-sm text-slate-600">Each group becomes a row. Each chosen category becomes a value column.</p>
      </header>
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      {columns.length === 0 ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Loom has not returned scalar fields for this stage.</p>
      ) : (
        <>
          <fieldset className="grid gap-2 rounded border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
            <legend className="px-1 text-sm font-semibold text-slate-800">Keep one row per</legend>
            {columns.map((column) => (
              <label key={column.id} className="flex items-center gap-2 text-sm text-slate-700">
                <input
                  type="checkbox"
                  aria-label={`Pivot group ${column.label}`}
                  checked={props.form.groupKeyIds.includes(column.id)}
                  disabled={props.disabled || !props.supported || column.id === props.form.categoryColumnId || column.id === props.form.valueColumnId}
                  onChange={(event) => toggleGroup(column.id, event.currentTarget.checked)}
                />
                {column.label}
              </label>
            ))}
          </fieldset>
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              Category field
              <select aria-label="Pivot category field" value={props.form.categoryColumnId} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, categoryColumnId: event.currentTarget.value, categories: [], categoriesPair: undefined })} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                {columns.filter((column) => !props.form.groupKeyIds.includes(column.id)).map((column) => <option key={column.id} value={column.id}>{column.label}</option>)}
              </select>
            </label>
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              Values field
              <select aria-label="Pivot values field" value={props.form.valueColumnId} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, valueColumnId: event.currentTarget.value, categories: [], categoriesPair: undefined })} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                {columns.filter((column) => !props.form.groupKeyIds.includes(column.id) && column.id !== props.form.categoryColumnId).map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type ?? 'unknown type'})</option>)}
              </select>
            </label>
          </div>
          <button
            type="button"
            disabled={props.disabled || !props.supported || !props.canDiscover || props.form.categoryColumnId === '' || props.form.valueColumnId === ''}
            onClick={props.onDiscover}
            className="justify-self-start rounded border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:text-slate-400"
          >
            Find category values
          </button>
          {!props.canDiscover && !categoriesKnown ? <p role="status" className="text-sm text-amber-900">Category discovery for this stage is not connected yet. Existing pivots can still be edited using their saved categories.</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'loading' ? <p role="status" className="text-sm text-slate-600">Finding category values…</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'failed' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason}</p> : null}
          {props.discovery && !discoveryMatches ? <p role="status" className="text-sm text-amber-900">The available category list belongs to different fields. Find values for this category and values pair before applying.</p> : null}
          {!categoriesKnown ? <p role="status" className="text-sm text-amber-900">Find category values for the selected fields before applying this pivot.</p> : null}
          {categoriesKnown ? (
            <fieldset className="grid gap-3 rounded border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
              <legend className="px-1 text-sm font-semibold text-slate-800">Category columns</legend>
              {categoriesAvailable.map((available) => {
                const identity = scalarIdentity(available.key);
                const current = props.form.categories.find((category) => scalarIdentity(category.key) === identity);
                return (
                  <div key={identity} className="grid gap-2 rounded bg-slate-50 p-2">
                    <label className="flex items-center gap-2 text-sm font-medium text-slate-800">
                      <input type="checkbox" aria-label={`Include category ${available.label}`} checked={Boolean(current)} disabled={props.disabled || !props.supported} onChange={(event) => toggleCategory(available, event.currentTarget.checked)} />
                      {available.label}
                    </label>
                    {current ? <PivotCategoryOutput category={current} disabled={props.disabled || !props.supported} onChange={(update) => updateCategory(identity, update)} /> : null}
                  </div>
                );
              })}
              {existingNotListed.map((category) => {
                const identity = scalarIdentity(category.key);
                return (
                  <div key={identity} className="grid gap-2 rounded border border-amber-200 bg-amber-50 p-2">
                    <label className="flex items-center gap-2 text-sm font-medium text-slate-800">
                      <input type="checkbox" aria-label={`Keep saved category ${scalarLabel(category.key)}`} checked disabled={props.disabled || !props.supported} onChange={(event) => { if (!event.currentTarget.checked) toggleCategory({ key: category.key, label: scalarLabel(category.key) }, false); }} />
                      {scalarLabel(category.key)} <span className="font-normal text-amber-900">Not found in the latest category list</span>
                    </label>
                    <PivotCategoryOutput category={category} disabled={props.disabled || !props.supported} onChange={(update) => updateCategory(identity, update)} />
                  </div>
                );
              })}
              {categoriesAvailable.length === 0 && existingNotListed.length === 0 ? <p role="status" className="text-sm text-slate-600">No categories were found for this pair.</p> : null}
            </fieldset>
          ) : null}
          <div className="grid gap-3 sm:grid-cols-3">
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              If a group has duplicate values
              <select aria-label="Pivot duplicate policy" value={props.form.duplicatePolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotDuplicatePolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, duplicatePolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                <option value="ERROR">Show an error</option>
                <option value="SUM" disabled={!numericValue}>Add them together</option>
                <option value="MIN">Keep the smallest</option>
                <option value="MAX">Keep the largest</option>
              </select>
            </label>
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              If a group has no value
              <select aria-label="Pivot missing cell policy" value={props.form.missingCellPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotMissingPolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, missingCellPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                <option value="NULL">Leave the new cell empty</option>
                <option value="ERROR">Show an error</option>
              </select>
            </label>
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              If a new category appears
              <select aria-label="Pivot unlisted category policy" value={props.form.unlistedCategoryPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotUnlistedPolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, unlistedCategoryPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                <option value="ERROR">Show an error</option>
                <option value="EXCLUDE_WITH_EVIDENCE">Skip it and report it</option>
              </select>
            </label>
          </div>
        </>
      )}
      {!outputNamesValid ? <p role="status" className="text-sm text-amber-900">Give each output a unique column name using letters, numbers, or underscores, and add a label.</p> : null}
      {props.form.groupKeyIds.length === 0 ? <p role="status" className="text-sm text-amber-900">Choose at least one group field for this pivot.</p> : null}
    </section>
  );
};

const PivotCategoryOutput = (props: {
  readonly category: PivotCategoryForm;
  readonly disabled: boolean;
  readonly onChange: (update: Partial<PivotCategoryForm>) => void;
}) => (
  <div className="grid gap-2 sm:grid-cols-2">
    <label className="grid gap-1 text-xs font-medium text-slate-600">
      New column name
      <input aria-label={`Pivot output name ${scalarLabel(props.category.key)}`} value={props.category.name} disabled={props.disabled} onChange={(event) => props.onChange({ name: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5 text-sm" />
    </label>
    <label className="grid gap-1 text-xs font-medium text-slate-600">
      New column label
      <input aria-label={`Pivot output label ${scalarLabel(props.category.key)}`} value={props.category.label} disabled={props.disabled} onChange={(event) => props.onChange({ label: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5 text-sm" />
    </label>
  </div>
);

const UnpivotEditor = (props: {
  readonly form: UnpivotForm;
  readonly stage: ReshapeStage;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly onChange: (form: UnpivotForm) => void;
}) => {
  const inputIDs = new Set(props.form.inputs.map((input) => input.columnId));
  const outputNamesValid = outputNamesAreValid(unpivotOutputsFor(props.form, props.stage));
  const updateInputKey = (columnId: string, key: ConstructionTableScalar) => props.onChange({
    ...props.form,
    inputs: props.form.inputs.map((input) => input.columnId === columnId ? { ...input, key } : input),
  });
  const toggleInput = (column: ReshapeColumn, checked: boolean) => {
    const inputs = checked
      ? [...props.form.inputs, { columnId: column.id, key: defaultKeyForColumn(column) }]
      : props.form.inputs.filter((input) => input.columnId !== column.id);
    props.onChange({ ...props.form, inputs });
  };

  return (
    <section aria-label="Turn columns into rows" data-testid="construction-reshape-unpivot" className="grid gap-4 rounded-lg border border-slate-200 p-3">
      <header>
        <h4 className="text-sm font-semibold text-slate-900">Turn columns into rows</h4>
        <p className="mt-1 text-sm text-slate-600">Selected columns become values in one column. Their keys identify which source column held each value.</p>
      </header>
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      <fieldset className="grid gap-3 rounded border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
        <legend className="px-1 text-sm font-semibold text-slate-800">Columns to turn into rows</legend>
        {scalarColumnsFor(props.stage).map((column) => {
          const input = props.form.inputs.find((candidate) => candidate.columnId === column.id);
          return (
            <div key={column.id} className="grid gap-2 rounded bg-slate-50 p-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
              <label className="flex items-center gap-2 text-sm font-medium text-slate-800">
                <input type="checkbox" aria-label={`Unpivot ${column.label}`} checked={Boolean(input)} disabled={props.disabled || !props.supported} onChange={(event) => toggleInput(column, event.currentTarget.checked)} />
                {column.label}
              </label>
              {input ? <ScalarKeyEditor input={input} disabled={props.disabled || !props.supported} onChange={(key) => updateInputKey(column.id, key)} /> : null}
            </div>
          );
        })}
      </fieldset>
      <div className="grid gap-3 sm:grid-cols-2">
        <fieldset className="grid gap-2 rounded border border-slate-200 p-3">
          <legend className="px-1 text-sm font-semibold text-slate-800">New key column</legend>
          <label className="grid gap-1 text-sm font-medium text-slate-700">Column name<input aria-label="Unpivot key output name" value={props.form.keyOutputName} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, keyOutputName: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" /></label>
          <label className="grid gap-1 text-sm font-medium text-slate-700">Column label<input aria-label="Unpivot key output label" value={props.form.keyOutputLabel} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, keyOutputLabel: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" /></label>
        </fieldset>
        <fieldset className="grid gap-2 rounded border border-slate-200 p-3">
          <legend className="px-1 text-sm font-semibold text-slate-800">New value column</legend>
          <label className="grid gap-1 text-sm font-medium text-slate-700">Column name<input aria-label="Unpivot value output name" value={props.form.valueOutputName} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, valueOutputName: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" /></label>
          <label className="grid gap-1 text-sm font-medium text-slate-700">Column label<input aria-label="Unpivot value output label" value={props.form.valueOutputLabel} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, valueOutputLabel: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" /></label>
        </fieldset>
      </div>
      <label className="grid gap-1 text-sm font-medium text-slate-700">
        If a selected value is missing
        <select aria-label="Unpivot null row policy" value={props.form.nullRowPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = event.currentTarget.value; if (policy === 'DROP' || policy === 'PRESERVE') props.onChange({ ...props.form, nullRowPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
          <option value="DROP">Leave that row out</option>
          <option value="PRESERVE">Keep the row with a missing value</option>
        </select>
      </label>
      {props.form.inputs.length === 0 ? <p role="status" className="text-sm text-amber-900">Choose at least one input column.</p> : null}
      {!outputNamesValid ? <p role="status" className="text-sm text-amber-900">Give every output a unique column name using letters, numbers, or underscores, and add a label.</p> : null}
    </section>
  );
};

const ScalarKeyEditor = (props: {
  readonly input: UnpivotInputForm;
  readonly disabled: boolean;
  readonly onChange: (key: ConstructionTableScalar) => void;
}) => {
  const value = scalarLabel(props.input.key);
  return (
    <div className="grid gap-2 sm:grid-cols-[minmax(0,8rem)_minmax(0,1fr)]">
      <label className="grid gap-1 text-xs font-medium text-slate-600">
        Key type
        <select aria-label={`Unpivot key type ${props.input.columnId}`} value={props.input.key.kind} disabled={props.disabled} onChange={(event) => {
          const kind = scalarKindFromInput(event.currentTarget.value);
          if (kind) props.onChange(convertScalarKind(kind, props.input.key));
        }} className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm">
          <option value="STRING">Text</option><option value="INTEGER">Whole number</option><option value="DECIMAL">Decimal number</option><option value="BOOLEAN">True or false</option><option value="NULL">Null (not allowed as a key)</option><option value="MISSING">Missing (not allowed as a key)</option>
        </select>
      </label>
      {props.input.key.kind === 'BOOLEAN' ? (
        <label className="grid gap-1 text-xs font-medium text-slate-600">
          Key value
          <select aria-label={`Unpivot key value ${props.input.columnId}`} value={value} disabled={props.disabled} onChange={(event) => { const next = scalarFromText('BOOLEAN', event.currentTarget.value); if (next) props.onChange(next); }} className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"><option value="true">true</option><option value="false">false</option></select>
        </label>
      ) : props.input.key.kind === 'NULL' || props.input.key.kind === 'MISSING' ? (
        <p role="status" className="self-end text-xs text-amber-900">A null or missing key is not allowed. Choose a concrete key type.</p>
      ) : (
        <label className="grid gap-1 text-xs font-medium text-slate-600">
          Key value
          <input aria-label={`Unpivot key value ${props.input.columnId}`} value={value} disabled={props.disabled} onChange={(event) => { const next = scalarFromText(props.input.key.kind, event.currentTarget.value); if (next) props.onChange(next); }} className="rounded border border-slate-300 px-2 py-1.5 text-sm" />
        </label>
      )}
    </div>
  );
};
