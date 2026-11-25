import { createOpaqueId, isNumericColumn, normalizedLogicalType, normalizedName, uniqueName } from './editorShared';
import { constructionInputForStage, isSourceProjectionStage } from '../constructionWorkspace/constructionStages';
import { constructionSchema } from '../../../contracts/types';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionStageDescriptor,
  ConstructionTableScalar,
  ConstructionStep,
} from '../../../contracts/types';

export type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'groupSource' | 'groupSources' | 'pivotSources' | 'unpivotSources' | 'expandSources'
>;

type ReshapeOperationKind = 'PIVOT' | 'CODED_PIVOT' | 'UNPIVOT' | 'GROUP' | 'CODED_GROUP' | 'EXPAND' | 'RELATED_EXPAND';
export type GroupAggregateKind = 'COUNT_ROWS' | 'COUNT_NON_NULL' | 'COUNT_DISTINCT' | 'SUM' | 'MEAN';
export type EmptyListPolicy = 'ERROR' | 'EXCLUDE' | 'PRESERVE_PARENT';

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
  | { readonly status: 'no-values'; readonly reason: string }
  | { readonly status: 'complete'; readonly categories: ReadonlyArray<ConstructionReshapePivotCategory> }
);

export type CompletePivotDiscovery = Extract<ConstructionReshapePivotDiscovery, { readonly status: 'complete' }>;

type PivotOperation = Extract<ConstructionStep['operation'], { readonly kind: 'PIVOT' }>;
type UnpivotOperation = Extract<ConstructionStep['operation'], { readonly kind: 'UNPIVOT' }>;

export type ReshapeColumn = ConstructionStageDescriptor['columns'][number];
export type ReshapeStage = ConstructionStageDescriptor;
export type ReshapeCapabilities = ConstructionCapabilitiesResponse;
export type SourceInputChoice = NonNullable<ReshapeCapabilities['sourceInput']>['choices'][number];
export type AvailableSourceField = {
  readonly choice: SourceInputChoice;
  readonly examples: ReadonlyArray<string>;
};
export type StageCapability = { readonly supported: boolean; readonly reason: string; readonly reasonCode?: string };

export type GroupKey = {
  readonly inputColumnId: string;
  readonly outputColumnId: string;
  readonly name: string;
  readonly label: string;
};

type GroupOperation = Extract<ConstructionStep['operation'], { readonly kind: 'GROUP' }>;
type GroupOperationKey = NonNullable<GroupOperation['group']['keys']>[number];
type GroupSemanticOperationKey = Extract<GroupOperationKey, { readonly semanticCategory: unknown }>;
type GroupSemanticCategory = GroupSemanticOperationKey['semanticCategory'];
type GroupFieldSourceOperationKey = Extract<GroupOperationKey, { readonly fieldSource: unknown }>;
type GroupFieldSource = GroupFieldSourceOperationKey['fieldSource'];
export type GroupFieldChoice = NonNullable<ReshapeCapabilities['groupFieldChoices']>[number];
export type GroupFieldSourceChoice = Extract<GroupFieldChoice, { readonly kind: 'FIELD_SOURCE' }>;
type GroupScalarFieldSource = Extract<GroupFieldSource, { readonly kind: 'CHOICE' }>
  | Extract<GroupFieldSource, { readonly kind: 'BINDING'; readonly valueKind: 'SCALAR' }>;
type GroupCodingFieldSource = Extract<GroupFieldSource, { readonly kind: 'CHOICE' }>
  | Extract<GroupFieldSource, { readonly kind: 'BINDING'; readonly valueKind: 'CODING_IDENTITY' }>;
export type SemanticGroupChoice = NonNullable<ReshapeCapabilities['semanticGroupChoices']>[number];
export type GroupFieldKey =
  | {
      readonly kind: 'SCALAR';
      readonly valueKind: 'SCALAR';
      readonly fieldSource: GroupScalarFieldSource;
      readonly outputColumnId: string;
      readonly name: string;
      readonly label: string;
      readonly displayLabel: string;
      readonly scopeLabel: string;
      readonly valueType: string;
    }
  | {
      readonly kind: 'CODING_IDENTITY';
      readonly valueKind: 'CODING_IDENTITY';
      readonly fieldSource: GroupCodingFieldSource;
      readonly outputColumnId: string;
      readonly name: string;
      readonly label: string;
      readonly displayLabel: string;
      readonly scopeLabel: string;
      readonly valueType: 'string';
    };
type GroupSemanticKey = {
  readonly semanticCategory: GroupSemanticCategory;
  readonly outputColumnId: string;
  readonly name: string;
  readonly label: string;
  readonly displayLabel: string;
  readonly scopeLabel: string;
};

export type GroupCodedCategory = {
  readonly key: string;
  readonly familyKey: string;
  readonly system: string;
  readonly code: string;
  readonly display: string;
  readonly valueType: string;
  readonly helperOutputColumnId: string;
  readonly groupOutputColumnId: string;
  readonly groupOutputName: string;
  readonly groupOutputLabel: string;
  readonly helperOutputName: string;
  readonly helperOutputLabel: string;
} & (
  | { readonly choice: 'saved' }
  | { readonly choice: 'ready'; readonly sourceChoiceId: string; readonly categoryChoiceId: string }
);

type GroupCodedPassthrough = {
  readonly inputColumnId: string;
  readonly outputColumnId: string;
  readonly name: string;
  readonly label: string;
  readonly type?: string;
};

export type GroupAggregate =
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

type ConstructionRowValue = NonNullable<ConstructionStep['rowValues']>[number];
type GroupRowValueFieldSource = Extract<ConstructionRowValue, { readonly fieldSource: unknown }>['fieldSource'];
export type GroupRowValueChoice = ConstructionCapabilitiesResponse['groupRowValueChoices'][number];
export type GroupRowValue = {
  readonly outputColumnId: string;
  readonly policy: 'ALL' | 'ONE';
  readonly name: string;
  readonly label: string;
  readonly displayLabel: string;
  readonly scopeLabel: string;
  readonly valueType: string;
} & (
  | { readonly inputColumnId: string }
  | { readonly fieldSource: GroupRowValueFieldSource }
);

export type PivotCategoryForm = {
  readonly key: ConstructionTableScalar;
  readonly outputColumnId: string;
  readonly name: string;
  readonly label: string;
};

export type PivotCategoryMemory = {
  readonly excluded: Set<string>;
};

export const PIVOT_DISCOVERY_MAX_CATEGORIES = 256;

export type PivotForm = {
  readonly pivotSources?: ConstructionProposalRequest['pivotSources'];
  readonly kind: 'pivot';
  readonly stepId: string;
  readonly groupKeyIds: ReadonlyArray<string>;
  readonly categoryColumnId: string;
  readonly valueColumnId: string;
  readonly categories: ReadonlyArray<PivotCategoryForm>;
  readonly discovery?: { readonly maxCategories: number };
  readonly duplicatePolicy: 'ERROR' | 'SUM' | 'MIN' | 'MAX';
  readonly missingCellPolicy: 'NULL' | 'ERROR';
  readonly unlistedCategoryPolicy: 'ERROR' | 'EXCLUDE_WITH_EVIDENCE';
  readonly originalPair?: { readonly categoryColumnId: string; readonly valueColumnId: string };
  readonly categoriesPair?: { readonly categoryColumnId: string; readonly valueColumnId: string };
  readonly savedOutputs?: ConstructionStep['outputs'];
};

export type UnpivotInputForm = {
  readonly columnId: string;
  readonly key: ConstructionTableScalar;
};

