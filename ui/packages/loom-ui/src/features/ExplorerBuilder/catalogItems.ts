import type {
  ConstructionChoice,
  ConstructionChoiceForm,
  ConstructionChoiceSelection,
  ExplorerBuilderCatalog,
  ExplorerBuilderCandidate,
  FieldChoiceSource,
  SemanticInventoryItem,
} from '../../types';

export type CatalogItem =
  | {
      readonly kind: 'FIELD';
      readonly candidate: ExplorerBuilderCandidate;
      readonly constructionChoice: ConstructionChoice & {
        readonly source: FieldChoiceSource;
      };
    }
  | {
      readonly kind: 'SEMANTIC';
      readonly item: SemanticInventoryItem;
      readonly constructionChoice?: ConstructionChoice;
    };

export type CatalogChoiceIntent = {
  readonly constructionChoice: ConstructionChoiceSelection;
  readonly title?: string;
  readonly relatedSource?: {
    readonly choice: ConstructionChoice;
    readonly candidate: ExplorerBuilderCandidate;
  };
};

export type CatalogChoiceGroup = {
  readonly item: CatalogItem;
  readonly choices: ReadonlyArray<ConstructionChoice>;
  readonly complete: boolean;
  readonly truncated: boolean;
};

export type CatalogItemAvailability =
  | { readonly selectable: true }
  | { readonly selectable: false; readonly reason: string };

export type CatalogSourceOption =
  | {
      readonly kind: 'ROOT';
      readonly key: string;
      readonly label: string;
      readonly resourceType: string;
      readonly sourceNodeId?: string;
    }
  | {
      readonly kind: 'RELATED';
      readonly key: string;
      readonly label: string;
      readonly resourceType: string;
      readonly sourceNodeId: string;
    }
  | {
      readonly kind: 'SAVED_OCCURRENCE';
      readonly key: string;
      readonly label: string;
      readonly resourceType?: string;
      readonly sourceNodeId: string;
    };

export const catalogSourceOptions = (
  catalog: ExplorerBuilderCatalog,
  rowRoot: string,
  savedOccurrenceNodeId?: string,
): ReadonlyArray<CatalogSourceOption> => {
  const rootNodes = catalog.nodes.filter((node) => node.resourceType === rowRoot);
  const rootSeeds = rootNodes.filter((node) => node.rowRootEligible);
  const seeds = rootSeeds.length > 0 ? rootSeeds : rootNodes;
  const reachableNodeIds = new Set(seeds.map((node) => node.nodeId));
  const queue = seeds.map((node) => node.nodeId);

  for (let queueIndex = 0; queueIndex < queue.length; queueIndex += 1) {
    const currentNodeId = queue[queueIndex];
    if (!currentNodeId) continue;
    for (const edge of catalog.edges) {
      const nextNodeId = edge.fromNodeId === currentNodeId
        ? edge.toNodeId
        : edge.toNodeId === currentNodeId
          ? edge.fromNodeId
          : undefined;
      if (!nextNodeId || reachableNodeIds.has(nextNodeId)) continue;
      reachableNodeIds.add(nextNodeId);
      queue.push(nextNodeId);
    }
  }

  const savedRootNode = rootNodes.find(
    (node) => node.nodeId === savedOccurrenceNodeId,
  );
  const options: CatalogSourceOption[] = [{
    kind: 'ROOT',
    key: `root:${rowRoot}`,
    label: `${rowRoot} — table rows${savedRootNode ? ' · selected occurrence' : ''}`,
    resourceType: rowRoot,
    ...(savedRootNode ? { sourceNodeId: savedRootNode.nodeId } : {}),
  }];

  for (const node of catalog.nodes) {
    if (!reachableNodeIds.has(node.nodeId) || node.resourceType === rowRoot) continue;
    options.push({
      kind: 'RELATED',
      key: `node:${node.nodeId}`,
      label: `${node.resourceType} — related source${node.nodeId === savedOccurrenceNodeId ? ' · selected occurrence' : ''}`,
      resourceType: node.resourceType,
      sourceNodeId: node.nodeId,
    });
  }

  if (savedOccurrenceNodeId && !options.some(
    (option) => option.sourceNodeId === savedOccurrenceNodeId,
  )) {
    const savedNode = catalog.nodes.find(
      (node) => node.nodeId === savedOccurrenceNodeId,
    );
    options.push({
      kind: 'SAVED_OCCURRENCE',
      key: `occurrence:${savedOccurrenceNodeId}`,
      label: savedNode
        ? `${savedNode.resourceType} — selected occurrence`
        : 'Selected occurrence',
      ...(savedNode ? { resourceType: savedNode.resourceType } : {}),
      sourceNodeId: savedOccurrenceNodeId,
    });
  }

  return options;
};

