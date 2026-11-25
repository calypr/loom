import { sameCatalogSnapshot, sameDraftVersion, sameDraftDigest } from '../../../contracts/identity';
import { RequestOwner } from '../../../data/requests';
import { useRef, useState } from 'react';
import type { useLoomClient } from '../../../react';
import type { ConstructionCombineCatalog, ConstructionCombinePublishedRevision } from '../model/combineEditorTypes';
import { selectedTable, type BuilderAuthoringState } from '../model/authoring';

type ConstructionCombineCatalogLoad = {
  readonly identity: string;
  readonly catalog: ConstructionCombineCatalog;
  readonly loadingMore: boolean;
  readonly loadMoreError?: string;
};

type CombineCatalogOwner = {
  readonly ownerKey: string;
  readonly explorerId: string;
};

type UseCombineCatalogOptions = {
  readonly ownerKey: string;
  readonly explorerId: string;
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly state: BuilderAuthoringState;
  readonly draftAccess: { getState(): BuilderAuthoringState };
  readonly loomClient: ReturnType<typeof useLoomClient>;
};

export const useCombineCatalog = ({
  ownerKey,
  explorerId,
  projectId,
  authResourcePath,
  state,
  draftAccess,
  loomClient,
}: UseCombineCatalogOptions) => {
  const [combineCatalogLoad, setCombineCatalogLoad] = useState<ConstructionCombineCatalogLoad>({
    identity: '',
    catalog: { kind: 'loading' },
    loadingMore: false,
  });
  const [requests] = useState(() => new RequestOwner());
  const combineCatalogOwnerRef = useRef({ ownerKey, explorerId });
  combineCatalogOwnerRef.current = { ownerKey, explorerId };

  const combineCatalogIdentityFor = (value: BuilderAuthoringState): string => {
    const selected = selectedTable(value);
    return JSON.stringify([
      ownerKey,
      value.explorerId,
      selected?.outputId ?? '',
      value.catalog.snapshotToken,
      value.draftVersion,
      value.draftDigest,
    ]);
  };
  const combineCatalogIdentity = combineCatalogIdentityFor(state);
  const invalidateCombineCatalogRequest = () => {
    requests.cancel('combine-catalog');
  };
  const cancelCombineCatalogRequest = () => {
    requests.cancel('combine-catalog');
    setCombineCatalogLoad((current) => current.loadingMore ? { ...current, loadingMore: false } : current);
  };
  const loadCombineCatalogPage = async (cursor?: string) => {
    const requestState = draftAccess.getState();
    const requestTable = selectedTable(requestState);
    const identity = combineCatalogIdentityFor(requestState);
    if (!requestTable || !requestState.catalog.snapshotToken || requestState.draftVersion <= 0 || !requestState.draftDigest) {
      setCombineCatalogLoad({
        identity,
        catalog: { kind: 'failed', message: 'The current table draft is still loading.' },
        loadingMore: false,
      });
      return;
    }
    const previous = combineCatalogLoad.identity === identity ? combineCatalogLoad : undefined;
    if (!cursor && previous?.catalog.kind === 'ready') return;
    if (cursor && (
      previous?.catalog.kind !== 'ready' ||
      previous.catalog.nextCursor !== cursor ||
      previous.loadingMore
    )) return;

    requests.setIdentity(identity);
    const request = requests.start('combine-catalog', identity);
    if (!request) return;
    if (cursor && previous?.catalog.kind === 'ready') {
      setCombineCatalogLoad({ ...previous, loadingMore: true, loadMoreError: undefined });
    } else {
      setCombineCatalogLoad({ identity, catalog: { kind: 'loading' }, loadingMore: false });
    }

    try {
      const response = await loomClient.getConstructionInputs({
        project: projectId,
        explorerId: requestState.explorerId,
        ...(authResourcePath ? { authResourcePath } : {}),
        snapshotToken: requestState.catalog.snapshotToken,
        expectedDraftVersion: requestState.draftVersion,
        expectedDraftDigest: requestState.draftDigest,
        ...(cursor ? { cursor } : {}),
        limit: 100,
      }, request.signal);
      const latest = draftAccess.getState();
      if (
        !request.isCurrent() ||
        combineCatalogOwnerRef.current.ownerKey !== ownerKey ||
        combineCatalogOwnerRef.current.explorerId !== requestState.explorerId ||
        identity !== combineCatalogIdentityFor(latest)
      ) return;
      if (
        !sameCatalogSnapshot(response.snapshotToken, requestState.catalog.snapshotToken) ||
        !sameDraftVersion(response.draftVersion, requestState.draftVersion) ||
        !sameDraftDigest(response.draftDigest, requestState.draftDigest) ||
        response.datasetGeneration !== requestState.catalog.generation ||
        !sameCatalogSnapshot(response.snapshotToken, latest.catalog.snapshotToken) ||
        !sameDraftVersion(response.draftVersion, latest.draftVersion) ||
        !sameDraftDigest(response.draftDigest, latest.draftDigest) ||
        response.datasetGeneration !== latest.catalog.generation
      ) {
        setCombineCatalogLoad({
          identity,
          catalog: { kind: 'failed', message: 'The table changed while published versions were loading. Reopen Combine tables to refresh.' },
          loadingMore: false,
        });
        return;
      }
      const revisions: ConstructionCombinePublishedRevision[] = response.entries.map((entry) => ({
        kind: 'TABLE_REVISION',
        tableId: entry.tableId,
        revisionId: entry.revisionId,
        outputId: entry.outputId,
        tableTitle: entry.tableTitle,
        outputTitle: entry.outputTitle,
        rowMeaning: entry.rowMeaning,
        isCurrent: entry.isCurrent,
        columns: entry.columns,
      }));
      setCombineCatalogLoad((current) => {
        if (
          current.identity !== identity ||
          !request.isCurrent() ||
          combineCatalogOwnerRef.current.ownerKey !== ownerKey ||
          combineCatalogOwnerRef.current.explorerId !== requestState.explorerId
        ) return current;
        const existing = cursor && current.catalog.kind === 'ready' ? current.catalog.revisions : [];
        const byKey = new Map<string, ConstructionCombinePublishedRevision>();
        for (const revision of [...existing, ...revisions]) {
          byKey.set(JSON.stringify([revision.tableId, revision.revisionId, revision.outputId]), revision);
        }
        return {
          identity,
          catalog: { kind: 'ready', revisions: [...byKey.values()], ...(response.nextCursor ? { nextCursor: response.nextCursor } : {}) },
          loadingMore: false,
        };
      });
    } catch (error) {
      if (
        !request.isCurrent() ||
        combineCatalogOwnerRef.current.ownerKey !== ownerKey ||
        combineCatalogOwnerRef.current.explorerId !== requestState.explorerId
      ) return;
      const message = error instanceof Error && error.message.trim()
        ? error.message
        : 'Loom could not load published table versions.';
      setCombineCatalogLoad((current) => {
        if (
          current.identity !== identity ||
          combineCatalogOwnerRef.current.ownerKey !== ownerKey ||
          combineCatalogOwnerRef.current.explorerId !== requestState.explorerId
        ) return current;
        if (cursor && current.catalog.kind === 'ready') {
          return { ...current, loadingMore: false, loadMoreError: message };
        }
        return { identity, catalog: { kind: 'failed', message }, loadingMore: false };
      });
    }
  };
  const currentCombineCatalogLoad = combineCatalogLoad.identity === combineCatalogIdentity
    ? combineCatalogLoad
    : { identity: combineCatalogIdentity, catalog: { kind: 'loading' as const }, loadingMore: false };

  const isOwnerCurrent = (candidate: CombineCatalogOwner): boolean =>
    combineCatalogOwnerRef.current.ownerKey === candidate.ownerKey &&
    combineCatalogOwnerRef.current.explorerId === candidate.explorerId;

  return {
    identity: combineCatalogIdentity,
    currentLoad: currentCombineCatalogLoad,
    loadPage: loadCombineCatalogPage,
    cancel: cancelCombineCatalogRequest,
    invalidate: invalidateCombineCatalogRequest,
    isOwnerCurrent,
  };
};
