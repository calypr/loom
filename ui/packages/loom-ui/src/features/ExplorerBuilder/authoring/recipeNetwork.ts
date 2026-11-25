import type { MutableRefObject } from 'react';
import type { LoomClient } from '../../../client/api';
import type { AuthoringNetworkResponse } from '../../../contracts/types';
import type { BuilderAuthoringState, DraftTable } from './model';
import type { RecipeFocusActions } from './useRecipeFocus';

export type RecipeNetworkIdentity = Pick<AuthoringNetworkResponse, 'draftVersion' | 'draftDigest' | 'snapshotToken'> & {
  readonly network: Pick<AuthoringNetworkResponse['network'], 'outputId'>;
};

type RecipeNetworkOptions<Response extends RecipeNetworkIdentity> = {
  readonly firstTableProgress: { readonly kind: 'idle' | 'running' };
  readonly table: Pick<DraftTable, 'outputId'> | undefined;
  readonly networkRequestOwner: MutableRefObject<string>;
  readonly relationshipOwner: string;
  readonly loomClient: { readonly describeAuthoringNetwork: (request: Parameters<LoomClient['describeAuthoringNetwork']>[0]) => Promise<Response> };
  readonly projectId: string;
  readonly state: Pick<BuilderAuthoringState, 'explorerId' | 'draftVersion' | 'draftDigest'> & {
    readonly catalog: Pick<BuilderAuthoringState['catalog'], 'snapshotToken'>;
  };
  readonly authResourcePath?: string;
  readonly setSavedNetwork: (value: { owner: string; result: Response }) => void;
  readonly completePendingRecipeFocus: RecipeFocusActions['completePendingRecipeFocus'];
};

export const createRecipeNetworkLoader = <Response extends RecipeNetworkIdentity>({
  firstTableProgress,
  table,
  networkRequestOwner,
  relationshipOwner,
  loomClient,
  projectId,
  state,
  authResourcePath,
  setSavedNetwork,
  completePendingRecipeFocus,
}: RecipeNetworkOptions<Response>): (() => void) => {
  const loadRecipeNetwork = () => {
    if (firstTableProgress.kind !== 'idle' || !table) return;

    completePendingRecipeFocus(table.outputId);
    if (networkRequestOwner.current === relationshipOwner) return;
    networkRequestOwner.current = relationshipOwner;
    void loomClient.describeAuthoringNetwork({ project: projectId, explorerId: state.explorerId, authResourcePath,
      snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion,
      expectedDraftDigest: state.draftDigest, outputId: table.outputId }).then((result) => {
      if (networkRequestOwner.current !== relationshipOwner) return;
      if (result.draftVersion !== state.draftVersion || result.draftDigest !== state.draftDigest ||
        result.snapshotToken !== state.catalog.snapshotToken || result.network.outputId !== table.outputId) return;
      setSavedNetwork({ owner: relationshipOwner, result });
      completePendingRecipeFocus(result.network.outputId);
    }).catch(() => { if (networkRequestOwner.current === relationshipOwner) networkRequestOwner.current = ''; });
  };
  return loadRecipeNetwork;
};
