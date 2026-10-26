export class BoundedFormatCache {
  private readonly entries = new Map<string, string>();

  public constructor(private readonly capacity: number) {}

  public clear(): void {
    this.entries.clear();
  }

  public getOrSet(key: string, create: () => string): string {
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
