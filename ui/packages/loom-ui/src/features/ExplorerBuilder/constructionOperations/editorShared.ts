import { constructionSchema } from '../../../types';
import type { LoomClient } from '../../../api';
import type { Construction, ConstructionCapabilitiesResponse, ConstructionStageDescriptor, ConstructionStep } from '../../../types';

export type CodedPivotContext = {
  readonly client: Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory' | 'getConstructionColumnPopulation'>;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
};

export const createOpaqueId = (prefix: string): string => {
  const randomPart = globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2);
  return `${prefix}_${randomPart}`;
};

export const normalizedLogicalType = (type: string | undefined): string =>
  (type ?? '').trim().toLowerCase().replace(/[\s-]+/g, '_');

export const isNumericColumn = (column: ConstructionStageDescriptor['columns'][number]): boolean => {
  const type = normalizedLogicalType(column.type);
  return type === 'integer' || type === 'decimal' || type === 'number';
};

export const normalizedName = (value: string): string => {
  const normalized = value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'column';
  return /^[0-9]/.test(normalized) ? `column_${normalized}` : normalized;
};

export const uniqueName = (base: string, used: ReadonlySet<string>): string => {
  const normalized = normalizedName(base);
  if (!used.has(normalized)) return normalized;
  let suffix = 2;
  while (used.has(`${normalized}_${suffix}`)) suffix += 1;
  return `${normalized}_${suffix}`;
};

export const sourceFieldPathLabel = (path: string): string => {
  const words: string[] = [];
  for (const segment of path.replace(/^root\./, '').split('.')) {
    const readable = segment.replace(/\[\]/g, '').replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/[_-]+/g, ' ').trim();
    if (!readable) continue;
    words.push(readable.toLowerCase() === 'id' ? 'ID' : readable[0].toUpperCase() + readable.slice(1));
  }
  return words.join(' ');
};

export const sourceLabel = (choice: Pick<ConstructionStageDescriptor['columns'][number], 'label'> & { readonly fieldPath: string }): string => {
  const rawPath = choice.fieldPath.replace(/^root\./, '');
  const label = choice.label.trim().replace(/^root\./, '');
  const prefixedPath = label.endsWith(`.${rawPath}`) ? label.slice(0, -rawPath.length - 1) : undefined;
  const rawSegments = rawPath.split('.');
  const rawPathResource = rawSegments.length > 1 && /^[A-Z]/.test(rawSegments[0] ?? '') ? rawSegments[0] : undefined;
  const context = prefixedPath ?? (label === rawPath ? rawPathResource : undefined);
  const fieldPath = rawPathResource && context === rawPathResource ? rawSegments.slice(1).join('.') : rawPath;
  const labelIsPath = label === rawPath || Boolean(prefixedPath) || label === choice.fieldPath;
  const field = labelIsPath ? sourceFieldPathLabel(fieldPath) : label;
  return context ? `${context} · ${field}` : field;
};

export const editorContextKey = (
  capabilities: Pick<ConstructionCapabilitiesResponse, 'snapshotToken' | 'draftVersion' | 'draftDigest' | 'outputId' | 'stageId'>,
  editingStep: Pick<ConstructionStep, 'id'> | undefined,
  ...context: ReadonlyArray<string>
): string => [
  capabilities.snapshotToken,
  capabilities.draftVersion,
  capabilities.draftDigest,
  capabilities.outputId,
  capabilities.stageId,
  editingStep?.id ?? 'new',
  ...context,
].join(':');

type SourceInputChoice = Pick<NonNullable<ConstructionCapabilitiesResponse['filterSourceInput']>['choices'][number], 'choiceId' | 'fieldPath' | 'label' | 'logicalType'>;
type SourceInputSelection = { readonly choiceId: string; readonly columnId: string };

export const stageWithSourceInputs = ({ stage, selections, choices, kind }: {
  readonly stage: ConstructionStageDescriptor;
  readonly selections: ReadonlyArray<SourceInputSelection>;
  readonly choices: ReadonlyArray<SourceInputChoice>;
  readonly kind: 'FILTER' | 'DERIVE' | 'PIVOT' | 'UNPIVOT' | 'EXPAND';
}): ConstructionStageDescriptor => {
  const sequentialNames = kind === 'PIVOT' || kind === 'UNPIVOT';
  const usedNames = new Set(stage.columns.map((column) => column.name.toLowerCase()));
  const inputs = selections.flatMap((selection) => {
    if ((kind === 'FILTER' || kind === 'DERIVE') && selection.columnId.trim() === '') return [];
    if (stage.columns.some((column) => column.id === selection.columnId)) return [];
    const choice = choices.find((candidate) => candidate.choiceId === selection.choiceId);
    if (!choice) return [];
    const name = uniqueName(choice.fieldPath, usedNames);
    if (sequentialNames) usedNames.add(name.toLowerCase());
    const column: ConstructionStageDescriptor['columns'][number] = {
      id: selection.columnId, name, label: choice.label, type: choice.logicalType,
      cardinality: kind === 'EXPAND' ? 'many' : 'optional_one',
    };
    return [column];
  });
  return inputs.length || sequentialNames ? { ...stage, columns: [...stage.columns, ...inputs] } : stage;
};

export const constructionWithStep = ({ construction, editingStep, step }: {
  readonly construction: Construction;
  readonly editingStep: Pick<ConstructionStep, 'id'> | undefined;
  readonly step: ConstructionStep;
}): Construction | undefined => {
  const parsed = constructionSchema.safeParse({
    ...construction,
    steps: editingStep
      ? construction.steps.map((candidate) => candidate.id === editingStep.id ? step : candidate)
      : [...construction.steps, step],
  });
  return parsed.success ? parsed.data : undefined;
};
