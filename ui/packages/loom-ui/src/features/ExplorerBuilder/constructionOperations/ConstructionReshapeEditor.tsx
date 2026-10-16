import React, { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '../../../react';
import type { LoomClient } from '../../../api';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionStageDescriptor,
  ConstructionTableScalar,
  ConstructionStep,
  ExplorerBuilderCatalog,
  FrameSourceOption,
  SemanticInventoryBrowseResponse,
  SemanticInventoryItem,
} from '../../../types';
import { constructionSchema } from '../../../types';
import { RelatedExpandEditor, type RelatedExpandQueryOwner } from './RelatedExpandEditor';
import { CodedPivotEditor } from './CodedPivotEditor';
import { semanticConceptLabel } from '../catalogItems';
import {
  constructionInputForStage,
  isSourceProjectionStage,
  sourceInputMatchesStage,
} from '../constructionWorkspace/constructionStages';

export type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'groupSource' | 'groupSources' | 'pivotSources'
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
type AvailableSourceField = {
  readonly choice: SourceInputChoice;
  readonly examples: ReadonlyArray<string>;
};
type StageCapability = { readonly supported: boolean; readonly reason: string; readonly reasonCode?: string };

type GroupKey = {
  readonly inputColumnId: string;
  readonly outputColumnId: string;
  readonly name: string;
  readonly label: string;
};

type GroupCodedCategory = {
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
  readonly codedPivotStepId: string;
  readonly codedCategories: ReadonlyArray<GroupCodedCategory>;
  readonly codedPassthroughs: ReadonlyArray<GroupCodedPassthrough>;
  readonly rowValues: NonNullable<ConstructionStep['rowValues']>;
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
  readonly additionalKeys?: ReadonlyArray<{ readonly choiceId: string; readonly inputColumnId: string; readonly keyOutput: ConstructionStep['outputs'][number] }>;
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
  readonly candidateIntent: CandidateIntent;
}

export type ReshapeEntryKind = 'choose' | 'group' | 'source-group' | 'coded-group' | 'expand' | 'pivot' | 'coded-pivot' | 'categories' | 'related-expand' | 'unpivot';

const reshapeFormLabels = {
  group: 'Group rows',
  'source-group': 'Group by source field',
  'coded-group': 'Group by coded value',
  expand: 'Expand repeated values',
  'related-expand': 'One row per related record',
  'coded-pivot': 'Coded values as columns',
  pivot: 'Turn categories into columns',
  unpivot: 'Turn columns into rows',
} satisfies Record<Exclude<ReshapeForm['kind'], 'choose' | 'unsupported'>, string>;

export interface ConstructionReshapeEditorProps {
  readonly construction: Construction;
  readonly capabilities: ReshapeCapabilities;
  readonly editingStep?: ConstructionReshapeStep;
  readonly initialKind?: ReshapeEntryKind;
  readonly initialEntry?: ConstructionReshapeEditorEntry;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly onFormKindChange?: (kind: ReshapeForm['kind']) => void;
  readonly pivotDiscovery?: ConstructionReshapePivotDiscovery;
  readonly onDiscoverCategories?: (request: ConstructionReshapePivotDiscoveryRequest) => void;
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
  readonly relatedExpandQueryOwnerRef?: React.RefObject<RelatedExpandQueryOwner | null>;
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

const reshapeScalarTypes = new Set(['string', 'date', 'datetime', 'code', 'uuid', 'integer', 'decimal', 'boolean']);

const isScalarValueType = (type: string | undefined): boolean => reshapeScalarTypes.has((type ?? '').trim().toLowerCase());

const isScalarColumn = (column: ReshapeColumn): boolean =>
  (column.cardinality === 'required_one' || column.cardinality === 'optional_one')
  && isScalarValueType(column.type);

const isListColumn = (column: ReshapeColumn): boolean => column.cardinality === 'many'
  && isScalarValueType(column.type);

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

export const hasPublicScalarListColumn = (stage: ConstructionStageDescriptor | undefined): boolean =>
  Boolean(stage && listColumnsFor(stage).length > 0);

const codingPathLabel = (path: string): string => path.split('.').map((segment) => {
  const words = segment.replaceAll('[]', '').replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/^_+/, '').trim();
  return words ? words[0].toUpperCase() + words.slice(1) : '';
}).filter(Boolean).join(' → ');

const normalizedName = (value: string): string => {
  const normalized = value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'column';
  return /^[0-9]/.test(normalized) ? `column_${normalized}` : normalized;
};

const codedFamilyKey = (bindingId: string, sourcePath: string): string => `${bindingId}\u0000${sourcePath}`;
const codedCategoryKey = (familyKey: string, system: string, code: string): string => `${familyKey}\u0000${system}\u0000${code}`;
const INITIAL_VISIBLE_CODED_CHOICES = 10;
const CODED_INVENTORY_READINESS_POLL_INTERVAL_MS = 1_000;

const waitForCodedInventoryReadinessPoll = (signal: AbortSignal): Promise<void> => new Promise((resolve) => {
  if (signal.aborted) {
    resolve();
    return;
  }
  let timer: number | undefined;
  const finish = () => {
    if (timer !== undefined) window.clearTimeout(timer);
    signal.removeEventListener('abort', finish);
    resolve();
  };
  timer = window.setTimeout(finish, CODED_INVENTORY_READINESS_POLL_INTERVAL_MS);
  signal.addEventListener('abort', finish, { once: true });
});

const availableGroupCodedEntry = (
  item: SemanticInventoryItem,
  sources: ReadonlyArray<FrameSourceOption>,
) => {
  const source = sources.find((candidate) => candidate.resourceType === item.resourceType
    && candidate.bindingId === item.bindingId && candidate.sourcePath === item.sourcePath);
  const semanticSource = item.constructionChoice?.source;
  if (!source || !item.code || item.codingVersion || !item.constructionChoice || item.constructionChoice.route.length !== 0
      || semanticSource?.kind !== 'SEMANTIC' || semanticSource.resourceType !== source.resourceType
      || semanticSource.bindingId !== source.bindingId || semanticSource.sourcePath !== source.sourcePath
      || semanticSource.code !== item.code || (semanticSource.system ?? '') !== item.system
      || item.readiness.status !== 'READY' && item.readiness.status !== 'READY_WITH_WARNING') return undefined;
  return { item, source, familyKey: codedFamilyKey(source.bindingId, source.sourcePath) };
};

type GroupCodedSourceDiscovery = {
  readonly scopeKey: string;
  readonly sources: ReadonlyArray<FrameSourceOption>;
  readonly complete: boolean;
  readonly truncated: boolean;
  readonly loading: boolean;
};

type GroupCodedInventoryReadiness = {
  readonly scopeKey: string;
  readonly contextToken: string;
  readonly buildId: string;
  readonly state: SemanticInventoryBrowseResponse['state'];
};

type GroupCodedInventoryFamily = {
  readonly source: FrameSourceOption;
  readonly entries: SemanticInventoryItem[];
  readonly seenCursors: Set<string>;
  started: boolean;
  cursor?: string;
  exhausted: boolean;
};

type GroupCodedInventoryCheckpoint = {
  readonly scopeKey: string;
  readonly families: Map<string, GroupCodedInventoryFamily>;
  contextToken?: string;
  buildId?: string;
  initialized: boolean;
  loadedRound: number;
};

