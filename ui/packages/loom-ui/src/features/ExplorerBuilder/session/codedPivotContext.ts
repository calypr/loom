import type { LoomClient } from '../../../client/api';

export type CodedPivotContext = {
  readonly client: Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory' | 'getConstructionColumnPopulation'>;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
};
