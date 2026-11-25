import type { ExplorerBuilderState } from '../../../contracts/types';
import type { ExplorerAuthoringStateArgs } from '../../../client/api';
import type { BuilderDraftClient } from '../../../data/builderDraftClient';
import { createQueryResource } from '../../../data/queryResource';

export function createBuilderSession({
  client,
  scope,
}: {
  readonly client: BuilderDraftClient;
  readonly scope: ExplorerAuthoringStateArgs;
}): ReturnType<typeof createQueryResource<ExplorerBuilderState>> {
  return createQueryResource(
    (signal, options) => client.getBuilder(scope, { signal, reload: options?.reload }),
    Boolean(scope.explorerId),
  );
}