export type UnpivotForm = {
  readonly kind: 'unpivot';
  readonly stepId: string;
  readonly inputs: ReadonlyArray<UnpivotInputForm>;
  readonly unpivotSources?: ConstructionProposalRequest['unpivotSources'];
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

export type GroupForm = {
  readonly kind: 'group';
  readonly stepId: string;
  readonly missingKeyPolicy: 'GROUP' | 'EXCLUDE' | 'ERROR';
  readonly keys: ReadonlyArray<GroupKey>;
  readonly fieldKeys: ReadonlyArray<GroupFieldKey>;
  readonly keyOrder: ReadonlyArray<string>;
  readonly semanticKey?: GroupSemanticKey;
  readonly aggregates: ReadonlyArray<GroupAggregate>;
  readonly codedPivotStepId: string;
  readonly codedCategories: ReadonlyArray<GroupCodedCategory>;
  readonly codedPassthroughs: ReadonlyArray<GroupCodedPassthrough>;
  readonly rowValues: ReadonlyArray<GroupRowValue>;
  readonly unmatchedSourceKeyOutputIds?: ReadonlyArray<string>;
  readonly conflictingRelatedRouteIds?: ReadonlyArray<string>;
};

export const groupFieldChoiceForKey = (
  choices: ReadonlyArray<GroupFieldChoice>,
  key: GroupFieldKey,
): GroupFieldSourceChoice | undefined => choices.find((choice): choice is GroupFieldSourceChoice =>
  choice.kind === 'FIELD_SOURCE' && (
    key.fieldSource.kind === 'CHOICE'
      ? choice.fieldSource.kind === 'CHOICE' && choice.fieldSource.choiceId === key.fieldSource.choiceId
      : choice.matchedOutputKeyId === key.outputColumnId && choice.valueKind === key.valueKind
  ),
);

export const relatedRouteIdsForGroupFieldKeys = (
  choices: ReadonlyArray<GroupFieldChoice>,
  keys: ReadonlyArray<GroupFieldKey>,
): string[] => [...new Set(keys.flatMap((key) => {
  const routeId = groupFieldChoiceForKey(choices, key)?.relatedOccurrence?.routeId;
  return routeId ? [routeId] : [];
}))];

export type CodedGroupForm = {
  readonly kind: 'coded-group';
  readonly stepId: string;
  readonly codingPath: string;
  readonly missingKeyPolicy: 'GROUP' | 'EXCLUDE' | 'ERROR';
  readonly outputs: ConstructionStep['outputs'];
};

export type SourceGroupForm = {
  readonly kind: 'source-group';
  readonly additionalKeys?: ReadonlyArray<{ readonly choiceId: string; readonly inputColumnId: string; readonly keyOutput: ConstructionStep['outputs'][number] }>;
  readonly stepId: string;
  readonly choiceId: string;
  readonly inputColumnId: string;
  readonly missingKeyPolicy: 'GROUP' | 'EXCLUDE' | 'ERROR';
  readonly keyOutput: ConstructionStep['outputs'][number];
  readonly countOutput: ConstructionStep['outputs'][number];
};

export type ExpandForm = {
  readonly kind: 'expand';
  readonly stepId: string;
  readonly inputColumnId: string;
  readonly expandSources?: ConstructionProposalRequest['expandSources'];
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

export type ReshapeForm =
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

export interface ConstructionReshapeEditorEntry {
  readonly form: ReshapeForm;
  readonly candidateIntent?: CandidateIntent;
}

export type ReshapeEntryKind = 'choose' | 'group' | 'source-group' | 'coded-group' | 'expand' | 'pivot' | 'coded-pivot' | 'categories' | 'related-expand' | 'unpivot';

export const CONSTRUCTION_RESHAPE_EDITABLE_KINDS = ['PIVOT', 'CODED_PIVOT', 'UNPIVOT', 'GROUP', 'CODED_GROUP', 'EXPAND', 'RELATED_EXPAND'] as const;

export const capabilityFor = (
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

export const semanticGroupChoicesForStage = (
  capabilities: ReshapeCapabilities,
  stage: ReshapeStage,
): ReadonlyArray<SemanticGroupChoice> =>
  capabilities.stageId === stage.id && isSourceProjectionStage(stage)
    ? capabilities.semanticGroupChoices ?? []
    : [];

export const groupFieldChoicesForStage = (
  capabilities: ReshapeCapabilities,
  stage: ReshapeStage,
): ReadonlyArray<GroupFieldChoice> => capabilities.stageId === stage.id
  ? capabilities.groupFieldChoices ?? []
  : [];

export const groupRowValueChoicesForStage = (
  capabilities: ReshapeCapabilities,
  stage: ReshapeStage,
): ReadonlyArray<GroupRowValueChoice> => capabilities.stageId === stage.id
  ? capabilities.groupRowValueChoices
  : [];

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

const reshapeScalarTypes = new Set(['string', 'date', 'date_time', 'datetime', 'code', 'uuid', 'integer', 'decimal', 'number', 'boolean']);

const isScalarValueType = (type: string | undefined): boolean => reshapeScalarTypes.has(normalizedLogicalType(type));

export const isScalarColumn = (column: ReshapeColumn): boolean =>
  (column.cardinality === 'required_one' || column.cardinality === 'optional_one')
  && isScalarValueType(column.type);

export const isListColumn = (column: ReshapeColumn): boolean => column.cardinality === 'many'
  && isScalarValueType(column.type);

export const scalarColumnsFor = (stage: ReshapeStage): ReadonlyArray<ReshapeColumn> =>
  stage.columns.filter(isScalarColumn);

export const numericColumnsFor = (stage: ReshapeStage): ReadonlyArray<ReshapeColumn> =>
  scalarColumnsFor(stage).filter(isNumericColumn);

export const listColumnsFor = (stage: ReshapeStage): ReadonlyArray<ReshapeColumn> =>
  stage.columns.filter(isListColumn);

export const publicScalarListColumnIds = (stage: ConstructionStageDescriptor | undefined): ReadonlyArray<string> =>
  stage ? listColumnsFor(stage).map((column) => column.id) : [];

export const hasPublicScalarListColumn = (stage: ConstructionStageDescriptor | undefined): boolean =>
  publicScalarListColumnIds(stage).length > 0;

export const codingPathLabel = (path: string): string => path.split('.').map((segment) => {
  const words = segment.replaceAll('[]', '').replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/^_+/, '').trim();
  return words ? words[0].toUpperCase() + words.slice(1) : '';
}).filter(Boolean).join(' → ');

export const codedFamilyKey = (bindingId: string, sourcePath: string): string => `${bindingId}\u0000${sourcePath}`;
export const codedCategoryKey = (familyKey: string, system: string, code: string): string => `${familyKey}\u0000${system}\u0000${code}`;

export const groupOutputColumns = (form: GroupForm, stage: ReshapeStage): ConstructionStep['outputs'] => {
  const keyOutputs = new Map<string, ConstructionStep['outputs'][number]>();
  for (const key of form.keys) {
    const input = stage.columns.find((column) => column.id === key.inputColumnId);
    const codedCategory = form.codedCategories.find((category) => category.helperOutputColumnId === key.inputColumnId);
    const type = input?.type ?? codedCategory?.valueType;
    keyOutputs.set(key.outputColumnId, {
      id: key.outputColumnId,
      name: key.name,
      label: key.label.trim(),
      ...(type ? { type } : {}),
    });
  }
  for (const key of form.fieldKeys) {
    keyOutputs.set(key.outputColumnId, {
      id: key.outputColumnId,
      name: key.name,
      label: key.label.trim(),
      type: key.valueType,
    });
  }
  if (form.semanticKey) {
    keyOutputs.set(form.semanticKey.outputColumnId, {
      id: form.semanticKey.outputColumnId,
      name: form.semanticKey.name,
      label: form.semanticKey.label.trim(),
      type: 'string',
    });
  }
  const outputs = [
    ...form.keyOrder.flatMap((id) => {
      const output = keyOutputs.get(id);
      if (!output) return [];
      keyOutputs.delete(id);
      return [output];
    }),
    ...keyOutputs.values(),
  ];
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
  for (const value of form.rowValues) {
    outputs.push({
      id: value.outputColumnId,
      name: value.name,
      label: value.label.trim(),
      type: value.policy === 'ALL' ? 'array' : value.valueType,
    });
  }
  return outputs;
};

const groupOperationFor = (form: GroupForm): Extract<ConstructionStep['operation'], { readonly kind: 'GROUP' }> => {
  const passthroughID = new Map(form.codedPassthroughs.map(({ inputColumnId, outputColumnId }) => [inputColumnId, outputColumnId]));
  const codedInputs = new Set(form.codedCategories.map((category) => category.helperOutputColumnId));
  const inputID = (columnID: string) => form.codedCategories.length > 0 && !codedInputs.has(columnID)
    ? passthroughID.get(columnID) ?? columnID
    : columnID;
  const keyByOutput = new Map<string, GroupOperationKey>();
  for (const key of form.keys) {
    keyByOutput.set(key.outputColumnId, { inputColumnId: inputID(key.inputColumnId), outputColumnId: key.outputColumnId });
  }
  for (const key of form.fieldKeys) {
    keyByOutput.set(key.outputColumnId, { fieldSource: key.fieldSource, outputColumnId: key.outputColumnId });
  }
  if (form.semanticKey) {
    keyByOutput.set(form.semanticKey.outputColumnId, {
      semanticCategory: form.semanticKey.semanticCategory,
      outputColumnId: form.semanticKey.outputColumnId,
    });
  }
  const keys = [
    ...form.keyOrder.flatMap((id) => {
      const key = keyByOutput.get(id);
      if (!key) return [];
      keyByOutput.delete(id);
      return [key];
    }),
    ...keyByOutput.values(),
  ];
  return {
    kind: 'GROUP',
    group: {
      constructionId: form.stepId,
      missingKeyPolicy: form.missingKeyPolicy,
      keys,
      aggregates: form.aggregates.map((aggregate) => aggregate.operation === 'COUNT_ROWS'
        ? { operation: aggregate.operation, outputColumnId: aggregate.outputColumnId }
        : {
            operation: aggregate.operation,
            inputColumnId: inputID(aggregate.inputColumnId),
            outputColumnId: aggregate.outputColumnId,
          }),
    },
  };
};

export const expandOutputsFor = (form: ExpandForm, stage: ReshapeStage): ConstructionStep['outputs'] => {
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

export const scalarIdentity = (scalar: ConstructionTableScalar): string => JSON.stringify(scalar);

export const pivotSelectionIdentity = (form: PivotForm, stageId: string): string => JSON.stringify([
  form.stepId,
  stageId,
  form.categoryColumnId,
  form.valueColumnId,
  form.pivotSources,
  form.pivotSources?.length ? form.groupKeyIds : undefined,
]);

export const pivotDiscoveryRequestIdentity = (form: PivotForm, stageId: string, contextKey: string): string => JSON.stringify([
  contextKey,
  stageId,
  form.categoryColumnId,
  form.valueColumnId,
  form.pivotSources?.length
    ? { pivotStepId: form.stepId, groupKeyIds: form.groupKeyIds, pivotSources: form.pivotSources }
    : undefined,
]);

export const pivotCategoryMemoryKey = (form: PivotForm, stageId: string, contextKey: string): string => JSON.stringify([
  contextKey,
  stageId,
  form.stepId,
  form.categoryColumnId,
  form.valueColumnId,
]);

export const scalarLabel = (scalar: ConstructionTableScalar): string => {
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

export const createPivotCategoryForm = (
  available: ConstructionReshapePivotCategory,
  usedNames: ReadonlySet<string>,
): PivotCategoryForm => ({
  key: available.key,
  outputColumnId: createOpaqueId('pivot-column'),
  name: uniqueName(available.suggestedName ?? normalizedName(scalarLabel(available.key)), usedNames),
  label: available.suggestedLabel ?? (available.key.kind === 'NULL' ? scalarLabel(available.key) : available.label),
});

export const scalarFromText = (kind: ConstructionTableScalar['kind'], value: string): ConstructionTableScalar | undefined => {
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

export const scalarKindFromInput = (value: string): ConstructionTableScalar['kind'] | undefined => {
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

export const convertScalarKind = (kind: ConstructionTableScalar['kind'], current: ConstructionTableScalar): ConstructionTableScalar => {
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

export const pivotOutputsFor = (form: PivotForm, stage: ReshapeStage): ConstructionStep['outputs'] => {
  const savedById = new Map(form.savedOutputs?.map((column) => [column.id, column]));
  const groupOutputs = form.groupKeyIds.flatMap((id) => {
    const column = stage.columns.find((candidate) => candidate.id === id);
    if (!column) return [];
    const saved = savedById.get(column.id);
    return [saved ?? { id: column.id, name: column.name, label: column.label, ...(column.type ? { type: column.type } : {}) }];
  });
  const valueType = stage.columns.find((column) => column.id === form.valueColumnId)?.type;
  const categoryOutputs = form.discovery ? [] : form.categories.map((category) => {
    const saved = savedById.get(category.outputColumnId);
    const type = saved?.type ?? valueType;
    return {
      id: category.outputColumnId,
      name: category.name,
      label: category.label.trim(),
      ...(type ? { type } : {}),
      ...(saved?.table ? { table: saved.table } : {}),
    };
  });
  return [...groupOutputs, ...categoryOutputs];
};

const pivotOperationFor = (form: PivotForm): PivotOperation => ({
  kind: 'PIVOT',
  pivot: {
    constructionId: form.stepId,
    groupKeyIds: [...form.groupKeyIds],
    categoryColumnId: form.categoryColumnId,
    valueColumnId: form.valueColumnId,
    categories: form.discovery
      ? []
      : form.categories.map(({ key, outputColumnId }) => ({ key, outputColumnId })),
    ...(form.discovery ? { discovery: { maxCategories: form.discovery.maxCategories } } : {}),
    duplicatePolicy: form.duplicatePolicy,
    missingCellPolicy: form.missingCellPolicy,
    unlistedCategoryPolicy: form.unlistedCategoryPolicy,
  },
});

export const unpivotOutputsFor = (form: UnpivotForm, stage: ReshapeStage): ConstructionStep['outputs'] => {
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

export type CandidateEvaluation =
  | { readonly kind: 'ready'; readonly intent: CandidateIntent }
  | { readonly kind: 'incomplete' }
  | { readonly kind: 'schema-pending' };

export const candidateIntentFor = (args: {
  readonly construction: Construction;
  readonly editingStep?: ConstructionReshapeStep;
  readonly step: ConstructionReshapeStep;
  readonly groupSource?: CandidateIntent['groupSource'];
  readonly groupSources?: CandidateIntent['groupSources'];
  readonly pivotSources?: CandidateIntent['pivotSources'];
  readonly unpivotSources?: CandidateIntent['unpivotSources'];
  readonly expandSources?: CandidateIntent['expandSources'];
  readonly ownedSteps?: ReadonlyArray<ConstructionStep>;
}): CandidateEvaluation => {
  const { construction, editingStep, groupSource, groupSources, pivotSources, unpivotSources, expandSources, ownedSteps = [] } = args;
  const rowValues = args.step.rowValues ?? editingStep?.rowValues;
  const valueIds = new Set(rowValues?.map((value) => value.outputColumnId));
  const step = rowValues?.length ? {
    ...args.step,
    rowValues,
    outputs: [...args.step.outputs.filter((output) => !valueIds.has(output.id)),
      ...args.step.outputs.filter((output) => valueIds.has(output.id)),
      ...(editingStep?.outputs ?? []).filter((output) => valueIds.has(output.id) && !args.step.outputs.some((candidate) => candidate.id === output.id))],
  } : args.step;
  if (editingStep && !construction.steps.some((candidate) => candidate.id === editingStep.id)) return { kind: 'incomplete' };
  const priorSteps = editingStep
    ? construction.steps.filter((candidate) => !(candidate.ownerStepId === editingStep.id && candidate.operation.kind === 'CODED_PIVOT'))
    : construction.steps;
  const nextSteps = editingStep
    ? priorSteps.flatMap((candidate) => candidate.id === editingStep.id ? [...ownedSteps, step] : [candidate])
    : [...priorSteps, ...ownedSteps, step];
  const draft: unknown = {
    version: construction.version,
    steps: nextSteps,
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
      ...(groupSources ? { groupSources } : {}),
      ...(pivotSources !== undefined ? { pivotSources } : {}),
      ...(unpivotSources !== undefined ? { unpivotSources } : {}),
      ...(expandSources !== undefined ? { expandSources } : {}),
    },
  };
};

export const outputNamesAreValid = (columns: ReadonlyArray<{ readonly name: string; readonly label: string }>): boolean => {
  const names = columns.map((column) => column.name.trim().toLowerCase());
  return names.every((name) => /^[A-Za-z_][A-Za-z0-9_]*$/.test(name))
    && names.length === new Set(names).size
    && columns.every((column) => column.label.trim() !== '');
};

export const aggregateInputColumnsFor = (
  stage: ReshapeStage,
  operation: GroupAggregateKind,
): ReadonlyArray<ReshapeColumn> => {
  if (operation === 'COUNT_ROWS') return [];
  return operation === 'SUM' || operation === 'MEAN'
    ? numericColumnsFor(stage)
    : scalarColumnsFor(stage);
};

export const aggregateNameFor = (operation: GroupAggregateKind, column?: ReshapeColumn): string => {
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

export const makeAggregate = (
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

export const operationLabel = (operation: GroupAggregateKind): string => {
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

export const groupStepFromForm = (args: {
  readonly stage: ReshapeStage;
  readonly editingStep?: ConstructionReshapeStep;
  readonly form: GroupForm;
}): { readonly step: ConstructionReshapeStep; readonly codedInput?: ConstructionReshapeStep } | undefined => {
  const { stage, editingStep, form } = args;
  const outputs = groupOutputColumns(form, stage);
  const hasGrouping = form.keys.length > 0 || form.fieldKeys.length > 0 || Boolean(form.semanticKey) ||
    form.codedCategories.length > 0 || form.rowValues.length > 0;
  if ((!hasGrouping && form.aggregates.length === 0) || !outputNamesAreValid(outputs)) return undefined;
  if (form.semanticKey && (!isSourceProjectionStage(stage) || form.codedCategories.length > 0)) return undefined;
  if (form.fieldKeys.some((key) => key.kind === 'CODING_IDENTITY') &&
      (form.semanticKey !== undefined || form.codedCategories.length > 0 || form.fieldKeys.filter((key) => key.kind === 'CODING_IDENTITY').length > 1)) return undefined;
  if (form.fieldKeys.some((key) => key.fieldSource.kind === 'CHOICE' && key.fieldSource.choiceId.trim() === '')) return undefined;
  if (form.codedCategories.length > 0 && form.fieldKeys.length > 0) return undefined;
  if (form.codedCategories.length > 0 && form.rowValues.some((value) => 'fieldSource' in value)) return undefined;
  const codedInputIDs = new Set(form.codedCategories.map((category) => category.helperOutputColumnId));
  if (form.keys.some((key) => !scalarColumnsFor(stage).some((column) => column.id === key.inputColumnId)
      && !codedInputIDs.has(key.inputColumnId))) return undefined;
  if (new Set(form.keys.map((key) => key.inputColumnId)).size !== form.keys.length) return undefined;
  if (form.aggregates.some((aggregate) => aggregate.operation !== 'COUNT_ROWS'
      && !aggregateInputColumnsFor(stage, aggregate.operation).some((column) => column.id === aggregate.inputColumnId))) return undefined;
  const groupStep: ConstructionReshapeStep = {
    id: editingStep?.id ?? form.stepId,
    inputs: form.codedCategories.length > 0
      ? [{ kind: 'STEP_OUTPUT', stepId: form.codedPivotStepId }]
      : [constructionInputForStage(stage)],
    operation: groupOperationFor(form),
    ...(form.rowValues.length ? {
      rowValues: form.rowValues.map((value) => {
        if ('fieldSource' in value) {
          return { fieldSource: value.fieldSource, outputColumnId: value.outputColumnId, policy: value.policy };
        }
        return {
          inputColumnId: form.codedCategories.length > 0
            ? form.codedPassthroughs.find((passthrough) => passthrough.inputColumnId === value.inputColumnId)?.outputColumnId ?? ''
            : value.inputColumnId,
          outputColumnId: value.outputColumnId,
          policy: value.policy,
        };
      }),
    } : {}),
    outputs,
  };
  if (groupStep.rowValues?.some((value) => 'inputColumnId' in value && value.inputColumnId === '')) return undefined;
  if (form.codedCategories.length === 0) return { step: groupStep };
  if (new Set(form.codedCategories.map((category) => category.familyKey)).size !== 1
      || form.codedCategories.some((category) => category.choice !== 'ready')) return undefined;
  const selected = form.codedCategories;
  const first = selected[0];
  if (!first || first.choice !== 'ready') return undefined;
  const sourceChoiceIDs = new Set(selected.flatMap((category) => category.choice === 'ready' ? [category.sourceChoiceId] : []));
  if (sourceChoiceIDs.size !== 1) return undefined;
  const rowInputIDs = new Set([
    ...form.keys.flatMap((key) => codedInputIDs.has(key.inputColumnId) ? [] : [key.inputColumnId]),
    ...form.aggregates.flatMap((aggregate) => aggregate.operation === 'COUNT_ROWS' ? [] : [aggregate.inputColumnId]),
    ...form.rowValues.flatMap((value) => 'inputColumnId' in value ? [value.inputColumnId] : []),
  ]);
  const rowValues = [...rowInputIDs].flatMap((inputColumnId) => {
    const outputColumnId = form.codedPassthroughs.find((passthrough) => passthrough.inputColumnId === inputColumnId)?.outputColumnId;
    return outputColumnId ? [{ inputColumnId, outputColumnId, policy: 'ONE' as const }] : [];
  });
  if (rowValues.length !== rowInputIDs.size) return undefined;
  const codedInput: ConstructionReshapeStep = {
    id: form.codedPivotStepId,
    ownerStepId: groupStep.id,
    inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: {
      kind: 'CODED_PIVOT',
      codedPivot: {
        constructionId: form.codedPivotStepId,
        sourceChoiceId: first.sourceChoiceId,
        categories: selected.flatMap((category) => category.choice === 'ready'
          ? [{ choiceId: category.categoryChoiceId, outputColumnId: category.helperOutputColumnId }]
          : []),
        duplicatePolicy: 'ERROR',
        missingCellPolicy: 'NULL',
      },
    },
    rowValues,
    outputs: [
      ...selected.map((category) => ({
        id: category.helperOutputColumnId,
        name: category.helperOutputName,
        label: category.helperOutputLabel,
        type: 'INFER',
      })),
      ...rowValues.flatMap((rowValue) => {
        if (!('inputColumnId' in rowValue)) return [];
        const input = stage.columns.find((column) => column.id === rowValue.inputColumnId);
        const passthrough = form.codedPassthroughs.find((candidate) => candidate.outputColumnId === rowValue.outputColumnId);
        return input && passthrough ? [{ id: rowValue.outputColumnId, name: passthrough.name, label: passthrough.label, ...(passthrough.type ? { type: passthrough.type } : {}) }] : [];
      }),
    ],
  };
  return { step: groupStep, codedInput };
};

const sourceGroupProjection = (construction: Construction, step: ConstructionReshapeStep) => {
  if (step.operation.kind !== 'GROUP') return undefined;
  if (step.operation.group.keys?.some((candidate) => 'semanticCategory' in candidate)) return undefined;
  const key = step.operation.group.keys?.find((candidate) => 'inputColumnId' in candidate);
  return construction.sourceProjections?.find((projection) => projection.columnId === key?.inputColumnId);
};

export const initialSourceGroupForm = (
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
  const countId = editingStep?.operation.kind === 'GROUP' ? editingStep.operation.group.aggregates?.find((item) => item.operation === 'COUNT_ROWS')?.outputColumnId : undefined;
  const countOutput = editingStep?.outputs.find((output) => output.id === countId) ?? {
    id: createOpaqueId('group-count'), name: 'source_records', label: 'Source records', type: 'integer',
  };
  return {
    kind: 'source-group',
    additionalKeys: editingStep?.operation.kind === 'GROUP' ? (editingStep.operation.group.keys ?? []).flatMap((key) => {
      if (!('inputColumnId' in key) || key.inputColumnId === projection?.columnId) return [];
      const keyProjection = construction.sourceProjections?.find((item) => item.columnId === key.inputColumnId);
      const choice = choices.find((item) => item.fieldPath === keyProjection?.fieldPath && item.occurrenceId === keyProjection?.occurrenceId);
      const output = editingStep.outputs.find((item) => item.id === key.outputColumnId);
      return choice && output ? [{ choiceId: choice.choiceId, inputColumnId: key.inputColumnId, keyOutput: output }] : [];
    }) : [],
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

export const sourceGroupStepFromForm = (
  form: SourceGroupForm,
  stage: ReshapeStage,
  choice: SourceInputChoice,
): ConstructionReshapeStep | undefined => {
  const outputs = [form.keyOutput, ...(form.additionalKeys ?? []).map((key) => key.keyOutput), form.countOutput];
  if (!isSourceProjectionStage(stage) || !outputNamesAreValid(outputs)) return undefined;
  return {
    id: form.stepId,
    inputs: [constructionInputForStage(stage)],
    operation: {
      kind: 'GROUP',
      group: {
        constructionId: form.stepId,
        missingKeyPolicy: form.missingKeyPolicy,
        keys: [{ inputColumnId: form.inputColumnId, outputColumnId: form.keyOutput.id }, ...(form.additionalKeys ?? []).map((key) => ({ inputColumnId: key.inputColumnId, outputColumnId: key.keyOutput.id }))],
        aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: form.countOutput.id }],
      },
    },
    outputs: [
      { ...form.keyOutput, type: choice.logicalType },
      ...(form.additionalKeys ?? []).map((key) => key.keyOutput),
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

export const codedGroupStepFromForm = (
  stage: ReshapeStage,
  form: CodedGroupForm,
  editingStep?: ConstructionReshapeStep,
): ConstructionReshapeStep | undefined => {
  const choice = stage.codedGroupChoices?.find((candidate) => candidate.codingPath === form.codingPath);
  if (!choice || form.outputs.length !== 4 || !outputNamesAreValid(form.outputs)) return undefined;
  const [system, version, code, count] = form.outputs;
  return {
    id: editingStep?.id ?? form.stepId,
    inputs: [constructionInputForStage(stage)],
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

export const expandStepFromForm = (args: {
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
    inputs: [constructionInputForStage(stage)],
    operation,
    outputs,
  };
  return step;
};

export const pivotStepFromForm = (args: {
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
  if (form.groupKeyIds.length === 0 || (!form.discovery && form.categories.length === 0) || !categoryColumn || !valueColumn) return undefined;
  if (form.duplicatePolicy === 'SUM' && !isNumericColumn(valueColumn)) return undefined;
  if (groupIDs.size !== form.groupKeyIds.length || groupIDs.has(form.categoryColumnId) || groupIDs.has(form.valueColumnId)) return undefined;
  if (form.groupKeyIds.some((id) => !scalarColumnsFor(stage).some((column) => column.id === id))) return undefined;
  if (form.categoryColumnId === form.valueColumnId || new Set(keyIDs).size !== keyIDs.length) return undefined;
  if (new Set(outputIDs).size !== outputIDs.length || !outputNamesAreValid(outputs)) return undefined;
  const step: ConstructionReshapeStep = {
    id: editingStep?.id ?? form.stepId,
    inputs: [constructionInputForStage(stage)],
    operation: pivotOperationFor(form),
    outputs,
  };
  return step;
};

export const unpivotStepFromForm = (args: {
  readonly stage: ReshapeStage;
  readonly editingStep?: ConstructionReshapeStep;
  readonly form: UnpivotForm;
}): ConstructionReshapeStep | undefined => {
  const { stage, editingStep, form } = args;
  const inputIDs = form.inputs.map((input) => input.columnId);
  const keys = form.inputs.map((input) => scalarIdentity(input.key));
  const scalarInputIDs = new Set(scalarColumnsFor(stage).map((column) => column.id));
  const outputs = unpivotOutputsFor(form, stage);
  if (form.inputs.length === 0 || form.inputs.some((input) => !stage.columns.some((column) => column.id === input.columnId))) return undefined;
  if (inputIDs.some((columnId) => !scalarInputIDs.has(columnId))) return undefined;
  if (form.inputs.some((input) => input.key.kind === 'NULL' || input.key.kind === 'MISSING')) return undefined;
  if (new Set(inputIDs).size !== inputIDs.length || new Set(keys).size !== keys.length) return undefined;
  if (inputIDs.includes(form.keyOutputColumnId) || inputIDs.includes(form.valueOutputColumnId)) return undefined;
  if (form.keyOutputColumnId === form.valueOutputColumnId || !outputNamesAreValid(outputs)) return undefined;
  const step: ConstructionReshapeStep = {
    id: editingStep?.id ?? form.stepId,
    inputs: [constructionInputForStage(stage)],
    operation: unpivotOperationFor(form),
    outputs,
  };
  return step;
};

export const initialGroupForm = (
  stage: ReshapeStage,
  selectedColumns: ReadonlyArray<string>,
  editingStep?: ConstructionReshapeStep,
  construction?: Construction,
  semanticGroupChoices: ReadonlyArray<SemanticGroupChoice> = [],
  groupFieldChoices: ReadonlyArray<GroupFieldChoice> = [],
  groupRowValueChoices: ReadonlyArray<GroupRowValueChoice> = [],
): GroupForm => {
  if (editingStep?.operation.kind === 'GROUP') {
    const operation = editingStep.operation.group;
    const outputById = new Map(editingStep.outputs.map((column) => [column.id, column]));
    const semanticOperationKey = operation.keys?.find((key) => 'semanticCategory' in key);
    const semanticCategory = semanticOperationKey?.semanticCategory;
    const semanticSourceDetails = semanticCategory?.kind === 'CHOICE'
      ? semanticGroupChoices.find((choice) => choice.choiceId === semanticCategory.choiceId)
      : undefined;
    const semanticOutput = semanticOperationKey ? outputById.get(semanticOperationKey.outputColumnId) : undefined;
    const semanticKey: GroupSemanticKey | undefined = semanticOperationKey && semanticCategory
      ? {
          semanticCategory,
          outputColumnId: semanticOperationKey.outputColumnId,
          name: semanticOutput?.name ?? normalizedName(semanticSourceDetails?.label ?? 'semantic_category'),
          label: semanticOutput?.label ?? semanticSourceDetails?.label ?? 'Semantic category',
          displayLabel: semanticSourceDetails?.label ?? semanticOutput?.label ?? 'Saved semantic category field',
          scopeLabel: semanticCategory.kind === 'CHOICE'
            ? semanticSourceDetails?.scopeLabel ?? 'Current semantic family'
            : `${semanticCategory.sourceKind} · ${semanticCategory.resourceType}.${semanticCategory.codingPath} · ${semanticCategory.system}${semanticCategory.version === '' ? ' · versionless' : ` · version ${semanticCategory.version}`}`,
        }
      : undefined;
    const codedInput = construction?.steps.find((step) => step.ownerStepId === editingStep.id && step.operation.kind === 'CODED_PIVOT');
    const codedPivot = codedInput?.operation.kind === 'CODED_PIVOT' ? codedInput.operation.codedPivot : undefined;
    const codedSource = codedPivot?.source;
    const familyKey = codedSource ? codedFamilyKey(codedSource.family.bindingId, codedSource.family.sourcePath) : '';
    const codedPassthroughs: GroupCodedPassthrough[] = codedInput?.rowValues?.flatMap((value) => {
      if (!('inputColumnId' in value)) return [];
      const output = codedInput.outputs.find((column) => column.id === value.outputColumnId);
      return output ? [{
        inputColumnId: value.inputColumnId, outputColumnId: value.outputColumnId,
        name: output.name, label: output.label, ...(output.type ? { type: output.type } : {}),
      }] : [];
    }) ?? [];
    const originalInputByHelperOutput = new Map(codedPassthroughs.map((value) => [value.outputColumnId, value.inputColumnId]));
    const codedCategories: GroupCodedCategory[] = codedInput && codedPivot ? codedPivot.categories.flatMap((category) => {
      const inputOutput = codedInput.outputs.find((output) => output.id === category.outputColumnId);
      const groupKey = operation.keys?.find((key) => 'inputColumnId' in key && key.inputColumnId === category.outputColumnId);
      const groupOutput = groupKey ? outputById.get(groupKey.outputColumnId) : undefined;
      if (!inputOutput || !groupKey || !groupOutput || !codedSource) return [];
      return [{
        choice: 'saved',
        key: codedCategoryKey(familyKey, category.system ?? '', category.code ?? ''),
        familyKey,
        system: category.system ?? '',
        code: category.code ?? '',
        display: inputOutput.label,
        valueType: inputOutput.type ?? 'string',
        helperOutputColumnId: category.outputColumnId,
        groupOutputColumnId: groupKey.outputColumnId,
        groupOutputName: groupOutput.name,
        groupOutputLabel: groupOutput.label,
        helperOutputName: inputOutput.name,
        helperOutputLabel: inputOutput.label,
      }];
    }) : [];
    const codedInputIDs = new Set(codedCategories.map((category) => category.helperOutputColumnId));
    const keys = (operation.keys ?? []).flatMap((key) => {
      if (!('inputColumnId' in key)) return [];
      const output = outputById.get(key.outputColumnId);
      const sourceInputColumnId = originalInputByHelperOutput.get(key.inputColumnId) ?? key.inputColumnId;
      const input = stage.columns.find((column) => column.id === sourceInputColumnId);
      const coded = codedCategories.find((category) => category.helperOutputColumnId === key.inputColumnId);
      return [{
        inputColumnId: coded ? key.inputColumnId : sourceInputColumnId,
        outputColumnId: key.outputColumnId,
        name: output?.name ?? coded?.groupOutputName ?? input?.name ?? 'group_key',
        label: output?.label ?? coded?.groupOutputLabel ?? input?.label ?? 'Group',
      }];
    });
    const fieldKeys: GroupFieldKey[] = [];
    const unmatchedSourceKeyOutputIds: string[] = [];
    for (const key of operation.keys ?? []) {
      if (!('fieldSource' in key)) continue;
      const output = outputById.get(key.outputColumnId);
      const choiceId = key.fieldSource.kind === 'CHOICE' ? key.fieldSource.choiceId : undefined;
      const bindingValueKind = key.fieldSource.kind === 'BINDING' ? key.fieldSource.valueKind : undefined;
      const choice = groupFieldChoices.find((item): item is GroupFieldSourceChoice => item.kind === 'FIELD_SOURCE' && (
        choiceId
          ? item.fieldSource.kind === 'CHOICE' && item.fieldSource.choiceId === choiceId
          : item.matchedOutputKeyId === key.outputColumnId && item.valueKind === bindingValueKind
      ));
      const valueKind = key.fieldSource.kind === 'BINDING' ? key.fieldSource.valueKind : choice?.valueKind;
      if (key.fieldSource.kind === 'BINDING' && !choice) unmatchedSourceKeyOutputIds.push(key.outputColumnId);
      const savedSource = groupFieldSourcePresentation(key.fieldSource);
      const displayLabel = choice
        ? choice.label
        : output?.label ?? (valueKind === 'CODING_IDENTITY' ? 'Coding identity' : 'Saved source field');
      const scopeLabel = choice
        ? choice.scopeLabel ?? ''
        : savedSource
          ? `${savedSource.sourceRole} · ${savedSource.ownerResourceType}${savedSource.canonicalPath ? `.${savedSource.canonicalPath}` : ''}`
          : 'Current field choice';
      const common = {
        outputColumnId: key.outputColumnId,
        name: output?.name ?? normalizedName(displayLabel),
        label: output?.label ?? displayLabel,
        displayLabel,
        scopeLabel,
      };
      if (valueKind === 'SCALAR') {
        const source = scalarGroupFieldSource(key.fieldSource);
        if (source) fieldKeys.push({
          kind: 'SCALAR', valueKind, fieldSource: source, ...common,
          valueType: choice?.valueType ?? output?.type ?? 'string',
        });
      }
      if (valueKind === 'CODING_IDENTITY') {
        const source = codingGroupFieldSource(key.fieldSource);
        if (source) fieldKeys.push({ kind: 'CODING_IDENTITY', valueKind, fieldSource: source, ...common, valueType: 'string' });
      }
    }
    const aggregates: GroupAggregate[] = (operation.aggregates ?? []).map((aggregate) => {
      const output = outputById.get(aggregate.outputColumnId);
      const inputColumnId = 'inputColumnId' in aggregate
        ? originalInputByHelperOutput.get(aggregate.inputColumnId) ?? aggregate.inputColumnId
        : undefined;
      const common = {
        outputColumnId: aggregate.outputColumnId,
        name: output?.name ?? aggregateNameFor(aggregate.operation, stage.columns.find((column) => column.id === inputColumnId)),
        label: output?.label ?? operationLabel(aggregate.operation),
      };
      return aggregate.operation === 'COUNT_ROWS'
        ? { operation: aggregate.operation, ...common }
        : { operation: aggregate.operation, inputColumnId: inputColumnId ?? '', ...common };
    });
    const rowValues: GroupRowValue[] = (editingStep.rowValues ?? []).map((value) => {
      const output = outputById.get(value.outputColumnId);
      if ('fieldSource' in value) {
        const identity = groupRowValueBindingIdentity(value.fieldSource);
        const choice = identity
          ? groupRowValueChoices.find((candidate) => fieldChoiceIdentity(candidate) === identity)
          : undefined;
        const displayLabel = choice?.label ?? output?.label ?? 'Saved source field';
        return {
          fieldSource: value.fieldSource,
          outputColumnId: value.outputColumnId,
          policy: value.policy,
          name: output?.name ?? normalizedName(displayLabel),
          label: output?.label ?? displayLabel,
          displayLabel,
          scopeLabel: choice?.scopeLabel ?? 'Saved compiler-authorized source field',
          valueType: choice?.valueType ?? (output?.type === 'array' ? 'string' : output?.type ?? 'string'),
        };
      }
      const inputColumnId = originalInputByHelperOutput.get(value.inputColumnId) ?? value.inputColumnId;
      const input = stage.columns.find((column) => column.id === inputColumnId);
      const displayLabel = input?.label ?? output?.label ?? 'Saved source column';
      return {
        inputColumnId,
        outputColumnId: value.outputColumnId,
        policy: value.policy,
        name: output?.name ?? normalizedName(displayLabel),
        label: output?.label ?? displayLabel,
        displayLabel,
        scopeLabel: 'Current Group input column',
        valueType: input?.type ?? (output?.type === 'array' ? 'string' : output?.type ?? 'string'),
      };
    });
    const relatedRouteIds = relatedRouteIdsForGroupFieldKeys(groupFieldChoices, fieldKeys);
    return {
      kind: 'group', stepId: editingStep.id, missingKeyPolicy: operation.missingKeyPolicy ?? 'GROUP', keys, fieldKeys,
      keyOrder: (operation.keys ?? []).map((key) => key.outputColumnId),
      ...(semanticKey ? { semanticKey } : {}), aggregates,
      codedPivotStepId: codedInput?.id ?? createOpaqueId('group-coded-input'),
      codedCategories: codedCategories.filter((category) => codedInputIDs.has(category.helperOutputColumnId)),
      codedPassthroughs,
      rowValues,
      ...(unmatchedSourceKeyOutputIds.length ? { unmatchedSourceKeyOutputIds } : {}),
      ...(relatedRouteIds.length > 1 ? { conflictingRelatedRouteIds: relatedRouteIds } : {}),
    };
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
  const fieldKeys: GroupFieldKey[] = [];
  return {
    kind: 'group', stepId: createOpaqueId('group'), missingKeyPolicy: 'GROUP', keys, fieldKeys,
    keyOrder: keys.map((key) => key.outputColumnId), aggregates: [],
    codedPivotStepId: createOpaqueId('group-coded-input'), codedCategories: [], codedPassthroughs: [], rowValues: [],
  };
};

export const initialExpandForm = (
  stage: ReshapeStage,
  editingStep?: ConstructionReshapeStep,
  preferredColumnId?: string,
  signed?: ReadonlyArray<{ readonly choiceId: string; readonly fieldPath: string }>,
  sourceProjections?: Construction['sourceProjections'],
): ExpandForm => {
  if (editingStep?.operation.kind === 'EXPAND') {
    const operation = editingStep.operation.expand;
    const outputById = new Map(editingStep.outputs.map((column) => [column.id, column]));
    const owned = (sourceProjections ?? []).find((projection) => projection.columnId === operation.inputColumnId && projection.ownerStepId === editingStep.id);
    const signedChoice = owned ? signed?.find((choice) => choice.fieldPath === owned.fieldPath) : undefined;
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
      ...(signedChoice ? { expandSources: [{ choiceId: signedChoice.choiceId, columnId: operation.inputColumnId }] } : {}),
    };
  }
  const listColumns = listColumnsFor(stage);
  const firstList = preferredColumnId === undefined
    ? undefined
    : listColumns.find((column) => column.id === preferredColumnId);
  return {
    kind: 'expand',
    stepId: createOpaqueId('expand'),
    inputColumnId: firstList?.id ?? '',
    outputColumnId: createOpaqueId('expanded-column'),
    outputName: firstList ? uniqueName(`${normalizedName(firstList.name)}_item`,
      new Set(stage.columns.filter((column) => column.id !== firstList.id).map((column) => column.name.toLowerCase()))) : 'item',
    outputLabel: firstList ? `${firstList.label} item` : 'Item',
    ordinal: { kind: 'none' },
    emptyPolicy: 'PRESERVE_PARENT',
  };
};

export const initialPivotForm = (
  stage: ReshapeStage,
  selectedColumns: ReadonlyArray<string>,
  editingStep?: ConstructionReshapeStep,
): PivotForm => {
  if (editingStep?.operation.kind === 'PIVOT') {
    const operation = editingStep.operation.pivot;
    const outputById = new Map(editingStep.outputs.map((column) => [column.id, column]));
    const savedValueColumn = stage.columns.find((column) => column.id === operation.valueColumnId);
    const duplicatePolicy = operation.duplicatePolicy === 'SUM' && savedValueColumn && !isNumericColumn(savedValueColumn)
      ? 'MIN'
      : operation.duplicatePolicy;
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
      ...(operation.discovery ? { discovery: { maxCategories: operation.discovery.maxCategories } } : {}),
      duplicatePolicy,
      missingCellPolicy: operation.missingCellPolicy,
      unlistedCategoryPolicy: operation.unlistedCategoryPolicy,
      originalPair: { categoryColumnId: operation.categoryColumnId, valueColumnId: operation.valueColumnId },
      categoriesPair: { categoryColumnId: operation.categoryColumnId, valueColumnId: operation.valueColumnId },
      savedOutputs: editingStep.outputs,
    };
  }

  const scalar = scalarColumnsFor(stage);
  const groups = selectedColumns.filter((columnId) => scalar.some((column) => column.id === columnId));
  return {
    kind: 'pivot',
    stepId: createOpaqueId('pivot'),
    groupKeyIds: groups,
    categoryColumnId: '',
    valueColumnId: '',
    categories: [],
    duplicatePolicy: 'ERROR',
    missingCellPolicy: 'NULL',
    unlistedCategoryPolicy: 'ERROR',
  };
};

export const defaultKeyForColumn = (
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

export const initialUnpivotForm = (
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
  const scalarIDs = new Set(scalarColumnsFor(stage).map((column) => column.id));
  const initialInputs = selectedColumns.filter((columnId) => scalarIDs.has(columnId)).reduce<UnpivotInputForm[]>((inputs, columnId) => {
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

const groupKeyName = (stage: ReshapeStage, key: GroupKey): string =>
  stage.columns.find((column) => column.id === key.inputColumnId)?.label ?? 'selected column';

export const formForStep = (
  construction: Construction,
  capabilities: ReshapeCapabilities,
  editingStep: ConstructionReshapeStep | undefined,
  stage: ReshapeStage,
  selectedColumns: ReadonlyArray<string>,
  initialKind?: ReshapeEntryKind,
): ReshapeForm => {
  if (!editingStep) {
    if (initialKind === 'group') return initialGroupForm(stage, selectedColumns, undefined, construction, semanticGroupChoicesForStage(capabilities, stage), groupFieldChoicesForStage(capabilities, stage), groupRowValueChoicesForStage(capabilities, stage));
    if (initialKind === 'source-group') return initialSourceGroupForm(construction, capabilities.sourceInput?.choices ?? [])
      ?? { kind: 'unsupported', message: 'No source field can group these rows.' };
    if (initialKind === 'coded-group') return initialCodedGroupForm(stage);
    if (initialKind === 'expand') return initialExpandForm(stage);
    if (initialKind === 'pivot') return initialPivotForm(stage, selectedColumns);
    if (initialKind === 'coded-pivot') return { kind: 'coded-pivot' };
    if (initialKind === 'unpivot') return initialUnpivotForm(stage, selectedColumns);
    if (initialKind === 'categories') {
      return capabilityFor(stage, 'CODED_PIVOT').supported ? { kind: 'coded-pivot' } : initialPivotForm(stage, selectedColumns);
    }
    if (initialKind === 'related-expand') return { kind: 'related-expand' };
    if (initialKind === undefined || initialKind === 'choose') return initialPivotForm(stage, selectedColumns);
    return { kind: initialKind };
  }
  if (sourceGroupProjection(construction, editingStep)) return initialSourceGroupForm(
    construction, capabilities.sourceInput?.choices ?? [], editingStep,
  ) ?? { kind: 'unsupported', message: 'The saved source field is no longer available for grouping.' };
  if (editingStep.operation.kind === 'GROUP') return initialGroupForm(stage, [], editingStep, construction, semanticGroupChoicesForStage(capabilities, stage), groupFieldChoicesForStage(capabilities, stage), groupRowValueChoicesForStage(capabilities, stage));
  if (editingStep.operation.kind === 'CODED_GROUP') return initialCodedGroupForm(stage, editingStep);
  if (editingStep.operation.kind === 'EXPAND') return initialExpandForm(stage, editingStep, undefined, capabilities.expandSourceInput?.choices ?? [], construction.sourceProjections);
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
  if (editingStep.operation.kind === 'UNPIVOT') {
    const form = initialUnpivotForm(stage, [], editingStep);
    const melted = new Set(form.inputs.map((input) => input.columnId));
    const unpivotSources = (capabilities.unpivotSourceInput?.choices ?? []).flatMap((choice) =>
      choice.columnId && melted.has(choice.columnId) ? [{ choiceId: choice.choiceId, columnId: choice.columnId }] : [],
    );
    return { ...form, ...(unpivotSources.length ? { unpivotSources } : {}) };
  }
  return { kind: 'unsupported', message: 'This saved reshape is not supported by this editor.' };
};

export const pivotDiscoveryMatches = (
  form: PivotForm,
  stageId: string,
  discovery: ConstructionReshapePivotDiscovery | undefined,
): discovery is ConstructionReshapePivotDiscovery => {
  if (!discovery || discovery.stageId !== stageId
      || discovery.categoryColumnId !== form.categoryColumnId
      || discovery.valueColumnId !== form.valueColumnId) return false;
  if (!form.pivotSources?.length) return !discovery.pivotSources?.length;
  return discovery.pivotStepId === form.stepId
    && JSON.stringify(discovery.groupKeyIds ?? []) === JSON.stringify(form.groupKeyIds)
    && JSON.stringify(discovery.pivotSources ?? []) === JSON.stringify(form.pivotSources);
};

export const pivotOriginalPairApplies = (form: PivotForm): boolean => !form.pivotSources?.length
  && form.originalPair?.categoryColumnId === form.categoryColumnId
  && form.originalPair.valueColumnId === form.valueColumnId;

export const pivotCategoriesAreKnown = (
  form: PivotForm,
  stageId: string,
  discovery: ConstructionReshapePivotDiscovery | undefined,
): boolean => {
  const categoriesBelongToPair = form.categoriesPair?.categoryColumnId === form.categoryColumnId
    && form.categoriesPair.valueColumnId === form.valueColumnId;
  const resultBelongsToPair = pivotDiscoveryMatches(form, stageId, discovery)
    && discovery.status === 'complete';
  return categoriesBelongToPair && (resultBelongsToPair || pivotOriginalPairApplies(form));
};

export const allDiscoveredPivotCategoriesSelected = (
  form: PivotForm,
  stageId: string,
  discovery: ConstructionReshapePivotDiscovery | undefined,
): boolean => {
  if (!pivotDiscoveryMatches(form, stageId, discovery) || discovery.status !== 'complete') return false;
  const selectedIdentities = new Set(form.categories.map((category) => scalarIdentity(category.key)));
  return discovery.categories.every((category) => selectedIdentities.has(scalarIdentity(category.key)));
};

export const getUsedGroupNames = (form: GroupForm, excludingColumnId?: string): ReadonlySet<string> =>
  new Set([
    ...form.keys.filter((key) => key.outputColumnId !== excludingColumnId).map((key) => key.name.trim().toLowerCase()),
    ...form.fieldKeys.filter((key) => key.outputColumnId !== excludingColumnId).map((key) => key.name.trim().toLowerCase()),
    ...(form.semanticKey && form.semanticKey.outputColumnId !== excludingColumnId ? [form.semanticKey.name.trim().toLowerCase()] : []),
    ...form.aggregates.filter((aggregate) => aggregate.outputColumnId !== excludingColumnId).map((aggregate) => aggregate.name.trim().toLowerCase()),
    ...form.rowValues.filter((value) => value.outputColumnId !== excludingColumnId).map((value) => value.name.trim().toLowerCase()),
  ]);

export const ensureGroupCodedPassthroughs = (
  form: GroupForm,
  stage: ReshapeStage,
  inputColumnIDs: ReadonlyArray<string>,
): ReadonlyArray<GroupCodedPassthrough> => {
  const passthroughs = new Map(form.codedPassthroughs.map((value) => [value.inputColumnId, value]));
  for (const inputColumnId of inputColumnIDs) {
    if (passthroughs.has(inputColumnId)) continue;
    const input = stage.columns.find((column) => column.id === inputColumnId);
    if (!input) continue;
    passthroughs.set(inputColumnId, {
      inputColumnId,
      outputColumnId: createOpaqueId('coded-row-value'),
      name: input.name,
      label: input.label,
      ...(input.type ? { type: input.type } : {}),
    });
  }
  return [...passthroughs.values()];
};

export const addGroupKey = (form: GroupForm, stage: ReshapeStage, inputColumnId: string): GroupForm => {
  const input = scalarColumnsFor(stage).find((column) => column.id === inputColumnId);
  if (!input || form.keys.some((key) => key.inputColumnId === inputColumnId)) return form;
  const usedNames = getUsedGroupNames(form);
  const key = {
    inputColumnId,
    outputColumnId: createOpaqueId('group-key'),
    name: uniqueName(input.name, usedNames),
    label: input.label,
  };
  return {
    ...form,
    ...(form.codedCategories.length > 0 ? {
      codedPassthroughs: ensureGroupCodedPassthroughs(form, stage, [inputColumnId]),
    } : {}),
    keys: [...form.keys, key],
    keyOrder: [...form.keyOrder, key.outputColumnId],
  };
};

export const groupFieldKeyForChoice = (form: GroupForm, choice: GroupFieldSourceChoice): GroupFieldKey | undefined => {
  if (choice.fieldSource.kind !== 'CHOICE') return undefined;
  const outputColumnId = createOpaqueId('group-field-key');
  const usedNames = getUsedGroupNames(form);
  const common = {
    outputColumnId,
    name: uniqueName(normalizedName(choice.label), usedNames),
    label: choice.label,
    displayLabel: choice.label,
    scopeLabel: choice.scopeLabel ?? choice.canonicalPath ?? choice.label,
  };
  if (choice.valueKind === 'CODING_IDENTITY') {
    if (form.semanticKey || form.fieldKeys.some((key) => key.kind === 'CODING_IDENTITY') || form.codedCategories.length > 0) return undefined;
    return {
      kind: 'CODING_IDENTITY', valueKind: 'CODING_IDENTITY', fieldSource: choice.fieldSource,
      ...common, valueType: 'string',
    };
  }
  return {
    kind: 'SCALAR', valueKind: 'SCALAR', fieldSource: choice.fieldSource,
    ...common, valueType: choice.valueType ?? 'string',
  };
};

const scalarGroupFieldSource = (source: GroupFieldSource): GroupScalarFieldSource | undefined => {
  if (source.kind === 'CHOICE') return source;
  if (source.valueKind === 'SCALAR') return source;
  return undefined;
};

type GroupFieldSourcePresentation = {
  readonly sourceRole: string;
  readonly ownerResourceType: string;
  readonly canonicalPath?: string;
};

export const groupFieldSourcePresentation = (source: GroupFieldSource): GroupFieldSourcePresentation | undefined => {
  if (source.kind !== 'BINDING') return undefined;
  if (!('input' in source)) {
    return {
      sourceRole: source.sourceRole,
      ownerResourceType: source.ownerResourceType,
      canonicalPath: source.canonicalPath,
    };
  }
  switch (source.input.kind) {
    case 'CURRENT_STAGE':
      return {
        sourceRole: source.input.currentStage.sourceRole,
        ownerResourceType: source.input.currentStage.ownerResourceType,
        canonicalPath: source.input.currentStage.canonicalPath,
      };
    case 'RELATED_OCCURRENCE': {
      const related = source.input.relatedOccurrence;
      const canonicalPath = 'canonicalPath' in related ? related.canonicalPath : related.codingSelector;
      return {
        sourceRole: 'RELATED_OCCURRENCE',
        ownerResourceType: related.targetResourceType,
        canonicalPath,
      };
    }
    default: {
      const _exhaustive: never = source.input;
      return _exhaustive;
    }
  }
};

const codingGroupFieldSource = (source: GroupFieldSource): GroupCodingFieldSource | undefined => {
  if (source.kind === 'CHOICE') return source;
  if (source.valueKind === 'CODING_IDENTITY') return source;
  return undefined;
};

export const fieldChoiceIdentity = (choice: GroupFieldSourceChoice): string | undefined => {
  const candidateId = choice.valueKind === 'SCALAR' ? choice.candidateId : undefined;
  if (choice.fieldSource.kind !== 'CHOICE' || !choice.sourceStageId || !choice.sourceShapeId ||
      !choice.sourceRole || !choice.occurrenceId || !choice.resourceType || !choice.ownerOccurrenceId ||
      !choice.ownerResourceType || !choice.canonicalPath ||
      choice.valueKind === 'SCALAR' && !candidateId) return undefined;
  return JSON.stringify([
    choice.valueKind, choice.sourceStageId, choice.sourceShapeId, choice.sourceRole,
    choice.occurrenceId, choice.resourceType, choice.ownerOccurrenceId, choice.ownerResourceType,
    candidateId ?? '', choice.canonicalPath, choice.valueKind === 'SCALAR' ? choice.boundarySelector ?? '' : '',
  ]);
};

export const groupRowValueBindingIdentity = (source: GroupRowValueFieldSource): string | undefined => {
  if (source.kind !== 'BINDING' || source.valueKind !== 'SCALAR') return undefined;
  let sourceRole: string;
  let occurrenceId: string;
  let resourceType: string;
  let ownerOccurrenceId: string;
  let ownerResourceType: string;
  let candidateId: string | undefined;
  let canonicalPath: string | undefined;
  let boundarySelector: string | undefined;

  if ('input' in source) {
    switch (source.input.kind) {
      case 'CURRENT_STAGE': {
        const current = source.input.currentStage;
        sourceRole = current.sourceRole;
        occurrenceId = current.occurrenceId;
        resourceType = current.resourceType;
        ownerOccurrenceId = current.occurrenceId;
        ownerResourceType = current.ownerResourceType;
        candidateId = current.candidateId;
        canonicalPath = current.canonicalPath;
        boundarySelector = current.boundarySelector;
        break;
      }
      case 'RELATED_OCCURRENCE': {
        const related = source.input.relatedOccurrence;
        sourceRole = 'RELATED_OCCURRENCE';
        occurrenceId = related.targetOccurrenceId;
        resourceType = related.resourceType;
        ownerOccurrenceId = related.targetOccurrenceId;
        ownerResourceType = related.targetResourceType;
        candidateId = related.candidateId;
        canonicalPath = related.canonicalPath;
        break;
      }
      default: {
        const _exhaustive: never = source.input;
        return _exhaustive;
      }
    }
  } else {
    sourceRole = source.sourceRole;
    occurrenceId = source.occurrenceId;
    resourceType = source.resourceType;
    ownerOccurrenceId = source.ownerOccurrenceId;
    ownerResourceType = source.ownerResourceType;
    candidateId = source.candidateId;
    canonicalPath = source.canonicalPath;
    boundarySelector = source.boundarySelector;
  }
  if (!source.sourceStageId || !source.sourceShapeId || !occurrenceId || !resourceType ||
      !ownerOccurrenceId || !ownerResourceType || !candidateId || !canonicalPath) return undefined;
  return JSON.stringify([
    source.valueKind, source.sourceStageId, source.sourceShapeId, sourceRole,
    occurrenceId, resourceType, ownerOccurrenceId, ownerResourceType,
    candidateId, canonicalPath, boundarySelector ?? '',
  ]);
};

export const groupSemanticKeyForChoice = (form: GroupForm, choice: SemanticGroupChoice): GroupSemanticKey => {
  const usedNames = getUsedGroupNames(form, form.semanticKey?.outputColumnId);
  return {
    semanticCategory: { kind: 'CHOICE', choiceId: choice.choiceId },
    outputColumnId: form.semanticKey?.outputColumnId ?? createOpaqueId('group-semantic-key'),
    name: uniqueName(normalizedName(choice.label), usedNames),
    label: choice.label,
    displayLabel: choice.label,
    scopeLabel: choice.scopeLabel,
  };
};

export const replaceGroupAggregate = (
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
