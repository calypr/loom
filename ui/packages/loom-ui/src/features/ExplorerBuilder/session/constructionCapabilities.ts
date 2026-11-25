import type { GetConstructionCapabilitiesArgs } from '../../../client/api';
import { sameChoiceRequestScope } from '../../../contracts/identity';
import type { ConstructionCapabilitiesResponse } from '../../../contracts/types';
import { resourceFor } from '../../../data/resource';

export type ConstructionCapabilitiesViewState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly response: ConstructionCapabilitiesResponse }
  | { readonly status: 'error'; readonly message: string };

type CapabilitiesClient = {
  getConstructionCapabilities(args: GetConstructionCapabilitiesArgs, signal?: AbortSignal): Promise<ConstructionCapabilitiesResponse>;
};

export const constructionCapabilitiesResource = (
  client: CapabilitiesClient,
  getRequest: () => GetConstructionCapabilitiesArgs | undefined,
  enabled: boolean,
): ReturnType<typeof resourceFor<ConstructionCapabilitiesViewState>> => resourceFor(async (signal) => {
  const args = getRequest();
  if (!args) return { status: 'idle' };
  try {
    const response = await client.getConstructionCapabilities(args, signal);
    return sameChoiceRequestScope(response, {
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
    })
      ? { status: 'ready', response }
      : { status: 'error', message: 'Loom returned capabilities for a different draft or table.' };
  } catch (error) {
    return {
      status: 'error',
      message: error instanceof Error && error.message.trim() ? error.message : 'Loom could not load construction capabilities.',
    };
  }
}, enabled);
