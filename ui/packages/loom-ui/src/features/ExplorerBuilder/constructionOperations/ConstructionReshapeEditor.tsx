import React, { useEffect, useId, useRef, useState } from 'react';
import type { LoomClient } from '../../../api';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionStageDescriptor,
  ConstructionTableScalar,
  ConstructionStep,
  ExplorerBuilderCatalog,
} from '../../../types';
import { constructionSchema } from '../../../types';
import { RelatedExpandEditor } from './RelatedExpandEditor';
import { CodedPivotEditor } from './CodedPivotEditor';
import { relationshipLabel } from '../constructionWorkspace/routeDisplay';

type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'groupSource' | 'pivotSources'
>;

type ReshapeOperationKind = 'PIVOT' | 'CODED_PIVOT' | 'UNPIVOT' | 'GROUP' | 'CODED_GROUP' | 'EXPAND' | 'RELATED_EXPAND';
type GroupAggregateKind = 'COUNT_ROWS' | 'COUNT_NON_NULL' | 'COUNT_DISTINCT' | 'SUM' | 'MEAN';
type EmptyListPolicy = 'ERROR' | 'EXCLUDE' | 'PRESERVE_PARENT';

export interface ConstructionReshapePivotCategory {
  readonly key: ConstructionTableScalar;
  readonly label: string;
  readonly suggestedName?: string;
  readonly suggestedLabel?: string;
}

export interface ConstructionReshapePivotDiscoveryRequest {
  readonly groupKeyIds?: string[];
  readonly pivotStepId?: string;
  readonly pivotSources?: ConstructionProposalRequest['pivotSources'];
  readonly candidateConstruction?: Construction;
  readonly stageId: string;
  readonly categoryColumnId: string;
  readonly valueColumnId: string;
}

export type ConstructionReshapePivotDiscovery = ConstructionReshapePivotDiscoveryRequest & (
  | { readonly status: 'loading' }
  | { readonly status: 'failed'; readonly reason: string }
  | { readonly status: 'limit-exceeded'; readonly limit: number; readonly reason: string }
  | { readonly status: 'missing-unsupported'; readonly reason: string }
  | { readonly status: 'complete'; readonly categories: ReadonlyArray<ConstructionReshapePivotCategory> }
);

type PivotOperation = Extract<ConstructionStep['operation'], { readonly kind: 'PIVOT' }>;
type UnpivotOperation = Extract<ConstructionStep['operation'], { readonly kind: 'UNPIVOT' }>;

type ReshapeColumn = ConstructionStageDescriptor['columns'][number];
type ReshapeStage = ConstructionStageDescriptor;
type ReshapeCapabilities = ConstructionCapabilitiesResponse;
type SourceInputChoice = NonNullable<ReshapeCapabilities['sourceInput']>['choices'][number];
type StageCapability = { readonly supported: boolean; readonly reason: string; readonly reasonCode?: string };

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
  readonly pivotSources?: ConstructionProposalRequest['pivotSources'];
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
  readonly missingKeyPolicy: 'GROUP' | 'EXCLUDE' | 'ERROR';
  readonly keys: ReadonlyArray<GroupKey>;
  readonly aggregates: ReadonlyArray<GroupAggregate>;
};

type CodedGroupForm = {
  readonly kind: 'coded-group';
  readonly stepId: string;
  readonly codingPath: string;
  readonly missingKeyPolicy: 'GROUP' | 'EXCLUDE' | 'ERROR';
  readonly outputs: ConstructionStep['outputs'];
};

type SourceGroupForm = {
  readonly kind: 'source-group';
  readonly stepId: string;
  readonly choiceId: string;
  readonly inputColumnId: string;
  readonly missingKeyPolicy: 'GROUP' | 'EXCLUDE' | 'ERROR';
  readonly keyOutput: ConstructionStep['outputs'][number];
  readonly countOutput: ConstructionStep['outputs'][number];
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
  | { readonly kind: 'related-expand' }
  | { readonly kind: 'coded-pivot' }
  | GroupForm
  | SourceGroupForm
  | CodedGroupForm
  | ExpandForm
  | PivotForm
  | UnpivotForm
  | { readonly kind: 'unsupported'; readonly message: string };

export type ReshapeEntryKind = 'choose' | 'group' | 'source-group' | 'coded-group' | 'pivot' | 'coded-pivot' | 'categories' | 'related-expand';

const reshapeFormLabels = {
  group: 'Group records',
  'source-group': 'Group by source field',
  'coded-group': 'Group by coded value',
  expand: 'Expand repeated values',
  'related-expand': 'Related records',
  'coded-pivot': 'Coded values as columns',
  pivot: 'Categories to columns',
  unpivot: 'Turn columns into rows',
} satisfies Record<Exclude<ReshapeForm['kind'], 'choose' | 'unsupported'>, string>;

export interface ConstructionReshapeEditorProps {
  readonly construction: Construction;
  readonly capabilities: ReshapeCapabilities;
  readonly editingStep?: ConstructionReshapeStep;
  readonly initialKind?: ReshapeEntryKind;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly pivotDiscovery?: ConstructionReshapePivotDiscovery;
  readonly onDiscoverCategories?: (request: ConstructionReshapePivotDiscoveryRequest) => void;
  readonly onAddCodedValues?: () => void;
  readonly disabled: boolean;
  readonly onCandidateChange: (intent: CandidateIntent | undefined) => void;
  readonly onEditStep: (stepId: string) => void;
  readonly relatedExpandContext?: {
    readonly project: string;
    readonly explorerId: string;
    readonly authResourcePath?: string;
    readonly snapshotToken: string;
    readonly outputId: string;
    readonly catalog: ExplorerBuilderCatalog;
  };
  readonly codedPivotContext?: {
    readonly client: Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>;
    readonly project: string;
    readonly explorerId: string;
    readonly authResourcePath?: string;
    readonly snapshotToken: string;
    readonly outputId: string;
    readonly rowRoot: string;
  };
}

export const CONSTRUCTION_RESHAPE_EDITABLE_KINDS = ['PIVOT', 'CODED_PIVOT', 'UNPIVOT', 'GROUP', 'CODED_GROUP', 'EXPAND', 'RELATED_EXPAND'] as const;

const createOpaqueId = (prefix: string): string => {
  const randomPart = globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2);
  return `${prefix}_${randomPart}`;
};

const capabilityFor = (
  stage: ReshapeStage,
  kind: ReshapeOperationKind,
): StageCapability => {
  const capability = stage.capabilities.find((candidate) => candidate.kind === kind);
  if (!capability) {
    return {
      supported: false,
      reason: `Loom has not returned ${reshapeKindLabel(kind)} support for this stage.`,
    };
  }
  const reason = kind === 'PIVOT' && capability.reasonCode === 'INSUFFICIENT_SCALAR_COLUMNS'
    ? 'No executable combination of row, category, and value fields is available at this stage.'
    : capability.reason ?? 'Loom does not support this operation at the selected stage.';
  return capability.supported
    ? { supported: true, reason: '', reasonCode: capability.reasonCode }
    : {
      supported: false,
      reason,
      reasonCode: capability.reasonCode,
    };
};

const isReshapeOperationKind = (kind: string): kind is ReshapeOperationKind =>
  kind === 'PIVOT' || kind === 'CODED_PIVOT' || kind === 'UNPIVOT' || kind === 'GROUP' || kind === 'CODED_GROUP' || kind === 'EXPAND' || kind === 'RELATED_EXPAND';

const capabilityForStep = (stage: ReshapeStage, kind: string) =>
  isReshapeOperationKind(kind)
    ? capabilityFor(stage, kind)
    : { supported: false, reason: 'This operation is not edited in the reshape panel.' };