export const catalogItemKey = (item: CatalogItem): string => {
  switch (item.kind) {
    case 'FIELD':
      return JSON.stringify(['FIELD', item.constructionChoice.choiceId]);
    case 'SEMANTIC':
      return JSON.stringify([
        'SEMANTIC',
        item.constructionChoice?.choiceId ?? item.item.conceptId,
        item.item.bindingId,
      ]);
    default: {
      const exhaustive: never = item;
      return exhaustive;
    }
  }
};

export const catalogItemLabel = (item: CatalogItem): string => {
  switch (item.kind) {
    case 'FIELD':
      return item.candidate.label.trim() || item.candidate.fieldPath;
    case 'SEMANTIC':
      return item.item.display.trim() || item.item.code.trim() || item.item.sourcePath;
    default: {
      const exhaustive: never = item;
      return exhaustive;
    }
  }
};

export const isRelatedFieldCatalogItem = (
  item: CatalogItem,
  rowRoot: string,
): boolean => item.kind === 'FIELD' && (
  item.constructionChoice.source.resourceType !== rowRoot ||
  item.constructionChoice.route.length > 0
);

export const catalogChoiceIntent = ({
  item,
  choice,
  form,
  rowRoot,
}: {
  readonly item: CatalogItem;
  readonly choice: ConstructionChoice;
  readonly form: ConstructionChoiceForm;
  readonly rowRoot?: string;
}): CatalogChoiceIntent => {
  const relatedSource = item.kind === 'FIELD' &&
    rowRoot !== undefined &&
    form === 'ALL' &&
    isRelatedFieldCatalogItem(item, rowRoot) &&
    choice.source.kind === 'FIELD' &&
    choice.source.candidateId === item.candidate.candidateId &&
    choice.source.nodeId === item.candidate.nodeId &&
    choice.source.resourceType === item.constructionChoice.source.resourceType &&
    choice.source.path === item.candidate.fieldPath &&
    choice.source.cardinality === item.candidate.cardinality &&
    (choice.source.cardinality === 'required_one' || choice.source.cardinality === 'optional_one') &&
    choice.options.some((option) =>
      option.form === 'ALL' &&
      option.shape === 'LIST' &&
      option.support === 'SUPPORTED'
    )
      ? { choice, candidate: item.candidate }
      : undefined;

  return {
    constructionChoice: { choiceId: choice.choiceId, form },
    title: catalogItemLabel(item),
    ...(relatedSource ? { relatedSource } : {}),
  };
};

export const catalogItemConstructionChoice = (
  item: CatalogItem,
): ConstructionChoice | undefined => {
  switch (item.kind) {
    case 'FIELD':
      return item.constructionChoice;
    case 'SEMANTIC':
      return item.constructionChoice;
    default: {
      const exhaustive: never = item;
      return exhaustive;
    }
  }
};

