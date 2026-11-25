import type { LoomClient } from '../../client/api';
import { useQuery } from '../../react';
import type { SemanticInventoryItem } from '../../contracts/types';

export interface ColumnRuleInventoryScope {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly rowRoot: string;
  readonly resourceType: string;
}

export function useColumnRuleInventory(client: LoomClient, scope: ColumnRuleInventoryScope, query: string, enabled: boolean) {
  const { project, explorerId, authResourcePath, snapshotToken, rowRoot, resourceType } = scope;
  return useQuery(async (signal) => {
    const entries: SemanticInventoryItem[] = [];
    const seenCursors = new Set<string>();
    let cursor: string | undefined;
    let inventoryIdentity: string | undefined;
    do {
      const page = await client.browseSemanticInventory({
        project, explorerId, authResourcePath, snapshotToken, rowRoot, resourceType, query, cursor, limit: 50,
      }, signal);
      if (page.state !== 'complete') {
        throw new Error('The observed field inventory is not ready.');
      }
      const pageIdentity = JSON.stringify([page.contextToken, page.buildId]);
      if (inventoryIdentity !== undefined && inventoryIdentity !== pageIdentity) {
        throw new Error('The observed field inventory changed. Open Advanced again to reload it.');
      }
      inventoryIdentity = pageIdentity;
      entries.push(...page.entries.filter(entry => entry.resourceType === resourceType));
      cursor = page.nextCursor;
      if (cursor) {
        if (seenCursors.has(cursor)) throw new Error('The observed field inventory repeated a page.');
        seenCursors.add(cursor);
      }
    } while (cursor && !signal.aborted);
    return entries;
  }, [client, project, explorerId, authResourcePath, snapshotToken, rowRoot, resourceType, query], enabled);
}

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
