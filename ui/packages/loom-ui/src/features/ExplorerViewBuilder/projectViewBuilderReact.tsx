import { useLoomClient, useQuery } from '../../react';
import { useLoomReadCache } from '../../data/loomReadCache';
import type { ProjectViewBuilderArgs } from '../../client/api';

export const useGetProjectViewBuilderQuery = (args: ProjectViewBuilderArgs) => {
  const client = useLoomClient();
  const cache = useLoomReadCache(client, 'view-builder', args);
  return useQuery(
    (signal, options) => client.getProjectViewBuilder(args, { signal, reload: options?.reload }),
    [client, args.project, args.authResourcePath], true, cache,
  );
};
