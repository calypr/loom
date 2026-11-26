import { QueryClient, useMutation as useTanStackMutation } from '@tanstack/react-query';
import { useCallback, useRef, useState, useSyncExternalStore } from 'react';

type Trigger<TArgs, TResult> = (args: TArgs) => {
  readonly unwrap: () => Promise<TResult>;
  readonly abort: () => void;
};

const mutationClient = new QueryClient({
  defaultOptions: { mutations: { networkMode: 'always', retry: false } },
});

const subscribe = (notify: () => void) => mutationClient.getMutationCache().subscribe(notify);

export const useMutation = <TArgs, TResult>(
  operation: (args: TArgs, signal: AbortSignal) => Promise<TResult>,
): [Trigger<TArgs, TResult>, { readonly isLoading: boolean }] => {
  const [owner] = useState<object>(() => ({}));
  const operationRef = useRef(operation);
  operationRef.current = operation;
  const { mutateAsync } = useTanStackMutation({
    meta: { loomOwner: owner },
    mutationFn: ({ promise }: { readonly promise: Promise<TResult> }) => promise,
  }, mutationClient);
  const getPendingRequests = () => mutationClient.isMutating({
    predicate: (mutation) => mutation.meta?.loomOwner === owner,
  });
  const pendingRequests = useSyncExternalStore(subscribe, getPendingRequests, getPendingRequests);
  const trigger = useCallback<Trigger<TArgs, TResult>>((args) => {
    const controller = new AbortController();
    const promise = operationRef.current(args, controller.signal);
    void promise.catch(() => undefined);
    const completion = mutateAsync({ promise });
    void completion.catch(() => undefined);
    return { unwrap: () => completion, abort: () => controller.abort() };
  }, [mutateAsync]);

  return [trigger, { isLoading: pendingRequests > 0 }];
};
