import type { ExplorerAuthoringStateArgs } from '../../../client/api';
import type { BuilderDraftClient } from '../../../data/builderDraftClient';
import { resourceFor } from '../../../data/resource';

export function createBuilderSession({
  client,
  scope,
}: {
  readonly client: BuilderDraftClient;
  readonly scope: ExplorerAuthoringStateArgs;
}) {
  return resourceFor(
    (signal, options) => client.getBuilder(scope, { signal, reload: options?.reload }),
    Boolean(scope.explorerId),
  );
}
