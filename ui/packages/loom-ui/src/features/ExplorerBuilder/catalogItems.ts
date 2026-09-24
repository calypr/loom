import type {
  ConstructionChoice,
  ConstructionChoiceForm,
  ConstructionChoiceSearchSource,
  ConstructionChoiceSelection,
  FeatureCatalogBrowseResponse,
  FeatureCatalogItem,
  FeatureCatalogSection,
} from '../../types';

export type CatalogItem = FeatureCatalogItem;

export type CatalogChoiceIntent = {
  readonly constructionChoice: ConstructionChoiceSelection;
  readonly title?: string;
};

export type CatalogChoiceGroup = {
  readonly item: CatalogItem;
  readonly choices: ReadonlyArray<ConstructionChoice>;
  readonly complete: boolean;
  readonly truncated: boolean;
  readonly requiresSourceChoice: boolean;
  readonly rowsWithValue?: number;
};

export type CatalogItemAvailability =
  | { readonly selectable: true }
  | { readonly selectable: false; readonly reason: string };

export const catalogItemKey = (item: CatalogItem): string => item.featureId;

export const catalogItemLabel = (item: CatalogItem): string => item.title;

export const catalogItemConstructionChoice = (
  item: CatalogItem,
): ConstructionChoice | undefined => item.constructionChoice;

export const catalogItemAvailability = (
  item: CatalogItem,
  section: FeatureCatalogSection,
  state: FeatureCatalogBrowseResponse['state'],
): CatalogItemAvailability => {
  if (section === 'NEEDS_REVIEW') {
    return {
      selectable: false,
      reason: 'This feature needs review before it can be added.',
    };
  }
  if (state !== 'complete') {
    return {
      selectable: false,
      reason: 'This feature is unavailable until Loom finishes building the catalog.',
    };
  }
  if (item.coverage.state === 'INDEXED' && item.coverage.rowsWithValue === 0) {
    return { selectable: false, reason: 'No current table rows have a value for this feature.' };
  }
  switch (item.readiness.status) {
    case 'READY':
    case 'READY_WITH_WARNING':
      return { selectable: true };
    case 'NEEDS_MAPPING':
    case 'UNSUPPORTED':
      return { selectable: false, reason: item.readiness.message };
    default: {
      const exhaustive: never = item.readiness.status;
      return exhaustive;
    }
  }
};

export const catalogItemSearchSource = (
  item: CatalogItem,
  context: { readonly contextToken: string; readonly buildId: string },
): ConstructionChoiceSearchSource => {
  switch (item.kind) {
    case 'DIRECT_FIELD':
      return { kind: 'FIELD', candidateId: item.source.candidateId };
    case 'SEMANTIC_FEATURE':
      return {
        kind: 'SEMANTIC',
        contextToken: context.contextToken,
        buildId: context.buildId,
        conceptId: item.source.conceptId,
        bindingId: item.source.bindingId,
      };
    default: {
      const exhaustive: never = item;
      return exhaustive;
    }
  }
};

export const catalogItemDefaultForm = (
  choice: ConstructionChoice,
): ConstructionChoiceForm | undefined => {
  const defaults = choice.options.filter((option) => option.decision === 'DEFAULT');
  return defaults.length === 1 ? defaults[0]?.form : undefined;
};