const reshapeKindLabel = (kind: ReshapeOperationKind): string => {
  switch (kind) {
    case 'PIVOT': return 'pivot';
    case 'CODED_PIVOT': return 'coded pivot';
    case 'UNPIVOT': return 'unpivot';
    case 'GROUP': return 'group summary';
    case 'CODED_GROUP': return 'coded value grouping';
    case 'EXPAND': return 'list expansion';
    case 'RELATED_EXPAND': return 'related record expansion';
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

const codingPathLabel = (path: string): string => path.split('.').map((segment) => {
  const words = segment.replaceAll('[]', '').replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/^_+/, '').trim();
  return words ? words[0].toUpperCase() + words.slice(1) : '';
}).filter(Boolean).join(' → ');

const normalizedName = (value: string): string => {
  const normalized = value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'column';
  return /^[0-9]/.test(normalized) ? `column_${normalized}` : normalized;
};

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
    missingKeyPolicy: form.missingKeyPolicy,
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

const pivotSelectionIdentity = (form: PivotForm, stageId: string): string => JSON.stringify([
  form.stepId,
  stageId,
  form.categoryColumnId,
  form.valueColumnId,
  form.pivotSources,
]);

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

const createPivotCategoryForm = (
  available: ConstructionReshapePivotCategory,
  usedNames: ReadonlySet<string>,
): PivotCategoryForm => ({
  key: available.key,
  outputColumnId: createOpaqueId('pivot-column'),
  name: uniqueName(available.suggestedName ?? normalizedName(scalarLabel(available.key)), usedNames),
  label: available.suggestedLabel ?? available.label,
});

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
  readonly groupSource?: CandidateIntent['groupSource'];
  readonly pivotSources?: CandidateIntent['pivotSources'];
}): CandidateEvaluation => {
  const { construction, editingStep, step, groupSource, pivotSources } = args;
  if (editingStep && !construction.steps.some((candidate) => candidate.id === editingStep.id)) return { kind: 'incomplete' };
  const draft: unknown = {
    version: construction.version,
    steps: editingStep
      ? construction.steps.map((candidate) => candidate.id === editingStep.id ? step : candidate)
      : [...construction.steps, step],
    ...(construction.sourceProjections ? { sourceProjections: construction.sourceProjections } : {}),
  };
  const parsed = constructionSchema.safeParse(draft);
  if (!parsed.success) return { kind: 'schema-pending' };
  return {
    kind: 'ready',
    intent: {
      candidateConstruction: parsed.data,
      changedStepId: step.id,
      ...(groupSource ? { groupSource } : {}),
      ...(pivotSources !== undefined ? { pivotSources } : {}),
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

const sourceGroupProjection = (construction: Construction, step: ConstructionReshapeStep) => {
  if (step.operation.kind !== 'GROUP') return undefined;
  const key = step.operation.group.keys?.[0];
  return construction.sourceProjections?.find((projection) => projection.columnId === key?.inputColumnId);
};

const initialSourceGroupForm = (
  construction: Construction,
  choices: ReadonlyArray<SourceInputChoice>,
  editingStep?: ConstructionReshapeStep,
): SourceGroupForm | undefined => {
  const projection = editingStep ? sourceGroupProjection(construction, editingStep) : undefined;
  const choice = projection
    ? choices.find((candidate) => candidate.occurrenceId === projection.occurrenceId && candidate.fieldPath === projection.fieldPath)
    : choices.find((candidate) => candidate.isPopulated);
  if (!choice) return undefined;
  const keyOutput = editingStep?.outputs[0] ?? {
    id: createOpaqueId('group-key'),
    name: normalizedName(choice.fieldPath.split('.').at(-1) ?? choice.label),
    label: choice.label,
    type: choice.logicalType,
  };
  const countOutput = editingStep?.outputs[1] ?? {
    id: createOpaqueId('group-count'), name: 'source_records', label: 'Source records', type: 'integer',
  };
  return {
    kind: 'source-group',
    stepId: editingStep?.id ?? createOpaqueId('group'),
    choiceId: choice.choiceId,
    inputColumnId: projection?.columnId ?? createOpaqueId('group-source'),
    missingKeyPolicy: editingStep?.operation.kind === 'GROUP'
      ? editingStep.operation.group.missingKeyPolicy ?? 'GROUP'
      : 'GROUP',
    keyOutput,
    countOutput,
  };
};

const sourceGroupStepFromForm = (
  form: SourceGroupForm,
  stage: ReshapeStage,
  choice: SourceInputChoice,
): ConstructionReshapeStep | undefined => {
  const outputs = [form.keyOutput, form.countOutput];
  if (stage.operation || !outputNamesAreValid(outputs)) return undefined;
  return {
    id: form.stepId,
    inputs: [stepInputFor(stage)],
    operation: {
      kind: 'GROUP',
      group: {
        constructionId: form.stepId,
        missingKeyPolicy: form.missingKeyPolicy,
        keys: [{ inputColumnId: form.inputColumnId, outputColumnId: form.keyOutput.id }],
        aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: form.countOutput.id }],
      },
    },
    outputs: [
      { ...form.keyOutput, type: choice.logicalType },
      form.countOutput,
    ],
  };
};

const initialCodedGroupForm = (stage: ReshapeStage, editingStep?: ConstructionReshapeStep): CodedGroupForm => {
  if (editingStep?.operation.kind === 'CODED_GROUP') {
    return {
      kind: 'coded-group',
      stepId: editingStep.id,
      codingPath: editingStep.operation.codedGroup.source.codingPath,
      missingKeyPolicy: editingStep.operation.codedGroup.missingKeyPolicy,
      outputs: editingStep.outputs,
    };
  }
  return {
    kind: 'coded-group',
    stepId: createOpaqueId('coded-group'),
    codingPath: stage.codedGroupChoices?.[0]?.codingPath ?? '',
    missingKeyPolicy: 'GROUP',
    outputs: [
      { id: createOpaqueId('coded-system'), name: 'code_system', label: 'Code system', type: 'string', nullable: true },
      { id: createOpaqueId('coded-version'), name: 'code_version', label: 'Code version', type: 'string', nullable: true },
      { id: createOpaqueId('coded-code'), name: 'code', label: 'Code', type: 'string', nullable: true },
      { id: createOpaqueId('coded-count'), name: 'source_records', label: 'Source records', type: 'integer' },
    ],
  };
};

const codedGroupStepFromForm = (
  stage: ReshapeStage,
  form: CodedGroupForm,
  editingStep?: ConstructionReshapeStep,
): ConstructionReshapeStep | undefined => {
  const choice = stage.codedGroupChoices?.find((candidate) => candidate.codingPath === form.codingPath);
  if (!choice || form.outputs.length !== 4 || !outputNamesAreValid(form.outputs)) return undefined;
  const [system, version, code, count] = form.outputs;
  return {
    id: editingStep?.id ?? form.stepId,
    inputs: [stepInputFor(stage)],
    operation: {
      kind: 'CODED_GROUP',
      codedGroup: {
        constructionId: editingStep?.id ?? form.stepId,
        choiceId: choice.choiceId,
        source: {
          occurrenceId: choice.occurrenceId,
          resourceType: choice.resourceType,
          codingPath: choice.codingPath,
          fhirType: 'Coding',
          cardinality: 'MANY',
          shape: 'ARRAY',
          route: [],
        },
        missingKeyPolicy: form.missingKeyPolicy,
        systemOutputColumnId: system.id,
        versionOutputColumnId: version.id,
        codeOutputColumnId: code.id,
        distinctSourceCountOutputColumnId: count.id,
      },
    },
    outputs: form.outputs,
  };
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
  if (form.duplicatePolicy !== 'ERROR' && !isNumericColumn(valueColumn)) return undefined;
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
    return { kind: 'group', stepId: editingStep.id, missingKeyPolicy: operation.missingKeyPolicy ?? 'GROUP', keys, aggregates };
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
  return { kind: 'group', stepId: createOpaqueId('group'), missingKeyPolicy: 'GROUP', keys, aggregates: [aggregate] };
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
    emptyPolicy: 'PRESERVE_PARENT',
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
  const value = numericColumnsFor(stage).find((column) => !groups.includes(column.id));
  return {
    kind: 'pivot',
    stepId: createOpaqueId('pivot'),
    groupKeyIds: groups,
    categoryColumnId: '',
    valueColumnId: value?.id ?? '',
    categories: [],
    duplicatePolicy: 'ERROR',
    missingCellPolicy: 'NULL',
    unlistedCategoryPolicy: 'ERROR',
  };
};

const defaultKeyForColumn = (
  column: ReshapeColumn | undefined,
  inputs: ReadonlyArray<UnpivotInputForm> = [],
): ConstructionTableScalar => {
  const label = column?.label.trim() || column?.name || 'value';
  const used = new Set(inputs.flatMap((input) =>
    input.key.kind === 'STRING' ? [input.key.string.toLowerCase()] : [],
  ));
  let value = label;
  for (let suffix = 2; used.has(value.toLowerCase()); suffix += 1) {
    value = `${label} (${suffix})`;
  }
  return { kind: 'STRING', string: value };
};

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
  const initialInputs = selectedColumns.reduce<UnpivotInputForm[]>((inputs, columnId) => {
    const column = stage.columns.find((candidate) => candidate.id === columnId);
    if (column) inputs.push({ columnId, key: defaultKeyForColumn(column, inputs) });
    return inputs;
  }, []);
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
    nullRowPolicy: 'PRESERVE',
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
      const missing = group.missingKeyPolicy === 'EXCLUDE'
        ? ' Rows missing a key are excluded.'
        : group.missingKeyPolicy === 'ERROR'
          ? ' Missing keys stop this step.'
          : ' Absent and null keys share one missing group.';
      return keys.length > 0
        ? `One row per ${keys.join(', ')} with ${measures} ${measures === 1 ? 'summary' : 'summaries'}.${missing}`
        : `One summary row for the whole table with ${measures} ${measures === 1 ? 'summary' : 'summaries'}.`;
    }
    case 'CODED_GROUP': {
      const codingPath = step.operation.codedGroup.source.codingPath;
      return `One row per code in ${codingPath}, with the number of distinct source records.`;
    }
    case 'EXPAND': {
      const expand = step.operation.expand;
      const input = stage?.columns.find((column) => column.id === expand.inputColumnId);
      const policy = expand.emptyPolicy ?? 'EXCLUDE';
      return `Make one row per item in ${input?.label ?? input?.name ?? 'the selected list'}. Empty lists: ${emptyPolicyLabel(policy).toLowerCase()}.`;
    }
    case 'RELATED_EXPAND': {
      const expansion = step.operation.relatedExpand;
      const path = expansion.route.map(relationshipLabel).join(' → ');
      return `Make one row per distinct ${expansion.targetResourceType} via ${path}. Empty matches: ${emptyPolicyLabel(expansion.emptyPolicy).toLowerCase()}.`;
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
  construction: Construction,
  capabilities: ReshapeCapabilities,
  editingStep: ConstructionReshapeStep | undefined,
  stage: ReshapeStage,
  initialKind?: ReshapeEntryKind,
): ReshapeForm => {
  if (!editingStep) {
    if (initialKind === 'group') return initialGroupForm(stage, []);
    if (initialKind === 'source-group') return initialSourceGroupForm(construction, capabilities.sourceInput?.choices ?? [])
      ?? { kind: 'unsupported', message: 'No source field can group these rows.' };
    if (initialKind === 'coded-group') return initialCodedGroupForm(stage);
    if (initialKind === 'pivot') return initialPivotForm(stage, []);
    if (initialKind === 'coded-pivot') return { kind: 'coded-pivot' };
    if (initialKind === 'categories') {
      return capabilityFor(stage, 'CODED_PIVOT').supported ? { kind: 'coded-pivot' } : initialPivotForm(stage, []);
    }
    return { kind: initialKind ?? 'choose' };
  }
  if (sourceGroupProjection(construction, editingStep)) return initialSourceGroupForm(
    construction, capabilities.sourceInput?.choices ?? [], editingStep,
  ) ?? { kind: 'unsupported', message: 'The saved source field is no longer available for grouping.' };
  if (editingStep.operation.kind === 'GROUP') return initialGroupForm(stage, [], editingStep);
  if (editingStep.operation.kind === 'CODED_GROUP') return initialCodedGroupForm(stage, editingStep);
  if (editingStep.operation.kind === 'EXPAND') return initialExpandForm(stage, editingStep);
  if (editingStep.operation.kind === 'RELATED_EXPAND') return { kind: 'related-expand' };
  if (editingStep.operation.kind === 'PIVOT') {
    const form = initialPivotForm(stage, [], editingStep);
    const used = new Set([...form.groupKeyIds, form.categoryColumnId, form.valueColumnId]);
    const pivotSources = (capabilities.pivotSourceInput?.choices ?? []).flatMap((choice) =>
      choice.columnId && used.has(choice.columnId) ? [{ choiceId: choice.choiceId, columnId: choice.columnId }] : [],
    );
    return { ...form, ...(pivotSources.length ? { pivotSources } : {}) };
  }
  if (editingStep.operation.kind === 'CODED_PIVOT') return { kind: 'coded-pivot' };
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
  && discovery.valueColumnId === form.valueColumnId
  && JSON.stringify(discovery.pivotSources ?? []) === JSON.stringify(form.pivotSources ?? []),
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

const allDiscoveredPivotCategoriesSelected = (
  form: PivotForm,
  stageId: string,
  discovery: ConstructionReshapePivotDiscovery | undefined,
): boolean => {
  if (!pivotDiscoveryMatches(form, stageId, discovery) || discovery.status !== 'complete') return false;
  const selectedIdentities = new Set(form.categories.map((category) => scalarIdentity(category.key)));
  return discovery.categories.every((category) => selectedIdentities.has(scalarIdentity(category.key)));
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
  readonly capabilities: ReshapeCapabilities;
  readonly stage: ReshapeStage;
  readonly editingStep?: ConstructionReshapeStep;
  readonly pivotCategoriesKnown: boolean;
}): CandidateEvaluation => {
  const { form, construction, capabilities, stage, editingStep, pivotCategoriesKnown } = args;
  if (form.kind === 'source-group') {
    const choice = capabilities.sourceInput?.choices.find((candidate) => candidate.choiceId === form.choiceId);
    if (!capabilities.sourceInput?.supported || !choice?.isPopulated) return { kind: 'incomplete' };
    const step = sourceGroupStepFromForm(form, stage, choice);
    return step ? candidateIntentFor({
      construction, editingStep, step,
      groupSource: { rowChoiceId: choice.choiceId, columnId: form.inputColumnId },
    }) : { kind: 'incomplete' };
  }
  if (form.kind === 'group') {
    const step = groupStepFromForm({ stage, editingStep, form });
    return step ? candidateIntentFor({ construction, editingStep, step }) : { kind: 'incomplete' };
  }
  if (form.kind === 'coded-group') {
    const step = codedGroupStepFromForm(stage, form, editingStep);
    return step ? candidateIntentFor({ construction, editingStep, step }) : { kind: 'incomplete' };
  }
  if (form.kind === 'expand') {
    const step = expandStepFromForm({ stage, editingStep, form });
    return step ? candidateIntentFor({ construction, editingStep, step }) : { kind: 'incomplete' };
  }
  if (form.kind === 'pivot') {
    if (!pivotCategoriesKnown) return { kind: 'incomplete' };
    const step = pivotStepFromForm({ stage: pivotInputStage(stage, form, capabilities.pivotSourceInput?.choices ?? []), editingStep, form });
    return step ? candidateIntentFor({ construction, editingStep, step, pivotSources: form.pivotSources }) : { kind: 'incomplete' };
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
  const [form, setForm] = useState<ReshapeForm>(() => formForStep(construction, capabilities, editingStep, stage, props.initialKind));
  const [formContextKey, setFormContextKey] = useState(contextKey);
  const [contractUnavailable, setContractUnavailable] = useState(false);
  const automaticallySelectedPivotPairs = useRef(new Set<string>());
  const manuallySelectedPivotPairs = useRef(new Set<string>());
  const groupSupport = capabilityFor(stage, 'GROUP');
  const codedGroupSupport = capabilityFor(stage, 'CODED_GROUP');
  const expandSupport = capabilityFor(stage, 'EXPAND');
  const relatedExpandSupport = capabilityFor(stage, 'RELATED_EXPAND');
  const savedPivotSupport = capabilityFor(stage, 'PIVOT');
  const pivotSupport = capabilities.pivotSourceInput?.supported ? { supported: true, reason: '' } : savedPivotSupport;
  const codedPivotSupport = capabilityFor(stage, 'CODED_PIVOT');
  const unpivotSupport = capabilityFor(stage, 'UNPIVOT');
  const expandColumns = listColumnsFor(stage);
  const expandReason = expandColumns.length === 0
    ? 'Add a column with multiple values before expanding it.'
    : expandSupport.supported ? '' : expandSupport.reason;
  const newPivotSupport = props.onDiscoverCategories
    ? pivotSupport
    : { supported: false, reason: pivotSupport.supported ? 'Stage-scoped category discovery is not available yet.' : pivotSupport.reason };
  const pivotDiscovery = props.pivotDiscovery;

  useEffect(() => {
    const initialForm = formForStep(construction, capabilities, editingStep, capabilities.selectedStage, props.initialKind);
    setForm(initialForm);
    setFormContextKey(contextKey);
    setContractUnavailable(false);
    const initialCandidate = !editingStep && (
      initialForm.kind === 'coded-group' && codedGroupSupport.supported
      || initialForm.kind === 'source-group' && capabilities.sourceInput?.supported
    )
      ? candidateFor({
          form: initialForm,
          construction,
          capabilities,
          stage,
          editingStep,
          pivotCategoriesKnown: false,
        })
      : undefined;
    onCandidateChange(initialCandidate?.kind === 'ready' ? initialCandidate.intent : undefined);
  }, [contextKey]);

  const capabilityForForm = (next: ReshapeForm) => {
    switch (next.kind) {
      case 'group': return groupSupport;
      case 'source-group': return { supported: capabilities.sourceInput?.supported ?? false, reason: capabilities.sourceInput?.reason ?? 'No source field is available.' };
      case 'coded-group': return codedGroupSupport;
      case 'expand': return expandSupport;
      case 'related-expand': return relatedExpandSupport;
      case 'pivot': return pivotSupport;
      case 'coded-pivot': return codedPivotSupport;
      case 'unpivot': return unpivotSupport;
      default: return undefined;
    }
  };

  const evaluate = (next: ReshapeForm): CandidateEvaluation => {
    const support = capabilityForForm(next);
    if (!support?.supported) return { kind: 'incomplete' };
    const pivotCategoriesKnown = next.kind === 'pivot' && pivotCategoriesAreKnown(next, stage.id, pivotDiscovery);
    if (
      next.kind === 'pivot'
      && editingStep?.operation.kind !== 'PIVOT'
      && pivotCategoriesKnown
      && !allDiscoveredPivotCategoriesSelected(next, stage.id, pivotDiscovery)
    ) return { kind: 'incomplete' };
    return candidateFor({
      form: next,
      construction,
      capabilities,
      stage,
      editingStep,
      pivotCategoriesKnown,
    });
  };

  const updateForm = (next: ReshapeForm) => {
    if (
      form.kind === 'pivot'
      && next.kind === 'pivot'
      && (form.categoryColumnId !== next.categoryColumnId || form.valueColumnId !== next.valueColumnId)
    ) {
      automaticallySelectedPivotPairs.current.delete(pivotSelectionIdentity(form, stage.id));
    }
    setForm(next);
    const evaluation = evaluate(next);
    setContractUnavailable(evaluation.kind === 'schema-pending');
    onCandidateChange(evaluation.kind === 'ready' ? evaluation.intent : undefined);
  };

  const rememberManualPivotSelection = (pivot: PivotForm) => {
    if (pivot.categoryColumnId === '' || pivot.valueColumnId === '') return;
    manuallySelectedPivotPairs.current.add(pivotSelectionIdentity(pivot, stage.id));
  };

  useEffect(() => {
    if (
      formContextKey !== contextKey
      || editingStep !== undefined
      || form.kind !== 'pivot'
      || form.originalPair
      || !pivotDiscoveryMatches(form, stage.id, pivotDiscovery)
      || pivotDiscovery.status !== 'complete'
    ) return;

    const selectionIdentity = pivotSelectionIdentity(form, stage.id);
    if (
      automaticallySelectedPivotPairs.current.has(selectionIdentity)
      || manuallySelectedPivotPairs.current.has(selectionIdentity)
    ) return;

    automaticallySelectedPivotPairs.current.add(selectionIdentity);
    const usedNames = new Set([
      ...stage.columns.map((column) => column.name.toLowerCase()),
      ...form.categories.map((category) => category.name.toLowerCase()),
    ]);
    const categories = pivotDiscovery.categories.map((available) => {
      const category = createPivotCategoryForm(available, usedNames);
      usedNames.add(category.name.toLowerCase());
      return category;
    });
    updateForm({
      ...form,
      categories,
      categoriesPair: { categoryColumnId: form.categoryColumnId, valueColumnId: form.valueColumnId },
    });
  }, [form, formContextKey, contextKey, editingStep, pivotDiscovery, stage.id]);

  const savedSteps = construction.steps.filter((step) =>
    isReshapeOperationKind(step.operation.kind),
  );

  const requestPivotCategories = (pivot: PivotForm) => {
    if (!props.onDiscoverCategories || pivot.categoryColumnId === '' || pivot.valueColumnId === '') return;
    props.onDiscoverCategories({
      stageId: stage.id,
      ...(pivot.pivotSources?.length ? { pivotStepId: pivot.stepId, groupKeyIds: [...pivot.groupKeyIds], pivotSources: pivot.pivotSources, candidateConstruction: construction } : {}),
      categoryColumnId: pivot.categoryColumnId,
      valueColumnId: pivot.valueColumnId,
    });
  };

  return (
    <section aria-label="Reshape editor" data-testid="construction-reshape-editor" className="grid content-start gap-3">

      {props.onAddCodedValues && form.kind !== 'pivot' && form.kind !== 'coded-group' && form.kind !== 'source-group' ? (
        <div className="flex flex-wrap items-center gap-2 text-xs text-slate-600">
          <span>Need a coded-value column first?</span>
          <button type="button" onClick={props.onAddCodedValues} disabled={disabled} className="font-semibold text-blue-800 underline underline-offset-2 disabled:text-slate-400">
            Add coded-value column
          </button>
        </div>
      ) : null}

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
      {form.kind === 'coded-pivot' && !props.codedPivotContext ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Coded source discovery is unavailable for these starting records. Use category and value fields already in this table, or add a coded-value column first.</p>
      ) : null}
      {contractUnavailable ? (
        <p role="status" data-testid="construction-reshape-schema-unavailable" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">The construction schema did not accept this reshape, so Loom has not previewed it.</p>
      ) : null}

      {!editingStep && form.kind !== 'choose' && form.kind !== 'unsupported' ? (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm">
          <span className="font-semibold text-slate-900">{reshapeFormLabels[form.kind]}</span>
          <button type="button" disabled={disabled} onClick={() => updateForm({ kind: 'choose' })}
            className="font-medium text-blue-800 hover:underline disabled:opacity-50">Change row operation</button>
        </div>
      ) : null}

      {!editingStep && form.kind === 'choose' ? (
        <fieldset className="grid gap-2">
          <legend className="mb-1 text-sm font-semibold text-slate-800">Choose how rows and columns change</legend>
          <ReshapeChoice
            testId="construction-reshape-choice-group"
            title="Summarize into groups"
            rows="Count or summarize records by chosen fields."
            columns="Keep the group fields and add counts or summaries of selected fields."
            supported={groupSupport.supported}
            reason={groupSupport.reason}
            disabled={disabled}
            onChoose={() => updateForm(initialGroupForm(stage, props.selectedColumns ?? []))}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-coded-group"
            title="Group by coded value"
            rows="Count source records for each code."
            columns="Replace current columns with system, version, code, and source record count."
            supported={codedGroupSupport.supported && Boolean(stage.codedGroupChoices?.length)}
            reason={stage.operation && !codedGroupSupport.supported
              ? 'Available on starting records before other table changes.'
              : codedGroupSupport.reason || (stage.codedGroupChoices?.length ? '' : 'No Coding fields are available here.')}
            disabled={disabled}
            onChoose={() => updateForm(initialCodedGroupForm(stage))}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-source-group"
            title="Group by a source field"
            rows="Count records for each value of a field in the starting records."
            columns="Show the chosen field and its source record count."
            supported={Boolean(capabilities.sourceInput?.supported && capabilities.sourceInput.choices.some((choice) => choice.isPopulated))}
            reason={capabilities.sourceInput?.supported && capabilities.sourceInput.choices.some((choice) => choice.isPopulated)
              ? ''
              : capabilities.sourceInput?.reason ?? 'No source field with recorded values is available for grouping.'}
            disabled={disabled}
            onChoose={() => {
              const next = initialSourceGroupForm(construction, capabilities.sourceInput?.choices ?? []);
              if (next) updateForm(next);
            }}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-expand"
            title="Expand a repeated value"
            rows="Make one row for each value in a list."
            columns="Replace the list with its item; optionally add the item's position."
            supported={expandSupport.supported}
            reason={expandReason}
            disabled={disabled || expandColumns.length === 0}
            onChoose={() => updateForm(initialExpandForm(stage))}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-related-expand"
            title="Expand related records"
            rows="Make one row for each matching related record."
            columns="Keep current columns and add the related record ID."
            supported={relatedExpandSupport.supported && Boolean(props.relatedExpandContext)}
            reason={props.relatedExpandContext ? relatedExpandSupport.reason : 'Related path search is unavailable.'}
            disabled={disabled}
            onChoose={() => updateForm({ kind: 'related-expand' })}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-coded-pivot"
            title="Coded values as columns"
            rows="Keep one row per source record."
            columns="Choose coded values and use their paired values as cells."
            supported={codedPivotSupport.supported && Boolean(props.codedPivotContext)}
            reason={props.codedPivotContext ? codedPivotSupport.reason : 'Coded source discovery is unavailable.'}
            disabled={disabled}
            onChoose={() => updateForm({ kind: 'coded-pivot' })}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-pivot"
            title="Turn categories into columns"
            rows="Make one column for each category."
            columns="Add one column for each accepted category, filled from a selected value field."
            supported={newPivotSupport.supported}
            reason={newPivotSupport.reason}
            disabled={disabled}
            onChoose={() => updateForm(initialPivotForm(stage, props.selectedColumns ?? []))}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-unpivot"
            title="Turn columns into rows"
            rows="Make one row for each chosen column."
            columns="Replace those columns with a source-name column and a value column."
            supported={unpivotSupport.supported}
            reason={unpivotSupport.reason}
            disabled={disabled}
            onChoose={() => updateForm(initialUnpivotForm(stage, props.selectedColumns ?? []))}
          />
          {form.kind === 'choose' && !groupSupport.supported && !codedGroupSupport.supported && !expandSupport.supported && !relatedExpandSupport.supported && !pivotSupport.supported && !unpivotSupport.supported ? (
            <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">
              Loom has not confirmed a reshape this stage can run. The operation choices stay unavailable until it returns support.
            </p>
          ) : null}
        </fieldset>
      ) : null}

      {form.kind === 'related-expand' && props.relatedExpandContext ? (
        <RelatedExpandEditor
          key={`${id}:related-expand:${contextKey}`}
          {...props.relatedExpandContext}
          construction={construction}
          capabilities={capabilities}
          step={editingStep?.operation.kind === 'RELATED_EXPAND' ? editingStep as Extract<ConstructionStep, { readonly operation: { readonly kind: 'RELATED_EXPAND' } }> : undefined}
          disabled={disabled || !relatedExpandSupport.supported}
          onCandidateChange={onCandidateChange}
        />
      ) : null}

      {form.kind === 'coded-pivot' && props.codedPivotContext ? (
        <CodedPivotEditor
          key={`${id}:coded-pivot:${contextKey}`}
          {...props.codedPivotContext}
          construction={construction}
          editingStep={editingStep}
          disabled={disabled || !codedPivotSupport.supported}
          unavailableReason={!codedPivotSupport.supported ? codedPivotSupport.reason : undefined}
          onCandidateChange={onCandidateChange}
        />
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

      {form.kind === 'coded-group' ? (
        <CodedGroupEditor
          form={form}
          stage={stage}
          supported={codedGroupSupport.supported}
          reason={codedGroupSupport.reason}
          disabled={disabled}
          onChange={updateForm}
        />
      ) : null}

      {form.kind === 'source-group' ? (
        <SourceGroupEditor
          form={form}
          choices={capabilities.sourceInput?.choices ?? []}
          supported={capabilities.sourceInput?.supported ?? false}
          reason={capabilities.sourceInput?.reason ?? 'No source field is available for grouping.'}
          disabled={disabled}
          onChange={updateForm}
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
          stage={pivotInputStage(stage, form, capabilities.pivotSourceInput?.choices ?? [])}
          sourceChoices={capabilities.pivotSourceInput?.choices ?? []}
          supported={pivotSupport.supported}
          reason={pivotSupport.reason}
          disabled={disabled}
          discovery={pivotDiscovery}
          requireEveryDiscoveredCategory={editingStep?.operation.kind !== 'PIVOT'}
          canDiscover={Boolean(props.onDiscoverCategories)}
          onDiscover={() => requestPivotCategories(form)}
          onManualCategorySelection={rememberManualPivotSelection}
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
  readonly rows: string;
  readonly columns: string;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly onChoose: () => void;
}) => (
  <button
    type="button"
    data-testid={props.testId}
    aria-label={`${props.title}. ${props.rows} ${props.columns}${props.reason ? ` ${props.reason}` : ''}`}
    disabled={props.disabled || !props.supported}
    onClick={props.onChoose}
    className="grid gap-0.5 rounded-lg border border-slate-200 px-3 py-2 text-left enabled:hover:border-blue-400 enabled:hover:bg-blue-50 disabled:cursor-not-allowed disabled:bg-slate-50"
  >
    <span className="text-sm font-semibold text-slate-900">{props.title}</span>
    <span className="text-xs text-slate-600">{props.rows}</span>
    {!props.supported || props.reason ? <span className="text-xs text-amber-900">{props.reason}</span> : null}
  </button>
);

const SourceGroupEditor = (props: {
  readonly form: SourceGroupForm;
  readonly choices: ReadonlyArray<SourceInputChoice>;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly onChange: (form: SourceGroupForm) => void;
}) => (
  <section aria-label="Group by a source field" data-testid="construction-reshape-source-group" className="grid content-start gap-3 rounded-lg border border-slate-200 p-3">
    <header>
      <h4 className="m-0 text-sm font-semibold text-slate-900">One row per source field value</h4>
      <p className="mb-0 mt-1 text-sm text-slate-600">Choose a field in the starting records. Loom counts how many records have each value.</p>
    </header>
    {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
    <label className="grid gap-1 text-sm font-medium text-slate-800">
      Field to group by
      <select
        data-testid="construction-source-group-field"
        value={props.form.choiceId}
        disabled={props.disabled || !props.supported}
        onChange={(event) => {
          const choice = props.choices.find((candidate) => candidate.choiceId === event.currentTarget.value);
          if (!choice) return;
          props.onChange({
            ...props.form,
            choiceId: choice.choiceId,
            keyOutput: {
              ...props.form.keyOutput,
              name: normalizedName(choice.fieldPath.split('.').at(-1) ?? choice.label),
              label: choice.label,
              type: choice.logicalType,
            },
          });
        }}
        className="rounded border border-slate-300 bg-white px-2 py-2"
      >
        {props.choices.map((choice) => (
          <option key={choice.choiceId} value={choice.choiceId} disabled={!choice.isPopulated}>{choice.label}{choice.isPopulated ? '' : ' (no recorded values)'}</option>
        ))}
      </select>
    </label>
    <p className="m-0 text-xs text-slate-600">Result columns: {props.form.keyOutput.label} and Source records.</p>
    <details className="rounded border border-slate-200 p-2 text-sm">
      <summary className="cursor-pointer font-medium text-slate-800">Advanced: missing values</summary>
      <label className="mt-2 grid gap-1 text-slate-700">
        When a source record has no value for this field
        <select
          data-testid="construction-source-group-missing"
          value={props.form.missingKeyPolicy}
          disabled={props.disabled || !props.supported}
          onChange={(event) => {
            const value = event.currentTarget.value;
            if (value === 'GROUP' || value === 'EXCLUDE' || value === 'ERROR') {
              props.onChange({ ...props.form, missingKeyPolicy: value });
            }
          }}
          className="rounded border border-slate-300 bg-white px-2 py-2"
        >
          <option value="GROUP">Count them in a missing-value row</option>
          <option value="EXCLUDE">Leave them out</option>
          <option value="ERROR">Stop if a value is missing</option>
        </select>
      </label>
    </details>
  </section>
);

const CodedGroupEditor = (props: {
  readonly form: CodedGroupForm;
  readonly stage: ReshapeStage;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly onChange: (form: CodedGroupForm) => void;
}) => (
  <section aria-label="Group by coded value" data-testid="construction-reshape-coded-group" className="grid gap-3 rounded-lg border border-slate-200 p-3">
    <div>
      <h4 className="text-sm font-semibold text-slate-900">Group by coded value</h4>
      <p className="mt-1 text-sm text-slate-600">One row per distinct system, version, and code. Repeated copies of a code in one source record count once.</p>
    </div>
    {!props.supported ? <p role="status" className="text-sm text-amber-900">{props.reason}</p> : null}
    <label className="grid gap-1 text-sm font-medium text-slate-800">
      Coding field
      <select
        data-testid="construction-coded-group-path"
        value={props.form.codingPath}
        disabled={props.disabled || !props.supported}
        onChange={(event) => props.onChange({ ...props.form, codingPath: event.target.value })}
        className="rounded border border-slate-300 bg-white px-2 py-2"
      >
        {(props.stage.codedGroupChoices ?? []).map((choice) => (
          <option key={`${choice.occurrenceId}:${choice.codingPath}`} value={choice.codingPath}>{codingPathLabel(choice.codingPath)}</option>
        ))}
      </select>
    </label>
    <p className="text-sm text-slate-600">The result has Code system, Code version, Code, and Source records. Current columns leave this table when you apply the step.</p>
    <details className="rounded border border-slate-200 p-2 text-sm">
      <summary className="cursor-pointer font-medium text-slate-800">Advanced: records without a complete code</summary>
      <label className="mt-2 grid gap-1 text-slate-700">
        When a source record has no code or system
        <select
          data-testid="construction-coded-group-missing"
          value={props.form.missingKeyPolicy}
          disabled={props.disabled || !props.supported}
          onChange={(event) => {
            const value = event.target.value;
            if (value === 'GROUP' || value === 'EXCLUDE' || value === 'ERROR') {
              props.onChange({ ...props.form, missingKeyPolicy: value });
            }
          }}
          className="rounded border border-slate-300 bg-white px-2 py-2"
        >
          <option value="GROUP">Put it in a missing-code row</option>
          <option value="EXCLUDE">Leave it out of this result</option>
          <option value="ERROR">Stop if a code is missing</option>
        </select>
      </label>
    </details>
  </section>
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
    <section aria-label="Summarize into groups" data-testid="construction-reshape-group" className="grid content-start gap-4 rounded-lg border border-slate-200 p-3">
      <header>
        <h4 className="m-0 text-sm font-semibold text-slate-900">Summarize rows</h4>
        <p className="mb-0 mt-1 text-sm text-slate-600">Group fields become the new row labels. Each summary becomes another column.</p>
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
                    disabled={props.disabled || !props.supported}
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
        <p data-testid="construction-reshape-group-missing-key-effect" className="text-sm text-slate-600">
          If a group key is absent or null, {props.form.missingKeyPolicy === 'GROUP'
            ? 'those rows stay together in one missing-key group.'
            : props.form.missingKeyPolicy === 'EXCLUDE'
              ? 'rows with any missing key are excluded before summaries run.'
              : 'the operation stops before summaries run.'}
        </p>
      ) : null}

      <fieldset className="grid gap-3 rounded-lg border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
        <legend className="px-1 text-sm font-semibold text-slate-800">Summaries</legend>
        <p className="m-0 text-sm text-slate-600">Choose what each group should report. Count rows works without a selected field.</p>
        {props.form.aggregates.length === 0 ? (
          <p role="status" className="text-sm text-amber-900">Add at least one summary before proposing this group.</p>
        ) : null}
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
                  disabled={props.disabled || !props.supported}
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
                    disabled={props.disabled || !props.supported}
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
              <button type="button" aria-label={`Remove summary ${index + 1}`} disabled={props.disabled || !props.supported} onClick={() => props.onChange({ ...props.form, aggregates: props.form.aggregates.filter((_, itemIndex) => itemIndex !== index) })} className="justify-self-start rounded px-2 py-1 text-sm font-medium text-red-700 hover:bg-red-50">Remove summary</button>
            </div>
          );
        })}
        <button
          type="button"
          data-testid="construction-reshape-add-summary"
          disabled={props.disabled || !props.supported}
          onClick={() => {
            const aggregate = makeAggregate('COUNT_ROWS', props.stage, usedNames);
            props.onChange({ ...props.form, aggregates: [...props.form.aggregates, aggregate] });
          }}
          className="justify-self-start rounded border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-white"
        >
          Add summary
        </button>
      </fieldset>

      <details data-testid="construction-reshape-group-advanced" className="rounded-lg border border-slate-200">
        <summary className="cursor-pointer px-3 py-2 text-sm font-medium text-slate-700">Advanced options</summary>
        <div className="grid gap-3 p-3 pt-0">
          {props.form.keys.length > 0 ? (
            <label className="grid gap-1 text-sm font-medium text-slate-800">
              When a group key is absent or null
              <select
                aria-label="Missing group key policy"
                value={props.form.missingKeyPolicy}
                disabled={props.disabled || !props.supported}
                onChange={(event) => {
                  const policy = event.currentTarget.value;
                  if (policy === 'GROUP' || policy === 'EXCLUDE' || policy === 'ERROR') {
                    props.onChange({ ...props.form, missingKeyPolicy: policy });
                  }
                }}
                className="rounded border border-slate-300 bg-white px-2 py-1.5"
              >
                <option value="GROUP">Keep one missing group (absent and null together)</option>
                <option value="EXCLUDE">Exclude rows missing any group key</option>
                <option value="ERROR">Stop if any group key is missing</option>
              </select>
              <span className="text-xs font-normal text-slate-500">The rule applies before summaries are calculated.</span>
            </label>
          ) : null}

          {props.form.keys.length > 0 ? (
            <fieldset className="grid gap-3 rounded-lg border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
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

          {props.form.aggregates.length > 0 ? (
            <fieldset className="grid gap-3 rounded-lg border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
              <legend className="px-1 text-sm font-semibold text-slate-800">Summary output names and labels</legend>
              {props.form.aggregates.map((aggregate, index) => (
                <div key={aggregate.outputColumnId} className="grid gap-2 sm:grid-cols-2">
                  <label className="grid gap-1 text-sm font-medium text-slate-700">
                    Column name
                    <input aria-label={`Summary output name ${index + 1}`} value={aggregate.name} disabled={props.disabled || !props.supported} onChange={(event) => changeAggregate(index, { ...aggregate, name: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
                  </label>
                  <label className="grid gap-1 text-sm font-medium text-slate-700">
                    Column label
                    <input aria-label={`Summary output label ${index + 1}`} value={aggregate.label} disabled={props.disabled || !props.supported} onChange={(event) => changeAggregate(index, { ...aggregate, label: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
                  </label>
                </div>
              ))}
            </fieldset>
          ) : null}
        </div>
      </details>
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
          <p data-testid="construction-reshape-expand-empty-effect" className="text-sm text-slate-600">
            For an empty or missing list, {props.form.emptyPolicy === 'PRESERVE_PARENT'
              ? 'keep the original row with an empty item.'
              : props.form.emptyPolicy === 'EXCLUDE'
                ? 'leave out the original row.'
                : props.form.emptyPolicy === 'ERROR'
                  ? 'stop the operation with an error.'
                  : 'choose how to handle the original row in Advanced options.'}
          </p>
          <details data-testid="construction-reshape-expand-advanced" className="rounded-lg border border-slate-200">
            <summary className="cursor-pointer px-3 py-2 text-sm font-medium text-slate-700">Advanced options</summary>
            <div className="grid gap-3 p-3 pt-0">
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
            </div>
          </details>
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

const pivotSourceLabel = (choice: SourceInputChoice): string => choice.label.endsWith(choice.fieldPath)
  ? choice.label : `${choice.label} (${choice.fieldPath})`;

const pivotInputStage = (stage: ReshapeStage, form: PivotForm, choices: ReadonlyArray<SourceInputChoice>): ReshapeStage => {
  const usedNames = new Set(stage.columns.map((column) => column.name.toLowerCase()));
  const inputs = (form.pivotSources ?? []).flatMap((selection) => {
    if (stage.columns.some((column) => column.id === selection.columnId)) return [];
    const choice = choices.find((candidate) => candidate.choiceId === selection.choiceId);
    if (!choice) return [];
    const name = uniqueName(normalizedName(choice.fieldPath), usedNames);
    usedNames.add(name.toLowerCase());
    return [{ id: selection.columnId, name, label: choice.label, type: choice.logicalType, cardinality: 'optional_one' as const }];
  });
  return { ...stage, columns: [...stage.columns, ...inputs] };
};

const PivotEditor = (props: {
  readonly sourceChoices: ReadonlyArray<SourceInputChoice>;
  readonly form: PivotForm;
  readonly stage: ReshapeStage;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly discovery?: ConstructionReshapePivotDiscovery;
  readonly requireEveryDiscoveredCategory: boolean;
  readonly canDiscover: boolean;
  readonly onDiscover: () => void;
  readonly onManualCategorySelection: (form: PivotForm) => void;
  readonly onChange: (form: PivotForm) => void;
}) => {
  const [categorySearch, setCategorySearch] = useState('');
  const columns = scalarColumnsFor(props.stage);
  const discoveryMatches = pivotDiscoveryMatches(props.form, props.stage.id, props.discovery);
  const discoveryComplete = discoveryMatches && props.discovery.status === 'complete';
  const pairUnchanged = props.form.originalPair?.categoryColumnId === props.form.categoryColumnId
    && props.form.originalPair.valueColumnId === props.form.valueColumnId;
  const categoriesKnown = pairUnchanged || discoveryComplete;
  const categoriesAvailable = discoveryComplete ? props.discovery.categories : [];
  const missingDiscoveredCategories = props.requireEveryDiscoveredCategory
    && discoveryComplete
    && !allDiscoveredPivotCategoriesSelected(props.form, props.stage.id, props.discovery);
  const listedKeys = new Set(categoriesAvailable.map((category) => scalarIdentity(category.key)));
  const existingNotListed = pairUnchanged
    ? props.form.categories.filter((category) => !listedKeys.has(scalarIdentity(category.key)))
    : [];
  const categorySearchTerm = categorySearch.trim().toLowerCase();
  const matchesCategorySearch = (label: string, key: ConstructionTableScalar): boolean =>
    `${label} ${scalarLabel(key)}`.toLowerCase().includes(categorySearchTerm);
  const shownCategories = categoriesAvailable.filter((category) => matchesCategorySearch(category.label, category.key));
  const shownExistingNotListed = existingNotListed.filter((category) => matchesCategorySearch(scalarLabel(category.key), category.key));
  const selectedCategoryIdentities = new Set(props.form.categories.map((category) => scalarIdentity(category.key)));
  const selectedCategoryLabels = props.form.categories.map((category) =>
    categoriesAvailable.find((available) => scalarIdentity(available.key) === scalarIdentity(category.key))?.label
      ?? scalarLabel(category.key),
  );
  const displayedSelectedCategoryLabels = selectedCategoryLabels.slice(0, 3);
  const additionalSelectedCategoryCount = selectedCategoryLabels.length - displayedSelectedCategoryLabels.length;
  const showSelectedCategoryLabels = selectedCategoryLabels.length <= 10;
  const shownCategoryIdentities = new Set([
    ...shownCategories.map((category) => scalarIdentity(category.key)),
    ...shownExistingNotListed.map((category) => scalarIdentity(category.key)),
  ]);
  const hasUnselectedShownCategory = shownCategories.some((category) => !selectedCategoryIdentities.has(scalarIdentity(category.key)));
  const hasSelectedShownCategory = props.form.categories.some((category) => shownCategoryIdentities.has(scalarIdentity(category.key)));
  const shownCategoryCount = shownCategories.length + shownExistingNotListed.length;
  const categoryCount = categoriesAvailable.length + existingNotListed.length;
  const groupColumns = props.form.groupKeyIds.flatMap((id) => {
    const column = columns.find((candidate) => candidate.id === id);
    return column ? [column] : [];
  });
  const outputNamesValid = outputNamesAreValid(pivotOutputsFor(props.form, props.stage));
  const numericValue = isNumericColumn(columns.find((column) => column.id === props.form.valueColumnId) ?? { id: '', name: '', label: '', cardinality: 'optional_one' });

  const chooseInput = (role: 'group' | 'category' | 'value', selected: string) => {
    if (role === 'group' && selected === '') return;
    const choice = props.sourceChoices.find((candidate) => `source:${candidate.choiceId}` === selected);
    const existing = choice && props.form.pivotSources?.find((binding) => binding.choiceId === choice.choiceId);
    const columnId = choice ? existing?.columnId ?? createOpaqueId('pivot-input') : selected;
    const bindings = choice && !existing ? [...(props.form.pivotSources ?? []), { choiceId: choice.choiceId, columnId }] : props.form.pivotSources ?? [];
    const next = {
      ...props.form,
      groupKeyIds: role === 'group' ? [...new Set([...props.form.groupKeyIds, columnId])] : props.form.groupKeyIds,
      categoryColumnId: role === 'category' ? columnId : props.form.categoryColumnId,
      valueColumnId: role === 'value' ? columnId : role === 'category' && columnId === props.form.valueColumnId ? '' : props.form.valueColumnId,
      categories: [], categoriesPair: undefined,
    };
    const used = new Set([...next.groupKeyIds, next.categoryColumnId, next.valueColumnId]);
    props.onChange({ ...next, pivotSources: bindings.filter((binding) => used.has(binding.columnId)) });
  };
  const sourceOptions = props.sourceChoices.filter((choice) => !props.form.pivotSources?.some((binding) => binding.choiceId === choice.choiceId));

  const toggleGroup = (columnId: string, checked: boolean) => {
    const groupKeyIds = checked
      ? [...props.form.groupKeyIds, columnId]
      : props.form.groupKeyIds.filter((id) => id !== columnId);
    const used = new Set([...groupKeyIds, props.form.categoryColumnId, props.form.valueColumnId]);
    props.onChange({ ...props.form, groupKeyIds, pivotSources: props.form.pivotSources?.filter((binding) => used.has(binding.columnId)) });
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
        : [...props.form.categories, createPivotCategoryForm(available, new Set([
            ...props.stage.columns.map((column) => column.name.toLowerCase()),
            ...props.form.categories.map((category) => category.name.toLowerCase()),
          ]))]
      : props.form.categories.filter((category) => scalarIdentity(category.key) !== identity);
    const next = {
      ...props.form,
      categories,
      categoriesPair: { categoryColumnId: props.form.categoryColumnId, valueColumnId: props.form.valueColumnId },
    };
    props.onManualCategorySelection(next);
    props.onChange(next);
  };

  const selectShownCategories = () => {
    const selectedIdentities = new Set(selectedCategoryIdentities);
    const categories = [...props.form.categories];
    const usedNames = new Set([
      ...props.stage.columns.map((column) => column.name.toLowerCase()),
      ...props.form.categories.map((category) => category.name.toLowerCase()),
    ]);
    for (const available of shownCategories) {
      const identity = scalarIdentity(available.key);
      if (selectedIdentities.has(identity)) continue;
      const category = createPivotCategoryForm(available, usedNames);
      categories.push(category);
      selectedIdentities.add(identity);
      usedNames.add(category.name.toLowerCase());
    }
    if (categories.length === props.form.categories.length) return;
    const next = {
      ...props.form,
      categories,
      categoriesPair: { categoryColumnId: props.form.categoryColumnId, valueColumnId: props.form.valueColumnId },
    };
    props.onManualCategorySelection(next);
    props.onChange(next);
  };

  const clearShownCategories = () => {
    const categories = props.form.categories.filter((category) => !shownCategoryIdentities.has(scalarIdentity(category.key)));
    if (categories.length === props.form.categories.length) return;
    const next = {
      ...props.form,
      categories,
      categoriesPair: { categoryColumnId: props.form.categoryColumnId, valueColumnId: props.form.valueColumnId },
    };
    props.onManualCategorySelection(next);
    props.onChange(next);
  };

  return (
    <section aria-label="Pivot categories into columns" data-testid="construction-reshape-pivot" className="grid gap-4 rounded-lg border border-slate-200 p-3">
      <header>
        <h4 className="text-sm font-semibold text-slate-900">Turn categories into columns</h4>
        <p className="mt-1 text-sm text-slate-600">Choose what identifies a row, what names the columns, and which values fill them. Source fields are included automatically.</p>
      </header>
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      {columns.length === 0 && props.sourceChoices.length === 0 ? (
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
            {sourceOptions.length > 0 ? <select aria-label="Add pivot group field" value="" disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('group', event.currentTarget.value)} className="rounded border border-slate-300 bg-white px-2 py-1.5">
              <option value="">Add a field to identify each row</option>
              {sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`}>{pivotSourceLabel(choice)}</option>)}
            </select> : null}
          </fieldset>
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              Category field
              <select aria-label="Pivot category field" value={props.form.categoryColumnId} disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('category', event.currentTarget.value)} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                <option value="">Choose a category field</option>
                {columns.filter((column) => !props.form.groupKeyIds.includes(column.id)).map((column) => <option key={column.id} value={column.id}>{column.label}</option>)}
                <optgroup label="Available source fields">{sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`}>{pivotSourceLabel(choice)}</option>)}</optgroup>
              </select>
            </label>
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              Values field
              <select aria-label="Pivot values field" value={props.form.valueColumnId} disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('value', event.currentTarget.value)} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                <option value="">Choose a values field</option>
                {columns.filter((column) => !props.form.groupKeyIds.includes(column.id) && column.id !== props.form.categoryColumnId).map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type ?? 'unknown type'})</option>)}
                <optgroup label="Available source fields">{sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`}>{pivotSourceLabel(choice)} ({choice.logicalType})</option>)}</optgroup>
              </select>
            </label>
          </div>
          <button
            type="button"
            disabled={props.disabled || !props.supported || !props.canDiscover || props.form.categoryColumnId === '' || props.form.valueColumnId === '' || Boolean(props.form.pivotSources?.length && props.form.groupKeyIds.length === 0)}
            onClick={props.onDiscover}
            className="justify-self-start rounded border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:text-slate-400"
          >
            Find category values
          </button>
          {props.form.pivotSources?.length && props.form.groupKeyIds.length === 0 ? <p className="text-sm text-slate-600">Select a row field above to find category values.</p> : null}
          {!props.canDiscover && !categoriesKnown ? <p role="status" className="text-sm text-amber-900">Category discovery for this stage is not connected yet. Existing pivots can still be edited using their saved categories.</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'loading' ? <p role="status" className="text-sm text-slate-600">Finding category values…</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'failed' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason}</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'limit-exceeded' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason} (Maximum supported categories: {props.discovery.limit}.)</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'missing-unsupported' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason}</p> : null}
          {props.discovery && !discoveryMatches ? <p role="status" className="text-sm text-amber-900">The available category list belongs to different fields. Find values for this category and values pair before applying.</p> : null}
          {!categoriesKnown && !(props.discovery && discoveryMatches && (props.discovery.status === 'limit-exceeded' || props.discovery.status === 'missing-unsupported')) ? <p role="status" className="text-sm text-amber-900">Find category values for the selected fields before applying this pivot.</p> : null}
          {categoriesKnown ? (
            <>
              <p role="status" data-testid="construction-reshape-pivot-category-summary" className="rounded bg-slate-50 px-3 py-2 text-sm text-slate-700">
                Selected {selectedCategoryLabels.length} of {categoryCount} categories{selectedCategoryLabels.length === 0 ? ': none' : showSelectedCategoryLabels ? `: ${displayedSelectedCategoryLabels.join(', ')}${additionalSelectedCategoryCount > 0 ? `, and ${additionalSelectedCategoryCount} more` : ''}` : '. Review the list to change them.'}
              </p>
              {missingDiscoveredCategories ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Select every discovered category, or filter rows before pivoting.</p> : null}
              <p role="status" aria-live="polite" data-testid="construction-reshape-pivot-category-status" className="text-sm text-slate-600">
                {props.form.categories.length} selected · Showing {shownCategoryCount} of {categoryCount} category values
              </p>
              <details data-testid="construction-reshape-pivot-categories" className="rounded border border-slate-200 p-3">
                <summary className="cursor-pointer text-sm font-semibold text-slate-800">Change selected categories</summary>
                <fieldset className="mt-3 grid gap-3" disabled={props.disabled || !props.supported}>
                  <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-end">
                    <label className="grid gap-1 text-sm font-medium text-slate-700">
                      Search category values
                      <input
                        type="search"
                        aria-label="Search category values"
                        value={categorySearch}
                        onChange={(event) => setCategorySearch(event.currentTarget.value)}
                        placeholder="Search by label or value"
                        className="rounded border border-slate-300 bg-white px-2 py-1.5"
                      />
                    </label>
                    <button
                      type="button"
                      aria-label="Select shown categories"
                      disabled={props.disabled || !props.supported || !hasUnselectedShownCategory}
                      onClick={selectShownCategories}
                      className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:text-slate-400"
                    >
                      Select shown categories
                    </button>
                    <button
                      type="button"
                      aria-label="Clear shown categories"
                      disabled={props.disabled || !props.supported || !hasSelectedShownCategory}
                      onClick={clearShownCategories}
                      className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:text-slate-400"
                    >
                      Clear shown categories
                    </button>
                  </div>
                  {shownCategories.map((available) => {
                    const identity = scalarIdentity(available.key);
                    const current = props.form.categories.find((category) => scalarIdentity(category.key) === identity);
                    return (
                      <div key={identity} className="grid gap-2 rounded bg-slate-50 p-2">
                        <label className="flex items-center gap-2 text-sm font-medium text-slate-800">
                          <input type="checkbox" aria-label={`Include category ${available.label}`} checked={Boolean(current)} disabled={props.disabled || !props.supported} onChange={(event) => toggleCategory(available, event.currentTarget.checked)} />
                          {available.label}
                        </label>
                      </div>
                    );
                  })}
                  {shownExistingNotListed.map((category) => {
                    const identity = scalarIdentity(category.key);
                    return (
                      <div key={identity} className={`grid gap-2 rounded p-2 ${discoveryComplete ? 'border border-amber-200 bg-amber-50' : 'bg-slate-50'}`}>
                        <label className="flex items-center gap-2 text-sm font-medium text-slate-800">
                          <input type="checkbox" aria-label={`Keep saved category ${scalarLabel(category.key)}`} checked disabled={props.disabled || !props.supported} onChange={(event) => { if (!event.currentTarget.checked) toggleCategory({ key: category.key, label: scalarLabel(category.key) }, false); }} />
                          {scalarLabel(category.key)} {discoveryComplete ? <span className="font-normal text-amber-900">Not found in the latest category list</span> : null}
                        </label>
                      </div>
                    );
                  })}
                  {categoriesAvailable.length === 0 && existingNotListed.length === 0 ? <p role="status" className="text-sm text-slate-600">No categories were found for this pair.</p> : null}
                  {categoryCount > 0 && shownCategoryCount === 0 ? <p role="status" className="text-sm text-slate-600">No categories match this search.</p> : null}
                </fieldset>
              </details>
            </>
          ) : null}
          <p role="status" data-testid="construction-reshape-pivot-policy-summary" className="text-sm text-slate-600">
            Duplicate values: {pivotDuplicateEffect(props.form.duplicatePolicy)}. Empty cells: {pivotMissingCellEffect(props.form.missingCellPolicy)}. Unselected categories: {pivotUnlistedCategoryEffect(props.form.unlistedCategoryPolicy)}.
          </p>
          <details data-testid="construction-reshape-pivot-advanced" className="rounded border border-slate-200 p-3">
            <summary className="cursor-pointer text-sm font-semibold text-slate-800">Advanced settings</summary>
            <div className="mt-3 grid gap-4">
              <div className="grid gap-3 sm:grid-cols-3">
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  If a group has duplicate values
                  <select aria-label="Pivot duplicate policy" value={props.form.duplicatePolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotDuplicatePolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, duplicatePolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                    <option value="ERROR">Stop with an error</option>
                    <option value="SUM" disabled={!numericValue}>Add them together</option>
                    <option value="MIN" disabled={!numericValue}>Keep the smallest</option>
                    <option value="MAX" disabled={!numericValue}>Keep the largest</option>
                  </select>
                </label>
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  If a group has no value
                  <select aria-label="Pivot missing cell policy" value={props.form.missingCellPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotMissingPolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, missingCellPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                    <option value="NULL">Leave the new cell empty</option>
                    <option value="ERROR">Stop with an error</option>
                  </select>
                </label>
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  If a row has an unselected category
                  <select aria-label="Pivot unlisted category policy" value={props.form.unlistedCategoryPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotUnlistedPolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, unlistedCategoryPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                    <option value="ERROR">Stop with an error</option>
                    <option value="EXCLUDE_WITH_EVIDENCE" disabled>Skip it and report it</option>
                  </select>
                </label>
              </div>
              <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">
                New pivots require every category present in the rows to be selected. Skipping unselected categories is unavailable.
              </p>
              {props.form.categories.length > 0 ? (
                <fieldset className="grid gap-3 rounded border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
                  <legend className="px-1 text-sm font-semibold text-slate-800">Output column names and labels</legend>
                  {props.form.categories.map((category) => {
                    const identity = scalarIdentity(category.key);
                    const label = categoriesAvailable.find((available) => scalarIdentity(available.key) === identity)?.label
                      ?? scalarLabel(category.key);
                    return (
                      <div key={identity} className="grid gap-2 rounded bg-slate-50 p-2">
                        <p className="text-sm font-medium text-slate-700">{label}</p>
                        <PivotCategoryOutput category={category} disabled={props.disabled || !props.supported} onChange={(update) => updateCategory(identity, update)} />
                      </div>
                    );
                  })}
                </fieldset>
              ) : null}
              {!outputNamesValid ? <p role="status" className="text-sm text-amber-900">Give each output a unique column name using letters, numbers, or underscores, and add a label.</p> : null}
            </div>
          </details>
        </>
      )}
      {props.form.groupKeyIds.length === 0 ? <p role="status" className="text-sm text-amber-900">Choose at least one group field for this pivot.</p> : null}
    </section>
  );
};

const pivotDuplicateEffect = (policy: PivotForm['duplicatePolicy']): string => {
  switch (policy) {
    case 'ERROR': return 'stop the pivot with an error';
    case 'SUM': return 'be added together';
    case 'MIN': return 'keep the smallest value';
    case 'MAX': return 'keep the largest value';
    default: {
      const exhaustive: never = policy;
      return exhaustive;
    }
  }
};

const pivotMissingCellEffect = (policy: PivotForm['missingCellPolicy']): string =>
  policy === 'NULL' ? 'stay empty' : 'stop the pivot with an error';

const pivotUnlistedCategoryEffect = (policy: PivotForm['unlistedCategoryPolicy']): string =>
  policy === 'ERROR' ? 'stop the pivot with an error' : 'be skipped and reported';

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
      ? [...props.form.inputs, { columnId: column.id, key: defaultKeyForColumn(column, props.form.inputs) }]
      : props.form.inputs.filter((input) => input.columnId !== column.id);
    props.onChange({ ...props.form, inputs });
  };
  const inputCount = props.form.inputs.length;
  const rowEffect = inputCount === 0
    ? 'Choose columns to see how many rows each original record will create. Rows with missing values stay in the table by default.'
    : `${props.form.nullRowPolicy === 'PRESERVE' ? '' : 'Up to '}${inputCount} new ${inputCount === 1 ? 'row' : 'rows'} per original row. ${props.form.nullRowPolicy === 'PRESERVE'
      ? 'Rows with missing values stay in the table.'
      : 'Rows with missing values are left out.'}`;

  return (
    <section aria-label="Turn columns into rows" data-testid="construction-reshape-unpivot" className="grid gap-4 rounded-lg border border-slate-200 p-3">
      <header>
        <h4 className="text-sm font-semibold text-slate-900">Turn columns into rows</h4>
        <p className="mt-1 text-sm text-slate-600">Choose the columns to stack. Each selected column makes a row with its field name and value.</p>
      </header>
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      <fieldset className="grid gap-3 rounded border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
        <legend className="px-1 text-sm font-semibold text-slate-800">Columns to turn into rows</legend>
        {scalarColumnsFor(props.stage).map((column) => {
          const input = props.form.inputs.find((candidate) => candidate.columnId === column.id);
          return (
            <div key={column.id} className="rounded bg-slate-50 p-2">
              <label className="flex items-center gap-2 text-sm font-medium text-slate-800">
                <input type="checkbox" aria-label={`Unpivot ${column.label}`} checked={Boolean(input)} disabled={props.disabled || !props.supported} onChange={(event) => toggleInput(column, event.currentTarget.checked)} />
                {column.label}
              </label>
            </div>
          );
        })}
      </fieldset>
      <p data-testid="construction-unpivot-effect" className="text-sm text-slate-600">
        {rowEffect}
      </p>
      <details data-testid="construction-unpivot-advanced" className="rounded border border-slate-200 p-3">
        <summary className="cursor-pointer text-sm font-medium text-blue-800">Advanced options: field names and missing values</summary>
        <div className="mt-3 grid gap-3">
          {props.form.inputs.map((input) => {
            const column = props.stage.columns.find((candidate) => candidate.id === input.columnId);
            return <div key={input.columnId} className="grid gap-2 rounded bg-slate-50 p-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
              <span className="text-sm font-medium text-slate-800">{column?.label ?? 'Selected field'}</span>
              <ScalarKeyEditor input={input} disabled={props.disabled || !props.supported} onChange={(key) => updateInputKey(input.columnId, key)} />
            </div>;
          })}
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
        </div>
      </details>
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
