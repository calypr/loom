import type {
  ConstructionChoiceForm,
  ConstructionRouteStep,
  ExplorerBuilderColumn,
  ExplorerBuilderRouteNode,
  FeatureCatalogItem,
} from '../../types';
import { catalogItemAvailability } from './catalogItems';

type CodedItem = Extract<FeatureCatalogItem, { readonly kind: 'SEMANTIC_FEATURE' }>;

export type PivotCode = {
  readonly item: CodedItem;
  readonly form: ConstructionChoiceForm;
  readonly code: string;
  readonly sourceRecords: number | undefined;
  readonly configured: boolean;
};

export type PivotFamily = {
  readonly id: string;
  readonly title: string;
  readonly relationship: string;
  readonly codes: ReadonlyArray<PivotCode>;
  readonly sourceRecords: number | undefined;
  readonly recurrentCodes: number;
  readonly multiCodeRowsPossible: boolean;
};

export type ConfiguredPivotContext = {
  readonly route: ExplorerBuilderRouteNode;
  readonly columns: ReadonlyArray<ExplorerBuilderColumn>;
};

const choiceOccurrence = (
  root: ExplorerBuilderRouteNode,
  steps: ReadonlyArray<ConstructionRouteStep>,
): string | undefined => {
  let current = root;
  for (const step of steps) {
    if (current.resourceType !== step.fromResourceType) return undefined;
    const next = current.children?.find((child) =>
      child.catalogEdgeId === step.edgeId &&
      child.relationship === step.relationship &&
      child.resourceType === step.toResourceType,
    );
    if (!next) return undefined;
    current = next;
  }
  return current.occurrenceId;
};

const isConfiguredCode = (item: CodedItem, form: ConstructionChoiceForm, context?: ConfiguredPivotContext): boolean => {
  const choice = item.constructionChoice;
  if (!context || !choice || choice.source.kind !== 'SEMANTIC') return false;
  const source = choice.source;
  const occurrenceId = choiceOccurrence(context.route, choice.route);
  if (!occurrenceId || !source.keySelector || !source.system || !source.code) return false;
  return context.columns.some((column) => {
    if (column.occurrenceId !== occurrenceId || column.source.kind !== 'codedValue') return false;
    const lookup = column.source.lookup;
    return lookup.projectionMode === form &&
      lookup.key.system === source.system && lookup.key.code === source.code &&
      lookup.binding.keyPath === source.keySelector &&
      lookup.binding.valuePath === source.valueSelector &&
      lookup.binding.logicalType === source.logicalType &&
      (!source.owningScope || lookup.binding.ownerPath === source.owningScope) &&
      (!source.choiceArm || lookup.binding.choiceArms?.includes(source.choiceArm));
  });
};

export const pivotFamilies = (
  items: ReadonlyArray<FeatureCatalogItem>,
  configured?: ConfiguredPivotContext,
): ReadonlyArray<PivotFamily> => {
  const groups = new Map<string, { family: NonNullable<CodedItem['pivotFamily']>; codes: PivotCode[] }>();
  for (const item of items) {
    if (item.kind !== 'SEMANTIC_FEATURE' || !item.pivotFamily || !item.constructionChoice) continue;
    if (!catalogItemAvailability(item, 'CONCEPTS', 'complete').selectable) continue;
    const form = item.pivotFamily.form;
    const preserving = item.constructionChoice.options.some((option) =>
      option.form === form && option.support === 'SUPPORTED' &&
      option.preservation === 'PRESERVING' && option.rowEffect === 'PRESERVES_ROW_GRAIN',
    );
    const code = item.pivotFamily.code;
    if (!preserving || !code) continue;
    const group = groups.get(item.pivotFamily.id) ?? { family: item.pivotFamily, codes: [] };
    group.codes.push({ item, form, code, sourceRecords: item.sourceRecords, configured: isConfiguredCode(item, form, configured) });
    groups.set(item.pivotFamily.id, group);
  }
  return [...groups].map(([id, group]): PivotFamily => {
    const codes = group.codes.sort((left, right) =>
      (right.sourceRecords ?? 0) - (left.sourceRecords ?? 0) ||
      left.item.title.localeCompare(right.item.title) ||
      left.code.localeCompare(right.code),
    );
    return {
      id,
      title: group.family.title,
      relationship: group.family.relationship,
      codes,
      sourceRecords: codes.every((candidate) => candidate.sourceRecords !== undefined)
        ? codes.reduce((total, candidate) => total + (candidate.sourceRecords ?? 0), 0)
        : undefined,
      recurrentCodes: codes.filter((candidate) => (candidate.sourceRecords ?? 0) >= 2).length,
      multiCodeRowsPossible: group.family.multiCodeRowsPossible,
    };
  }).sort((left, right) =>
    right.recurrentCodes - left.recurrentCodes ||
    right.codes.length - left.codes.length ||
    (right.sourceRecords ?? 0) - (left.sourceRecords ?? 0) ||
    left.title.localeCompare(right.title) ||
    left.id.localeCompare(right.id),
  );
};
