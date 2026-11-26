type ReadKind = 'explorers' | 'builder' | 'viewer' | 'view-builder' | 'interpretations' | 'capability';

export type LoomReadKey = readonly [ReadKind, ...string[]];
type InvalidateRead = (key: LoomReadKey, refreshActive: boolean) => void;

const publishers = new WeakMap<object, { listener?: InvalidateRead }>();

export function registerLoomReadPublisher(client: object): void {
  publishers.set(client, {});
}

export function isLoomReadPublisher(client: object): boolean {
  return publishers.has(client);
}

export function observeLoomReadInvalidation(client: object, listener: InvalidateRead): void {
  const publisher = publishers.get(client);
  if (publisher) publisher.listener = listener;
}

export function invalidateLoomRead(client: object, key: LoomReadKey, refreshActive = false): void {
  publishers.get(client)?.listener?.(key, refreshActive);
}
