import { useQuery } from '../../../react';
import { sameChoiceRequestScope } from '../../../contracts/identity';
import type { ConstructionCapabilitiesResponse } from '../../../contracts/types';
import type { CodedPivotContext } from './codedPivotContext';

type PopulationStatus = 'POPULATED' | 'EMPTY' | 'UNKNOWN';

export function usePivotPopulation({
  context, capabilities, populationIdentity, stageId, populationColumns, onStatuses,
}: {
  readonly context?: CodedPivotContext;
  readonly capabilities: ConstructionCapabilitiesResponse;
  readonly populationIdentity?: string;
  readonly stageId: string;
  readonly populationColumns: string[];
  readonly onStatuses: (statuses: Readonly<Record<string, PopulationStatus>>) => void;
}) {
  return useQuery(async (signal) => {
    if (!context || !populationIdentity) return { identity: '', statuses: {} as Record<string, 'POPULATED' | 'EMPTY' | 'UNKNOWN'> };
    const batches: string[][] = [];
    for (let index = 0; index < populationColumns.length; index += 16) batches.push(populationColumns.slice(index, index + 16));
    const statuses: Record<string, 'POPULATED' | 'EMPTY' | 'UNKNOWN'> = {};
    await Promise.all(batches.map(async (columnIds) => {
      try {
        const response = await context.client.getConstructionColumnPopulation({
          project: context.project,
          explorerId: context.explorerId,
          authResourcePath: context.authResourcePath,
          snapshotToken: capabilities.snapshotToken,
          expectedDraftVersion: capabilities.draftVersion,
          expectedDraftDigest: capabilities.draftDigest,
          outputId: capabilities.outputId,
          stageId: stageId,
          columnIds,
        }, signal);
        if (signal.aborted || !sameChoiceRequestScope(response, {
          snapshotToken: capabilities.snapshotToken, draftVersion: capabilities.draftVersion,
          draftDigest: capabilities.draftDigest, outputId: capabilities.outputId, stageId,
        })) return;
        for (const column of response.columns) statuses[column.columnId] = column.status;
      } catch {
        if (!signal.aborted) for (const columnId of columnIds) statuses[columnId] = 'UNKNOWN';
      }
    }));
    onStatuses(statuses);
    return { identity: populationIdentity, statuses };
  }, [context?.client, populationIdentity], Boolean(populationIdentity));
}
