interface VirtualRange {
  readonly start: number;
  readonly end: number;
}

export const virtualRange = ({
  count,
  offset,
  viewport,
  itemSize,
  overscan = 2,
}: {
  readonly count: number;
  readonly offset: number;
  readonly viewport: number;
  readonly itemSize: number;
  readonly overscan?: number;
}): VirtualRange => {
  if (count <= 0 || itemSize <= 0) return { start: 0, end: 0 };
  const first = Math.max(0, Math.floor(Math.max(0, offset) / itemSize) - overscan);
  const visible = Math.max(1, Math.ceil(Math.max(0, viewport) / itemSize));
  return {
    start: Math.min(first, count),
    end: Math.min(count, first + visible + overscan * 2),
  };
};

export class BoundedCache<V> {
  private readonly entries = new Map<string, V>();

  public constructor(private readonly capacity: number) {}

  public clear(): void {
    this.entries.clear();
  }

  public getOrSet(key: string, create: () => V): V {
    const existing = this.entries.get(key);
    if (existing !== undefined) {
      this.entries.delete(key);
      this.entries.set(key, existing);
      return existing;
    }
    const value = create();
    this.entries.set(key, value);
    while (this.entries.size > this.capacity) {
      const oldest = this.entries.keys().next();
      if (oldest.done) break;
      this.entries.delete(oldest.value);
    }
    return value;
  }
}
