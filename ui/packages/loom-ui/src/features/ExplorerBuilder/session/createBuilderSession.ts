import type { ExplorerBuilderState } from '../../../contracts/types';
import type { ExplorerAuthoringStateArgs, LoomClient } from '../../../client/api';
import { loomReadCache } from '../../../data/loomReadCache';
import { loomReadKey } from '../../../client/api';
import { createQueryResource } from '../../../data/queryResource';

export function createBuilderSession({
  client,
  scope,
}: {
  readonly client: Pick<LoomClient, 'getBuilder'>;
  readonly scope: ExplorerAuthoringStateArgs;
}): ReturnType<typeof createQueryResource<ExplorerBuilderState>> {
  return createQueryResource(
    (signal, options) => client.getBuilder(scope, { signal, reload: options?.reload }),
    Boolean(scope.explorerId),
    loomReadCache(client, loomReadKey('builder', scope)),
  );
}
