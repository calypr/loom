import type { LoomClient } from '../client/api';
import { useOptionalLoomClient } from './provider';
import { useMutation } from './useMutation';

type BuilderMutation =
  | 'applyCommands'
  | 'assessRowChange'
  | 'reconcile'
  | 'suggestions'
  | 'preview'
  | 'populationMapping'
  | 'publish'
  | 'createExplorer'
  | 'deleteExplorer';

type MutationArgs<Method extends BuilderMutation> = Parameters<LoomClient[Method]>[0];
type MutationResponse<Method extends BuilderMutation> = Awaited<ReturnType<LoomClient[Method]>>;

export type BuilderMutationResult<Method extends BuilderMutation> =
  ReturnType<typeof useMutation<MutationArgs<Method>, MutationResponse<Method>>>;

export function useBuilderMutation<Method extends BuilderMutation>(method: Method): BuilderMutationResult<Method> {
  const client = useOptionalLoomClient();
  if (!client && method !== 'populationMapping') {
    throw new Error('Loom UI must be rendered inside LoomProvider.');
  }
  return useMutation((args, signal) => {
    if (!client) return Promise.reject(new Error('Loom UI must be rendered inside LoomProvider.'));
    const run = client[method] as (args: MutationArgs<Method>, signal: AbortSignal) => Promise<MutationResponse<Method>>;
    return run.call(client, args, signal);
  });
}
