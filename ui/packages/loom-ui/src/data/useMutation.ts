import { useCallback, useRef, useState } from 'react';

type Trigger<TArgs, TResult> = (args: TArgs) => {
  readonly unwrap: () => Promise<TResult>;
  readonly abort: () => void;
};

export const useMutation = <TArgs, TResult>(
  operation: (args: TArgs, signal: AbortSignal) => Promise<TResult>,
): [Trigger<TArgs, TResult>, { readonly isLoading: boolean }] => {
  const [pendingRequests, setPendingRequests] = useState(0);
  const operationRef = useRef(operation);
  operationRef.current = operation;
  const trigger = useCallback<Trigger<TArgs, TResult>>((args) => {
    const controller = new AbortController();
    const promise = operationRef.current(args, controller.signal);
    setPendingRequests((count) => count + 1);
    const settle = () => setPendingRequests((count) => count - 1);
    void promise.then(settle, settle);
    return { unwrap: () => promise, abort: () => controller.abort() };
  }, []);
  return [trigger, { isLoading: pendingRequests > 0 }];
};
