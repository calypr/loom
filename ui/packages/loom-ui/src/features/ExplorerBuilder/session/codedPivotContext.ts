import { useBuilderSession } from './BuilderSessionProvider';
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

export function useCodedPivotContext(scope: Omit<CodedPivotContext, 'client'>): CodedPivotContext {
  return { ...scope, client: useBuilderSession(session => session.transport) };
}
