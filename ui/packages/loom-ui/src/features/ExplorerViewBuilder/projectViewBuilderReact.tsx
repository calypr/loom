import { useLoomClient, useQuery } from '../../react';
import type { ProjectViewBuilderArgs } from '../../client/api';

export const useGetProjectViewBuilderQuery = (args: ProjectViewBuilderArgs) => {
  const client = useLoomClient();
  return useQuery(
    (signal, options) => client.getProjectViewBuilder(args, { signal, reload: options?.reload }),
    [client, args.project, args.authResourcePath],
  );
};
