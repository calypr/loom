import type { LoomClient, ResolveConfiguredColumnContextsArgs } from '../client/api';
import type { SelectionRevision } from '../contracts/selection';
import { useLoomClient } from './provider';
import { useQuery, type QueryResult } from './queries';

export const useResolveConfiguredColumnContextsQuery = (
  args: ResolveConfiguredColumnContextsArgs | undefined,
  key: string,
): QueryResult<Awaited<ReturnType<LoomClient['resolveConfiguredColumnContexts']>>> => {
  const client = useLoomClient();
  return useQuery(
    (signal) => {
      if (!args) throw new Error('Configured column context query started without a saved draft identity.');
      const requestId = `configured-column-context-${window.crypto.randomUUID()}`;
      return client.resolveConfiguredColumnContexts({ ...args, requestId }, signal);
    },
    [
      client,
      key,
      args?.project,
      args?.explorerId,
      args?.authResourcePath,
      args?.snapshotToken,
      args?.expectedDraftVersion,
      args?.expectedDraftDigest,
    ],
    args !== undefined,
  );
};

interface ResolvePopulationSelectionQueryArgs {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly generation: string;
  readonly outputId: string;
  readonly resourceType: string;
  readonly attachedSelectionId?: string;
  readonly cohortRevisionId?: string;
}

export const useResolvePopulationSelectionQuery = (
  args: ResolvePopulationSelectionQueryArgs | undefined,
  key: string,
): QueryResult<SelectionRevision> => {
  const client = useLoomClient();
  return useQuery(
    async (signal) => {
      if (!args) throw new Error('Population selection query started without a table identity.');
      let selectionRevisionId = args.attachedSelectionId;
      if (!selectionRevisionId) {
        if (!args.cohortRevisionId) {
          throw new Error('Population selection query started without a saved selection.');
        }
        const choices = await client.listRowDefinitionChoices({
          project: args.project,
          explorerId: args.explorerId,
          authResourcePath: args.authResourcePath,
          snapshotToken: args.snapshotToken,
          outputId: args.outputId,
        }, signal);
        if (choices.outputId !== args.outputId || choices.snapshotToken !== args.snapshotToken) {
          throw new Error('Loom returned cohort choices for a different table or catalog snapshot.');
        }
        const cohortChoice = choices.explicitGroups.find((candidate) => candidate.revisionId === args.cohortRevisionId);
        if (!cohortChoice?.sourceSelectionRevisionId) {
          throw new Error('The saved cohort does not expose its source selection in this authorized catalog snapshot.');
        }
        selectionRevisionId = cohortChoice.sourceSelectionRevisionId;
      }
      const page = await client.getSelection({
        project: args.project,
        explorerId: args.explorerId,
        authResourcePath: args.authResourcePath,
        selectionRevision: selectionRevisionId,
        limit: 1,
      }, signal);
      if (page.revision.id !== selectionRevisionId || page.revision.project !== args.project ||
        page.revision.generation !== args.generation ||
        (!args.attachedSelectionId && page.revision.resourceType !== args.resourceType)) {
        throw new Error('The saved cohort source selection does not match this table and catalog snapshot.');
      }
      return page.revision;
    },
    [
      client,
      key,
      args?.project,
      args?.explorerId,
      args?.authResourcePath,
      args?.snapshotToken,
      args?.generation,
      args?.outputId,
      args?.resourceType,
      args?.attachedSelectionId,
      args?.cohortRevisionId,
    ],
    args !== undefined,
  );
};