export const catalogItemAvailability = (
  item: CatalogItem,
): CatalogItemAvailability => {
  switch (item.kind) {
    case 'FIELD':
      return item.constructionChoice.source.kind === 'FIELD'
        ? { selectable: true }
        : {
            selectable: false,
            reason: 'Loom did not provide a field construction choice for this source.',
          };
    case 'SEMANTIC': {
      const readiness = item.item.readiness;
      if (readiness.status === 'NEEDS_MAPPING' || readiness.status === 'UNSUPPORTED') {
        return { selectable: false, reason: readiness.message };
      }
      if (!item.constructionChoice) {
        return {
          selectable: false,
          reason: 'Loom has not provided a compiler-proved output form for this concept.',
        };
      }
      if (item.constructionChoice.source.kind !== 'SEMANTIC') {
        return {
          selectable: false,
          reason: 'Loom returned a construction choice for a different source kind.',
        };
      }
      if (
        item.constructionChoice.source.conceptId !== item.item.conceptId ||
        item.constructionChoice.source.bindingId !== item.item.bindingId ||
        item.constructionChoice.source.resourceType !== item.item.resourceType
      ) {
        return {
          selectable: false,
          reason: 'Loom returned a construction choice for a different concept binding.',
        };
      }
      return { selectable: true };
    }
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

export const directCatalogChoiceIntents = (
  items: ReadonlyArray<CatalogItem>,
  rowRoot: string,
): ReadonlyArray<CatalogChoiceIntent> | undefined => {
  if (items.length === 0) return undefined;
  const selections: CatalogChoiceIntent[] = [];
  for (const item of items) {
    const choice = catalogItemConstructionChoice(item);
    if (
      !choice ||
      !catalogItemAvailability(item).selectable ||
      choice.options.length !== 1
    ) {
      return undefined;
    }
    const [option] = choice.options;
    if (!option || option.decision !== 'DEFAULT') return undefined;
    const intent = catalogChoiceIntent({
      item,
      choice,
      form: option.form,
      rowRoot,
    });
    if (isRelatedFieldCatalogItem(item, rowRoot) && !intent.relatedSource) return undefined;
    selections.push(intent);
  }
  return selections;
};

export const fieldCatalogItems = (
  catalog: ExplorerBuilderCatalog,
  rowRoot: string,
  resourceType: string | undefined,
  query: string,
  sourceNodeId?: string,
): ReadonlyArray<CatalogItem> => {
  const search = query.trim().toLowerCase();
  const rootPriority = (item: CatalogItem): number =>
    item.kind === 'FIELD' && item.constructionChoice.source.resourceType === rowRoot
      ? 0
      : 1;
  return (catalog.candidates ?? []).flatMap((candidate) => {
    const choice = candidate.constructionChoice;
    if (!choice) return [];
    const source = choice.source;
    const node = catalog.nodes.find(
      (candidateNode) => candidateNode.nodeId === candidate.nodeId,
    );
    if (
      !node ||
      (resourceType
        ? node.resourceType !== resourceType
        : !search && node.resourceType !== rowRoot) ||
      (sourceNodeId && node.nodeId !== sourceNodeId) ||
      source.kind !== 'FIELD' ||
      source.candidateId !== candidate.candidateId ||
      source.nodeId !== node.nodeId ||
      source.resourceType !== node.resourceType ||
      source.path !== candidate.fieldPath ||
      source.cardinality !== candidate.cardinality
    ) {
      return [];
    }

    const item: CatalogItem = {
      kind: 'FIELD',
      candidate,
      constructionChoice: { ...choice, source },
    };
    if (!search) return [item];
    const searchable = [
      candidate.label,
      candidate.fieldPath,
      candidate.logicalType,
      candidate.cardinality,
    ].join(' ').toLowerCase();
    return searchable.includes(search) ? [item] : [];
  }).sort((left, right) => rootPriority(left) - rootPriority(right));
};

export const rootFieldCatalogItems = (
  catalog: ExplorerBuilderCatalog,
  rowRoot: string,
  query: string,
): ReadonlyArray<CatalogItem> => fieldCatalogItems(catalog, rowRoot, rowRoot, query);

export const semanticCatalogItems = (
  items: ReadonlyArray<SemanticInventoryItem>,
  resourceType?: string,
  sourceNodeId?: string,
): ReadonlyArray<CatalogItem> => items
  .filter((item) =>
    (!resourceType || item.resourceType === resourceType) &&
    (!sourceNodeId || item.constructionChoice?.source.nodeId === sourceNodeId),
  )
  .map((item) => ({
    kind: 'SEMANTIC',
    item,
    constructionChoice: item.constructionChoice,
  }));
