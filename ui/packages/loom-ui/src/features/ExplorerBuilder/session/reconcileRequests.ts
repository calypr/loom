import { RequestOwner } from '../../../data/requests';

type TransportRequest<Result> = {
  readonly unwrap: () => Promise<Result>;
  readonly abort: () => void;
};

export type ReconcileRequestSlot = {
  readonly advance: () => number;
  readonly generation: () => number;
  readonly invalidate: () => void;
  track<Result>(generation: number, open: () => TransportRequest<Result>): (TransportRequest<Result> & {
    readonly finish: () => void;
  }) | undefined;
};

export function createReconcileRequests() {
  const owner = new RequestOwner();
  let revision = 0;
  owner.setIdentity(String(revision));
  const createSlot = (name: 'compile' | 'preview'): ReconcileRequestSlot => {
    let current = 0;
    return {
      generation: () => current,
      advance: () => ++current,
      invalidate() {
        current += 1;
        owner.cancel(name);
      },
      track(generation, open) {
        if (generation !== current) return undefined;
        const slot = owner.start(name, String(revision));
        if (!slot) return undefined;
        if (generation !== current) {
          slot.finish();
          return undefined;
        }
        let transport: ReturnType<typeof open>;
        try {
          transport = open();
        } catch (error) {
          slot.finish();
          throw error;
        }
        const abort = () => transport.abort();
        if (slot.signal.aborted) abort();
        else slot.signal.addEventListener('abort', abort, { once: true });
        return {
          unwrap: () => transport.unwrap(),
          abort: slot.cancel,
          finish() {
            slot.signal.removeEventListener('abort', abort);
            slot.finish();
          },
        };
      },
    };
  };
  const compileRequests = createSlot('compile');
  const previewRequests = createSlot('preview');
  return {
    compileRequests,
    previewRequests,
    invalidateCompileAndPreview() {
      compileRequests.advance();
      previewRequests.advance();
      owner.setIdentity(String(++revision));
    },
  };
}
