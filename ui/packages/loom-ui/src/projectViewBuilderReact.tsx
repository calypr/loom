import { useCallback, useState } from 'react';
import { useLoomClient, useQuery } from './react';
import type {
  DeleteProjectViewConfigArgs,
  ProjectViewBuilderArgs,
  SaveProjectViewConfigArgs,
  SaveProjectViewDefaultsArgs,
} from './client/api';
import type { ProjectViewBuilderState } from './contracts/projectViewBuilder';

export const useGetProjectViewBuilderQuery = (args: ProjectViewBuilderArgs) => {
  const client = useLoomClient();
  return useQuery(
    (signal, options) => client.getProjectViewBuilder(args, { signal, reload: options?.reload }),
    [client, args.project, args.authResourcePath],
  );
};

interface MutationState { readonly isLoading: boolean }
interface MutationTrigger<TArgs, TResult> {
  (args: TArgs): { readonly unwrap: () => Promise<TResult> };
}

const useMutation = <TArgs,>(
  operation: (args: TArgs, signal: AbortSignal) => Promise<ProjectViewBuilderState>,
): [MutationTrigger<TArgs, ProjectViewBuilderState>, MutationState] => {
  const [pending, setPending] = useState(0);
  const trigger = useCallback<MutationTrigger<TArgs, ProjectViewBuilderState>>((args) => {
    const controller = new AbortController();
    const promise = operation(args, controller.signal);
    setPending((value) => value + 1);
    void promise.then(
      () => setPending((value) => value - 1),
      () => setPending((value) => value - 1),
    );
    return { unwrap: () => promise };
  }, [operation]);
  return [trigger, { isLoading: pending > 0 }];
};

export const useSaveProjectViewConfigMutation = () => {
  const client = useLoomClient();
  return useMutation((args: SaveProjectViewConfigArgs, signal) => client.saveProjectViewConfig(args, signal));
};
export const useDeleteProjectViewConfigMutation = () => {
  const client = useLoomClient();
  return useMutation((args: DeleteProjectViewConfigArgs, signal) => client.deleteProjectViewConfig(args, signal));
};
export const useSaveProjectViewDefaultsMutation = () => {
  const client = useLoomClient();
  return useMutation((args: SaveProjectViewDefaultsArgs, signal) => client.saveProjectViewDefaults(args, signal));
};
export const useAdoptProjectViewBuilderCatalogsMutation = () => {
  const client = useLoomClient();
  return useMutation((args: ProjectViewBuilderArgs, signal) => client.adoptProjectViewBuilderCatalogs(args, signal));
};
