import { useMemo, useRef, useState } from 'react';
import { useBuilderMutation, useLoomClient, useQuery } from '../../../react';
import type { PopulationMappingResponse } from '../../../client/api';
import type { SelectionRevision } from '../../../contracts/selection';
import type { DraftTable } from '../model/authoring';
import { matchesSavedPopulationRoute, populationRouteOptions } from '../model/populationRoutes';

type RouteLoadState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly truncated: boolean }
  | { readonly status: 'error'; readonly message: string };

export function usePopulationWorkflow({
  table, selection, project, explorerId, authResourcePath, snapshotToken, receiptId,
}: {
  readonly table: DraftTable;
  readonly selection?: SelectionRevision;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly receiptId?: string;
}) {
  const client = useLoomClient();
  const [routeSelection, setRouteSelection] = useState<{
    readonly key: string;
    readonly client: typeof client;
    readonly pathIndex: number;
    readonly otherConnectionsOpen: boolean;
  }>();
  const [checkPopulation] = useBuilderMutation('populationMapping');
  const [coverageState, setCoverageState] = useState<{
    readonly scopeKey: string;
    readonly client: typeof client;
    readonly loading: boolean;
    readonly report?: PopulationMappingResponse;
    readonly error?: string;
  }>();
  const activeCoverage = useRef<object | undefined>(undefined);
  const attached = table.document.population;
  const selectionRevisionId = selection?.id;
  const routeScopeKey = JSON.stringify([
    project, explorerId, authResourcePath ?? '', snapshotToken, table.outputId,
    selectionRevisionId ?? '',
  ]);
  const routeQuery = useQuery(async (signal) => {
    if (!selectionRevisionId || signal.aborted) return undefined;
    const response = await client.searchPopulationRoutes({
      project,
      explorerId,
      authResourcePath,
      snapshotToken,
      outputId: table.outputId,
      selectionRevisionId,
      limit: 50,
      requestId: `population-routes-${window.crypto.randomUUID()}`,
    });
    if (signal.aborted) return undefined;
    if (
      response.snapshotToken !== snapshotToken ||
      response.outputId !== table.outputId ||
      response.selectionRevisionId !== selectionRevisionId
    ) {
      throw new Error('Loom returned population routes for another table or collection.');
    }
    return response;
  }, [client, authResourcePath, explorerId, project, selectionRevisionId, snapshotToken, table.outputId], Boolean(selectionRevisionId));
  const routeChoices = routeQuery.data?.choices ?? [];
  const routeLoadState: RouteLoadState = !selectionRevisionId
    ? { status: 'idle' }
    : routeQuery.isLoading
      ? { status: 'loading' }
      : routeQuery.error
        ? { status: 'error', message: routeQuery.error instanceof Error ? routeQuery.error.message : 'Loom could not find a supported population route.' }
        : routeQuery.data
          ? { status: 'ready', truncated: routeQuery.data.truncated }
          : { status: 'idle' };
  const currentRouteSelection = routeSelection?.key === routeScopeKey
    && routeSelection.client === client
    ? routeSelection
    : { key: routeScopeKey, client, pathIndex: 0, otherConnectionsOpen: false };
  const coverageScopeKey = JSON.stringify([
    project, explorerId, authResourcePath ?? '', receiptId ?? '', table.outputId,
    attached?.selectionRevisionId ?? '', selection?.id ?? '', selection?.project ?? '',
    selection?.generation ?? '', selection?.scopeDigest ?? '', selection?.membershipDigest ?? '',
    selection?.resourceType ?? '',
  ]);
  const currentCoverage = coverageState?.scopeKey === coverageScopeKey && coverageState.client === client
    ? coverageState
    : undefined;
  const coverage = currentCoverage?.report;
  const coverageError = currentCoverage?.error;
  const pathIndex = Math.min(currentRouteSelection.pathIndex, Math.max(0, routeChoices.length - 1));
  const otherConnectionsOpen = currentRouteSelection.otherConnectionsOpen;
  const attachedChoices = useMemo(
    () => attached
      ? routeChoices.filter((choice) => matchesSavedPopulationRoute(choice.route, attached.route))
      : [],
    [attached, routeChoices],
  );
  const routeOptions = useMemo(
    () => populationRouteOptions({
      choices: routeChoices,
      selectionResourceType: selection?.resourceType,
      rootResourceType: table.document.rootResourceType,
    }),
    [routeChoices, selection?.resourceType, table.document.rootResourceType],
  );
  const attachedChoice = attachedChoices.length === 1 ? attachedChoices[0] : undefined;
  const selectedChoice = routeOptions[pathIndex]?.choice;
  const directRouteFirst = routeOptions[0]?.isDirectSameResource === true;
  const directSameResourceRouteSelected = selection?.resourceType === table.document.rootResourceType && (
    attached && attached.selectionRevisionId === selection.id
      ? attached.route.length === 0
      : routeOptions[pathIndex]?.isDirectSameResource === true
  );
  const checkCoverage = () => {
    if (!receiptId || !project || !explorerId || !attached || !selection) return;
    const request = {};
    activeCoverage.current = request;
    setCoverageState({ scopeKey: coverageScopeKey, client, loading: true });
    const expectedBinding = {
      receiptId,
      outputId: table.outputId,
      project: selection.project,
      explorerId,
      generation: selection.generation,
      scopeDigest: selection.scopeDigest,
      selectionRevisionId: selection.id,
      membershipDigest: selection.membershipDigest,
      resourceType: selection.resourceType,
    };
    void checkPopulation({
      project,
      explorerId,
      authResourcePath,
      receiptId,
      outputId: table.outputId,
      limit: 100,
    }).unwrap().then((report) => {
      if (activeCoverage.current !== request) return;
      const bindingMatches = Object.entries(expectedBinding).every(([key, value]) => report.binding[key as keyof typeof report.binding] === value);
      if (!bindingMatches) {
        setCoverageState({ scopeKey: coverageScopeKey, client, loading: false, error: 'Coverage check returned a stale report.' });
        return;
      }
      setCoverageState({ scopeKey: coverageScopeKey, client, loading: false, report });
    }).catch((error: unknown) => {
      if (activeCoverage.current !== request) return;
      setCoverageState({ scopeKey: coverageScopeKey, client, loading: false,
        error: error instanceof Error ? error.message : 'Coverage check failed.' });
    });
  };
  return {
    memberTransport: client, attached, selectionRevisionId, routeLoadState, currentRouteSelection,
    setRouteSelection, currentCoverage, coverage, coverageError, pathIndex, otherConnectionsOpen,
    routeOptions, attachedChoice, selectedChoice, directRouteFirst, directSameResourceRouteSelected, checkCoverage,
  };
}
