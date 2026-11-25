type RequestLoader<Result> = (signal: AbortSignal) => Promise<Result>;

type RequestSlot = {
  readonly identity: string;
  readonly controller: AbortController;
};

export class RequestOwner {
  private readonly slots = new Map<string, RequestSlot>();
  private currentIdentity: string | undefined;
  private disposed = false;

  setIdentity(identity: string): void {
    if (this.disposed || identity === this.currentIdentity) return;
    this.currentIdentity = identity;
    const stale = [...this.slots].filter(([, slot]) => slot.identity !== identity);
    for (const [name] of stale) this.slots.delete(name);
    for (const [, slot] of stale) slot.controller.abort();
  }

  start(name: string, identity: string) {
    if (this.disposed || identity !== this.currentIdentity) return undefined;

    const previous = this.slots.get(name);
    const slot: RequestSlot = { identity, controller: new AbortController() };
    this.slots.set(name, slot);
    previous?.controller.abort();
    if (this.slots.get(name) !== slot) return undefined;

    return {
      signal: slot.controller.signal,
      isCurrent: () => this.slots.get(name) === slot,
      cancel: () => {
        if (this.slots.get(name) === slot) this.cancel(name);
      },
      finish: () => { if (this.slots.get(name) === slot) this.slots.delete(name); },
    };
  }

  cancel(name: string): void {
    const slot = this.slots.get(name);
    this.slots.delete(name);
    slot?.controller.abort();
  }

  async run<Result>(
    name: string,
    identity: string,
    load: RequestLoader<Result>,
  ): Promise<Result | undefined> {
    const request = this.start(name, identity);
    if (!request) return undefined;

    let removeAbortListener: () => void = () => {};
    const aborted = new Promise<{ readonly kind: 'aborted' }>(resolve => {
      const onAbort = () => resolve({ kind: 'aborted' });
      request.signal.addEventListener('abort', onAbort, { once: true });
      removeAbortListener = () => request.signal.removeEventListener('abort', onAbort);
    });

    try {
      const outcome = await Promise.race([
        load(request.signal).then(value => ({ kind: 'result' as const, value })),
        aborted,
      ]);
      if (outcome.kind === 'aborted' || !request.isCurrent()) return undefined;
      return outcome.value;
    } catch (error) {
      if (request.signal.aborted) return undefined;
      throw error;
    } finally {
      removeAbortListener();
      request.finish();
    }
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    const active = [...this.slots.values()];
    this.slots.clear();
    for (const slot of active) slot.controller.abort();
  }
}
