import { useCallback, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import type { useLoomClient } from '../../../react';
import type { ResourceRef, SelectionRevision } from '../../../contracts/selection';
import type { ConstructionRouteStep } from '../../../contracts/types';
import { sameConstructionRoute } from '../model/populationRoutes';
import { selectedTable, type BuilderAuthoringState, type DraftTable } from '../model/authoring';
import type { useCommandQueue } from './useCommandQueue';

type UsePopulationExclusionOptions = {
  readonly table: DraftTable | undefined;
  readonly draftAccess: { getState(): BuilderAuthoringState };
  readonly activePopulationSelection: SelectionRevision | undefined;
  readonly populationSelectionContextKey: string;
  readonly loomClient: ReturnType<typeof useLoomClient>;
  readonly workspaceAttached: MutableRefObject<boolean>;
  readonly serverDraft: MutableRefObject<{ version: number; digest: string }>;
  readonly applyCommands: ReturnType<typeof useCommandQueue>['applyCommands'];
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly setPopulationSelectionOverride: Dispatch<SetStateAction<{
    readonly contextKey: string;
    readonly baseSelectionID: string;
    readonly selection: SelectionRevision;
  } | undefined>>;
};

const populationBindingKey = (
  table: BuilderAuthoringState['tables'][number] | undefined,
  selectionRevisionId = table?.document.population?.selectionRevisionId,
) => JSON.stringify([
  selectionRevisionId ?? '',
  (table?.document.population?.route ?? []).map((step) => [
    step.resourceType, step.relationship, step.catalogEdgeId ?? '', step.storageDirection ?? '',
  ]),
]);

export const usePopulationExclusion = ({
  table, draftAccess, activePopulationSelection, populationSelectionContextKey, loomClient,
  workspaceAttached, serverDraft, applyCommands, projectId, authResourcePath,
  setPopulationSelectionOverride,
}: UsePopulationExclusionOptions) => {
  const [populationVariantError, setPopulationVariantError] = useState<{
    readonly contextKey: string;
    readonly message: string;
  }>();
  const [populationVariantPending, setPopulationVariantPending] = useState(false);
  const populationExclusionContext = {
    contextKey: populationSelectionContextKey,
    client: loomClient,
    baseSelectionID: activePopulationSelection?.id,
    bindingKey: populationBindingKey(table),
  };
  const populationExclusionContextRef = useRef(populationExclusionContext);
  populationExclusionContextRef.current = populationExclusionContext;
  const excludePopulationMember = useCallback(async (
    ref: ResourceRef,
    route: ReadonlyArray<ConstructionRouteStep>,
  ) => {
    const current = draftAccess.getState();
    const currentTable = selectedTable(current);
    const baseSelection = activePopulationSelection;
    if (!currentTable || !baseSelection) return;
    const exclusionContext = populationExclusionContextRef.current;
    const requestIsCurrent = () => {
      const latestContext = populationExclusionContextRef.current;
      const latest = draftAccess.getState();
      const latestTable = selectedTable(latest);
      return workspaceAttached.current &&
        latestContext.contextKey === exclusionContext.contextKey &&
        latestContext.client === exclusionContext.client &&
        latestContext.baseSelectionID === baseSelection.id &&
        latestContext.bindingKey === exclusionContext.bindingKey &&
        latestTable?.outputId === currentTable.outputId &&
        latestTable.document.rootResourceType === currentTable.document.rootResourceType &&
        populationBindingKey(latestTable) === exclusionContext.bindingKey &&
        latest.draftVersion === serverDraft.current.version &&
        latest.draftDigest === serverDraft.current.digest;
    };
    if (!requestIsCurrent()) return;
    setPopulationVariantPending(true);
    setPopulationVariantError(undefined);
    try {
      const idempotencyKey = `selection-variant-${window.crypto.randomUUID()}`;
      const variant = await loomClient.createSelection({
        project: projectId,
        explorerId: current.explorerId,
        authResourcePath,
        snapshotToken: current.catalog.snapshotToken,
        idempotencyKey,
        source: { kind: 'selectionRevision', selectionRevision: { selectionRevisionId: baseSelection.id } },
        exclusions: [ref],
        requestId: idempotencyKey,
      });
      if (!requestIsCurrent()) return;
      let cursor: string | undefined;
      let routeChoiceId: string | undefined;
      do {
        const routes = await loomClient.searchPopulationRoutes({
          project: projectId,
          explorerId: current.explorerId,
          authResourcePath,
          snapshotToken: current.catalog.snapshotToken,
          outputId: currentTable.outputId,
          selectionRevisionId: variant.id,
          limit: 50,
          ...(cursor ? { cursor } : {}),
          requestId: `population-routes-${window.crypto.randomUUID()}`,
        });
        if (!requestIsCurrent()) return;
        routeChoiceId = routes.choices.find((choice) =>
          sameConstructionRoute(choice.route, route),
        )?.routeChoiceId;
        cursor = routes.nextCursor;
      } while (!routeChoiceId && cursor);
      if (!routeChoiceId) {
        throw new Error('Loom could not preserve this table connection for the revised collection.');
      }
      if (!requestIsCurrent()) return;
      const attached = await applyCommands([{
        type: 'SET_TABLE_POPULATION',
        outputId: currentTable.outputId,
        selectionRevisionId: variant.id,
        routeChoiceId,
      }], undefined, requestIsCurrent);
      if (!attached) {
        if (!requestIsCurrent()) return;
        throw new Error('Loom created the revised collection but could not attach it to this table.');
      }
      const resultContext = populationExclusionContextRef.current;
      const resultTable = selectedTable(draftAccess.getState());
      if (!workspaceAttached.current ||
          resultContext.contextKey !== exclusionContext.contextKey ||
          resultContext.client !== exclusionContext.client ||
          (resultContext.baseSelectionID !== undefined && resultContext.baseSelectionID !== baseSelection.id && resultContext.baseSelectionID !== variant.id) ||
          resultTable?.outputId !== currentTable.outputId ||
          resultTable.document.rootResourceType !== currentTable.document.rootResourceType ||
          populationBindingKey(resultTable) !== populationBindingKey(currentTable, variant.id)) return;
      setPopulationSelectionOverride({
        contextKey: populationSelectionContextKey,
        baseSelectionID: baseSelection.id,
        selection: variant,
      });
    } catch (error) {
      if (!requestIsCurrent()) return;
      setPopulationVariantError({
        contextKey: populationSelectionContextKey,
        message: error instanceof Error ? error.message : 'Loom could not create the revised collection.',
      });
    } finally {
      setPopulationVariantPending(false);
    }
  }, [activePopulationSelection, applyCommands, authResourcePath, loomClient, populationSelectionContextKey, projectId]);

  return { excludePopulationMember, populationVariantError, populationVariantPending };
};
