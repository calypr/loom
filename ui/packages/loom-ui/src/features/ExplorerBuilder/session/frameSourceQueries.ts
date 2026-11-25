import { sameCatalogSnapshot } from '../../../contracts/identity';
import { useLoomClient, useQuery } from '../../../react';

interface FrameQueryScope {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
  readonly query: string;
}

export function useFrameCategories({
  project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot, query,
  frameId, frameKey, currentCursor,
}: FrameQueryScope & {
  readonly frameId: string;
  readonly frameKey: string;
  readonly currentCursor?: string;
}) {
  const client = useLoomClient();
  return useQuery(async (signal) => {
    const response = await client.browseSemanticInventory({
      project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
      frameId: frameId, query, cursor: currentCursor, limit: 50,
      requestId: `frame-categories-${window.crypto.randomUUID()}`,
    }, signal);
    if (response.frameId !== frameId || response.frameSource?.id !== frameId) {
      throw new Error('Loom returned categories for a different framing source.');
    }
    if (response.state !== 'complete') {
      throw new Error(`The coded-value inventory is ${response.state}. Try again when it is complete.`);
    }
    return { entries: response.entries, nextCursor: response.nextCursor };
  }, [authResourcePath, client, currentCursor, explorerId, frameId, frameKey, outputId, project, query, rowRoot, snapshotToken]);
}

export function useFrameSourceOptions({
  project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot, query,
  cursor, sourceResourceType, sourcePickerEnabled,
}: FrameQueryScope & {
  readonly cursor?: string;
  readonly sourceResourceType?: string;
  readonly sourcePickerEnabled: boolean;
}) {
  const client = useLoomClient();
  return useQuery(async (signal) => {
    const response = await client.browseFrameSourceOptions({
      project, explorerId, authResourcePath, snapshotToken, outputId, resourceType: sourceResourceType, query, cursor, limit: 20,
      requestId: `frame-source-options-${window.crypto.randomUUID()}`,
    }, signal);
    if (response.outputId !== outputId || !sameCatalogSnapshot(response.snapshotToken, snapshotToken)) {
      throw new Error('Loom returned framing choices for another table or dataset.');
    }
    return { sources: response.sources, nextCursor: response.nextCursor };
  }, [authResourcePath, client, cursor, explorerId, outputId, project, query, rowRoot, snapshotToken, sourceResourceType], sourcePickerEnabled);
}
