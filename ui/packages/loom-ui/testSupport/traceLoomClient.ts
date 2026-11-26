import { traceClientCall } from './uiTrace';

const clientProxies = new WeakMap<object, object>();

export function traceLoomClient<Client extends object>(client: Client): Client {
  if (!process.env.LOOM_UI_TRACE) return client;

  const existingClient = clientProxies.get(client);
  if (existingClient) return existingClient as Client;

  const wrappers = new WeakMap<Function, Map<string, Function>>();
  const tracedClient = new Proxy(client, {
    get(target, property, receiver) {
      const method = Reflect.get(target, property, receiver);
      if (typeof method !== 'function') return method;

      const name = String(property);
      const methodWrappers = wrappers.get(method) ?? new Map<string, Function>();
      const existing = methodWrappers.get(name);
      if (existing) return existing;

      const wrapped = new Proxy(method, {
        apply(targetMethod, thisArg, args: unknown[]) {
          const trace = traceClientCall(name, args);
          if (!trace) return Reflect.apply(targetMethod, thisArg, args);

          const signal = signalFrom(args);
          let abortListener: (() => void) | undefined;
          if (signal instanceof AbortSignal) {
            if (signal.aborted) trace.abort();
            else {
              abortListener = () => queueMicrotask(() => trace.abort());
              signal.addEventListener('abort', abortListener, { once: true });
            }
          }

          const settle = () => {
            trace.settle();
            if (signal && abortListener) signal.removeEventListener('abort', abortListener);
          };

          let result: unknown;
          try {
            result = Reflect.apply(targetMethod, thisArg, args);
          } catch (error) {
            settle();
            throw error;
          }

          if (result instanceof Promise) void result.then(settle, settle);
          else settle();
          return result;
        },
      });
      methodWrappers.set(name, wrapped);
      wrappers.set(method, methodWrappers);
      return wrapped;
    },
  });
  clientProxies.set(client, tracedClient);
  clientProxies.set(tracedClient, tracedClient);
  return tracedClient;
}

function signalFrom(args: readonly unknown[]): AbortSignal | undefined {
  for (const arg of args) {
    if (arg instanceof AbortSignal) return arg;
    if (typeof arg === 'object' && arg !== null && 'signal' in arg
      && arg.signal instanceof AbortSignal) return arg.signal;
  }
  return undefined;
}