type GroupCodedInventoryView = {
  readonly scopeKey: string;
  readonly sourceKey: string;
  readonly entries: ReadonlyArray<SemanticInventoryItem>;
  readonly hasMore: boolean;
  readonly complete: boolean;
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
    const codedCategory = form.codedCategories.find((category) => category.helperOutputColumnId === key.inputColumnId);
    const type = input?.type ?? codedCategory?.valueType;
    return {
      id: key.outputColumnId,
      name: key.name,
      label: key.label.trim(),
      ...(type ? { type } : {}),
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

const groupOperationFor = (form: GroupForm): Extract<ConstructionStep['operation'], { readonly kind: 'GROUP' }> => {
  const passthroughID = new Map(form.codedPassthroughs.map(({ inputColumnId, outputColumnId }) => [inputColumnId, outputColumnId]));
  const codedInputs = new Set(form.codedCategories.map((category) => category.helperOutputColumnId));
  const inputID = (columnID: string) => form.codedCategories.length > 0 && !codedInputs.has(columnID)
    ? passthroughID.get(columnID) ?? columnID
    : columnID;
  return {
    kind: 'GROUP',
    group: {
      constructionId: form.stepId,
      missingKeyPolicy: form.missingKeyPolicy,
      keys: form.keys.map(({ inputColumnId, outputColumnId }) => ({ inputColumnId: inputID(inputColumnId), outputColumnId })),
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
  const categoryOutputs = form.categories.map((category) => {
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

type CandidateEvaluation =
  | { readonly kind: 'ready'; readonly intent: CandidateIntent }
  | { readonly kind: 'incomplete' }
  | { readonly kind: 'schema-pending' };

const candidateIntentFor = (args: {
  readonly construction: Construction;
  readonly editingStep?: ConstructionReshapeStep;
  readonly step: ConstructionReshapeStep;
  readonly groupSource?: CandidateIntent['groupSource'];
  readonly groupSources?: CandidateIntent['groupSources'];
  readonly pivotSources?: CandidateIntent['pivotSources'];
  readonly ownedSteps?: ReadonlyArray<ConstructionStep>;
}): CandidateEvaluation => {
  const { construction, editingStep, groupSource, groupSources, pivotSources, ownedSteps = [] } = args;
  const rowValues = args.step.rowValues ?? editingStep?.rowValues;
  const valueIds = new Set(rowValues?.map((value) => value.outputColumnId));
  const step = rowValues?.length ? {
    ...args.step, rowValues,
    outputs: [...args.step.outputs.filter((output) => !valueIds.has(output.id)), ...(editingStep?.outputs ?? []).filter((output) => valueIds.has(output.id))],
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
}): { readonly step: ConstructionReshapeStep; readonly codedInput?: ConstructionReshapeStep } | undefined => {
  const { stage, editingStep, form } = args;
  const outputs = groupOutputColumns(form, stage);
  if (form.aggregates.length === 0 || !outputNamesAreValid(outputs)) return undefined;
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
      rowValues: form.rowValues.map((value) => ({
        ...value,
        inputColumnId: form.codedCategories.length > 0
          ? form.codedPassthroughs.find((passthrough) => passthrough.inputColumnId === value.inputColumnId)?.outputColumnId ?? ''
          : value.inputColumnId,
      })),
    } : {}),
    outputs,
  };
  if (groupStep.rowValues?.some((value) => value.inputColumnId === '')) return undefined;
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
    ...form.rowValues.map((value) => value.inputColumnId),
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
  const countId = editingStep?.operation.kind === 'GROUP' ? editingStep.operation.group.aggregates?.find((item) => item.operation === 'COUNT_ROWS')?.outputColumnId : undefined;
  const countOutput = editingStep?.outputs.find((output) => output.id === countId) ?? {
    id: createOpaqueId('group-count'), name: 'source_records', label: 'Source records', type: 'integer',
  };
  return {
    kind: 'source-group',
    additionalKeys: editingStep?.operation.kind === 'GROUP' ? (editingStep.operation.group.keys ?? []).slice(1).flatMap((key) => {
      const projection = construction.sourceProjections?.find((item) => item.columnId === key.inputColumnId);
      const choice = choices.find((item) => item.fieldPath === projection?.fieldPath && item.occurrenceId === projection?.occurrenceId);
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

const sourceGroupStepFromForm = (
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
    inputs: [constructionInputForStage(stage)],
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
    inputs: [constructionInputForStage(stage)],
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
    inputs: [constructionInputForStage(stage)],
    operation: unpivotOperationFor(form),
    outputs,
  };
  return step;
};

const initialGroupForm = (
  stage: ReshapeStage,
  selectedColumns: ReadonlyArray<string>,
  editingStep?: ConstructionReshapeStep,
  construction?: Construction,
): GroupForm => {
  if (editingStep?.operation.kind === 'GROUP') {
    const operation = editingStep.operation.group;
    const outputById = new Map(editingStep.outputs.map((column) => [column.id, column]));
    const codedInput = construction?.steps.find((step) => step.ownerStepId === editingStep.id && step.operation.kind === 'CODED_PIVOT');
    const codedPivot = codedInput?.operation.kind === 'CODED_PIVOT' ? codedInput.operation.codedPivot : undefined;
    const codedSource = codedPivot?.source;
    const familyKey = codedSource ? codedFamilyKey(codedSource.family.bindingId, codedSource.family.sourcePath) : '';
    const codedPassthroughs: GroupCodedPassthrough[] = codedInput?.rowValues?.flatMap((value) => {
      const output = codedInput.outputs.find((column) => column.id === value.outputColumnId);
      return output ? [{
        inputColumnId: value.inputColumnId, outputColumnId: value.outputColumnId,
        name: output.name, label: output.label, ...(output.type ? { type: output.type } : {}),
      }] : [];
    }) ?? [];
    const originalInputByHelperOutput = new Map(codedPassthroughs.map((value) => [value.outputColumnId, value.inputColumnId]));
    const codedCategories: GroupCodedCategory[] = codedInput && codedPivot ? codedPivot.categories.flatMap((category) => {
      const inputOutput = codedInput.outputs.find((output) => output.id === category.outputColumnId);
      const groupKey = operation.keys?.find((key) => key.inputColumnId === category.outputColumnId);
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
    const keys = (operation.keys ?? []).map((key) => {
      const output = outputById.get(key.outputColumnId);
      const sourceInputColumnId = originalInputByHelperOutput.get(key.inputColumnId) ?? key.inputColumnId;
      const input = stage.columns.find((column) => column.id === sourceInputColumnId);
      const coded = codedCategories.find((category) => category.helperOutputColumnId === key.inputColumnId);
      return {
        inputColumnId: coded ? key.inputColumnId : sourceInputColumnId,
        outputColumnId: key.outputColumnId,
        name: output?.name ?? coded?.groupOutputName ?? input?.name ?? 'group_key',
        label: output?.label ?? coded?.groupOutputLabel ?? input?.label ?? 'Group',
      };
    });
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
    return {
      kind: 'group', stepId: editingStep.id, missingKeyPolicy: operation.missingKeyPolicy ?? 'GROUP', keys, aggregates,
      codedPivotStepId: codedInput?.id ?? createOpaqueId('group-coded-input'),
      codedCategories: codedCategories.filter((category) => codedInputIDs.has(category.helperOutputColumnId)),
      codedPassthroughs,
      rowValues: (editingStep.rowValues ?? []).map((value) => ({
        ...value,
        inputColumnId: originalInputByHelperOutput.get(value.inputColumnId) ?? value.inputColumnId,
      })),
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
  const aggregate = makeAggregate('COUNT_ROWS', stage, new Set());
  return {
    kind: 'group', stepId: createOpaqueId('group'), missingKeyPolicy: 'GROUP', keys, aggregates: [aggregate],
    codedPivotStepId: createOpaqueId('group-coded-input'), codedCategories: [], codedPassthroughs: [], rowValues: [],
  };
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

const groupKeyName = (stage: ReshapeStage, key: GroupKey): string =>
  stage.columns.find((column) => column.id === key.inputColumnId)?.label ?? 'selected column';

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
  selectedColumns: ReadonlyArray<string>,
  initialKind?: ReshapeEntryKind,
): ReshapeForm => {
  if (!editingStep) {
    if (initialKind === 'group') return initialGroupForm(stage, [], undefined, construction);
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
    return { kind: initialKind ?? 'choose' };
  }
  if (sourceGroupProjection(construction, editingStep)) return initialSourceGroupForm(
    construction, capabilities.sourceInput?.choices ?? [], editingStep,
  ) ?? { kind: 'unsupported', message: 'The saved source field is no longer available for grouping.' };
  if (editingStep.operation.kind === 'GROUP') return initialGroupForm(stage, [], editingStep, construction);
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

const ensureGroupCodedPassthroughs = (
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

const addGroupKey = (form: GroupForm, stage: ReshapeStage, inputColumnId: string): GroupForm => {
  const input = scalarColumnsFor(stage).find((column) => column.id === inputColumnId);
  if (!input || form.keys.some((key) => key.inputColumnId === inputColumnId)) return form;
  const usedNames = getUsedGroupNames(form);
  return {
    ...form,
    ...(form.codedCategories.length > 0 ? {
      codedPassthroughs: ensureGroupCodedPassthroughs(form, stage, [inputColumnId]),
    } : {}),
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
    if (!capabilities.sourceInput?.supported || !sourceInputMatchesStage(capabilities.sourceInput.stageId, stage) ||
      !choice?.isPopulated) return { kind: 'incomplete' };
    const step = sourceGroupStepFromForm(form, stage, choice);
    return step ? candidateIntentFor({
      construction, editingStep, step,
      groupSources: [{ rowChoiceId: choice.choiceId, columnId: form.inputColumnId }, ...(form.additionalKeys ?? []).map((key) => ({ rowChoiceId: key.choiceId, columnId: key.inputColumnId }))],
    }) : { kind: 'incomplete' };
  }
  if (form.kind === 'group') {
    const result = groupStepFromForm({ stage, editingStep, form });
    return result ? candidateIntentFor({ construction, editingStep, step: result.step, ownedSteps: result.codedInput ? [result.codedInput] : [] }) : { kind: 'incomplete' };
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

export const createReshapeEditorEntry = (args: {
  readonly construction: Construction;
  readonly capabilities: ReshapeCapabilities;
  readonly kind: 'group' | 'expand';
  readonly selectedColumns?: ReadonlyArray<string>;
}): ConstructionReshapeEditorEntry | undefined => {
  const stage = args.capabilities.selectedStage;
  if (args.kind === 'expand' && (!capabilityFor(stage, 'EXPAND').supported || !hasPublicScalarListColumn(stage))) return undefined;
  if (args.kind === 'group' && !capabilityFor(stage, 'GROUP').supported) return undefined;
  const form = args.kind === 'group'
    ? initialGroupForm(stage, args.selectedColumns ?? [], undefined, args.construction)
    : initialExpandForm(stage);
  const evaluation = candidateFor({
    form,
    construction: args.construction,
    capabilities: args.capabilities,
    stage,
    pivotCategoriesKnown: false,
  });
  return evaluation.kind === 'ready'
    ? { form, candidateIntent: evaluation.intent }
    : undefined;
};

export const ConstructionReshapeEditor = (props: ConstructionReshapeEditorProps) => {
  const id = useId();
  const { construction, capabilities, editingStep, disabled, onCandidateChange } = props;
  const stage = capabilities.selectedStage;
  const rootStage = capabilities.stages.find(isSourceProjectionStage) ?? stage;
  const savedOwnedCodedInput = editingStep?.operation.kind === 'GROUP'
    ? construction.steps.find((candidate) => candidate.ownerStepId === editingStep.id
      && candidate.operation.kind === 'CODED_PIVOT'
      && candidate.inputs[0]?.kind === 'SOURCE_PROJECTION')
    : undefined;
  const groupStage = savedOwnedCodedInput ? rootStage : stage;
  const sourceInput = capabilities.sourceInput;
  const sourceGroupChoices = sourceInput?.choices ?? [];
  const sourceGroupStageMatches = sourceInputMatchesStage(sourceInput?.stageId, groupStage);
  const sourceGroupHasPopulatedChoice = sourceGroupChoices.some((choice) => choice.isPopulated);
  const sourceGroupSupported = Boolean(sourceInput?.supported && sourceGroupStageMatches && sourceGroupHasPopulatedChoice);
  const availableSourceFields: ReadonlyArray<AvailableSourceField> = sourceGroupSupported
    ? sourceGroupChoices.filter((choice) => choice.isPopulated).map((choice) => ({
      choice,
      examples: props.relatedExpandContext?.catalog.availability?.fields.find((field) =>
        field.resourceType === props.codedPivotContext?.rowRoot && field.path === choice.fieldPath,
      )?.examples.slice(0, 3) ?? [],
    }))
    : [];
  const sourceGroupReason = !sourceGroupStageMatches
    ? 'Source field grouping is available only at the starting source stage.'
    : sourceInput?.supported
      ? sourceGroupHasPopulatedChoice ? '' : 'No source field with recorded values is available for grouping.'
      : sourceInput?.reason ?? 'No source field is available for grouping.';
  const groupCodedPivotCapability = capabilityFor(groupStage, 'CODED_PIVOT');
  const groupCodedValuesSupported = Boolean(savedOwnedCodedInput || groupCodedPivotCapability.supported) &&
    isSourceProjectionStage(groupStage);
  const contextKey = editorContextKey(capabilities, editingStep);
  const [form, setForm] = useState<ReshapeForm>(() => props.initialEntry?.form
    ?? formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind));
  const [formContextKey, setFormContextKey] = useState(contextKey);
  const [contractUnavailable, setContractUnavailable] = useState(false);
  const automaticallySelectedPivotPairs = useRef(new Set<string>());
  const manuallySelectedPivotPairs = useRef(new Set<string>());
  const requestedPivotDiscovery = useRef<string | undefined>(undefined);
  const groupSupport = capabilityFor(groupStage, 'GROUP');
  const codedGroupSupport = capabilityFor(stage, 'CODED_GROUP');
  const expandSupport = capabilityFor(stage, 'EXPAND');
  const relatedExpandSupport = capabilityFor(stage, 'RELATED_EXPAND');
  const savedPivotSupport = capabilityFor(stage, 'PIVOT');
  const pivotSupport = capabilities.pivotSourceInput?.supported ? { supported: true, reason: '' } : savedPivotSupport;
  const codedPivotSupport = capabilityFor(stage, 'CODED_PIVOT');
  const unpivotSupport = capabilityFor(stage, 'UNPIVOT');
  const expandColumns = listColumnsFor(stage);
  const expandReason = expandColumns.length === 0
    ? 'No list column is available in this table.'
    : expandSupport.supported ? '' : expandSupport.reason;
  const newPivotSupport = props.onDiscoverCategories
    ? pivotSupport
    : { supported: false, reason: pivotSupport.supported ? 'Stage-scoped category discovery is not available yet.' : pivotSupport.reason };
  const pivotDiscovery = props.pivotDiscovery;

  useLayoutEffect(() => {
    if (props.initialEntry) return;
    const initialForm = formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind);
    setForm(initialForm);
    props.onFormKindChange?.(initialForm.kind);
    setFormContextKey(contextKey);
    setContractUnavailable(false);
    const initialCandidate = !editingStep && (
      initialForm.kind === 'coded-group' && codedGroupSupport.supported
      || initialForm.kind === 'source-group' && sourceGroupSupported
      || initialForm.kind === 'unpivot' && unpivotSupport.supported
    )
      ? candidateFor({
          form: initialForm,
          construction,
          capabilities,
          stage: groupStage,
          editingStep,
          pivotCategoriesKnown: false,
        })
      : undefined;
    onCandidateChange(initialCandidate?.kind === 'ready' ? initialCandidate.intent : undefined);
  }, [contextKey, props.initialEntry, props.onFormKindChange]);

  const capabilityForForm = (next: ReshapeForm) => {
    switch (next.kind) {
      case 'group': return groupCodedValuesSupported ? { supported: true, reason: '' } : groupSupport;
      case 'source-group': return { supported: sourceGroupSupported, reason: sourceGroupReason };
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
      stage: next.kind === 'group' ? groupStage : stage,
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
    props.onFormKindChange?.(next.kind);
    const evaluation = next.kind === 'group' && !editingStep && availableSourceFields.length > 0
      ? { kind: 'incomplete' as const }
      : evaluate(next);
    setContractUnavailable(evaluation.kind === 'schema-pending');
    onCandidateChange(evaluation.kind === 'ready' ? evaluation.intent : undefined);
  };

  const chooseSourceField = (choice: SourceInputChoice) => {
    const next = initialSourceGroupForm(construction, [choice, ...sourceGroupChoices.filter((candidate) => candidate.choiceId !== choice.choiceId)]);
    if (next) updateForm(next);
  };

  const rememberManualPivotSelection = (pivot: PivotForm) => {
    if (pivot.categoryColumnId === '' || pivot.valueColumnId === '') return;
    manuallySelectedPivotPairs.current.add(pivotSelectionIdentity(pivot, stage.id));
  };

  useEffect(() => {
    if (
      formContextKey !== contextKey
      || form.kind !== 'pivot'
      || (form.originalPair?.categoryColumnId === form.categoryColumnId
        && form.originalPair.valueColumnId === form.valueColumnId)
      || !pivotDiscoveryMatches(form, stage.id, pivotDiscovery)
      || pivotDiscovery.status !== 'complete'
    ) return;

    const selectionIdentity = pivotSelectionIdentity(form, stage.id);
    if (
      automaticallySelectedPivotPairs.current.has(selectionIdentity)
      || manuallySelectedPivotPairs.current.has(selectionIdentity)
    ) return;

    automaticallySelectedPivotPairs.current.add(selectionIdentity);
    const savedCategories = editingStep?.operation.kind === 'PIVOT'
      && editingStep.operation.pivot.categoryColumnId === form.categoryColumnId
      && editingStep.operation.pivot.valueColumnId === form.valueColumnId
      ? initialPivotForm(stage, [], editingStep).categories : [];
    const usedNames = new Set([
      ...stage.columns.map((column) => column.name.toLowerCase()),
      ...form.categories.map((category) => category.name.toLowerCase()),
      ...savedCategories.map((category) => category.name.toLowerCase()),
    ]);
    const categories = pivotDiscovery.categories.map((available) => {
      const category = savedCategories.find((saved) => scalarIdentity(saved.key) === scalarIdentity(available.key))
        ?? createPivotCategoryForm(available, usedNames);
      usedNames.add(category.name.toLowerCase());
      return category;
    });
    updateForm({
      ...form,
      categories,
      categoriesPair: { categoryColumnId: form.categoryColumnId, valueColumnId: form.valueColumnId },
    });
  }, [form, formContextKey, contextKey, editingStep, pivotDiscovery, stage.id]);

  const requestPivotCategories = (pivot: PivotForm) => {
    if (!props.onDiscoverCategories || (pivot.pivotSources?.length && pivot.groupKeyIds.length === 0) || pivot.categoryColumnId === '' || pivot.valueColumnId === '') return;
    props.onDiscoverCategories({
      stageId: stage.id,
      ...(pivot.pivotSources?.length ? { pivotStepId: pivot.stepId, groupKeyIds: [...pivot.groupKeyIds], pivotSources: pivot.pivotSources, candidateConstruction: construction } : {}),
      categoryColumnId: pivot.categoryColumnId,
      valueColumnId: pivot.valueColumnId,
    });
  };

  useEffect(() => {
    if (
      disabled || formContextKey !== contextKey || form.kind !== 'pivot'
      || !pivotSupport.supported || !props.onDiscoverCategories
      || form.categoryColumnId === '' || form.valueColumnId === ''
      || Boolean(form.pivotSources?.length && form.groupKeyIds.length === 0)
      || pivotCategoriesAreKnown(form, stage.id, pivotDiscovery)
    ) return;
    const requestKey = JSON.stringify([
      contextKey, stage.id, form.categoryColumnId, form.valueColumnId,
      form.groupKeyIds, form.pivotSources,
    ]);
    if (requestedPivotDiscovery.current === requestKey) return;
    const timer = window.setTimeout(() => {
      requestedPivotDiscovery.current = requestKey;
      requestPivotCategories(form);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [disabled, form, formContextKey, contextKey, pivotSupport.supported, props.onDiscoverCategories, stage.id, pivotDiscovery]);

  return (
    <section aria-label="Reshape editor" data-testid="construction-reshape-editor" className="grid content-start gap-3">

      {form.kind === 'unsupported' ? (
        <p role="status" data-testid="construction-reshape-edit-unavailable" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{form.message}</p>
      ) : null}
      {form.kind === 'coded-pivot' && !props.codedPivotContext ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Coded source discovery is unavailable for these starting records. Use category and value fields already in this table.</p>
      ) : null}
      {contractUnavailable ? (
        <p role="status" data-testid="construction-reshape-schema-unavailable" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">The construction schema did not accept this reshape, so Loom has not previewed it.</p>
      ) : null}

      {form.kind !== 'choose' && form.kind !== 'unsupported' ? (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm">
          <h2 className="m-0 font-semibold text-slate-900">{reshapeFormLabels[form.kind]}</h2>
          {!editingStep ? <button type="button" disabled={disabled} onClick={() => updateForm({ kind: 'choose' })}
            className="font-medium text-blue-800 hover:underline disabled:opacity-50">Change row operation</button> : null}
        </div>
      ) : null}

      {!editingStep && form.kind === 'choose' ? (
        <fieldset className="grid gap-2">
          <legend className="mb-1 text-sm font-semibold text-slate-800">Choose how rows and columns change</legend>
          <ReshapeChoice
            testId="construction-reshape-choice-group"
            title="Summarize into groups"
            rows="One per unique combination of chosen fields."
            columns="Keep group fields and add summary columns."
            supported={groupSupport.supported || groupCodedValuesSupported}
            reason={groupSupport.supported || groupCodedValuesSupported ? '' : groupSupport.reason}
            disabled={disabled}
            onChoose={() => updateForm(initialGroupForm(stage, props.selectedColumns ?? [], undefined, construction))}
          />
          <SourceFieldChoices fields={availableSourceFields} disabled={disabled} onChoose={chooseSourceField} />
          <ReshapeChoice
            testId="construction-reshape-choice-coded-group"
            title="Group by coded value"
            rows="One per observed code."
            columns="System, version, code, and source count."
            supported={codedGroupSupport.supported && Boolean(stage.codedGroupChoices?.length)}
            reason={!isSourceProjectionStage(stage) && !codedGroupSupport.supported
              ? 'Available on starting records before other table changes.'
              : codedGroupSupport.reason || (stage.codedGroupChoices?.length ? '' : 'No Coding fields are available here.')}
            disabled={disabled}
            onChoose={() => updateForm(initialCodedGroupForm(stage))}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-expand"
            title="Expand a repeated value"
            rows="One per list item."
            columns="Replace the list with its item; optionally add its position."
            supported={expandSupport.supported}
            reason={expandReason}
            disabled={disabled || expandColumns.length === 0}
            onChoose={() => updateForm(initialExpandForm(stage))}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-related-expand"
            title="Expand related records"
            rows="One per matching related record."
            columns="Keep current columns and add the related record ID."
            supported={relatedExpandSupport.supported && Boolean(props.relatedExpandContext)}
            reason={props.relatedExpandContext ? relatedExpandSupport.reason : 'Related path search is unavailable.'}
            disabled={disabled}
            onChoose={() => updateForm({ kind: 'related-expand' })}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-coded-pivot"
            title="Coded values as columns"
            rows="One per source record."
            columns="One per selected code, filled from its paired value."
            supported={codedPivotSupport.supported && Boolean(props.codedPivotContext)}
            reason={props.codedPivotContext ? codedPivotSupport.reason : 'Coded source discovery is unavailable.'}
            disabled={disabled}
            onChoose={() => updateForm({ kind: 'coded-pivot' })}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-pivot"
            title="Turn categories into columns"
            rows="One per unique combination of row values."
            columns="One per accepted category, filled from the chosen value field."
            supported={newPivotSupport.supported}
            reason={newPivotSupport.reason}
            disabled={disabled}
            onChoose={() => updateForm(initialPivotForm(stage, props.selectedColumns ?? []))}
          />
          <ReshapeChoice
            testId="construction-reshape-choice-unpivot"
            title="Turn columns into rows"
            rows="One per selected column."
            columns="One source-name field and one value field."
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
          queryOwnerRef={props.relatedExpandQueryOwnerRef}
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
        <>
          {!editingStep ? <SourceFieldChoices fields={availableSourceFields} disabled={disabled} onChoose={chooseSourceField} /> : null}
          {(editingStep || availableSourceFields.length === 0) ? (
            <GroupEditor
              key={`${id}:group:${contextKey}`}
              form={form}
              stage={groupStage}
              codedPivotContext={groupCodedValuesSupported ? props.codedPivotContext : undefined}
              codedPivotSupported={groupCodedValuesSupported}
              supported={groupSupport.supported || groupCodedValuesSupported}
              reason={groupSupport.supported || groupCodedValuesSupported ? '' : groupSupport.reason}
              disabled={disabled}
              onChange={(next) => updateForm(next)}
            />
          ) : null}
        </>
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
          fields={availableSourceFields}
          supported={sourceGroupSupported}
          reason={sourceGroupReason}
          disabled={disabled}
          canClear={!editingStep}
          onClear={() => updateForm(initialGroupForm(groupStage, props.selectedColumns ?? [], undefined, construction))}
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
    className="grid gap-1 rounded-lg border border-slate-200 px-3 py-2 text-left enabled:hover:border-blue-400 enabled:hover:bg-blue-50 disabled:cursor-not-allowed disabled:bg-slate-50"
  >
    <span className="text-sm font-semibold text-slate-900">{props.title}</span>
    <span className="grid gap-x-3 gap-y-0.5 text-xs text-slate-600 sm:grid-cols-2">
      <span><span className="font-medium text-slate-700">Rows:</span> {props.rows}</span>
      <span><span className="font-medium text-slate-700">Columns:</span> {props.columns}</span>
    </span>
    {!props.supported || props.reason ? <span className="text-xs text-amber-900">{props.reason}</span> : null}
  </button>
);

const SourceFieldChoices = (props: {
  readonly fields: ReadonlyArray<AvailableSourceField>;
  readonly disabled: boolean;
  readonly onChoose: (choice: SourceInputChoice) => void;
}) => props.fields.length > 0 ? (
  <section aria-label="Available dataset fields" data-testid="construction-reshape-source-field-choices" className="grid gap-2 rounded-lg border border-slate-200 bg-slate-50 p-3">
    <h4 className="m-0 text-sm font-semibold text-slate-900">Available dataset fields</h4>
    <ul className="m-0 grid list-none gap-2 p-0 sm:grid-cols-2">
      {props.fields.map((field) => (
        <li key={field.choice.choiceId}>
          <button type="button" aria-label={`Group by ${field.choice.label}`} disabled={props.disabled}
            onClick={() => props.onChoose(field.choice)}
            className="grid w-full gap-1 rounded border border-slate-200 bg-white px-3 py-2 text-left hover:border-blue-400 hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50">
            <span className="text-sm font-medium text-slate-900">{field.choice.label}</span>
            <span className="text-xs text-slate-600">{field.choice.logicalType}</span>
            {field.examples.length > 0 ? <span className="text-xs text-slate-600">Examples: {field.examples.join(' · ')}</span> : null}
          </button>
        </li>
      ))}
    </ul>
  </section>
) : null;

const SourceFieldDescription = ({ field }: { readonly field: AvailableSourceField }) => (
  <span className="grid gap-0.5">
    <span className="text-sm font-medium text-slate-900">{field.choice.label}</span>
    <span className="text-xs text-slate-600">{field.choice.logicalType}</span>
    {field.examples.length > 0 ? <span className="text-xs text-slate-600">Examples: {field.examples.join(' · ')}</span> : null}
  </span>
);

const SourceGroupEditor = (props: {
  readonly form: SourceGroupForm;
  readonly fields: ReadonlyArray<AvailableSourceField>;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly canClear: boolean;
  readonly onClear: () => void;
  readonly onChange: (form: SourceGroupForm) => void;
}) => {
  const selectedCount = 1 + (props.form.additionalKeys?.length ?? 0);
  const selected = (choiceId: string) => props.form.choiceId === choiceId || props.form.additionalKeys?.some((key) => key.choiceId === choiceId) === true;
  const toggleChoice = (field: AvailableSourceField, checked: boolean) => {
    const { choice } = field;
    if (checked) {
      if (selected(choice.choiceId)) return;
      const usedNames = new Set([props.form.keyOutput.name, props.form.countOutput.name, ...(props.form.additionalKeys ?? []).map((key) => key.keyOutput.name)]);
      props.onChange({ ...props.form, additionalKeys: [...(props.form.additionalKeys ?? []), {
        choiceId: choice.choiceId,
        inputColumnId: createOpaqueId('group-source'),
        keyOutput: {
          id: createOpaqueId('group-key'),
          name: uniqueName(normalizedName(choice.fieldPath.split('.').at(-1) ?? choice.label), usedNames),
          label: choice.label,
          type: choice.logicalType,
        },
      }] });
      return;
    }
    if (props.form.choiceId === choice.choiceId) {
      const [nextPrimary, ...remainingKeys] = props.form.additionalKeys ?? [];
      if (!nextPrimary) {
        if (props.canClear) props.onClear();
        return;
      }
      props.onChange({
        ...props.form,
        choiceId: nextPrimary.choiceId,
        inputColumnId: nextPrimary.inputColumnId,
        keyOutput: nextPrimary.keyOutput,
        additionalKeys: remainingKeys,
      });
      return;
    }
    props.onChange({ ...props.form, additionalKeys: props.form.additionalKeys?.filter((key) => key.choiceId !== choice.choiceId) });
  };

  return (
    <section aria-label="Group by a source field" data-testid="construction-reshape-source-group" className="grid content-start gap-2">
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      <fieldset disabled={props.disabled || !props.supported} className="grid gap-1">
        <legend className="m-0 text-sm font-medium text-slate-800">Available dataset fields</legend>
        {props.fields.map((field) => {
          const isSelected = selected(field.choice.choiceId);
          const cannotClearLast = isSelected && selectedCount === 1 && !props.canClear;
          return (
            <label key={field.choice.choiceId} className="flex min-w-0 items-start gap-2 rounded px-2 py-1.5 hover:bg-slate-50">
              <input type="checkbox" aria-label={`Group by ${field.choice.label}`} checked={isSelected}
                disabled={cannotClearLast}
                onChange={(event) => toggleChoice(field, event.currentTarget.checked)} />
              <SourceFieldDescription field={field} />
            </label>
          );
        })}
        {selectedCount === 1 && !props.canClear ? <p className="m-0 text-xs text-slate-600">Keep at least one grouping field while editing this step.</p> : null}
      </fieldset>
      <p role="status" data-testid="construction-source-group-summary" className="m-0 text-xs text-slate-600">One row per distinct combination of {[props.form.keyOutput.label, ...(props.form.additionalKeys ?? []).map((key) => key.keyOutput.label)].join(' and ')}. Each row retains its contributing records for Add columns.</p>
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
};

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
  readonly codedPivotContext?: NonNullable<ConstructionReshapeEditorProps['codedPivotContext']>;
  readonly codedPivotSupported: boolean;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly onChange: (form: GroupForm) => void;
}) => {
  const scalarColumns = scalarColumnsFor(props.stage);
  const listColumns = listColumnsFor(props.stage);
  const usedNames = getUsedGroupNames(props.form);
  const changeKey = (index: number, update: Partial<GroupKey>) => {
    props.onChange({
      ...props.form,
      keys: props.form.keys.map((key, keyIndex) => keyIndex === index ? { ...key, ...update } : key),
    });
  };
  const changeAggregate = (index: number, aggregate: GroupAggregate) => {
    const aggregates = props.form.aggregates.map((current, aggregateIndex) => aggregateIndex === index ? aggregate : current);
    props.onChange({
      ...props.form,
      aggregates,
      ...(props.form.codedCategories.length > 0 ? {
        codedPassthroughs: ensureGroupCodedPassthroughs(props.form, props.stage, [
          ...props.form.keys.map((key) => key.inputColumnId),
          ...aggregates.flatMap((item) => item.operation === 'COUNT_ROWS' ? [] : [item.inputColumnId]),
          ...props.form.rowValues.map((value) => value.inputColumnId),
        ]),
      } : {}),
    });
  };

  return (
    <section aria-label="Summarize into groups" data-testid="construction-reshape-group" className="construction-reshape-group-editor grid content-start gap-3 rounded-lg border border-slate-200 p-3">
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}

      <div className="grid items-start gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.35fr)]">
        <fieldset className="grid min-w-0 content-start gap-2 rounded-lg border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
          <legend className="m-0 px-1 text-sm font-semibold text-slate-800">Group by</legend>
          <p className="m-0 text-xs text-slate-600">Choose values that define each output row.</p>
          {scalarColumns.length === 0 ? (
            <p role="status" className="m-0 text-sm text-slate-600">No scalar fields are available to group by. You can still summarize the whole table.</p>
          ) : (
            <div className="grid max-h-56 gap-1 overflow-y-auto sm:grid-cols-2">
              {scalarColumns.map((column) => {
                const checked = props.form.keys.some((key) => key.inputColumnId === column.id);
                return (
                  <label key={column.id} className="flex min-w-0 items-center gap-2 rounded px-2 py-1 text-sm text-slate-700 hover:bg-slate-50">
                    <input
                      type="checkbox"
                      checked={checked}
                      aria-label={`Group by ${column.label}`}
                      disabled={props.disabled || !props.supported}
                      onChange={() => props.onChange(checked
                        ? { ...props.form, keys: props.form.keys.filter((key) => key.inputColumnId !== column.id) }
                        : addGroupKey(props.form, props.stage, column.id))}
                    />
                    <span className="min-w-0 truncate" title={column.label}>{column.label}</span>
                  </label>
                );
              })}
            </div>
          )}
          {listColumns.length > 0 ? (
            <p role="status" className="m-0 text-xs text-amber-900">
              {listColumns.length} {listColumns.length === 1 ? 'list field cannot' : 'list fields cannot'} be grouped directly. Choose “Expand a repeated value” first to group by each item.
            </p>
          ) : null}
          {props.codedPivotSupported && props.codedPivotContext ? (
            <GroupCodedValuePicker
              context={props.codedPivotContext}
              form={props.form}
              stage={props.stage}
              disabled={props.disabled || !props.supported}
              onChange={props.onChange}
            />
          ) : null}
          {props.form.keys.length > 0 ? (
            <p data-testid="construction-reshape-group-missing-key-effect" className="m-0 text-xs text-slate-600">
              If a group key is absent or null, {props.form.missingKeyPolicy === 'GROUP'
                ? 'those rows stay together in one missing-key group.'
                : props.form.missingKeyPolicy === 'EXCLUDE'
                  ? 'rows with any missing key are excluded before summaries run.'
                  : 'the operation stops before summaries run.'}
            </p>
          ) : null}
        </fieldset>

        <fieldset className="grid min-w-0 content-start gap-2 rounded-lg border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
          <legend className="m-0 px-1 text-sm font-semibold text-slate-800">Summaries</legend>
          <p className="m-0 text-xs text-slate-600">Count rows needs no field; other summaries use a compatible field.</p>
          {props.form.aggregates.length === 0 ? (
            <p role="status" className="m-0 text-sm text-amber-900">Add at least one summary before proposing this group.</p>
          ) : null}
          {props.form.aggregates.map((aggregate, index) => {
            const inputColumns = aggregateInputColumnsFor(props.stage, aggregate.operation);
            const operation: GroupAggregateKind = aggregate.operation;
            return (
              <div key={aggregate.outputColumnId} data-testid={`construction-reshape-aggregate-${index}`} className="grid items-end gap-2 rounded bg-slate-50 p-2 sm:grid-cols-[minmax(8rem,0.8fr)_minmax(8rem,1fr)_minmax(8rem,1fr)_auto]">
                <label className="grid min-w-0 gap-1 text-xs font-medium text-slate-700">
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
                    className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
                  >
                    <option value="COUNT_ROWS">Count rows</option>
                    <option value="COUNT_NON_NULL">Count present values</option>
                    <option value="COUNT_DISTINCT">Count distinct values</option>
                    <option value="SUM" disabled={numericColumnsFor(props.stage).length === 0}>Sum numeric values</option>
                    <option value="MEAN" disabled={numericColumnsFor(props.stage).length === 0}>Mean numeric values</option>
                  </select>
                </label>
                {aggregate.operation !== 'COUNT_ROWS' ? (
                  <label className="grid min-w-0 gap-1 text-xs font-medium text-slate-700">
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
                      className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
                    >
                      {inputColumns.map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type ?? 'unknown type'})</option>)}
                    </select>
                    {inputColumns.length === 0 ? <span role="status" className="text-xs text-amber-900">No compatible columns were returned for this summary.</span> : null}
                  </label>
                ) : <span className="pb-2 text-xs text-slate-500">Counts every input row.</span>}
                <label className="grid min-w-0 gap-1 text-xs font-medium text-slate-700">
                  Output name
                  <input
                    aria-label={`Summary output name ${index + 1}`}
                    value={aggregate.name}
                    disabled={props.disabled || !props.supported}
                    onChange={(event) => changeAggregate(index, { ...aggregate, name: event.currentTarget.value })}
                    className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
                  />
                </label>
                <button type="button" aria-label={`Remove summary ${index + 1}`} disabled={props.disabled || !props.supported} onClick={() => props.onChange({ ...props.form, aggregates: props.form.aggregates.filter((_, itemIndex) => itemIndex !== index) })} className="mb-0.5 justify-self-start whitespace-nowrap rounded px-2 py-1 text-sm font-medium text-red-700 hover:bg-red-50">Remove</button>
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
            className="justify-self-start rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-white"
          >
            Add summary
          </button>
        </fieldset>
      </div>

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
              <legend className="m-0 px-1 text-sm font-semibold text-slate-800">Group column names</legend>
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
              <legend className="m-0 px-1 text-sm font-semibold text-slate-800">Summary output labels</legend>
              {props.form.aggregates.map((aggregate, index) => (
                <div key={aggregate.outputColumnId} className="grid gap-2">
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

const GroupCodedValuePicker = (props: {
  readonly context: NonNullable<ConstructionReshapeEditorProps['codedPivotContext']>;
  readonly form: GroupForm;
  readonly stage: ReshapeStage;
  readonly disabled: boolean;
  readonly onChange: (form: GroupForm) => void;
}) => {
  const { client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot } = props.context;
  const [pageRound, setPageRound] = useState(0);
  const [sourceProgress, setSourceProgress] = useState<GroupCodedSourceDiscovery>();
  const [inventoryProgress, setInventoryProgress] = useState<GroupCodedInventoryView>();
  const [readinessProgress, setReadinessProgress] = useState<GroupCodedInventoryReadiness>();
  const checkpointRef = useRef<GroupCodedInventoryCheckpoint | undefined>(undefined);
  const requestedRoundRef = useRef(0);
  const currentForm = useRef(props.form);
  currentForm.current = props.form;
  const changeForm = useRef(props.onChange);
  changeForm.current = props.onChange;

  const sourceScopeKey = JSON.stringify([project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot]);
  const readinessQuery = useQuery(async (signal) => {
    const browseReadiness = () => client.browseSemanticInventory({
      project, explorerId, authResourcePath, snapshotToken, rowRoot, limit: 1,
    }, signal);
    let response = await browseReadiness();
    while (response.state === 'running' && !signal.aborted) {
      setReadinessProgress({
        scopeKey: sourceScopeKey,
        contextToken: response.contextToken,
        buildId: response.buildId,
        state: response.state,
      });
      await waitForCodedInventoryReadinessPoll(signal);
      if (!signal.aborted) response = await browseReadiness();
    }
    return {
      scopeKey: sourceScopeKey,
      contextToken: response.contextToken,
      buildId: response.buildId,
      state: response.state,
    } satisfies GroupCodedInventoryReadiness;
  }, [client, sourceScopeKey]);
  const readiness = readinessQuery.data?.scopeKey === sourceScopeKey
    ? readinessQuery.data
    : readinessProgress?.scopeKey === sourceScopeKey ? readinessProgress : undefined;
  const inventoryReady = Boolean(readiness?.buildId && readiness.state === 'complete');
  const inventoryScopeKey = JSON.stringify([sourceScopeKey, readiness?.contextToken, readiness?.buildId]);
  const sourcesQuery = useQuery(async (signal) => {
    const sources = new Map<string, FrameSourceOption>();
    const seenCursors = new Set<string>();
    let cursor: string | undefined;
    let complete = false;
    let truncated = false;
    do {
      const response = await client.browseFrameSourceOptions({
        project, explorerId, authResourcePath, snapshotToken, outputId, resourceType: rowRoot, limit: 50,
        ...(cursor ? { cursor } : {}),
      }, signal);
      if (response.snapshotToken !== snapshotToken || response.outputId !== outputId) {
        throw new Error('Coded source fields were returned for a different table snapshot. Reload the table and retry.');
      }
      for (const source of response.sources) {
        if (source.route.length === 0 && source.resourceType === rowRoot) sources.set(source.choiceId, source);
      }
      complete = response.complete;
      truncated = truncated || response.truncated;
      cursor = response.nextCursor;
      const progress = {
        scopeKey: inventoryScopeKey,
        sources: [...sources.values()],
        complete: complete && !cursor,
        truncated,
        loading: Boolean(cursor),
      } satisfies GroupCodedSourceDiscovery;
      if (!signal.aborted) setSourceProgress(progress);
      if (cursor && seenCursors.has(cursor)) throw new Error('Coded source fields could not be completely listed. Reopen the editor to retry.');
      if (cursor) seenCursors.add(cursor);
    } while (cursor && !signal.aborted);
    return {
      scopeKey: inventoryScopeKey,
      sources: [...sources.values()],
      complete: complete && !cursor,
      truncated,
      loading: false,
    } satisfies GroupCodedSourceDiscovery;
  }, [client, inventoryScopeKey], inventoryReady);

  const sourceDiscovery = sourcesQuery.data?.scopeKey === inventoryScopeKey
    ? sourcesQuery.data
    : sourceProgress?.scopeKey === inventoryScopeKey ? sourceProgress : undefined;
  const sources = sourceDiscovery?.sources ?? [];
  const sourceKey = JSON.stringify(sources.map((source) => [source.choiceId, source.bindingId, source.sourcePath]));
  const currentInventoryScopeKey = useRef(inventoryScopeKey);
  currentInventoryScopeKey.current = inventoryScopeKey;
  const inventoryQuery = useQuery(async (signal) => {
    let checkpoint = checkpointRef.current;
    if (!checkpoint || checkpoint.scopeKey !== inventoryScopeKey
        || checkpoint.contextToken !== readiness?.contextToken || checkpoint.buildId !== readiness?.buildId) {
      checkpoint = {
        scopeKey: inventoryScopeKey,
        families: new Map(),
        contextToken: readiness?.contextToken,
        buildId: readiness?.buildId,
        initialized: false,
        loadedRound: 0,
      };
      checkpointRef.current = checkpoint;
    }
    const currentCheckpoint = checkpoint;
    for (const source of sources) {
      const existing = currentCheckpoint.families.get(source.choiceId);
      if (existing && (existing.source.resourceType !== source.resourceType
          || existing.source.bindingId !== source.bindingId || existing.source.sourcePath !== source.sourcePath)) {
        throw new Error('The direct coded source changed while it was loading. Reload the table and retry.');
      }
      if (!existing) currentCheckpoint.families.set(source.choiceId, {
        source, entries: [], seenCursors: new Set<string>(), started: false, exhausted: false,
      });
    }
    const makeView = (): GroupCodedInventoryView => {
      const families = [...currentCheckpoint.families.values()];
      return {
        scopeKey: inventoryScopeKey,
        sourceKey,
        entries: families.flatMap((family) => family.entries),
        hasMore: families.some((family) => Boolean(family.cursor)),
        complete: sourceDiscovery?.complete === true && !sourceDiscovery.truncated && families.every((family) => family.exhausted),
      };
    };
    const publish = () => setInventoryProgress(makeView());
    const fetchPage = async (family: GroupCodedInventoryFamily) => {
      if (signal.aborted || family.exhausted) return;
      const response = await client.browseSemanticInventory({
        project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
        sourceChoiceId: family.source.choiceId,
        ...(family.cursor ? { cursor: family.cursor } : {}),
        limit: 50,
      }, signal);
      if (signal.aborted) return;
      if (response.state !== 'complete') {
        throw new Error(`Coded values for ${family.source.title} are not confirmed by a complete inventory. Wait for the catalog to be ready, then reopen the editor.`);
      }
      if (currentCheckpoint.contextToken !== response.contextToken
          || currentCheckpoint.buildId !== response.buildId) {
        throw new Error('The coded-value inventory changed while it was loading. Reload the table and retry.');
      }
      family.entries.push(...response.entries.filter((item) => item.resourceType === family.source.resourceType
        && item.bindingId === family.source.bindingId && item.sourcePath === family.source.sourcePath));
      family.cursor = response.nextCursor;
      family.exhausted = !response.nextCursor;
      family.started = true;
      if (response.nextCursor) {
        if (family.seenCursors.has(response.nextCursor)) {
          throw new Error(`Coded values for ${family.source.title} could not be completely loaded. Reopen the editor to retry.`);
        }
        family.seenCursors.add(response.nextCursor);
      }
    };
    const unstarted = [...currentCheckpoint.families.values()].filter((family) => !family.started);
    if (unstarted.length > 0) {
      await Promise.all(unstarted.map(fetchPage));
      if (signal.aborted) return makeView();
      publish();
    }
    if (!currentCheckpoint.initialized || unstarted.length > 0) {
      while (!signal.aborted) {
        const visibleKeys = new Set(makeView().entries.flatMap((item) => {
          const entry = availableGroupCodedEntry(item, sources);
          return entry ? [codedCategoryKey(entry.familyKey, item.system, item.code)] : [];
        }));
        if (visibleKeys.size >= INITIAL_VISIBLE_CODED_CHOICES) break;
        const pending = [...currentCheckpoint.families.values()].filter((family) => !family.exhausted);
        if (pending.length === 0) break;
        await Promise.all(pending.map(fetchPage));
        if (signal.aborted) return makeView();
        publish();
      }
      if (signal.aborted) return makeView();
      currentCheckpoint.initialized = true;
      currentCheckpoint.loadedRound = pageRound;
    } else if (pageRound > currentCheckpoint.loadedRound) {
      for (let round = currentCheckpoint.loadedRound; round < pageRound && !signal.aborted; round += 1) {
        const pending = [...currentCheckpoint.families.values()].filter((family) => Boolean(family.cursor));
        if (pending.length === 0) break;
        await Promise.all(pending.map(fetchPage));
        if (signal.aborted) return makeView();
        currentCheckpoint.loadedRound = round + 1;
        publish();
      }
    }
    return makeView();
  }, [client, inventoryScopeKey, readiness?.contextToken, readiness?.buildId, sourceKey, sourceDiscovery?.complete, sourceDiscovery?.truncated, sourceDiscovery?.loading, pageRound],
  inventoryReady && ((sourceDiscovery !== undefined && sources.length > 0) || sourceDiscovery?.complete === true));

  const inventoryView = inventoryQuery.data?.scopeKey === inventoryScopeKey && inventoryQuery.data.sourceKey === sourceKey
    ? inventoryQuery.data
    : inventoryProgress?.scopeKey === inventoryScopeKey && inventoryProgress.sourceKey === sourceKey ? inventoryProgress : undefined;
  const inventory = inventoryView?.entries ?? [];
  const available = useMemo(() => {
    const entries = new Map<string, NonNullable<ReturnType<typeof availableGroupCodedEntry>>>();
    for (const item of inventory) {
      const entry = availableGroupCodedEntry(item, sources);
      if (entry) entries.set(codedCategoryKey(entry.familyKey, item.system, item.code), entry);
    }
    return [...entries.values()];
  }, [inventory, sources]);

  const savedCategoryKey = props.form.codedCategories.filter((category) => category.choice === 'saved')
    .map((category) => category.key).join('\u0001');
  const savedCategoryQuery = useQuery(async (signal) => {
    const unresolved = currentForm.current.codedCategories.filter((category) => category.choice === 'saved');
    const hydrated = await Promise.all(unresolved.map(async (category) => {
      const source = sources.find((candidate) => candidate.resourceType === rowRoot
        && codedFamilyKey(candidate.bindingId, candidate.sourcePath) === category.familyKey);
      if (!source) return { key: category.key, value: undefined, pending: !sourceDiscovery?.complete };
      try {
        const response = await client.browseSemanticInventory({
          project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
          sourceChoiceId: source.choiceId, query: category.code, limit: 50,
        }, signal);
        if (response.state !== 'complete') {
          throw new Error('The saved coded value is not confirmed by a complete inventory. Wait for the catalog to be ready, then reopen the editor.');
        }
        if (response.contextToken !== readiness?.contextToken || response.buildId !== readiness?.buildId) {
          throw new Error('The coded-value inventory changed while it was loading. Reload the table and retry.');
        }
        const item = response.entries.find((candidate) => candidate.system === category.system && candidate.code === category.code
          && availableGroupCodedEntry(candidate, [source]));
        return { key: category.key, value: item?.constructionChoice ? {
          sourceChoiceId: source.choiceId,
          categoryChoiceId: item.constructionChoice.choiceId,
          display: semanticConceptLabel(item),
          valueType: item.valueType,
        } : undefined, pending: false };
      } catch (reason: unknown) {
        return {
          key: category.key,
          value: undefined,
          pending: false,
          error: reason instanceof Error ? reason.message : 'A saved coded value could not be reauthorized.',
        };
      }
    }));
    if (signal.aborted || currentInventoryScopeKey.current !== inventoryScopeKey) return { unavailableKeys: [], error: undefined };
    const byKey = new Map(hydrated.flatMap((item) => item.value ? [[item.key, item.value] as const] : []));
    const form = currentForm.current;
    const savedKeys = new Set(form.codedCategories.filter((category) => category.choice === 'saved').map((category) => category.key));
    let changed = false;
    const codedCategories = form.codedCategories.map((category): GroupCodedCategory => {
      if (category.choice !== 'saved') return category;
      const item = byKey.get(category.key);
      if (!item) return category;
      changed = true;
      return {
        ...category,
        choice: 'ready',
        sourceChoiceId: item.sourceChoiceId,
        categoryChoiceId: item.categoryChoiceId,
        display: item.display,
        valueType: item.valueType,
      };
    });
    if (changed) changeForm.current({ ...form, codedCategories });
    return {
      unavailableKeys: hydrated.filter((item) => savedKeys.has(item.key) && !item.value && !item.pending).map((item) => item.key),
      error: hydrated.find((item) => item.error)?.error,
    };
  }, [client, inventoryScopeKey, sourceKey, sourceDiscovery?.complete, sourceDiscovery?.loading, savedCategoryKey],
  inventoryReady && sourceDiscovery !== undefined && savedCategoryKey.length > 0);
  const unavailableSavedKeys = new Set(savedCategoryQuery.data?.unavailableKeys ?? []);

  const selectedFamily = props.form.codedCategories[0]?.familyKey;
  const removeSelectedCategory = (category: GroupCodedCategory) => {
    props.onChange({
      ...props.form,
      codedCategories: props.form.codedCategories.filter((candidate) => candidate.key !== category.key),
      keys: props.form.keys.filter((groupKey) => groupKey.outputColumnId !== category.groupOutputColumnId),
    });
  };
  const toggle = (entry: (typeof available)[number]) => {
    const { item, source, familyKey } = entry;
    const key = codedCategoryKey(familyKey, item.system, item.code);
    const prior = props.form.codedCategories.find((category) => category.key === key);
    if (prior) {
      removeSelectedCategory(prior);
      return;
    }
    if (selectedFamily && selectedFamily !== familyKey || props.form.codedCategories.length >= 50 || !item.constructionChoice) return;
    const label = semanticConceptLabel(item);
    const usedNames = getUsedGroupNames(props.form);
    const groupOutputName = uniqueName(item.code, usedNames);
    const helperOutputName = uniqueName(item.code, new Set([
      ...props.stage.columns.map((column) => column.name.toLowerCase()),
      ...props.form.codedCategories.map((category) => category.helperOutputName.toLowerCase()),
    ]));
    const groupKey: GroupKey = {
      inputColumnId: createOpaqueId('coded-group-input'),
      outputColumnId: createOpaqueId('coded-group-output'),
      name: groupOutputName,
      label,
    };
    const codedCategory: GroupCodedCategory = {
      choice: 'ready',
      key,
      familyKey,
      system: item.system,
      code: item.code,
      display: label,
      valueType: item.valueType,
      sourceChoiceId: source.choiceId,
      categoryChoiceId: item.constructionChoice.choiceId,
      helperOutputColumnId: groupKey.inputColumnId,
      groupOutputColumnId: groupKey.outputColumnId,
      groupOutputName: groupKey.name,
      groupOutputLabel: groupKey.label,
      helperOutputName,
      helperOutputLabel: label,
    };
    const codedCategories = [...props.form.codedCategories, codedCategory];
    const nextForm = { ...props.form, codedCategories, keys: [...props.form.keys, groupKey] };
    props.onChange({
      ...nextForm,
      codedPassthroughs: ensureGroupCodedPassthroughs(nextForm, props.stage, [
        ...nextForm.keys.flatMap((key) => codedCategories.some((category) => category.helperOutputColumnId === key.inputColumnId) ? [] : [key.inputColumnId]),
        ...nextForm.aggregates.flatMap((aggregate) => aggregate.operation === 'COUNT_ROWS' ? [] : [aggregate.inputColumnId]),
        ...nextForm.rowValues.map((value) => value.inputColumnId),
      ]),
    });
  };
  const loadNextPage = () => {
    if (!inventoryView?.hasMore || inventoryQuery.isLoading || pageRound !== requestedRoundRef.current) return;
    requestedRoundRef.current = pageRound + 1;
    setPageRound(pageRound + 1);
  };
  const onChoicesScroll = (event: React.UIEvent<HTMLDivElement>) => {
    const element = event.currentTarget;
    if (element.scrollHeight - element.scrollTop - element.clientHeight <= 24) loadNextPage();
  };
  const sourceListIncomplete = Boolean(sourceDiscovery && !sourceDiscovery.loading && (!sourceDiscovery.complete || sourceDiscovery.truncated));
  const error = readinessQuery.error ?? sourcesQuery.error ?? inventoryQuery.error ?? savedCategoryQuery.data?.error;
  const errorText = error instanceof Error ? error.message : error ? 'Coded values could not be loaded.' : undefined;
  const choicesLoading = readinessQuery.isLoading || sourcesQuery.isLoading || inventoryQuery.isLoading || savedCategoryQuery.isLoading;
  if (inventoryReady && !choicesLoading && !errorText && !sourceListIncomplete && inventoryView?.complete && available.length === 0
      && props.form.codedCategories.length === 0) return null;

  return (
    <div data-testid="construction-group-coded-values" className="grid gap-2 rounded-lg border border-blue-200 bg-blue-50/50 p-3">
      <div>
        <p className="m-0 text-sm font-semibold text-slate-800">Coded values</p>
        <p className="mb-0 mt-1 text-xs text-slate-600">Choose coded values to group by.</p>
      </div>
      {readinessQuery.isLoading && !readiness ? <p role="status" className="m-0 text-xs text-slate-600">Checking coded-value availability…</p> : null}
      {!readinessQuery.error && readiness && !inventoryReady ? (
        <p role="status" className="m-0 text-xs text-slate-600">
          {!readiness.buildId
            ? 'No current coded-value catalog is available.'
            : `The coded-value catalog is ${readiness.state}. Choices will appear when it is complete.`}
        </p>
      ) : null}
      {inventoryReady && choicesLoading ? <p role="status" className="m-0 text-xs text-slate-600">Loading coded values…</p> : null}
      {errorText ? <p role="alert" className="m-0 text-xs text-red-700">{errorText}</p> : null}
      {sourceListIncomplete && available.length > 0 ? <p role="status" className="m-0 text-xs text-amber-900">The direct source-field list may be incomplete; additional coded choices may exist.</p> : null}
      {selectedFamily && props.form.codedCategories.some((category) => category.familyKey !== selectedFamily) ? (
        <p role="status" className="m-0 text-xs text-amber-900">These selected concepts come from different source fields. A group can use coded values from one direct source field at a time; remove the conflicting selection to continue.</p>
      ) : selectedFamily ? (
        <p className="m-0 text-xs text-slate-600">Selected source field: {sources.find((source) => codedFamilyKey(source.bindingId, source.sourcePath) === selectedFamily)?.title ?? 'saved direct source'}. Choose coded values from this source field.</p>
      ) : null}
        <div
        aria-label="Coded value choices by direct source field"
        className="grid max-h-[min(14rem,45vh)] grid-cols-1 gap-3 overflow-y-auto"
        onScroll={onChoicesScroll}
        role="region"
        tabIndex={0}
      >
        {sources.map((source) => {
          const sourceEntries = available.filter((entry) => entry.source.choiceId === source.choiceId);
          if (sourceEntries.length === 0 && !inventoryView?.complete) return null;
          return (
            <section key={source.choiceId} aria-label={`Coded values from ${source.title}`} className="grid content-start gap-1">
              <h5 className="m-0 text-xs font-semibold text-slate-700">{source.title}</h5>
              {sourceEntries.map((entry) => {
                const { item, familyKey } = entry;
                const key = codedCategoryKey(familyKey, item.system, item.code);
                const selected = props.form.codedCategories.some((category) => category.key === key);
                const familyConflict = Boolean(selectedFamily && selectedFamily !== familyKey);
                return (
                  <label key={key} data-testid="construction-group-coded-option" className={`flex min-h-10 items-start gap-2 rounded border border-slate-200 bg-white px-2 py-1.5 text-sm ${familyConflict ? 'opacity-60' : 'hover:bg-slate-50'}`}>
                    <input
                      type="checkbox"
                      aria-label={`Group by coded value: ${semanticConceptLabel(item)}`}
                      checked={selected}
                      disabled={props.disabled || familyConflict || props.form.codedCategories.length >= 50 && !selected}
                      onChange={() => toggle(entry)}
                    />
                    <span><span className="block font-medium text-slate-800">{semanticConceptLabel(item)}</span><span className="block text-xs text-slate-500">{item.sourcePath} · {item.valueType} · {item.occurrences.toLocaleString()} observed</span></span>
                  </label>
                );
              })}
            </section>
          );
        })}
        {props.form.codedCategories.filter((category) => !available.some((entry) =>
          category.key === codedCategoryKey(entry.familyKey, entry.item.system, entry.item.code))).map((category) => {
          const familyConflict = Boolean(selectedFamily && selectedFamily !== category.familyKey);
          return (
            <label key={category.key} data-testid="construction-group-coded-option" className={`flex items-start gap-2 rounded border border-slate-200 bg-white px-2 py-1.5 text-sm ${familyConflict ? 'opacity-60' : 'hover:bg-slate-50'}`}>
              <input
                type="checkbox"
                aria-label={`Group by coded value: ${category.display}`}
                checked
                disabled={props.disabled}
                onChange={() => removeSelectedCategory(category)}
              />
              <span>
                <span className="block font-medium text-slate-800">{category.display}</span>
                <span className="block text-xs text-slate-500">{category.system} · {category.code} · selected</span>
                {unavailableSavedKeys.has(category.key) ? <span role="status" className="block text-xs text-amber-800">This coded value is no longer available. Remove it to continue.</span> : null}
              </span>
            </label>
          );
        })}
        {inventoryView?.hasMore ? <p className="m-0 text-xs text-slate-500">Scroll for more choices from these source fields.</p> : null}
      </div>
      {inventoryReady && !choicesLoading && !errorText && available.length === 0 ? (
        <p role="status" className="m-0 text-xs text-slate-600">
          {sourceListIncomplete
            ? 'No ready coded values were found among the listed direct source fields; the source list may be incomplete.'
            : inventoryView?.complete
              ? 'No ready coded values are available from these direct source fields.'
              : 'Coded values from direct source fields are still loading.'}
        </p>
      ) : null}
      {props.form.codedCategories.length > 0 ? <p className="m-0 text-xs text-slate-600">{props.form.codedCategories.length} coded value{props.form.codedCategories.length === 1 ? '' : 's'} selected.</p> : null}
    </div>
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
    <section aria-label="Expand repeated values" data-testid="construction-reshape-expand" className="grid content-start gap-3">
      <p className="m-0 text-sm text-slate-600">Each list item becomes a row; other columns stay with its parent.</p>
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
          <p data-testid="construction-reshape-expand-empty-effect" className="m-0 text-sm text-slate-600">
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

const pivotSourceFieldPathLabel = (path: string): string => {
  const words: string[] = [];
  for (const segment of path.replace(/^root\./, '').split('.')) {
    const readable = segment.replace(/\[\]/g, '').replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/[_-]+/g, ' ').trim();
    if (!readable) continue;
    words.push(readable.toLowerCase() === 'id' ? 'ID' : readable[0].toUpperCase() + readable.slice(1));
  }
  return words.join(' ');
};

const pivotSourceLabel = (choice: SourceInputChoice): string => {
  const rawPath = choice.fieldPath.replace(/^root\./, '');
  const label = choice.label.trim().replace(/^root\./, '');
  const prefixedPath = label.endsWith(`.${rawPath}`) ? label.slice(0, -rawPath.length - 1) : undefined;
  const rawSegments = rawPath.split('.');
  const rawPathResource = rawSegments.length > 1 && /^[A-Z]/.test(rawSegments[0] ?? '') ? rawSegments[0] : undefined;
  const context = prefixedPath ?? (label === rawPath ? rawPathResource : undefined);
  const fieldPath = rawPathResource && context === rawPathResource ? rawSegments.slice(1).join('.') : rawPath;
  const labelIsPath = label === rawPath || Boolean(prefixedPath) || label === choice.fieldPath;
  const field = labelIsPath ? pivotSourceFieldPathLabel(fieldPath) : label;
  return context ? `${context} · ${field}` : field;
};

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
  const repeatedColumns = props.stage.columns.filter((column) => column.cardinality === 'many');
  const discoveryMatches = pivotDiscoveryMatches(props.form, props.stage.id, props.discovery);
  const discoveryComplete = discoveryMatches && props.discovery.status === 'complete';
  const pairUnchanged = props.form.originalPair?.categoryColumnId === props.form.categoryColumnId
    && props.form.originalPair.valueColumnId === props.form.valueColumnId;
  const categoriesKnown = pairUnchanged || discoveryComplete;
  const discoveryEligible = props.canDiscover
    && props.supported
    && (!props.form.pivotSources?.length || props.form.groupKeyIds.length > 0)
    && props.form.categoryColumnId !== ''
    && props.form.valueColumnId !== ''
    && !categoriesKnown;
  const discoveryPending = discoveryEligible
    && discoveryMatches
    && props.discovery?.status === 'loading';
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
  const sourceChoiceForColumn = (columnId: string): SourceInputChoice | undefined => {
    const binding = props.form.pivotSources?.find((candidate) => candidate.columnId === columnId);
    return binding ? props.sourceChoices.find((candidate) => candidate.choiceId === binding.choiceId) : undefined;
  };
  const pivotColumnLabel = (column: ReshapeColumn): string => {
    const sourceChoice = sourceChoiceForColumn(column.id);
    return sourceChoice ? pivotSourceLabel(sourceChoice) : column.label;
  };
  const pivotColumnTitle = (column: ReshapeColumn): string => {
    const sourceChoice = sourceChoiceForColumn(column.id);
    return sourceChoice?.fieldPath ?? (column.name === column.label ? column.name : `${column.name} · ${column.label}`);
  };
  const outputNamesValid = outputNamesAreValid(pivotOutputsFor(props.form, props.stage));
  const numericValue = isNumericColumn(columns.find((column) => column.id === props.form.valueColumnId) ?? { id: '', name: '', label: '', cardinality: 'optional_one' });
  const categoryColumn = columns.find((column) => column.id === props.form.categoryColumnId);
  const valueColumn = columns.find((column) => column.id === props.form.valueColumnId);
  const categoryColumnLabel = categoryColumn ? pivotColumnLabel(categoryColumn) : 'Column names';
  const valueColumnLabel = valueColumn ? pivotColumnLabel(valueColumn) : 'Cell values';
  const rowColumnLabels = groupColumns.map(pivotColumnLabel);
  const roleSummary = rowColumnLabels.length > 0 && categoryColumn && valueColumn
    ? `One row per ${rowColumnLabels.join(', ')}. Each ${categoryColumnLabel} value creates a column containing ${valueColumnLabel}.`
    : 'Example: One row per Patient ID. Each Visit type value creates a column containing Score.';

  const chooseInput = (role: 'group' | 'category' | 'value', selected: string) => {
    if (role === 'group' && selected === '') return;
    const choice = props.sourceChoices.find((candidate) => `source:${candidate.choiceId}` === selected);
    const existing = choice && props.form.pivotSources?.find((binding) => binding.choiceId === choice.choiceId);
    const columnId = choice ? existing?.columnId ?? createOpaqueId('pivot-input') : selected;
    if ((role === 'category' && columnId === props.form.categoryColumnId)
      || (role === 'value' && columnId === props.form.valueColumnId)) return;
    const bindings = choice && !existing ? [...(props.form.pivotSources ?? []), { choiceId: choice.choiceId, columnId }] : props.form.pivotSources ?? [];
    const next = {
      ...props.form,
      groupKeyIds: role === 'group' ? [...new Set([...props.form.groupKeyIds, columnId])] : props.form.groupKeyIds,
      categoryColumnId: role === 'category' ? columnId : props.form.categoryColumnId,
      valueColumnId: role === 'value' ? columnId : role === 'category' && columnId === props.form.valueColumnId ? '' : props.form.valueColumnId,
      categories: [], categoriesPair: undefined, originalPair: undefined,
    };
    const used = new Set([...next.groupKeyIds, next.categoryColumnId, next.valueColumnId]);
    props.onChange({ ...next, pivotSources: bindings.filter((binding) => used.has(binding.columnId)) });
  };
  const sourceOptions = props.sourceChoices.filter((choice) => choice.isPopulated && !props.form.pivotSources?.some((binding) => binding.choiceId === choice.choiceId));

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
    <section aria-label="Pivot categories into columns" data-testid="construction-reshape-pivot" className="construction-reshape-pivot-editor grid gap-3 rounded-lg border border-slate-200 p-3">
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      {columns.length === 0 && props.sourceChoices.length === 0 ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Loom has not returned scalar fields for this stage.</p>
      ) : (
        <>
          <div className="grid items-start gap-3 sm:grid-cols-3">
            <fieldset className="grid min-w-0 content-start gap-2 rounded border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
              <legend className="px-1 text-sm font-semibold text-slate-800">One row per</legend>
              <div className="grid max-h-28 gap-1 overflow-auto">
                {columns.map((column) => {
                  const label = pivotColumnLabel(column);
                  return (
                    <label key={column.id} title={pivotColumnTitle(column)} className="flex items-center gap-2 text-sm text-slate-700">
                      <input
                        type="checkbox"
                        aria-label={`Pivot group ${label}`}
                        checked={props.form.groupKeyIds.includes(column.id)}
                        disabled={props.disabled || !props.supported || column.id === props.form.categoryColumnId || column.id === props.form.valueColumnId}
                        onChange={(event) => toggleGroup(column.id, event.currentTarget.checked)}
                      />
                      {label}
                    </label>
                  );
                })}
              </div>
              {sourceOptions.length > 0 ? <select aria-label="Add pivot group field" value="" disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('group', event.currentTarget.value)} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                <option value="">Add another field</option>
                {sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)}</option>)}
              </select> : null}
              {props.form.groupKeyIds.length === 0 && props.supported ? <p role="status" className="text-xs text-amber-900">Choose one or more fields to identify each row{props.form.pivotSources?.length ? '. Choose a row identity before finding categories from source fields.' : '.'}</p> : null}
            </fieldset>
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              New column names
              <select aria-label="Pivot category field" value={props.form.categoryColumnId} disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('category', event.currentTarget.value)} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                <option value="">Choose a column-name field</option>
                {columns.filter((column) => !props.form.groupKeyIds.includes(column.id)).map((column) => <option key={column.id} value={column.id} title={pivotColumnTitle(column)}>{pivotColumnLabel(column)}</option>)}
                {sourceOptions.length > 0 ? <optgroup label="Source fields">{sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)}</option>)}</optgroup> : null}
              </select>
            </label>
            <label className="grid gap-1 text-sm font-medium text-slate-700">
              Cell values
              <select aria-label="Pivot values field" value={props.form.valueColumnId} disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('value', event.currentTarget.value)} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                <option value="">Choose a cell-values field</option>
                {columns.filter((column) => !props.form.groupKeyIds.includes(column.id) && column.id !== props.form.categoryColumnId).map((column) => <option key={column.id} value={column.id} title={pivotColumnTitle(column)}>{pivotColumnLabel(column)} ({column.type ?? 'unknown type'})</option>)}
                {sourceOptions.length > 0 ? <optgroup label="Source fields">{sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)} ({choice.logicalType})</option>)}</optgroup> : null}
              </select>
            </label>
          </div>
          <p data-testid="construction-reshape-pivot-role-summary" className="text-sm text-slate-600">
            {roleSummary}
          </p>
          {repeatedColumns.length > 0 ? (
            <details className="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-950">
              <summary className="cursor-pointer">{repeatedColumns.length} {repeatedColumns.length === 1 ? 'column contains a list' : 'columns contain lists'}</summary>
              <p className="mt-1">Use “Make one row per list value” to expand these columns before choosing Pivot fields.</p>
            </details>
          ) : null}
          {!props.canDiscover && !categoriesKnown ? <p role="status" className="text-sm text-amber-900">Category discovery is unavailable for this stage.</p> : null}
          {props.canDiscover && props.supported && !categoriesKnown && (!props.form.pivotSources?.length || props.form.groupKeyIds.length > 0) && (props.form.categoryColumnId === '' || props.form.valueColumnId === '') ? <p role="status" className="text-sm text-slate-600">Choose fields for New column names and Cell values to find categories.</p> : null}
          {discoveryPending ? <p role="status" className="text-sm text-slate-600">Finding categories…</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'failed' ? <div role="status" className="text-sm text-amber-900"><p>{props.discovery.reason}</p><button type="button" onClick={props.onDiscover} disabled={props.disabled || !discoveryEligible} className="mt-1 text-blue-800 underline">Retry finding categories</button></div> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'limit-exceeded' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason} (Maximum supported categories: {props.discovery.limit}.)</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'missing-unsupported' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason}</p> : null}
          {categoriesKnown ? (
            <>
              <p role="status" data-testid="construction-reshape-pivot-category-summary" className="rounded bg-slate-50 px-3 py-2 text-sm text-slate-700">
                Selected {selectedCategoryLabels.length} of {categoryCount} categories{selectedCategoryLabels.length === 0 ? ': none' : showSelectedCategoryLabels ? `: ${displayedSelectedCategoryLabels.join(', ')}${additionalSelectedCategoryCount > 0 ? `, and ${additionalSelectedCategoryCount} more` : ''}` : '. Review the list to change them.'}
              </p>
              {discoveryComplete && categoryCount === 0 ? <p role="status" className="text-sm text-slate-600">No categories were found for this pair. Choose another New column names field or adjust the rows before continuing.</p> : null}
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
              <div className="grid gap-3 sm:grid-cols-2">
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
    <section aria-label="Turn columns into rows" data-testid="construction-reshape-unpivot" className="grid content-start gap-3">
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      <fieldset className="grid gap-1" disabled={props.disabled || !props.supported}>
        <legend className="m-0 text-sm font-semibold text-slate-800">Columns to turn into rows</legend>
        <div className="grid gap-1 sm:grid-cols-2">
          {scalarColumnsFor(props.stage).map((column) => {
            const input = props.form.inputs.find((candidate) => candidate.columnId === column.id);
            return (
              <label key={column.id} className="flex items-center gap-2 rounded px-2 py-1.5 text-sm text-slate-800 hover:bg-slate-50">
                <input type="checkbox" aria-label={`Unpivot ${column.label}`} checked={Boolean(input)} disabled={props.disabled || !props.supported} onChange={(event) => toggleInput(column, event.currentTarget.checked)} />
                {column.label}
              </label>
            );
          })}
        </div>
      </fieldset>
      <p data-testid="construction-unpivot-effect" className="m-0 text-sm text-slate-600">
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
