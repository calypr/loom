import type { SemanticInventoryItem } from '../../../contracts/types';

export function observedIdentifierSystems(items: ReadonlyArray<SemanticInventoryItem>) {
  return [...new Set(items
    .filter(item => item.occurrences > 0 && item.sourcePath === 'identifier[]' && item.system)
    .map(item => item.system))].sort();
}

export function observedExtensionPaths(items: ReadonlyArray<SemanticInventoryItem>) {
  return [...new Set(items
    .filter(item => item.occurrences > 0 && item.valueSelector &&
      /^extension\[\](\.extension\[\])*$/.test(item.sourcePath))
    .map(item => item.sourcePath))].sort();
}

export function observedCategorySystems(items: ReadonlyArray<SemanticInventoryItem>) {
  return [...new Set(items
    .filter(item => item.occurrences > 0 && item.system && item.sourcePath.startsWith('category[]'))
    .map(item => item.system))].sort();
}
