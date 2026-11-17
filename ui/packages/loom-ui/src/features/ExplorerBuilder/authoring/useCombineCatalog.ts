import { useRef, useState, type MutableRefObject } from 'react';
import type { useLoomClient } from '../../../react';
import type { ConstructionCombineCatalog, ConstructionCombinePublishedRevision } from '../constructionOperations/combineEditorTypes';
import { selectedTable, type BuilderAuthoringState } from './model';

type ConstructionCombineCatalogLoad = {
  readonly identity: string;
  readonly catalog: ConstructionCombineCatalog;
  readonly loadingMore: boolean;
  readonly loadMoreError?: string;
};

export type CombineCatalogOwner = {
  readonly ownerKey: string;
  readonly explorerId: string;
};

type UseCombineCatalogOptions = {
  readonly ownerKey: string;
  readonly explorerId: string;
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly state: BuilderAuthoringState;
  readonly latestState: MutableRefObject<BuilderAuthoringState>;
  readonly loomClient: ReturnType<typeof useLoomClient>;
};

export const useCombineCatalog = ({
  ownerKey,
  explorerId,
  projectId,
  authResourcePath,
  state,
  latestState,
  loomClient,
}: UseCombineCatalogOptions) => {
  const [combineCatalogLoad, setCombineCatalogLoad] = useState<ConstructionCombineCatalogLoad>({
    identity: '',
    catalog: { kind: 'loading' },
    loadingMore: false,
  });
  const combineCatalogController = useRef<AbortController | undefined>(undefined);
  const combineCatalogRequestGeneration = useRef(0);
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
    combineCatalogRequestGeneration.current += 1;
    combineCatalogController.current?.abort();
    combineCatalogController.current = undefined;
  };
  const cancelCombineCatalogRequest = () => {
    combineCatalogRequestGeneration.current += 1;
    combineCatalogController.current?.abort();
    combineCatalogController.current = undefined;
    setCombineCatalogLoad((current) => current.loadingMore ? { ...current, loadingMore: false } : current);
  };
  const loadCombineCatalogPage = async (cursor?: string) => {
    const requestState = latestState.current;
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

    combineCatalogController.current?.abort();
    const requestGeneration = ++combineCatalogRequestGeneration.current;
    const controller = new AbortController();
    combineCatalogController.current = controller;
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
      }, controller.signal);
      const latest = latestState.current;
      if (
        controller.signal.aborted ||
        requestGeneration !== combineCatalogRequestGeneration.current ||
        combineCatalogOwnerRef.current.ownerKey !== ownerKey ||
        combineCatalogOwnerRef.current.explorerId !== requestState.explorerId ||
        identity !== combineCatalogIdentityFor(latest)
      ) return;
      if (
        response.snapshotToken !== requestState.catalog.snapshotToken ||
        response.draftVersion !== requestState.draftVersion ||
        response.draftDigest !== requestState.draftDigest ||
        response.datasetGeneration !== requestState.catalog.generation ||
        response.snapshotToken !== latest.catalog.snapshotToken ||
        response.draftVersion !== latest.draftVersion ||
        response.draftDigest !== latest.draftDigest ||
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
          requestGeneration !== combineCatalogRequestGeneration.current ||
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
        controller.signal.aborted ||
        requestGeneration !== combineCatalogRequestGeneration.current ||
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
