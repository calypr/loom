import { bindRequestTransport, RequestOwner } from '../../../data/requests';

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
  const identity = 'reconcile';
  owner.setIdentity(identity);
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
        const slot = owner.start(name, identity);
        if (!slot) return undefined;
        if (generation !== current) {
          slot.finish();
          return undefined;
        }
        return bindRequestTransport(slot, open);
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
      owner.cancelAll();
    },
  };
}
